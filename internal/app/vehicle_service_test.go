package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func TestPublishedStatusDecodingAndStopJoin(t *testing.T) {
	f, _ := continuityFetcher()
	p, _ := providerByID("mobi")
	now := time.Now().UTC()
	d := fixtureStatic(p.ID, now)
	d.PlanID = "plan"
	f.Cache.update(p.ID, d, nil, staticHealth(f.Cache.operator(p.ID), d))
	for _, status := range []string{"STOPPED_AT", "INCOMING_AT", "IN_TRANSIT_TO", "UNKNOWN", ""} {
		raw := hubPosition{ID: "[HF16N]v", Agency: p.Agency, Trip: "[plan][HF16N]T", Stop: ptr("S"), Status: ptr(status), OperationalDate: 20260926}
		v := f.hubVehicle(p, raw, now, now)
		if (v.CurrentStatus != nil) != (status != "UNKNOWN" && status != "") || v.StopName == nil || *v.StopName != "Estação" || v.OperationalDate == nil || *v.OperationalDate != "2026-09-26" {
			t.Fatalf("published status lost or manufactured: %+v", v)
		}
	}
	for _, trip := range []string{"T", "[other][HF16N]T", "[plan][OTHER]T"} {
		v := f.hubVehicle(p, hubPosition{ID: "v", Trip: trip, Stop: ptr("S")}, now, now)
		if v.StopId != nil || v.StopName != nil {
			t.Fatal("unverified plan joined a stop")
		}
	}
	if publishedServiceDate(20260230) != nil || boundedStopReference(ptr("")) != nil {
		t.Fatal("invalid source metadata accepted")
	}
}

func TestCMStoppedObservationKeepsPublishedReference(t *testing.T) {
	f, _ := continuityFetcher()
	p, _ := providerByID("cm")
	now := time.Now().UTC()
	d := fixtureStatic(p.ID, now)
	f.Cache.update(p.ID, d, nil, f.Cache.operator(p.ID))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "v", "lat": 38.72, "lon": -9.15, "timestamp": now.UnixMilli(), "current_status": "STOPPED_AT", "stop_id": "S"}})
	}))
	defer ts.Close()
	f.CM = ts.URL
	rows, err := f.cmLive(context.Background(), p, now)
	if err != nil || len(rows) != 1 || rows[0].CurrentStatus == nil || *rows[0].CurrentStatus != api.STOPPEDAT || rows[0].StopId == nil || *rows[0].StopId != "cm:S" || rows[0].SpeedKmh != nil {
		t.Fatalf("CM status lost or fake speed: %+v %v", rows, err)
	}
}

func TestStoppedStateOriginalClocksAndResume(t *testing.T) {
	now := time.Now().UTC()
	op := api.Operator{Status: api.OperatorStatusOk}
	v := continuityVehicle("stopped", now)
	v.CurrentStatus, v.StopName = ptr(api.STOPPEDAT), ptr("Oriente")
	d, _ := nextLive(nil, op, []api.Vehicle{v}, now)
	d, _ = nextLive(d, op, nil, now.Add(5*time.Minute))
	rows, reported, _, _, _ := projectLive(d, op, nil, now.Add(5*time.Minute))
	if len(rows) != 1 || *reported != 0 || rows[0].CurrentStatus == nil || *rows[0].CurrentStatus != api.STOPPEDAT || !rows[0].InactiveAt.Equal(now.Add(5*time.Minute)) || len(d.Samples) != 0 {
		t.Fatal("missing row became active or lost state")
	}
	blob, err := encodeCache(d)
	var restored LiveData
	if err != nil {
		t.Fatal(err)
	}
	rd, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	if json.NewDecoder(rd).Decode(&restored) != nil || restored.LastKnown[0].StopName == nil {
		t.Fatal("state missing from durable live cache")
	}
	rows, _, _, _, _ = projectLive(&restored, op, nil, now.Add(time.Hour))
	if len(rows) != 0 {
		t.Fatal("stopped state refreshed expiry")
	}
	v.ObservedAt, v.CollectedAt = now.Add(6*time.Minute), now.Add(6*time.Minute)
	v.CurrentStatus, v.StopName = nil, nil
	d, _ = nextLive(d, op, []api.Vehicle{v}, v.ObservedAt)
	if d.Vehicles[0].CurrentStatus != nil || d.Vehicles[0].SpeedKmh != nil {
		t.Fatal("resume inherited old state or bridged speed")
	}
}

func TestFreshStationaryPairAndRepeatedState(t *testing.T) {
	now := time.Now().UTC()
	op := api.Operator{Status: api.OperatorStatusOk}
	v := continuityVehicle("still", now)
	v.CurrentStatus = ptr(api.STOPPEDAT)
	d, _ := nextLive(nil, op, []api.Vehicle{v}, now)
	v.ObservedAt = now.Add(5 * time.Second)
	d, _ = nextLive(d, op, []api.Vehicle{v}, v.ObservedAt)
	if d.Vehicles[0].SpeedKmh == nil || *d.Vehicles[0].SpeedKmh != 0 {
		t.Fatal("valid stationary pair lost zero")
	}
	v.CurrentStatus = ptr(api.INTRANSITTO)
	d, _ = nextLive(d, op, []api.Vehicle{v}, now.Add(10*time.Second))
	if len(d.Samples) != 0 || *d.Vehicles[0].CurrentStatus != api.STOPPEDAT {
		t.Fatal("old observation was altered")
	}
}

func TestFleetPlateQueriesPreserveRawIdentity(t *testing.T) {
	v := api.FleetVehicle{SourceId: "bus-123", LicensePlate: ptr("CE29PV"), Model: ptr("Volvo")}
	for _, q := range []string{"ce29pv", "ce-29-pv", "ce 29 pv", "ce-29", "29-pv", "volvo", "bus-123"} {
		if !fleetMatches(v, q) {
			t.Fatalf("query %q failed", q)
		}
	}
	if fleetMatches(v, "---") || *v.LicensePlate != "CE29PV" {
		t.Fatal("query matched empty plate or changed identity")
	}
}
