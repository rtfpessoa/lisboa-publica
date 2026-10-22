package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const pressureBatchRows = 10_000
const pressureCreditRemainder = 1_000
const pressureRecentWindow = 7 * 24 * time.Hour

func (s *Store) historyRetentionCutoff(ctx context.Context) (*time.Time, error) {
	var cutoff *time.Time
	err := s.DB.QueryRow(ctx, `SELECT cutoff FROM history_pressure WHERE id=1`).Scan(&cutoff)
	return cutoff, err
}

// replacementEligible permits only recent snapshot inserts backed by committed
// deletions. The ordinary database-size ceiling remains in force otherwise.
func (s *Store) replacementEligible(ctx context.Context, records []historicalRecord) (bool, error) {
	selected, err := s.replacementRecords(ctx, records)
	return len(selected) > 0 && len(selected) == len(records), err
}

func (s *Store) replacementRecords(ctx context.Context, records []historicalRecord) ([]historicalRecord, error) {
	if len(records) == 0 {
		return nil, nil
	}
	var enabled, pending bool
	var credit int64
	if err := s.DB.QueryRow(ctx, `SELECT enabled,insert_credit,vacuum_pending FROM history_pressure WHERE id=1`).Scan(&enabled, &credit, &pending); err != nil {
		return nil, err
	}
	if !enabled || pending || credit <= 0 {
		return nil, nil
	}
	allowed, err := s.pressureVacuumAllowed(ctx)
	if err != nil || !allowed {
		return nil, err
	}
	return selectReplacementRecords(records, credit), nil
}

func selectReplacementRecords(records []historicalRecord, credit int64) []historicalRecord {
	now := time.Now().UTC()
	cutoff := now.Add(-pressureRecentWindow)
	selected := make([]historicalRecord, 0, min(int64(len(records)), credit))
	for _, record := range records {
		if !record.Vehicle.ObservedAt.Before(cutoff) && !record.Vehicle.ObservedAt.After(now.Add(providerClockSkew)) {
			selected = append(selected, record)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if !selected[i].Vehicle.ObservedAt.Equal(selected[j].Vehicle.ObservedAt) {
			return selected[i].Vehicle.ObservedAt.Before(selected[j].Vehicle.ObservedAt)
		}
		return selected[i].Vehicle.Id < selected[j].Vehicle.Id
	})
	return selected[:min(int64(len(selected)), credit)]
}

// pressurePrune runs with writeMu held. A new batch waits until committed
// recent insertions have consumed all but a small remainder of the prior credit.
func (s *Store) pressurePrune(ctx context.Context) error {
	if s.budget == nil {
		return nil
	}
	pending, err := s.resumePressureVacuum(ctx)
	if err != nil || pending {
		return err
	}
	eligible, err := s.pressureCleanupEligible(ctx)
	if err != nil || !eligible {
		return err
	}
	allowed, err := s.pressureCleanupAllowed(ctx)
	if err != nil || !allowed {
		return err
	}
	if err := s.reserveStorage(ctx, cleanupReservation, pressureDatabaseBytes); err != nil {
		return err
	}
	deleted, err := s.deletePressureBatch(ctx)
	if err == nil && deleted > 0 {
		s.schedulePressureVacuum(ctx)
	}
	return err
}

// resumePressureVacuum retries durable work before any new deletion.
func (s *Store) resumePressureVacuum(ctx context.Context) (bool, error) {
	var pending bool
	if err := s.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil {
		return false, err
	}
	if pending {
		s.schedulePressureVacuum(ctx)
		if problem := s.pressureError.Load(); problem != nil {
			return true, fmt.Errorf("%s", *problem)
		}
	}
	return pending, nil
}

// Unsupported engines do not run pressure cleanup.
func (s *Store) pressureCleanupAllowed(ctx context.Context) (bool, error) {
	var version string
	if err := s.DB.QueryRow(ctx, `SELECT version()`).Scan(&version); err != nil {
		return false, err
	}
	if !strings.Contains(version, "PostgreSQL") || strings.Contains(version, "CockroachDB") {
		return false, nil
	}
	allowed, err := s.pressureVacuumAllowed(ctx)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, fmt.Errorf("history pressure maintenance is not permitted or supported")
	}
	return true, nil
}

func (s *Store) pressureCleanupEligible(ctx context.Context) (bool, error) {
	b := s.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.refresh(ctx); err != nil {
		return false, err
	}
	if err := b.measureFresh(ctx); err != nil {
		return false, err
	}
	return b.bytes >= historyDatabaseBytes && b.bytes+b.reserved+cleanupReservation < pressureDatabaseBytes, nil
}

func (s *Store) deletePressureBatch(ctx context.Context) (int64, error) {
	s.historyReadMu.Lock()
	defer s.historyReadMu.Unlock()
	var deleted int64
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var available, pending int64
		if err := tx.QueryRow(ctx, `SELECT delete_credit,insert_credit FROM history_pressure WHERE id=1 FOR UPDATE`).Scan(&available, &pending); err != nil {
			return err
		}
		if available <= 0 || pending > pressureCreditRemainder {
			return nil
		}
		limit := min(available, int64(pressureBatchRows))
		var cutoff *time.Time
		err := tx.QueryRow(ctx, `WITH oldest AS (
			SELECT ctid FROM snapshots WHERE observed_at < $1
			ORDER BY observed_at,operator_id,vehicle_id LIMIT $2
		), removed AS (
			DELETE FROM snapshots USING oldest WHERE snapshots.ctid=oldest.ctid RETURNING observed_at
		) SELECT count(*),max(observed_at) FROM removed`, time.Now().UTC().Add(-pressureRecentWindow), limit).Scan(&deleted, &cutoff)
		if err != nil || deleted == 0 {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,delete_credit=delete_credit-$1,
			insert_credit=insert_credit+$1,epoch=epoch+1,cutoff=GREATEST(cutoff,$2),vacuum_pending=TRUE WHERE id=1`, deleted, cutoff.Add(time.Microsecond))
		return err
	})
	return deleted, err
}

func (s *Store) schedulePressureVacuum(parent context.Context) {
	s.pauseHistoryCollection()
	if !s.pressureVacuum.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.pressureVacuum.Store(false)
		failed := func() {
			message := "history pressure vacuum failed or is not permitted"
			s.pressureError.Store(&message)
		}
		ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
		defer cancel()
		allowed, err := s.pressureVacuumAllowed(ctx)
		if err != nil || !allowed {
			failed()
			return
		}
		if _, err := s.DB.Exec(ctx, `VACUUM (TRUNCATE FALSE) snapshots`); err != nil {
			failed()
			return
		}
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if _, err := s.DB.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=FALSE WHERE id=1 AND vacuum_pending=TRUE`); err != nil {
			failed()
			return
		}
		s.pressureError.Store(nil)
	}()
}

// PostgreSQL 17 introduced MAINTAIN. Fail closed on older versions and when
// the current role cannot vacuum this exact relation.
func (s *Store) pressureVacuumAllowed(ctx context.Context) (bool, error) {
	var product string
	if err := s.DB.QueryRow(ctx, `SELECT version()`).Scan(&product); err != nil {
		return false, err
	}
	if !strings.Contains(product, "PostgreSQL") || strings.Contains(product, "CockroachDB") {
		return false, nil
	}
	var version int
	if err := s.DB.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&version); err != nil {
		return false, err
	}
	if version < 170000 {
		return false, nil
	}
	var allowed bool
	err := s.DB.QueryRow(ctx, `SELECT has_table_privilege(current_user, 'snapshots', 'MAINTAIN')`).Scan(&allowed)
	return allowed, err
}
