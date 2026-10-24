package patterns

import (
	"reflect"
	"testing"
	"time"
)

func TestCloneMetroEngineIsolatesMutableState(t *testing.T) {
	now := time.Now()
	value := 5.0
	f := Forecast{OwnValidUntil: &now, SourceAt: &now, OfficialAt: &now, OwnAt: &now, LowerAt: &now, UpperAt: &now, ReferenceLower: &now, ReferenceUpper: &now, ErrorLower: &value, ErrorUpper: &value, Components: []Component{{Origin: "A"}}}
	original := &engine{
		ColdDays: map[string]bool{"a": true}, RecoveryThrough: map[string]int64{"a": 2},
		Conditions: map[string]string{"a": "b"}, ReportSeen: map[string]int64{"a": 3},
		Aggregates: map[string]Aggregate{"a": {Count: 1}}, Previous: map[string]priorRow{"a": {Train: "t"}},
		Selections: map[string]selection{"a": {Episode: "e"}}, DirtyDays: map[string]bool{"a": true},
		Groups:   map[string]*group{"a": {Signals: []signal{{Stop: "a"}}, Pairs: map[string]bool{"a": true}}, "nil": nil},
		Topology: Topology{Segments: map[string]string{"a": "b"}, Stations: []Station{{ID: "a"}}, Patterns: []Pattern{{Stops: []string{"a"}, Sequences: []int{1}}}},
		Cases:    []Forecast{f}, Live: []Forecast{f}, Outcomes: []Forecast{f}, Replaying: true,
		LastMetroStats: MetroSampleStats{Gaps: 42},
	}
	expected := cloneMetroEngine(original)
	candidate := cloneMetroEngine(original)
	if !reflect.DeepEqual(original, candidate) {
		t.Fatal("clone omitted state, including process-local fields")
	}
	candidate.ColdDays["a"] = false
	candidate.RecoveryThrough["a"]++
	candidate.Conditions["a"] = "changed"
	candidate.ReportSeen["a"]++
	candidate.Aggregates["a"] = Aggregate{Count: 2}
	candidate.Previous["a"] = priorRow{Train: "changed"}
	candidate.Selections["a"] = selection{Episode: "changed"}
	candidate.DirtyDays["a"] = false
	candidate.Groups["a"].Signals[0].Stop = "changed"
	candidate.Groups["a"].Pairs["a"] = false
	candidate.Topology.Segments["a"] = "changed"
	candidate.Topology.Stations[0].ID = "changed"
	candidate.Topology.Patterns[0].Stops[0] = "changed"
	candidate.Topology.Patterns[0].Sequences[0]++
	for _, list := range [][]Forecast{candidate.Cases, candidate.Live, candidate.Outcomes} {
		forecast := &list[0]
		*forecast.OwnValidUntil = time.Time{}
		*forecast.SourceAt = time.Time{}
		*forecast.OfficialAt = time.Time{}
		*forecast.OwnAt = time.Time{}
		*forecast.LowerAt = time.Time{}
		*forecast.UpperAt = time.Time{}
		*forecast.ReferenceLower = time.Time{}
		*forecast.ReferenceUpper = time.Time{}
		*forecast.ErrorLower = 0
		*forecast.ErrorUpper = 0
		forecast.Components[0].Origin = "changed"
	}
	if !reflect.DeepEqual(original, expected) {
		t.Fatal("mutating the candidate changed admitted engine state")
	}
}
