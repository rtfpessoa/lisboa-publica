package app

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestMaintenanceLockCancellationDoesNotWaitForWriter(t *testing.T) {
	s := &Store{}
	s.writeMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.pruneWithVacuumContext(ctx, ctx)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("maintenance wait returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled maintenance waited for the active writer")
	}
	if s.writeMu.TryLock() {
		t.Fatal("cancellation released the active writer")
	}
	s.writeMu.Unlock()
	if !s.writeMu.TryLock() {
		t.Fatal("canceled waiter retained the lock")
	}
	s.writeMu.Unlock()
}

func TestWriteLockCancellationRacingAcquisition(t *testing.T) {
	for i := 0; i < 500; i++ {
		var lock writeLock
		lock.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			err := lock.LockContext(ctx)
			if err == nil {
				lock.Unlock()
			}
			done <- err
		}()
		cancel()
		lock.Unlock()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if !lock.TryLock() {
			t.Fatalf("iteration %d leaked lock", i)
		}
		lock.Unlock()
	}
}

func TestWriteLockQueuedMaintenancePrecedesNewWriter(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	var lock writeLock
	lock.Lock()
	started := make(chan struct{})
	order := make(chan string, 2)
	go func() {
		close(started)
		lock.Lock()
		order <- "maintenance"
		lock.Unlock()
	}()
	<-started
	// Yield so the maintenance waiter enters the semaphore queue while the
	// first writer still holds it. New waiters cannot pass queued waiters.
	runtime.Gosched()
	go func() {
		lock.Lock()
		order <- "writer"
		lock.Unlock()
	}()
	lock.Unlock()
	if first := <-order; first != "maintenance" {
		t.Fatalf("new writer passed queued maintenance: %s", first)
	}
	<-order
}
