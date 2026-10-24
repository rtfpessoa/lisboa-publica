package app

import (
	"encoding/json"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

// These synthetic waits use both occurrences of Lisbon's repeated 01:30.
func TestMetroAmbiguousSourceClockAcrossLiveSurfaces(t *testing.T) {
	tests := []struct {
		name string
		at   string
		now  time.Time
		want bool
	}{
		{"before repeated hour", "20261025003000", time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC), true},
		{"last unique autumn second", "20261025005959", time.Date(2026, 10, 24, 23, 59, 59, 0, time.UTC), true},
		{"first repeated second", "20261025010000", time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), false},
		{"first occurrence", "20261025013000", time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC), false},
		{"second occurrence or stale first", "20261025013000", time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), false},
		{"last repeated second", "20261025015959", time.Date(2026, 10, 25, 1, 59, 59, 0, time.UTC), false},
		{"first unique autumn second", "20261025020000", time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC), true},
		{"after repeated hour", "20261025023000", time.Date(2026, 10, 25, 2, 30, 0, 0, time.UTC), true},
		{"nonexistent spring hour", "20260329013000", time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, static := metroPlatformFixture()
			static.Routes[0].ShortName, static.Routes[0].LongName = "Az", "Linha Azul"
			for i := range data.Stations {
				data.Stations[i].Lines = "[Azul]"
			}
			data.Destinations = []MetroDestination{{ID: "33", Name: "Santa Apolónia"}}
			data.Status = api.MetroStatus{Status: "ok", CheckedAt: &tc.now}
			data.Waits = []MetroWait{{Stop: "SS", Platform: "1", At: tc.at, Train: "7", Destination: "33", Wait1: json.RawMessage("120")}}
			data.Topology = metroTopology(data, static)
			data.topologyPlan = static
			state := &State{Created: tc.now, Metro: data, Static: map[string]*StaticData{"metro": static}}
			arrivals := predictedArrivals(state, tc.now, tc.now.Add(time.Hour), "", "metro:SS")
			if got := len(arrivals) != 0; got != tc.want {
				t.Errorf("generic arrivals available = %v, want %v", got, tc.want)
			}
			if len(arrivals) > 0 && (arrivals[0].ObservedAt == nil || !arrivals[0].ObservedAt.Equal(tc.now) || arrivals[0].ExpectedAt == nil || !arrivals[0].ExpectedAt.Equal(tc.now.Add(120*time.Second))) {
				t.Error("valid arrival did not retain its original source clock")
			}
			if got := len(latestMetroPublications(data.Waits, "SS", tc.now)) != 0; got != tc.want {
				t.Errorf("publication available = %v, want %v", got, tc.want)
			}
			if got := metroVehicleDestination(state, &api.Vehicle{SourceId: "7"}, tc.now) != ""; got != tc.want {
				t.Errorf("vehicle destination available = %v, want %v", got, tc.want)
			}
			batch := collectMetroPoints(data, data.Topology, static, tc.now)
			if got := len(batch.groups) != 0 || len(batch.localOnly) != 0; got != tc.want {
				t.Errorf("live wait available = %v, want %v", got, tc.want)
			}
			if got := len(metroOfficialCache(state, "SS", tc.now)) != 0; got != tc.want {
				t.Errorf("official cache available = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMetroAmbiguousSourceClockSuspendsLiveJourney(t *testing.T) {
	_, static, data, _ := metroLiveFixture(t)
	before := time.Date(2026, 10, 24, 23, 59, 50, 0, time.UTC)
	data.Waits = []MetroWait{metroTestRow(before, "RM", "7", "10"), metroTestRow(before, "CS", "7", "120")}
	r := newMetroRuntime()
	r.observe(data, static, nil, before)
	if len(data.Trains) != 1 || data.Trains[0].Association != "supported" {
		t.Fatalf("missing initial journey: %+v", data.Trains)
	}
	id := data.Trains[0].JourneyId
	fold := before.Add(10 * time.Second)
	next := *data
	next.Waits = []MetroWait{metroTestRow(fold, "RM", "7", "0"), metroTestRow(fold, "CS", "7", "100")}
	r.observe(&next, static, nil, fold)
	if len(next.Trains) != 1 || next.Trains[0].JourneyId != id || next.Trains[0].Association != "suspended" || next.Trains[0].Calls[0].Arrival.Inferred != nil {
		t.Fatalf("ambiguous wait retained support or emitted an event: %+v", next.Trains)
	}
}
