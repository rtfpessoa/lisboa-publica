package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"lisboapublica/internal/api"
	"strings"
	"testing"
	"time"
)

func TestPublishedSpecificationMetadata(t *testing.T) {
	original := &StaticData{Models: map[string]Metadata{"[BNA17]7": {Propulsion: "8", TotalCapacity: ptr(80), SeatedCapacity: ptr(22), WheelchairAccessible: ptr(true)}}}
	rows := []publishedMetadata{{ID: "42-7", Agency: "BNA17", Propulsion: "electricity", Seats: ptr(0), Wheelchair: ptr(false), Contactless: ptr(false)}, {ID: "42-7", Agency: "wrong", Seats: ptr(99)}, {ID: "41-7", Agency: "BNA17", Seats: ptr(99)}}
	updated := mergePublishedMetadata(original, "cm", rows)
	m := updated.Models["[BNA17]7"]
	if m.Propulsion != "electricity" || m.SeatedCapacity == nil || *m.SeatedCapacity != 0 || m.TotalCapacity == nil || *m.TotalCapacity != 80 || m.WheelchairAccessible == nil || *m.WheelchairAccessible || m.Contactless == nil || *m.Contactless {
		t.Fatalf("published values lost: %+v", m)
	}
	if *original.Models["[BNA17]7"].SeatedCapacity != 22 {
		t.Fatal("mutated existing immutable metadata")
	}
	invalid := mergePublishedMetadata(updated, "cm", []publishedMetadata{{ID: "42-7", Agency: "BNA17", Propulsion: strings.Repeat("x", maxPropulsionBytes+1), Seats: ptr(-1)}})
	if *invalid.Models["[BNA17]7"].SeatedCapacity != 0 || invalid.Models["[BNA17]7"].Propulsion != "electricity" {
		t.Fatal("invalid/missing erased valid fallback")
	}
	var restored StaticData
	blob, err := encodeCache(updated)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err = json.NewDecoder(reader).Decode(&restored); err != nil {
		t.Fatal(err)
	}
	if *restored.Models["[BNA17]7"].WheelchairAccessible || *restored.Models["[BNA17]7"].SeatedCapacity != 0 {
		t.Fatal("cache roundtrip lost zero/false")
	}
}

func TestPublishedGTFSWheelchairAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  *bool
	}{{"", nil}, {"0", nil}, {"1", ptr(true)}, {"2", ptr(false)}, {"3", nil}} {
		got := metadataRow(map[string]string{"wheelchair_accessible": tc.value, "total_capacity": "0", "seated_capacity": "77", "contactless": "1"})
		if (got.WheelchairAccessible == nil) != (tc.want == nil) || tc.want != nil && *got.WheelchairAccessible != *tc.want {
			t.Fatalf("invalid enum mapping %s", tc.value)
		}
		if got.TotalCapacity == nil || *got.TotalCapacity != 0 || got.SeatedCapacity != nil || got.Contactless != nil {
			t.Fatal("unknown GTFS fields inferred")
		}
	}
	for _, value := range []string{"-1", "10001", "1.2", "bad", ""} {
		if capacityText(value) != nil {
			t.Fatalf("invalid capacity %q accepted", value)
		}
	}
}

func TestPublishedCMDirectSpecificationsTakePrecedence(t *testing.T) {
	now := time.Now().UTC()
	f := &Fetcher{publicationState: publicationState{Cache: NewCache()}}
	p, _ := providerByID("cm")
	var raw cmPosition
	if err := json.Unmarshal([]byte(`{"id":"7","capacity_seated":0,"capacity_total":-1,"wheelchair_accessible":false,"contactless":true}`), &raw); err != nil {
		t.Fatal(err)
	}
	vehicle := f.cmVehicle(p, raw, now, now)
	enrichVehicle(&vehicle, &StaticData{Models: map[string]Metadata{"7": {SeatedCapacity: ptr(42), TotalCapacity: ptr(80), WheelchairAccessible: ptr(true), Contactless: ptr(false)}}})
	if vehicle.SeatedCapacity == nil || *vehicle.SeatedCapacity != 0 || vehicle.TotalCapacity == nil || *vehicle.TotalCapacity != 80 || vehicle.WheelchairAccessible == nil || *vehicle.WheelchairAccessible || vehicle.Contactless == nil || !*vehicle.Contactless {
		t.Fatal("direct source precedence or fallback failed")
	}
	unknown := api.Vehicle{SourceId: "not-a-physical-match", PositionKind: api.VehiclePositionKindEstimated}
	enrichVehicle(&unknown, &StaticData{Models: map[string]Metadata{"7": {TotalCapacity: ptr(80)}}})
	if unknown.TotalCapacity != nil {
		t.Fatal("unverified identity enriched")
	}
}

func TestPublishedSpecificationsAreRecordedFromCollection(t *testing.T) {
	s := testStore(t)
	now := time.Now().UTC()
	v := api.Vehicle{Id: "cm:7", OperatorId: "cm", SourceId: "7", ObservedAt: now, CollectedAt: now, PositionKind: api.VehiclePositionKindReported, Lat: 38.72, Lon: -9.15, SeatedCapacity: ptr(0), TotalCapacity: ptr(80), WheelchairAccessible: ptr(false), Contactless: ptr(true)}
	putLive(t, s, NewCache(), "cm", []api.Vehicle{v})
	var raw []byte
	if err := s.DB.QueryRow(context.Background(), "SELECT payload FROM snapshots WHERE vehicle_id=$1", v.Id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var recorded api.Vehicle
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.SeatedCapacity == nil || *recorded.SeatedCapacity != 0 || recorded.WheelchairAccessible == nil || *recorded.WheelchairAccessible || recorded.Contactless == nil || !*recorded.Contactless {
		t.Fatal("published specifications not retained for offline history")
	}
	if specificationWriteBytes(v) != snapshotSpecificationBytes*storageWriteOverhead || specificationWriteBytes(api.Vehicle{}) != 0 {
		t.Fatal("specification storage reservation missing")
	}
}
