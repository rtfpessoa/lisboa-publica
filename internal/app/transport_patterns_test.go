package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestTransportPatternsPublicSelectorsAndOfficialColdStart(t *testing.T) {
	cache := NewCache()
	server, h := securityServer(t, &Store{}, cache)
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	server.Patterns = history
	ops := []string{"metro", "cm", "carris", "cp", "fertagus", "ttsl", "tcb", "mobi"}
	if err = history.ConfigureOperators(ops); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for _, op := range ops[1:] {
		cache.update(op, &StaticData{Stops: []api.Stop{{Id: op + ":A", SourceId: "A", Name: "Published", OperatorId: op, Lat: 38.7, Lon: -9.1}}}, nil, cache.operator(op))
		if err = history.RecordProvider(patterns.ProviderReceipt{Operator: op, ReceivedAt: at, Partial: true, PredictionStop: "*", Predictions: []patterns.ProviderPrediction{{ID: "feed:fixture", Route: op + ":r", Trip: op + ":t", Stop: op + ":A", ReceivedAt: at, ExpectedAt: at.Add(time.Minute), ValidUntil: at.Add(90 * time.Second)}}}); err != nil {
			t.Fatal(err)
		}
		w := securityRequest(h, "GET", "/api/v1/transport/patterns?operator_id="+op+"&stop_id="+op+":A", "", nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", op, w.Code, w.Body)
		}
		var v api.MetroPatterns
		if err = json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.Operator != op || v.PhysicalValidation || len(v.Forecasts) != 1 || v.Forecasts[0].OfficialAt == nil || v.Forecasts[0].OwnAt != nil {
			t.Fatalf("cold-start semantics: %+v", v)
		}
	}
	for _, selector := range []struct {
		query string
		code  int
	}{{"operator_id=bad", 400}, {"operator_id=cm&stop_id=cp:A", 400}, {"operator_id=cm&stop_id=cm:missing", 404}, {"operator_id=cm&episode=bad", 400}} {
		w := securityRequest(h, "GET", "/api/v1/transport/patterns?"+selector.query, "", nil)
		if w.Code != selector.code {
			t.Fatalf("%s: %d %s", selector.query, w.Code, w.Body)
		}
	}
}
func TestProviderJourneyUsesExactServiceAndContiguousCoverage(t *testing.T) {
	cache, d, v, _ := callsFixture(t, "cp")
	_ = cache
	stops := map[string]api.Stop{}
	for _, s := range d.Stops {
		stops[s.Id] = s
	}
	p, ok := providerJourney(v, d, stops)
	if !ok || len(p.Visits) < 2 {
		t.Fatal("complete published service rejected")
	}
	// The local catalogue retains the first contiguous run, not the far visit.
	d.Schedule.Trips[0].JourneyTimes = append([]StopTime{}, d.Schedule.Trips[0].Times...)
	d.Schedule.Trips[0].JourneyTimes = append(d.Schedule.Trips[0].JourneyTimes, StopTime{Stop: "outside", Sequence: 3}, StopTime{Stop: "s", Sequence: 4})
	if _, ok = providerJourney(v, d, stops); ok {
		t.Fatal("ambiguous run around a repeated source stop accepted")
	}
	d.Schedule.Trips[0].JourneyTimes = d.Schedule.Trips[0].JourneyTimes[:4]
	p, ok = providerJourney(v, d, stops)
	if !ok || len(p.Visits) != 3 {
		t.Fatal("contiguous local run rejected or omitted middle visit bridged")
	}
	v.PlanId = ptr("wrong")
	if _, ok = providerJourney(v, d, stops); ok {
		t.Fatal("mismatched plan accepted")
	}
}
func TestPredictionHistoryKeepsOfficialSourceClock(t *testing.T) {
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	if err = history.ConfigureOperators([]string{"metro", "cm", "carris"}); err != nil {
		t.Fatal(err)
	}
	cache := NewCache()
	at := time.Now().UTC()
	source := at.Add(-2 * time.Minute)
	data := &CPData{Availability: api.CPPredictionAvailability{Status: "ok", CollectedAt: &at}, Rows: []api.CPPrediction{{Id: "x", RouteId: "carris:r", StopId: "carris:A", SourceTripId: "carris:t", ExpectedAt: at.Add(time.Minute), SourceUpdatedAt: source, ValidUntil: at.Add(time.Minute)}}}
	cache.updateProviderPredictions(nil, map[string]*CPData{"carris": data})
	f := &Fetcher{Patterns: history, publicationState: publicationState{Cache: cache, Log: zap.NewNop()}}
	f.recordFeedHistory(map[string]*CPData{"carris": data})
	view, err := history.ProviderView(context.Background(), "carris", "carris:A", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Forecasts) != 0 {
		t.Fatal("collection renewed stale official clock")
	}
}

func TestProviderPathCapacityDisclosesTruncationAndReusesExistingPaths(t *testing.T) {
	cache, d, v, _ := callsFixture(t, "cp")
	prototype := d.Schedule.Trips[0]
	d.Schedule.Trips = nil
	vehicles := []api.Vehicle{}
	for i := 0; i < 257; i++ {
		trip := prototype
		trip.ID = fmt.Sprintf("trip%03d", i)
		trip.Headsign = trip.ID
		d.Schedule.Trips = append(d.Schedule.Trips, trip)
		row := v
		row.Id = fmt.Sprintf("cp:vehicle%03d", i)
		row.SourceId = row.Id
		row.TripId = ptr("cp:" + trip.ID)
		vehicles = append(vehicles, row)
	}
	last := vehicles[0]
	last.Id = "cp:after-cap"
	last.SourceId = last.Id
	last.StopId = ptr("cp:next")
	vehicles = append(vehicles, last)
	cache.update("cp", d, nil, cache.operator("cp"))
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	if err = history.ConfigureOperators([]string{"metro", "cm", "carris", "cp"}); err != nil {
		t.Fatal(err)
	}
	f := &Fetcher{Patterns: history, publicationState: publicationState{Cache: cache, Log: zap.NewNop()}}
	f.recordProviderHistory("cp", vehicles, time.Now().UTC(), "")
	view, err := history.ProviderView(context.Background(), "cp", "cp:next", "")
	if err != nil {
		t.Fatal(err)
	}
	if view.CollectedDays != 1 || !strings.Contains(view.Message, "limitado") {
		t.Fatal("capacity silently dropped an already defined path or omitted disclosure")
	}
}

func TestOfficialCacheSurvivesDisabledAndUnreadablePatternArchive(t *testing.T) {
	cache := NewCache()
	server, h := securityServer(t, &Store{}, cache)
	now := time.Now().UTC()
	source := now.Add(-10 * time.Second)
	d := &StaticData{PlanID: "plan", Stops: []api.Stop{{Id: "cp:A", SourceId: "A", Name: "Published", OperatorId: "cp", Lat: 38.7, Lon: -9.1}}}
	cache.update("cp", d, nil, cache.operator("cp"))
	data := &CPData{PlanID: "plan", Availability: api.CPPredictionAvailability{Status: "ok", CollectedAt: &now}, Rows: []api.CPPrediction{{Id: "synthetic-live", OperatorId: "cp", PlanId: "plan", RouteId: "cp:r", SourceTripId: "cp:t", StopId: "cp:A", StopSequence: 3, ExpectedAt: now.Add(time.Minute), SourceUpdatedAt: source, CollectedAt: now, ValidUntil: now.Add(time.Minute)}}}
	cache.updateProviderPredictions(map[string]*StaticData{"cp": d}, map[string]*CPData{"cp": data})
	assert := func(status string) {
		t.Helper()
		w := securityRequest(h, "GET", "/api/v1/transport/patterns?operator_id=cp&stop_id=cp:A", "", nil)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		var v api.MetroPatterns
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		if v.Status != status || len(v.Forecasts) != 1 || v.Forecasts[0].Mode != "official-cache/v1" || v.Forecasts[0].OwnAt != nil || v.Forecasts[0].SourceAt == nil || !v.Forecasts[0].SourceAt.Equal(source) || v.Evaluated != 0 {
			t.Fatalf("official fallback lost or historical case changed: %+v", v)
		}
	}
	assert("disabled")
	c := patterns.DefaultConfig(t.TempDir())
	history, err := patterns.Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	server.Patterns = history
	if err = os.RemoveAll(c.Directory); err != nil {
		t.Fatal(err)
	}
	assert("degraded")
	data.Rows[0].SourceUpdatedAt = now.Add(-2 * time.Minute)
	w := securityRequest(h, "GET", "/api/v1/transport/patterns?operator_id=cp&stop_id=cp:A", "", nil)
	var v api.MetroPatterns
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if len(v.Forecasts) != 0 {
		t.Fatal("fallback renewed source validity")
	}
}
func TestMetroOfficialCacheUsesPublishedSlotEligibilityWithoutHistory(t *testing.T) {
	cache := NewCache()
	_, h := securityServer(t, &Store{}, cache)
	now := time.Now().UTC().Truncate(time.Second)
	source := now.Add(-10 * time.Second)
	cache.updateMetro(&MetroData{Status: api.MetroStatus{Status: "ok", CheckedAt: &now}, Stations: []MetroStation{{ID: "BC", Name: "Baixa / Chiado"}}, Waits: []MetroWait{{Stop: "BC", Platform: "1", Destination: "33", At: source.In(lisbon).Format("20060102150405"), Train: "0", Wait1: json.RawMessage("120"), Train2: "published", Wait2: json.RawMessage("150")}}}, cache.operator("metro"))
	w := securityRequest(h, "GET", "/api/v1/metro/patterns?stop_id=metro:BC", "", nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var v api.MetroPatterns
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if len(v.Forecasts) != 1 || v.Forecasts[0].Train != "published" || v.Forecasts[0].OwnAt != nil || v.Forecasts[0].OfficialAt == nil || !v.Forecasts[0].OfficialAt.Equal(source.Add(150*time.Second)) {
		t.Fatalf("published cache eligibility altered: %+v", v)
	}
	w = securityRequest(h, "GET", "/api/v1/metro/patterns?stop_id=metro:unknown", "", nil)
	if w.Code != 404 {
		t.Fatal("disabled archive skipped station validation")
	}
}
