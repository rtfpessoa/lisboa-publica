package patterns

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestKnownIntermediateLossNeverSurvivesSampling(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	topology := testTopology()
	e := newEngine()
	e.Profile = topology.Profile
	e.Condition = "reported_normal"
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	g := &group{ID: "episode", Train: "x", Route: "line", Direction: "d", Active: true}
	key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
	e.Groups[key] = g
	e.observeIntermediate(testReceipt(base.Add(5*time.Second)), topology, c)
	if len(e.Groups) != 0 {
		t.Fatal("known intermediate absence retained support")
	}
	e.observeIntermediate(testReceipt(base.Add(10*time.Second), testRow("A", "x", base.Add(10*time.Second), 0)), topology, c)
	if len(e.Groups) != 0 {
		t.Fatal("return before next archive sample restored support")
	}
	if len(e.Aggregates) != 0 {
		t.Fatal("intermediate check created training samples")
	}
}

// An unsampled loss at one station cannot be bridged by the train's presence
// elsewhere, and an unrelated train's loss must not erase a valid baseline.
func TestIntermediateStationContinuityIsScoped(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	groupFor := func(e *engine, train string) string {
		key := groupKey(groupIdentity{Train: train, Direction: "d", Route: "line"})
		e.Groups[key] = &group{ID: train, Train: train, Direction: "d", Route: "line"}
		return key
	}
	t.Run("station missing while train remains elsewhere", func(t *testing.T) {
		e := newEngine()
		e.step(testReceipt(base, testRow("A", "x", base, 10)), topology, config)
		key := groupFor(e, "x")
		mid := base.Add(10 * time.Second)
		e.observeIntermediate(testReceipt(mid, testRow("B", "x", mid, 100)), topology, config)
		if e.Groups[key] != nil || len(e.Previous) != 0 {
			t.Fatal("lost station retained its prior episode")
		}
		at := base.Add(30 * time.Second)
		e.step(testReceipt(at, testRow("A", "x", at, 0), testRow("B", "x", at, 80)), topology, config)
		if e.LastMetroStats.Signals != 0 {
			t.Fatal("reused station baseline created a signal")
		}
	})
	t.Run("unrelated loss preserves supported baseline", func(t *testing.T) {
		e := newEngine()
		e.step(testReceipt(base, testRow("A", "x", base, 10), testRow("B", "y", base, 40)), topology, config)
		x, y := groupFor(e, "x"), groupFor(e, "y")
		mid := base.Add(10 * time.Second)
		e.observeIntermediate(testReceipt(mid, testRow("A", "x", mid, 5)), topology, config)
		if e.Groups[x] == nil || e.Groups[y] != nil || len(e.Previous) != 1 || e.IntermediateCuts != 1 {
			t.Fatalf("unrelated loss cut valid support: groups=%v previous=%v cuts=%d", e.Groups, e.Previous, e.IntermediateCuts)
		}
		at := base.Add(30 * time.Second)
		e.step(testReceipt(at, testRow("A", "x", at, 0)), topology, config)
		if e.LastMetroStats.Signals != 1 {
			t.Fatalf("valid transition lost: %+v", e.LastMetroStats)
		}
	})
}

func TestIntermediatePriorNeedsSameValidFirstSlot(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	mid := base.Add(10 * time.Second)
	good := testRow("A", "x", mid, 5)
	tests := []struct {
		name string
		rows []Row
		keep bool
	}{
		{"fresh", []Row{good}, true},
		{"unchanged original", []Row{testRow("A", "x", base, 10)}, true},
		{"missing", nil, false},
		{"different station", []Row{testRow("B", "x", mid, 5)}, false},
		{"duplicate", []Row{good, good}, false},
		{"different first slot", []Row{testRow("A", "y", mid, 5)}, false},
		{"slot moved", []Row{func() Row { row := good; row.Train, row.Train2, row.ETA2 = "y", "x", json.RawMessage("5"); return row }()}, false},
		{"repeated reference", []Row{func() Row { row := good; row.Train2, row.ETA2 = "x", json.RawMessage("5"); return row }()}, false},
		{"invalid eta", []Row{func() Row { row := good; row.ETA = json.RawMessage("null"); return row }()}, false},
		{"invalid clock", []Row{func() Row { row := good; row.Clock = "invalid"; return row }()}, false},
		{"future clock", []Row{testRow("A", "x", mid.Add(5*time.Second), 5)}, false},
		{"stale clock", []Row{testRow("A", "x", base.Add(-2*time.Minute), 5)}, false},
		{"regressed clock", []Row{testRow("A", "x", base.Add(-time.Second), 5)}, false},
		{"equal changed clock", []Row{testRow("A", "x", base, 5)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine()
			e.step(testReceipt(base, testRow("A", "x", base, 10)), topology, config)
			// No group yet: initial baselines still require intermediate validation.
			e.observeIntermediate(testReceipt(mid, tc.rows...), topology, config)
			if got := len(e.Previous) != 0; got != tc.keep {
				t.Fatalf("baseline retained=%t, want %t", got, tc.keep)
			}
			at := base.Add(30 * time.Second)
			e.step(testReceipt(at, testRow("A", "x", at, 0)), topology, config)
			want := 0
			if tc.keep {
				want = 1
			}
			if e.LastMetroStats.Signals != want {
				t.Fatalf("signals=%d, want %d", e.LastMetroStats.Signals, want)
			}
		})
	}
}

func TestIntermediateCutLosesOnlyAffectedPendingCase(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	e.step(testReceipt(base, testRow("A", "x", base, 10), testRow("B", "y", base, 20)), topology, config)
	for _, train := range []string{"x", "y"} {
		key := groupKey(groupIdentity{Train: train, Direction: "d", Route: "line"})
		e.Groups[key] = &group{ID: train, Train: train, Route: "line", Direction: "d"}
		e.Cases = append(e.Cases, Forecast{ID: train, Episode: train, Route: "line", Direction: "d", Profile: topology.Profile, IssuedAt: base})
	}
	mid := base.Add(10 * time.Second)
	e.observeIntermediate(testReceipt(mid, testRow("B", "y", mid, 10)), topology, config)
	// Repeated absence is one cut; no outcome is manufactured before a sample.
	e.observeIntermediate(testReceipt(mid.Add(time.Second), testRow("B", "y", mid.Add(time.Second), 9)), topology, config)
	if e.IntermediateCuts != 1 || len(e.Outcomes) != 0 {
		t.Fatalf("unexpected repeated cut or early outcome: %d %v", e.IntermediateCuts, e.Outcomes)
	}
	at := base.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("B", "y", at, 0)), topology, config)
	if len(e.Outcomes) != 1 || e.Outcomes[0].Episode != "x" || e.Outcomes[0].Result != "lost_support" || len(e.Cases) != 1 || e.Cases[0].Episode != "y" {
		t.Fatalf("wrong case outcomes: outcomes=%+v cases=%+v", e.Outcomes, e.Cases)
	}
}

func TestIntermediateConditionChangeRemovesLiveAssociation(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	first := testReceipt(base, testRow("A", "x", base, 10))
	first.RouteConditions = map[string]string{"line": "reported_normal"}
	e.step(first, topology, config)
	key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
	e.Groups[key] = &group{ID: "old-episode", Train: "x", Direction: "d", Route: "line"}
	e.Live = []Forecast{{Episode: "old-episode"}}
	at := base.Add(10 * time.Second)
	changed := testReceipt(at, testRow("A", "x", at, 5))
	changed.RouteConditions = map[string]string{"line": "reported_disruption"}
	e.observeIntermediate(changed, topology, config)
	if e.Groups[key] != nil {
		t.Fatal("condition change retained the old group")
	}
	for _, forecast := range e.Live {
		if forecast.Episode == "old-episode" {
			t.Fatal("condition change retained a live forecast from the old group")
		}
	}
}

func TestIntermediateStationLossCannotEvaluateOldCaseAfterReferenceReturns(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	seedCausalTriples(t, e, base, topology, config)
	g := e.Groups["x|d|line"]
	if g == nil || !g.Active {
		t.Fatal("missing supported episode")
	}
	e.Cases = append(e.Cases, Forecast{ID: "old-D", Episode: g.ID, IssuedAt: base.Add(155 * time.Second), Route: "line", Direction: "d", Stop: "D", Platform: "1", Train: "x", Profile: topology.Profile, Function: "official"})
	mid := base.Add(160 * time.Second)
	e.observeIntermediate(testReceipt(mid,
		testRow("A", "x", mid, 0), testRow("B", "x", mid, 0), testRow("C", "x", mid, 0),
		testRow("E", "x", mid, 600), testRow("F", "x", mid, 600)), topology, config)
	if e.Groups["x|d|line"] != nil {
		t.Fatal("station loss retained the previous episode")
	}
	at := base.Add(180 * time.Second)
	e.step(testReceipt(at, testRow("C", "x", at, 0), testRow("D", "x", at, 600)), topology, config)
	seen := 0
	for _, outcome := range e.Outcomes {
		if outcome.ID == "old-D" {
			seen++
			if outcome.Result != "lost_support" || outcome.ReferenceLower != nil {
				t.Fatalf("old case acquired post-gap reference: %+v", outcome)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("old case lost-support outcomes=%d", seen)
	}
	at = base.Add(210 * time.Second)
	e.step(testReceipt(at, testRow("C", "x", at, 0), testRow("D", "x", at, 0)), topology, config)
	for _, outcome := range e.Outcomes {
		if outcome.ID == "old-D" {
			t.Fatal("later reference produced a second outcome for the old case")
		}
	}
}

func TestIntermediateClocksAreIsolatedAndResetOnRestart(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(-30 * time.Minute)
	topology := testTopology()
	if err = s.Record(testReceipt(base, testRow("A", "x", base, 100)), topology); err != nil {
		t.Fatal(err)
	}
	intermediate := base.Add(10 * time.Second)
	if err = s.Record(testReceipt(intermediate, testRow("A", "x", intermediate, 0)), topology); err != nil {
		t.Fatal(err)
	}
	key := "A|1|d"
	prior := s.engine.Previous[key]
	if !prior.IntermediateZero.Equal(intermediate) || !prior.IntermediateClock.Equal(intermediate) {
		t.Fatal("intermediate zero was not observed")
	}
	checkpoint, err := json.Marshal(s.engine)
	if err != nil || bytes.Contains(checkpoint, []byte("IntermediateZero")) || bytes.Contains(checkpoint, []byte("IntermediateClock")) {
		t.Fatal("process-local evidence was serialized", err)
	}
	candidate := cloneMetroEngine(s.engine)
	at := base.Add(30 * time.Second)
	candidate.step(testReceipt(at, testRow("A", "x", at, 100)), s.topology, config)
	if candidate.LastMetroStats.Signals != 0 || s.engine.Previous[key] != prior {
		t.Fatal("discarded candidate changed the retained intermediate evidence")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.engine.Previous) != 0 || len(s.engine.Groups) != 0 || !s.pendingMetroDeliveryGap {
		t.Fatal("restart preserved intermediate continuity")
	}
}
