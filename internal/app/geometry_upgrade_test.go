package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"lisboapublica/internal/api"
	"testing"
	"time"
)

func TestLegacyStaticEnrichmentRefreshesBeforeTTL(t *testing.T) {
	cache := NewCache()
	for _, p := range providers {
		t.Run(p.ID, func(t *testing.T) {
			data := &StaticData{Updated: time.Now(), Schedule: &Schedule{Trips: []ScheduledTrip{{Shape: "official"}}}}
			op := cache.operator(p.ID)
			op.StaticStatus = api.OperatorStaticStatusOk
			op.StaticUpdatedAt = ptr(data.Updated)
			cache.update(p.ID, data, nil, op)
			state, _ := cache.state("")
			legacy := p.ID != "metro"
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

func TestCMPathAttemptRestoresNormalStaticTTL(t *testing.T) {
	p, _ := providerByID("cm")
	for _, test := range []struct {
		name    string
		network *StaticData
	}{
		{"verified", &StaticData{CMPaths: []CMPath{{ID: "cm:[plan][agency]pattern"}}}},
		{"no published path", &StaticData{}},
		{"rejected association", &StaticData{CMPathError: ptr("Ambiguous")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			d := &StaticData{Updated: now}
			mergeCMNetwork(d, test.network)
			blob, err := encodeCache(d)
			if err != nil {
				t.Fatal(err)
			}
			var restored StaticData
			reader, err := gzip.NewReader(bytes.NewReader(blob))
			if err != nil {
				t.Fatal(err)
			}
			err = json.NewDecoder(reader).Decode(&restored)
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
			cache := NewCache()
			cache.update("cm", &restored, nil, staticHealth(cache.operator("cm"), &restored))
			state, _ := cache.state("")
			if !reusableStaticCache(p, state) {
				t.Fatal("completed path attempt bypassed normal TTL after restart")
			}
		})
	}
}
