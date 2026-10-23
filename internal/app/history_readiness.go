package app

import (
	"context"
	"errors"
)

// refreshEmptyHistoryStatus checks whether collection can resume without
// charging an empty batch against the ordinary history ceiling.
func (s *Store) refreshEmptyHistoryStatus(ctx context.Context) error {
	return s.refreshEmptyHistoryStatusWithPending(ctx, 0)
}

func (s *Store) refreshEmptyHistoryStatusWithPending(ctx context.Context, pendingOperations int64) error {
	check, cancel := context.WithTimeout(ctx, storageCheckTimeout)
	defer cancel()
	checkRecovery, err := s.budget.emptyHistoryHeadroom(check, pendingOperations)
	if err != nil || !checkRecovery {
		return err
	}
	ready, err := s.emptyHistoryRecoveryReady(check)
	return s.applyPressureReadiness(check, ready, pendingOperations, err)
}

// applyPressureReadiness rechecks mutable safety conditions after the query.
func (s *Store) applyPressureReadiness(check context.Context, ready bool, pendingOperations int64, queryErr error) error {
	b := s.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if queryErr != nil {
		b.setHistoryState("unavailable")
		return queryErr
	}
	if err := check.Err(); err != nil {
		b.setHistoryState("unavailable")
		return err
	}
	if b.uncertainUnknown || len(b.uncertainBackends) != 0 {
		b.setHistoryState("unavailable")
		return errUncertainStorageWrite
	}
	if !ready || s.pressureVacuum.Load() || (b.vacuumActive != nil && b.vacuumActive.Load()) || b.bytes+b.reserved+historyRecordOverhead > pressureDatabaseBytes {
		b.setHistoryState("paused")
		return nil
	}
	filesystemReady, err := b.historyReadinessFilesystem(pendingOperations)
	if err != nil || !filesystemReady {
		return err
	}
	b.state = "collecting"
	b.pressureReady = true
	return nil
}

func (b *storageBudget) emptyHistoryHeadroom(ctx context.Context, pendingOperations int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.uncertainUnknown || len(b.uncertainBackends) != 0 {
		b.setHistoryState("unavailable")
		return false, errUncertainStorageWrite
	}
	projected := b.bytes + b.reserved + historyRecordOverhead
	if projected > historyDatabaseBytes {
		if err := b.measureFresh(ctx); err != nil {
			return false, err
		}
		projected = b.bytes + b.reserved + historyRecordOverhead
	}
	filesystemReady, err := b.historyReadinessFilesystem(pendingOperations)
	if err != nil || !filesystemReady {
		return false, err
	}
	return b.updateEmptyHistoryState(projected), nil
}

// updateEmptyHistoryState requires budget.mu and reports whether pressure
// credit and privilege must be checked before collection can resume.
func (b *storageBudget) updateEmptyHistoryState(projected int64) bool {
	if projected <= historyDatabaseBytes {
		b.setOrdinaryCollecting()
		return false
	}
	if projected > pressureDatabaseBytes {
		b.setHistoryState("paused")
		return false
	}
	if !b.pressureReady || (b.vacuumActive != nil && b.vacuumActive.Load()) {
		b.setHistoryState("paused")
	}
	return true
}

// historyReadinessFilesystem requires budget.mu. If history does not fit,
// an operation that has not committed must pass its own fresh check before it
// can continue without history.
func (b *storageBudget) historyReadinessFilesystem(pendingOperations int64) (bool, error) {
	err := b.checkFilesystem(pendingOperations + historyRecordOverhead)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, errFilesystemHeadroom) {
		return false, err
	}
	if pendingOperations > 0 {
		if err := b.checkFilesystem(pendingOperations); err != nil {
			return false, err
		}
	}
	b.setHistoryState("paused")
	return false, nil
}

func (s *Store) emptyHistoryRecoveryReady(ctx context.Context) (bool, error) {
	var enabled, pending bool
	var credit int64
	if err := s.DB.QueryRow(ctx, `SELECT enabled,insert_credit,vacuum_pending FROM history_pressure WHERE id=1`).Scan(&enabled, &credit, &pending); err != nil {
		return false, err
	}
	if !enabled || pending || credit <= 0 {
		return false, nil
	}
	return s.pressureVacuumAllowed(ctx)
}

func (s *Store) pauseHistoryCollection() {
	if s.budget == nil {
		return
	}
	s.budget.mu.Lock()
	defer s.budget.mu.Unlock()
	if s.budget.state != "unavailable" {
		s.budget.setHistoryState("paused")
	} else {
		s.budget.pressureReady = false
	}
}
