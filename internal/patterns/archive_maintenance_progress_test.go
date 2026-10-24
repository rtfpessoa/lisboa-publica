package patterns

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func waitMaintenanceProgress(t *testing.T, s *Service, ready func(MaintenanceProgress) bool) MaintenanceProgress {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		progress := s.MaintenanceProgress()
		if ready(progress) {
			return progress
		}
		select {
		case <-deadline.C:
			t.Fatalf("maintenance progress did not reach expected state: %+v", progress)
		case <-tick.C:
		}
	}
}

func wakeMaintenance(s *Service) {
	select {
	case s.maintenanceWake <- struct{}{}:
	default:
	}
}

func TestArchiveMaintenanceProgressWaitsForOwnerAndJoins(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	initial := s.MaintenanceProgress()
	if initial.LastResult != "never_started" || initial.WorkerStartedAt.IsZero() || initial.Interval != time.Hour || initial.InProgress || initial.Stopped {
		t.Fatalf("unexpected initial progress: %+v", initial)
	}
	wakeMaintenance(s)
	completed := waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool { return p.LastResult == "completed" && !p.InProgress })
	if completed.LastStartedAt.IsZero() || completed.LastCompletedAt.Before(completed.LastStartedAt) {
		t.Fatalf("completion clocks missing or reversed: %+v", completed)
	}

	// The heartbeat snapshot must remain available while the worker waits for
	// the owner mutex, and must preserve the preceding completed pass.
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		wakeMaintenance(s)
		blocked := waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool { return p.InProgress })
		if blocked.LastResult != "completed" || !blocked.LastCompletedAt.Equal(completed.LastCompletedAt) || !blocked.LastStartedAt.After(completed.LastStartedAt) {
			t.Fatalf("blocked pass hid previous completion: %+v", blocked)
		}
	}()
	recovered := waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool {
		return !p.InProgress && p.LastCompletedAt.After(completed.LastCompletedAt)
	})
	if recovered.LastResult != "completed" {
		t.Fatalf("unexpected recovery: %+v", recovered)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if stopped := s.MaintenanceProgress(); !stopped.Stopped || stopped.InProgress || stopped.LastResult != "completed" {
		t.Fatalf("worker was not joined: %+v", stopped)
	}
}

func TestArchiveMaintenanceProgressBoundedExpiry(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fixtureNow := time.Now().UTC().AddDate(0, 0, -8)
	older := checkpointFixture(t, s, "older", fixtureNow.Add(-2*time.Hour), fixtureNow)
	old := checkpointFixture(t, s, "old", fixtureNow.Add(-time.Hour), fixtureNow)
	wakeMaintenance(s)
	pending := waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool { return p.LastResult == "pending" && !p.InProgress })
	if _, err := os.Stat(filepath.Join(config.Directory, older.File)); !os.IsNotExist(err) {
		t.Fatalf("first expired generation was not retired: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.Directory, old.File)); err != nil {
		t.Fatalf("bounded first pass removed too much: %v", err)
	}
	completed := waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool {
		return p.LastResult == "completed" && p.LastCompletedAt.After(pending.LastCompletedAt)
	})
	if _, err := os.Stat(filepath.Join(config.Directory, old.File)); !os.IsNotExist(err) {
		t.Fatalf("pending work did not converge: %v (%+v)", err, completed)
	}
}

func TestArchiveMaintenanceProgressErrorAndRecovery(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	setProbe := func(probe func(string) (archiveSpace, error)) {
		s.mu.Lock()
		s.config.MinFreeBytes = 1
		s.spaceProbe = probe
		s.mu.Unlock()
		wakeMaintenance(s)
	}
	setProbe(func(string) (archiveSpace, error) {
		return archiveSpace{}, errors.New("synthetic private failure detail")
	})
	failed := waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool {
		return p.LastResult == "error" && !p.LastCompletedAt.IsZero()
	})
	setProbe(func(string) (archiveSpace, error) { return archiveSpace{bytes: 1 << 30, inodes: 1000}, nil })
	recovered := waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool {
		return p.LastResult == "completed" && p.LastCompletedAt.After(failed.LastCompletedAt)
	})
	if recovered.Stopped {
		t.Fatalf("recovery incorrectly stopped the worker: %+v", recovered)
	}
}

func TestArchiveMaintenanceProgressDuringBlockedPassAndClose(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce sync.Once
	s.mu.Lock()
	s.config.MinFreeBytes = 1
	s.spaceProbe = func(string) (archiveSpace, error) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return archiveSpace{bytes: 1 << 30, inodes: 1000}, nil
	}
	s.mu.Unlock()
	wakeMaintenance(s)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		_ = s.Close()
		t.Fatal("worker did not enter the filesystem probe")
	}
	progress := s.MaintenanceProgress()
	if !progress.InProgress || progress.LastResult != "never_started" || !progress.LastCompletedAt.IsZero() {
		close(release)
		_ = s.Close()
		t.Fatalf("blocked pass reported as complete: %+v", progress)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		close(release)
		t.Fatalf("Close returned before the pass finished: %v", err)
	default:
	}
	// Release the probe for both the worker and Close's final flush.
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not join the released maintenance worker")
	}
	if stopped := s.MaintenanceProgress(); !stopped.Stopped || stopped.InProgress || stopped.LastResult != "completed" {
		t.Fatalf("invalid stopped progress: %+v", stopped)
	}
}
