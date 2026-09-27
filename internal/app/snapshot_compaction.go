package app

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const snapshotArchiveBatch = 128
const snapshotArchiveTimeout = 2 * time.Second
const snapshotArchiveAge = 24 * time.Hour

type snapshotArchiveKey struct {
	At                time.Time
	Operator, Vehicle string
}
type snapshotArchiveCandidate struct {
	Key    snapshotArchiveKey
	Stored int64
	Raw    *string
}
type snapshotArchiveUpdate struct {
	Candidate           snapshotArchiveCandidate
	Projection, Archive []byte
}

// Optional maintenance never queues behind ingestion. Pruning runs independently first.
func (s *Store) compactSnapshots(parent context.Context) error {
	if !s.writeMu.TryLock() {
		return nil
	}
	defer s.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, snapshotArchiveTimeout)
	defer cancel()
	supported, err := s.supportsSnapshotCompaction(ctx)
	if err != nil || !supported {
		return err
	}
	candidates, err := s.archiveCandidates(ctx)
	if err != nil {
		return err
	}
	committed, err := s.compactSnapshotBatch(ctx, candidates)
	if committed {
		s.archiveCursor = nil
		if len(candidates) == snapshotArchiveBatch {
			s.archiveCursor = &candidates[len(candidates)-1].Key
		}
	}
	return err
}

func (s *Store) supportsSnapshotCompaction(ctx context.Context) (bool, error) {
	var version string
	err := s.DB.QueryRow(ctx, "SELECT version()").Scan(&version)
	// Cockroach's pg_column_size is encoded size, not compressed SST storage size.
	return strings.Contains(version, "PostgreSQL") && !strings.Contains(version, "CockroachDB"), err
}

func (s *Store) archiveCandidates(ctx context.Context) ([]snapshotArchiveCandidate, error) {
	now := time.Now().UTC()
	var after *time.Time
	operator, vehicle := "", ""
	if s.archiveCursor != nil {
		after = &s.archiveCursor.At
		operator = s.archiveCursor.Operator
		vehicle = s.archiveCursor.Vehicle
	}
	rows, err := s.DB.Query(ctx, `SELECT operator_id,vehicle_id,observed_at,pg_column_size(payload),
 CASE WHEN octet_length(payload::TEXT)<=$3 THEN payload::TEXT ELSE NULL END
 FROM snapshots WHERE observed_at >= $1 AND observed_at < $2 AND payload_archive IS NULL
 AND ($4::TIMESTAMPTZ IS NULL OR (observed_at,operator_id,vehicle_id)>($4,$5,$6))
 ORDER BY observed_at,operator_id,vehicle_id LIMIT $7`, now.AddDate(0, 0, -s.retentionDays()), now.Add(-snapshotArchiveAge), snapshotArchiveLimit, after, operator, vehicle, snapshotArchiveBatch)
	if err != nil {
		return nil, err
	}
	out := []snapshotArchiveCandidate{}
	var row snapshotArchiveCandidate
	_, err = pgx.ForEachRow(rows, []any{&row.Key.Operator, &row.Key.Vehicle, &row.Key.At, &row.Stored, &row.Raw}, func() error { out = append(out, row); return ctx.Err() })
	return out, err
}

func (s *Store) archiveUpdates(ctx context.Context, candidates []snapshotArchiveCandidate) ([]snapshotArchiveUpdate, error) {
	updates := []snapshotArchiveUpdate{}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		update, err := s.prepareArchive(ctx, candidate)
		if err != nil {
			return nil, err
		}
		if update != nil {
			updates = append(updates, *update)
		}
	}
	return updates, nil
}

func (s *Store) prepareArchive(ctx context.Context, candidate snapshotArchiveCandidate) (*snapshotArchiveUpdate, error) {
	if candidate.Raw == nil || candidate.Stored <= snapshotArchiveMinimumSaving {
		return nil, nil
	}
	projection, archive, err := archiveSnapshot([]byte(*candidate.Raw))
	if err != nil {
		return nil, nil
	}
	return s.admitArchive(ctx, &snapshotArchiveUpdate{Candidate: candidate, Projection: projection, Archive: archive})
}

func archiveRewriteBytes(updates []snapshotArchiveUpdate) int64 {
	var total int64
	for _, update := range updates {
		total += int64(len(*update.Candidate.Raw)+len(update.Projection)+len(update.Archive))*storageWriteOverhead + historyRecordOverhead
	}
	return total
}
func (s *Store) reserveCompaction(ctx context.Context, bytes int64) (bool, error) {
	if bytes == 0 {
		return true, nil
	}
	if s.budget == nil {
		return bytes <= maximumWriteBytes, nil
	}
	return s.budget.reserveOptional(ctx, bytes)
}
func (b *storageBudget) reserveOptional(ctx context.Context, bytes int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.refresh(ctx); err != nil {
		return false, err
	}
	if bytes < 0 || bytes > maximumWriteBytes || b.bytes+b.reserved+bytes > operationalDatabaseBytes {
		return false, nil
	}
	b.reserved += bytes
	return true, nil
}
func (s *Store) writeArchives(ctx context.Context, updates []snapshotArchiveUpdate) error {
	if len(updates) == 0 {
		return ctx.Err()
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for _, update := range updates {
			key := update.Candidate.Key
			batch.Queue(`UPDATE snapshots SET payload=$4,payload_archive=$5 WHERE operator_id=$1 AND vehicle_id=$2 AND observed_at=$3 AND payload_archive IS NULL AND payload=$6::JSONB`, key.Operator, key.Vehicle, key.At, update.Projection, update.Archive, *update.Candidate.Raw)
		}
		return tx.SendBatch(ctx, batch).Close()
	})
}

func (s *Store) compactSnapshotBatch(ctx context.Context, candidates []snapshotArchiveCandidate) (bool, error) {
	updates, err := s.archiveUpdates(ctx, candidates)
	if err != nil {
		return false, err
	}
	admitted, err := s.reserveCompaction(ctx, archiveRewriteBytes(updates))
	if err != nil || !admitted {
		return false, err
	}
	err = s.writeArchives(ctx, updates)
	return err == nil, err
}

func (s *Store) admitArchive(ctx context.Context, update *snapshotArchiveUpdate) (*snapshotArchiveUpdate, error) {
	var stored int64
	err := s.DB.QueryRow(ctx, "SELECT pg_column_size($1::JSONB)+pg_column_size($2::BYTEA)", update.Projection, update.Archive).Scan(&stored)
	if err != nil {
		return nil, err
	}
	if stored+snapshotArchiveRowOverhead+snapshotArchiveMinimumSaving > update.Candidate.Stored {
		return nil, nil
	}
	return update, nil
}
