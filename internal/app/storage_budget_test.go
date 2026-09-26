package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestStorageBudgetThresholdAndFailure(t *testing.T) {
	now := time.Now()
	measured := historyDatabaseBytes - historyRecordOverhead
	var failure error
	checks := 0
	budget := &storageBudget{now: func() time.Time { return now }, measure: func(context.Context) (int64, error) { checks++; return measured, failure }}
	var accepted atomic.Int32
	var workers sync.WaitGroup
	for range 100 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if budget.reserve(context.Background(), historyRecordOverhead, historyDatabaseBytes) == nil {
				accepted.Add(1)
			}
		}()
	}
	workers.Wait()
	if accepted.Load() != 1 || checks != 1 || budget.status() != "paused" {
		t.Fatalf("concurrent threshold: %d writes,%d measurements,status %s", accepted.Load(), checks, budget.status())
	}
	if err := budget.reserve(context.Background(), historyRecordOverhead, operationalDatabaseBytes); err != nil {
		t.Fatal("operational headroom:", err)
	}
	now = now.Add(storageCheckInterval)
	failure = errors.New("unavailable")
	if budget.reserve(context.Background(), 1, operationalDatabaseBytes) == nil || budget.status() != "unavailable" {
		t.Fatal("measurement must fail closed")
	}
	now = now.Add(storageCheckInterval)
	failure, measured = nil, 0
	if err := budget.reserve(context.Background(), 1, historyDatabaseBytes); err != nil || budget.status() != "collecting" {
		t.Fatalf("remeasure recovery: %v", err)
	}
	if budget.reserve(context.Background(), maximumWriteBytes+1, operationalDatabaseBytes) == nil {
		t.Fatal("unbounded transaction accepted")
	}
}

func TestDatabaseBytes(t *testing.T) {
	store := testStore(t)
	bytes, err := store.databaseBytes(context.Background())
	if err != nil || bytes < 0 {
		t.Fatalf("database measurement: %d %v", bytes, err)
	}
}

func TestMigrationBudgetRejectsBackfillAndUnavailableMeasurement(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, "DROP INDEX snapshots_route"); err != nil {
		t.Fatal(err)
	}
	store.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return maximumWriteBytes, nil }}
	if store.reserveMigrations(ctx) == nil {
		t.Fatal("unbounded backfill accepted")
	}
	var indexes int
	if err := store.DB.QueryRow(ctx, "SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='snapshots_route'").Scan(&indexes); err != nil || indexes != 0 {
		t.Fatalf("rejected migration changed schema: %d %v", indexes, err)
	}
	store.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 0, errors.New("unavailable") }}
	if store.reserveMigrations(ctx) == nil {
		t.Fatal("measurement failure permitted migration")
	}
	store.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 0, nil }}
	if err := store.reserveMigrations(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCachePartsReplaceAndShrink(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	blob := make([]byte, cachePartBytes+1)
	blob[len(blob)-1] = 1
	for _, data := range [][]byte{blob, blob, []byte("small")} {
		if err := store.transaction(ctx, func(tx pgx.Tx) error { return writeCache(ctx, tx, "carris", "fixture", data) }); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var data []byte
	if err := store.DB.QueryRow(ctx, "SELECT count(*) FROM cache_parts WHERE kind='fixture'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(ctx, "SELECT data FROM cache_parts WHERE kind='fixture' AND part=0").Scan(&data); err != nil {
		t.Fatal(err)
	}
	if count != 1 || string(data) != "small" {
		t.Fatalf("cache retained old tail: %d %q", count, data)
	}
}

func TestStorageUpdateUsesSingleMeasurement(t *testing.T) {
	now := time.Now()
	checks := 0
	budget := &storageBudget{now: func() time.Time { now = now.Add(storageCheckInterval); return now }, measure: func(context.Context) (int64, error) { checks++; return historyDatabaseBytes - 10_000_000, nil }}
	keep, err := budget.reserveUpdate(context.Background(), 120_000_000, 1_000_000)
	if err != nil || keep || checks != 1 || budget.reserved != 120_000_000 || budget.status() != "paused" {
		t.Fatalf("split reservation crossed measurement: keep=%v err=%v checks=%d reserved=%d state=%s", keep, err, checks, budget.reserved, budget.status())
	}
}

func TestOpenStoreWithStorageGuard(t *testing.T) {
	store := testStore(t)
	guarded, err := OpenStoreWithStorageGuard(context.Background(), store.DB.Config().ConnString(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer guarded.DB.Close()
	original := guarded.budget
	if original == nil || guarded.historyStatus() != "collecting" {
		t.Fatal("startup guard unavailable")
	}
	if err := guarded.ConfigureHistory(30, staticRefreshInterval, true); err != nil {
		t.Fatal(err)
	}
	if guarded.budget != original {
		t.Fatal("configuration discarded migration reservation")
	}
}
