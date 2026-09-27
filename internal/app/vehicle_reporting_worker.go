package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

const reportingSweepBatch = 128
const reportingWorkerInterval = time.Second
const reportingSweepInterval = 30 * time.Second

type storedReporting struct {
	Operator, Source string
	Record           reportingRecord
}

// RunReporting materializes clock-only transitions independently of ingestion and client reads.
// Each sweep reads at most128 latest rows through the primary key, never the entire inventory.
func (s *Store) RunReporting(ctx context.Context, cache *Cache, log *zap.Logger) {
	ticker := time.NewTicker(reportingWorkerInterval)
	defer ticker.Stop()
	for {
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := s.advanceReporting(writeCtx, cache, time.Now().UTC()); err != nil {
			log.Warn("reporting state persistence failed", zap.Error(err))
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Store) advanceReporting(ctx context.Context, cache *Cache, now time.Time) error {
	s.PublishMu.Lock()
	defer s.PublishMu.Unlock()
	state, _ := cache.state("")
	result := s.sweepReporting(ctx, state, now)
	for _, p := range providers {
		live, op := state.Live[p.ID], state.Operators[p.ID]
		_, stageErr := s.reporting.stage(reportingLookup{ctx, s.readReporting}, p.ID, live, op, now)
		result = errors.Join(result, stageErr)
		if !s.reporting.immediate(p.ID) {
			continue
		}
		projection := s.reporting.projection(p.ID, live)
		err := s.Save(ctx, p.ID, nil, withoutReportingSamples(projection), op, nil)
		cache.update(p.ID, nil, s.reporting.projection(p.ID, live), op)
		result = errors.Join(result, err)
	}
	return result
}
func withoutReportingSamples(live *LiveData) *LiveData {
	if live == nil {
		return nil
	}
	copyLive := *live
	copyLive.Samples = []api.Vehicle{}
	return &copyLive
}
func (s *Store) readReportingBatch(ctx context.Context) ([]storedReporting, error) {
	if s.DB == nil {
		return nil, nil
	}
	cursor := strings.SplitN(s.reporting.Cursor, "\x00", 2)
	operator, source := "", ""
	if len(cursor) == 2 {
		operator, source = cursor[0], cursor[1]
	}
	rows, err := s.DB.Query(ctx, `SELECT operator_id,source_id,payload FROM vehicle_reporting
 WHERE (operator_id,source_id)>($1,$2) ORDER BY operator_id,source_id LIMIT $3`, operator, source, reportingSweepBatch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanReportingBatch(rows)
}
func scanReportingBatch(rows pgx.Rows) ([]storedReporting, error) {
	out := []storedReporting{}
	var err error
	for rows.Next() {
		var row storedReporting
		var payload []byte
		if err = rows.Scan(&row.Operator, &row.Source, &payload); err != nil {
			break
		}
		if err = json.Unmarshal(payload, &row.Record); err != nil {
			break
		}
		out = append(out, row)
	}
	if err == nil {
		err = rows.Err()
	}
	return out, err
}

func (s *Store) sweepReporting(ctx context.Context, state *State, now time.Time) error {
	if now.Before(s.reporting.NextSweep) {
		return nil
	}
	rows, err := s.readReportingBatch(ctx)
	if err != nil {
		return err
	}
	return s.reporting.applySweep(rows, state, now)
}
func (r *reportingRegistry) applySweep(rows []storedReporting, state *State, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, row := range rows {
		if err := r.applyStoredReporting(row, state, now); err != nil {
			return err
		}
		r.Cursor = factKey(row.Operator, row.Source)
	}
	r.NextSweep = now.Add(reportingSweepInterval)
	if len(rows) < reportingSweepBatch {
		r.Cursor = ""
	}
	return nil
}
func (r *reportingRegistry) applyStoredReporting(row storedReporting, state *State, now time.Time) error {
	e, err := r.entry(row.Operator, row.Source)
	if err != nil {
		return err
	}
	if !e.Loaded {
		e.Record = row.Record
		e.Loaded = true
	}
	live := state.Live[row.Operator]
	verified := live != nil && !live.Unverified
	members := reportingMembers(live)
	v, present := members[row.Source]
	e.acceptMembership(live, v, present, now)
	e.classify(state.Operators[row.Operator], !verified, now)
	return nil
}
