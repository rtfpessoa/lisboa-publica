package app

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func TestMetroMemoryAdmissionBeforeDurabilityAndEventFreeRestart(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	s.Cache.metroRuntime.observe(data, d, s.Patterns, now)
	s.Cache.updateMetro(data, s.Cache.operator("metro"))
	before, _, err := s.metroFrame(context.Background(), metroInterest{Stop: "metro:gtfs-rm"})
	if err != nil || len(before.Trains) != 1 || before.Trains[0].Persistence == nil || before.Trains[0].Persistence.State != "pending" {
		t.Fatal("memory admission incorrectly depends on disk", before, err)
	}
	s.Cache.metroRuntime.flushCheckpoints(context.Background(), s.Patterns, now)
	// No provider publication follows the commit; frames read the coherent view.
	committed, _, err := s.metroFrame(context.Background(), metroInterest{Stop: "metro:gtfs-rm"})
	if err != nil || len(committed.Trains) != 1 || committed.Trains[0].Persistence == nil || committed.Trains[0].Persistence.State != "committed" {
		t.Fatal("commit was not visible", committed, err)
	}
	id := committed.Trains[0].JourneyId
	verifyMetroEventFreeRestart(t, s, id, now)
}
func verifyMetroEventFreeRestart(t *testing.T, s *Server, id string, now time.Time) {
	t.Helper()
	var err error
	s.Cache.metroRuntime = newMetroRuntime()
	restored, _, err := s.metroFrame(context.Background(), metroInterest{Journey: id})
	if err != nil || restored.SelectedJourneyId == nil || *restored.SelectedJourneyId != id || len(restored.Trains) != 1 || len(restored.Trains[0].Calls) != 2 || restored.Recovery == nil || restored.Recovery.Status != "historical" {
		t.Fatal("event-free journey lost", restored, err)
	}
	train := restored.Trains[0]
	if train.Association != "suspended" || train.NextIndex != nil || train.CurrentIndex != nil || train.VehicleId != nil || !train.SourceUpdatedAt.Equal(now) {
		t.Fatal("restart renewed continuity or clocks", train)
	}
	s.Cache.metroRuntime.mu.Lock()
	s.Cache.metroRuntime.queueCurrentCheckpoints()
	dirty := len(s.Cache.metroRuntime.dirty)
	s.Cache.metroRuntime.mu.Unlock()
	if dirty != 0 {
		t.Fatal("reading recovered history created a write")
	}
	// Once verified, a hot historical copy does not need another archive read.
	if err = s.Patterns.Close(); err != nil {
		t.Fatal(err)
	}
	again, _, err := s.metroFrame(context.Background(), metroInterest{Journey: id})
	if err != nil || again.SelectedJourneyId == nil || again.Recovery.Status != "historical" || !again.Trains[0].SourceUpdatedAt.Equal(now) {
		t.Fatal("hot history renewed clocks or reread closed archive", again, err)
	}
}

func TestMetroMissingPinHasExplicitRecoveryWithoutDanglingSelection(t *testing.T) {
	s, _, _, _ := metroLiveFixture(t)
	f, _, err := s.metroFrame(context.Background(), metroInterest{Journey: "metro:run:missing"})
	if err != nil || f.SelectedJourneyId != nil || f.Recovery == nil || f.Recovery.Status != "unavailable" || f.Recovery.RequestedJourneyId == nil {
		t.Fatal("missing pin hidden or dangling", f, err)
	}
	if err = s.Patterns.Close(); err != nil {
		t.Fatal(err)
	}
	again, _, err := s.metroFrame(context.Background(), metroInterest{Journey: "metro:run:missing"})
	if err != nil || again.SelectedJourneyId != nil || again.Recovery.Status != "unavailable" {
		t.Fatal("missing pin repeated archive scan", again, err)
	}
}

func TestMetroScopedContextPreservesContinuityAndVehicleLink(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	id := data.Trains[0].JourneyId
	// Synthetic qualified direction: this test checks scoped linking, not model admission.
	s.Cache.metroRuntime.tracks[id].Train.DirectionEvidence = &api.MetroDirectionEvidence{State: "confirmed", Reason: "synthetic qualified direction"}
	// An unmapped optional destination cannot override the classified candidate.
	extra := metroTestRow(now, "CS", "7", "null")
	extra.Destination = "unknown"
	data.Waits = append(data.Waits, extra)
	publishMetroTest(s, d, data, now)
	op := s.Cache.operator("metro")
	op.Status, op.LiveUpdatedAt = "ok", &now
	s.Cache.update("metro", nil, &LiveData{Collected: now, Vehicles: []api.Vehicle{{Id: "metro:7", SourceId: "7", OperatorId: "metro", RouteId: ptr("metro:r"), ObservedAt: now, CollectedAt: now, PositionKind: "estimated", Lat: 38.75, Lon: -9.14}}}, op)
	f, _, err := s.metroFrame(context.Background(), metroInterest{Vehicle: "metro:7"})
	if err != nil || f.SelectedJourneyId == nil || *f.SelectedJourneyId != id || len(f.Trains) != 1 || f.Trains[0].VehicleId == nil {
		t.Fatal("second raw matcher erased valid selection", f, err)
	}
}

func capturedMetroJSON(t *testing.T, name string, out any) {
	t.Helper()
	f, err := os.Open("testdata/metro-20260928/" + name + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil || json.Unmarshal(raw, out) != nil {
		t.Fatal("fixture decoding failed", name, err)
	}
}

func TestMetroCaptured24C5BClassifiedForecastsAndPermutations(t *testing.T) {
	var captures []struct {
		Data MetroData `json:"data"`
	}
	var static StaticData
	capturedMetroJSON(t, "metro-debug-direct-capture.json", &captures)
	capturedMetroJSON(t, "metro-debug-static.json", &static)
	if len(captures) == 0 || static.Schedule == nil {
		t.Fatal("missing real captured inputs")
	}
	data := captures[0].Data
	now := *data.Status.CheckedAt
	var baseline []api.MetroForecastContext
	for seed := int64(0); seed < 8; seed++ {
		input := data
		input.Waits = append([]MetroWait{}, data.Waits...)
		rand.New(rand.NewSource(seed)).Shuffle(len(input.Waits), func(i, j int) { input.Waits[i], input.Waits[j] = input.Waits[j], input.Waits[i] })
		r := newMetroRuntime()
		r.session = "permutation-control" // Hold episode identity fixed while permuting source rows.
		r.observe(&input, &static, nil, now)
		_, _, contexts, _ := r.view(now)
		if seed == 0 {
			baseline = contexts
		} else if !reflect.DeepEqual(baseline, contexts) {
			t.Fatal("row order changed classified forecasts")
		}
		for _, ref := range []string{"24C", "5B"} {
			calls, directions := 0, map[string]bool{}
			for _, c := range contexts {
				if c.Reference == ref && c.Status == "admissible" {
					calls += len(c.Calls)
					if c.DirectionCode != nil {
						directions[*c.DirectionCode] = true
					}
				}
			}
			if calls == 0 || len(directions) < 2 {
				t.Fatalf("captured %s forecasts were hidden: calls=%d directions=%v", ref, calls, directions)
			}
		}
		for _, track := range r.tracks {
			if strings.Contains(track.Train.Reason, "Referência contraditória") {
				t.Fatal("coexistence treated as reference contradiction")
			}
		}
	}
}

func TestMetroCheckpointCommitFailureKeepsForecasts(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	s.Cache.metroRuntime.observe(data, d, s.Patterns, now)
	if err := s.Patterns.Close(); err != nil {
		t.Fatal(err)
	}
	s.Cache.metroRuntime.flushCheckpoints(context.Background(), s.Patterns, now)
	s.Cache.updateMetro(data, s.Cache.operator("metro"))
	f, _, err := s.metroFrame(context.Background(), metroInterest{Stop: "metro:gtfs-rm"})
	if err != nil || len(f.Trains) != 1 || f.Trains[0].Persistence.State != "pending" || f.Trains[0].Calls[0].Arrival.Prediction == nil || f.HistoryStatus != "paused" {
		t.Fatal("failed admission hid source forecasts or selected identity", f, err)
	}
}

func TestMetroNoSourceClockRenewalOnRepeatedReceipt(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	initial := data.Trains[0]
	publishMetroTest(s, d, data, now.Add(time.Second))
	if !data.Trains[0].SourceUpdatedAt.Equal(initial.SourceUpdatedAt) || !data.Trains[0].ValidUntil.Equal(initial.ValidUntil) || data.Trains[0].Persistence.Revision != initial.Persistence.Revision {
		t.Fatal("receipt renewed source checkpoint", data.Trains)
	}
}

func TestMetroHotRecoveryHonorsOriginalAgeWithoutIngestion(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	id := data.Trains[0].JourneyId
	r := s.Cache.metroRuntime
	if _, ok := r.retained(id, now.AddDate(0, 0, 7)); !ok {
		t.Fatal("historical entry expired before its original-age boundary")
	}
	if _, ok := r.retained(id, now.AddDate(0, 0, 7).Add(time.Second)); ok {
		t.Fatal("hot history survived TTL while ingestion was stopped")
	}
	view, _, _, _ := r.view(now.AddDate(0, 0, 7).Add(time.Second))
	if len(view.Trains) != 0 {
		t.Fatal("expired active inventory bypassed hot history TTL")
	}
	if !r.tracks[id].Train.SourceUpdatedAt.Equal(now) {
		t.Fatal("history consultation renewed the source clock")
	}
}

func TestMetroPendingCheckpointAccountingIncludesEncodedMetadata(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	r := newMetroRuntime()
	r.archiveAvailable = true
	// Synthetic resource pressure, independent of production model qualification.
	for n := 0; n < 100; n++ {
		train := cloneMetroTrain(data.Trains[0])
		train.JourneyId = fmt.Sprintf("synthetic-capacity:%d", n)
		train.Reason = strings.Repeat("x", 90<<10)
		track := &metroTrack{Train: train, Points: map[string]metroPoint{}}
		r.tracks[train.JourneyId] = track
		r.queueCheckpoint(track)
	}
	if len(r.dirty) == 0 || len(r.dirty) == len(r.tracks) || r.dirtyBytes > 8<<20 || r.historyStatus != "paused" {
		t.Fatal("pending capacity was not enforced", len(r.dirty), r.dirtyBytes, r.historyStatus)
	}
	encoded := 0
	for _, value := range r.dirty {
		value.Generation, value.CommittedAt = strings.Repeat("f", 64), now
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > pendingMetroCheckpointBytes(value) {
			t.Fatal("metadata reserve did not bound encoded checkpoint", err)
		}
		encoded += len(raw)
	}
	if encoded > 8<<20 {
		t.Fatal("bounded queue produced an inadmissible archive batch")
	}
	r.flushCheckpoints(context.Background(), s.Patterns, now)
	if len(r.dirty) != 0 || r.dirtyBytes != 0 || r.historyStatus != "collecting" {
		t.Fatal("bounded batch could not commit", len(r.dirty), r.dirtyBytes, r.historyStatus)
	}
	// Replacing pending progress neither double-counts nor leaks its reservation.
	track := r.tracks["synthetic-capacity:0"]
	track.Train.Reason = "first revision"
	r.queueCheckpoint(track)
	track.Train.Reason = "replacement revision"
	r.queueCheckpoint(track)
	if r.dirtyBytes != pendingMetroCheckpointBytes(r.dirty[track.Train.JourneyId]) {
		t.Fatal("coalescing double-counted checkpoint metadata")
	}
	r.prune(now.AddDate(0, 0, 8))
	if len(r.dirty) != 0 || r.dirtyBytes != 0 {
		t.Fatal("pruning leaked pending capacity")
	}
}

func TestMetroStationForecastDeduplicationRemainsLineScoped(t *testing.T) {
	call := api.StopCall{Id: "first-line-call", StopId: "metro:interchange", Arrival: api.CallTime{Kind: "prediction", Prediction: &api.CallTimeEvidence{}}}
	train := api.MetroTrain{Reference: "shared-reference", RouteId: "metro:first", Destination: "Shared destination", Association: "supported", ValidUntil: time.Now().Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "confirmed", Reason: "synthetic qualified direction"}, Calls: []api.StopCall{call}}
	other := call
	other.Id = "second-line-call"
	b := &metroFrameBuilder{metroFrameRequest: metroFrameRequest{interest: metroInterest{Stop: call.StopId}}, frame: api.MetroLiveFrame{Trains: []api.MetroTrain{train}, ForecastContexts: &[]api.MetroForecastContext{}}, contexts: []api.MetroForecastContext{
		{Reference: train.Reference, RouteId: train.RouteId, Destination: train.Destination, Status: "admissible", Calls: []api.StopCall{call}},
		{Reference: train.Reference, RouteId: "metro:second", Destination: train.Destination, Status: "admissible", Calls: []api.StopCall{other}},
	}}
	b.stationForecasts()
	if len(b.frame.UnassociatedForecasts) != 1 || b.frame.UnassociatedForecasts[0].Id != other.Id {
		t.Fatal("a link on another line erased an unassociated forecast", b.frame.UnassociatedForecasts)
	}
}

func TestMetroVehicleIncompatibleContextHasSpecificReason(t *testing.T) {
	s, d, _, _ := metroLiveFixture(t)
	b := &metroFrameBuilder{metroFrameRequest: metroFrameRequest{server: s, interest: metroInterest{Vehicle: "metro:7"}}, static: d, frame: api.MetroLiveFrame{ForecastContexts: &[]api.MetroForecastContext{}, Vehicles: []api.Vehicle{{Id: "metro:7", SourceId: "7", RouteId: ptr("metro:r")}}}, contexts: []api.MetroForecastContext{{Reference: "7", RouteId: "metro:r", Status: "incompatible"}}}
	b.stationForecasts()
	if b.frame.AssociationReason == nil || *b.frame.AssociationReason != "Dados incompatíveis nesta direção" {
		t.Fatal("incompatibility was presented as missing data", b.frame.AssociationReason)
	}
}

func TestMetroRecoveryRejectsOversizedIdentityBeforeCaching(t *testing.T) {
	s, _, _, _ := metroLiveFixture(t)
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	id := "metro:" + strings.Repeat("x", 240)
	for _, path := range []string{"/api/v1/metro/live", "/api/v1/metro/live/stream"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path+"?journey_id="+id, nil))
		if response.Code != 400 {
			t.Fatalf("oversized journey accepted by %s: %d", path, response.Code)
		}
	}
	if len(s.Cache.metroRuntime.recoveryMisses) != 0 {
		t.Fatal("invalid identity grew the recovery cache")
	}
}
