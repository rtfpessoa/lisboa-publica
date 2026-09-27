package app

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

// Saturate independent ETA retention while the unchanged eight-provider/churn/
// history/64-CP-snapshot workload remains alive. These bytes are not State history.
type arrivalResourceLoad struct {
	workspace [][]byte
	readers   [][]api.Arrival
}

func resourceArrivalRetention(t *testing.T, cache *Cache) arrivalResourceLoad {
	t.Helper()
	now := time.Now().UTC()
	state, _ := cache.state("")
	for _, operator := range []string{"carris", "cm"} {
		budget := arrivalTMLBudget
		if operator == "cm" {
			budget = arrivalCMBudget
		}
		used := 0
		for n := 0; n < arrivalDemandLimit; n++ {
			stop := fmt.Sprintf("%s:resource-%d", operator, n)
			static := state.Static[operator]
			cache.arrivals.request(stop, static, now)
			rows := []api.Arrival{}
			for row := 0; row < arrivalRowsLimit; row++ {
				r := api.Arrival{Id: fmt.Sprintf("%s-%d", stop, row), OperatorId: operator, StopId: stop, RouteId: operator + ":route", TripId: operator + ":trip", Headsign: strings.Repeat("H", 480) + fmt.Sprint(n, row), RouteName: ptr(strings.Repeat("R", 480) + fmt.Sprint(n, row)), ExpectedAt: ptr(now.Add(time.Hour)), ValidUntil: ptr(now.Add(time.Minute)), Kind: "prediction", SourceUrl: cpSourceURL}
				candidate := append(rows, r)
				if arrivalBytes(candidate) > (budget-used)/(arrivalDemandLimit-n) {
					break
				}
				rows = candidate
			}
			v := arrivalSnapshot{rows: rows, static: static, expires: now.Add(time.Minute), availability: api.ArrivalAvailability{Status: "ok", PlannedStatus: "ok", SourceUrl: cpSourceURL}}
			cache.arrivals.publish(stop, v)
			used += arrivalBytes(rows)
			if n < 16 {
				_, err := cache.arrivals.pin(Filter{Stop: stop, From: now, To: now.Add(time.Hour)}, v, now)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		if used < budget*9/10 {
			t.Fatalf("%s retention workload not saturated: %d/%d", operator, used, budget)
		}
		t.Logf("arrival_retention_%s_accounted_bytes=%d", operator, used)
	}
	readers := [][]api.Arrival{}
	for generation := 0; generation < 3; generation++ {
		rows := []api.Arrival{}
		for n := 0; n < arrivalRowsLimit; n++ {
			r := api.Arrival{Id: fmt.Sprintf("old-reader-%d-%d", generation, n), OperatorId: "carris", StopId: "carris:reader", RouteId: "carris:route", TripId: "carris:trip", Headsign: strings.Repeat("H", 480) + fmt.Sprint(generation, n), RouteName: ptr(strings.Repeat("R", 480) + fmt.Sprint(generation, n)), ExpectedAt: ptr(now.Add(time.Hour)), ValidUntil: ptr(now.Add(time.Minute)), Kind: "prediction", SourceUrl: cpSourceURL}
			if arrivalBytes(append(rows, r)) > arrivalResultBudget*95/100 {
				break
			}
			rows = append(rows, r)
		}
		v := arrivalSnapshot{rows: rows, static: state.Static["carris"], expires: now.Add(time.Minute)}
		_, err := cache.arrivals.pin(Filter{Stop: "carris:reader", From: now, To: now.Add(time.Hour)}, v, now)
		if err != nil {
			t.Fatal(err)
		}
		if generation < 2 {
			readers = append(readers, rows)
		}
	}
	t.Logf("arrival_evicted_inflight_readers=%d reader_accounted_bytes=%d", len(readers), arrivalBytes(readers[0])+arrivalBytes(readers[1]))
	// Simultaneous maximum CM response bodies and bounded mapping/build workspace.
	workspace := [][]byte{make([]byte, arrivalBodyLimit), make([]byte, arrivalBodyLimit), make([]byte, 2*1024*1024)}
	for _, b := range workspace {
		for n := range b {
			b[n] = byte(n)
		}
	}
	t.Logf("arrival_result_leases=%d additional_workspace_bytes=%d", len(cache.arrivals.results), len(workspace[0])+len(workspace[1])+len(workspace[2]))
	runtime.KeepAlive(workspace)
	return arrivalResourceLoad{workspace: workspace, readers: readers}
}
