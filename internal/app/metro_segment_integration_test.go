package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lisboapublica/internal/patterns"
)

// A direct app publication and its journey evidence share the real archive
// with sampled Metro history across the legacy-to-segment rollover.
func TestMetroPublicationSurvivesSegmentRollover(t *testing.T) {
	s, static, data, _ := metroLiveFixture(t)
	config := patterns.DefaultConfig(t.TempDir())
	history, err := patterns.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	s.Patterns = history
	client := &MetroClient{Cache: s.Cache, History: history}
	boundary := time.Now().UTC().Truncate(24 * time.Hour).Add(-12 * time.Hour)
	first, second := boundary.Add(-30*time.Second), boundary.Add(time.Second)
	data.Waits = []MetroWait{metroTestRow(first, "RM", "7", "40"), metroTestRow(first, "CS", "7", "120")}
	publishMetroTest(s, static, data, first)
	if err = client.recordPatterns(data, static, first); err != nil {
		t.Fatal(err)
	}
	next := *data
	next.Waits = []MetroWait{metroTestRow(second, "RM", "7", "0"), metroTestRow(second, "CS", "7", "90")}
	publishMetroTest(s, static, &next, second)
	if len(next.Trains) != 1 || next.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("publication did not retain the supported arrival")
	}
	journey := next.Trains[0].JourneyId
	if err = client.recordPatterns(&next, static, second); err != nil {
		t.Fatal(err)
	}
	s.Cache.metroRuntime.flushEvents(history, second)
	if err = history.Close(); err != nil {
		t.Fatal(err)
	}
	assertMetroSegmentPublished(t, config.Directory, second)
	history, err = patterns.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	assertMetroSegmentJourneyReopened(t, history, journey, second)
	client = &MetroClient{History: history}
	if err = client.recordPatterns(&next, static, second.Add(30*time.Second)); err != nil {
		t.Fatalf("reopened segmented archive rejected a new sample: %v", err)
	}
	view, err := history.View(context.Background(), "")
	if err != nil || view.Status == "paused" || view.AsOf == nil || !view.AsOf.Equal(second.Add(30*time.Second)) {
		t.Fatalf("new sample did not clear the restart gap: %s %q %v", view.Status, view.Message, err)
	}
}

func assertMetroSegmentPublished(t *testing.T, directory string, sampled time.Time) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Blocks []struct {
			Kind    string    `json:"kind"`
			AsOf    time.Time `json:"as_of"`
			Samples int64     `json:"samples"`
		} `json:"blocks"`
	}
	if err = json.Unmarshal(raw, &index); err != nil {
		t.Fatal(err)
	}
	for _, block := range index.Blocks {
		if block.Kind == "metro-detail-segment" && block.AsOf.Equal(sampled) && block.Samples == 1 {
			return
		}
	}
	t.Fatal("new-hour sample did not publish a detail segment")
}

func assertMetroSegmentJourneyReopened(t *testing.T, history *patterns.Service, journey string, sampled time.Time) {
	t.Helper()
	checkpoint, status, err := history.MetroJourneyCheckpoint(context.Background(), journey, sampled)
	var payload metroCheckpointPayload
	if err != nil || status != "restored" || json.Unmarshal(checkpoint.Payload, &payload) != nil || len(payload.Train.Calls) == 0 || payload.Train.Calls[0].Arrival.Inferred == nil {
		t.Fatalf("journey checkpoint did not survive segment rollover: %s %v", status, err)
	}
	events, err := history.MetroJourneyEvents(context.Background(), journey, sampled)
	if err != nil || len(events) != 1 || !events[0].At.Equal(sampled) {
		t.Fatalf("journey event did not survive segment rollover: %d %v", len(events), err)
	}
	view, err := history.View(context.Background(), "")
	if err != nil || view.Status != "paused" || !history.MetroCut().Snapshot().Pending || view.AsOf == nil || !view.AsOf.Equal(sampled) {
		t.Fatalf("reopened archive did not preserve its restart gap: %s %q %v", view.Status, view.Message, err)
	}
}
