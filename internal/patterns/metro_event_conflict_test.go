package patterns

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMetroEventRetryNeverOverwritesConflictingEvidence(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC()
	event := MetroEventRecord{ID: "proof", Journey: "journey", At: now, Payload: json.RawMessage(`{"version":1}`)}
	noncanonical := event
	noncanonical.Payload = json.RawMessage(`{"version": 1}`)
	if err = s.RecordMetroEvents([]MetroEventRecord{noncanonical}, now); err == nil {
		t.Fatal("payload that cannot be retained byte-for-byte was accepted")
	}
	if err = s.RecordMetroEvents([]MetroEventRecord{event}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordMetroEvents([]MetroEventRecord{event}, now.Add(time.Second)); err != nil {
		t.Fatal("identical retry rejected", err)
	}
	before := digest(s.index)
	changed := event
	changed.Payload = json.RawMessage(`{"version":2}`)
	if err = s.RecordMetroEvents([]MetroEventRecord{changed}, now.Add(2*time.Second)); err == nil {
		t.Fatal("conflicting retained proof overwritten")
	}
	if digest(s.index) != before {
		t.Fatal("conflict changed archive manifest")
	}
	later := event
	later.At = now.Add(time.Hour)
	if err = s.RecordMetroEvents([]MetroEventRecord{event, later}, now); err == nil {
		t.Fatal("conflicting duplicate across hour groups accepted")
	}
	rows, err := s.MetroJourneyEvents(context.Background(), event.Journey, now)
	if err != nil || len(rows) != 1 || string(rows[0].Payload) != string(event.Payload) {
		t.Fatalf("original proof changed: %+v %v", rows, err)
	}
}

func TestMetroEventPartialHoursRetryIdentically(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC()
	values := []MetroEventRecord{
		{ID: "first", Journey: "journey", At: now.Add(-time.Hour), Payload: json.RawMessage(`{"hour":1}`)},
		{ID: "second", Journey: "journey", At: now, Payload: json.RawMessage(`{"hour":2}`)},
	}
	blocker := filepath.Join(s.config.Directory, ".manifest.tmp")
	s.mu.Lock()
	injected := false
	s.manifestSync = func(path string) error {
		if !injected {
			injected = true
			if err := os.WriteFile(blocker, []byte("block next manifest"), 0600); err != nil {
				return err
			}
		}
		return syncDirectory(path)
	}
	s.mu.Unlock()
	if err = s.RecordMetroEvents(values, now); err == nil || !injected {
		t.Fatal("partial event publication was not injected")
	}
	partial, err := s.MetroJourneyEvents(context.Background(), "journey", now)
	if err != nil || len(partial) != 1 {
		t.Fatalf("first committed hour missing: %+v %v", partial, err)
	}
	if err = os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.manifestSync = nil
	s.mu.Unlock()
	if err = s.RecordMetroEvents(values, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	retained, err := s.MetroJourneyEvents(context.Background(), "journey", now.Add(time.Second))
	if err != nil || len(retained) != 2 {
		t.Fatalf("partial retry lost or duplicated evidence: %+v %v", retained, err)
	}
	seen := map[string]bool{}
	for _, event := range retained {
		if seen[event.ID] {
			t.Fatal("event retry duplicated a proof")
		}
		seen[event.ID] = true
	}
}
