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

func TestMetroAdmissionRejectsUncertainExternalPublication(t *testing.T) {
	for _, publication := range []string{"events", "journey"} {
		for _, delivery := range []string{"standalone", "fifo"} {
			t.Run(publication+"/"+delivery, func(t *testing.T) {
				verifyMetroUncertainAdmission(t, publication, delivery)
			})
		}
	}
}

func verifyMetroUncertainAdmission(t *testing.T, publication, delivery string) {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(durabilityReceipt(base, "retained"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if !s.lastCheckpoint.Equal(base.Add(time.Second)) {
		t.Fatal("fixture did not establish a recent checkpoint")
	}
	record, lease := uncertainMetroRecorder(t, s, delivery)
	makeExternalPublicationUncertain(t, s, publication, base)
	assertUncertainMetroRejected(t, s, record, base)
	if lease != nil {
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err == nil {
		t.Fatal("uncertainty was hidden on close")
	}
	reopened, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.detailFence != nil || !reopened.MetroCut().Snapshot().Pending {
		t.Fatalf("reopen did not establish a safe restart cut: %v", reopened.detailFence)
	}
	rows, _ := detailForHour(t, reopened, base.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "retained")
}

func uncertainMetroRecorder(t *testing.T, s *Service, delivery string) (metroTestRecorder, *MetroFIFO) {
	t.Helper()
	if delivery == "fifo" {
		lease, err := s.AcquireMetroFIFO()
		if err != nil {
			t.Fatal(err)
		}
		return func(at time.Time, id string) (MetroRecordResult, error) {
			return lease.RecordMetro(durabilityReceipt(at, id), testTopology())
		}, lease
	}
	return func(at time.Time, id string) (MetroRecordResult, error) {
		return s.RecordMetro(durabilityReceipt(at, id), testTopology())
	}, nil
}

func makeExternalPublicationUncertain(t *testing.T, s *Service, publication string, base time.Time) {
	t.Helper()
	at := base.Add(time.Second)
	key := "metro-popup-events:" + at.Truncate(time.Hour).Format(time.RFC3339)
	event := MetroEventRecord{ID: "first", Journey: "journey", At: at, Payload: json.RawMessage(`{"proof":1}`)}
	journey := MetroJourneyCheckpoint{Journey: "journey", Revision: 1, SourceAt: at, Payload: json.RawMessage(`{"state":1}`)}
	var err error
	if publication == "events" {
		err = s.RecordMetroEvents([]MetroEventRecord{event}, at)
	} else {
		key = metroCheckpointKey(journey.Journey)
		_, err = s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{journey}, at, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	old := blockForKey(t, s, key)
	s.mu.Lock()
	s.manifestSync = func(string) error { return errors.New("injected manifest directory sync failure") }
	s.mu.Unlock()
	at = base.Add(2 * time.Second)
	if publication == "events" {
		event.ID, event.At = "second", at
		err = s.RecordMetroEvents([]MetroEventRecord{event}, at)
	} else {
		journey.Revision, journey.SourceAt = 2, at
		_, err = s.CommitMetroJourneysGuarded(context.Background(), []MetroJourneyCheckpoint{journey}, at, nil)
	}
	if err == nil || !s.manifestUncertain || !s.MetroCut().Snapshot().Pending {
		t.Fatal("fixture did not establish uncertain, fenced manifest", err)
	}
	current := blockForKey(t, s, key)
	if current.File == old.File {
		t.Fatal("fixture did not rename a replacement manifest")
	}
	for _, b := range []block{old, current} {
		if _, err := os.Stat(filepath.Join(s.config.Directory, b.File)); err != nil {
			t.Fatalf("uncertain generation missing: %v", err)
		}
	}
}

func assertUncertainMetroRejected(t *testing.T, s *Service, record metroTestRecorder, base time.Time) {
	t.Helper()
	beforeDetail, beforeEngine, beforeIndex := digest(s.detail), digest(s.engine), digest(s.index)
	beforeSample, beforeHour, beforeCheckpoint := s.lastSample, s.hour, s.lastCheckpoint
	for _, offset := range []time.Duration{5 * time.Second, 30 * time.Second} {
		result, err := record(base.Add(offset), "rejected")
		assertUncertainMetroResult(t, s, result, err)
		if digest(s.detail) != beforeDetail || digest(s.engine) != beforeEngine || digest(s.index) != beforeIndex {
			t.Fatal("rejected receipt changed retained history")
		}
		if !s.lastSample.Equal(beforeSample) || !s.hour.Equal(beforeHour) || !s.lastCheckpoint.Equal(beforeCheckpoint) {
			t.Fatal("rejected receipt changed sample state")
		}
	}
}

func assertUncertainMetroResult(t *testing.T, s *Service, result MetroRecordResult, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("uncertain input was accepted")
	}
	unexpected := []struct {
		field   string
		present bool
	}{
		{"appended", result.Appended}, {"acknowledged", result.Acknowledged},
		{"sampled", result.Kind != MetroUnsampled}, {"unmarked failure", !result.FailureMarked},
		{"valid cut", result.Cut.Valid}, {"ready cut", !s.MetroCut().Snapshot().Pending},
	}
	for _, field := range unexpected {
		if field.present {
			t.Errorf("uncertain input produced %s: %+v %v", field.field, result, err)
		}
	}
}

func blockForKey(t *testing.T, s *Service, key string) block {
	t.Helper()
	for _, b := range s.index.Blocks {
		if b.Key == key {
			return b
		}
	}
	t.Fatalf("missing block %s", key)
	return block{}
}

func TestMetroAdmissionRejectsCheckpointOutcomeUncertainty(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	s.checkpointUncertain = true
	s.mu.Unlock()
	result, err := s.RecordMetro(testReceipt(time.Now().UTC()), testTopology())
	if err == nil || result.Appended || !result.FailureMarked || !s.MetroCut().Snapshot().Pending {
		t.Fatalf("uncertain checkpoint admitted input: %+v %v", result, err)
	}
}
