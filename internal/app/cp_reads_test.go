package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func cpReadFixture(t *testing.T) (*Cache, *StaticData, *CPData) {
	t.Helper()
	d, _ := cpTestData()
	cache := NewCache()
	cache.update("cp", d, nil, staticHealth(cache.operator("cp"), d))
	now := time.Now().UTC()
	data := &CPData{PlanID: d.PlanID, Availability: api.CPPredictionAvailability{Status: api.CPPredictionAvailabilityStatusOk, SourceUrl: cpSourceURL}}
	for n := 0; n < 3; n++ {
		data.Rows = append(data.Rows, api.CPPrediction{Id: stringID(uint64(n)), OperatorId: "cp", PlanId: d.PlanID, StopId: "cp:S", RouteId: "cp:1", SourceTripId: "cp:A_20251214", StopSequence: n, ExpectedAt: now.Add(time.Duration(n+1) * time.Minute), SourceUpdatedAt: now, ValidUntil: now.Add(90 * time.Second)})
	}
	cache.updateCP(d, data)
	return cache, d, data
}

func TestListCpPredictionsPagingFrozenAndExpiredAsAWhole(t *testing.T) {
	cache, _, data := cpReadFixture(t)
	_, h := securityServer(t, &Store{}, cache)
	first := securityRequest(h, "GET", "/api/v1/cp/predictions?limit=1", "", nil)
	if first.Code != 200 {
		t.Fatalf("first page %d %s", first.Code, first.Body.String())
	}
	var page api.CPPredictionPage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Page.Total != 3 || !page.Page.HasMore || page.Page.Revision == nil {
		t.Fatal("wrong page")
	}
	state, _ := cache.state("")
	cache.update("mobi", nil, nil, cache.operator("mobi"))
	path := "/api/v1/cp/predictions?limit=1&offset=1&revision=" + url.QueryEscape(*page.Page.Revision)
	second := securityRequest(h, "GET", path, "", nil)
	var next api.CPPredictionPage
	_ = json.Unmarshal(second.Body.Bytes(), &next)
	if second.Code != 200 || next.Page.Total != 3 || next.Data[0].Id == page.Data[0].Id {
		t.Fatal("pinned membership changed")
	}
	// Owned immutable fixture reconstruction, not mutation of in-flight collector rows.
	expired := *data
	expired.Rows = append([]api.CPPrediction(nil), data.Rows...)
	expired.Rows[0].ValidUntil = state.Created.Add(time.Nanosecond)
	cache.mu.Lock()
	copyState := *state
	copyState.CP = &expired
	cache.versions[state.Revision] = &copyState
	cache.mu.Unlock()
	if got := securityRequest(h, "GET", path, "", nil); got.Code != 410 {
		t.Fatalf("partial expired page instead of410: %d", got.Code)
	}
}

func TestListCpPredictionsReadValidationAndNoUpstreamFanout(t *testing.T) {
	cache, _, _ := cpReadFixture(t)
	_, h := securityServer(t, &Store{}, cache)
	for _, path := range []string{"?operators=mobi", "?trip_id=mobi:x", "?stop_id=cp:unknown", "?limit=501", "?from=invalid", "?from=2026-09-26T00:00:00Z&to=2026-09-26T03:00:00Z"} {
		got := securityRequest(h, "GET", "/api/v1/cp/predictions"+path, "", nil)
		if got.Code < 400 || got.Code >= 500 {
			t.Fatalf("invalid selector %s: %d", path, got.Code)
		}
	}
	request := httptest.NewRequest("GET", "https://example.test/api/v1/cp/predictions", nil)
	request.Header.Set("Authorization", "Bearer invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 401 {
		t.Fatal("invalid scoped key accepted")
	}
}

func TestCPCachePlanRaceFailureAndEmptyClear(t *testing.T) {
	cache, static, previous := cpReadFixture(t)
	newStatic := *static
	newStatic.PlanID = "new-plan"
	cache.update("cp", &newStatic, nil, cache.operator("cp"))
	if cache.updateCP(static, previous) {
		t.Fatal("old static race published")
	}
	state, _ := cache.state("")
	if state.CP != nil {
		t.Fatal("predictions survive plan change")
	}
	cache.update("cp", static, nil, cache.operator("cp"))
	cache.updateCP(static, previous)
	status := http.StatusServiceUnavailable
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	defer upstream.Close()
	f := NewFetcher(&Store{}, cache, zap.NewNop())
	f.Hub = upstream.URL
	opBefore := cache.operator("cp")
	f.refreshCP(context.Background())
	state, _ = cache.state("")
	if len(state.CP.Rows) != 3 || state.CP.Availability.Status != "error" || state.CP.Rows[0].SourceUpdatedAt != previous.Rows[0].SourceUpdatedAt {
		t.Fatal("failure renews/discards still-eligible original data")
	}
	if cache.operator("cp").Status != opBefore.Status {
		t.Fatal("prediction failure changes position health")
	}
	status = http.StatusNoContent
	f.refreshCP(context.Background())
	state, _ = cache.state("")
	if len(state.CP.Rows) != 0 || state.CP.Availability.Status != "ok" {
		t.Fatal("204 resurrects old arrivals")
	}
}

func TestCPReadsRequireTransitScope(t *testing.T) {
	store := testStore(t)
	cache, _, _ := cpReadFixture(t)
	_, h := securityServer(t, store, cache)
	for _, scope := range []string{"read:history", "read:transit"} {
		key, secret, err := newPersonalKey("CP fixture", []string{scope})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.insertPersonalKey(context.Background(), "fixture@example.test", key, secret); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "https://example.test/api/v1/cp/predictions", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 200
		if scope == "read:history" {
			want = 403
		}
		if w.Code != want {
			t.Fatalf("scope %s got%d", scope, w.Code)
		}
	}
}

func TestCPNamesAndTypedPlannedArrivalAssociations(t *testing.T) {
	for _, pair := range [][2]string{{"São João", "sao joao"}, {"Santa Apolonia", "Santa Apolónia"}, {"ALCÂNTARA", "alcantara"}} {
		if !nameSearch(pair[0], pair[1]) {
			t.Fatal("name search", pair)
		}
	}
	d, now := cpTestData()
	day := time.Date(2026, 9, 26, 12, 0, 0, 0, lisbon)
	q := scheduleQuery{ctx: context.Background(), data: d, operator: "cp", filter: Filter{Stop: "cp:S", From: now, To: now.Add(time.Hour)}}
	trip := q.trip(d.Schedule.Trips[0], day)
	rows, err := q.stopVisits(d.Schedule.Trips[0], trip, day)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	a := rows[0]
	if a.SourceTripId == nil || *a.SourceTripId != "cp:A_20251214" || a.TripId == *a.SourceTripId || a.PlanId == nil || *a.PlanId != "plan" || a.ServiceDate == nil || a.ServiceDate.Format("2006-01-02") != "2026-09-26" || a.StopSequence == nil || *a.StopSequence != 1 || a.RouteName == nil {
		t.Fatalf("typed bridge missing: %+v", a)
	}
}
