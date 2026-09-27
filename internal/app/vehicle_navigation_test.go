package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"lisboapublica/internal/api"
)

func callsFixture(t *testing.T, operator string) (*Cache, *StaticData, api.Vehicle, http.Handler) {
	t.Helper()
	now := time.Now().UTC()
	day := now.In(lisbon).Format("2006-01-02")
	date := now.In(lisbon).Format("20060102")
	d := &StaticData{PlanID: "plan", ValidFrom: date, ValidUntil: date, Source: "https://official.example/gtfs", Models: map[string]Metadata{}, Schedule: &Schedule{Exceptions: map[string]map[string]int{"daily": {date: 1}}, Parents: map[string]string{"s": "station"}}}
	for _, id := range []string{"s", "next", "last"} {
		d.Stops = append(d.Stops, api.Stop{Id: qualify(operator, id), SourceId: id, OperatorId: operator, Name: "Paragem " + id, Lat: 38.73, Lon: -9.14, RouteIds: []string{qualify(operator, "r")}})
	}
	d.Schedule.Trips = []ScheduledTrip{{ID: "trip", Route: "r", Service: "daily", Label: "18456", Times: []StopTime{{Stop: "s", Sequence: 0, Arrival: 100}, {Stop: "next", Sequence: 1, Arrival: 200}, {Stop: "last", Sequence: 2, Arrival: 300}}}}
	v := api.Vehicle{Id: qualify(operator, "v"), SourceId: "v", OperatorId: operator, PlanId: ptr("plan"), TripId: ptr(qualify(operator, "trip")), OperationalDate: &day, RouteId: ptr(qualify(operator, "r")), StopId: ptr(qualify(operator, "s")), CurrentStatus: ptr(api.VehicleCurrentStatus("STOPPED_AT")), ObservedAt: now, CollectedAt: now, Lat: 38.73, Lon: -9.14, PositionKind: "reported", SourceUrl: "https://official.example/positions", Model: ptr("Original model")}
	cache := NewCache()
	op := cache.operator(operator)
	op.Status = "ok"
	op.LiveUpdatedAt = &now
	cache.update(operator, d, &LiveData{Vehicles: []api.Vehicle{v}, Collected: now}, op)
	_, h := securityServer(t, &Store{}, cache)
	return cache, d, v, h
}
func readCalls(t *testing.T, h http.Handler, path string) api.VehicleCallsPage {
	t.Helper()
	response := securityRequest(h, "GET", path, "", nil)
	if response.Code != 200 {
		t.Fatalf("calls %d %s", response.Code, response.Body.String())
	}
	var out api.VehicleCallsPage
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestVehicleCallsAllProvidersAndPublishedProgress(t *testing.T) {
	for _, operator := range []string{"carris", "cm", "mobi", "tcb", "cp", "metro", "ttsl", "fertagus"} {
		t.Run(operator, func(t *testing.T) {
			_, _, v, h := callsFixture(t, operator)
			page := readCalls(t, h, "/api/v1/vehicles/"+url.PathEscape(v.Id)+"/calls?limit=1")
			if page.Coverage != "regional_subset" || page.Progress != "known" || page.Page.Total != 2 || page.Data[0].StopId != qualify(operator, "next") || page.Data[0].Kind != "scheduled" || page.Data[0].Stop == nil {
				t.Fatalf("wrong visits %+v", page)
			}
			next := readCalls(t, h, "/api/v1/vehicles/"+url.PathEscape(v.Id)+"/calls?limit=1&offset=1&revision="+url.QueryEscape(*page.Page.Revision))
			if next.Data[0].StopId != qualify(operator, "last") {
				t.Fatal("paging repeated visit")
			}
		})
	}
}
func TestNavigationBindsFrozenServiceAndFailsClosed(t *testing.T) {
	cache, d, v, h := callsFixture(t, "cp")
	state, _ := cache.state("")
	reference := vehicleReference(state, state.Created, v)
	newData := *d
	newData.PlanID = "later-plan"
	newData.Models = map[string]Metadata{"v": {Model: "Later model"}}
	newVehicle := v
	newVehicle.TripId = ptr("cp:new-trip")
	newVehicle.PlanId = ptr(newData.PlanID)
	cache.update("cp", &newData, &LiveData{Vehicles: []api.Vehicle{newVehicle}, Collected: v.CollectedAt}, cache.operator("cp"))
	path := "/api/v1/vehicles/cp:v/calls?reference=" + url.QueryEscape(reference.Reference)
	page := readCalls(t, h, path)
	if textValue(page.Vehicle.TripId) != *v.TripId || textValue(page.Vehicle.Model) != "Original model" || page.Page.Total != 2 {
		t.Fatal("reference jumped to later service/model")
	}
	if got := securityRequest(h, "GET", "/api/v1/vehicles/cp:wrong/calls?reference="+url.QueryEscape(reference.Reference), "", nil); got.Code != 400 {
		t.Fatal("reference accepted for wrong vehicle")
	}
	altered := navigationFor(state, state.Created, v)
	altered.Day = "2000-01-01"
	if got := securityRequest(h, "GET", "/api/v1/vehicles/cp:v/calls?reference="+url.QueryEscape(encodeNavigation("n1.", altered)), "", nil); got.Code != 410 {
		t.Fatal("altered tuple accepted")
	}
	cache.mu.Lock()
	delete(cache.versions, state.Revision)
	cache.mu.Unlock()
	if got := securityRequest(h, "GET", path, "", nil); got.Code != 410 {
		t.Fatal("evicted reference did not expire")
	}
}
func TestStationVehicleFilterAndUniqueServiceAssociation(t *testing.T) {
	cache, _, v, h := callsFixture(t, "metro")
	for _, stop := range []string{"metro:s", "metro:station"} {
		response := securityRequest(h, "GET", "/api/v1/vehicles?operators=metro&stop_id="+url.QueryEscape(stop), "", nil)
		var page api.VehiclePage
		_ = json.Unmarshal(response.Body.Bytes(), &page)
		if response.Code != 200 || len(page.Data) != 1 || page.Data[0].VehicleRef == nil {
			t.Fatal("published station references missing")
		}
	}
	for _, query := range []string{"operators=cp&stop_id=metro:s", "operators=metro&stop_id=metro:unknown"} {
		got := securityRequest(h, "GET", "/api/v1/vehicles?"+query, "", nil)
		if got.Code == 200 {
			t.Fatal("invalid station selector accepted")
		}
	}
	other := v
	other.Id = "metro:another"
	other.SourceId = "another"
	cache.update("metro", nil, &LiveData{Vehicles: []api.Vehicle{v, other}, Collected: v.CollectedAt}, cache.operator("metro"))
	state, _ := cache.state("")
	links, err := serviceVehicles(context.Background(), state, "metro")
	if err != nil || links[serviceFor(v)] != nil {
		t.Fatal("ambiguous Metro trip linked to arbitrary entity")
	}
}
func TestVehicleCallsPlanMismatchLoopsAndRetention(t *testing.T) {
	cache, d, v, h := callsFixture(t, "cp")
	v.PlanId = ptr("old")
	cache.update("cp", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cp"))
	page := readCalls(t, h, "/api/v1/vehicles/cp:v/calls")
	if page.Availability != "plan_mismatch" || len(page.Data) != 0 || textValue(page.Vehicle.Model) != "Original model" {
		t.Fatal("mismatch was enriched")
	}
	v.PlanId = ptr(d.PlanID)
	visits := append(append([]StopTime{}, d.Schedule.Trips[0].Times...), StopTime{Stop: "s", Sequence: 3})
	if _, known := vehicleProgress(v, visits); known {
		t.Fatal("loop guessed first occurrence")
	}
	v.LastKnown = true
	if _, known := vehicleProgress(v, d.Schedule.Trips[0].Times); known {
		t.Fatal("retained state used as current progress")
	}
	v.ObservedAt = time.Now().Add(-lastKnownLifetime - time.Second)
	cache.update("cp", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cp"))
	if got := securityRequest(h, "GET", "/api/v1/vehicles/cp:v/calls", "", nil); got.Code != 404 {
		t.Fatal("expired observation remained navigable")
	}
}
func TestVehicleCallsPredictionExpiryAcrossPages(t *testing.T) {
	cache, d, v, h := callsFixture(t, "cp")
	v.StopId = nil
	cache.update("cp", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cp"))
	day, _ := time.Parse("2006-01-02", *v.OperationalDate)
	now := time.Now()
	cp := &CPData{PlanID: d.PlanID, Availability: api.CPPredictionAvailability{Status: "ok"}}
	for i, stop := range d.Stops {
		cp.Rows = append(cp.Rows, api.CPPrediction{Id: stop.Id, OperatorId: "cp", PlanId: d.PlanID, SourceTripId: *v.TripId, ServiceDate: &openapi_types.Date{Time: day}, StopId: stop.Id, StopName: stop.Name, StopSequence: i, ExpectedAt: now.Add(time.Minute), ValidUntil: now.Add(500 * time.Millisecond)})
	}
	cache.updateCP(d, cp)
	first := readCalls(t, h, "/api/v1/vehicles/cp:v/calls?limit=1")
	if first.ValidUntil == nil || first.Data[0].Kind != "predicted" {
		t.Fatal("missing source expiry")
	}
	time.Sleep(time.Until(*first.ValidUntil) + 10*time.Millisecond)
	path := "/api/v1/vehicles/cp:v/calls?limit=1&offset=1&revision=" + url.QueryEscape(*first.Page.Revision)
	if got := securityRequest(h, "GET", path, "", nil); got.Code != 410 {
		t.Fatalf("expired calls page %d %s", got.Code, got.Body.String())
	}
	fallback := readCalls(t, h, "/api/v1/vehicles/cp:v/calls?limit=1")
	if fallback.ValidUntil != nil || fallback.Data[0].Kind != "scheduled" {
		t.Fatal("expired forecast resurrected instead of planned fallback")
	}
}

func TestVehicleCallsRegionalSubsetAndResponseOnlyReferences(t *testing.T) {
	cache, d, v, h := callsFixture(t, "cp")
	d = cpEndpointFixture(t)
	now := time.Now().UTC()
	v.PlanId = ptr("plan")
	v.TripId = ptr("cp:A")
	v.RouteId = ptr("cp:1")
	v.OperationalDate = ptr("2026-09-26")
	v.StopId = nil
	enrichScheduledService(&v, d)
	cache.update("cp", d, &LiveData{Vehicles: []api.Vehicle{v}, Collected: now}, cache.operator("cp"))
	page := readCalls(t, h, "/api/v1/vehicles/cp:v/calls")
	if page.Coverage != "regional_subset" || page.Page.Total != 1 || page.Data[0].StopName != "Oriente" || page.Vehicle.ScheduledService == nil || page.Vehicle.ScheduledService.OriginName != "Porto" || page.Vehicle.ScheduledService.DestinationName != "Faro" {
		t.Fatal("national endpoints/local visits confused")
	}
	state, _ := cache.state("")
	if state.Live["cp"].Vehicles[0].VehicleRef != nil {
		t.Fatal("response reference persisted in cache")
	}
}
func TestVehicleCallsCMNextStopAndBoundedReferences(t *testing.T) {
	cache, d, v, h := callsFixture(t, "cm")
	d.Schedule = nil
	v.PlanId = nil
	v.OperationalDate = nil
	cache.update("cm", d, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cm"))
	page := readCalls(t, h, "/api/v1/vehicles/cm:v/calls")
	if page.Availability != "next_stop_only" || page.Progress != "unknown" || len(page.Data) != 1 || page.Data[0].Kind != "published_route" || page.Data[0].ScheduledAt != nil || page.Data[0].ExpectedAt != nil {
		t.Fatal("fallback invented journey or ETA")
	}
	v.TripId = ptr(strings.Repeat("t", 3000))
	cache.update("cm", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cm"))
	if got := securityRequest(h, "GET", "/api/v1/vehicles/cm:v/calls", "", nil); got.Code != 404 {
		t.Fatal("oversized navigation should fail closed, not panic")
	}
}
func TestPublishedProgressExcludesEarlierPredictions(t *testing.T) {
	cache, d, v, _ := callsFixture(t, "cp")
	state, _ := cache.state("")
	day, _ := time.Parse("2006-01-02", *v.OperationalDate)
	state.CP = &CPData{PlanID: d.PlanID, Availability: api.CPPredictionAvailability{Status: "ok"}, Rows: []api.CPPrediction{{Id: "previous", PlanId: d.PlanID, SourceTripId: *v.TripId, ServiceDate: &openapi_types.Date{Time: day}, StopId: "cp:s", StopSequence: 0, ExpectedAt: state.Created.Add(time.Hour), ValidUntil: state.Created.Add(time.Minute)}}}
	rows, _, progress, _, err := vehicleCalls(context.Background(), state, v, state.Created, true)
	if err != nil || progress != "known" || len(rows) != 2 || rows[0].StopId != "cp:next" {
		t.Fatal("already visited station reappeared in remaining calls")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, err := vehicleCalls(ctx, state, v, state.Created, true); err != context.Canceled {
		t.Fatal("read ignored cancellation")
	}
}
func TestMobiLegacySeatsRequireExactProviderAndPreserveQuality(t *testing.T) {
	for _, operator := range []string{"mobi", "carris", "metro", "fertagus"} {
		p, _ := providerByID(operator)
		g := gtfsReader{provider: p, data: &StaticData{Models: map[string]Metadata{}}}
		if err := g.vehicle(map[string]string{"vehicle_id": "v", "available_seats": "0", "agency_id": "21"}); err != nil {
			t.Fatal(err)
		}
		seats := g.data.Models["v"].SeatedCapacity
		if operator == "mobi" {
			if seats == nil || *seats != 0 {
				t.Fatal("zero capacity lost")
			}
		} else if seats != nil {
			t.Fatal("unverified legacy provider capacity applied")
		}
	}
	p, _ := providerByID("mobi")
	g := gtfsReader{provider: p, data: &StaticData{Models: map[string]Metadata{}}}
	for _, value := range []string{"-1", "10001", "unknown"} {
		_ = g.vehicle(map[string]string{"vehicle_id": "v", "available_seats": value, "agency_id": "21"})
		if g.data.Models["v"].SeatedCapacity != nil {
			t.Fatal("invalid seats accepted")
		}
	}
	for _, agency := range []string{"", "wrong", "1"} {
		_ = g.vehicle(map[string]string{"vehicle_id": "v", "agency_id": agency, "available_seats": "19"})
		if g.data.Models["v"].SeatedCapacity != nil {
			t.Fatal("unverified legacy agency joined")
		}
	}
	merged := mergeMetadata(Metadata{SeatedCapacity: ptr(19)}, Metadata{SeatedCapacity: ptr(42)})
	if *merged.SeatedCapacity != 42 {
		t.Fatal("current published capacity lost precedence")
	}
}

func TestMetroStationCrosswalkRequiresBothDirectionsUnique(t *testing.T) {
	cache, d, v, _ := callsFixture(t, "metro")
	v.StopId = nil
	v.StopName = nil
	v.SourceStopId = ptr("[IA2N9]SP")
	state, _ := cache.state("")
	stop := d.Stops[0]
	state.Metro = &MetroData{Stations: []MetroStation{{ID: "SP", Name: stop.Name, Lat: strconv.FormatFloat(stop.Lat, 'f', 6, 64), Lon: strconv.FormatFloat(stop.Lon, 'f', 6, 64)}}}
	linked := v
	resolveMetroVehicleStop(&linked, state)
	if textValue(linked.StopId) != stop.Id {
		t.Fatal("unique official station code did not link")
	}
	duplicate := state.Metro.Stations[0]
	duplicate.ID = "another-code"
	state.Metro.Stations = append(state.Metro.Stations, duplicate)
	linked = v
	resolveMetroVehicleStop(&linked, state)
	if linked.StopId != nil {
		t.Fatal("two official station codes resolved one target")
	}
	state.Metro.Stations = state.Metro.Stations[:1]
	twin := stop
	twin.Id = "metro:twin"
	d.Stops = append(d.Stops, twin)
	d.Schedule.Trips[0].Times = append(d.Schedule.Trips[0].Times, StopTime{Stop: "twin", Sequence: 3})
	linked = v
	resolveMetroVehicleStop(&linked, state)
	if linked.StopId != nil {
		t.Fatal("one station resolved ambiguous GTFS targets")
	}
}

func TestCallsFinalValidationRejectsExpiredPredictionsAndObservations(t *testing.T) {
	cache, d, v, _ := callsFixture(t, "cp")
	v.StopId = nil
	state, _ := cache.state("")
	day, _ := time.Parse("2006-01-02", *v.OperationalDate)
	expiry := time.Now().Add(40 * time.Millisecond)
	state.CP = &CPData{PlanID: d.PlanID, Availability: api.CPPredictionAvailability{Status: "ok"}, Rows: []api.CPPrediction{{Id: "prediction", PlanId: d.PlanID, SourceTripId: *v.TripId, ServiceDate: &openapi_types.Date{Time: day}, StopId: "cp:s", StopSequence: 0, ExpectedAt: state.Created.Add(time.Minute), ValidUntil: expiry}}}
	result, err := buildVehicleCalls(context.Background(), state, v, state.Created, true)
	if err != nil {
		t.Fatal(err)
	}
	read := callsRead{State: state, AsOf: state.Created, Filter: Filter{Limit: 20}, Cursor: callsRevision{Reference: vehicleReference(state, state.Created, v).Reference, Predictions: true}, Result: result}
	out := callsResponse(result, v, read.Filter, read.Cursor)
	time.Sleep(time.Until(expiry) + time.Millisecond)
	fallback, err := finishCallsPage(context.Background(), read, out)
	if err != nil || fallback.ValidUntil != nil || fallback.Data[0].Kind != "scheduled" {
		t.Fatal("read crossing expiry did not replace whole initial result")
	}
	read.Filter.Revision = "pinned"
	if _, err := finishCallsPage(context.Background(), read, out); err == nil {
		t.Fatal("pinned result crossing expiry did not fail")
	}
	read.Filter.Revision = ""
	cancelled, cancelFallback := context.WithCancel(context.Background())
	late := &lateCancelContext{Context: cancelled, cancel: cancelFallback}
	if _, err := finishCallsPage(late, read, out); err != context.Canceled {
		t.Fatal("cancelled after fallback work was returned as successful")
	}
	cancelFallback()
	v.ObservedAt = time.Now().Add(-lastKnownLifetime)
	if err := validateCallsReturn(context.Background(), v, time.Now()); err == nil {
		t.Fatal("observation expired during read remained navigable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateCallsReturn(ctx, v, time.Now()); err != context.Canceled {
		t.Fatal("late read cancellation ignored")
	}
}
func TestNoReferenceClockIsCapturedAfterCacheSelection(t *testing.T) {
	cache, _, v, _ := callsFixture(t, "cp")
	earlier := time.Now().Add(-time.Second)
	cache.update("cp", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cp"))
	state, _ := cache.state("")
	server, _ := securityServer(t, &Store{}, cache)
	raw, err := server.latestNavigationReference(v.Id, earlier)
	if err != nil {
		t.Fatal(err)
	}
	var n vehicleNavigation
	if err := decodeNavigation(raw, "n1.", 2048, &n); err != nil {
		t.Fatal(err)
	}
	if time.Unix(0, n.AsOf).Before(state.Created) {
		t.Fatal("first page projected before selected publication")
	}
	if _, _, _, err := server.navigationState(raw, v.Id, time.Now()); err != nil {
		t.Fatal("ordinary first page rejected by publication clock")
	}
}
func TestStationVehicleKnownPlanMismatchFailsClosed(t *testing.T) {
	cache, d, v, h := callsFixture(t, "cp")
	later := *d
	later.PlanID = "replacement"
	cache.update("cp", &later, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cp"))
	response := securityRequest(h, "GET", "/api/v1/vehicles?operators=cp&stop_id=cp:s", "", nil)
	var page api.VehiclePage
	_ = json.Unmarshal(response.Body.Bytes(), &page)
	if response.Code != 200 || len(page.Data) != 0 {
		t.Fatal("old known plan referenced new catalog station")
	}
	state, _ := cache.state("")
	v.OperatorId = "cm"
	v.PlanId = nil
	v.StopId = ptr("cm:s")
	if !vehicleStopMatches(state, v, "cm:s") {
		t.Fatal("explicit no-plan CM reference was unnecessarily removed")
	}
}
func TestStopTimeCompactClocksRetainLegacyCacheCompatibility(t *testing.T) {
	maximum := maxGTFSServiceHours*3600 + 3599
	raw := fmt.Sprintf(`{"Stop":"S","Arrival":%d,"Departure":%d,"Sequence":2147483648}`, maximum, maximum)
	var visit StopTime
	if err := json.Unmarshal([]byte(raw), &visit); err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(visit)
	if err != nil {
		t.Fatal(err)
	}
	var decoded StopTime
	if err := json.Unmarshal(blob, &decoded); err != nil || decoded != visit || int(decoded.Arrival) != maximum || decoded.Sequence != 2147483648 {
		t.Fatal("bounded clocks or original sequence lost")
	}
	if err := json.Unmarshal([]byte(`{"Stop":"S","Arrival":2147483648}`), &decoded); err == nil {
		t.Fatal("oversized legacy clock silently truncated")
	}
}
func TestVehicleCallsRequireTransitScope(t *testing.T) {
	store := testStore(t)
	cache, _, v, _ := callsFixture(t, "cp")
	_, h := securityServer(t, store, cache)
	for _, scope := range []string{"read:history", "read:transit"} {
		key, secret, err := newPersonalKey("calls fixture", []string{scope})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.insertPersonalKey(context.Background(), "fixture@example.test", key, secret); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "https://example.test/api/v1/vehicles/"+url.PathEscape(v.Id)+"/calls", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 200
		if scope == "read:history" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("scope%s got%d", scope, w.Code)
		}
	}
}

type lateCancelContext struct {
	context.Context
	checks int
	cancel context.CancelFunc
}

func (c *lateCancelContext) Err() error {
	c.checks++
	if c.checks == 6 {
		c.cancel()
	}
	return c.Context.Err()
}
