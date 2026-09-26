package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func TestServiceStateDurableCacheWithoutHistoricalPayload(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	d := cpEndpointFixture(t)
	now := time.Now().UTC()
	v := continuityVehicle("train", now)
	v.CurrentStatus, v.StopName, v.SourceStopId = ptr(api.STOPPEDAT), ptr("Oriente"), ptr("S")
	v.PlanId, v.TripId, v.RouteId, v.OperationalDate = ptr("plan"), ptr("cp:A"), ptr("cp:1"), ptr("2026-09-26")
	enrichScheduledService(&v, d)
	op := api.Operator{Id: "cp", Status: api.OperatorStatusOk, StaticStatus: api.OperatorStaticStatusOk, LiveUpdatedAt: &now}
	live, _ := nextLive(nil, op, []api.Vehicle{v}, now)
	if err := s.Save(ctx, "cp", d, live, op, nil); err != nil {
		t.Fatal(err)
	}
	cache := NewCache()
	if err := s.Restore(ctx, cache); err != nil {
		t.Fatal(err)
	}
	state, _ := cache.state("")
	got := state.Live["cp"].Vehicles[0]
	if got.CurrentStatus == nil || *got.CurrentStatus != api.STOPPEDAT || got.ScheduledService == nil || got.ScheduledService.OriginName != "Porto" || !got.ObservedAt.Equal(now) || !state.Live["cp"].Unverified {
		t.Fatal("restore lost provenance or freshness")
	}
	var payload string
	if err := s.DB.QueryRow(ctx, "SELECT payload::text FROM snapshots WHERE operator_id='cp' LIMIT 1").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, "current_status") || strings.Contains(payload, "scheduled_service") || strings.Contains(payload, "stop_name") {
		t.Fatal("live state inflated history")
	}
	// Legacy optional fields remain unknown, without populating modern metadata.
	var legacy api.Vehicle
	if json.Unmarshal([]byte(`{"id":"cp:old","operator_id":"cp"}`), &legacy) != nil || legacy.CurrentStatus != nil || legacy.ScheduledService != nil {
		t.Fatal("legacy fields manufactured")
	}
}

func TestServiceEnrichmentFrozenAcrossStaticRefresh(t *testing.T) {
	f, s := continuityFetcher()
	p, _ := providerByID("cp")
	now := time.Now().UTC()
	d := cpEndpointFixture(t)
	f.Cache.update("cp", d, nil, staticHealth(f.Cache.operator("cp"), d))
	v := f.hubVehicle(p, hubPosition{ID: "[N18KL]v", Trip: "[plan][N18KL]A", Route: "[N18KL]1", OperationalDate: 20260926, Status: ptr("STOPPED_AT"), Stop: ptr("S"), Lat: 38.72, Lon: -9.15}, now, now)
	f.saveLive(context.Background(), p, []api.Vehicle{v}, now)
	first := vehiclePage(t, s, "operators=cp")
	changed := cpEndpointFixture(t)
	changed.Stops[0].Name = "Different name"
	changed.Schedule.Trips[0].Endpoints.Origin.Name = "Different origin"
	f.Cache.update("cp", changed, nil, staticHealth(f.Cache.operator("cp"), changed))
	pinned := vehiclePage(t, s, "operators=cp&revision="+*first.Page.Revision)
	latest := vehiclePage(t, s, "operators=cp")
	for _, page := range []api.VehiclePage{first, pinned, latest} {
		if len(page.Data) != 1 || page.Data[0].StopName == nil || *page.Data[0].StopName != "Oriente" || page.Data[0].ScheduledService == nil || page.Data[0].ScheduledService.OriginName != "Porto" {
			t.Fatal("old observation enriched from later mutable metadata")
		}
	}
}
