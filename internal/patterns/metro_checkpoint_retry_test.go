package patterns

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointRetryRequiresExactTicketAndPredecessor(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	old := MetroJourneyCheckpoint{Journey: "retry", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"version":1}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{old}, now); err != nil {
		t.Fatal(err)
	}
	candidate := old
	candidate.Revision++
	candidate.Payload = json.RawMessage(`{"version":2}`)
	blocker := filepath.Join(s.config.Directory, ".manifest.tmp")
	if err := os.WriteFile(blocker, []byte("block manifest rename"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{candidate}, now.Add(time.Second), nil)
	if err == nil || !result.Retryable || result.Retry == nil || result.Generation != "" {
		t.Fatalf("pre-rename failure lacks retry proof: %+v %v", result, err)
	}
	current, state, err := s.MetroJourneyCheckpoint(context.Background(), old.Journey, now)
	if err != nil || state != "restored" || current.Revision != 1 {
		t.Fatal("pre-rename failure replaced predecessor", state, err)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	changed := candidate
	changed.Payload = json.RawMessage(`{"version":3}`)
	if _, err := s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{changed}, now.Add(2*time.Second), result.Retry); err == nil {
		t.Fatal("changed retry payload was accepted")
	}
	if _, err := s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{candidate}, now.Add(2*time.Second), nil); err == nil {
		t.Fatal("retry without its ticket was accepted")
	}
	committed, err := s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{candidate}, now.Add(3*time.Second), result.Retry)
	if err != nil || committed.Generation == "" || committed.Retryable {
		t.Fatalf("valid retry failed: %+v %v", committed, err)
	}
	current, state, err = s.MetroJourneyCheckpoint(context.Background(), old.Journey, now.Add(3*time.Second))
	if err != nil || state != "restored" || current.Revision != 2 || current.Generation != committed.Generation || !current.CommittedAt.Equal(now.Add(3*time.Second)) {
		t.Fatalf("retry did not use the fresh transaction clock: %+v %s %v", current, state, err)
	}
}

func TestCheckpointRetryRejectsCorruptPredecessor(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC()
	old := MetroJourneyCheckpoint{Journey: "retry-corrupt", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"old":true}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{old}, now); err != nil {
		t.Fatal(err)
	}
	candidate := old
	candidate.Revision++
	candidate.Payload = json.RawMessage(`{"new":true}`)
	blocker := filepath.Join(s.config.Directory, ".manifest.tmp")
	if err = os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{candidate}, now.Add(time.Second), nil)
	if err == nil || !result.Retryable {
		t.Fatal("missing pre-rename retry ticket")
	}
	if err = os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	for _, b := range s.index.Blocks {
		if b.Key == metroCheckpointKey(old.Journey) {
			if err = os.WriteFile(filepath.Join(s.config.Directory, b.File), []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err = s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{candidate}, now.Add(2*time.Second), result.Retry)
	if err == nil || result.Retryable || result.Generation != "" {
		t.Fatal("unverified predecessor permitted retry")
	}
}

func TestCheckpointPostRenameFailureRequiresRestart(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC()
	value := MetroJourneyCheckpoint{Journey: "uncertain", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"old":true}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{value}, now); err != nil {
		t.Fatal(err)
	}
	value.Revision++
	value.Payload = json.RawMessage(`{"new":true}`)
	s.mu.Lock()
	s.manifestSync = func(string) error { return errors.New("injected sync uncertainty") }
	s.mu.Unlock()
	result, err := s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{value}, now.Add(time.Second), nil)
	if err == nil || result.Retryable || result.Retry != nil || result.Generation != "" || !s.manifestUncertain || !s.MetroCut().Snapshot().Pending {
		t.Fatalf("post-rename uncertainty was treated as retryable: %+v %v", result, err)
	}
	if _, err = s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{value}, now.Add(2*time.Second), nil); err == nil {
		t.Fatal("uncertain owner accepted a second transaction")
	}
}

func TestAbandonPreRenameCheckpointReleasesOwner(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC()
	first := MetroJourneyCheckpoint{Journey: "abandoned", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"first":true}`)}
	blocker := filepath.Join(s.config.Directory, ".manifest.tmp")
	if err = os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{first}, now, nil)
	if err == nil || !result.Retryable || result.Retry == nil {
		t.Fatal("pre-rename failure did not return a ticket")
	}
	expired, err := s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{first}, now.AddDate(0, 0, 8), result.Retry)
	if err == nil || !expired.Retryable || expired.Retry != result.Retry {
		t.Fatal("expired batch lost its proven pre-rename ticket")
	}
	if err = s.AbandonMetroCheckpointAttempt(result.Retry); err != nil {
		t.Fatal(err)
	}
	if err = s.AbandonMetroCheckpointAttempt(result.Retry); err == nil {
		t.Fatal("stale ticket abandoned a later owner")
	}
	if err = os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	second := MetroJourneyCheckpoint{Journey: "new", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"new":true}`)}
	if _, err = s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{second}, now.Add(time.Second), nil); err != nil {
		t.Fatal(err)
	}
	_, state, err := s.MetroJourneyCheckpoint(context.Background(), first.Journey, now)
	if err != nil || state != "unavailable" {
		t.Fatal("abandoned checkpoint was published", state, err)
	}
}
