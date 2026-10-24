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

	"lisboapublica/internal/api"
)

func readUnavailableTopologyStream(t *testing.T, scanner *bufio.Scanner, want string) api.MetroLiveFrame {
	t.Helper()
	event := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			var frame api.MetroLiveFrame
			if event != want || json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) != nil {
				t.Fatalf("invalid stream %s frame", want)
			}
			return frame
		}
	}
	t.Fatalf("stream did not publish %s: %v", want, scanner.Err())
	return api.MetroLiveFrame{}
}

func unavailableTopologyHTTP(t *testing.T, handler http.Handler) api.MetroLiveFrame {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/metro/live?stop_id=metro:gtfs-cs", nil))
	var frame api.MetroLiveFrame
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &frame) != nil {
		t.Fatal("public Metro frame unavailable", response.Code)
	}
	return frame
}

func assertCurrentLocalMetroForecast(t *testing.T, frame api.MetroLiveFrame, at time.Time) {
	t.Helper()
	if frame.Status.Status != "ok" || len(frame.Directions) != 0 || len(frame.UnassociatedForecasts) != 1 {
		t.Fatal("current official wait or unavailable topology not represented")
	}
	call := frame.UnassociatedForecasts[0]
	if call.JourneyId != nil || call.DirectionKey != nil || call.OwnPrediction != nil || call.OwnDeparturePrediction != nil || call.Arrival.Prediction == nil || call.Arrival.Prediction.SourceUpdatedAt == nil || !call.Arrival.Prediction.SourceUpdatedAt.Equal(at) || !call.Arrival.Prediction.At.Equal(at.Add(40*time.Second)) {
		t.Fatal("local forecast changed its source clock or claimed unsupported topology")
	}
	for _, train := range frame.Trains {
		if train.Association == "supported" {
			t.Fatal("unavailable topology retained a supported train association")
		}
	}
}

func TestMetroCurrentOfficialWaitsWithoutTopology(t *testing.T) {
	for _, missing := range []string{"static", "schedule"} {
		t.Run(missing, func(t *testing.T) { testMetroCurrentWaitsWithoutTopology(t, missing) })
	}
}

func testMetroCurrentWaitsWithoutTopology(t *testing.T, missing string) {
	t.Helper()
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(s, static, data, now)
	if len(data.Trains) != 1 {
		t.Fatal("synthetic baseline did not admit a train")
	}
	id, sequence := data.Trains[0].JourneyId, s.Cache.metroRuntime.sequence
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live/stream?stop_id=metro:gtfs-cs", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	initial := readUnavailableTopologyStream(t, scanner, "reset")
	currentAt := now.Add(time.Second)
	current := *data
	current.Waits = []MetroWait{metroTestRow(currentAt, "CS", "7", "40")}
	var plan *StaticData
	if missing == "schedule" {
		copy := *static
		copy.Schedule = nil
		plan = &copy
	}
	publishMetroTest(s, plan, &current, currentAt)
	s.Cache.metroRuntime.observeHub(nil, now.Add(2*time.Second))
	updated := readUnavailableTopologyStream(t, scanner, "frame")
	assertCurrentLocalMetroForecast(t, updated, currentAt)
	if updated.Revision == initial.Revision || s.Cache.metroRuntime.sequence != sequence {
		t.Fatal("current wait failed to revise the frame or invented a journey")
	}
	httpFrame := unavailableTopologyHTTP(t, handler)
	assertCurrentLocalMetroForecast(t, httpFrame, currentAt)
	if httpFrame.Revision != updated.Revision {
		t.Fatal("HTTP and stream disagree on the current official wait")
	}
	missingWait := *data
	missingWait.Waits = []MetroWait{metroTestRow(now.Add(2*time.Second), "CS", "7", "null")}
	publishMetroTest(s, plan, &missingWait, now.Add(2*time.Second))
	withoutWait := readUnavailableTopologyStream(t, scanner, "frame")
	if withoutWait.Revision == updated.Revision || len(withoutWait.UnassociatedForecasts) != 0 {
		t.Fatal("newer missing wait retained an older forecast")
	}

	recoveredAt := now.Add(3 * time.Second)
	recovered := *data
	recovered.Waits = []MetroWait{metroTestRow(recoveredAt, "CS", "7", "35")}
	publishMetroTest(s, static, &recovered, recoveredAt)
	final := readUnavailableTopologyStream(t, scanner, "frame")
	if final.Revision == withoutWait.Revision || len(final.Directions) != 1 || len(final.Trains) != 1 || final.Trains[0].JourneyId != id || final.Trains[0].Association != "supported" || !final.Trains[0].SourceUpdatedAt.Equal(recoveredAt) {
		t.Fatal("fresh supported topology did not recover coherently")
	}
}

func TestMetroUnavailableInitialTopologyDoesNotInventJourney(t *testing.T) {
	_, _, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "CS", "7", "40")}
	r := newMetroRuntime()
	r.observe(data, nil, nil, now)
	r.observeHub(nil, now.Add(time.Second))
	view, _, contexts, _ := r.view(now.Add(time.Second))
	if len(view.Trains) != 0 || len(contexts) != 0 || len(view.Topology.Patterns) != 0 {
		t.Fatal("missing all route catalogues invented a journey or route")
	}
}

func TestMetroUnavailableTopologyUsesCurrentDestinationCatalogue(t *testing.T) {
	tests := []struct {
		name         string
		originLines  string
		targetLines  string
		destinations []MetroDestination
		wantName     string
		wantLine     string
	}{
		{"renamed code", "[Verde]", "[Verde]", []MetroDestination{{ID: "54", Name: "Roma"}}, "Roma", "metro:r"},
		{"unknown code", "[Verde]", "[Verde]", nil, "Destino publicado: 54", "metro:r"},
		{"duplicate code", "[Verde]", "[Verde]", []MetroDestination{{ID: "54", Name: "Roma"}, {ID: "54", Name: "Cais do Sodré"}}, "Destino publicado: 54", "metro:r"},
		{"cross line", "[Verde, Azul]", "[Azul]", []MetroDestination{{ID: "54", Name: "Roma"}}, "Roma", "metro:blue"},
		{"conflicting lines", "[Verde]", "[Azul]", []MetroDestination{{ID: "54", Name: "Roma"}}, "Roma", ""},
		{"ambiguous line", "[Verde, Azul]", "[Azul]", nil, "Destino publicado: 54", ""},
		{"ambiguous destination and line", "[Verde, Azul]", "[Azul]", []MetroDestination{{ID: "54", Name: "Roma"}, {ID: "54", Name: "Cais do Sodré"}}, "Destino publicado: 54", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testMetroUnavailableDestination(t, tc.originLines, tc.targetLines, tc.destinations, tc.wantName, tc.wantLine)
		})
	}
}

func testMetroUnavailableDestination(t *testing.T, originLines, targetLines string, destinations []MetroDestination, wantName, wantLine string) {
	t.Helper()
	s, static, data, now := metroLiveFixture(t)
	static.Routes = append(static.Routes, api.RouteDetail{Id: "metro:blue", SourceId: "blue", ShortName: "Az", LongName: "Linha Azul"})
	publishMetroTest(s, static, data, now)
	current := *data
	current.Stations = append([]MetroStation(nil), data.Stations...)
	current.Stations[0].Lines, current.Stations[1].Lines = targetLines, originLines
	current.Destinations = destinations
	current.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "CS", "7", "40")}
	publishMetroTest(s, nil, &current, now.Add(time.Second))
	s.Cache.metroRuntime.observeHub(nil, now.Add(2*time.Second))
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	frame := unavailableTopologyHTTP(t, handler)
	assertCurrentLocalMetroForecast(t, frame, now.Add(time.Second))
	call := frame.UnassociatedForecasts[0]
	if call.Destination != wantName || call.LineKey != wantLine {
		t.Fatalf("destination/line = %q/%q, want %q/%q", call.Destination, call.LineKey, wantName, wantLine)
	}
}

func TestMetroUnavailableTopologyRejectsStaleOrFutureSourceClocks(t *testing.T) {
	for _, offset := range []time.Duration{-sourceFreshness - time.Second, providerClockSkew + 5*time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			s, static, data, now := metroLiveFixture(t)
			publishMetroTest(s, static, data, now)
			current := *data
			current.Waits = []MetroWait{metroTestRow(now.Add(offset), "CS", "7", "120")}
			publishMetroTest(s, nil, &current, now)
			s.Cache.metroRuntime.observeHub(nil, now.Add(time.Second))
			handler, err := s.Handler()
			if err != nil {
				t.Fatal(err)
			}
			frame := unavailableTopologyHTTP(t, handler)
			if len(frame.UnassociatedForecasts) != 0 || len(frame.Trains) != 0 {
				t.Fatal("stale or future publication was exposed", frame.UnassociatedForecasts)
			}
		})
	}
}

func TestMetroUnavailableTopologyDoesNotRenewSourceClock(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	publishMetroTest(s, static, data, now)
	current := *data
	current.Waits = []MetroWait{metroTestRow(now.Add(-sourceFreshness+time.Second), "CS", "7", "120")}
	publishMetroTest(s, nil, &current, now)
	_, _, contexts, _ := s.Cache.metroRuntime.view(now)
	if len(contexts) != 1 || len(contexts[0].Calls) != 1 || !contexts[0].Calls[0].Arrival.Prediction.SourceUpdatedAt.Equal(now.Add(-sourceFreshness+time.Second)) {
		t.Fatal("fresh source timestamp was not preserved")
	}
	publishMetroTest(s, nil, &current, now.Add(2*time.Second))
	s.Cache.metroRuntime.observeHub(nil, now.Add(3*time.Second))
	_, _, contexts, _ = s.Cache.metroRuntime.view(now.Add(3 * time.Second))
	if len(contexts) != 0 {
		t.Fatal("repeated old source timestamp renewed an expired forecast")
	}
}

func TestMetroUnavailableTopologyRejectsConflictingStaticRouteID(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	static.Routes = append(static.Routes, api.RouteDetail{Id: "metro:r", SourceId: "blue", ShortName: "Az", LongName: "Linha Azul"})
	publishMetroTest(s, static, data, now)
	current := *data
	current.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "CS", "7", "40")}
	publishMetroTest(s, nil, &current, now.Add(time.Second))
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	frame := unavailableTopologyHTTP(t, handler)
	assertCurrentLocalMetroForecast(t, frame, now.Add(time.Second))
	if frame.UnassociatedForecasts[0].LineKey != "" {
		t.Fatal("conflicting static route ID invented a line", frame.UnassociatedForecasts)
	}
}
