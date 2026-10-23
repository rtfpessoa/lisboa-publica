package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
)

const pressureBatchRows = 10_000
const pressureCreditRemainder = 1_000
const pressureRecentWindow = 7 * 24 * time.Hour
const pressureVacuumTimeout = 15 * time.Minute

type pressureMaintenanceResult struct {
	reason  string
	deleted int64
	cutoff  *time.Time
	epoch   int64
}

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
func (s *Store) pressurePrune(ctx, vacuumParent context.Context) (pressureMaintenanceResult, error) {
	if s.budget == nil {
		return pressureMaintenanceResult{reason: "storage_guard_disabled"}, nil
	}
	pending, err := s.resumePressureVacuum(ctx, vacuumParent)
	if err != nil || pending {
		return pressureMaintenanceResult{reason: "vacuum_pending"}, err
	}
	// A previously pending vacuum needs the ordinary cleanup reservation until
	// it finishes. Only reconcile that completed transaction once no vacuum is
	// being resumed.
	if err := s.reconcileCompletedReservations(ctx); err != nil {
		return pressureMaintenanceResult{reason: "ordinary_cleanup_committed_measurement_failed"}, err
	}
	eligible, reason, err := s.pressureCleanupEligible(ctx)
	if err != nil || !eligible {
		return pressureMaintenanceResult{reason: reason}, err
	}
	allowed, err := s.pressureCleanupAllowed(ctx)
	if err != nil || !allowed {
		return pressureMaintenanceResult{reason: "unsupported_or_not_permitted"}, err
	}
	if err := s.reserveStorage(ctx, cleanupReservation, pressureDatabaseBytes); err != nil {
		return pressureMaintenanceResult{reason: "insufficient_reserved_headroom"}, err
	}
	result, err := s.deletePressureBatch(ctx)
	if err == nil && result.deleted > 0 {
		s.schedulePressureVacuum(vacuumParent)
	}
	return result, err
}

// resumePressureVacuum retries durable work before any new deletion.
func (s *Store) resumePressureVacuum(ctx, vacuumParent context.Context) (bool, error) {
	var pending bool
	if err := s.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil {
		return false, err
	}
	if pending {
		s.schedulePressureVacuum(vacuumParent)
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

func (s *Store) pressureCleanupEligible(ctx context.Context) (bool, string, error) {
	b := s.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.refresh(ctx); err != nil {
		return false, "measurement_failed", err
	}
	if err := b.measureFresh(ctx); err != nil {
		return false, "measurement_failed", err
	}
	if b.bytes < historyDatabaseBytes {
		return false, "below_history_limit", nil
	}
	if b.bytes+b.reserved+cleanupReservation >= pressureDatabaseBytes {
		return false, "insufficient_reserved_headroom", nil
	}
	return true, "eligible", nil
}

func (s *Store) deletePressureBatch(ctx context.Context) (pressureMaintenanceResult, error) {
	if err := s.historyReadMu.LockContext(ctx); err != nil {
		return pressureMaintenanceResult{reason: "history_reader_wait_canceled"}, err
	}
	defer s.historyReadMu.Unlock()
	result := pressureMaintenanceResult{reason: "no_eligible_old_snapshots"}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		result = pressureMaintenanceResult{reason: "no_eligible_old_snapshots"}
		var available, pending int64
		if err := tx.QueryRow(ctx, `SELECT delete_credit,insert_credit FROM history_pressure WHERE id=1 FOR UPDATE`).Scan(&available, &pending); err != nil {
			return err
		}
		if available <= 0 {
			result.reason = "delete_credit_exhausted"
			return nil
		}
		if pending > pressureCreditRemainder {
			result.reason = "awaiting_insert_credit_consumption"
			return nil
		}
		limit := min(available, int64(pressureBatchRows))
		var cutoff *time.Time
		err := tx.QueryRow(ctx, `WITH oldest AS (
			SELECT ctid FROM snapshots WHERE observed_at < $1
			ORDER BY observed_at,operator_id,vehicle_id LIMIT $2
		), removed AS (
			DELETE FROM snapshots USING oldest WHERE snapshots.ctid=oldest.ctid RETURNING observed_at
		) SELECT count(*),max(observed_at) FROM removed`, time.Now().UTC().Add(-pressureRecentWindow), limit).Scan(&result.deleted, &cutoff)
		if err != nil || result.deleted == 0 {
			return err
		}
		// Prevent new staging before the pending vacuum becomes durable.
		s.pauseHistoryCollection()
		retainedCutoff := cutoff.Add(time.Microsecond)
		result.cutoff = &retainedCutoff
		result.reason = "vacuum_scheduled"
		err = tx.QueryRow(ctx, `UPDATE history_pressure SET enabled=TRUE,delete_credit=delete_credit-$1,
			insert_credit=insert_credit+$1,epoch=epoch+1,cutoff=GREATEST(cutoff,$2),vacuum_pending=TRUE WHERE id=1 RETURNING epoch,cutoff`, result.deleted, retainedCutoff).Scan(&result.epoch, &retainedCutoff)
		result.cutoff = &retainedCutoff
		return err
	})
	if err != nil {
		return pressureMaintenanceResult{reason: "pressure_transaction_failed"}, err
	}
	return result, err
}

func (s *Store) schedulePressureVacuum(parent context.Context) {
	s.pauseHistoryCollection()
	if !s.pressureVacuum.CompareAndSwap(false, true) {
		return
	}
	s.pressureVacuumWG.Add(1)
	go func() {
		defer s.pressureVacuumWG.Done()
		ownsFlag := true
		defer func() {
			if ownsFlag {
				s.pressureVacuum.Store(false)
			}
		}()
		started := time.Now()
		failed := func(err error) {
			message := "history pressure vacuum failed or is not permitted"
			s.pressureError.Store(&message)
			if log := s.maintenanceLog.Load(); log != nil {
				log.Warn(message, zap.Error(err), zap.Duration("duration", time.Since(started)))
			}
		}
		ctx, cancel := context.WithTimeout(parent, pressureVacuumTimeout)
		defer cancel()
		if s.budget != nil {
			s.budget.mu.Lock()
			err := s.budget.checkFilesystem(cleanupReservation)
			s.budget.mu.Unlock()
			if err != nil {
				failed(err)
				return
			}
		}
		allowed, err := s.pressureVacuumAllowed(ctx)
		if err != nil || !allowed {
			failed(err)
			return
		}
		if err := s.runPressureVacuum(ctx); err != nil {
			failed(err)
			return
		}
		if err := s.writeMu.LockContext(ctx); err != nil {
			failed(err)
			return
		}
		defer s.writeMu.Unlock()
		if s.budget != nil {
			s.budget.mu.Lock()
			err := s.budget.checkFilesystem(cleanupReservation)
			s.budget.mu.Unlock()
			if err != nil {
				failed(err)
				return
			}
		}
		if err := s.transaction(ctx, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=FALSE WHERE id=1 AND vacuum_pending=TRUE`)
			return err
		}); err != nil {
			failed(err)
			return
		}
		// VACUUM and the durable pending-clear transaction have completed.
		// Drop ownership while holding writeMu, so an older goroutine cannot
		// clear a later vacuum's flag after that lock is released.
		s.pressureVacuum.Store(false)
		ownsFlag = false
		s.pressureError.Store(nil)
		s.recoverAfterPressureVacuum(ctx)
		if log := s.maintenanceLog.Load(); log != nil {
			log.Info("history pressure vacuum completed", zap.Duration("duration", time.Since(started)))
		}
	}()
}

// The pending clear is already committed; recovery failures are reported
// separately and cannot turn a successful vacuum into a failed transaction.
func (s *Store) recoverAfterPressureVacuum(ctx context.Context) {
	if s.budget == nil {
		return
	}
	check, cancel := context.WithTimeout(ctx, storageCheckTimeout)
	defer cancel()
	err := s.reconcileCompletedReservations(check)
	if err == nil {
		err = s.refreshEmptyHistoryStatus(check)
	}
	if err != nil {
		if log := s.maintenanceLog.Load(); log != nil {
			log.Warn("history vacuum completed; readiness check failed", zap.Error(err))
		}
	}
}

func (s *Store) runPressureVacuum(ctx context.Context) error {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	backend, err := s.backendForWrite(ctx, conn)
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, `VACUUM (TRUNCATE FALSE) snapshots`)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		// Without an explicitly verified direct PostgreSQL connection,
		// a nontransactional VACUUM cannot be assigned a trusted backend.
		s.discardUncertainConnection(conn, backend)
	}
	return err
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
