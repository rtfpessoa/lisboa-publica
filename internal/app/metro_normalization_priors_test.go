package app

import (
	"reflect"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func legacyMetroPriors(data *MetroData, static *StaticData, path patterns.Pattern, now time.Time) []metroVisitPrior {
	out := make([]metroVisitPrior, len(path.Stops))
	if static == nil || static.Schedule == nil {
		return out
	}
	b := metroPlanBuilder{data: data, static: static, stations: map[string]MetroStation{}, mapping: map[string]string{}, routes: map[string]string{}}
	b.addStations()
	b.mapStaticPlan()
	dwell, run := make([][]int, len(out)), make([][]int, len(out))
	for _, trip := range static.Schedule.Trips {
		_, direction := legacyMetroTripDestination(data, trip)
		if b.routes[trip.Route] != path.Route || !static.Schedule.active(trip.Service, now.In(lisbon)) || direction != path.Direction {
			continue
		}
		visits := localJourneyTimes(&trip)
		if metroPriorPathMatches(&b, visits, path) {
			collectMetroPriorDurations(visits, dwell, run)
		}
	}
	for i := range out {
		out[i] = metroVisitPrior{Dwell: metroPriorMedian(dwell[i]), Run: metroPriorMedian(run[i]), DwellSamples: dwell[i]}
	}
	return out
}

func metroNormalizationPriorFixture() (*MetroData, *StaticData, time.Time) {
	data, static := metroPlatformFixture()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now, SourceUrl: metroBase, Lines: []api.MetroLine{}}
	static.Schedule.Exceptions = map[string]map[string]int{"active": {now.In(lisbon).Format("20060102"): 1}}
	for i := range static.Schedule.Trips {
		static.Schedule.Trips[i].Service = "active"
	}
	static.Schedule.Trips[0].Times = []StopTime{{Stop: "a", Sequence: 1, Arrival: 100, Departure: 120}, {Stop: "p1", Sequence: 2, Arrival: 180, Departure: 210}, {Stop: "bx", Sequence: 3, Arrival: 270, Departure: 285}}
	static.Schedule.Trips[0].Shape = "synthetic"
	static.Shapes = []api.RouteShape{{ShapeId: "synthetic", RouteId: "metro:4_0", Geometry: [][]float64{{-9.22, 38.75}, {-9.1402, 38.7102}, {-9.12, 38.71}}}}
	return data, static, now
}

func TestMetroNormalizedSchedulePriorsAndProjections(t *testing.T) {
	for _, tc := range []struct {
		name         string
		destinations []MetroDestination
		direction    string
		active       bool
	}{
		{"legacy fallback", nil, "42", true},
		{"changed destination", []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}, "new", true},
		{"duplicate destination", []MetroDestination{{ID: "new", Name: "Santa Apolónia"}, {ID: "new", Name: "Santa Apolónia"}}, "new", true},
		{"ambiguous destination", []MetroDestination{{ID: "new", Name: "Santa Apolónia"}, {ID: "other", Name: "Santa Apolónia"}}, "new", false},
		{"empty destination", []MetroDestination{{ID: "", Name: "Santa Apolónia"}}, "42", true},
		{"nonempty then empty", []MetroDestination{{ID: "new", Name: "Santa Apolónia"}, {ID: "", Name: "Santa Apolónia"}}, "new", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, static, now := metroNormalizationPriorFixture()
			data.Destinations = tc.destinations
			path := patterns.Pattern{Route: "metro:4_0", Direction: tc.direction, Destination: "SP", Stops: []string{"RB", "SS", "SP"}, Sequences: []int{1, 2, 3}}
			want := legacyMetroPriors(data, static, path, now)
			got := metroSchedulePriors(data, static, path, now)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("schedule priors differ from original resolver")
			}
			if tc.active && (got[0].Dwell != 20 || got[0].Run != 60 || got[1].Dwell != 30 || got[1].Run != 60) {
				t.Fatalf("active trip did not produce expected dwell and run priors: %+v", got)
			}
			if !tc.active && (got[0].Dwell != 0 || got[0].Run != 0) {
				t.Fatal("ambiguous destination produced a schedule prior")
			}
			assertMetroNormalizedProjection(t, data, static, path, now, got, want, tc.active)
		})
	}
}

func assertMetroNormalizedProjection(t *testing.T, data *MetroData, static *StaticData, path patterns.Pattern, now time.Time, got, want []metroVisitPrior, active bool) {
	t.Helper()
	expiry := now.Add(90 * time.Second)
	anchor := &api.CallTimeEvidence{At: now.Add(10 * time.Second), SourceUpdatedAt: &now, ValidUntil: &expiry}
	makeTrack := func(priors []metroVisitPrior) *metroTrack {
		return &metroTrack{Profile: "synthetic", Priors: priors, Train: api.MetroTrain{JourneyId: "synthetic", Association: "supported", SourceUpdatedAt: now, ValidUntil: expiry, Calls: []api.StopCall{{Arrival: api.CallTime{Prediction: anchor}}, {}, {}}}}
	}
	current, legacy := makeTrack(got), makeTrack(want)
	projectMetroScheduled(current, now)
	projectMetroScheduled(legacy, now)
	if !reflect.DeepEqual(current.Train.Calls, legacy.Train.Calls) {
		t.Fatal("track schedule projections differ from original resolver")
	}
	departure := current.Train.Calls[0].OwnDeparturePrediction
	if active && (departure == nil || !departure.At.Equal(now.Add(30*time.Second))) {
		t.Fatal("expected nonzero track departure projection")
	}
	if !active && departure != nil {
		t.Fatal("unsupported track departure projection")
	}
	r := newMetroRuntime()
	r.publication, r.plan = data, static
	r.topology, _ = metroTopologyReport(data, static)
	dwell := r.contextDwellPriors(path, now)
	calls := metroPathCalls(path, data, static, "context")
	call := api.StopCall{StopId: calls[0].StopId, Arrival: api.CallTime{Prediction: anchor}}
	r.projectContextPriorDeparture(&call, dwell, now)
	if active && (call.OwnDeparturePrediction == nil || !call.OwnDeparturePrediction.At.Equal(now.Add(30*time.Second))) {
		t.Fatal("expected nonzero context departure projection")
	}
	if !active && call.OwnDeparturePrediction != nil {
		t.Fatal("ambiguous context produced a departure projection")
	}
	assertMetroNormalizedRuntimeTrack(t, data, static, path, now, want, active)
}

func assertMetroNormalizedRuntimeTrack(t *testing.T, data *MetroData, static *StaticData, path patterns.Pattern, now time.Time, want []metroVisitPrior, active bool) {
	t.Helper()
	input := *data
	first := metroTestRow(now, "RB", "7", "10")
	first.Destination = path.Direction
	input.Waits = []MetroWait{first}
	r := newMetroRuntime()
	r.observe(&input, static, nil, now)
	if !active {
		if len(r.active) != 0 {
			t.Fatal("ambiguous destination admitted a runtime track")
		}
		return
	}
	if len(r.active) != 1 {
		t.Fatal("active compatible trip did not admit a runtime track")
	}
	for _, id := range r.active {
		if track := r.tracks[id]; track == nil || !reflect.DeepEqual(track.Priors, want) {
			t.Fatal("runtime track priors differ from original resolver")
		}
	}
	r.projectOwnWithClock(&input, nil, now, func() time.Time { return now })
	if len(input.Trains) != 1 || input.Trains[0].Calls[0].OwnDeparturePrediction == nil || !input.Trains[0].Calls[0].OwnDeparturePrediction.At.Equal(now.Add(30*time.Second)) {
		t.Fatal("runtime track did not project the expected departure")
	}
}

func TestMetroNormalizedPriorGeometryUsesOriginalStationMapping(t *testing.T) {
	data, static, _ := metroNormalizationPriorFixture()
	path := patterns.Pattern{Route: "metro:4_0", Direction: "42", Destination: "SP", Stops: []string{"RB", "SS", "SP"}, Sequences: []int{1, 2, 3}}
	geometry := metroPathGeometry(data, static, path)
	axis := metroRawAxisGeometry(data, static, path)
	if len(geometry) != 3 || len(geometry[0]) == 0 || len(geometry[1]) == 0 || len(axis) != 3 {
		t.Fatalf("synthetic motion geometry was not mapped: %d segments, %d axis points", len(geometry), len(axis))
	}
	segments := [][][]float64{{axis[0], axis[1], axis[1]}, {axis[1], axis[1], axis[2]}, nil}
	if !reflect.DeepEqual(axis, static.Shapes[0].Geometry) || !reflect.DeepEqual(geometry, segments) {
		t.Fatal("motion geometry changed from the published synthetic shape")
	}
	progress, distance := metroGeometryProgress(axis, hubPosition{Lat: 38.7102, Lon: -9.1402})
	if progress <= 0 || distance > .01 {
		t.Fatal("the synthetic station did not project onto its expected motion axis")
	}
}

func TestMetroNormalizedQueuedSnapshotsAndRuntimeUseTheirOwnMetadata(t *testing.T) {
	data, static, now := metroNormalizationPriorFixture()
	client := &MetroClient{}
	var tasks []metroHistoryTask
	for _, destinations := range [][]MetroDestination{
		{{ID: "old", Name: "Santa Apolónia"}},
		{{ID: "new", Name: "Santa Apolónia"}},
		{{ID: "new", Name: "Santa Apolónia"}, {ID: "other", Name: "Santa Apolónia"}},
	} {
		snapshot := *data
		snapshot.Destinations = destinations
		tasks = append(tasks, client.patternHistoryTask(&snapshot, static, now))
	}
	runtime := newMetroRuntime()
	for _, index := range []int{0, 1, 2, 0} {
		task := &tasks[index]
		want, missing := legacyMetroTopology(&task.Data, task.Static)
		_, got := client.patternReceipt(&task.Data, task.Static, task.At)
		runtime.updateTopology(&task.Data, task.Static)
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(runtime.topology, want) || !reflect.DeepEqual(runtime.unmappedStations, missing) {
			t.Fatal("queued or runtime topology used a different snapshot")
		}
	}
}
