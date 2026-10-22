package patterns

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMetroCheckpointWaitCancellationReleasesReader(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := s.MetroJourneyCheckpoint(ctx, "missing", time.Now().UTC()); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			s.mu.Unlock()
			t.Fatalf("cancelled read: %v", err)
		}
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("cancelled reader remained blocked on archive mutex")
	}
	s.mu.Unlock()
	if _, status, err := s.MetroJourneyCheckpoint(context.Background(), "missing", time.Now().UTC()); err != nil || status != "unavailable" {
		t.Fatalf("archive failed after cancellation: %q %v", status, err)
	}
}

func TestMetroCheckpointWaitDeadlineReleasesReader(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := s.MetroJourneyCheckpoint(ctx, "missing", time.Now().UTC()); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			s.mu.Unlock()
			t.Fatalf("deadline read: %v", err)
		}
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("deadline reader remained blocked on archive mutex")
	}
	s.mu.Unlock()
	if _, status, err := s.MetroJourneyCheckpoint(context.Background(), "missing", time.Now().UTC()); err != nil || status != "unavailable" {
		t.Fatalf("archive failed after deadline: %q %v", status, err)
	}
}
