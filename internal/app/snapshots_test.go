package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func historyVehicle(id string, at time.Time, speed float64) api.Vehicle {
	return api.Vehicle{Id: "carris:" + id, SourceId: id, OperatorId: "carris", ObservedAt: at, CollectedAt: at, PositionKind: api.VehiclePositionKindReported, Lat: 38.72, Lon: -9.15, RouteId: ptr("carris:1"), TripId: ptr("carris:T"), SpeedKmh: ptr(speed)}
}

func TestHistoryBucketsDuplicatesAndLateData(t *testing.T) {
	base := time.Now().Truncate(staticRefreshInterval)
	original := newHistoryCollector()
	vehicle := historyVehicle("X", base.Add(10*time.Second), 20)
	next, records := original.propose("carris", &LiveData{Collected: base.Add(15 * time.Second), Vehicles: []api.Vehicle{vehicle}}, map[string]*float64{vehicle.Id: ptr(.1)}, staticRefreshInterval)
	if len(original.Pending) != 0 || len(next.Pending) != 1 || len(records) != 0 {
		t.Fatal("proposal mutated original or closed too early")
	}
	duplicate, _ := next.propose("carris", &LiveData{Collected: base.Add(20 * time.Second), Vehicles: []api.Vehicle{vehicle}}, map[string]*float64{vehicle.Id: ptr(.1)}, staticRefreshInterval)
	vehicle.ObservedAt, vehicle.SpeedKmh = base.Add(40*time.Second), ptr(40.0)
	next, _ = duplicate.propose("carris", &LiveData{Collected: base.Add(45 * time.Second), Vehicles: []api.Vehicle{vehicle}}, map[string]*float64{vehicle.Id: ptr(.2)}, staticRefreshInterval)
	vehicle.ObservedAt, vehicle.TripId = base.Add(60*time.Second), ptr("carris:T2")
	next, _ = next.propose("carris", &LiveData{Collected: base.Add(65 * time.Second), Vehicles: []api.Vehicle{vehicle}}, nil, staticRefreshInterval)
	next, records = next.propose("carris", &LiveData{Collected: base.Add(staticRefreshInterval + sourceFreshness)}, nil, staticRefreshInterval)
	if len(records) != 2 || len(next.Pending) != 0 {
		t.Fatal("trip split or closure failed")
	}
	for _, record := range records {
		if *record.Vehicle.TripId == "carris:T" && (record.SpeedSamples != 2 || *record.Vehicle.SpeedKmh != 30 || math.Abs(*record.Distance-.3) > 1e-9 || !record.First.Equal(base.Add(10*time.Second))) {
			t.Fatalf("bucket statistics: %+v", record)
		}
	}
	vehicle.ObservedAt = base.Add(70 * time.Second)
	next, records = next.propose("carris", &LiveData{Collected: base.Add(staticRefreshInterval + sourceFreshness + time.Second), Vehicles: []api.Vehicle{vehicle}}, nil, staticRefreshInterval)
	if len(next.Pending) != 0 || len(records) != 0 {
		t.Fatal("late data reopened a finalized bucket")
	}
}

func TestHistoryPendingBound(t *testing.T) {
	collector := newHistoryCollector()
	at := time.Now().Truncate(staticRefreshInterval)
	for index := range maxPendingHistory + 1 {
		collector.observe(historyVehicle(fmt.Sprint(index), at, 20), nil, at, staticRefreshInterval)
	}
	if len(collector.Pending) > maxPendingHistory || len(collector.Observed) > maxPendingHistory {
		t.Fatal("pending history unbounded")
	}
}

func TestHistoryCommitRollbackAndRevision(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, staticRefreshInterval, false); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-15 * time.Minute).Truncate(staticRefreshInterval)
	first := historyVehicle("X", base.Add(10*time.Second), 20)
	other := historyVehicle("Y", base.Add(15*time.Second), 100)
	saveHistory(t, store, base.Add(20*time.Second), []api.Vehicle{first, other}, map[string]*float64{first.Id: ptr(.1), other.Id: ptr(.3)})
	first.ObservedAt, first.SpeedKmh = base.Add(40*time.Second), ptr(40.0)
	saveHistory(t, store, base.Add(45*time.Second), []api.Vehicle{first}, map[string]*float64{first.Id: ptr(.2)})
	if _, err := store.DB.Exec(context.Background(), "DROP TABLE source_health"); err != nil {
		t.Fatal(err)
	}
	failed := &LiveData{Collected: base.Add(staticRefreshInterval + sourceFreshness)}
	if store.Save(context.Background(), "carris", nil, failed, NewCache().operator("carris"), nil) == nil {
		t.Fatal("transaction failure not exercised")
	}
	checkSnapshotCount(t, store, 0)
	if len(store.collector.Pending) != 2 {
		t.Fatal("failed transaction consumed pending state")
	}
	if _, err := store.DB.Exec(context.Background(), "CREATE TABLE source_health(operator_id TEXT PRIMARY KEY,payload JSONB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	saveHistory(t, store, failed.Collected, nil, nil)
	checkSnapshotCount(t, store, 2)
	checkHistoryAPI(t, store, base, failed.Collected)
}

func saveHistory(t *testing.T, store *Store, at time.Time, vehicles []api.Vehicle, distances map[string]*float64) {
	t.Helper()
	if err := store.Save(context.Background(), "carris", nil, &LiveData{Collected: at, Vehicles: vehicles}, NewCache().operator("carris"), distances); err != nil {
		t.Fatal(err)
	}
}

func checkSnapshotCount(t *testing.T, store *Store, want int) {
	t.Helper()
	var count int
	if err := store.DB.QueryRow(context.Background(), "SELECT count(*) FROM snapshots").Scan(&count); err != nil || count != want {
		t.Fatalf("snapshots: got%d want%d %v", count, want, err)
	}
}

func checkHistoryAPI(t *testing.T, store *Store, from, to time.Time) {
	t.Helper()
	server, err := NewServer(store, NewCache(), Options{Origin: "https://example.com", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	browser := httptest.NewServer(handler)
	defer browser.Close()
	query := url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}, "limit": {"1"}}
	status, body := req(t, browser.Client(), http.MethodGet, browser.URL+"/api/v1/metrics?"+query.Encode(), nil, nil)
	expectStatus(t, status, http.StatusOK, body)
	metrics := decode[api.Metrics](t, body)
	if metrics.SpeedKmh == nil || math.Abs(*metrics.SpeedKmh-160.0/3) > 1e-9 || math.Abs(*metrics.DistanceKm-.6) > 1e-9 {
		t.Fatalf("weighted metrics: %+v", metrics)
	}
	status, body = req(t, browser.Client(), http.MethodGet, browser.URL+"/api/v1/fleet?"+query.Encode(), nil, nil)
	expectStatus(t, status, http.StatusOK, body)
	fleet := decode[api.FleetVehiclePage](t, body)
	if !fleet.Data[0].FirstSeen.Equal(from.Add(10 * time.Second)) {
		t.Fatal("first observation lost")
	}
	vehicle := historyVehicle("A", from.Add(staticRefreshInterval+10*time.Second), 60)
	saveHistory(t, store, vehicle.ObservedAt, []api.Vehicle{vehicle}, nil)
	saveHistory(t, store, from.Add(2*staticRefreshInterval+sourceFreshness), nil, nil)
	query.Set("revision", *fleet.Page.Revision)
	query.Set("offset", "1")
	status, body = req(t, browser.Client(), http.MethodGet, browser.URL+"/api/v1/fleet?"+query.Encode(), nil, nil)
	expectStatus(t, status, http.StatusOK, body)
	next := decode[api.FleetVehiclePage](t, body)
	if next.Page.Total != 2 || next.Data[0].Id != "carris:Y" {
		t.Fatalf("later finalized bucket shifted revision: %+v", next)
	}
}

func TestPausedHistoryKeepsLiveCacheAndBoundsPending(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, staticRefreshInterval, true); err != nil {
		t.Fatal(err)
	}
	store.budget.measure = func(context.Context) (int64, error) { return historyDatabaseBytes, nil }
	base := time.Now().Truncate(staticRefreshInterval)
	vehicle := historyVehicle("X", base.Add(10*time.Second), 20)
	saveHistory(t, store, vehicle.ObservedAt, []api.Vehicle{vehicle}, nil)
	saveHistory(t, store, base.Add(staticRefreshInterval+sourceFreshness), nil, nil)
	checkSnapshotCount(t, store, 0)
	if len(store.collector.Pending) != 0 || store.historyStatus() != "paused" {
		t.Fatal("paused history retained a backlog")
	}
	var parts int
	if err := store.DB.QueryRow(context.Background(), "SELECT count(*) FROM cache_parts WHERE kind='live'").Scan(&parts); err != nil || parts == 0 {
		t.Fatalf("live cache unavailable under history limit: %v", err)
	}
}
