package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func popupFixture(t *testing.T, op string, count int) (*Server, *StaticData, api.Vehicle) {
	t.Helper()
	now := time.Now().UTC()
	day := now.In(lisbon)
	data := fixtureStatic(op, now)
	data.PlanID = "plan"
	data.Schedule.CompleteJourneys = true
	data.Schedule.StopNames = map[string]string{"S": "Lisboa", "N": "Porto"}
	base := int(now.Sub(serviceStart(day)).Seconds())
	times := []StopTime{}
	for n := 0; n < count; n++ {
		stop := "N"
		if n == count/2 {
			stop = "S"
		}
		times = append(times, StopTime{Stop: stop, Sequence: n + 1, Arrival: int32(base + (n-count/2)*60), Departure: int32(base + (n-count/2)*60 + 30)})
	}
	trip := ScheduledTrip{ID: "T", Route: "1", Service: "daily", Headsign: "Porto", Direction: ptr(0), Times: []StopTime{times[count/2]}, JourneyTimes: times}
	data.Schedule.Trips = []ScheduledTrip{trip}
	cache := NewCache()
	cache.update(op, data, nil, staticHealth(cache.operator(op), data))
	vehicle := api.Vehicle{Id: qualify(op, "v"), SourceId: "v", OperatorId: op, PlanId: ptr("plan"), RouteId: ptr(qualify(op, "1")), TripId: ptr(qualify(op, "T")), OperationalDate: ptr(day.Format("2006-01-02")), StopId: ptr(qualify(op, "S")), CurrentStatus: ptr(api.INCOMINGAT), Lat: 38.73, Lon: -9.14, ObservedAt: now, CollectedAt: now, PositionKind: "reported", SourceUrl: hubBase}
	operator := cache.operator(op)
	operator.Status = "ok"
	operator.LiveUpdatedAt = &now
	cache.update(op, nil, &LiveData{Vehicles: []api.Vehicle{vehicle}, Collected: now}, operator)
	server, err := NewServer(&Store{}, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true, RateLimit: 10000}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return server, data, vehicle
}
func popupGET[T any](t *testing.T, s *Server, path string) T {
	t.Helper()
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
	if rr.Code != 200 {
		t.Fatalf("%s: %d %s", path, rr.Code, rr.Body.String())
	}
	var out T
	if err = json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestPopupContractAcrossOperators(t *testing.T) {
	for _, p := range providers {
		t.Run(p.ID, func(t *testing.T) {
			s, _, _ := popupFixture(t, p.ID, 3)
			board := popupGET[api.StopBoard](t, s, "/api/v1/stops/"+url.PathEscape(p.ID+":S")+"/board")
			if len(board.Directions) != 1 || board.Directions[0].DirectionKey == nil {
				t.Fatalf("direction missing: %+v", board)
			}
			calls := popupGET[api.StopCallPage](t, s, "/api/v1/stops/"+url.PathEscape(p.ID+":S")+"/board/calls?revision="+url.QueryEscape(board.Revision))
			if len(calls.Data) != 1 || calls.Data[0].Departure.Kind != "schedule" || calls.Data[0].Departure.At == nil {
				t.Fatalf("independent departure missing: %+v", calls)
			}
			journey := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/"+url.PathEscape(p.ID+":v")+"/journey")
			if p.ID == "metro" {
				if journey.Association != "unresolved" || len(journey.Data) != 0 {
					t.Fatal("guessed Metro journey exposed")
				}
				return
			}
			if journey.Association != "resolved" || !journey.Complete || len(journey.Data) != 3 || journey.Data[0].StopName != "Porto" {
				t.Fatalf("full journey lost: %+v", journey)
			}
			if journey.Data[0].Arrival.Kind != "unavailable" || journey.Data[0].Departure.Kind != "unavailable" {
				t.Fatal("past schedule presented as actual")
			}
		})
	}
}
func TestJourneyPaginationAndUnknownRepeatedProgress(t *testing.T) {
	s, data, v := popupFixture(t, "cp", 601)
	first := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/cp%3Av/journey?limit=100")
	if first.Page.Offset != 300 || first.NextIndex == nil || *first.NextIndex != 300 || first.Page.Total != 601 {
		t.Fatalf("next focus/pagination: %+v", first.Page)
	}
	start := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/cp%3Av/journey?limit=100&offset=0&revision="+url.QueryEscape(*first.Page.Revision))
	if start.Data[0].StopSequence != 1 || *start.Page.Revision != *first.Page.Revision {
		t.Fatal("inconsistent sequence/revision")
	}
	// Same published stop appears twice: no sequence is known, so no guessed next.
	data.Schedule.Trips[0].JourneyTimes[0].Stop = "S"
	v.StopId = ptr("cp:S")
	live := &LiveData{Vehicles: []api.Vehicle{v}, Collected: time.Now()}
	s.Cache.update("cp", nil, live, s.Cache.operator("cp"))
	unknown := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/cp%3Av/journey")
	if unknown.NextIndex != nil || unknown.Progress != "unknown" || unknown.Data[0].Phase != "unknown" {
		t.Fatal("repeat visit was guessed")
	}
}
func TestVehicleJourneyFailsClosedForIdentityAndService(t *testing.T) {
	for _, scenario := range []string{"date", "plan", "route", "duplicate", "frequency", "sequence"} {
		t.Run(scenario, func(t *testing.T) {
			s, data, v := popupFixture(t, "carris", 3)
			switch scenario {
			case "date":
				v.OperationalDate = nil
			case "plan":
				v.PlanId = ptr("other")
			case "route":
				v.RouteId = ptr("carris:other")
			case "duplicate":
				data.Schedule.Trips = append(data.Schedule.Trips, data.Schedule.Trips[0])
			case "frequency":
				data.Schedule.HasFrequencies = true
			case "sequence":
				data.Schedule.Trips[0].JourneyTimes[1].Sequence = 1
			}
			s.Cache.update("carris", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: time.Now()}, s.Cache.operator("carris"))
			out := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/carris%3Av/journey")
			if out.Association == "resolved" || len(out.Data) > 0 {
				t.Fatal("unsafe journey exposed")
			}
		})
	}
}
func TestMetroShortVariantSharesVerifiedOrientation(t *testing.T) {
	s, d, _ := popupFixture(t, "metro", 3)
	d.Routes = []api.RouteDetail{{Id: "metro:2_0", SourceId: "2_0", ShortName: "Am", Color: "#ff0"}, {Id: "metro:2_1", SourceId: "2_1", ShortName: "Am", Color: "#ff0"}}
	full := []StopTime{{Stop: "R", Sequence: 1}, {Stop: "S", Sequence: 2}, {Stop: "C", Sequence: 3}, {Stop: "O", Sequence: 4}}
	reverse := []StopTime{{Stop: "O", Sequence: 1}, {Stop: "C", Sequence: 2}, {Stop: "S", Sequence: 3}, {Stop: "R", Sequence: 4}}
	d.Schedule.Trips = []ScheduledTrip{{ID: "out", Route: "2_0", Headsign: "Odivelas", JourneyTimes: full, Times: full}, {ID: "back", Route: "2_0", Headsign: "Rato", JourneyTimes: reverse, Times: reverse}, {ID: "short", Route: "2_1", Headsign: "Campo Grande", Direction: ptr(1), JourneyTimes: full[:3], Times: full[:3]}}
	idx := d.journeys("metro")
	if len(idx.directions["metro:line:amarela"]) != 2 || !equalDirection(idx.direction[&d.Schedule.Trips[0]], idx.direction[&d.Schedule.Trips[2]]) {
		t.Fatal("short destination became a third direction")
	}
	_ = s
}
func TestStopTimesRemainCompleteOutsideMapAndCache(t *testing.T) {
	p, _ := providerByID("cp")
	blob := shapeArchive(t, false)
	blob = replaceGTFS(t, blob, map[string]string{"stops.txt": "stop_id,stop_name,stop_lat,stop_lon\nS,Local,38.72,-9.15\nN,Porto,41.15,-8.6\n", "stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nA,10:00:00,10:01:00,N,1\nA,12:00:00,12:01:00,S,2\nA,13:00:00,13:01:00,N,3\n"})
	d, err := readGTFS(blob, p, "plan", "20260101", "20261231", hubBase, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Stops) != 1 || len(d.Schedule.Trips[0].Times) != 1 || len(d.Schedule.Trips[0].JourneyTimes) != 3 {
		t.Fatal("map coverage or full journey changed incorrectly")
	}
	encoded, err := encodeCache(d)
	if err != nil {
		t.Fatal(err)
	}
	var restored StaticData
	reader, e := gzip.NewReader(bytes.NewReader(encoded))
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	if err = json.NewDecoder(reader).Decode(&restored); err != nil {
		t.Fatal(err)
	}
	if !restored.Schedule.CompleteJourneys || restored.Schedule.StopNames["N"] != "Porto" || len(restored.Schedule.Trips[0].JourneyTimes) != 3 {
		t.Fatal("restart lost journey metadata")
	}
}
func TestStoredStopEventsIndependentAndCorrectionRules(t *testing.T) {
	now := time.Now().UTC()
	journey := "carris|plan|T|2026-09-27"
	original := reportedStopEvent{Journey: journey, Sequence: 1, Kind: "arrival", Occurred: true, Revision: 1, Evidence: api.CallTimeEvidence{At: now.Add(-time.Minute), SourceUrl: hubBase, SourceUpdatedAt: &now, CollectedAt: &now}}
	call := api.StopCall{JourneyId: &journey, StopSequence: 1, Phase: "previous", Arrival: missingCallTime(""), Departure: missingCallTime("")}
	applyStoredEvents(&call, []reportedStopEvent{original}, now)
	if call.Arrival.Kind != "actual" || call.Departure.Kind != "unavailable" {
		t.Fatal("arrival copied to departure")
	}
	corrected := original
	corrected.Revision = 2
	corrected.Evidence.At = now.Add(-2 * time.Minute)
	applyStoredEvents(&call, []reportedStopEvent{original, corrected}, now)
	if call.Arrival.Kind != "unavailable" {
		t.Fatal("unverified correction applied")
	}
	corrected.Correction = true
	applyStoredEvents(&call, []reportedStopEvent{original, corrected}, now)
	if call.Arrival.At == nil || !call.Arrival.At.Equal(corrected.Evidence.At) {
		t.Fatal("explicit correction lost")
	}
	prediction := api.CallTimeEvidence{At: now.Add(-time.Second), SourceUrl: hubBase, ValidUntil: ptr(now.Add(time.Minute))}
	if selectCallTime(nil, &prediction, nil, false, now).Kind != "unavailable" {
		t.Fatal("elapsed ETA promoted")
	}
}
func TestDurableStopEventsAndPinnedCorrections(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	journey := "cp|plan|T|2026-09-27"
	event := reportedStopEvent{Journey: journey, Sequence: 1, Kind: "arrival", Occurred: true, Revision: 1, Evidence: api.CallTimeEvidence{At: now.Add(-time.Minute), SourceUrl: hubBase, SourceUpdatedAt: &now, CollectedAt: &now}}
	if err := store.saveReportedStopEvents(ctx, []reportedStopEvent{event}); err != nil {
		t.Fatal(err)
	}
	first, err := store.generation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.saveReportedStopEvents(ctx, []reportedStopEvent{event}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = store.DB.QueryRow(ctx, "SELECT count(*) FROM stop_events").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate polling grew events", count, err)
	}
	correction := event
	correction.Revision = 2
	correction.Correction = true
	correction.Evidence.At = now.Add(-2 * time.Minute)
	if err = store.saveReportedStopEvents(ctx, []reportedStopEvent{correction}); err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: &Store{DB: store.DB}}
	rows, err := server.journeyEvents(ctx, journey, first, now)
	if err != nil || len(rows) != 1 || !rows[0].Evidence.At.Equal(event.Evidence.At) {
		t.Fatal("pinned history changed", rows, err)
	}
	generation, _ := store.generation(ctx)
	rows, err = server.journeyEvents(ctx, journey, generation, now)
	if err != nil || len(rows) != 2 {
		t.Fatal("durable correction missing after restart", rows, err)
	}
	invalid := event
	invalid.Occurred = false
	if store.saveReportedStopEvents(ctx, []reportedStopEvent{invalid}) == nil {
		t.Fatal("prediction accepted as real")
	}
}

func TestCPDepartureOnlyPredictionIsNotAnArrival(t *testing.T) {
	data, now := cpTestData()
	u := cpTestUpdate(now, 0, true)
	// Start from the existing exact CP fixture and remove only the arrival event.
	stop := u.Stops[0]
	expected := now.Add(10 * time.Minute).Unix()
	stop.Arrival.Time = nil
	stop.Arrival.Delay = nil
	stop.Departure.Time = &expected
	u.Stops = []cpStopUpdate{stop}
	feed := cpTestFeed(now, u)
	out, err := normalizeCP(context.Background(), feed, data, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 0 || len(out.Departures) != 1 || out.Departures[0].ExpectedDepartureAt == nil {
		t.Fatalf("departure lost or copied to arrival: %+v", out)
	}
}

func TestSharedPredictionSourceAcrossAgencies(t *testing.T) {
	for _, p := range providers {
		if p.ID == "cm" || p.ID == "metro" {
			continue
		}
		t.Run(p.ID, func(t *testing.T) {
			d, now := cpTestData()
			d.Operator = p.ID
			d.Schedule.CompleteJourneys = true
			d.Stops[0].Id = qualify(p.ID, "S")
			d.Stops[0].OperatorId = p.ID
			d.Stops[1].Id = qualify(p.ID, "T")
			d.Stops[1].OperatorId = p.ID
			d.Routes[0].Id = qualify(p.ID, "1")
			u := cpTestUpdate(now, 0, true)
			u.Trip.ID = "[plan][" + p.Agency + "]A_20251214"
			u.Stops[0].Departure.Time = ptr(now.Add(12 * time.Minute).Unix())
			out, err := normalizeCP(context.Background(), cpTestFeed(now, u), d, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Rows) != 1 || len(out.Departures) != 1 || out.Rows[0].OperatorId != p.ID || out.Rows[0].SourceTripId != qualify(p.ID, "A_20251214") {
				t.Fatalf("source qualification lost: %+v", out)
			}
		})
	}
}
func TestCMPlanAssociationAndOperationalDate(t *testing.T) {
	s, d, v := popupFixture(t, "cm", 3)
	d.PlanID = ""
	d.Operator = "cm"
	t0 := &d.Schedule.Trips[0]
	t0.ID = "[plan][LA77N]T"
	t0.SourcePlan = "plan"
	t0.Agency = "LA77N"
	t0.SourceRoute = "1001_0"
	v.TripId = ptr(qualify("cm", t0.ID))
	v.SourceId = "[LA77N]v"
	v.OperationalDate = nil
	v.PlanId = nil
	v.ObservedAt = v.ObservedAt.Truncate(time.Second)
	raw := hubPosition{ID: v.SourceId, Agency: "LA77N", Trip: t0.ID, At: v.ObservedAt.Unix(), OperationalDate: 20260927}
	rows := []api.Vehicle{v}
	enrichCMOperationalDates(rows, []hubPosition{raw})
	if rows[0].OperationalDate == nil || rows[0].PlanId == nil {
		t.Fatal("exact source-date association lost")
	}
	enrichVehicle(&rows[0], d)
	if rows[0].RouteId == nil {
		t.Fatal("multi-plan route was incorrectly removed")
	}
	rows[0].OperationalDate = ptr(time.Now().In(lisbon).Format("2006-01-02"))
	s.Cache.update("cm", nil, &LiveData{Vehicles: rows, Collected: time.Now()}, s.Cache.operator("cm"))
	out := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/cm%3Av/journey")
	if out.Association != "resolved" {
		t.Fatal("verified CM journey rejected", out.Message)
	}
	rows = []api.Vehicle{v}
	raw.At++
	enrichCMOperationalDates(rows, []hubPosition{raw})
	if rows[0].OperationalDate != nil {
		t.Fatal("different observation borrowed a date")
	}
}
func TestCMReusesArchiveForFullSchedule(t *testing.T) {
	p, _ := providerByID("cm")
	plan := &hubPlan{ID: "plan", Agency: "LA77N", From: 20260101, Until: 20261231}
	out := &StaticData{Schedule: &Schedule{Calendars: map[string]Calendar{}, Exceptions: map[string]map[string]int{}, StopNames: map[string]string{}, Parents: map[string]string{}, CompleteJourneys: true}}
	if err := (cmJourneyImport{out, plan, p, hubBase}).merge(shapeArchive(t, true)); err != nil {
		t.Fatal(err)
	}
	if len(out.Schedule.Trips) != 3 || out.Schedule.Trips[0].Route != "1001" || out.Schedule.Trips[0].SourcePlan != "plan" || len(out.Schedule.Trips[0].JourneyTimes) != 1 {
		t.Fatal("CM schedule or provenance lost")
	}
}
func TestJourneyMissingTimesAndServiceDatesAcrossDST(t *testing.T) {
	for _, date := range []string{"2026-03-29", "2026-10-25"} {
		day, err := time.ParseInLocation("2006-01-02", date, lisbon)
		if err != nil {
			t.Fatal(err)
		}
		d := fixtureStatic("cp", time.Now())
		d.Schedule.StopNames = map[string]string{"S": "Lisboa"}
		trip := &d.Schedule.Trips[0]
		trip.Direction = ptr(0)
		idx := d.journeys("cp")
		visit := StopTime{Stop: "S", Sequence: 1, Arrival: 25 * 3600, Departure: 25*3600 + 60}
		call := (plannedPopupJourney{d, "cp", trip, day, idx, serviceStart(day)}).call(visit)
		if call.Arrival.At == nil || !call.Arrival.At.Equal(serviceStart(day).Add(25*time.Hour)) || call.Departure.At.Sub(*call.Arrival.At) != time.Minute {
			t.Fatal("GTFS service-day conversion changed")
		}
		visit.Arrival = -1
		call = (plannedPopupJourney{d, "cp", trip, day, idx, serviceStart(day)}).call(visit)
		if call.Arrival.At != nil || call.Departure.At == nil {
			t.Fatal("missing arrival filled from departure")
		}
	}
}

func TestStoppedVehicleFocusesFollowingVisitWithoutInventingArrival(t *testing.T) {
	s, _, v := popupFixture(t, "carris", 3)
	v.CurrentStatus = ptr(api.STOPPEDAT)
	s.Cache.update("carris", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: time.Now()}, s.Cache.operator("carris"))
	out := popupGET[api.VehicleJourney](t, s, "/api/v1/vehicles/carris%3Av/journey")
	if out.NextIndex == nil || *out.NextIndex != 2 || out.Data[1].Phase != "current" || out.Data[1].Arrival.Kind != "unavailable" || out.Data[1].Departure.Kind != "schedule" || out.Data[2].Departure.Kind != "unavailable" {
		t.Fatal("stopped state invented an event or terminal departure")
	}
	conflict := missingCallTime("Registos reais contraditórios na fonte")
	if pastCallTime(conflict, time.Now()).Reason != conflict.Reason {
		t.Fatal("past-time selection hid a source conflict")
	}
}
