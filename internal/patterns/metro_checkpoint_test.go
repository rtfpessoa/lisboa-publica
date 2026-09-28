package patterns

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMetroCheckpointEventFreeRecoveryAndLatestRevision(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	value := MetroJourneyCheckpoint{Journey: "metro:run:synthetic", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"calls":[],"synthetic":true}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{value}, now); err != nil {
		t.Fatal(err)
	}
	value.Revision++
	value.Payload = json.RawMessage(`{"calls":["first"],"synthetic":true}`)
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{value}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{value}, now.Add(2*time.Second)); err == nil {
		t.Fatal("stale revision accepted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, state, err := s.MetroJourneyCheckpoint(context.Background(), value.Journey, now.Add(time.Minute))
	if err != nil || state != "restored" || out.Revision != 2 || string(out.Payload) != string(value.Payload) || !out.SourceAt.Equal(now) || !out.CommittedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("latest baseline did not survive: %+v %s %v", out, state, err)
	}
	if len(s.engine.Previous) != 0 || len(s.engine.Groups) != 0 {
		t.Fatal("checkpoint restored inference continuity")
	}
	_, state, err = s.MetroJourneyCheckpoint(context.Background(), value.Journey, now.Add(8*24*time.Hour))
	if err != nil || state != "expired" {
		t.Fatal("read renewed evidence lifetime", state, err)
	}
}

func TestMetroCheckpointAtomicPairCancellationAndCorruption(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	values := []MetroJourneyCheckpoint{
		{Journey: "old", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"successor":"new","synthetic":true}`)},
		{Journey: "new", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"predecessor":"old","synthetic":true}`)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.CommitMetroJourneys(ctx, values, now); err == nil {
		t.Fatal("cancelled transaction committed")
	}
	for _, v := range values {
		_, state, readErr := s.MetroJourneyCheckpoint(context.Background(), v.Journey, now)
		if state != "unavailable" || readErr != nil {
			t.Fatal("partial generation exposed", state, readErr)
		}
	}
	generation, err := s.CommitMetroJourneys(context.Background(), values, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		out, state, readErr := s.MetroJourneyCheckpoint(context.Background(), v.Journey, now)
		if state != "restored" || readErr != nil || out.Generation != generation {
			t.Fatal("pair generation mismatch", out, state, readErr)
		}
	}
	for _, b := range s.index.Blocks {
		if b.Key == metroCheckpointKey("old") {
			if err = os.WriteFile(filepath.Join(s.config.Directory, b.File), []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, state, err := s.MetroJourneyCheckpoint(context.Background(), "old", now)
	if state != "corrupt" || err == nil {
		t.Fatal("corrupt checkpoint trusted", state, err)
	}
}

func TestMetroCheckpointLimitsDoNotExposeBaseline(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	value := MetroJourneyCheckpoint{Journey: "oversize", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"value":"` + strings.Repeat("x", maxMetroCheckpointBytes) + `"}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{value}, now); err == nil {
		t.Fatal("oversize checkpoint admitted")
	}
	_, state, err := s.MetroJourneyCheckpoint(context.Background(), value.Journey, now)
	if state != "unavailable" || err != nil {
		t.Fatal("rejected baseline selectable", state, err)
	}
	value.Payload = json.RawMessage(`{"synthetic":true}`)
	values := make([]MetroJourneyCheckpoint, 1025)
	for i := range values {
		values[i] = value
	}
	if _, err = s.CommitMetroJourneys(context.Background(), values, now); err == nil {
		t.Fatal("count limit ignored")
	}
	// Reservation includes files, replacement generations and the manifest.
	s.config.LimitBytes = 4096
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{value}, now); err == nil {
		t.Fatal("shared storage cap ignored")
	}
	_, state, err = s.MetroJourneyCheckpoint(context.Background(), value.Journey, now)
	if state != "unavailable" || err != nil {
		t.Fatal("failed baseline exposed", state, err)
	}
}
