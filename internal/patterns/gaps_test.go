package patterns

import (
	"context"
	"testing"
	"time"
)

func TestConditionsCutOnlyAffectedRoute(t *testing.T) {
	e := newEngine()
	top := testTopology()
	other := top.Patterns[0]
	other.Route = "other"
	other.Direction = "other-dir"
	other.Stops = []string{"X", "Y"}
	other.Sequences = []int{1, 2}
	top.Patterns = append(top.Patterns, other)
	e.Groups["a"] = &group{Route: "line"}
	e.Groups["b"] = &group{Route: "other"}
	e.updateConditions(Receipt{RouteConditions: map[string]string{"line": "reported_normal", "other": "reported_normal"}}, top)
	e.updateConditions(Receipt{RouteConditions: map[string]string{"line": "reported_disruption", "other": "reported_normal"}}, top)
	if e.Groups["a"] != nil || e.Groups["b"] == nil {
		t.Fatal("unrelated line alert revoked continuity")
	}
	if (Receipt{RouteConditions: map[string]string{"line": "reported_normal"}}).condition("other") != "unknown" {
		t.Fatal("absence of alert claimed normality")
	}
}
func TestHistoricFallbackCompatibilityAndContext(t *testing.T) {
	e := newEngine()
	e.Profile = "new-s30-b30"
	e.Topology = testTopology()
	key := segmentID(Segment{Route: "line", Direction: "d", Origin: "A", Target: "B"})
	e.Topology.Segments = map[string]string{key: "verified-shape"}
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	c := DefaultConfig(t.TempDir())
	at := now.AddDate(0, 0, -35)
	a := baseAggregate(aggregateRequest{at, "line", "d", "A", "1", "old-profile-s30-b5", "reported_normal", "component"}, c)
	a.Resolution = 5
	a.Compatibility = e.componentKey(Segment{Route: "line", Direction: "d", Origin: "A", Target: "B"})
	a.Target = "B"
	a.TargetPlatform = "2"
	a.Count = 2
	a.Sum = 120
	a.KnownAt = at.Add(time.Minute).UnixNano()
	e.add(a)
	result := e.componentSummary(componentRequest{"A", "B", "1", "line", "d", "reported_normal", now, now}, c)
	if result.Samples != 2 || result.Seconds != 60 || !result.HistoricalFallback || result.GeneralContext || result.OldestDate != a.Date {
		t.Fatalf("old compatible data lost: %+v", result)
	}
	result = e.componentSummary(componentRequest{"A", "B", "1", "line", "d", "reported_disruption", now, now}, c)
	if !result.GeneralContext || !result.HistoricalFallback {
		t.Fatal("general-context fallback not disclosed")
	}
	e.Profile = "new-s60-b30"
	if e.componentSummary(componentRequest{"A", "B", "1", "line", "d", "reported_normal", now, now}, c).Samples != 0 {
		t.Fatal("mixed sampling evidence")
	}
	e.Profile = "new-s30-b30"
	e.Topology.Segments[key] = "different-track"
	if e.componentSummary(componentRequest{"A", "B", "1", "line", "d", "reported_normal", now, now}, c).Samples != 0 {
		t.Fatal("changed physical geometry reused")
	}
}
func TestHolidaysAndLisbonCivilDay(t *testing.T) {
	for _, value := range []string{"2025-04-18", "2025-04-20", "2025-06-19", "2026-04-03", "2026-04-05", "2026-06-04", "2026-06-13", "2026-12-25"} {
		at, _ := time.ParseInLocation("2006-01-02", value, lisbon)
		if dayType(at) != "holiday" {
			t.Fatal(value)
		}
	}
	at := time.Date(2026, 6, 12, 23, 30, 0, 0, time.UTC)
	if dayType(at) != "holiday" {
		t.Fatal("used UTC calendar day")
	}
	if dayType(time.Date(2026, 2, 17, 12, 0, 0, 0, lisbon)) != "weekday" {
		t.Fatal("optional Carnival inferred mandatory")
	}
}
func TestEvaluationDurableHistogramsAndDistinctAssociations(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		issued := base.Add(time.Duration(i) * time.Minute)
		own := issued.Add(4 * time.Minute)
		official := issued.Add(5 * time.Minute)
		f := Forecast{Episode: "one-association", Route: "line", Direction: "d", Stop: "E", Platform: "1", Profile: "p", Mode: forecastMode, Condition: "reported_normal", Function: "waiting", IssuedAt: issued, OwnAt: &own, OfficialAt: &official}
		s.engine.reportIssued(f, c)
		s.engine.Cases = []Forecast{f}
		low := own.Add(time.Duration(i) * 10 * time.Second)
		high := low.Add(5 * time.Second)
		s.engine.evaluateReference(&group{ID: f.Episode, Signals: []signal{{Stop: "E", Platform: "1", L: low, U: high}}}, signal{Stop: "E", Platform: "1", L: low, U: high}, issued.Add(6*time.Minute), c)
	}
	if err = s.flush(base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	v, err := s.View(context.Background(), "E")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Evaluation) != 1 {
		t.Fatalf("missing report: %+v", v.Evaluation)
	}
	r := v.Evaluation[0]
	if r.Cases != 10 || r.Paired != 10 || r.Evaluated != 10 || r.Journeys != 1 || r.Days != 1 || r.MAEOwnLower == nil || *r.MAEOwnLower != 45 || r.P90OwnUpper == nil || *r.P90OwnUpper < 85 {
		t.Fatalf("bad bounded report: %+v", r)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v, err = s.View(context.Background(), "E")
	if err != nil || len(v.Evaluation) != 1 || v.Evaluation[0].Journeys != 1 {
		t.Fatal("report required expired detail")
	}
}

func TestReprocessCorrectsRetainedInputsAndKeepsIssuedValues(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
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
		if err = s.Record(testReceipt(at, rows...), testTopology()); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.flush(base.Add(3 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	before := digest(s.detail)
	original := s.detail[3].Rows[1]
	changed := original
	changed.ETA = []byte("300")
	control := baseAggregate(aggregateRequest{base, "other", "d", "X", "1", "p", "reported_normal", "component"}, c)
	control.Count = 1
	control.Sum = 42
	s.engine.add(control)
	result, err := s.Reprocess(context.Background(), []Correction{{ReceivedAt: s.detail[3].ReceivedAt, ExpectedHash: digest(original), Row: changed, Evidence: "synthetic verified source revision"}}, base.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.Corrections != 1 || !result.PartialCoverage || digest(s.detail) != before {
		t.Fatal("reprocessing changed original detail/emissions")
	}
	for _, a := range s.engine.Aggregates {
		if a.Route == "line" && (a.Kind == "signals" || a.Kind == "component") {
			t.Fatal("invalid triple survived source correction")
		}
	}
	found := false
	for _, a := range s.engine.Aggregates {
		if a.Route == "other" && a.Sum == 42 {
			found = true
		}
	}
	if !found {
		t.Fatal("unaffected metric erased")
	}
	_, err = s.Reprocess(context.Background(), []Correction{{ReceivedAt: s.detail[3].ReceivedAt, ExpectedHash: digest(original), Row: changed, Evidence: "stale synthetic revision"}}, base.Add(5*time.Minute))
	if err == nil {
		t.Fatal("stale expected row hash accepted after prior correction")
	}
	_, err = s.Reprocess(context.Background(), []Correction{{ReceivedAt: base.Add(-time.Hour), ExpectedHash: digest(original), Row: changed, Evidence: "synthetic revision without retained data"}}, base.Add(5*time.Minute))
	if err == nil {
		t.Fatal("reconstructed expired input")
	}
}

func TestCompatibleResolutionCalibrationAndConfigRestart(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Hour).Add(time.Minute)
	own := at.Add(4 * time.Minute)
	f := Forecast{Route: "line", Direction: "d", Stop: "A", Profile: "fixture-s30-b30", Mode: forecastMode, Condition: "reported_normal", Function: "waiting", IssuedAt: at, OwnAt: &own}
	for _, width := range []int{5, 30} {
		a := baseAggregate(aggregateRequest{at.Add(-time.Hour), f.Route, f.Direction, f.Stop, "1", "fixture-s30-b5", f.Condition, "calibration:waiting"}, c)
		a.Mode = forecastMode
		a.Horizon = 0
		a.Resolution = int32(width)
		a.Count = 2
		a.Bucket = 1
		a.KnownAt = at.Add(-time.Minute).UnixNano()
		s.engine.add(a)
	}
	radius, n := s.engine.radius(f, at, c)
	if n != 4 || radius == nil || *radius != 60 {
		t.Fatalf("mixed resolution rank: %v %d", radius, n)
	}
	f.Profile = "fixture-s60-b30"
	if radius, n = s.engine.radius(f, at, c); radius != nil || n != 0 {
		t.Fatal("calibration mixed sampling profiles")
	}
	receipt := testReceipt(at, testRow("A", "x", at, 30))
	if err = s.Record(receipt, testTopology()); err != nil {
		t.Fatal(err)
	}
	before := len(s.detail)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	c.BinSeconds = 5
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.detail) != before {
		t.Fatal("configuration change discarded retained source/emissions")
	}
	if err = s.Record(testReceipt(at.Add(30*time.Second), testRow("A", "x", at.Add(30*time.Second), 10)), testTopology()); err != nil {
		t.Fatal(err)
	}
	if len(s.detail) != before+1 {
		t.Fatal("new configuration overwrote earlier detail")
	}
}

func TestReprocessCancellationAndStaleGuard(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Reprocess(ctx, []Correction{{}}, time.Now())
	if err != context.Canceled {
		t.Fatalf("cancellation ignored: %v", err)
	}
	if len(s.index.Blocks) != 0 {
		t.Fatal("cancelled maintenance published data")
	}
}

func TestHistoryRestorationDoesNotResurrectPendingWithdrawal(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().UTC()
	a := baseAggregate(aggregateRequest{at, "line", "d", "A", "1", "p", "reported_normal", "component"}, c)
	a.Count = 1
	a.Sum = 100
	a.KnownAt = at.UnixNano()
	s.engine.add(a)
	if err = s.flush(at); err != nil {
		t.Fatal(err)
	}
	delete(s.engine.Aggregates, aggregateKey(a))
	s.engine.DirtyDays[a.Date] = true
	if err = s.loadComponentHistory(); err != nil {
		t.Fatal(err)
	}
	if len(s.engine.Aggregates) != 0 {
		t.Fatal("older published generation resurrected withdrawn evidence")
	}
}

func TestEvaluationSummaryLimitsKeepUnknownAndExactSumsDistinct(t *testing.T) {
	at := time.Now().UTC()
	c := DefaultConfig(t.TempDir())
	a := baseAggregate(aggregateRequest{at, "line", "d", "A", "1", "p", "reported_normal", "report-journeys:waiting"}, c)
	a.Count = 1
	a.EpisodeHash = "one"
	builder := reportBuilder{associations: maxEngineAggregates, dates: maxEngineAggregates, histograms: maxEngineAggregates}
	builder.add(a)
	a.Kind = "evaluation-own:waiting"
	a.ErrorHistogram = true
	a.LowerSum = 10
	a.UpperSum = 20
	builder.add(a)
	rows := builder.finish()
	if len(rows) != 1 || rows[0].JourneyCountComplete || rows[0].Support != "proxy_same_source_partial_summary" || rows[0].P90OwnUpper != nil || rows[0].MAEOwnLower == nil || *rows[0].MAEOwnLower != 10 {
		t.Fatalf("limited summary invented distribution/support or discarded exact means: %+v", rows)
	}
}
