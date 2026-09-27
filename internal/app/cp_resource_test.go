package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func resourceCPPredictions(t *testing.T, cache *Cache) []*CPData {
	t.Helper()
	state, _ := cache.state("")
	static := state.Static["cp"]
	if static == nil {
		t.Fatal("resource CP network missing")
	}
	versions := make([]*CPData, 64)
	now := time.Now().UTC()
	for version := range versions {
		d := &CPData{PlanID: static.PlanID, Availability: api.CPPredictionAvailability{Status: api.CPPredictionAvailabilityStatusOk, SourceUrl: cpSourceURL}, Rows: []api.CPPrediction{}}
		for n := 0; n < 110; n++ {
			name := strings.Repeat("N", 480) + fmt.Sprintf("%d-%d", version, n)
			r := api.CPPrediction{Id: fmt.Sprintf("%d-%d", version, n), OperatorId: "cp", PlanId: static.PlanID, SourceTripId: fmt.Sprintf("cp:trip%d", n), StopId: fmt.Sprintf("cp:stop%d", n), StopSequence: n, RouteId: "cp:route", StopName: name, RouteName: strings.Clone(name), DestinationName: strings.Clone(name), ExpectedAt: now.Add(time.Hour), SourceUpdatedAt: now, CollectedAt: now, ValidUntil: now.Add(sourceFreshness), SourceUrl: cpSourceURL}
			d.Rows = append(d.Rows, r)
		}
		for {
			blob, _ := json.Marshal(d)
			if len(blob) <= cpMaxBytes {
				break
			}
			d.Rows = d.Rows[:len(d.Rows)-1]
		}
		if err := finishCP(d); err != nil {
			t.Fatal(err)
		}
		versions[version] = d
	}
	if !cache.updateCP(static, versions[len(versions)-1]) {
		t.Fatal("resource CP publication")
	}
	t.Logf("cp_worst_case_distinct_snapshots=%d cp_rows_per_snapshot=%d", len(versions), len(versions[0].Rows))
	return versions
}

func concurrentCPDecode(t *testing.T) {
	t.Helper()
	entity := `{"trip_update":{"trip":{"trip_id":"[plan][other]trip"},"stop_time_update":[],"timestamp":1,"note":"` + strings.Repeat("x", 256) + `"}}`
	var body bytes.Buffer
	body.WriteString(`{"data":{"header":{"gtfs_realtime_version":"2.0","incrementality":"FULL_DATASET","timestamp":1790455682},"entity":[`)
	for body.Len()+len(entity)+8 < providerJSONBytes {
		if body.Bytes()[body.Len()-1] != '[' {
			body.WriteByte(',')
		}
		body.WriteString(entity)
	}
	body.WriteString(`]}}`)
	blob := body.Bytes()
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := decodeCPFeed(blob); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := decodeCPFeed(bytes.Repeat([]byte("x"), providerJSONBytes+1)); err == nil {
		t.Fatal("raw overflow admitted")
	}
	t.Logf("cp_concurrent_decode_bytes=%d workers=2", len(blob))
}

// Exercise distinct retained forecast revisions for every collected operator.
func resourceProviderPredictions(t *testing.T, cache *Cache) []*CPData {
	cp := resourceCPPredictions(t, cache)
	state, _ := cache.state("")
	retained := append([]*CPData(nil), cp...)
	for version := range cp {
		results := map[string]*CPData{"cp": cp[version]}
		for _, p := range providers {
			if p.ID == "cp" {
				continue
			}
			d := *cp[version]
			d.PlanID = state.Static[p.ID].PlanID
			d.Rows = make([]api.CPPrediction, len(cp[version].Rows))
			for n, row := range cp[version].Rows {
				row.OperatorId = p.ID
				row.StopName = strings.Clone(row.StopName)
				row.RouteName = strings.Clone(row.RouteName)
				row.DestinationName = strings.Clone(row.DestinationName)
				d.Rows[n] = row
			}
			results[p.ID] = &d
			retained = append(retained, &d)
		}
		cache.updateProviderPredictions(state.Static, results)
	}
	t.Logf("all_operator_forecast_snapshots=%d", len(retained))
	return retained
}
