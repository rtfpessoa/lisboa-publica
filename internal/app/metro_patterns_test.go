package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestMetroStationMappingPunctuationAndAmbiguity(t *testing.T) {
	static := &StaticData{Stops: []api.Stop{{Id: "metro:gtfs", SourceId: "gtfs", Name: "Baixa / Chiado", Lat: 38.710, Lon: -9.139}}}
	published := []MetroStation{{ID: "BC", Name: "Baixa/Chiado", Lat: "38.710", Lon: "-9.139"}}
	stations := map[string]MetroStation{"BC": published[0]}
	if id := metroStationID(static, published, stations, "metro:gtfs"); id != "BC" {
		t.Fatalf("punctuation join failed: %s", id)
	}
	published = append(published, MetroStation{ID: "other", Name: "Baixa/Chiado", Lat: "38.710", Lon: "-9.139"})
	if id := metroStationID(static, published, stations, "metro:gtfs"); id != "gtfs" {
		t.Fatalf("ambiguous match guessed: %s", id)
	}
}

func TestMetroRawWaitUnknownFieldsSurvive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/estadoLinha/todos":
			json.NewEncoder(w).Encode(map[string]any{"codigo": 200, "resposta": map[string]string{"azul": "ok", "amarela": "ok", "verde": "ok", "vermelha": "ok", "azul_curta": "normal", "amarela_curta": "normal", "verde_curta": "normal", "vermelha_curta": "normal"}})
		case "/tempoEspera/Estacao/todos":
			w.Write([]byte(`{"codigo":200,"resposta":[{"stop_id":"BC","cais":"1","hora":"20260928100000","comboio":"fixture","tempoChegada1":100,"destino":"33","future_field":{"value":17}}]}`))
		case "/infoEstacao/todos":
			w.Write([]byte(`{"codigo":200,"resposta":[{"stop_id":"BC","stop_name":"Baixa/Chiado","stop_lat":"38.71","stop_lon":"-9.13"}]}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	now := time.Now().UTC()
	m := MetroClient{Client: upstream.Client(), Base: upstream.URL, metroRefreshState: metroRefreshState{tokenValue: "fixture", expires: now.Add(time.Hour)}}
	data := m.fetchData(context.Background(), nil, now)
	if data.Status.Status != "ok" || !strings.Contains(string(data.RawWaits), `"future_field"`) || len(data.Waits) != 1 {
		t.Fatalf("raw field lost: %+v", data)
	}
}

func TestMetroPatternsPublicReadAndUnknownStation(t *testing.T) {
	store := testStore(t)
	cache := NewCache()
	now := time.Now().UTC()
	data := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusOk, CheckedAt: &now}, Stations: []MetroStation{{ID: "BC", Name: "Baixa/Chiado"}}, Waits: []MetroWait{}}
	cache.updateMetro(data, cache.operator("metro"))
	server, err := NewServer(store, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	server.Patterns, err = patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Patterns.Close()
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	status, body := req(t, ts.Client(), "GET", ts.URL+"/api/v1/metro/patterns?stop_id=metro:BC", nil, nil)
	expectStatus(t, status, 200, body)
	view := decode[api.MetroPatterns](t, body)
	if !view.Experimental || view.PhysicalValidation || view.DwellSeconds != nil || view.SpeedKmh != nil || len(view.Forecasts) != 0 {
		t.Fatalf("false physical/empty semantics: %+v", view)
	}
	status, body = req(t, ts.Client(), "GET", ts.URL+"/api/v1/metro/patterns?stop_id=metro:unknown", nil, nil)
	expectStatus(t, status, 404, body)
	status, body = req(t, ts.Client(), "GET", ts.URL+"/api/v1/metro/patterns?episode=bad", nil, nil)
	expectStatus(t, status, 400, body)
}

func TestPublishedSegmentCompatibilitySurvivesUnrelatedPlanChanges(t *testing.T) {
	static := &StaticData{PlanID: "one", Stops: []api.Stop{{SourceId: "A", Lat: 38.7, Lon: -9.1}, {SourceId: "B", Lat: 38.701, Lon: -9.099}, {SourceId: "C", Lat: 38.702, Lon: -9.098}}, Shapes: []api.RouteShape{{ShapeId: "s", RouteId: "metro:r", Geometry: [][]float64{{-9.1, 38.7}, {-9.099, 38.701}, {-9.098, 38.702}}}}}
	trip := ScheduledTrip{Route: "r", Shape: "s"}
	visits := []StopTime{{Stop: "A", Sequence: 1}, {Stop: "B", Sequence: 2}, {Stop: "C", Sequence: 3}}
	before := plannedSegmentEvidence(static, trip, visits)
	if before[0] == "" || before[1] == "" {
		t.Fatal("unambiguous published geometry unavailable")
	}
	static.PlanID = "two"
	after := plannedSegmentEvidence(static, trip, visits)
	if before[0] != after[0] {
		t.Fatal("unrelated plan metadata invalidated unchanged segment")
	}
	static.Shapes[0].Geometry[2][0] -= .0001
	after = plannedSegmentEvidence(static, trip, visits)
	if before[0] != after[0] || before[1] == after[1] {
		t.Fatal("geometry change did not isolate affected segment")
	}
	static.Shapes = append(static.Shapes, api.RouteShape{ShapeId: "s", RouteId: "metro:r", Geometry: [][]float64{{-9.1, 38.7}, {-9.11, 38.701}}})
	if plannedSegmentEvidence(static, trip, visits)[0] != "" {
		t.Fatal("ambiguous published geometry certified")
	}
}

func TestProviderHistoryKeepsSourceProvenanceAndSkipsLastKnown(t *testing.T) {
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	if err = history.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	fetcher := &Fetcher{Patterns: history, publicationState: publicationState{Log: zap.NewNop()}}
	fetcher.recordProviderHistory("cm", []api.Vehicle{{Id: "cm:1", ObservedAt: at.Add(-time.Minute), PositionKind: api.VehiclePositionKindReported, Lat: 38.7, Lon: -9.1}, {Id: "cm:2", LastKnown: true}}, at, "")
	view, err := history.View(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Operators[1].Samples != 1 || !view.Operators[1].Forecasts {
		t.Fatal("captured positions claimed forecast support")
	}
}

func TestProviderHistoryQueueIsBoundedAndDisclosesDrops(t *testing.T) {
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	f := &Fetcher{Patterns: history, history: make(chan patterns.ProviderReceipt, 1), historyGaps: map[string]bool{}, publicationState: publicationState{Log: zap.NewNop()}}
	at := time.Now().UTC()
	f.recordProviderHistory("cm", nil, at, "")
	f.recordProviderHistory("cm", nil, at.Add(time.Second), "")
	if len(f.history) != 1 || !f.historyGaps["cm"] {
		t.Fatal("full queue neither bounded nor marked")
	}
	<-f.history
	f.recordProviderHistory("cm", nil, at.Add(2*time.Second), "")
	receipt := <-f.history
	if !receipt.Gap || f.historyGaps["cm"] || receipt.CollectedAt == nil || !receipt.CollectedAt.Equal(at.Add(2*time.Second)) {
		t.Fatal("later accepted receipt lost collection-gap provenance")
	}
}
