package app

import (
	"lisboapublica/internal/api"
	"testing"
	"time"
)

func TestLegacyRailFerryCacheRefreshesBeforeTTL(t *testing.T) {
	cache := NewCache()
	for _, p := range providers {
		t.Run(p.ID, func(t *testing.T) {
			data := &StaticData{Updated: time.Now(), Schedule: &Schedule{Trips: []ScheduledTrip{{Shape: "official"}}}}
			op := cache.operator(p.ID)
			op.StaticStatus = api.OperatorStaticStatusOk
			op.StaticUpdatedAt = ptr(data.Updated)
			cache.update(p.ID, data, nil, op)
			state, _ := cache.state("")
			legacy := p.Mode == "train" || p.Mode == "ferry"
			if reusableStaticCache(p, state) == legacy {
				t.Fatal("legacy geometry skipped or unrelated cache reloaded")
			}
			if state.Static[p.ID] != data || state.Operators[p.ID].StaticStatus != api.OperatorStaticStatusOk {
				t.Fatal("eligibility mutated retained data")
			}
		})
	}
}

func TestGeometryAttemptRestoresNormalStaticTTL(t *testing.T) {
	for _, id := range []string{"cp", "fertagus", "ttsl"} {
		t.Run(id, func(t *testing.T) {
			p, _ := providerByID(id)
			for _, absent := range []bool{false, true} {
				archive := shapeArchive(t, false)
				if absent {
					archive = replaceGTFS(t, archive, map[string]string{"shapes.txt": ""})
				}
				data, err := readGTFS(archive, p, "plan", "20260101", "20261231", hubBase, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				prepareGTFSGeometry(data, nil, api.Operator{Id: id})
				cache := NewCache()
				cache.update(id, data, nil, staticHealth(cache.operator(id), data))
				state, _ := cache.state("")
				if !reusableStaticCache(p, state) {
					t.Fatal("attempted geometry bypassed normal TTL")
				}
				if absent && data.GeometryError == nil {
					t.Fatal("absence not recorded")
				}
			}
		})
	}
}
