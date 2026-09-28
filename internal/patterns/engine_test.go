package patterns

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func testTopology() Topology {
	return Topology{Profile: "test-profile", Patterns: []Pattern{{Route: "line", Direction: "d", Destination: "F", Stops: []string{"A", "B", "C", "D", "E", "F"}, Sequences: []int{1, 2, 3, 4, 5, 6}}}, Stations: []Station{{"A", "A"}, {"B", "B"}, {"C", "C"}, {"D", "D"}, {"E", "E"}, {"F", "F"}}}
}
func testRow(stop, train string, at time.Time, seconds int) Row {
	return Row{Stop: stop, Platform: "1", Destination: "d", Clock: at.In(lisbon).Format("20060102150405"), Train: train, ETA: json.RawMessage(jsonNumber(seconds))}
}
func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }
func testReceipt(at time.Time, rows ...Row) Receipt {
	return Receipt{ReceivedAt: at.Add(time.Second), Rows: rows, ServiceCondition: "reported_normal"}
}

func TestCausalTriplesAndLoss(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	config := DefaultConfig(t.TempDir())
	topology := testTopology()
	e := newEngine()
	seedCausalTriples(t, e, base, topology, config)
	if len(e.Groups) != 1 {
		t.Fatal("missing continuous episode")
	}
	assertCausalComponentBounds(t, e)
	before := nonReceiptDigest(e)
	// Same receipt and a later repeated-zero clock cannot create another event.
	e.step(testReceipt(base.Add(150*time.Second), testRow("C", "x", base.Add(150*time.Second), 0)), topology, config)
	if nonReceiptDigest(e) != before {
		t.Fatal("duplicate receipt changed aggregates")
	}
	at := base.Add(180 * time.Second)
	e.step(testReceipt(at, testRow("C", "x", at, 0)), topology, config)
	if nonReceiptDigest(e) != before {
		t.Fatal("repeated zero changed aggregates")
	}
	e.step(testReceipt(base.Add(210*time.Second)), topology, config)
	if len(e.Groups) != 0 {
		t.Fatal("presence loss did not cut episode")
	}
	e.step(testReceipt(base.Add(240*time.Second), testRow("C", "x", base.Add(240*time.Second), 0)), topology, config)
	for _, g := range e.Groups {
		if g.Active {
			t.Fatal("isolated return restored support")
		}
	}
}

func TestInvalidSignalsAndClocks(t *testing.T) {
	for _, clock := range []string{"20261025013000", "20260329013000", "garbage"} {
		if _, ok := sourceClock(clock); ok {
			t.Fatalf("accepted ambiguous/nonexistent clock %s", clock)
		}
	}
	for _, value := range []string{"true", "null", "-1", "1e309", "\"bad\""} {
		if _, ok := eta(json.RawMessage(value)); ok {
			t.Fatalf("accepted bad ETA %s", value)
		}
	}
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	c := DefaultConfig(t.TempDir())
	tests := []struct {
		name   string
		change func(Row) []Row
		delta  time.Duration
	}{
		{"initial-zero", func(r Row) []Row { return []Row{r} }, 30 * time.Second},
		{"different-id", func(r Row) []Row { r.Train = "other"; return []Row{r} }, 30 * time.Second},
		{"duplicate-context", func(r Row) []Row { return []Row{r, r} }, 30 * time.Second},
		{"duplicate-slots", func(r Row) []Row { r.Train2 = r.Train; r.ETA2 = json.RawMessage("100"); return []Row{r} }, 30 * time.Second},
		{"same-clock-conflict", func(r Row) []Row { r.Clock = base.In(lisbon).Format("20060102150405"); return []Row{r} }, 30 * time.Second},
		{"receipt-gap", func(r Row) []Row { return []Row{r} }, 90 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := newEngine()
			initial := 100
			if test.name == "initial-zero" {
				initial = 0
			}
			e.step(testReceipt(base, testRow("A", "x", base, initial)), testTopology(), c)
			at := base.Add(test.delta)
			e.step(testReceipt(at, test.change(testRow("A", "x", at, 0))...), testTopology(), c)
			for _, g := range e.Groups {
				if len(g.Signals) != 0 {
					t.Fatal("ineligible signal admitted")
				}
			}
		})
	}
}

func TestTopologyAmbiguityAndContradiction(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	topo := testTopology()
	sig := func(stop string, second int) signal {
		return signal{Stop: stop, L: base.Add(time.Duration(second) * time.Second), U: base.Add(time.Duration(second+10) * time.Second)}
	}
	original := []signal{sig("A", 0), sig("B", 60), sig("C", 120)}
	if len(associate(original, topo, "line", "d")) != 2 {
		t.Fatal("expected adjacent chain")
	}
	if len(associate(append(original, sig("B", 180)), topo, "line", "d")) != 1 {
		t.Fatal("multiple targets were guessed")
	}
	changed := testTopology()
	changed.Patterns[0].Sequences = []int{1, 3, 4, 5, 6, 7}
	if changed.next(plannedOrigin{Stop: "A", Direction: "d", Route: "line"}) != "" {
		t.Fatal("skipped unretained visit")
	}
	changed.Patterns = append(changed.Patterns, Pattern{Route: "line", Direction: "d", Stops: []string{"A", "C"}, Sequences: []int{1, 2}})
	if changed.next(plannedOrigin{Stop: "A", Direction: "d", Route: "line"}) != "" {
		t.Fatal("guessed between conflicting paths")
	}
	overlap := []signal{sig("A", 0), sig("B", 5)}
	if len(associate(overlap, topo, "line", "d")) != 0 {
		t.Fatal("accepted overlapping windows")
	}
	if topo.next(plannedOrigin{Stop: "F", Direction: "d", Route: "line"}) != "" {
		t.Fatal("invented next terminal")
	}
}

func TestSequentialFutureHoursAndOfficialIndependence(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 59, 0, 0, time.UTC)
	c := DefaultConfig(t.TempDir())
	e := newEngine()
	topo := testTopology()
	e.Profile = topo.Profile
	g := &group{ID: "episode", Train: "x", Route: "line", Direction: "d", Active: true, Signals: []signal{{Stop: "A", U: base.Add(-120 * time.Second), Supported: true}, {Stop: "B", U: base.Add(-90 * time.Second), Supported: true}, {Stop: "C", U: base.Add(-60 * time.Second), Supported: true}}}
	e.Groups[groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})] = g
	// D at local 11:01; E at local 12:01. Both component lookups must use
	// their predicted origin hours, not the emission hour (10:59).
	for i, origin := range []string{"D", "E"} {
		at := base.AddDate(0, 0, -7).Add(time.Duration(i+1) * time.Hour)
		a := baseAggregate(aggregateRequest{at, "line", "d", origin, "1", e.Profile, "reported_normal", "component"}, c)
		a.Target = []string{"E", "F"}[i]
		a.TargetPlatform = "1"
		a.Count = 1
		a.Sum = []float64{3600, 120}[i]
		a.KnownAt = base.Add(-time.Hour).UnixNano()
		e.add(a)
	}
	r := testReceipt(base, testRow("D", "x", base, 120), testRow("E", "x", base, 4200), testRow("F", "x", base, 4500))
	forecasts := e.forecasts(r, topo, c)
	var target *Forecast
	for i := range forecasts {
		if forecasts[i].Stop == "F" && forecasts[i].Function == "onward" {
			target = &forecasts[i]
		}
	}
	if target == nil || target.OwnAt == nil || len(target.Components) != 2 {
		t.Fatalf("missing sequential point: %+v", forecasts)
	}
	if target.OwnAt.Sub(base) != 3840*time.Second || target.OfficialAt.Sub(base) != 4500*time.Second {
		t.Fatalf("sum or official changed: %+v", target)
	}
	if target.Components[0].OriginAt.In(lisbon).Hour() != 11 || target.Components[1].OriginAt.In(lisbon).Hour() != 12 {
		t.Fatal("incorrect predicted origin hours")
	}
	// Future knowledge and incompatible service conditions cannot train the point.
	for key, a := range e.Aggregates {
		a.KnownAt = r.ReceivedAt.UnixNano()
		e.Aggregates[key] = a
	}
	for _, f := range e.forecasts(r, topo, c) {
		if f.OwnAt != nil {
			t.Fatal("trained on knowledge at/after issuance")
		}
		if f.Stop == "F" && f.OfficialAt == nil {
			t.Fatal("own unavailability hid official")
		}
	}
}

func TestCalibrationSelectionAndProxyBounds(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	e := newEngine()
	c := DefaultConfig(t.TempDir())
	e.Profile = "p"
	point := base.Add(4 * time.Minute)
	official := base.Add(3 * time.Minute)
	f := Forecast{Episode: "trip", Stop: "E", Route: "line", Direction: "d", Function: "waiting", Mode: forecastMode, Profile: "p", Condition: "reported_normal", IssuedAt: base, OwnAt: &point, OfficialAt: &official, Selected: true}
	update := f
	update.IssuedAt = base.Add(time.Minute)
	update.Selected = false
	if selectionKey(f) != selectionKey(update) {
		t.Fatal("same horizon update changed selection")
	}
	e.Cases = []Forecast{f, update}
	g := &group{ID: "trip", Signals: []signal{{Stop: "E", L: point.Add(-10 * time.Second), U: point.Add(20 * time.Second)}}}
	e.evaluateReference(g, g.Signals[0], point.Add(time.Minute), c)
	var scoreCount int64
	for _, a := range e.Aggregates {
		if a.Kind == "calibration:waiting" {
			scoreCount += a.Count
		}
		if a.Kind == "evaluation-own:waiting" && a.Horizon != 0 {
			t.Fatal("comparison used wrong horizon")
		}
	}
	if scoreCount != 1 {
		t.Fatal("calibrated repeated update")
	}
	radius, n := e.radius(f, point.Add(2*time.Minute), c)
	if radius != nil || n != 1 {
		t.Fatal("invented finite small-sample band")
	}
	for i := 0; i < 3; i++ {
		a := baseAggregate(aggregateRequest{base, "line", "d", "E", "", "p", "reported_normal", "calibration:waiting"}, c)
		a.Mode = forecastMode
		a.Horizon = 0
		a.Count = 1
		a.Bucket = 0
		a.KnownAt = base.UnixNano()
		e.add(a)
	}
	radius, n = e.radius(f, point.Add(2*time.Minute), c)
	if radius == nil || *radius != 30 || n != 4 {
		t.Fatalf("wrong corrected rank/bin outer edge: %v %d", radius, n)
	}
	low, high := errorBounds(point, point.Add(-10*time.Second), point.Add(20*time.Second))
	if low != 0 || high != 20 {
		t.Fatal("wrong bounded-reference error")
	}
	if math.IsNaN(high) {
		t.Fatal("invalid score")
	}
}

func TestOfficialNextFutureSurvivesZeroFirstSlot(t *testing.T) {
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	e := newEngine()
	topology := testTopology()
	e.Profile = topology.Profile
	row := testRow("A", "old", base, 0)
	row.Train2 = "next"
	row.ETA2 = json.RawMessage("120")
	row.Train3 = "later"
	row.ETA3 = json.RawMessage("300")
	r := testReceipt(base, row)
	forecasts := e.forecasts(r, topology, DefaultConfig(t.TempDir()))
	if len(forecasts) != 1 || forecasts[0].Function != "waiting" || forecasts[0].Train != "next" || forecasts[0].OfficialAt == nil || forecasts[0].OwnAt != nil {
		t.Fatalf("available next official service hidden by zero first slot: %+v", forecasts)
	}
}

func seedCausalTriples(t *testing.T, e *engine, base time.Time, topology Topology, config Config) {
	t.Helper()
	for i := 0; i < 6; i++ {
		at := base.Add(time.Duration(i) * 30 * time.Second)
		rows := []Row{}
		for j, stop := range []string{"A", "B", "C", "D", "E", "F"} {
			n := 600
			if i >= 2*j+1 {
				n = 0
			}
			rows = append(rows, testRow(stop, "x", at, n))
		}
		e.step(testReceipt(at, rows...), topology, config)
		early := 0
		for _, a := range e.Aggregates {
			if a.Kind != "receipts" && !strings.HasPrefix(a.Kind, "report-") {
				early++
			}
		}
		if i < 5 && early != 0 {
			t.Fatal("used signals before third supported station")
		}
	}
}

func assertCausalComponentBounds(t *testing.T, e *engine) {
	t.Helper()
	signals, components := int64(0), int64(0)
	for _, a := range e.Aggregates {
		if a.Kind == "signals" {
			signals += a.Count
		}
		if a.Kind == "component" {
			components += a.Count
			if a.Sum != 60 || a.LowerSum != 30 || a.UpperSum != 90 {
				t.Fatalf("wrong bounded component: %+v", a)
			}
		}
	}
	if signals != 3 || components != 2 {
		t.Fatalf("signals=%d components=%d", signals, components)
	}
}

func nonReceiptDigest(e *engine) string {
	m := map[string]Aggregate{}
	for k, a := range e.Aggregates {
		if a.Kind != "receipts" && !strings.HasPrefix(a.Kind, "report-") {
			m[k] = a
		}
	}
	return digest(m)
}
