package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestMetroMissingEarlierForecastDoesNotInventPassage(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "90"), metroTestRow(now, "CS", "7", "150")}
	publishMetroTest(s, d, data, now)
	id := data.Trains[0].JourneyId
	omitted := *data
	omitted.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "CS", "7", "140")}
	publishMetroTest(s, d, &omitted, now.Add(time.Second))
	train := omitted.Trains[0]
	if train.JourneyId != id || train.NextIndex == nil || *train.NextIndex != 0 || train.Calls[0].Arrival.Prediction == nil || !train.Calls[0].Arrival.Prediction.SourceUpdatedAt.Equal(now) {
		t.Fatalf("missing row advanced or renewed a station: %+v", train)
	}
	reappeared := *data
	reappeared.Waits = []MetroWait{metroTestRow(now.Add(2*time.Second), "CS", "7", "130"), metroTestRow(now.Add(2*time.Second), "RM", "7", "80")}
	publishMetroTest(s, d, &reappeared, now.Add(2*time.Second))
	train = reappeared.Trains[0]
	if train.JourneyId != id || train.Association != "supported" || train.NextIndex == nil || *train.NextIndex != 0 || train.Calls[0].Arrival.Inferred != nil {
		t.Fatalf("reappearing row split episode or invented passage: %+v", train)
	}
}

func TestMetroMissingForecastCannotBridgeArrivalOrOutliveEstimate(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(s, d, data, now)
	omitted := *data
	omitted.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "CS", "7", "110")}
	publishMetroTest(s, d, &omitted, now.Add(time.Second))
	zero := *data
	zero.Waits = []MetroWait{metroTestRow(now.Add(2*time.Second), "RM", "7", "0"), metroTestRow(now.Add(2*time.Second), "CS", "7", "100")}
	publishMetroTest(s, d, &zero, now.Add(2*time.Second))
	if zero.Trains[0].Calls[0].Arrival.Inferred != nil {
		t.Fatal("missing source row was bridged into an arrival")
	}
	// Independently exercise expiry of the held estimate without relying on collection timing.
	track := &metroTrack{Codes: []string{"a", "b"}, Train: api.MetroTrain{Association: "supported", Calls: []api.StopCall{{}, {}}}}
	track.Train.NextIndex = ptr(1)
	old := &api.CallTimeEvidence{At: now.Add(time.Second), ValidUntil: ptr(now.Add(time.Minute))}
	normalizeMetroNextIndex(track, ptr(0), old, map[string]metroPoint{}, now.Add(2*time.Second))
	if track.Train.NextIndex == nil || *track.Train.NextIndex != 1 || track.Train.Calls[0].Arrival.Prediction != nil {
		t.Fatal("passed estimate was renewed")
	}
}

func metroPredecessorFixture(now time.Time) (*metroTrack, *metroTrack) {
	oldID, newID := "old", "new"
	makeTrack := func(id string) *metroTrack {
		return &metroTrack{Profile: "profile", ProviderDirection: "direction", Codes: []string{"A", "B"}, Train: api.MetroTrain{JourneyId: id, Reference: "7", RouteId: "line", DirectionCode: "direction", Destination: "B", SourceUpdatedAt: now, ValidUntil: now.Add(time.Minute), Calls: []api.StopCall{{Id: id + ":0", StopId: "stop-a", StopSequence: 0}, {Id: id + ":1", StopId: "stop-b", StopSequence: 1}}}}
	}
	old, next := makeTrack(oldID), makeTrack(newID)
	old.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "superseded", SuccessorJourneyId: &newID}
	next.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active", PredecessorJourneyId: &oldID}
	e := &api.MetroEventEvidence{At: now, WindowStart: now.Add(-time.Second), WindowEnd: now, Mode: "inferred_arrival", Persistence: "pending"}
	old.Train.Calls[0].Arrival = api.CallTime{Kind: "inferred", At: &e.At, Inferred: e}
	old.Train.Calls[1].Arrival = api.CallTime{Kind: "prediction", At: ptr(now.Add(time.Minute)), Prediction: &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: ptr(now.Add(time.Minute))}}
	return old, next
}

func TestMetroPredecessorFullSequenceAndCorrections(t *testing.T) {
	now := time.Now().UTC()
	old, next := metroPredecessorFixture(now)
	r := newMetroRuntime()
	r.tracks["old"], r.tracks["new"] = old, next
	s := &Server{Cache: NewCache()}
	s.Cache.metroRuntime = r
	build := func() *metroFrameBuilder {
		return &metroFrameBuilder{metroFrameRequest: metroFrameRequest{server: s}, now: now, frame: api.MetroLiveFrame{SelectedJourneyId: ptr("new"), Trains: []api.MetroTrain{cloneMetroTrain(next.Train)}}}
	}
	b := build()
	b.projectPredecessor()
	if b.frame.Predecessor == nil || b.frame.Predecessor.Status != "available" || b.frame.Predecessor.Train == nil {
		t.Fatalf("missing predecessor: %+v", b.frame.Predecessor)
	}
	calls := b.frame.Predecessor.Train.Calls
	if len(calls) != 2 || calls[0].Arrival.Inferred == nil || calls[1].Arrival.Prediction != nil || calls[1].Phase != "unknown" || calls[1].StopSequence != 1 || next.Train.Calls[0].Arrival.Inferred != nil {
		t.Fatalf("history leaked into selected episode: %+v", calls)
	}
	old.Train.Calls[0].Arrival = missingCallTime("arrival retracted")
	old.Train.Calls[0].Departure = missingCallTime("departure retracted")
	old.UncommittedRetraction = true
	b = build()
	b.projectPredecessor()
	if call := b.frame.Predecessor.Train.Calls[0]; call.Arrival.Inferred != nil || call.Arrival.Reason != "arrival retracted" || call.Departure.Reason != "departure retracted" {
		t.Fatalf("correction was not reflected: %+v", call)
	}
	r.evictRetired()
	if r.tracks["old"] == nil {
		t.Fatal("uncommitted retraction was evicted")
	}
	next.Codes = []string{"B", "A"}
	b = build()
	b.projectPredecessor()
	if b.frame.Predecessor.Train != nil {
		t.Fatal("different path borrowed predecessor history")
	}
}

func TestMetroPredecessorCheckpointLookupAndInvalidation(t *testing.T) {
	now := time.Now().UTC()
	old, next := metroPredecessorFixture(now)
	old.Train.Calls[1].Arrival = missingCallTime("arrival retracted")
	old.Train.Calls[1].Departure = missingCallTime("departure retracted")
	archive, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	payload, err := json.Marshal(metroCheckpointPayload{Version: 1, Train: old.Train, Profile: old.Profile, ProviderDirection: old.ProviderDirection, Codes: old.Codes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.CommitMetroJourneys(context.Background(), []patterns.MetroJourneyCheckpoint{{Journey: "old", Revision: 1, SourceAt: now, Payload: payload}}, now); err != nil {
		t.Fatal(err)
	}
	r := newMetroRuntime()
	r.tracks["new"] = next
	key := metroPredecessorKey{"new", "old", "profile"}
	entry := r.predecessorCacheEntry(key, now)
	entry.inflight, entry.started, r.predecessorWorkers = true, now, 1
	r.readMetroPredecessor(context.Background(), key, entry, entry.generation, archive.MetroJourneyCheckpoint)
	if entry.payload == nil || entry.status != "available" || r.predecessorWorkers != 0 {
		t.Fatalf("checkpoint unavailable: %+v", entry)
	}
	s := &Server{Cache: NewCache(), Patterns: archive}
	s.Cache.metroRuntime = r
	b := &metroFrameBuilder{metroFrameRequest: metroFrameRequest{server: s}, now: now, frame: api.MetroLiveFrame{SelectedJourneyId: ptr("new"), Trains: []api.MetroTrain{cloneMetroTrain(next.Train)}}}
	b.projectPredecessor()
	if b.frame.Predecessor == nil || b.frame.Predecessor.Train == nil || b.frame.Predecessor.Train.Persistence.State != "committed" {
		t.Fatalf("restored predecessor missing: %+v", b.frame.Predecessor)
	}
	if call := b.frame.Predecessor.Train.Calls[1]; call.Arrival.Reason != "arrival retracted" || call.Departure.Reason != "departure retracted" {
		t.Fatalf("restored correction reasons lost: %+v", call)
	}
	if b.frame.Predecessor.Train.Calls[0].Arrival.Inferred.Persistence != "committed" {
		t.Fatal("restored evidence not marked committed")
	}
	r.invalidateMetroPredecessor("old")
	if entry.payload != nil {
		t.Fatal("newer evidence did not invalidate cached predecessor")
	}
}

func TestMetroPredecessorBlockedWorkDoesNotPublishInvalidatedResult(t *testing.T) {
	now := time.Now().UTC()
	r := newMetroRuntime()
	key := metroPredecessorKey{"new", "old", "profile"}
	entry := r.predecessorCacheEntry(key, now)
	entry.inflight, entry.started, r.predecessorWorkers = true, now, 1
	old, _ := metroPredecessorFixture(now)
	raw, err := json.Marshal(metroCheckpointPayload{Version: 1, Train: old.Train, Profile: old.Profile, ProviderDirection: old.ProviderDirection, Codes: old.Codes})
	if err != nil {
		t.Fatal(err)
	}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	lookup := func(context.Context, string, time.Time) (patterns.MetroJourneyCheckpoint, string, error) {
		close(started)
		<-release
		return patterns.MetroJourneyCheckpoint{Journey: "old", SourceAt: now, Payload: raw}, "restored", nil
	}
	go func() { r.readMetroPredecessor(context.Background(), key, entry, 0, lookup); close(done) }()
	<-started
	r.mu.Lock()
	r.invalidateMetroPredecessor("old")
	if r.predecessorWorkers != 1 || !entry.inflight {
		t.Fatal("blocked worker lost its slot")
	}
	r.mu.Unlock()
	close(release)
	<-done
	if entry.inflight || r.predecessorWorkers != 0 || entry.payload != nil || !entry.retry.IsZero() {
		t.Fatalf("stale result changed cache: %+v", entry)
	}
}

func TestMetroPredecessorLookupBackoffAndCacheBound(t *testing.T) {
	now := time.Now().UTC()
	r := newMetroRuntime()
	key := metroPredecessorKey{"new", "old", "profile"}
	entry := r.predecessorCacheEntry(key, now)
	lookup := func(context.Context, string, time.Time) (patterns.MetroJourneyCheckpoint, string, error) {
		return patterns.MetroJourneyCheckpoint{}, "unavailable", errors.New("synthetic transient failure")
	}
	for _, want := range []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, time.Minute} {
		started := time.Now().UTC()
		entry.inflight, entry.started, r.predecessorWorkers = true, started, 1
		r.readMetroPredecessor(context.Background(), key, entry, entry.generation, lookup)
		if got := entry.retry.Sub(started); got < want || got > want+time.Second {
			t.Fatalf("retry %v, want %v", got, want)
		}
	}
	for n := 0; n < metroPredecessorCacheLimit*2; n++ {
		r.predecessorCacheEntry(metroPredecessorKey{selected: string(rune(n + 100)), previous: "old", profile: "profile"}, now.Add(time.Duration(n)*time.Second))
	}
	if len(r.predecessorCache) != metroPredecessorCacheLimit {
		t.Fatalf("cache unbounded: %d", len(r.predecessorCache))
	}
}

func TestMetroPredecessorRejectsExpiredAndAmbiguousPaths(t *testing.T) {
	now := time.Now().UTC()
	old, next := metroPredecessorFixture(now)
	if !metroCompatiblePredecessor(next, old, now) {
		t.Fatal("valid predecessor was rejected")
	}
	for _, test := range []struct {
		name  string
		alter func(*metroTrack, *metroTrack)
	}{
		{"expired", func(o, _ *metroTrack) { o.Train.SourceUpdatedAt = now.AddDate(0, 0, -7).Add(-time.Second) }},
		{"reverse", func(o, _ *metroTrack) { o.ProviderDirection = "reverse" }},
		{"duplicate visit", func(o, n *metroTrack) { o.Codes[1], n.Codes[1] = "A", "A" }},
		{"reference reuse", func(o, _ *metroTrack) { o.Train.Reference = "other" }},
		{"unconfirmed link", func(o, _ *metroTrack) { o.Train.Lifecycle.SuccessorJourneyId = nil }},
		{"short turn", func(o, _ *metroTrack) { o.Codes = o.Codes[:1]; o.Train.Calls = o.Train.Calls[:1] }},
		{"profile", func(o, _ *metroTrack) { o.Profile = "new-plan" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			o, n := metroPredecessorFixture(now)
			test.alter(o, n)
			if metroCompatiblePredecessor(n, o, now) {
				t.Fatal("ambiguous predecessor accepted")
			}
		})
	}
}

func TestMetroNewerMissingWaitDoesNotRestoreEarlierEstimate(t *testing.T) {
	for _, value := range []string{"null", "\"0\"", "-1"} {
		t.Run(value, func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "90"), metroTestRow(now, "CS", "7", "150")}
			publishMetroTest(s, d, data, now)
			next := *data
			next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", value), metroTestRow(now.Add(time.Second), "CS", "7", "140")}
			publishMetroTest(s, d, &next, now.Add(time.Second))
			if len(next.Trains) != 1 || next.Trains[0].Calls[0].Arrival.Prediction != nil {
				t.Fatalf("newer missing wait revived an older estimate: %+v", next.Trains)
			}
		})
	}
}

func TestMetroPredecessorSelectionCancellationAllowsRetry(t *testing.T) {
	now := time.Now().UTC()
	r := newMetroRuntime()
	key := metroPredecessorKey{"new", "old", "profile"}
	entry := r.predecessorCacheEntry(key, now)
	entry.inflight, entry.started, r.predecessorWorkers = true, now, 1
	parent, cancel := context.WithCancel(context.Background())
	started, done := make(chan struct{}), make(chan struct{})
	lookup := func(ctx context.Context, _ string, _ time.Time) (patterns.MetroJourneyCheckpoint, string, error) {
		close(started)
		<-ctx.Done()
		return patterns.MetroJourneyCheckpoint{}, "unavailable", ctx.Err()
	}
	go func() { r.readMetroPredecessor(parent, key, entry, 0, lookup); close(done) }()
	<-started
	cancel() // The old stream closes when its selection changes.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("selection change left predecessor lookup running")
	}
	if r.predecessorWorkers != 0 || entry.inflight || !entry.retry.IsZero() {
		t.Fatalf("another selection cannot retry: %+v", entry)
	}
}

func TestMetroPredecessorOccurrenceUsesItsOwnEvidenceClock(t *testing.T) {
	now := time.Now().UTC()
	evidence := &api.MetroEventEvidence{At: now, WindowStart: now.Add(-time.Second), WindowEnd: now, Mode: "inferred_arrival"}
	value := api.CallTime{Kind: "prediction", At: ptr(now.Add(time.Minute)), Inferred: evidence, Prediction: &api.CallTimeEvidence{At: now.Add(time.Minute)}}
	got := predecessorCallTime(value)
	if got.Kind != "inferred" || got.At == nil || !got.At.Equal(now) || got.Prediction != nil {
		t.Fatalf("occurrence inherited the forecast clock: %+v", got)
	}
}
