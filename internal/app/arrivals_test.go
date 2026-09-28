package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func TestCMArrivalsThroughHTTP(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/arrivals/by_stop/S" {
			t.Errorf("unexpected upstream %s", r.URL.Path)
		}
		fmt.Fprintf(w, `[{"line_id":"1","trip_id":"[plan][agency]trip","headsign":"Destino","scheduled_arrival_unix":%d,"estimated_arrival_unix":%d}]`, now.Add(-time.Minute).Unix(), now.Add(5*time.Minute).Unix())
	}))
	defer upstream.Close()
	cache := NewCache()
	d := &StaticData{Stops: []api.Stop{{Id: "cm:S", SourceId: "S", OperatorId: "cm", Name: "Paragem"}}, Routes: []api.RouteDetail{{Id: "cm:1", ShortName: "1"}}}
	cache.update("cm", d, nil, cache.operator("cm"))
	_, h := securityServer(t, &Store{}, cache)
	path := "/api/v1/arrivals?operators=cm&stop_id=cm:S&from=" + url.QueryEscape(now.Format(time.RFC3339)) + "&to=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339))
	if w := securityRequest(h, "GET", path, "", nil); w.Code != 200 {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	f := NewFetcher(nil, cache, zap.NewNop())
	f.CM = upstream.URL
	f.RefreshArrivals(context.Background())
	w := securityRequest(h, "GET", path, "", nil)
	var page api.ArrivalPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(page.Data) != 1 || page.Data[0].Kind != "prediction" || !page.Data[0].ExpectedAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("missing future prediction after past schedule: %d %s", w.Code, w.Body)
	}
}

// The captured identifiers are unmodified. Only temporal fields are rebased to
// the test's current service day, so recorded observations never become live data.
// Delay is synthetic too: a captured delay can exceed the seconds since midnight.
func TestTMLArrivalsSharedFeedAndCapturedIdentities(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	cache := NewCache()
	_, h := securityServer(t, &Store{}, cache)
	updates := []cpEntity{}
	paths := map[string]string{}
	expected := now.Add(5 * time.Minute)
	for _, id := range []string{"carris", "tcb", "mobi", "ttsl", "fertagus"} {
		var fixture struct {
			Plan struct {
				ID string `json:"_id"`
			}
			ETA       cpUpdate
			GTFSTrip  map[string]string `json:"gtfs_trip"`
			GTFSVisit map[string]string `json:"gtfs_stop_time"`
		}
		blob, err := os.ReadFile("testdata/eta/" + id + ".json")
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(blob, &fixture); err != nil {
			t.Fatal(err)
		}
		seq, _ := strconv.Atoi(fixture.GTFSVisit["stop_sequence"])
		var event cpStopUpdate
		for _, s := range fixture.ETA.Stops {
			if s.Sequence != nil && *s.Sequence == seq {
				event = s
				break
			}
		}
		if event.Sequence == nil {
			t.Fatalf("%s missing captured visit", id)
		}
		event.Arrival.Time = ptr(expected.Unix())
		event.Arrival.Delay = ptr(0)
		fixture.ETA.Stops = []cpStopUpdate{event}
		fixture.ETA.Timestamp = now.Unix()
		arrival := int(expected.Sub(serviceStart(now.In(lisbon))).Seconds())
		if event.Arrival.Delay != nil {
			arrival -= *event.Arrival.Delay
		}
		rawStop := fixture.GTFSVisit["stop_id"]
		rawTrip := fixture.GTFSTrip["trip_id"]
		rawRoute := fixture.GTFSTrip["route_id"]
		trip := ScheduledTrip{ID: rawTrip, Route: rawRoute, Service: "daily", Headsign: fixture.GTFSTrip["trip_headsign"], Times: []StopTime{{Stop: rawStop, Arrival: int32(arrival), Departure: int32(arrival), Sequence: seq}}, CPTiming: &cpTripTiming{FirstArrival: arrival, FirstDeparture: arrival, LastArrival: arrival, FirstSequence: seq, LastSequence: seq}}
		d := &StaticData{PlanID: fixture.Plan.ID, ArrivalMetadata: true, ValidFrom: "20200101", ValidUntil: "20990101", Stops: []api.Stop{{Id: qualify(id, rawStop), SourceId: rawStop, OperatorId: id, Name: "Publicada"}}, Routes: []api.RouteDetail{{Id: qualify(id, rawRoute), ShortName: "Linha"}}, Schedule: &Schedule{Trips: []ScheduledTrip{trip}, Calendars: map[string]Calendar{"daily": {Start: "20200101", End: "20990101", Days: [7]bool{true, true, true, true, true, true, true}}}}}
		d.Schedule.Trips[0].ArrivalTiming = packArrivalTiming(d.Schedule.Trips[0].CPTiming)
		d.Schedule.Trips[0].CPTiming = nil
		cache.update(id, d, nil, cache.operator(id))
		updates = append(updates, cpEntity{Update: fixture.ETA})
		paths[id] = "/api/v1/arrivals?operators=" + id + "&stop_id=" + url.QueryEscape(qualify(id, rawStop)) + "&from=" + url.QueryEscape(now.Format(time.RFC3339)) + "&to=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339))
		if w := securityRequest(h, "GET", paths[id], "", nil); w.Code != 200 {
			t.Fatalf("interest %s: %s", id, w.Body)
		}
	}
	feed := map[string]any{"data": map[string]any{"header": map[string]any{"gtfs_realtime_version": "2.0", "incrementality": "FULL_DATASET", "timestamp": now.Unix()}, "entity": updates}, "error": nil}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/realtime/eta/gtfs" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(feed)
	}))
	defer upstream.Close()
	f := NewFetcher(nil, cache, zap.NewNop())
	f.Hub = upstream.URL
	f.RefreshArrivals(context.Background())
	if calls.Load() != 1 {
		t.Fatalf("feed fetched %d times", calls.Load())
	}
	for id, path := range paths {
		w := securityRequest(h, "GET", path, "", nil)
		var p api.ArrivalPage
		_ = json.Unmarshal(w.Body.Bytes(), &p)
		if w.Code != 200 || len(p.Data) != 1 || p.Data[0].Kind != "prediction" || !p.Data[0].ExpectedAt.Equal(expected) || p.Data[0].ServiceDate == nil {
			t.Fatalf("%s identity not translated: %d %s", id, w.Code, w.Body)
		}
		if p.Data[0].VehicleId != nil {
			t.Fatalf("%s invented vehicle", id)
		}
		if p.Data[0].DateBasis == nil || *p.Data[0].DateBasis != api.ArrivalDateBasisMatchedSchedule {
			t.Fatalf("%s missing inferred date basis", id)
		}
	}
	// Fetching the same old observation again must not renew source validity.
	for n := range updates {
		updates[n].Update.Timestamp = now.Add(-sourceFreshness - time.Second).Unix()
	}
	f.RefreshArrivals(context.Background())
	for id, path := range paths {
		w := securityRequest(h, "GET", path, "", nil)
		var p api.ArrivalPage
		_ = json.Unmarshal(w.Body.Bytes(), &p)
		for _, r := range p.Data {
			if r.Kind == "prediction" {
				t.Fatalf("%s old source rejuvenated", id)
			}
		}
	}
}

type arrivalHarness struct {
	cache   *Cache
	fetcher *Fetcher
	handler http.Handler
	now     time.Time
	path    string
	data    *StaticData
	updates []cpEntity
	status  int
}

func newArrivalHarness(t *testing.T) *arrivalHarness {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	cache := NewCache()
	d := fixtureStatic("carris", now)
	d.PlanID = "plan"
	d.ArrivalMetadata = true
	start := int(now.Add(5 * time.Minute).Sub(serviceStart(now.In(lisbon))).Seconds())
	trip := &d.Schedule.Trips[0]
	trip.Times[0].Arrival = int32(start)
	trip.Times[0].Departure = int32(start)
	trip.Times[1].Arrival = int32(start + 60)
	trip.Times[1].Departure = int32(start + 60)
	trip.ArrivalTiming = packArrivalTiming(&cpTripTiming{FirstArrival: start, FirstDeparture: start, LastArrival: start + 60})
	cache.update("carris", d, nil, cache.operator("carris"))
	_, handler := securityServer(t, &Store{}, cache)
	h := &arrivalHarness{cache: cache, data: d, handler: handler, now: now, status: 200, path: "/api/v1/arrivals?operators=carris&stop_id=carris:S&from=" + url.QueryEscape(now.Format(time.RFC3339)) + "&to=" + url.QueryEscape(now.Add(time.Hour).Format(time.RFC3339))}
	var u cpUpdate
	u.Trip.ID = "[plan][IA9T6]T"
	u.Timestamp = now.Unix()
	u.Vehicle.ID = "unit"
	for n, v := range trip.Times {
		var s cpStopUpdate
		s.ID = "internal"
		s.Sequence = ptr(v.Sequence)
		s.Arrival.Time = ptr(now.Add(time.Duration(5+n) * time.Minute).Unix())
		s.Arrival.Delay = ptr(0)
		u.Stops = append(u.Stops, s)
	}
	h.updates = []cpEntity{{Update: u}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(h.status)
		if h.status == 200 {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"header": map[string]any{"gtfs_realtime_version": "2.0", "incrementality": "FULL_DATASET", "timestamp": time.Now().Unix()}, "entity": h.updates}, "error": nil})
		}
	}))
	t.Cleanup(ts.Close)
	h.fetcher = NewFetcher(nil, cache, zap.NewNop())
	h.fetcher.Hub = ts.URL
	if w := securityRequest(handler, "GET", h.path, "", nil); w.Code != 200 {
		t.Fatal(w.Body)
	}
	return h
}
func (h *arrivalHarness) read(t *testing.T, path string) api.ArrivalPage {
	t.Helper()
	w := securityRequest(h.handler, "GET", path, "", nil)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var p api.ArrivalPage
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func TestTMLArrivalAdmissionAndFallbackThroughHTTP(t *testing.T) {
	for _, tc := range []struct {
		name                string
		change              func(*arrivalHarness)
		predictions         int
		unknownDay, vehicle bool
		status              string
	}{
		{name: "repeated visits and zero delay", predictions: 2, status: "ok"},
		{name: "absolute time takes precedence over conflicting delay", change: func(h *arrivalHarness) {
			h.updates[0].Update.Stops[0].Arrival.Time = ptr(h.now.Add(8 * time.Minute).Unix())
		}, predictions: 2, unknownDay: true, status: "ok"},
		{name: "integer delay overflow cannot invent a day", change: func(h *arrivalHarness) { h.updates[0].Update.Stops[0].Arrival.Delay = ptr(math.MinInt) }, predictions: 2, unknownDay: true, status: "ok"},
		{name: "underlying observation expired", change: func(h *arrivalHarness) { h.updates[0].Update.Timestamp = h.now.Add(-91 * time.Second).Unix() }, predictions: 0, status: "stale"},
		{name: "millisecond ETA timestamp rejected", change: func(h *arrivalHarness) {
			for n := range h.updates[0].Update.Stops {
				h.updates[0].Update.Stops[n].Arrival.Time = ptr(h.now.UnixMilli())
			}
		}, predictions: 0, status: "partial"},
		{name: "conflicting internal ID outside selected stop", change: func(h *arrivalHarness) { h.data.Schedule.Trips[0].Times[1].Stop = "T" }, predictions: 0, status: "partial"},
		{name: "reverse mapping conflict", change: func(h *arrivalHarness) { h.updates[0].Update.Stops[1].ID = "another-internal" }, predictions: 0, status: "partial"},
		{name: "frequency descriptors missing", change: func(h *arrivalHarness) { h.data.HasFrequencies = true }, predictions: 0, status: "partial"},
		{name: "invalid explicit day", change: func(h *arrivalHarness) { h.updates[0].Update.Trip.Date = "20200101" }, predictions: 0, status: "partial"},
		{name: "parent station includes child predictions", change: func(h *arrivalHarness) {
			h.data.Schedule.Parents["S"] = "parent"
			h.path = strings.Replace(h.path, "stop_id=carris:S", "stop_id=carris:parent", 1)
			_ = securityRequest(h.handler, "GET", h.path, "", nil)
		}, predictions: 2, status: "ok"},
		{name: "service day beyond 24 hours", change: func(h *arrivalHarness) {
			day := h.now.In(lisbon).AddDate(0, 0, -1)
			h.data.ValidFrom = day.Format("20060102")
			h.updates[0].Update.Trip.Date = day.Format("20060102")
			trip := &h.data.Schedule.Trips[0]
			for n := range trip.Times {
				trip.Times[n].Arrival += secondsPerDay
				trip.Times[n].Departure += secondsPerDay
			}
			timing := unpackArrivalTiming(trip.ArrivalTiming)
			timing.FirstArrival += secondsPerDay
			timing.FirstDeparture += secondsPerDay
			timing.LastArrival += secondsPerDay
			trip.ArrivalTiming = packArrivalTiming(timing)
		}, predictions: 2, status: "ok"},
		{name: "upstream error preserves schedules", change: func(h *arrivalHarness) { h.status = 503 }, predictions: 0, status: "error"},
		{name: "unique observed vehicle", change: func(h *arrivalHarness) {
			v := api.Vehicle{Id: "carris:unit", SourceId: "unit", OperatorId: "carris", PlanId: ptr("plan"), TripId: ptr("carris:T"), OperationalDate: ptr(h.now.In(lisbon).Format("2006-01-02")), ObservedAt: h.now}
			h.cache.update("carris", nil, &LiveData{Vehicles: []api.Vehicle{v}}, h.cache.operator("carris"))
		}, predictions: 2, vehicle: true, status: "ok"},
		{name: "ambiguous vehicles keep predictions", change: func(h *arrivalHarness) {
			v := api.Vehicle{Id: "carris:unit", SourceId: "unit", OperatorId: "carris", PlanId: ptr("plan"), TripId: ptr("carris:T"), OperationalDate: ptr(h.now.In(lisbon).Format("2006-01-02")), ObservedAt: h.now}
			h.cache.update("carris", nil, &LiveData{Vehicles: []api.Vehicle{v, v}}, h.cache.operator("carris"))
		}, predictions: 2, status: "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newArrivalHarness(t)
			if tc.change != nil {
				tc.change(h)
			}
			h.fetcher.RefreshArrivals(context.Background())
			p := h.read(t, h.path)
			predictions := 0
			planned := 0
			for _, r := range p.Data {
				if r.Kind == "prediction" {
					predictions++
					if tc.unknownDay && (r.ServiceDate != nil || r.DateBasis != nil || r.ScheduledAt != nil || r.VehicleId != nil) {
						t.Fatal("invented schedule instance")
					}
					if r.ServiceDate != nil {
						basis := api.ArrivalDateBasisMatchedSchedule
						if h.updates[0].Update.Trip.Date != "" {
							basis = api.ArrivalDateBasisPublished
						}
						if r.DateBasis == nil || *r.DateBasis != basis {
							t.Fatal("incorrect service date basis")
						}
					}
					if (r.VehicleId != nil) != tc.vehicle {
						t.Fatal("incorrect vehicle association")
					}
				} else {
					planned++
				}
			}
			if predictions != tc.predictions || p.Availability == nil || string(p.Availability.Status) != tc.status {
				t.Fatalf("wrong admission/status: %+v", p)
			}
			if tc.predictions == 0 && planned == 0 {
				t.Fatal("fallback schedules disappeared")
			}
		})
	}
}
func TestArrivalPredictionPagesAreFrozenAndSelectionBound(t *testing.T) {
	h := newArrivalHarness(t)
	h.fetcher.RefreshArrivals(context.Background())
	first := h.read(t, h.path+"&limit=1")
	if first.Page.Total != 2 || !first.Page.HasMore || first.Page.Revision == nil {
		t.Fatal("missing prediction pages")
	}
	next := h.path + "&limit=1&offset=1&revision=" + url.QueryEscape(*first.Page.Revision)
	original := h.now.Add(6 * time.Minute)
	h.updates[0].Update.Stops[1].Arrival.Time = ptr(h.now.Add(9 * time.Minute).Unix())
	h.fetcher.RefreshArrivals(context.Background())
	second := h.read(t, next)
	if !second.Data[0].ExpectedAt.Equal(original) {
		t.Fatal("next page silently switched to current feed")
	}
	if w := securityRequest(h.handler, "GET", strings.Replace(next, "stop_id=carris:S", "stop_id=carris:T", 1), "", nil); w.Code != 400 {
		t.Fatalf("selector drift: %d", w.Code)
	}
	h.cache.arrivals.mu.Lock()
	r := h.cache.arrivals.results[*first.Page.Revision]
	r.snapshot.expires = time.Now().Add(-time.Second)
	h.cache.arrivals.results[*first.Page.Revision] = r
	h.cache.arrivals.mu.Unlock()
	if w := securityRequest(h.handler, "GET", next, "", nil); w.Code != 410 {
		t.Fatalf("expired lease: %d", w.Code)
	}
	fresh := h.read(t, h.path+"&limit=1")
	h.cache.update("carris", fixtureStatic("carris", h.now), nil, h.cache.operator("carris"))
	if w := securityRequest(h.handler, "GET", h.path+"&offset=1&revision="+url.QueryEscape(*fresh.Page.Revision), "", nil); w.Code != 410 {
		t.Fatalf("changed plan generation: %d", w.Code)
	}
}

func TestCMArrivalSourceStatesAndBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(time.Hour)
	for _, tc := range []struct {
		name, body, status string
		httpStatus, total  int
		kind               string
	}{
		{name: "published empty", body: "[]", httpStatus: 200, status: "ok"},
		{name: "null is not an empty publication", body: "null", httpStatus: 200, status: "error"},
		{name: "source failed", body: "[]", httpStatus: 503, status: "error"},
		{name: "planned fallback", body: fmt.Sprintf(`[{"line_id":"1","trip_id":"trip","scheduled_arrival_unix":%d}]`, now.Add(5*time.Minute).Unix()), httpStatus: 200, status: "ok", total: 1, kind: "scheduled"},
		{name: "millisecond estimate rejected, planned time preserved", body: fmt.Sprintf(`[{"line_id":"1","trip_id":"trip","scheduled_arrival_unix":%d,"estimated_arrival_unix":%d}]`, now.Add(5*time.Minute).Unix(), now.Add(6*time.Minute).UnixMilli()), httpStatus: 200, status: "partial", total: 1, kind: "scheduled"},
		{name: "response byte cap", body: "[" + strings.Repeat(" ", arrivalBodyLimit) + "]", httpStatus: 200, status: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := NewCache()
			cache.update("cm", &StaticData{Stops: []api.Stop{{Id: "cm:S", SourceId: "S", OperatorId: "cm"}}}, nil, cache.operator("cm"))
			_, handler := securityServer(t, &Store{}, cache)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.httpStatus)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			f := NewFetcher(nil, cache, zap.NewNop())
			f.CM = upstream.URL
			path := "/api/v1/arrivals?operators=cm&stop_id=cm:S&to=" + url.QueryEscape(until.Format(time.RFC3339))
			w := securityRequest(handler, "GET", path, "", nil)
			var first api.ArrivalPage
			_ = json.Unmarshal(w.Body.Bytes(), &first)
			if first.Availability == nil || first.Availability.Status != "loading" {
				t.Fatal("unknown source presented as empty")
			}
			f.RefreshArrivals(context.Background())
			cache.arrivals.mu.Lock()
			publication := cache.arrivals.latest["cm:S"].availability
			cache.arrivals.mu.Unlock()
			if string(publication.Status) != tc.status {
				t.Fatalf("wrong publication status: %+v", publication)
			}
			expectedStatus := tc.status
			if tc.status == "ok" && until.After(*publication.CoverageUntil) {
				expectedStatus = "partial"
			}
			w = securityRequest(handler, "GET", path, "", nil)
			var p api.ArrivalPage
			_ = json.Unmarshal(w.Body.Bytes(), &p)
			if w.Code != 200 || p.Availability == nil || string(p.Availability.Status) != expectedStatus || p.Page.Total != tc.total {
				t.Fatalf("wrong source state: %d %s", w.Code, w.Body)
			}
			if tc.kind != "" && string(p.Data[0].Kind) != tc.kind {
				t.Fatal("schedule called prediction")
			}
			if p.Availability.SourceUpdatedAt != nil {
				t.Fatal("invented CM observation clock")
			}
		})
	}
}
func TestCMArrivalSharedRequestsAndCancellation(t *testing.T) {
	cache := NewCache()
	d := &StaticData{Stops: []api.Stop{{Id: "cm:S", SourceId: "S", OperatorId: "cm"}}}
	cache.update("cm", d, nil, cache.operator("cm"))
	_, h := securityServer(t, &Store{}, cache)
	var attempts atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realtime/eta/gtfs" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if attempts.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			_, _ = io.WriteString(w, "[]")
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	f := NewFetcher(nil, cache, zap.NewNop())
	f.CM = upstream.URL
	f.Hub = upstream.URL
	path := "/api/v1/arrivals?operators=cm&stop_id=cm:S"
	_ = securityRequest(h, "GET", path, "", nil)
	done := make(chan struct{})
	go func() { f.RefreshArrivals(context.Background()); close(done) }()
	<-entered
	// A cancelled reader does not own or cancel the shared upstream work.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	h.ServeHTTP(httptest.NewRecorder(), req)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); f.RefreshArrivals(context.Background()) }()
	}
	wg.Wait()
	close(release)
	<-done
	for range 10 {
		_ = securityRequest(h, "GET", path, "", nil)
		f.RefreshArrivals(context.Background())
	}
	if attempts.Load() != 1 {
		t.Fatalf("same-stop requests not coalesced or minimum interval lost: %d", attempts.Load())
	}
	// A cancelled collector frees its in-flight reservation for a later tick.
	cache.arrivals.mu.Lock()
	cache.arrivals.demands["cm:S"].attempted = time.Time{}
	cache.arrivals.mu.Unlock()
	cancelled, cancelCollector := context.WithCancel(context.Background())
	cancelCollector()
	f.RefreshArrivals(cancelled)
	cache.arrivals.mu.Lock()
	busy := cache.arrivals.demands["cm:S"].busy
	cache.arrivals.mu.Unlock()
	if busy {
		t.Fatal("cancelled collector leaked slot")
	}
}
func TestArrivalDemandCapacityAndSourceAttemptCeiling(t *testing.T) {
	cache := NewCache()
	d := &StaticData{}
	now := time.Now()
	for _, operator := range []string{"cm", "carris"} {
		for n := 0; n < arrivalDemandLimit; n++ {
			v := cache.arrivals.request(fmt.Sprintf("%s:%d", operator, n), d, now)
			if v.availability.Status != "loading" {
				t.Fatal("early capacity rejection")
			}
		}
		v := cache.arrivals.request(operator+":overflow", d, now)
		if v.availability.Status != "partial" {
			t.Fatal("full cache claimed complete")
		}
	}
	for tick := 0; tick < 30; tick++ {
		at := now.Add(time.Duration(tick) * 5 * time.Second)
		cache.arrivals.mu.Lock()
		for _, d := range cache.arrivals.demands {
			d.interest = at.Add(arrivalInterest)
		}
		cache.arrivals.mu.Unlock()
		for _, stop := range cache.arrivals.claimCM(at) {
			cache.arrivals.releaseCM(stop)
		}
	}
	if len(cache.arrivals.demands) > 2*arrivalDemandLimit || len(cache.arrivals.cmAttempts) > 48 {
		t.Fatal("retention/attempt ceiling exceeded")
	}
}

func TestCMFailureKeepsUnexpiredPlannedHours(t *testing.T) {
	cache := NewCache()
	static := &StaticData{Stops: []api.Stop{{Id: "cm:S", SourceId: "S", OperatorId: "cm"}}}
	cache.update("cm", static, nil, cache.operator("cm"))
	_, handler := securityServer(t, &Store{}, cache)
	now := time.Now().UTC()
	expiry := now.Add(20 * time.Second)
	scheduled := now.Add(5 * time.Minute)
	cache.arrivals.request("cm:S", static, now)
	cache.arrivals.publish("cm:S", arrivalSnapshot{static: static, expires: expiry, rows: []api.Arrival{{Id: "planned", OperatorId: "cm", StopId: "cm:S", RouteId: "cm:1", TripId: "cm:trip", Kind: "prediction", ScheduledAt: &scheduled, ExpectedAt: ptr(scheduled.Add(time.Minute)), ValidUntil: &expiry}}, availability: api.ArrivalAvailability{Status: "ok", PlannedStatus: "ok", SourceUrl: cmBase}})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	f := NewFetcher(nil, cache, zap.NewNop())
	f.CM = upstream.URL
	f.RefreshArrivals(context.Background())
	w := securityRequest(handler, "GET", "/api/v1/arrivals?operators=cm&stop_id=cm:S", "", nil)
	var p api.ArrivalPage
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || len(p.Data) != 1 || p.Data[0].Kind != "scheduled" || p.Data[0].ExpectedAt != nil || p.Availability.Status != "error" || p.Availability.PlannedStatus != "ok" || !p.Availability.ValidUntil.Equal(expiry) {
		t.Fatalf("failed prediction removed/rejuvenated valid planned hours: %s", w.Body)
	}
}

func TestTMLCachedGTFSBeyondMidnightAndDST(t *testing.T) {
	for _, clock := range []string{"2026-03-29T01:30:00Z", "2026-10-25T01:30:00Z", "2027-01-01T00:30:00Z"} {
		t.Run(clock, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, clock)
			if err != nil {
				t.Fatal(err)
			}
			day := now.In(lisbon).AddDate(0, 0, -1)
			expected := now.Add(5 * time.Minute)
			seconds := int(expected.Sub(serviceStart(day)).Seconds())
			clockText := fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds%3600/60, seconds%60)
			archive := replaceGTFS(t, shapeArchive(t, false), map[string]string{"trips.txt": "route_id,service_id,trip_id,trip_headsign,shape_id\n1,daily,A,Destino,one\n", "stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nA," + clockText + "," + clockText + ",S,1\n", "calendar.txt": "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\ndaily,1,1,1,1,1,1,1,20260101,20271231\n"})
			provider, _ := providerByID("carris")
			d, err := readGTFS(archive, provider, "plan", "20260101", "20271231", hubBase, now)
			if err != nil {
				t.Fatal(err)
			}
			var serialized bytes.Buffer
			if err = writeStaticCacheJSON(&serialized, d); err != nil {
				t.Fatal(err)
			}
			var restored StaticData
			if err = json.Unmarshal(serialized.Bytes(), &restored); err != nil {
				t.Fatal(err)
			}
			var u cpUpdate
			u.Trip.ID = "[plan][IA9T6]A"
			u.Timestamp = now.Unix()
			var s cpStopUpdate
			s.ID = "source-internal"
			s.Sequence = ptr(1)
			s.Arrival.Time = ptr(expected.Unix())
			s.Arrival.Delay = ptr(0)
			u.Stops = []cpStopUpdate{s}
			batch := newTMLArrivalBatch(context.Background(), &State{Static: map[string]*StaticData{"carris": &restored}}, map[string]*StaticData{"carris:S": &restored}, now)
			batch.visit(cpEntity{Update: u})
			result := batch.finish(nil)["carris:S"]
			if len(result.rows) != 1 || result.rows[0].ServiceDate == nil || result.rows[0].ServiceDate.Time.Format("20060102") != day.Format("20060102") || !result.rows[0].ExpectedAt.Equal(expected) {
				t.Fatalf("cached service day/DST changed: %+v", result)
			}
		})
	}
}

func TestTMLNewPlanDemandIsNotBlockedByAnOldSelection(t *testing.T) {
	h := newArrivalHarness(t)
	next := *h.data
	next.PlanID = "new-plan"
	previous := h.data.Schedule
	schedule := Schedule{Trips: previous.Trips, Calendars: previous.Calendars, Exceptions: previous.Exceptions, Parents: previous.Parents, StopNames: previous.StopNames, StopLines: previous.StopLines, CompleteJourneys: previous.CompleteJourneys, HasFrequencies: previous.HasFrequencies}
	schedule.Trips = append([]ScheduledTrip(nil), schedule.Trips...)
	schedule.Trips[0].Times = append([]StopTime(nil), schedule.Trips[0].Times...)
	for n := range schedule.Trips[0].Times {
		schedule.Trips[0].Times[n].Stop = "new-stop"
	}
	next.Schedule = &schedule
	next.Stops = append(append([]api.Stop(nil), next.Stops...), api.Stop{Id: "carris:new-stop", SourceId: "new-stop", OperatorId: "carris"})
	h.cache.update("carris", &next, nil, h.cache.operator("carris"))
	h.updates[0].Update.Trip.ID = "[new-plan][IA9T6]T"
	path := strings.Replace(h.path, "stop_id=carris:S", "stop_id=carris:new-stop", 1)
	_ = h.read(t, path)
	h.fetcher.RefreshArrivals(context.Background())
	p := h.read(t, path)
	if len(p.Data) != 2 || p.Data[0].Kind != "prediction" || p.Availability.Status != "ok" {
		t.Fatalf("obsolete demand blocked current plan: %+v", p)
	}
}

func TestCMArrivalCoverageAcrossMidnightIsPartial(t *testing.T) {
	cache := NewCache()
	cache.update("cm", &StaticData{Stops: []api.Stop{{Id: "cm:S", SourceId: "S", OperatorId: "cm"}}}, nil, cache.operator("cm"))
	_, h := securityServer(t, &Store{}, cache)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "[]") }))
	defer upstream.Close()
	f := NewFetcher(nil, cache, zap.NewNop())
	f.CM = upstream.URL
	now := time.Now().In(lisbon)
	end := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, lisbon)
	path := "/api/v1/arrivals?operators=cm&stop_id=cm:S&to=" + url.QueryEscape(end.Add(time.Hour).Format(time.RFC3339))
	_ = securityRequest(h, "GET", path, "", nil)
	f.RefreshArrivals(context.Background())
	w := securityRequest(h, "GET", path, "", nil)
	var p api.ArrivalPage
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if w.Code != 200 || p.Availability == nil || p.Availability.Status != "partial" || p.Availability.PlannedStatus != "partial" || !p.Availability.CoverageUntil.Equal(end) {
		t.Fatalf("tomorrow's unknown publication was called empty/complete: %d %s", w.Code, w.Body)
	}
}

func TestArrivalTokenCannotAliasAResultAfterRestart(t *testing.T) {
	old := newArrivalHarness(t)
	old.fetcher.RefreshArrivals(context.Background())
	first := old.read(t, old.path+"&limit=1")
	restarted := newArrivalHarness(t)
	_ = restarted.read(t, old.path)
	restarted.fetcher.RefreshArrivals(context.Background())
	current := restarted.read(t, old.path+"&limit=1")
	if *current.Page.Revision == *first.Page.Revision {
		t.Fatal("result token reused across stores")
	}
	w := securityRequest(restarted.handler, "GET", old.path+"&offset=1&revision="+url.QueryEscape(*first.Page.Revision), "", nil)
	if w.Code != 410 {
		t.Fatalf("old token silently read a new process result: %d %s", w.Code, w.Body)
	}
}

func TestArrivalVehicleLinkIsRemovedWhenCurrentJourneyChanges(t *testing.T) {
	h := newArrivalHarness(t)
	v := api.Vehicle{Id: "carris:unit", SourceId: "unit", OperatorId: "carris", PlanId: ptr("plan"), TripId: ptr("carris:T"), OperationalDate: ptr(h.now.In(lisbon).Format("2006-01-02")), ObservedAt: h.now}
	h.cache.update("carris", nil, &LiveData{Vehicles: []api.Vehicle{v}}, h.cache.operator("carris"))
	h.fetcher.RefreshArrivals(context.Background())
	first := h.read(t, h.path)
	if first.Data[0].VehicleId == nil {
		t.Fatal("valid vehicle link missing")
	}
	v.TripId = ptr("carris:another-journey")
	h.cache.update("carris", nil, &LiveData{Vehicles: []api.Vehicle{v}}, h.cache.operator("carris"))
	next := h.read(t, h.path)
	if len(next.Data) != 2 || next.Data[0].Kind != "prediction" || next.Data[0].VehicleId != nil {
		t.Fatal("current GPS changes removed the ETA or kept an obsolete journey link")
	}
}
