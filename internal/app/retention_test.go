package app

import (
	"context"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConfiguredHistoryRetention(t *testing.T) {
	t.Run("three days", func(t *testing.T) { checkHistoryRetention(t, 3) })
	t.Run("thirty days", func(t *testing.T) { checkHistoryRetention(t, 30) })
}

func checkHistoryRetention(t *testing.T, days int) {
	t.Helper()
	store := testStore(t)
	store.RetentionDays = days
	cache := NewCache()
	now := time.Now().UTC()
	for _, age := range []time.Duration{time.Duration(days+2) * 24 * time.Hour, time.Duration(days)*24*time.Hour + 30*time.Minute, time.Hour} {
		vehicle := api.Vehicle{Id: "carris:retention", OperatorId: "carris", SourceId: "retention", PositionKind: api.VehiclePositionKindReported, ObservedAt: now.Add(-age), CollectedAt: now, Lat: 38.72, Lon: -9.15}
		putLive(t, store, cache, "carris", []api.Vehicle{vehicle})
	}
	if err := store.prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB.QueryRow(context.Background(), "SELECT count(*) FROM snapshots").Scan(&count); err != nil || count != 2 {
		t.Fatalf("physical retention grace: count %d err %v", count, err)
	}
	server, err := NewServer(store, cache, Options{Origin: "https://example.com", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	config := httptest.NewRecorder()
	handler.ServeHTTP(config, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	if config.Code != http.StatusOK || decode[api.Config](t, config.Body.Bytes()).HistoryRetentionDays != days {
		t.Fatalf("retention config: %s", config.Body.String())
	}
	response := httptest.NewRecorder()
	endpoint := "/api/v1/history?from=" + now.Add(-time.Duration(days)*24*time.Hour-12*time.Hour).Format(time.RFC3339) + "&to=" + now.Format(time.RFC3339)
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint, nil))
	if response.Code != http.StatusGone {
		t.Fatalf("public retention: %d %s", response.Code, response.Body.String())
	}
}
