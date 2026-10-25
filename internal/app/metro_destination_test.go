package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// The changed destination codes in these tests are synthetic provider revisions.
func TestMetroTopologyRejectsConflictingLegacyDestinations(t *testing.T) {
	tests := []struct {
		name    string
		catalog []MetroDestination
		want    map[string]string
	}{
		{"legacy", nil, map[string]string{"RB": "33", "SP": "42"}},
		{"reused code", []MetroDestination{{ID: "33", Name: "Santa Apolónia"}}, map[string]string{"SP": "33"}},
		{"restored reverse", []MetroDestination{{ID: "33", Name: "Santa Apolónia"}, {ID: "reverse", Name: "Reboleira"}}, map[string]string{"SP": "33", "RB": "reverse"}},
		{"duplicate identical", []MetroDestination{{ID: "33", Name: "Santa Apolónia"}, {ID: "33", Name: "Santa Apolónia"}}, map[string]string{}},
		{"duplicate conflicting", []MetroDestination{{ID: "33", Name: "Santa Apolónia"}, {ID: "33", Name: "Reboleira"}}, map[string]string{}},
		{"conflicting names", []MetroDestination{{ID: "a", Name: "Santa Apolónia"}, {ID: "b", Name: "Santa Apolónia"}}, map[string]string{"RB": "33"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, static := metroPlatformFixture()
			data.Destinations = tc.catalog
			topology := metroTopology(data, static)
			got := map[string]string{}
			for _, path := range topology.Patterns {
				got[path.Destination] = path.Direction
			}
			if len(got) != len(tc.want) {
				t.Fatalf("directions = %v, want %v", got, tc.want)
			}
			for destination, direction := range tc.want {
				if got[destination] != direction {
					t.Fatalf("direction %s = %q, want %q", destination, got[destination], direction)
				}
			}
		})
	}
}

func TestMetroStationDirectionLabelsUseCanonicalPath(t *testing.T) {
	data, static := metroPlatformFixture()
	data.Destinations = []MetroDestination{{ID: "33", Name: "Santa Apolónia"}, {ID: "reverse", Name: "Reboleira"}}
	data.Topology = metroTopology(data, static)
	for _, path := range data.Topology.Patterns {
		label := metroBoardDirectionLabel(data, data.Topology, path.Route, path.Direction)
		want := map[string]string{"33": "Santa Apolónia", "reverse": "Reboleira"}[path.Direction]
		if label != want {
			t.Fatalf("direction %s = %q, want %q", path.Direction, label, want)
		}
	}
	short := patterns.Pattern{Route: "line", Direction: "short", Destination: "SS", Stops: []string{"RB", "SS"}}
	long := patterns.Pattern{Route: "line", Direction: "long", Destination: "SP", Stops: []string{"RB", "SS", "SP"}}
	data.Topology.Patterns = []patterns.Pattern{short, long}
	if direction := canonicalMetroDirection(data.Topology, short); direction != "long" || metroBoardDirectionLabel(data, data.Topology, "line", direction) != "Santa Apolónia" {
		t.Fatal("short turn did not retain the full canonical board destination")
	}
	data.Topology.Patterns = append(data.Topology.Patterns, patterns.Pattern{Route: "line", Direction: "long", Destination: "RB", Stops: []string{"RB", "SS", "SP"}})
	if label := metroBoardDirectionLabel(data, data.Topology, "line", "long"); label != "Destino publicado: long" {
		t.Fatalf("conflicting canonical endpoints selected %q", label)
	}
}

func TestMetroCurrentDestinationsAcrossArrivalSurfaces(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		catalog   []MetroDestination
		code      string
		topology  bool
		wantName  string
		wantRoute string
	}{
		{"reused code", []MetroDestination{{ID: "33", Name: "Santa Apolónia"}}, "33", true, "Santa Apolónia", "metro:4_0"},
		{"new code", []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}, "new", true, "Santa Apolónia", "metro:4_0"},
		{"legacy", nil, "33", true, "Reboleira", "metro:4_0"},
		{"empty ID only", []MetroDestination{{ID: "", Name: "Santa Apolónia"}}, "42", true, "Santa Apolónia", "metro:4_0"},
		{"moved code", []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}, "42", true, "Destino publicado: 42", ""},
		{"duplicate code", []MetroDestination{{ID: "33", Name: "Santa Apolónia"}, {ID: "33", Name: "Reboleira"}}, "33", true, "Destino publicado: 33", ""},
		{"without topology", []MetroDestination{{ID: "33", Name: "Santa Apolónia"}}, "33", false, "Santa Apolónia", "metro:4_0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, static := metroPlatformFixture()
			static.Routes[0].ShortName, static.Routes[0].LongName = "Az", "Linha Azul"
			for i := range data.Stations {
				data.Stations[i].Lines = "[Azul]"
				if data.Stations[i].ID == "SS" {
					data.Stations[i].Lines = "[Azul,Vermelha]"
				}
			}
			data.Destinations = tc.catalog
			data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
			data.Waits = []MetroWait{{Stop: "SS", Platform: "1", At: now.In(lisbon).Format("20060102150405"), Train: "7", Destination: tc.code, Wait1: json.RawMessage("120")}}
			if tc.topology {
				data.Topology, data.topologyPlan = metroTopology(data, static), static
			}
			state := &State{Created: now, Metro: data, Static: map[string]*StaticData{"metro": static}}
			arrivals := predictedArrivals(state, now, now.Add(time.Hour), "", "metro:SS")
			if len(arrivals) != 1 || arrivals[0].Headsign != tc.wantName || arrivals[0].RouteId != tc.wantRoute {
				t.Fatalf("arrival = %+v, want destination %q route %q", arrivals, tc.wantName, tc.wantRoute)
			}
			if arrivals[0].ObservedAt == nil || !arrivals[0].ObservedAt.Equal(now) || arrivals[0].ExpectedAt == nil || !arrivals[0].ExpectedAt.Equal(now.Add(120*time.Second)) {
				t.Fatal("arrival lost the original source clock")
			}
			vehicle := &api.Vehicle{SourceId: "7"}
			destination := metroVehicleDestination(state, vehicle, now)
			if tc.wantName == "Destino publicado: "+tc.code {
				if destination != "" {
					t.Fatalf("unknown destination presented as %q", destination)
				}
			} else if destination != tc.wantName {
				t.Fatalf("vehicle destination = %q, want %q", destination, tc.wantName)
			}
			cache := metroOfficialCache(state, "SS", now)
			if len(cache) != 1 {
				t.Fatalf("official cache lost the source forecast: %+v", cache)
			}
			wantCache := tc.wantName
			if wantCache == "Destino publicado: "+tc.code {
				wantCache = ""
			}
			if cache[0].DestinationName != wantCache {
				t.Fatalf("official cache destination = %q, want %q", cache[0].DestinationName, wantCache)
			}
		})
	}
}

func TestMetroLocalForecastUsesCurrentRuntimeTopology(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	data, static := metroPlatformFixture()
	static.Routes[0].ShortName = "Az"
	data.Destinations = []MetroDestination{{ID: "33", Name: "Santa Apolónia"}}
	data.Stations[2].Lines = "[Azul,Vermelha]"
	row := MetroWait{Stop: "SS", Platform: "1", Destination: "33", At: now.In(lisbon).Format("20060102150405"), Train: "7", Wait1: json.RawMessage("120")}
	topology := metroTopology(data, static)
	// The publication snapshot is taken before the runtime attaches topology.
	if len(data.Topology.Patterns) != 0 {
		t.Fatal("fixture unexpectedly supplied publication topology")
	}
	r := newMetroRuntime()
	r.publication, r.plan, r.topology, r.directionTopology = data, static, topology, metroDirectionTopology(data, static, topology)
	r.batch = &metroPointBatch{localOnly: []metroLocalForecast{{Row: row, Reference: "7", Clock: now, Seconds: 120, Valid: true}}}
	contexts := r.localForecastContexts(now)
	if len(contexts) != 1 || contexts[0].Destination != "Santa Apolónia" || contexts[0].RouteId != "metro:4_0" || textValue(contexts[0].DirectionCode) != "33" || len(contexts[0].Calls) != 1 {
		t.Fatalf("local forecast did not use current topology: %+v", contexts)
	}
	if prediction := contexts[0].Calls[0].Arrival.Prediction; prediction == nil || prediction.SourceUpdatedAt == nil || !prediction.SourceUpdatedAt.Equal(now) || !prediction.At.Equal(now.Add(120*time.Second)) {
		t.Fatal("local forecast lost its original source clock")
	}
	if expired := r.localForecastContexts(now.Add(sourceFreshness)); len(expired) != 0 {
		t.Fatal("local forecast renewed an expired source clock")
	}
}

func TestMetroLocalForecastDirectionRequiresSourceStationPath(t *testing.T) {
	path := func(direction string, stops ...string) patterns.Pattern {
		return patterns.Pattern{Route: "metro:4_0", Direction: direction, Stops: stops}
	}
	for _, tc := range []struct {
		name  string
		paths []patterns.Pattern
		stop  string
		want  string
	}{
		{"different origin", []patterns.Pattern{path("new", "DP", "SP")}, "RB", ""},
		{"matching origin", []patterns.Pattern{path("new", "DP", "SP")}, "DP", "new"},
		{"ambiguous containing orientations", []patterns.Pattern{path("new", "RB", "A"), path("new", "RB", "B"), path("long-a", "RB", "A", "SP"), path("long-b", "RB", "B", "SP")}, "RB", ""},
		{"unrelated candidate ignored", []patterns.Pattern{path("new", "RB", "A"), path("new", "DP", "B"), path("long-a", "RB", "A", "SP"), path("long-b", "DP", "B", "SP")}, "RB", "long-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direction := localMetroDirection(patterns.Topology{Patterns: tc.paths}, "metro:4_0", "new", tc.stop)
			if textValue(direction) != tc.want || tc.want == "" && direction != nil {
				t.Fatalf("direction = %v, want %q", direction, tc.want)
			}
		})
	}
}

func TestMetroLiveLocalForecastDoesNotBorrowAnotherStationsPath(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	data, static := metroPlatformFixture()
	static.Routes[0].ShortName = "Az"
	data.Stations[0].Lines, data.Stations[1].Lines, data.Stations[2].Lines = "[Azul]", "[Azul]", "[Vermelha]"
	data.Stations = append(data.Stations, MetroStation{ID: "DP", Name: "Destino Próximo", Lat: "38.72", Lon: "-9.13", Lines: "[Azul]"})
	static.Stops = append(static.Stops, api.Stop{Id: "metro:dp", SourceId: "dp", Name: "Destino Próximo", Lat: 38.72, Lon: -9.13})
	static.Schedule.Trips = append(static.Schedule.Trips, ScheduledTrip{ID: "other-origin", Route: "4_0", Headsign: "Santa Apolónia", Times: []StopTime{{Stop: "dp", Sequence: 1}, {Stop: "bx", Sequence: 2, Arrival: 60, Departure: 60}}})
	data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}
	data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
	data.Waits = []MetroWait{
		{Stop: "RB", Platform: "1", At: now.In(lisbon).Format("20060102150405"), Destination: "new", Wait1: json.RawMessage("120")},
		{Stop: "DP", Platform: "1", At: now.In(lisbon).Format("20060102150405"), Destination: "new", Wait1: json.RawMessage("120")},
	}
	cache := NewCache()
	cache.update("metro", static, nil, cache.operator("metro"))
	server, err := NewServer(nil, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, reverse := range []bool{false, true} {
		if reverse {
			data.Waits[0], data.Waits[1] = data.Waits[1], data.Waits[0]
		}
		cache.metroRuntime.observe(data, static, nil, now)
		cache.updateMetro(data, cache.operator("metro"))
		for _, tc := range []struct {
			stop, direction string
			directions      int
		}{{"metro:a", "", 0}, {"metro:dp", "new", 1}} {
			frame, _, err := server.metroFrame(context.Background(), metroInterest{Stop: tc.stop})
			if err != nil || len(frame.Directions) != tc.directions || len(frame.Trains) != 0 || len(frame.UnassociatedForecasts) != 1 {
				t.Fatalf("reverse=%v stop=%s: local forecast or direction tab unavailable: frame=%+v err=%v", reverse, tc.stop, frame, err)
			}
			call := frame.UnassociatedForecasts[0]
			if call.LineKey != "metro:4_0" || textValue(call.DirectionKey) != tc.direction || tc.direction == "" && call.DirectionKey != nil || call.JourneyId != nil || call.Destination != "Santa Apolónia" || call.Arrival.Prediction == nil || call.Arrival.Prediction.SourceUpdatedAt == nil || !call.Arrival.Prediction.SourceUpdatedAt.Equal(now) || !call.Arrival.Prediction.At.Equal(now.Add(120*time.Second)) {
				t.Fatalf("reverse=%v stop=%s: local forecast borrowed another station's path or changed its clock: %+v", reverse, tc.stop, call)
			}
		}
	}
	if _, _, contexts, _ := cache.metroRuntime.view(now.Add(sourceFreshness)); len(contexts) != 0 {
		t.Fatal("local forecast remained current past its original source expiry")
	}
}

func TestMetroCachedFrameBuildsDestinationDirections(t *testing.T) {
	s, _, data, now := metroLiveFixture(t)
	data.Destinations = []MetroDestination{{ID: "new", Name: "Cais do Sodré"}}
	s.Cache.updateMetro(data, s.Cache.operator("metro"))
	b, err := s.newMetroFrameBuilder(context.Background(), metroInterest{Stop: "metro:gtfs-cs"}, false)
	if err != nil {
		t.Fatal(err)
	}
	b.now = now
	b.stationDirections()
	if len(b.frame.Directions) != 1 || b.frame.Directions[0].Label != "Cais do Sodré" || textValue(b.frame.Directions[0].DirectionKey) != "new" {
		t.Fatalf("cached frame directions = %+v", b.frame.Directions)
	}
}

func TestMetroStationDirectionsWithholdContradictoryCanonicalPath(t *testing.T) {
	data, static := metroPlatformFixture()
	static.Routes[0].ShortName = "Az"
	data.Stations[0].Lines, data.Stations[2].Lines = "[Azul]", "[Azul]"
	data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}, {ID: "short", Name: "São Sebastião"}, {ID: "reverse", Name: "Reboleira"}}
	data.Topology.Patterns = []patterns.Pattern{
		{Route: "metro:4_0", Direction: "short", Destination: "SS", Stops: []string{"RB", "SS"}},
		{Route: "metro:4_0", Direction: "new", Destination: "SP", Stops: []string{"RB", "SS", "SP"}},
		{Route: "metro:4_0", Direction: "reverse", Destination: "RB", Stops: []string{"SS", "RB"}},
	}
	for _, tc := range []struct {
		targetLines string
		want        map[string]string
	}{
		{"[Vermelha]", map[string]string{"short": "São Sebastião", "reverse": "Reboleira"}},
		{"[Azul]", map[string]string{"new": "Santa Apolónia", "reverse": "Reboleira"}},
		{"", map[string]string{"new": "Santa Apolónia", "reverse": "Reboleira"}},
	} {
		data.Stations[1].Lines = tc.targetLines
		b := metroFrameBuilder{metroFrameRequest: metroFrameRequest{interest: metroInterest{Stop: "metro:p"}}, state: &State{Metro: data}, static: static}
		b.stationDirections()
		got := map[string]string{}
		for _, direction := range b.frame.Directions {
			got[textValue(direction.DirectionKey)] = direction.Label
		}
		if len(got) != len(tc.want) {
			t.Fatalf("target=%q: directions = %v, want %v", tc.targetLines, got, tc.want)
		}
		for direction, label := range tc.want {
			if got[direction] != label {
				t.Fatalf("target=%q: directions = %v, want %v", tc.targetLines, got, tc.want)
			}
		}
	}
}

// The station memberships and codes are synthetic provider revisions. A bad
// endpoint must not change the direction of the still-admissible short turn.
func TestMetroShortTurnDirectionMatchesLiveFramesAcrossMetadataChanges(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	data, static, cache, server := metroShortTurnFixture(t)
	var journey, profile string
	for _, tc := range []metroShortTurnCase{
		{"[Vermelha]", "short", "São Sebastião"},
		{"[Azul]", "new", "Santa Apolónia"},
		{"[Vermelha]", "short", "São Sebastião"},
		{"", "new", "Santa Apolónia"},
	} {
		data.Stations = append([]MetroStation(nil), data.Stations...)
		data.Stations[1].Lines = tc.targetLines
		data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
		data.Waits = []MetroWait{{Stop: "SS", Platform: "1", At: now.In(lisbon).Format("20060102150405"), Train: "7", Destination: "short", Wait1: json.RawMessage("120")}}
		cache.metroRuntime.observe(data, static, nil, now)
		cache.updateMetro(data, cache.operator("metro"))
		journey, profile = assertMetroShortTurnTrain(t, data, tc, journey, profile)
		assertMetroShortTurnForecast(t, cache, now, tc)
		for _, stop := range []string{"metro:a", "metro:p"} {
			assertMetroShortTurnFrame(t, server, stop, tc)
		}
		now = now.Add(time.Second)
	}
}

type metroShortTurnCase struct{ targetLines, direction, label string }

func metroShortTurnFixture(t *testing.T) (*MetroData, *StaticData, *Cache, *Server) {
	t.Helper()
	data, static := metroPlatformFixture()
	static.Routes[0].ShortName, static.Routes[0].LongName = "Az", "Linha Azul"
	static.Schedule.Trips = append(static.Schedule.Trips, ScheduledTrip{ID: "short", Route: "4_0", Headsign: "São Sebastião", Times: []StopTime{{Stop: "a", Sequence: 1}, {Stop: "p1", Sequence: 2, Arrival: 60, Departure: 60}}})
	data.Stations[0].Lines, data.Stations[2].Lines = "[Azul]", "[Azul]"
	data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}, {ID: "short", Name: "São Sebastião"}, {ID: "reverse", Name: "Reboleira"}}
	cache := NewCache()
	cache.update("metro", static, nil, cache.operator("metro"))
	server, err := NewServer(nil, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return data, static, cache, server
}

func assertMetroShortTurnTrain(t *testing.T, data *MetroData, tc metroShortTurnCase, journey, profile string) (string, string) {
	t.Helper()
	if len(data.Trains) != 1 || data.Trains[0].Association != "supported" || data.Trains[0].DirectionCode != tc.direction || data.Trains[0].Destination != "São Sebastião" {
		t.Fatalf("target=%q: short train = %+v", tc.targetLines, data.Trains)
	}
	train := data.Trains[0]
	if journey != "" && train.JourneyId != journey {
		t.Fatalf("metadata correction changed journey identity: %q -> %q", journey, train.JourneyId)
	}
	if profile != "" && data.Topology.Profile != profile {
		t.Fatal("metadata-only correction changed the raw path profile")
	}
	for _, call := range train.Calls {
		if textValue(call.DirectionKey) != tc.direction {
			t.Fatalf("target=%q: train call direction = %+v", tc.targetLines, call)
		}
	}
	return train.JourneyId, data.Topology.Profile
}

func assertMetroShortTurnForecast(t *testing.T, cache *Cache, now time.Time, tc metroShortTurnCase) {
	t.Helper()
	_, _, contexts, _ := cache.metroRuntime.view(now)
	if len(contexts) != 1 || textValue(contexts[0].DirectionCode) != tc.direction || contexts[0].Destination != "São Sebastião" || len(contexts[0].Calls) != 1 {
		t.Fatalf("target=%q: short forecast contexts = %+v", tc.targetLines, contexts)
	}
	forecast := contexts[0].Calls[0]
	if textValue(forecast.DirectionKey) != tc.direction || forecast.Arrival.Prediction == nil || forecast.Arrival.Prediction.SourceUpdatedAt == nil || !forecast.Arrival.Prediction.SourceUpdatedAt.Equal(now) || !forecast.Arrival.Prediction.At.Equal(now.Add(120*time.Second)) {
		t.Fatalf("target=%q: short forecast lost direction or clock: %+v", tc.targetLines, forecast)
	}
}

func assertMetroShortTurnFrame(t *testing.T, server *Server, stop string, tc metroShortTurnCase) {
	t.Helper()
	frame, _, err := server.metroFrame(context.Background(), metroInterest{Stop: stop})
	if err != nil || len(frame.Trains) != 1 || frame.Trains[0].DirectionCode != tc.direction {
		t.Fatalf("target=%q stop=%s: frame train=%+v err=%v", tc.targetLines, stop, frame.Trains, err)
	}
	for _, direction := range frame.Directions {
		if textValue(direction.DirectionKey) == tc.direction && direction.Label == tc.label && direction.Count == nil {
			return
		}
	}
	t.Fatalf("target=%q stop=%s: tab missing matching short train: %+v", tc.targetLines, stop, frame.Directions)
}

func TestMetroSuccessorCandidateDirectionFollowsCurrentCatalogue(t *testing.T) {
	now := time.Now()
	short := patterns.Pattern{Route: "metro:4_0", Direction: "short", Stops: []string{"RB", "SS"}}
	long := patterns.Pattern{Route: "metro:4_0", Direction: "new", Stops: []string{"RB", "SS", "SP"}}
	track := &metroTrack{Train: api.MetroTrain{JourneyId: "journey", DirectionCode: "new", SourceUpdatedAt: now, ValidUntil: now.Add(time.Minute), Calls: []api.StopCall{{DirectionKey: ptr("new")}}}}
	r := newMetroRuntime()
	r.candidates["candidate"] = "journey"
	r.tracks["journey"] = track
	r.directionTopology.Patterns = []patterns.Pattern{short}
	observation := metroSuccessorObservation{key: "candidate", batch: &metroPointBatch{paths: map[string]patterns.Pattern{"candidate": short}}, now: now}
	if got := r.successorCandidate(observation, "7"); got != track || track.Train.DirectionCode != "short" || textValue(track.Train.Calls[0].DirectionKey) != "short" {
		t.Fatalf("candidate did not follow the admitted short turn: %+v", track.Train)
	}
	track.BarrierRevision = 1
	r.directionTopology.Patterns = []patterns.Pattern{short, long}
	r.successorCandidate(observation, "7")
	if track.Train.DirectionCode != "short" || textValue(track.Train.Calls[0].DirectionKey) != "short" {
		t.Fatalf("candidate mutated during lifecycle barrier: %+v", track.Train)
	}
	track.BarrierRevision = 0
	r.successorCandidate(observation, "7")
	if track.Train.DirectionCode != "new" || textValue(track.Train.Calls[0].DirectionKey) != "new" {
		t.Fatalf("candidate did not recover after lifecycle barrier: %+v", track.Train)
	}
}

func TestMetroArrivalWithholdsContradictoryLineEvidence(t *testing.T) {
	data, static := metroPlatformFixture()
	data.Stations[0].Lines, data.Stations[1].Lines, data.Stations[2].Lines = "[Azul]", "[Azul]", "[Azul,Vermelha]"
	data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}
	// The synthetic GTFS route is red, but the uniquely named endpoint is blue.
	data.Topology = metroTopology(data, static)
	resolver := newMetroDestinationResolver(data, data.Topology)
	row := MetroWait{Stop: "SS", Destination: "new"}
	arrival := metroArrival(row, metroRouteIDs(static), resolver, "metro:SS")
	if arrival.Headsign != "Santa Apolónia" || arrival.RouteId != "" || arrival.RouteName != nil {
		t.Fatalf("contradictory topology selected a line: %+v", arrival)
	}
	data.Stations = append(data.Stations, data.Stations[2])
	resolver = newMetroDestinationResolver(data, data.Topology)
	arrival = metroArrival(row, metroRouteIDs(static), resolver, "metro:SS")
	if arrival.RouteId != "" {
		t.Fatalf("duplicate origin selected a line: %+v", arrival)
	}
}

func TestMetroDestinationEmptyIDOrderDoesNotChangeTopology(t *testing.T) {
	for _, emptyFirst := range []bool{true, false} {
		data, static, now := metroNormalizationPriorFixture()
		data.Destinations = []MetroDestination{{ID: "", Name: "Santa Apolónia"}, {ID: "new", Name: "Santa Apolónia"}}
		if !emptyFirst {
			data.Destinations[0], data.Destinations[1] = data.Destinations[1], data.Destinations[0]
		}
		topology := metroTopology(data, static)
		var outbound *patterns.Pattern
		for i := range topology.Patterns {
			if topology.Patterns[i].Destination == "SP" {
				outbound = &topology.Patterns[i]
			}
		}
		if outbound == nil || outbound.Direction != "new" {
			t.Fatalf("emptyFirst=%v: outbound path = %+v", emptyFirst, outbound)
		}
		priors := metroSchedulePriors(data, static, *outbound, now)
		if len(priors) != 3 || priors[0].Dwell != 20 || priors[0].Run != 60 || priors[1].Dwell != 30 || priors[1].Run != 60 {
			t.Fatalf("emptyFirst=%v: schedule priors = %+v", emptyFirst, priors)
		}
	}
}

func TestMetroArrivalWithholdsDisjointStationLines(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for _, code := range []string{"42", "new"} {
		for _, withTopology := range []bool{true, false} {
			data, static := metroPlatformFixture()
			static.Routes[0].ShortName = "Az"
			data.Stations[0].Lines, data.Stations[1].Lines, data.Stations[2].Lines = "[Azul]", "[Vermelha]", "[Azul]"
			data.Destinations = []MetroDestination{{ID: code, Name: "Santa Apolónia"}}
			data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
			data.Waits = []MetroWait{{Stop: "SS", At: now.In(lisbon).Format("20060102150405"), Train: "7", Destination: code, Wait1: json.RawMessage("120")}}
			if withTopology {
				data.Topology, data.topologyPlan = metroTopology(data, static), static
			}
			state := &State{Created: now, Metro: data, Static: map[string]*StaticData{"metro": static}}
			arrivals := predictedArrivals(state, now, now.Add(time.Hour), "", "metro:SS")
			if len(arrivals) != 1 || arrivals[0].Headsign != "Santa Apolónia" || arrivals[0].RouteId != "" || arrivals[0].RouteName != nil {
				t.Fatalf("code=%s topology=%v: contradictory arrival = %+v", code, withTopology, arrivals)
			}
			if arrivals[0].ObservedAt == nil || !arrivals[0].ObservedAt.Equal(now) || arrivals[0].ExpectedAt == nil || !arrivals[0].ExpectedAt.Equal(now.Add(120*time.Second)) {
				t.Fatal("contradictory line evidence changed the official prediction clock")
			}
			if line, direction := metroBoardDirection(static, "metro:p", arrivals[0]); line != "" || direction != nil {
				t.Fatalf("code=%s topology=%v: board assigned conflicting line %s", code, withTopology, line)
			}
			cache := metroOfficialCache(state, "SS", now)
			if len(cache) != 1 || cache[0].Route != "metro:published-destination:"+code || cache[0].DestinationName != "Santa Apolónia" || cache[0].SourceAt == nil || !cache[0].SourceAt.Equal(now) || cache[0].OfficialAt == nil || !cache[0].OfficialAt.Equal(now.Add(120*time.Second)) {
				t.Fatalf("code=%s topology=%v: official fallback lost the original prediction: %+v", code, withTopology, cache)
			}
			if filtered := predictedArrivals(state, now, now.Add(time.Hour), "metro:4_0", "metro:SS"); len(filtered) != 0 {
				t.Fatalf("code=%s topology=%v: route filter included contradictory arrival", code, withTopology)
			}
		}
	}
}

func TestMetroLiveStationPreservesRouteUnknownWait(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	data, static := metroPlatformFixture()
	static.Routes[0].ShortName, static.Routes[0].LongName = "Az", "Linha Azul"
	data.Stations[0].Lines, data.Stations[1].Lines, data.Stations[2].Lines = "[Azul]", "[Vermelha]", "[Azul]"
	data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}
	data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
	data.Waits = []MetroWait{{Stop: "SS", Platform: "1", At: now.In(lisbon).Format("20060102150405"), Train: "7", Destination: "new", Wait1: json.RawMessage("120")}}
	cache := NewCache()
	cache.update("metro", static, nil, cache.operator("metro"))
	server, err := NewServer(nil, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache.metroRuntime.observe(data, static, nil, now)
	cache.updateMetro(data, cache.operator("metro"))
	frame, _, err := server.metroFrame(context.Background(), metroInterest{Stop: "metro:p"})
	if err != nil {
		t.Fatal(err)
	}
	for _, direction := range frame.Directions {
		if direction.Label == "Santa Apolónia" {
			t.Fatal("contradictory destination advertised as a direction")
		}
	}
	for _, train := range frame.Trains {
		if train.Association == "supported" {
			t.Fatal("contradictory route admitted a train")
		}
	}
	if len(frame.UnassociatedForecasts) != 1 {
		t.Fatalf("route-unknown forecast missing from station frame: %+v", frame.UnassociatedForecasts)
	}
	call := frame.UnassociatedForecasts[0]
	if call.LineKey != "" || call.DirectionKey != nil || call.JourneyId != nil || call.Destination != "Santa Apolónia" || call.Arrival.Prediction == nil || call.Arrival.Prediction.SourceUpdatedAt == nil || !call.Arrival.Prediction.SourceUpdatedAt.Equal(now) || !call.Arrival.Prediction.At.Equal(now.Add(120*time.Second)) {
		t.Fatalf("route-unknown forecast claimed support or changed source clocks: %+v", call)
	}
	filtered, _, err := server.metroFrame(context.Background(), metroInterest{Stop: "metro:p", Route: "metro:4_0"})
	if err != nil || len(filtered.UnassociatedForecasts) != 0 {
		t.Fatalf("route filter claimed an unknown-route forecast: %+v %v", filtered.UnassociatedForecasts, err)
	}
	if _, _, contexts, _ := cache.metroRuntime.view(now.Add(sourceFreshness)); len(contexts) != 0 {
		t.Fatal("route-unknown wait remained available after its source clock expired")
	}
}

func TestMetroRuntimeWithholdsAndRecoversContradictoryPath(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	data, static := metroPlatformFixture()
	static.Routes[0].ShortName = "Az"
	data.Stations[0].Lines, data.Stations[1].Lines, data.Stations[2].Lines = "[Azul]", "[Vermelha]", "[Azul]"
	data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}
	data.Waits = []MetroWait{{Stop: "SS", At: now.In(lisbon).Format("20060102150405"), Train: "7", Destination: "new", Wait1: json.RawMessage("120")}}
	r := newMetroRuntime()
	for _, tc := range []struct {
		targetLines, route string
		supported          bool
	}{
		{"[Vermelha]", "metro:published-destination:new", false},
		{"[Azul]", "metro:4_0", true},
		{"[Vermelha]", "metro:published-destination:new", false},
	} {
		data.Stations[1].Lines = tc.targetLines
		data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
		data.Waits[0].At = now.In(lisbon).Format("20060102150405")
		r.observe(data, static, nil, now)
		supported := false
		for _, train := range data.Trains {
			if train.Association == "supported" {
				supported = true
			}
		}
		if supported != tc.supported {
			t.Fatalf("target=%s: supported=%v, trains=%+v", tc.targetLines, supported, data.Trains)
		}
		state := &State{Created: now, Metro: data, Static: map[string]*StaticData{"metro": static}}
		cache := metroOfficialCache(state, "SS", now)
		if len(cache) != 1 || cache[0].Route != tc.route || cache[0].SourceAt == nil || !cache[0].SourceAt.Equal(now) || cache[0].OfficialAt == nil || !cache[0].OfficialAt.Equal(now.Add(120*time.Second)) {
			t.Fatalf("target=%s: official fallback = %+v, want route %s", tc.targetLines, cache, tc.route)
		}
		arrivals := predictedArrivals(state, now, now.Add(time.Hour), "", "metro:SS")
		if len(arrivals) != 1 || arrivals[0].ObservedAt == nil || !arrivals[0].ObservedAt.Equal(now) || arrivals[0].ExpectedAt == nil || !arrivals[0].ExpectedAt.Equal(now.Add(120*time.Second)) {
			t.Fatalf("target=%s: independent wait lost: %+v", tc.targetLines, arrivals)
		}
		now = now.Add(time.Second)
	}
}

func TestMetroArrivalWithholdsConflictingRouteID(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	routes := []api.RouteDetail{
		{Id: "metro:shared", SourceId: "4_0", ShortName: "Az"},
		{Id: "metro:shared", SourceId: "4_0", ShortName: "Vm"},
		{Id: "metro:other-blue", SourceId: "other-blue", ShortName: "Az"},
	}
	orders := [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, order := range orders {
		for _, withTopology := range []bool{true, false} {
			data, static := metroPlatformFixture()
			static.Routes = []api.RouteDetail{routes[order[0]], routes[order[1]], routes[order[2]]}
			for i := range data.Stations {
				data.Stations[i].Lines = "[Vermelha]"
			}
			data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}
			data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
			data.Waits = []MetroWait{{Stop: "SS", At: now.In(lisbon).Format("20060102150405"), Train: "7", Destination: "new", Wait1: json.RawMessage("120")}}
			if len(metroTopology(data, static).Patterns) != 0 {
				t.Fatalf("order=%v: conflicting source route admitted to topology", order)
			}
			if withTopology {
				// A previously admitted or externally supplied path must be fenced too.
				data.Topology = patterns.Topology{Patterns: []patterns.Pattern{{Route: "metro:shared", Direction: "new", Destination: "SP", Stops: []string{"RB", "SS", "SP"}}}}
				data.topologyPlan = static
			}
			state := &State{Created: now, Metro: data, Static: map[string]*StaticData{"metro": static}}
			arrivals := predictedArrivals(state, now, now.Add(time.Hour), "", "metro:SS")
			if len(arrivals) != 1 || arrivals[0].Headsign != "Santa Apolónia" || arrivals[0].RouteId != "" {
				t.Fatalf("order=%v topology=%v: conflicting route = %+v", order, withTopology, arrivals)
			}
			if arrivals[0].ObservedAt == nil || !arrivals[0].ObservedAt.Equal(now) || arrivals[0].ExpectedAt == nil || !arrivals[0].ExpectedAt.Equal(now.Add(120*time.Second)) {
				t.Fatal("conflicting route changed the official prediction clock")
			}
		}
	}
}

func TestMetroArrivalsFenceUnavailableAndReplacedScheduleTopology(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	data, static := metroPlatformFixture()
	static.Routes[0].ShortName = "Az"
	for i := range data.Stations {
		data.Stations[i].Lines = "[Azul,Vermelha]"
	}
	data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}
	data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
	data.Waits = []MetroWait{{Stop: "SS", At: now.In(lisbon).Format("20060102150405"), Train: "7", Destination: "new", Wait1: json.RawMessage("120")}}
	r := newMetroRuntime()
	r.observe(data, static, nil, now)
	assertArrival := func(data *MetroData, plan *StaticData, wantRoute string) {
		t.Helper()
		state := &State{Created: now, Metro: data, Static: map[string]*StaticData{"metro": plan}}
		arrivals := predictedArrivals(state, now, now.Add(time.Hour), "", "metro:SS")
		if len(arrivals) != 1 || arrivals[0].Headsign != "Santa Apolónia" || arrivals[0].RouteId != wantRoute {
			t.Fatalf("route = %+v, want %q", arrivals, wantRoute)
		}
		if arrivals[0].ObservedAt == nil || !arrivals[0].ObservedAt.Equal(now) || arrivals[0].ExpectedAt == nil || !arrivals[0].ExpectedAt.Equal(now.Add(120*time.Second)) {
			t.Fatal("schedule change altered the original official clock")
		}
	}
	assertArrival(data, static, "metro:4_0")
	missing := *static
	missing.Schedule = nil
	assertArrival(data, &missing, "") // static replacement before the next Metro observation
	replacement := *static
	assertArrival(data, &replacement, "") // a different usable plan also needs fresh topology
	current := *data
	r.observe(&current, &missing, nil, now)
	if len(current.Topology.Patterns) != 0 || current.topologyPlan != nil || len(r.topology.Patterns) == 0 {
		t.Fatal("unavailable schedule published retained runtime topology")
	}
	view, _, _, _ := r.view(now)
	if len(view.Topology.Patterns) != 0 || view.topologyPlan != nil {
		t.Fatal("unavailable runtime view published retained topology")
	}
	assertArrival(&current, &missing, "")
	r.observe(&current, &replacement, nil, now)
	assertArrival(&current, &replacement, "metro:4_0")
}
