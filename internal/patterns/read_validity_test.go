package patterns

import (
	"context"
	"testing"
	"time"
)

func TestMetroViewExpiresPointsWithoutChangingIssuedValues(t *testing.T) {
	for _, scenario := range []string{"past", "stale", "anchor_expired"} {
		t.Run(scenario, func(t *testing.T) { checkExpiredMetroRead(t, scenario) })
	}
}

func checkExpiredMetroRead(t *testing.T, scenario string) {
	t.Helper()
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	own, official := now.Add(time.Minute), now.Add(2*time.Minute)
	source := now.Add(-20 * time.Second)
	s.engine.LastReceipt = now.Add(-10 * time.Second)
	if scenario == "past" {
		own, official = now.Add(-time.Second), now.Add(-time.Second)
	}
	if scenario == "stale" {
		s.engine.LastReceipt = now.Add(-91 * time.Second)
		source = s.engine.LastReceipt
	}
	expiry := now.Add(time.Minute)
	if scenario == "anchor_expired" {
		expiry = now.Add(-time.Second)
	}
	f := Forecast{ID: "original", Episode: "episode", Stop: "A", OwnAt: &own, OfficialAt: &official, SourceAt: &source, OwnValidUntil: &expiry}
	s.engine.Live, s.engine.Cases = []Forecast{f}, []Forecast{f}
	s.engine.Groups["synthetic"] = &group{ID: "episode", Active: true}
	view, err := s.View(context.Background(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Forecasts) != 1 || view.Forecasts[0].OwnAt != nil {
		t.Fatalf("expired own point retained: %+v", view.Forecasts)
	}
	if scenario != "anchor_expired" && view.Forecasts[0].OfficialAt != nil {
		t.Fatal("expired official point retained")
	}
	if s.engine.Live[0].OwnAt == nil || s.engine.Cases[0].OfficialAt == nil {
		t.Fatal("read rewrote issuance evidence")
	}
}

func TestStationSummaryDoesNotRecoverUnknownResolution(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().UTC()
	rows := []Aggregate{}
	for _, width := range []int32{599, 600, 31} {
		a := baseAggregate(aggregateRequest{at, "cm:route", "0", "cm:A", "visit:1", "fixture-s30-b30", "unknown", "component"}, s.config)
		a.Resolution, a.Count, a.Compatibility, a.Target = width, 1, "same-evidence", "cm:B"
		rows = append(rows, a)
	}
	writeColdAggregateFixture(t, s, at, rows)
	view, err := s.ProviderView(context.Background(), "cm", "cm:A", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Patterns) != 1 || view.Patterns[0].ResolutionSeconds != 0 {
		t.Fatalf("unknown precision became available: %+v", view.Patterns)
	}
}

func TestMetroOwnExpiryUsesAnchorClockIndependentlyOfTarget(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	anchorClock := seedMetroAnchorExpiry(s)
	originalPoint, found := metroOnwardPoint(s.engine.Live)
	if !found || originalPoint.OwnAt == nil {
		t.Fatal("fixture did not issue a supported own point")
	}
	original := digest(s.engine.Live)
	view, err := s.View(context.Background(), "E")
	if err != nil {
		t.Fatal(err)
	}
	f, found := metroOnwardPoint(view.Forecasts)
	if !found || f.OwnAt != nil || f.OfficialAt == nil {
		t.Fatal("target clock renewed the model anchor or expired the independent official point")
	}
	if f.OwnValidUntil == nil || !f.OwnValidUntil.Equal(anchorClock.Add(90*time.Second)) {
		t.Fatal("anchor freshness bound was not retained")
	}
	if original != digest(s.engine.Live) {
		t.Fatal("display expiry changed original emission")
	}
}

func seedMetroAnchorExpiry(s *Service) time.Time {
	base := time.Now().UTC().Add(-20 * time.Second).Truncate(time.Second)
	topology := testTopology()
	s.engine.Profile = topology.Profile
	g := &group{ID: "synthetic", Train: "x", Route: "line", Direction: "d", Active: true, Signals: []signal{{Stop: "C", U: base.Add(-5 * time.Second), Supported: true}}}
	s.engine.Groups["synthetic"] = g
	anchorClock := base.Add(-80 * time.Second)
	originAt := anchorClock.Add(300*time.Second).AddDate(0, 0, -7)
	a := baseAggregate(aggregateRequest{originAt, "line", "d", "D", "1", topology.Profile, "reported_normal", "component"}, s.config)
	a.Target, a.TargetPlatform, a.Count, a.Sum, a.KnownAt = "E", "1", 1, 60, base.Add(-time.Hour).UnixNano()
	s.engine.add(a)
	r := testReceipt(base, testRow("D", "x", anchorClock, 300), testRow("E", "x", base.Add(-5*time.Second), 600))
	s.engine.LastReceipt = r.ReceivedAt
	s.engine.Live = s.engine.forecasts(r, topology, s.config)
	return anchorClock
}

func metroOnwardPoint(values []Forecast) (Forecast, bool) {
	for _, f := range values {
		if f.Stop == "E" && f.Function == "onward" {
			return f, true
		}
	}
	return Forecast{}, false
}

func TestFreshOfficialColdStartKeepsAssociationUnavailableReason(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	point := now.Add(time.Minute)
	s.engine.LastReceipt = now
	s.engine.Live = []Forecast{{Stop: "A", OfficialAt: &point, SourceAt: &now, Unavailable: "association_not_supported"}}
	view, err := s.View(context.Background(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Forecasts) != 1 || view.Forecasts[0].Unavailable != "association_not_supported" || view.Forecasts[0].OfficialAt == nil {
		t.Fatal("fresh unassociated official point was described as expired")
	}
}
