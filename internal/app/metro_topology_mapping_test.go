package app

import (
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func metroPlatformFixture() (*MetroData, *StaticData) {
	now := time.Now().UTC()
	data := &MetroData{Stations: []MetroStation{
		{ID: "RB", Name: "Reboleira", Lat: "38.75", Lon: "-9.22"},
		{ID: "SP", Name: "Santa Apolónia", Lat: "38.71", Lon: "-9.12"},
		{ID: "SS", Name: "São Sebastião", Lat: "38.71", Lon: "-9.14"},
	}}
	static := &StaticData{
		PlanID:  "synthetic",
		Updated: now,
		Routes:  []api.RouteDetail{{Id: "metro:4_0", SourceId: "4_0", ShortName: "Vm", LongName: "Linha Vermelha"}},
		Stops: []api.Stop{
			{Id: "metro:a", SourceId: "a", Name: "Reboleira", Lat: 38.75, Lon: -9.22},
			{Id: "metro:bx", SourceId: "bx", Name: "Santa Apolónia", Lat: 38.71, Lon: -9.12},
			{Id: "metro:p", SourceId: "p", Name: "São Sebastião", Lat: 38.71, Lon: -9.14},
			{Id: "metro:p1", SourceId: "p1", Name: "São Sebastião", Lat: 38.7102, Lon: -9.1402, ParentId: ptr("metro:p")},
		},
		Schedule: &Schedule{
			Parents: map[string]string{"p1": "p"},
			Trips: []ScheduledTrip{
				{ID: "out", Route: "4_0", Headsign: "Santa Apolónia", Times: []StopTime{{Stop: "a", Sequence: 1}, {Stop: "p1", Sequence: 2, Arrival: 60, Departure: 60}, {Stop: "bx", Sequence: 3, Arrival: 120, Departure: 120}}},
				{ID: "in", Route: "4_0", Headsign: "Reboleira", Times: []StopTime{{Stop: "bx", Sequence: 1}, {Stop: "p1", Sequence: 2, Arrival: 60, Departure: 60}, {Stop: "a", Sequence: 3, Arrival: 120, Departure: 120}}},
			},
		},
	}
	return data, static
}

// A GTFS revision that references platform-level stops must resolve them through their
// station family and rebuild every direction, with no legacy code crosswalk.
func TestMetroTopologyResolvesPlatformStopsThroughParents(t *testing.T) {
	data, static := metroPlatformFixture()
	topology, unmapped := metroTopologyReport(data, static)
	if len(unmapped) != 0 {
		t.Fatalf("unexpected unmapped stops: %v", unmapped)
	}
	found := map[string][]string{}
	for _, path := range topology.Patterns {
		found[path.Route+"|"+path.Direction] = path.Stops
	}
	want := map[string][]string{
		"metro:4_0|42": {"RB", "SS", "SP"},
		"metro:4_0|33": {"SP", "SS", "RB"},
	}
	for key, stops := range want {
		got := found[key]
		if len(got) != len(stops) {
			t.Fatalf("%s stops %v, want %v", key, got, stops)
		}
		for n := range stops {
			if got[n] != stops[n] {
				t.Fatalf("%s stops %v, want %v", key, got, stops)
			}
		}
	}
}

// An unresolved station family must be reported instead of silently shrinking the
// topology.
func TestMetroTopologyReportsUnmappedStops(t *testing.T) {
	data, static := metroPlatformFixture()
	data.Stations = data.Stations[:2] // drop São Sebastião from the published catalogue
	topology, unmapped := metroTopologyReport(data, static)
	if len(topology.Patterns) != 0 {
		t.Fatalf("patterns built with an unmapped platform: %+v", topology.Patterns)
	}
	found := false
	for _, id := range unmapped {
		if id == "p1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unmapped stops %v do not include the platform", unmapped)
	}
}