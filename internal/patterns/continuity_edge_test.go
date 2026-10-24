package patterns

import (
	"testing"
	"time"
)

func TestMetroProvisionalPassedStationMayContinue(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	rowsAt := func(at time.Time, afterA bool, b, c int) []Row {
		a := "x"
		aETA := 0
		if afterA {
			a = "y"
			aETA = 500
		}
		return []Row{testRow("A", a, at, aETA), testRow("B", "x", at, b), testRow("C", "x", at, c), testRow("D", "x", at, 600), testRow("E", "x", at, 600), testRow("F", "x", at, 600)}
	}
	rows := rowsAt(base, false, 600, 600)
	rows[0] = testRow("A", "x", base, 100)
	e.step(testReceipt(base, rows...), topology, config)
	at := base.Add(30 * time.Second)
	e.step(testReceipt(at, rowsAt(at, false, 600, 600)...), topology, config)
	key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
	g := e.Groups[key]
	if g == nil || g.Active || len(g.Signals) != 1 || g.Signals[0].Supported {
		t.Fatal("missing provisional first signal")
	}
	old := g.ID
	// This case can only evaluate against the original A signal, which is earlier
	// than the departure. Its issuance is synthetic to stress eligibility.
	e.Cases = []Forecast{{ID: "pending-A", Episode: old, Route: "line", Direction: "d", Stop: "A", Platform: "1", Train: "x", Profile: topology.Profile, Function: "official", IssuedAt: base.Add(-time.Second)}}
	at = base.Add(40 * time.Second)
	e.observeIntermediate(testReceipt(at, rowsAt(at, true, 600, 600)...), topology, config)
	if e.Groups[key] == nil {
		t.Fatal("ordinary zero departure lost provisional signal")
	}
	for _, x := range []struct{ s, b, c int }{{60, 600, 600}, {90, 0, 600}, {120, 0, 600}, {150, 0, 0}} {
		at = base.Add(time.Duration(x.s) * time.Second)
		e.step(testReceipt(at, rowsAt(at, true, x.b, x.c)...), topology, config)
	}
	g = e.Groups[key]
	if g == nil || !g.Active {
		t.Fatal("trip failed to reach a causal triple")
	}
	result := "none"
	for _, f := range e.Outcomes {
		if f.ID == "pending-A" {
			result = f.Result
		}
	}
	if result != "evaluated_proxy" {
		t.Fatalf("original signal did not evaluate prior case: %s", result)
	}
	t.Log("provisional passed baseline retained; pending case evaluated against original signal")
}

func TestMetroReturningReferenceFencedBeforeSample(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	for _, sampled := range []bool{false, true} {
		t.Run(map[bool]string{false: "intermediate", true: "sampled"}[sampled], func(t *testing.T) {
			e := newEngine()
			seedCausalTriples(t, e, base, topology, config)
			key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
			old := e.Groups[key].ID
			e.Cases = []Forecast{{ID: "future-D", Episode: old, Route: "line", Direction: "d", Stop: "D", Platform: "1", Train: "x", Profile: topology.Profile, Function: "official", IssuedAt: base.Add(155 * time.Second)}}
			at := base.Add(160 * time.Second)
			e.observeIntermediate(testReceipt(at, testRow("A", "y", at, 500), testRow("B", "x", at, 0), testRow("C", "x", at, 0), testRow("D", "x", at, 600), testRow("E", "x", at, 600), testRow("F", "x", at, 600)), topology, config)
			if e.Groups[key] == nil {
				t.Fatal("ordinary departure cut old group")
			}
			at = base.Add(180 * time.Second)
			r := testReceipt(at, testRow("A", "x", at, 100), testRow("B", "x", at, 0), testRow("C", "x", at, 0), testRow("D", "x", at, 600), testRow("E", "x", at, 600), testRow("F", "x", at, 600))
			if sampled {
				e.step(r, topology, config)
			} else {
				e.observeIntermediate(r, topology, config)
			}
			if e.Groups[key] != nil {
				t.Fatal("returning old reference reused group")
			}
			if !sampled {
				at = at.Add(30 * time.Second)
				e.step(testReceipt(at, testRow("A", "x", at, 100), testRow("B", "x", at, 0), testRow("C", "x", at, 0), testRow("D", "x", at, 600), testRow("E", "x", at, 600), testRow("F", "x", at, 600)), topology, config)
			}
			assertMetroLostCase(t, e, "future-D")
		})
	}
}

func TestMetroZeroToPositiveWithoutDepartureFenced(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	seedCausalTriples(t, e, base, topology, config)
	key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
	at := base.Add(160 * time.Second)
	e.observeIntermediate(testReceipt(at, testRow("A", "x", at, 100), testRow("B", "x", at, 0), testRow("C", "x", at, 0), testRow("D", "x", at, 600), testRow("E", "x", at, 600), testRow("F", "x", at, 600)), topology, config)
	if e.Groups[key] != nil {
		t.Fatal("zero to positive reversal kept group")
	}
}

func TestMetroSampledSameClockConflictDoesNotBecomeNewBaseline(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	e.step(testReceipt(base, testRow("A", "x", base, 100)), topology, config)
	at := base.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("A", "x", base, 200)), topology, config)
	if len(e.Previous) != 0 || e.LastMetroStats.RejectedContinuity != 1 {
		t.Fatal("conflicting sampled row became a new baseline")
	}
	at = base.Add(60 * time.Second)
	e.step(testReceipt(at, testRow("A", "x", at, 0)), topology, config)
	t.Logf("next zero manufactured signals=%d groups=%d", e.LastMetroStats.Signals, len(e.Groups))
	if e.LastMetroStats.Signals != 0 {
		t.Fatalf("same-clock conflicted row created %d signal(s)", e.LastMetroStats.Signals)
	}
}

func TestMetroIntermediateZeroToPositiveWithPositiveSampleBaseline(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	seedCausalTriples(t, e, base, topology, config)
	key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
	g := e.Groups[key]
	e.Cases = []Forecast{{ID: "old-D", Episode: g.ID, Route: "line", Direction: "d", Stop: "D", Platform: "1", Train: "x", Profile: topology.Profile, Function: "official", IssuedAt: base.Add(155 * time.Second)}}
	rows := func(at time.Time, d int) []Row {
		return []Row{testRow("A", "x", at, 0), testRow("B", "x", at, 0), testRow("C", "x", at, 0), testRow("D", "x", at, d), testRow("E", "x", at, 600), testRow("F", "x", at, 600)}
	}
	at := base.Add(180 * time.Second)
	e.step(testReceipt(at, rows(at, 600)...), topology, config)
	at = base.Add(190 * time.Second)
	e.observeIntermediate(testReceipt(at, rows(at, 0)...), topology, config)
	at = base.Add(200 * time.Second)
	e.observeIntermediate(testReceipt(at, rows(at, 600)...), topology, config)
	at = base.Add(210 * time.Second)
	e.step(testReceipt(at, rows(at, 0)...), topology, config)
	assertMetroLostCase(t, e, "old-D")
}

func assertMetroLostCase(t *testing.T, e *engine, id string) {
	t.Helper()
	count := 0
	for _, f := range e.Outcomes {
		if f.ID == id {
			count++
			if f.Result != "lost_support" || f.ReferenceLower != nil {
				t.Fatalf("old case acquired unsafe outcome: %+v", f)
			}
		}
	}
	for _, f := range e.Cases {
		if f.ID == id {
			t.Fatal("old case remained pending")
		}
	}
	if count != 1 {
		t.Fatalf("old case produced %d outcomes", count)
	}
}

func TestMetroDepartedStationReferenceReturnsOnOtherOrUnknownPlatform(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	for _, platform := range []string{"2", ""} {
		t.Run(map[string]string{"2": "other-platform", "": "unknown-platform"}[platform], func(t *testing.T) {
			e := newEngine()
			seedCausalTriples(t, e, base, topology, config)
			key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
			old := e.Groups[key].ID
			e.Cases = []Forecast{{ID: "old-D", Episode: old, Route: "line", Direction: "d", Stop: "D", Platform: "1", Train: "x", Profile: topology.Profile, Function: "official", IssuedAt: base.Add(155 * time.Second)}}
			rows := func(at time.Time, d int) []Row {
				return []Row{testRow("A", "y", at, 500), testRow("B", "x", at, 0), testRow("C", "x", at, 0), testRow("D", "x", at, d), testRow("E", "x", at, 600), testRow("F", "x", at, 600)}
			}
			at := base.Add(160 * time.Second)
			e.observeIntermediate(testReceipt(at, rows(at, 600)...), topology, config)
			if e.Groups[key] == nil {
				t.Fatal("prior zero departure unexpectedly cut group")
			}
			at = base.Add(170 * time.Second)
			r := rows(at, 600)
			returned := testRow("A", "x", at, 100)
			returned.Platform = platform
			r = append(r, returned)
			e.observeIntermediate(testReceipt(at, r...), topology, config)
			t.Logf("train returns to already passed A on platform %q; old group active=%v", platform, e.Groups[key] != nil && e.Groups[key].Active)
			if e.Groups[key] != nil {
				t.Fatal("ambiguous returned stop retained old group")
			}
		})
	}
}

func TestMetroUnassociatedZeroDepartureDoesNotCountGap(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	for _, sampled := range []bool{false, true} {
		t.Run(map[bool]string{false: "intermediate", true: "sampled"}[sampled], func(t *testing.T) {
			e := newEngine()
			e.step(testReceipt(base, testRow("A", "x", base, 0)), topology, config)
			at := base.Add(10 * time.Second)
			r := testReceipt(at, testRow("A", "y", at, 500))
			if sampled {
				e.step(r, topology, config)
			} else {
				e.observeIntermediate(r, topology, config)
			}
			if e.Gaps != 0 || e.IntermediateCuts != 0 || e.LastMetroStats.Signals != 0 {
				t.Fatalf("ordinary departure counted as gap: gaps=%d cuts=%d", e.Gaps, e.IntermediateCuts)
			}
		})
	}
}

func TestMetroDepartedStationReferenceWithUnknownDirection(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	for _, direction := range []string{"", "unmapped", "bad|direction"} {
		t.Run(direction, func(t *testing.T) {
			e := newEngine()
			seedCausalTriples(t, e, base, topology, config)
			key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
			other := groupKey(groupIdentity{Train: "y", Direction: "d", Route: "line"})
			e.Groups[other] = &group{ID: "unrelated", Train: "y", Direction: "d", Route: "line"}
			rows := func(at time.Time) []Row {
				return []Row{testRow("A", "y", at, 500), testRow("B", "x", at, 0), testRow("C", "x", at, 0), testRow("D", "x", at, 600), testRow("E", "x", at, 600), testRow("F", "x", at, 600)}
			}
			at := base.Add(160 * time.Second)
			e.observeIntermediate(testReceipt(at, rows(at)...), topology, config)
			if e.Groups[key] == nil {
				t.Fatal("departure cut precondition")
			}
			at = base.Add(170 * time.Second)
			r := rows(at)
			returned := testRow("A", "x", at, 100)
			returned.Destination = direction
			r = append(r, returned)
			e.observeIntermediate(testReceipt(at, r...), topology, config)
			if e.Groups[key] != nil {
				t.Fatal("ambiguous direction at departed station retained old group")
			}
			if e.Groups[other] == nil {
				t.Fatal("ambiguous evidence cut an unrelated reference")
			}
		})
	}
}

func TestMetroMalformedSavedContextDoesNotEraseUnrelatedGroup(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	malformed := testRow("A", "x", base, 500)
	malformed.Platform = "1|bad"
	e.step(testReceipt(base, malformed, testRow("B", "y", base, 500)), topology, config)
	key := groupKey(groupIdentity{Train: "y", Direction: "d", Route: "line"})
	e.Groups[key] = &group{ID: "y", Train: "y", Direction: "d", Route: "line"}
	at := base.Add(10 * time.Second)
	malformed = testRow("A", "x", at, 490)
	malformed.Platform = "1|bad"
	e.observeIntermediate(testReceipt(at, malformed, testRow("B", "y", at, 490)), topology, config)
	if e.Groups[key] == nil {
		t.Fatal("unrelated group erased by malformed station/platform key")
	}
}

func TestMetroReservedSeparatorTrainNeverCreatesWrongIdentity(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topology, config := testTopology(), DefaultConfig(t.TempDir())
	e := newEngine()
	e.step(testReceipt(base, testRow("A", "x|evil", base, 100)), topology, config)
	at := base.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("A", "x|evil", at, 0)), topology, config)
	if len(e.Groups) != 0 || len(e.Previous) != 0 || e.LastMetroStats.Signals != 0 {
		t.Fatal("reference with a reserved separator entered inference")
	}
}
