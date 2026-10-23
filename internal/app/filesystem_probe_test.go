package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestFilesystemProbeFailsClosed(t *testing.T) {
	capacity := filesystemCapacity{bytes: 2 << 30, inodes: 20_000}
	measurementError := error(nil)
	p := &filesystemProbe{
		minimumBytes: 1 << 30, minimumInodes: 10_000,
		measure: func() (filesystemCapacity, error) { return capacity, measurementError },
	}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	if err := b.reserve(context.Background(), 128<<20, maximumDatabaseBytes); err != nil {
		t.Fatalf("adequate filesystem space rejected: %v", err)
	}
	capacity.bytes = (1 << 30) + 1024
	if err := b.reserve(context.Background(), 2048, maximumDatabaseBytes); !errors.Is(err, errFilesystemHeadroom) || b.status() != "unavailable" {
		t.Fatalf("write exceeded free-space reserve: %v status=%s", err, b.status())
	}
	capacity.bytes, capacity.inodes = 2<<30, 9999
	if err := b.reserve(context.Background(), 0, maximumDatabaseBytes); !errors.Is(err, errFilesystemHeadroom) {
		t.Fatalf("inode exhaustion accepted: %v", err)
	}
	measurementError = errors.New("probe failed")
	if err := b.reserve(context.Background(), 0, maximumDatabaseBytes); err == nil || b.status() != "unavailable" {
		t.Fatalf("failed probe accepted: %v status=%s", err, b.status())
	}
}

func TestFilesystemProbeRejectsUnsafeDirectory(t *testing.T) {
	if _, err := newFilesystemProbe(StorageFilesystemConfig{Path: "relative", MinimumBytes: 1 << 30, MinimumInodes: 1}); err == nil {
		t.Fatal("relative probe path accepted")
	}
	if _, err := measureDatabaseFilesystem(t.TempDir()); err == nil {
		t.Fatal("application-owned directory accepted as a private PGDATA probe")
	}
}

func TestFilesystemProbeChecksCombinedTransaction(t *testing.T) {
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) { return filesystemCapacity{bytes: (1 << 30) + 1200, inodes: 2}, nil }}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	if keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false); err != nil || keep || b.reserved != 700 {
		t.Fatalf("combined write should fall back to operations: keep=%v reserved=%d err=%v", keep, b.reserved, err)
	}
}

func TestFilesystemProbeAcceptsOperationsWhenHistoryIsDenied(t *testing.T) {
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) { return filesystemCapacity{bytes: (1 << 30) + 1000, inodes: 2}, nil }}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }, filesystem: p}
	keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false)
	if err != nil || keep || b.reserved != 700 {
		t.Fatalf("operations should fit after history is denied: keep=%v reserved=%d err=%v", keep, b.reserved, err)
	}
}

func TestFilesystemProbeFailureStillRejectsOperationsOnly(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			if checks > 1 {
				return filesystemCapacity{}, errors.New("lost probe")
			}
			return filesystemCapacity{bytes: 2 << 30, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }, filesystem: p}
	if keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false); err == nil || keep {
		t.Fatalf("failed probe allowed operations after history denial: keep=%v err=%v", keep, err)
	}
}

func TestFilesystemProbeDropsHistoryWhenOnlyOperationsFit(t *testing.T) {
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) { return filesystemCapacity{bytes: (1 << 30) + 1000, inodes: 2}, nil }}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false)
	if err != nil || keep || b.reserved != 700 || b.status() != "paused" {
		t.Fatalf("operations-only fallback: keep=%v reserved=%d status=%s err=%v", keep, b.reserved, b.status(), err)
	}
}

func TestFilesystemProbeFailureDuringOperationsFallbackIsFatal(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			if checks > 2 {
				return filesystemCapacity{}, errors.New("lost probe")
			}
			return filesystemCapacity{bytes: (1 << 30) + 1000, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	if keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false); err == nil || keep {
		t.Fatalf("lost probe allowed fallback: keep=%v err=%v", keep, err)
	}
}

func TestFilesystemProbeRechecksFinalCombinedAdmission(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			available := uint64(2000)
			if checks >= 4 {
				available = 800
			}
			return filesystemCapacity{bytes: (1 << 30) + available, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false)
	if err != nil || keep || b.reserved != 700 || b.status() != "paused" {
		t.Fatalf("later low observation must drop history: keep=%v reserved=%d status=%s err=%v", keep, b.reserved, b.status(), err)
	}
}

func TestFilesystemProbeFallsBackAfterHistoryClaimShortage(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			available := uint64(2000)
			if checks >= 4 {
				available = 600
			}
			return filesystemCapacity{bytes: (1 << 30) + available, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	keep, err := b.reserveUpdatePressure(context.Background(), 400, 700, false)
	if err != nil || keep || b.reserved != 400 || b.status() != "paused" {
		t.Fatalf("history claim shortage should allow operations: keep=%v reserved=%d status=%s err=%v", keep, b.reserved, b.status(), err)
	}
}

func TestFilesystemProbeFailureOnFinalCombinedCheckIsFatal(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			if checks >= 5 {
				return filesystemCapacity{}, errors.New("lost probe")
			}
			return filesystemCapacity{bytes: 2 << 30, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	if keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false); err == nil || keep {
		t.Fatalf("failed final probe accepted writes: keep=%v err=%v", keep, err)
	}
}

func TestEmptyUpdateReadinessIncludesPendingOperations(t *testing.T) {
	available := uint64(1200)
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			return filesystemCapacity{bytes: (1 << 30) + available, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	s := &Store{budget: b}
	if keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false); err != nil || keep || b.status() != "paused" {
		t.Fatalf("history shortfall fixture: keep=%v status=%s err=%v", keep, b.status(), err)
	}
	if _, _, _, err := s.guardEmptyUpdate(context.Background(), 700); err != nil || b.status() != "paused" {
		t.Fatalf("pending operations left no minimal history headroom: status=%s err=%v", b.status(), err)
	}
	// A post-commit status check has no pending operations to count again.
	if err := s.refreshEmptyHistoryStatus(context.Background()); err != nil || b.status() != "collecting" {
		t.Fatalf("post-commit readiness counted old operations: status=%s err=%v", b.status(), err)
	}
	available = 2000
	if _, _, _, err := s.guardEmptyUpdate(context.Background(), 700); err != nil || b.status() != "collecting" {
		t.Fatalf("empty update did not recover with enough headroom: status=%s err=%v", b.status(), err)
	}
}

func TestEmptyUpdateReadinessProbeFailureIsUnavailable(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			if checks >= 5 {
				return filesystemCapacity{}, errors.New("lost probe")
			}
			return filesystemCapacity{bytes: 2 << 30, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	s := &Store{budget: b}
	if _, _, _, err := s.guardEmptyUpdate(context.Background(), 700); err == nil || b.status() != "unavailable" {
		t.Fatalf("readiness probe failure did not fail closed: status=%s err=%v", b.status(), err)
	}
}

func TestEmptyUpdateReadinessRechecksPendingOperations(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			available := uint64(3000)
			if checks >= 5 {
				available = 500
			}
			return filesystemCapacity{bytes: (1 << 30) + available, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	s := &Store{budget: b}
	if _, _, _, err := s.guardEmptyUpdate(context.Background(), 700); !errors.Is(err, errFilesystemHeadroom) || b.status() != "unavailable" {
		t.Fatalf("last observation rejected pending operations: status=%s err=%v", b.status(), err)
	}
}

func TestEmptyUpdateReadinessRecheckPropagatesProbeFailure(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			if checks >= 6 {
				return filesystemCapacity{}, errors.New("lost probe")
			}
			available := uint64(3000)
			if checks == 5 {
				available = 1200
			}
			return filesystemCapacity{bytes: (1 << 30) + available, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	s := &Store{budget: b}
	if _, _, _, err := s.guardEmptyUpdate(context.Background(), 700); err == nil || b.status() != "unavailable" {
		t.Fatalf("failed operations recheck must block the update: status=%s err=%v", b.status(), err)
	}
}

func TestPressureReadinessRechecksPendingOperations(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,insert_credit=1,vacuum_pending=FALSE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			available := uint64(3000)
			if checks >= 7 {
				available = 500
			}
			return filesystemCapacity{bytes: (1 << 30) + available, inodes: 2}, nil
		}}
	s.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }, filesystem: p}
	if _, _, _, err := s.guardEmptyUpdate(ctx, 700); !errors.Is(err, errFilesystemHeadroom) || s.budget.status() != "unavailable" {
		t.Fatalf("pressure check rejected pending operations: checks=%d status=%s err=%v", checks, s.budget.status(), err)
	}
}

func TestFilesystemProbeAccountsForConcurrentVacuum(t *testing.T) {
	var vacuum atomic.Bool
	vacuum.Store(true)
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			return filesystemCapacity{bytes: uint64((1 << 30) + cleanupReservation + 600), inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p, vacuumActive: &vacuum}
	if keep, err := b.reserveUpdatePressure(context.Background(), 400, 400, false); err != nil || keep || b.reserved != 400 {
		t.Fatalf("transaction and vacuum should fall back to operations: keep=%v reserved=%d err=%v", keep, b.reserved, err)
	}
}

func TestRefreshPreservesVacuumReservation(t *testing.T) {
	var vacuum atomic.Bool
	vacuum.Store(true)
	b := &storageBudget{now: time.Now, reserved: cleanupReservation, vacuumActive: &vacuum,
		measure: func(context.Context) (int64, error) { return 1, nil }}
	if err := b.refresh(context.Background()); err != nil || b.reserved != cleanupReservation {
		t.Fatalf("refresh lost vacuum reservation: %d %v", b.reserved, err)
	}
}

func TestFilesystemProbeFailureDuringHistoryAdmissionIsFatal(t *testing.T) {
	checks := 0
	p := &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			checks++
			if checks >= 4 {
				return filesystemCapacity{}, errors.New("lost probe")
			}
			return filesystemCapacity{bytes: 2 << 30, inodes: 2}, nil
		}}
	b := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, filesystem: p}
	if keep, err := b.reserveUpdatePressure(context.Background(), 700, 700, false); err == nil || keep {
		t.Fatalf("failed history probe allowed operational writes: keep=%v err=%v", keep, err)
	}
}

func TestFilesystemProbeBlocksPendingVacuum(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	s.budget = &storageBudget{
		now:     time.Now,
		measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil },
		filesystem: &filesystemProbe{minimumBytes: 1 << 30, minimumInodes: 10000,
			measure: func() (filesystemCapacity, error) { return filesystemCapacity{bytes: 1 << 30, inodes: 20000}, nil }},
	}
	s.schedulePressureVacuum(ctx)
	s.pressureVacuumWG.Wait()
	var pending bool
	if err := s.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil || !pending || s.pressureError.Load() == nil {
		t.Fatalf("low free space must leave vacuum pending: pending=%v err=%v", pending, err)
	}
}
