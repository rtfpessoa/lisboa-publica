package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"lisboapublica/internal/patterns"
)

// This is the pre-optimization resolver; keep its ordered sentinel behavior.
func legacyMetroTripDestination(data *MetroData, trip ScheduledTrip) (string, string) {
	headsign, matches := "", 0
	for _, station := range data.Stations {
		if normalizeName(trip.Headsign) == normalizeName(station.Name) {
			headsign = station.ID
			matches++
		}
	}
	if matches != 1 {
		return "", ""
	}
	direction := ""
	for _, destination := range data.Destinations {
		if normalizeName(destination.Name) == normalizeName(trip.Headsign) {
			if direction != "" && direction != destination.ID {
				return "", ""
			}
			direction = destination.ID
		}
	}
	if direction != "" {
		return headsign, direction
	}
	for code, id := range destinations {
		if id == headsign {
			direction = code
		}
	}
	return headsign, direction
}

func TestMetroNormalizedDestinationMatchesLegacyResolver(t *testing.T) {
	station := MetroStation{ID: "CS", Name: "Cais do Sodré"}
	tests := []struct {
		name         string
		stations     []MetroStation
		destinations []MetroDestination
		headsign     string
		wantStop     string
		wantDir      string
	}{
		{"case and accent", []MetroStation{station}, nil, "CAIS DO SODRE", "CS", "54"},
		{"decomposed unicode", []MetroStation{station}, []MetroDestination{{ID: "x", Name: "cais do sodre\u0301"}}, "Cais do Sodre\u0301", "CS", "x"},
		{"whitespace is significant", []MetroStation{station}, nil, " Cais do Sodré", "", ""},
		{"empty name", []MetroStation{{ID: "CS", Name: ""}}, nil, "", "CS", "54"},
		{"space name", []MetroStation{{ID: "CS", Name: " "}}, nil, " ", "CS", "54"},
		{"no station", []MetroStation{station}, nil, "Other", "", ""},
		{"duplicate same station id", []MetroStation{station, station}, nil, station.Name, "", ""},
		{"duplicate different station ids", []MetroStation{station, {ID: "other", Name: station.Name}}, nil, station.Name, "", ""},
		{"conflicting destination ids", []MetroStation{station}, []MetroDestination{{ID: "x", Name: station.Name}, {ID: "y", Name: station.Name}}, station.Name, "", ""},
		{"empty station id", []MetroStation{{ID: "", Name: station.Name}}, []MetroDestination{{ID: "x", Name: station.Name}}, station.Name, "", "x"},
		{"empty destination id", []MetroStation{station}, []MetroDestination{{ID: "", Name: station.Name}}, station.Name, "CS", "54"},
		{"irrelevant destination", []MetroStation{station}, []MetroDestination{{ID: "x", Name: "Other"}}, station.Name, "CS", "54"},
		{"unknown legacy station", []MetroStation{{ID: "unknown", Name: station.Name}}, nil, station.Name, "unknown", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := &MetroData{Stations: tc.stations, Destinations: tc.destinations}
			trip := ScheduledTrip{Headsign: tc.headsign}
			oldStop, oldDir := legacyMetroTripDestination(data, trip)
			if oldStop != tc.wantStop || oldDir != tc.wantDir {
				t.Fatalf("legacy result %q/%q, want %q/%q", oldStop, oldDir, tc.wantStop, tc.wantDir)
			}
			for _, prepared := range []bool{false, true} {
				b := metroPlanBuilder{data: data}
				if prepared {
					b.prepareNames()
				}
				stop, direction := b.tripDestination(trip)
				if stop != oldStop || direction != oldDir {
					t.Fatalf("prepared=%v: got %q/%q, want %q/%q", prepared, stop, direction, oldStop, oldDir)
				}
			}
		})
	}
}

func TestMetroCapturedTopologyNormalizationGolden(t *testing.T) {
	var captures []struct {
		Data MetroData `json:"data"`
	}
	var static StaticData
	capturedMetroJSON(t, "metro-debug-direct-capture.json", &captures)
	capturedMetroJSON(t, "metro-debug-static.json", &static)
	if len(captures) != 8 || static.Schedule == nil || len(static.Schedule.Trips) != 2527 || len(captures[0].Data.Stations) != 50 || len(captures[0].Data.Waits) != 111 {
		t.Fatal("captured fixture dimensions changed")
	}
	topology, unmapped := metroTopologyReport(&captures[0].Data, &static)
	raw, err := json.Marshal(struct {
		Topology patterns.Topology
		Unmapped []string
	}{topology, unmapped})
	if err != nil {
		t.Fatal(err)
	}
	const before = "9e98ccd627fe5446fbc97a811c6f3299784fc030f3e8079b5818ce98e42fdade"
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != before {
		t.Fatalf("captured topology changed: %s", got)
	}
}

// The pre-change trip loop is kept as an independent resolver oracle.
func legacyMetroTopology(data *MetroData, static *StaticData) (patterns.Topology, []string) {
	b := metroPlanBuilder{topology: patterns.Topology{Patterns: []patterns.Pattern{}, Segments: map[string]string{}, Stations: []patterns.Station{}}, data: data, static: static, stations: map[string]MetroStation{}, mapping: map[string]string{}, routes: map[string]string{}, seen: map[string]bool{}}
	b.addStations()
	if static == nil || static.Schedule == nil {
		return b.topology, b.unmapped
	}
	b.mapStaticPlan()
	for _, trip := range static.Schedule.Trips {
		legacyMetroAddTrip(&b, trip)
	}
	b.finish()
	return b.topology, b.unmapped
}

func legacyMetroAddTrip(b *metroPlanBuilder, trip ScheduledTrip) {
	route := b.routes[trip.Route]
	headsign, direction := legacyMetroTripDestination(b.data, trip)
	if route == "" || direction == "" {
		return
	}
	p := patterns.Pattern{Route: route, Direction: direction, Destination: headsign, Stops: []string{}, Sequences: []int{}}
	times := append([]StopTime{}, localJourneyTimes(&trip)...)
	sort.Slice(times, func(i, j int) bool { return times[i].Sequence < times[j].Sequence })
	if !b.addVisits(&p, times) {
		return
	}
	b.addSegments(p, plannedSegmentEvidence(b.static, trip, times))
	raw, _ := json.Marshal(p)
	if key := string(raw); !b.seen[key] {
		b.seen[key] = true
		b.topology.Patterns = append(b.topology.Patterns, p)
	}
}

func TestMetroNormalizedTopologyMatchesLegacySnapshots(t *testing.T) {
	tests := []struct {
		name   string
		change func(*MetroData, *StaticData) *StaticData
	}{
		{"unchanged", func(_ *MetroData, s *StaticData) *StaticData { return s }},
		{"nil static", func(_ *MetroData, _ *StaticData) *StaticData { return nil }},
		{"nil schedule", func(_ *MetroData, s *StaticData) *StaticData { s.Schedule = nil; return s }},
		{"empty trips", func(_ *MetroData, s *StaticData) *StaticData { s.Schedule.Trips = nil; return s }},
		{"unknown route", func(_ *MetroData, s *StaticData) *StaticData { s.Schedule.Trips[0].Route = "unknown"; return s }},
		{"reordered stations and destinations", func(d *MetroData, s *StaticData) *StaticData {
			d.Stations[0], d.Stations[1] = d.Stations[1], d.Stations[0]
			d.Destinations = []MetroDestination{{ID: "x", Name: "Santa Apolónia"}, {ID: "y", Name: "Other"}}
			return s
		}},
		{"changed station id and name", func(d *MetroData, s *StaticData) *StaticData {
			d.Stations[1].ID, d.Stations[1].Name = "changed", "Other"
			return s
		}},
		{"changed destination", func(d *MetroData, s *StaticData) *StaticData {
			d.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}}
			return s
		}},
		{"ambiguous destination", func(d *MetroData, s *StaticData) *StaticData {
			d.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}, {ID: "other", Name: "Santa Apolónia"}}
			return s
		}},
		{"same plan changed static", func(_ *MetroData, s *StaticData) *StaticData { s.Schedule.Trips[0].Times[1].Sequence = 4; return s }},
		{"missing parent", func(_ *MetroData, s *StaticData) *StaticData { s.Schedule.Parents["a"] = "missing"; return s }},
		{"unmapped stop", func(d *MetroData, s *StaticData) *StaticData { d.Stations = d.Stations[:2]; return s }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, static := metroPlatformFixture()
			static = tc.change(data, static)
			want, missing := legacyMetroTopology(data, static)
			got, gotMissing := metroTopologyReport(data, static)
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotMissing, missing) {
				t.Fatal("topology or unmapped stops differ from the original resolver")
			}
		})
	}
}
