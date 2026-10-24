package app

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

// Synthetic short service ends before Santa Apolónia while an anonymous wait
// is available there. Neither the inventory nor the wait is a board count.
func metroLiveCountFixture(t *testing.T) (*Server, *StaticData, *MetroData, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	data, static := metroPlatformFixture()
	static.Routes[0].ShortName, static.Routes[0].LongName = "Az", "Linha Azul"
	static.Schedule.Trips = append(static.Schedule.Trips, ScheduledTrip{ID: "short", Route: "4_0", Headsign: "São Sebastião", Times: []StopTime{{Stop: "a", Sequence: 1}, {Stop: "p1", Sequence: 2, Arrival: 60, Departure: 60}}})
	for i := range data.Stations {
		data.Stations[i].Lines = "[Azul]"
	}
	data.Destinations = []MetroDestination{{ID: "new", Name: "Santa Apolónia"}, {ID: "short", Name: "São Sebastião"}, {ID: "reverse", Name: "Reboleira"}}
	data.Status = api.MetroStatus{Status: "ok", CheckedAt: &now}
	data.Waits = []MetroWait{
		{Stop: "SS", Platform: "1", At: now.In(lisbon).Format("20060102150405"), Train: "7", Destination: "short", Wait1: json.RawMessage("120")},
		{Stop: "SP", Platform: "1", At: now.In(lisbon).Format("20060102150405"), Destination: "new", Wait1: json.RawMessage("140")},
	}
	cache := NewCache()
	cache.update("metro", static, nil, cache.operator("metro"))
	s, err := NewServer(nil, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	cache.metroRuntime.observe(data, static, nil, now)
	cache.updateMetro(data, cache.operator("metro"))
	return s, static, data, now
}

func assertMetroLiveNullCounts(t *testing.T, body []byte) api.MetroLiveFrame {
	t.Helper()
	var raw struct {
		Directions []map[string]json.RawMessage `json:"directions"`
	}
	var frame api.MetroLiveFrame
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &frame); err != nil {
		t.Fatal(err)
	}
	if len(raw.Directions) == 0 {
		t.Fatal("fixture did not publish station directions")
	}
	for _, direction := range raw.Directions {
		value, present := direction["count"]
		if !present || string(value) != "null" {
			t.Fatalf("live direction count must be explicit null: %s", body)
		}
	}
	return frame
}

func metroLiveCountHTTP(t *testing.T, handler http.Handler) api.MetroLiveFrame {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/metro/live?stop_id=metro:bx", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("live frame: %d %s", response.Code, response.Body.String())
	}
	return assertMetroLiveNullCounts(t, response.Body.Bytes())
}

func TestMetroLiveDirectionCountIsUncomputed(t *testing.T) {
	s, static, data, now := metroLiveCountFixture(t)
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	frame := metroLiveCountHTTP(t, handler)
	if len(frame.Trains) != 1 || frame.Trains[0].Association != "supported" || len(frame.UnassociatedForecasts) != 1 {
		t.Fatalf("fixture omitted its short turn or independent forecast: %+v", frame)
	}
	for _, call := range frame.Trains[0].Calls {
		if call.StopId == "metro:bx" {
			t.Fatal("short turn unexpectedly serves the selected station")
		}
	}
	assertMetroLiveNullCountStream(t, handler, frame.Revision)
	failed := *data
	failed.Status.Status = "error"
	s.Cache.metroRuntime.observe(&failed, static, nil, now.Add(time.Second))
	s.Cache.updateMetro(&failed, s.Cache.operator("metro"))
	if got := metroLiveCountHTTP(t, handler); got.Status.Status != "error" {
		t.Fatalf("source failure not represented: %+v", got.Status)
	}
}

func assertMetroLiveNullCountStream(t *testing.T, handler http.Handler, revision string) {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live/stream?stop_id=metro:bx", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			frame := assertMetroLiveNullCounts(t, []byte(strings.TrimPrefix(scanner.Text(), "data: ")))
			if frame.Revision != revision {
				t.Fatal("stream and snapshot disagree")
			}
			return
		}
	}
	t.Fatalf("stream did not publish a frame: %v", scanner.Err())
}
