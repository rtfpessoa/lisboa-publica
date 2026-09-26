package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
