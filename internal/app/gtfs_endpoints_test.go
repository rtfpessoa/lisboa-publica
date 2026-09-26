package app

import (
	"context"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func cpEndpointFixture(t *testing.T) *StaticData {
	t.Helper()
	blob := replaceGTFS(t, shapeArchive(t, false), map[string]string{
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nO,Porto,41.15,-8.61\nS,Oriente,38.72,-9.15\nD,Faro,37.01,-7.94\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nA,26:00:00,26:00:00,D,3\nA,23:00:00,23:00:00,O,1\nA,24:00:00,24:00:00,S,2\n",
	})
	p, _ := providerByID("cp")
	d, err := readGTFS(blob, p, "plan", "20260101", "20261231", "https://official.example/gtfs", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCPFullScheduledEndpointsAndLocalNetwork(t *testing.T) {
	d := cpEndpointFixture(t)
	day, _ := time.ParseInLocation("2006-01-02", "2026-09-26", lisbon)
	q := scheduleQuery{ctx: context.Background(), data: d, operator: "cp"}
	trip := q.trip(d.Schedule.Trips[0], day)
	if len(d.Stops) != 1 || len(d.Schedule.Trips[0].Times) != 1 || trip.ScheduledService == nil || trip.ScheduledService.OriginName != "Porto" || trip.ScheduledService.DestinationName != "Faro" || trip.ScheduledService.ServiceDate != "2026-09-26" {
		t.Fatalf("clipped journey or expanded network: %+v", trip)
	}
	if !trip.PlannedDeparture.Equal(serviceStart(day).Add(24*time.Hour)) || !trip.PlannedEnd.Equal(trip.PlannedDeparture) {
		t.Fatal("regional times changed to national journey times")
	}
	if d.Schedule.Trips[0].scheduledEndpoints(d.Source, day).OriginSourceStopId != "O" {
		t.Fatal("full origin identity lost")
	}
}

func TestCPObservedServiceRequiresExplicitDatePlanRoute(t *testing.T) {
	d := cpEndpointFixture(t)
	v := api.Vehicle{OperatorId: "cp", PlanId: ptr("plan"), TripId: ptr("cp:A"), RouteId: ptr("cp:1"), OperationalDate: ptr("2026-09-26")}
	enrichScheduledService(&v, d)
	if v.ScheduledService == nil || v.ScheduledService.ServiceDate != "2026-09-26" {
		t.Fatal("exact join missing")
	}
	for _, change := range []func(*api.Vehicle){func(v *api.Vehicle) { v.PlanId = ptr("other") }, func(v *api.Vehicle) { v.OperationalDate = nil }, func(v *api.Vehicle) { v.OperationalDate = ptr("2026-02-30") }, func(v *api.Vehicle) { v.RouteId = ptr("cp:other") }, func(v *api.Vehicle) { v.TripId = ptr("cp:unknown") }, func(v *api.Vehicle) { v.OperationalDate = ptr("2027-01-01") }} {
		other := v
		other.ScheduledService = nil
		change(&other)
		enrichScheduledService(&other, d)
		if other.ScheduledService != nil {
			t.Fatal("unverified journey join")
		}
	}
	d.Schedule.Exceptions["daily"] = map[string]int{"20260926": 2}
	v.ScheduledService = nil
	enrichScheduledService(&v, d)
	if v.ScheduledService != nil {
		t.Fatal("cancelled calendar service joined")
	}
}

func TestLegacyCPEndpointsRefreshOnceWithoutChangingNormalTTL(t *testing.T) {
	cache := NewCache()
	p, _ := providerByID("cp")
	d := cpEndpointFixture(t)
	d.CPJourneyEndpoints = false
	cache.update("cp", d, nil, staticHealth(cache.operator("cp"), d))
	state, _ := cache.state("")
	if reusableStaticCache(p, state) {
		t.Fatal("legacy endpoints skipped until TTL")
	}
	parsed := cpEndpointFixture(t)
	cache.update("cp", parsed, nil, staticHealth(cache.operator("cp"), parsed))
	state, _ = cache.state("")
	if !reusableStaticCache(p, state) {
		t.Fatal("normal TTL not restored")
	}
	blob, err := encodeCache(parsed)
	if err != nil || len(blob) == 0 {
		t.Fatal("metadata version not serializable", err)
	}
}
