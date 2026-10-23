package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// The destination edit is synthetic; the wait rows retain their original clocks.
func TestMetroDestinationMetadataRefreshRevisesLiveStream(t *testing.T) {
	s, static, template, now := metroLiveFixture(t)
	client := &MetroClient{}
	client.metadataStations = append([]MetroStation(nil), template.Stations...)
	client.metadataDestinations = []MetroDestination{{ID: "54", Name: "Cais do Sodré"}}
	client.metadataAt = now
	second, evidence := metroDestinationBaseline(t, s, static, client, now)

	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live/stream?stop_id=metro:gtfs-cs", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	initial := readUnavailableTopologyStream(t, scanner, "reset")
	if len(initial.Trains) != 1 || initial.Trains[0].Association != "supported" {
		t.Fatal("initial stream omitted the supported train")
	}

	client.metadataMu.Lock()
	client.metadataDestinations = []MetroDestination{{ID: "changed", Name: "Cais do Sodré"}}
	client.metadataMu.Unlock()
	changed := client.metroMetadataSnapshot(now.Add(2 * time.Second))
	changed.Waits = append([]MetroWait(nil), second.Waits...)
	publishMetroTest(s, static, changed, now.Add(2*time.Second))
	freshTopology, freshUnmapped := metroTopologyReport(changed, static)
	if !reflect.DeepEqual(changed.Topology, freshTopology) || !slices.Equal(s.Cache.metroRuntime.unmappedStaticStops(), freshUnmapped) {
		t.Fatal("metadata snapshot did not reach runtime topology")
	}
	updated := readUnavailableTopologyStream(t, scanner, "frame")
	if updated.Revision == initial.Revision {
		t.Fatal("destination-only change did not revise the live frame")
	}
	if len(updated.Directions) != 1 || textValue(updated.Directions[0].DirectionKey) != "changed" {
		t.Fatal("live station directions did not reflect the new topology")
	}
	for _, train := range updated.Trains {
		if train.Association == "supported" {
			t.Fatal("obsolete destination topology retained a supported association")
		}
	}
	assertMetroDestinationRetained(t, s, evidence)
	if len(updated.UnassociatedForecasts) != 1 || updated.UnassociatedForecasts[0].JourneyId != nil || updated.UnassociatedForecasts[0].Arrival.Prediction == nil || updated.UnassociatedForecasts[0].Arrival.Prediction.SourceUpdatedAt == nil || !updated.UnassociatedForecasts[0].Arrival.Prediction.SourceUpdatedAt.Equal(now.Add(time.Second)) || !updated.UnassociatedForecasts[0].Arrival.Prediction.At.Equal(now.Add(120*time.Second)) {
		t.Fatal("metadata refresh discarded the independently published official forecast")
	}

	assertMetroStreamRevisionHTTP(t, ctx, server, updated.Revision)
}

func metroDestinationBaseline(t *testing.T, s *Server, static *StaticData, client *MetroClient, now time.Time) (*MetroData, metroDestinationEvidence) {
	t.Helper()
	first := client.metroMetadataSnapshot(now)
	first.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(s, static, first, now)
	if len(first.Trains) != 1 || first.Trains[0].Association != "supported" {
		t.Fatal("synthetic baseline did not admit a supported train")
	}
	id := first.Trains[0].JourneyId
	second := client.metroMetadataSnapshot(now.Add(time.Second))
	second.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "0"), metroTestRow(now.Add(time.Second), "CS", "7", "119")}
	publishMetroTest(s, static, second, now.Add(time.Second))
	if len(second.Trains) != 1 || second.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("synthetic prior station passage was not retained")
	}
	passage := *second.Trains[0].Calls[0].Arrival.Inferred
	pending := metroPendingSnapshot(s.Cache.metroRuntime)
	if len(pending) == 0 {
		t.Fatal("baseline did not retain the passage proof")
	}
	before, state, err := s.Patterns.MetroJourneyCheckpoint(context.Background(), id, now.Add(time.Second))
	if err != nil || state != "restored" {
		t.Fatal("baseline checkpoint unavailable", state, err)
	}
	var baseline metroCheckpointPayload
	if err := json.Unmarshal(before.Payload, &baseline); err != nil || len(baseline.Train.Calls) == 0 || baseline.Train.Calls[0].Arrival.Inferred == nil {
		t.Fatal("baseline checkpoint omitted passage evidence", err)
	}
	return second, metroDestinationEvidence{id, now, passage, before, baseline, pending}
}

func assertMetroStreamRevisionHTTP(t *testing.T, ctx context.Context, server *httptest.Server, revision string) {
	t.Helper()
	get, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live?stop_id=metro:gtfs-cs", nil)
	if err != nil {
		t.Fatal(err)
	}
	read, err := server.Client().Do(get)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Body.Close()
	var frame api.MetroLiveFrame
	if read.StatusCode != http.StatusOK || json.NewDecoder(read.Body).Decode(&frame) != nil || frame.Revision != revision {
		t.Fatal("public frame and stream revisions disagree")
	}
}

type metroDestinationEvidence struct {
	id       string
	at       time.Time
	passage  api.MetroEventEvidence
	before   patterns.MetroJourneyCheckpoint
	baseline metroCheckpointPayload
	pending  map[string][]byte
}

func metroPendingSnapshot(r *metroRuntime) map[string][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := make(map[string][]byte, len(r.pending))
	for key, event := range r.pending {
		pending[key] = append([]byte(nil), event.Payload...)
	}
	return pending
}

func assertMetroDestinationRetained(t *testing.T, s *Server, e metroDestinationEvidence) {
	t.Helper()
	retained, ok := s.Cache.metroRuntime.retained(e.id, e.at.Add(2*time.Second))
	if !ok || retained.Calls[0].Arrival.Inferred == nil || !reflect.DeepEqual(*retained.Calls[0].Arrival.Inferred, e.passage) {
		t.Fatal("metadata refresh lost the prior station passage")
	}
	after, state, err := s.Patterns.MetroJourneyCheckpoint(context.Background(), e.id, e.at.Add(2*time.Second))
	if err != nil || state != "restored" || len(e.before.Payload) == 0 || len(after.Payload) == 0 {
		t.Fatal("metadata refresh lost the retained checkpoint", state, err)
	}
	var payload metroCheckpointPayload
	if err := json.Unmarshal(after.Payload, &payload); err != nil || len(payload.Train.Calls) == 0 || payload.Train.Calls[0].Arrival.Inferred == nil {
		t.Fatal("metadata refresh lost checkpoint passage evidence", err)
	}
	got := payload.Train.Calls[0].Arrival.Inferred
	if !got.At.Equal(e.passage.At) || !got.WindowStart.Equal(e.passage.WindowStart) || !got.WindowEnd.Equal(e.passage.WindowEnd) || got.Mode != e.passage.Mode || got.ModelVersion != e.passage.ModelVersion {
		t.Fatalf("metadata refresh altered checkpoint passage evidence: got %+v, want %+v", got, e.passage)
	}
	if !reflect.DeepEqual(got, e.baseline.Train.Calls[0].Arrival.Inferred) {
		t.Fatal("metadata refresh changed retained checkpoint passage evidence")
	}
	if !reflect.DeepEqual(metroPendingSnapshot(s.Cache.metroRuntime), e.pending) {
		t.Fatal("metadata refresh changed or invented a passage proof")
	}
}

func TestMetroDestinationRefreshWaitFailureDoesNotAdmitFromHub(t *testing.T) {
	s, static, template, now := metroLiveFixture(t)
	static.Schedule.Trips[0].Direction = ptr(0)
	static.Schedule.Trips[0].Shape = "synthetic-shape"
	static.Shapes = []api.RouteShape{{ShapeId: "synthetic-shape", RouteId: "metro:r", Geometry: [][]float64{{-9.14, 38.75}, {-9.14, 38.71}}}}
	client := &MetroClient{}
	client.metadataStations = append([]MetroStation(nil), template.Stations...)
	client.metadataDestinations = []MetroDestination{{ID: "54", Name: "Cais do Sodré"}}
	client.metadataAt = now
	makeWaits := func(at time.Time, first string) []MetroWait {
		unknown := metroTestRow(at, "RM", "7", "10")
		unknown.Destination = "77"
		return []MetroWait{metroTestRow(at, "RM", "9", first), metroTestRow(at, "CS", "9", "120"), unknown}
	}
	first := client.metroMetadataSnapshot(now)
	first.Waits = makeWaits(now, "10")
	publishMetroTest(s, static, first, now)
	second := client.metroMetadataSnapshot(now.Add(time.Second))
	second.Waits = makeWaits(now.Add(time.Second), "0")
	publishMetroTest(s, static, second, now.Add(time.Second))
	if len(second.Trains) != 1 || second.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("synthetic baseline did not retain a station passage")
	}
	id := second.Trains[0].JourneyId
	passage := *second.Trains[0].Calls[0].Arrival.Inferred
	r := s.Cache.metroRuntime
	r.flushEvents(s.Patterns, now.Add(time.Second))
	r.flushCheckpoints(context.Background(), s.Patterns, now.Add(time.Second))
	baseline, ok := r.retained(id, now.Add(time.Second))
	if !ok || baseline.Calls[0].Arrival.Inferred == nil {
		t.Fatal("baseline passage was not retained after commit")
	}
	passage = *baseline.Calls[0].Arrival.Inferred
	events, err := s.Patterns.MetroJourneyEvents(context.Background(), id, now.Add(time.Second))
	if err != nil || len(events) != 1 {
		t.Fatal("baseline passage proof was not committed", err)
	}
	beforeProfile := second.Topology.Profile
	beforeSequence := r.sequence

	client.metadataMu.Lock()
	client.metadataDestinations = []MetroDestination{{ID: "77", Name: "Cais do Sodré"}}
	client.metadataMu.Unlock()
	failed := client.metroMetadataSnapshot(now.Add(2 * time.Second))
	completeMetroFetch(failed, second, nil, errors.New("synthetic wait failure"))
	publishMetroTest(s, static, failed, now.Add(2*time.Second))
	if !reflect.DeepEqual(failed.Waits, second.Waits) || failed.Status.Status != "error" {
		t.Fatal("fixture did not retain the original waits behind an error")
	}
	positionAt := now.Add(3 * time.Second)
	position := hubPosition{Agency: "IA2N9", ID: "[IA2N9]7", Route: "[IA2N9]r", Trip: "[synthetic][IA2N9]one", Shape: "[synthetic][IA2N9]synthetic-shape", Direction: json.RawMessage(`0`), Lat: 38.73, Lon: -9.14, At: positionAt.UnixMilli()}
	assertMetroHubWithFailedWait(t, s, position, metroFailedHubEvidence{id, passage, events, beforeProfile, beforeSequence})

	recoveredAt := now.Add(4 * time.Second)
	recovered := client.metroMetadataSnapshot(recoveredAt)
	row := metroTestRow(recoveredAt, "RM", "7", "10")
	row.Destination = "77"
	recovered.Waits = []MetroWait{row}
	publishMetroTest(s, static, recovered, recoveredAt)
	var fresh *api.MetroTrain
	for n := range recovered.Trains {
		if recovered.Trains[n].Reference == "7" && recovered.Trains[n].Association == "supported" {
			fresh = &recovered.Trains[n]
		}
	}
	if fresh == nil || fresh.JourneyId == id || !fresh.SourceUpdatedAt.Equal(recoveredAt) || fresh.Calls[0].Arrival.Inferred != nil || recovered.Topology.Profile == beforeProfile || r.sequence != beforeSequence+1 {
		t.Fatal("fresh direct recovery did not create only a newly supported episode")
	}
	retained, ok := r.retained(id, recoveredAt)
	if !ok || retained.Calls[0].Arrival.Inferred == nil || !reflect.DeepEqual(*retained.Calls[0].Arrival.Inferred, passage) {
		t.Fatal("recovery lost the previously retained passage")
	}
}

type metroFailedHubEvidence struct {
	id       string
	passage  api.MetroEventEvidence
	events   []patterns.MetroEventRecord
	profile  string
	sequence uint64
}

func assertMetroHubWithFailedWait(t *testing.T, s *Server, position hubPosition, e metroFailedHubEvidence) {
	t.Helper()
	r := s.Cache.metroRuntime
	positionAt := time.UnixMilli(position.At).UTC()
	r.observeHub([]hubPosition{position}, positionAt)
	r.mu.Lock()
	motion := r.operational["metro:r|7"]
	if r.sequence != e.sequence || len(r.tracks) != 1 || r.topology.Profile != e.profile || len(r.pending) != 0 {
		r.mu.Unlock()
		t.Fatal("Hub update admitted a journey or proof from failed retained waits")
	}
	if motion == nil || !motion.PublishedAt.Equal(positionAt) || len(r.capture.Queue) == 0 {
		r.mu.Unlock()
		t.Fatal("valid independent Hub observation was not retained")
	}
	var capture metroCapturedInput
	if err := json.Unmarshal(r.capture.Queue[len(r.capture.Queue)-1].Payload, &capture); err != nil || capture.WaitAvailability != "error" || len(capture.Models) != 1 || capture.Models[0].Position.At != position.At {
		r.mu.Unlock()
		t.Fatal("Hub observation was not captured with failed-wait provenance", err)
	}
	r.mu.Unlock()
	r.flushCheckpoints(context.Background(), s.Patterns, positionAt)
	retained, ok := r.retained(e.id, positionAt)
	if !ok || retained.Association != "suspended" || retained.Calls[0].Arrival.Inferred == nil || !reflect.DeepEqual(*retained.Calls[0].Arrival.Inferred, e.passage) {
		t.Fatal("failed waits or Hub processing changed the prior passage")
	}
	afterEvents, err := s.Patterns.MetroJourneyEvents(context.Background(), e.id, positionAt)
	if err != nil || !reflect.DeepEqual(e.events, afterEvents) {
		t.Fatal("failed waits or Hub processing changed the committed passage proof", err)
	}
	checkpoint, state, err := s.Patterns.MetroJourneyCheckpoint(context.Background(), e.id, positionAt)
	var checkpointPayload metroCheckpointPayload
	if err != nil || state != "restored" || json.Unmarshal(checkpoint.Payload, &checkpointPayload) != nil || checkpointPayload.Train.Calls[0].Arrival.Inferred == nil || !checkpointPayload.Train.Calls[0].Arrival.Inferred.At.Equal(e.passage.At) {
		t.Fatal("failed waits or Hub processing lost the previous checkpoint", state, err)
	}
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/metro/live", nil))
	var frame api.MetroLiveFrame
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &frame) != nil || frame.Status.Status != "error" || len(frame.Trains) != 1 || frame.Trains[0].JourneyId != e.id || frame.Trains[0].Association != "suspended" {
		t.Fatal("failed-wait frame exposed an invented current episode")
	}
}

func TestMetroHubDoesNotReclassifyUnavailableDirectTopology(t *testing.T) {
	for _, missing := range []string{"static", "schedule"} {
		t.Run(missing, func(t *testing.T) {
			_, static, data, now := metroLiveFixture(t)
			r := newMetroRuntime()
			data.Destinations = []MetroDestination{{ID: "54", Name: "Cais do Sodré"}}
			row := metroTestRow(now, "RM", "7", "10")
			row.Destination = "77"
			data.Waits = []MetroWait{row}
			r.observe(data, static, nil, now)
			profile := r.topology.Profile
			unavailable := *data
			unavailable.Destinations = []MetroDestination{{ID: "77", Name: "Cais do Sodré"}}
			var plan *StaticData
			if missing == "schedule" {
				copy := *static
				copy.Schedule = nil
				plan = &copy
			}
			r.observe(&unavailable, plan, nil, now.Add(time.Second))
			r.observeHub(nil, now.Add(2*time.Second))
			if len(r.tracks) != 0 || r.topology.Profile != profile {
				t.Fatal("Hub update reclassified an unsupported direct publication")
			}
		})
	}
}

func TestMetroHubCorrectionDuringWaitFailureIsRetained(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	static.Schedule.Trips[0].Direction = ptr(0)
	static.Schedule.Trips[0].Shape = "synthetic-shape"
	static.Shapes = []api.RouteShape{{ShapeId: "synthetic-shape", RouteId: "metro:r", Geometry: [][]float64{{-9.14, 38.75}, {-9.14, 38.71}}}}
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10")}
	publishMetroTest(s, static, data, now)
	if len(data.Trains) != 1 {
		t.Fatal("synthetic baseline did not admit a journey")
	}
	id := data.Trains[0].JourneyId
	r := s.Cache.metroRuntime
	at := now.Add(time.Second)
	original := hubPosition{Agency: "IA2N9", ID: "[IA2N9]7", Route: "[IA2N9]r", Trip: "[synthetic][IA2N9]one", Shape: "[synthetic][IA2N9]synthetic-shape", Direction: json.RawMessage(`0`), Lat: 38.73, Lon: -9.14, At: at.UnixMilli()}
	r.observeHub([]hubPosition{original}, at)
	r.mu.Lock()
	track := r.tracks[id]
	call := &track.Train.Calls[0]
	call.Departure = api.CallTime{Kind: "inferred", Inferred: &api.MetroEventEvidence{At: at, WindowStart: now, WindowEnd: at, Mode: "model_departure", ModelVersion: "synthetic"}}
	track.ModelDepartureSupport = map[string]map[int64]metroModelPublication{call.Id: {original.At: metroPublicationStamp(original)}}
	r.queueCheckpoint(track)
	r.mu.Unlock()
	r.flushCheckpoints(context.Background(), s.Patterns, at)
	failed := *data
	failed.Status.Status = "error"
	publishMetroTest(s, static, &failed, now.Add(2*time.Second))
	corrected := original
	corrected.Lat += .0001
	r.observeHub([]hubPosition{corrected}, now.Add(3*time.Second))
	r.mu.Lock()
	track = r.tracks[id]
	if track.Train.Calls[0].Departure.Inferred != nil || track.Train.Calls[0].DepartureRevisions == nil || len(r.hubCorrections) != 1 {
		r.mu.Unlock()
		t.Fatal("independent Hub correction did not withdraw its supported occurrence")
	}
	if _, ok := r.dirty[id]; !ok {
		r.mu.Unlock()
		t.Fatal("Hub correction was not queued for checkpoint persistence")
	}
	var capture metroCapturedInput
	if len(r.capture.Queue) == 0 || json.Unmarshal(r.capture.Queue[len(r.capture.Queue)-1].Payload, &capture) != nil || capture.WaitAvailability != "error" || len(capture.Corrections) != 1 || capture.Corrections[0].At != original.At {
		r.mu.Unlock()
		t.Fatal("Hub correction was not captured during the direct outage")
	}
	r.mu.Unlock()
	r.flushCheckpoints(context.Background(), s.Patterns, now.Add(3*time.Second))
	checkpoint, state, err := s.Patterns.MetroJourneyCheckpoint(context.Background(), id, now.Add(3*time.Second))
	var payload metroCheckpointPayload
	if err != nil || state != "restored" || json.Unmarshal(checkpoint.Payload, &payload) != nil || payload.Train.Calls[0].Departure.Inferred != nil || payload.Train.Calls[0].DepartureRevisions == nil {
		t.Fatal("Hub correction was not retained in the checkpoint", state, err)
	}
}
