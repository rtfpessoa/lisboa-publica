package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) readReporting(ctx context.Context, operator string, ids []string) (map[string]reportingRecord, error) {
	out := map[string]reportingRecord{}
	if len(ids) == 0 || s.DB == nil {
		return out, nil
	}
	rows, err := s.DB.Query(ctx, "SELECT source_id,payload FROM vehicle_reporting WHERE operator_id=$1 AND source_id=ANY($2)", operator, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReportingRecords(rows)
}
func scanReportingRecords(rows pgx.Rows) (map[string]reportingRecord, error) {
	out := map[string]reportingRecord{}
	var err error
	for rows.Next() {
		var id string
		var payload []byte
		if err = rows.Scan(&id, &payload); err != nil {
			break
		}
		var record reportingRecord
		if err = json.Unmarshal(payload, &record); err != nil {
			err = fmt.Errorf("decode reporting state: %w", err)
			break
		}
		out[id] = record
	}
	if err == nil {
		err = rows.Err()
	}
	return out, err
}

func (r *reportingRegistry) pending(operator string) ([]reportingWrite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	writes := []reportingWrite{}
	prefix := operator + "\x00"
	for key, e := range r.Entries {
		if !e.Loaded || !e.Dirty || !strings.HasPrefix(key, prefix) {
			continue
		}
		record := e.Record
		record.Value.Persisted = true
		payload, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		writes = append(writes, reportingWrite{strings.TrimPrefix(key, prefix), record, payload, e.Version})
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].SourceID < writes[j].SourceID })
	return writes, nil
}
func (r *reportingRegistry) acknowledge(operator string, writes []reportingWrite) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range writes {
		if e := r.Entries[factKey(operator, w.SourceID)]; e != nil && e.Version == w.Version {
			e.Dirty = false
			e.Immediate = false
			e.Record.Value.Persisted = true
		}
	}
}
func insertReporting(ctx context.Context, tx pgx.Tx, operator string, writes []reportingWrite) error {
	if len(writes) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, w := range writes {
		batch.Queue(`INSERT INTO vehicle_reporting(operator_id,source_id,updated_at,payload) VALUES($1,$2,$3,$4)
 ON CONFLICT(operator_id,source_id) DO UPDATE SET updated_at=excluded.updated_at,payload=excluded.payload
 WHERE vehicle_reporting.updated_at <= excluded.updated_at`, operator, w.SourceID, w.Record.UpdatedAt, w.Payload)
	}
	results := tx.SendBatch(ctx, batch)
	defer results.Close()
	for range writes {
		if err := reportingBatchResult(results); err != nil {
			return err
		}
	}
	return results.Close()
}
func reportingBatchResult(results pgx.BatchResults) error {
	tag, err := results.Exec()
	if err == nil && tag.RowsAffected() != 1 {
		err = fmt.Errorf("reporting state write superseded")
	}
	return err
}
