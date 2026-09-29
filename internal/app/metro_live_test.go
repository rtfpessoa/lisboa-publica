package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// Synthetic two-stop topology; no physical accuracy is claimed by these fixtures.
func metroLiveFixture(t *testing.T) (*Server, *StaticData, *MetroData, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	data := &MetroData{Status: api.MetroStatus{Status: "ok", CheckedAt: &now, Lines: []api.MetroLine{}, SourceUrl: metroBase}, Stations: []MetroStation{{ID: "RM", Name: "Roma", Lat: "38.75", Lon: "-9.14", Lines: "[Verde]"}, {ID: "CS", Name: "Cais do Sodré", Lat: "38.71", Lon: "-9.14", Lines: "[Verde]"}}}
	static := &StaticData{PlanID: "synthetic", Updated: now, Routes: []api.RouteDetail{{Id: "metro:r", SourceId: "r", ShortName: "Vd", LongName: "Linha Verde"}}, Stops: []api.Stop{{Id: "metro:gtfs-rm", SourceId: "rm", Name: "Roma", Lat: 38.75, Lon: -9.14}, {Id: "metro:gtfs-cs", SourceId: "cs", Name: "Cais do Sodré", Lat: 38.71, Lon: -9.14}}, Schedule: &Schedule{Trips: []ScheduledTrip{{ID: "one", Route: "r", Headsign: "Cais do Sodré", Times: []StopTime{{Stop: "rm", Sequence: 1, Arrival: 1, Departure: 1}, {Stop: "cs", Sequence: 2, Arrival: 121, Departure: 121}}}}}}
	cache := NewCache()
	cache.update("metro", static, nil, cache.operator("metro"))
	s, err := NewServer(nil, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	archive, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { archive.Close() })
	s.Patterns = archive
	return s, static, data, now
}
func metroTestRow(now time.Time, stop, train, value string) MetroWait {
	return MetroWait{Stop: stop, Platform: "1", At: now.In(lisbon).Format("20060102150405"), Train: train, Destination: "54", Wait1: json.RawMessage(value)}
}
func publishMetroTest(s *Server, d *StaticData, data *MetroData, now time.Time) {
	s.Cache.metroRuntime.observe(data, d, s.Patterns, now)
	// Most behavior fixtures use an already durable baseline. Dedicated tests
	// exercise uncommitted admission and commit failure without this helper.
	s.Cache.metroRuntime.flushCheckpoints(context.Background(), s.Patterns, now)
	s.Cache.metroRuntime.mu.Lock()
	data.Trains = s.Cache.metroRuntime.current(now)
	s.Cache.metroRuntime.mu.Unlock()
	s.Cache.updateMetro(data, s.Cache.operator("metro"))
}
func TestMetroStrictWaits(t *testing.T) {
	for _, value := range []string{"null", "", "\"0\"", "0.5", "-1", "7201", "{}"} {
		if _, ok := metroWaitSeconds(json.RawMessage(value)); ok {
			t.Fatalf("invalid wait admitted %q", value)
		}
	}
	for _, value := range []string{"0", "120", "7200"} {
		if _, ok := metroWaitSeconds(json.RawMessage(value)); !ok {
			t.Fatalf("numeric wait rejected %q", value)
		}
	}
}
func TestMetroOriginalTransitionsAndCoalescedHistory(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	s.Patterns = history
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(s, d, data, now)
	if len(data.Trains) != 1 || data.Trains[0].NextIndex == nil {
		t.Fatal("missing train association", data.Trains)
	}
	id := data.Trains[0].JourneyId
	next := *data
	next.Waits = []MetroWait{metroTestRow(now.Add(10*time.Second), "RM", "7", "0"), metroTestRow(now.Add(10*time.Second), "CS", "7", "100")}
	publishMetroTest(s, d, &next, now.Add(10*time.Second))
	train := next.Trains[0]
	if train.JourneyId != id || train.Calls[0].Arrival.Inferred == nil || train.Calls[0].Arrival.Actual != nil || train.Calls[0].Departure.Kind != "unavailable" {
		t.Fatal("incorrect evidence separation", train)
	}
	if got := train.Calls[0].Arrival.Inferred; !got.WindowStart.Equal(now) || !got.At.Equal(now.Add(10*time.Second)) {
		t.Fatal("receipt clock replaced original evidence", got)
	}
	// A repeated source clock, even with a later receipt, must not duplicate an event.
	repeated := next
	publishMetroTest(s, d, &repeated, now.Add(11*time.Second))
	if len(s.Cache.metroRuntime.pending) != 1 {
		t.Fatal("duplicate event", s.Cache.metroRuntime.pending)
	}
	// Later source update does not lose the event before any browser had a chance to read it.
	last := next
	last.Waits = []MetroWait{metroTestRow(now.Add(20*time.Second), "CS", "7", "80")}
	publishMetroTest(s, d, &last, now.Add(20*time.Second))
	if last.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("coalescing lost timeline")
	}
}
func TestMetroNullIdentityInventoryAndGap(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "null")}
	publishMetroTest(s, d, data, now)
	if len(data.Trains) != 1 || data.Trains[0].Association != "supported" || data.Trains[0].Calls[0].Arrival.Kind != "unavailable" {
		t.Fatal("missing ETA train excluded or phantom zero", data.Trains)
	}
	first := data.Trains[0].JourneyId
	next := *data
	next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "10")}
	publishMetroTest(s, d, &next, now.Add(time.Second))
	failed := next
	failed.Status.Status = "error"
	publishMetroTest(s, d, &failed, now.Add(2*time.Second))
	recovered := next
	recovered.Waits = []MetroWait{metroTestRow(now.Add(3*time.Second), "RM", "7", "0")}
	publishMetroTest(s, d, &recovered, now.Add(3*time.Second))
	if recovered.Trains[0].JourneyId != first || recovered.Trains[0].Calls[0].Arrival.Inferred != nil {
		t.Fatal("gap manufactured arrival", recovered.Trains)
	}
}
func TestMetroClockConflictSuspendsWithoutEmitting(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "100")}
	publishMetroTest(s, d, data, now)
	next := *data
	next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "0"), metroTestRow(now, "CS", "7", "95")}
	publishMetroTest(s, d, &next, now.Add(time.Second))
	if next.Trains[0].Association != "suspended" || len(s.Cache.metroRuntime.pending) != 0 {
		t.Fatal("partial incompatible update admitted", next.Trains)
	}
}
func TestMetroFrameConditionalAndScope(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest("GET", "/api/v1/metro/live?stop_id=metro:gtfs-rm", nil))
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var frame api.MetroLiveFrame
	if json.Unmarshal(first.Body.Bytes(), &frame) != nil || len(frame.Trains) != 1 || len(frame.Directions) != 1 || len(frame.Trains[0].Calls) != 1 {
		t.Fatal("bad scoped frame", first.Body.String())
	}
	etag := first.Header().Get("ETag")
	second := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/metro/live?stop_id=metro:gtfs-rm", nil)
	req.Header.Set("If-None-Match", etag)
	h.ServeHTTP(second, req)
	if second.Code != 304 || second.Body.Len() != 0 {
		t.Fatal("conditional request changed source", second.Code, second.Body.String())
	}
	oversized := s.Options
	s.Options.MetroStreamLimits.FrameBytes = 1
	third := httptest.NewRecorder()
	h.ServeHTTP(third, httptest.NewRequest("GET", "/api/v1/metro/live", nil))
	s.Options = oversized
	if third.Code != 413 {
		t.Fatal("oversized inventory silently truncated", third.Code)
	}
}
func TestMetroStreamResetAndCancellation(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live/stream", nil)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	scanner := bufio.NewScanner(res.Body)
	found := false
	for scanner.Scan() {
		if scanner.Text() == "event: reset" {
			found = true
		}
		if strings.HasPrefix(scanner.Text(), "data: ") {
			break
		}
	}
	if !found || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("missing complete reset")
	}
	cancel()
	res.Body.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.metroStreams.mu.Lock()
		n := s.metroStreams.total
		s.metroStreams.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stream admission leaked")
}

func TestMetroProgressRegressionAndETAOrderRejectBeforeEvents(t *testing.T) {
	for _, scenario := range []string{"backwards", "eta_order", "two_zeroes"} {
		t.Run(scenario, func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			data.Waits = []MetroWait{metroTestRow(now, "CS", "7", "100")}
			publishMetroTest(s, d, data, now)
			id := data.Trains[0].JourneyId
			next := *data
			switch scenario {
			case "backwards":
				next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "10"), metroTestRow(now.Add(time.Second), "CS", "7", "90")}
			case "eta_order":
				next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "120"), metroTestRow(now.Add(time.Second), "CS", "7", "90")}
			case "two_zeroes":
				next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "0"), metroTestRow(now.Add(time.Second), "CS", "7", "0")}
			}
			publishMetroTest(s, d, &next, now.Add(time.Second))
			if len(next.Trains) != 1 || next.Trains[0].JourneyId != id || next.Trains[0].Association != "suspended" || next.Trains[0].NextIndex != nil {
				t.Fatal("contradictory progress admitted", next.Trains)
			}
			for _, c := range next.Trains[0].Calls {
				if c.Arrival.Inferred != nil {
					t.Fatal("conflict manufactured event", c)
				}
			}
		})
	}
}

func TestMetroNoArchiveDoesNotPromiseDurability(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	s.Patterns = nil
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10")}
	publishMetroTest(s, d, data, now)
	next := *data
	next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "0")}
	publishMetroTest(s, d, &next, now.Add(time.Second))
	if len(next.Trains) != 1 || next.Trains[0].Persistence == nil || next.Trains[0].Persistence.State != "unavailable" || len(s.Cache.metroRuntime.pending) != 0 {
		t.Fatal("archive absence suppressed live estimates or promised durability", next.Trains)
	}
	frame, _, err := s.metroFrame(context.Background(), metroInterest{Stop: "metro:gtfs-rm"})
	if err != nil || len(frame.Trains) != 1 || frame.Trains[0].Persistence.State != "unavailable" {
		t.Fatal("archive absence suppressed live journey", frame, err)
	}
}

func TestMetroInventoryCapacityFailsExplicitly(t *testing.T) {
	s, _, _, _ := metroLiveFixture(t)
	s.Cache.updateMetro(&MetroData{InventoryOverflow: true}, s.Cache.operator("metro"))
	if _, _, err := s.metroFrame(context.Background(), metroInterest{}); err == nil {
		t.Fatal("incomplete inventory admitted")
	}
}

func TestMetroStreamAdmissionIsolationAndCleanup(t *testing.T) {
	s, _, _, _ := metroLiveFixture(t)
	s.Options.MetroStreamLimits = MetroStreamLimits{Process: 3, IP: 2, Principal: 1}
	ctx := func(ip, email string) context.Context {
		req := httptest.NewRequest("GET", "/api/v1/metro/live/stream", nil)
		req.RemoteAddr = ip + ":1234"
		c := context.WithValue(context.Background(), requestKey, req)
		if email != "" {
			c = context.WithValue(c, identityKey, &identity{Email: email, KeyID: "key"})
		}
		return c
	}
	first, err := s.admitMetroStream(ctx("192.0.2.1", "owner"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.admitMetroStream(ctx("192.0.2.2", "owner")); err == nil {
		t.Fatal("principal limit ignored")
	}
	second, err := s.admitMetroStream(ctx("192.0.2.1", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.admitMetroStream(ctx("192.0.2.1", "")); err == nil {
		t.Fatal("IP limit ignored")
	}
	third, err := s.admitMetroStream(ctx("192.0.2.2", ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.admitMetroStream(ctx("192.0.2.3", "")); err == nil {
		t.Fatal("process limit ignored")
	}
	first()
	second()
	third()
	if s.metroStreams.total != 0 || len(s.metroStreams.ips) != 0 || len(s.metroStreams.principals) != 0 {
		t.Fatal("admission leaked")
	}
	retry, err := s.admitMetroStream(ctx("192.0.2.1", "owner"))
	if err != nil {
		t.Fatal(err)
	}
	retry()
}

func TestMetroStreamClosesAtPrincipalExpiry(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := context.WithValue(req.Context(), requestKey, req)
		ctx = context.WithValue(ctx, identityKey, &identity{Email: "synthetic@example.test", Expires: time.Now().Add(100 * time.Millisecond)})
		response, err := s.StreamMetroLive(ctx, api.StreamMetroLiveRequestObject{})
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		if err = response.VisitStreamMetroLiveResponse(w); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	found := false
	for scanner.Scan() {
		if scanner.Text() == "event: reset" {
			found = true
		}
	}
	if scanner.Err() != nil || !found {
		t.Fatal("expiry did not close initialized stream", scanner.Err())
	}
	s.metroStreams.mu.Lock()
	count := s.metroStreams.total
	s.metroStreams.mu.Unlock()
	if count != 0 {
		t.Fatal("expired stream admission leaked")
	}
}

func TestMetroShortTurnCanonicalDirectionRequiresUniqueLongestPath(t *testing.T) {
	path := patterns.Pattern{Route: "line", Direction: "short", Stops: []string{"B", "C"}}
	topology := patterns.Topology{Patterns: []patterns.Pattern{path, {Route: "line", Direction: "equal", Stops: []string{"B", "C"}}, {Route: "line", Direction: "terminal", Stops: []string{"A", "B", "C", "D"}}}}
	if got := canonicalMetroDirection(topology, path); got != "terminal" {
		t.Fatal("short turn not placed in unique containing direction", got)
	}
	topology.Patterns = append(topology.Patterns, patterns.Pattern{Route: "line", Direction: "other", Stops: []string{"A", "B", "C", "E"}})
	if got := canonicalMetroDirection(topology, path); got != "short" {
		t.Fatal("ambiguous direction guessed", got)
	}
}

func TestMetroAuthenticatedStreamRevocation(t *testing.T) {
	store := testStore(t)
	s, d, data, now := metroLiveFixture(t)
	s.Store = store
	s.Options.PublicReads = false
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	token := "lp_synthetic_metro_stream_key"
	_, err := store.DB.Exec(context.Background(), "INSERT INTO api_keys(id,owner_email,name,token_hash,scopes,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)", "synthetic", "owner@example.test", "Synthetic stream test", tokenHash(token), []string{"read:transit"}, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("authenticated stream rejected", res.StatusCode)
	}
	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			break
		}
	}
	_, err = store.DB.Exec(context.Background(), "UPDATE api_keys SET revoked=TRUE WHERE id=$1", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	for scanner.Scan() {
	}
	if scanner.Err() != nil {
		t.Fatal("revoked stream remained open", scanner.Err())
	}
	s.metroStreams.mu.Lock()
	count := s.metroStreams.total
	s.metroStreams.mu.Unlock()
	if count != 0 {
		t.Fatal("revoked stream admission leaked")
	}
}

func TestMetroStreamCoalescesTransientReadContention(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/metro/live/stream", nil)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			break
		}
	}
	releaseOne, err := s.admitRead()
	if err != nil {
		t.Fatal(err)
	}
	releaseTwo, err := s.admitRead()
	if err != nil {
		releaseOne()
		t.Fatal(err)
	}
	next := *data
	next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "110")}
	publishMetroTest(s, d, &next, now.Add(time.Second))
	time.Sleep(750 * time.Millisecond)
	releaseOne()
	releaseTwo()
	for scanner.Scan() {
		if scanner.Text() == "event: unavailable" {
			t.Fatal("transient read contention closed healthy stream")
		}
		if scanner.Text() == "event: frame" {
			return
		}
	}
	t.Fatal("coalesced latest frame never arrived", scanner.Err())
}

func TestMetroConflictingOfficialForecastsRejectedAndNewClockRecovers(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	first := metroTestRow(now, "RM", "7", "120")
	second := metroTestRow(now, "RM", "7", "140")
	second.Platform = "2"
	data.Waits = []MetroWait{first, second}
	publishMetroTest(s, d, data, now)
	state, _ := s.Cache.state("")
	if got := predictedArrivals(state, now, now.Add(time.Hour), "", "metro:gtfs-rm"); len(got) != 0 {
		t.Fatal("contradictory original-clock forecasts arbitrarily selected", got)
	}
	// A strictly newer original publication supersedes the conflicting older instant.
	second.At = now.Add(time.Second).In(lisbon).Format("20060102150405")
	data.Waits = []MetroWait{first, second}
	publishMetroTest(s, d, data, now.Add(time.Second))
	state, _ = s.Cache.state("")
	got := predictedArrivals(state, now, now.Add(time.Hour), "", "metro:gtfs-rm")
	if len(got) != 1 || !got[0].ObservedAt.Equal(now.Add(time.Second)) {
		t.Fatal("new original publication failed to recover official forecast", got)
	}
}

func TestMetroLatestMissingOrPastWaitDoesNotReviveOlderForecast(t *testing.T) {
	for _, value := range []string{"null", "0"} {
		t.Run(value, func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			older := metroTestRow(now, "RM", "7", "120")
			latest := metroTestRow(now.Add(time.Second), "RM", "7", value)
			latest.Platform = "2"
			data.Waits = []MetroWait{older, latest}
			publishMetroTest(s, d, data, now.Add(2*time.Second))
			state, _ := s.Cache.state("")
			if got := predictedArrivals(state, now.Add(2*time.Second), now.Add(time.Hour), "", "metro:gtfs-rm"); len(got) != 0 {
				t.Fatal("obsolete forecast revived by window or absence filtering", got)
			}
		})
	}
}

// A synthetic socket that never drains exercises the production stream write deadline.
type metroBlockedWriter struct {
	net.Conn
	header http.Header
}

func (w *metroBlockedWriter) Header() http.Header { return w.header }
func (w *metroBlockedWriter) WriteHeader(int)     {}
func (w *metroBlockedWriter) Flush()              {}

func TestMetroSlowSocketDeadlineReleasesAdmissionWithoutBlockingReads(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	req := httptest.NewRequest("GET", "/api/v1/metro/live/stream", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	ctx := context.WithValue(context.Background(), requestKey, req)
	release, err := s.admitMetroStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writer, reader := net.Pipe()
	defer writer.Close()
	defer reader.Close()
	response := metroStreamResponse{server: s, ctx: ctx, first: []byte(`{"synthetic":true}`), revision: "blocked", release: release}
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		done <- response.VisitStreamMetroLiveResponse(&metroBlockedWriter{Conn: writer, header: make(http.Header)})
	}()
	if _, _, err := s.metroFrame(context.Background(), metroInterest{}); err != nil {
		t.Fatal("slow writer blocked independent snapshot", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(started) < 4*time.Second {
			t.Fatal("socket closed before bounded write deadline")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("slow socket escaped bounded stream deadline")
	}
	s.metroStreams.mu.Lock()
	total := s.metroStreams.total
	s.metroStreams.mu.Unlock()
	if total != 0 {
		t.Fatal("slow socket leaked stream admission", total)
	}
}

func TestMetroCollector500msSerializesSlowCyclesWithoutCatchup(t *testing.T) {
	s, _, data, _ := metroLiveFixture(t)
	starts := make(chan time.Time, 16)
	var requests, active, maximum atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/estadoLinha/todos" {
			fmt.Fprint(w, `{"codigo":"200","resposta":{"azul":"Ok","amarela":"Ok","verde":"Ok","vermelha":"Ok"}}`)
			return
		}
		current := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); current > old && !maximum.CompareAndSwap(old, current); old = maximum.Load() {
		}
		if req.URL.Path == "/estadoLinha/todos" {

			fmt.Fprint(w, `{"codigo":"200","resposta":{"azul":"Ok","amarela":"Ok","verde":"Ok","vermelha":"Ok"}}`)
			return
		}
		if req.URL.Path != "/tempoEspera/Estacao/todos" {
			t.Error("unexpected uncached request", req.URL.Path)
		}
		starts <- time.Now()
		if requests.Add(1) == 1 {
			time.Sleep(750 * time.Millisecond)
		}
		fmt.Fprint(w, `{"codigo":"200","resposta":[]}`)
	}))
	defer upstream.Close()
	client := NewMetroClient(upstream.Client(), &Store{}, s.Cache, "synthetic", "synthetic")
	client.Base = upstream.URL
	client.data = data
	client.metadataStations = data.Stations
	client.metadataStationsAt = time.Now()
	client.metadataDestinationsAt = time.Now()
	client.lastPersist = time.Now()
	client.tokenValue = "cached-synthetic-token"
	client.expires = time.Now().Add(time.Hour)
	client.Interval = time.Millisecond // The public floor must still enforce 500 ms.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); client.Run(ctx) }()
	var observed []time.Time
	for len(observed) < 3 {
		select {
		case at := <-starts:
			observed = append(observed, at)
		case <-ctx.Done():
			t.Fatal("collector failed to complete serial cycles")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("collector ignored cancellation")
	}
	if maximum.Load() != 1 {
		t.Fatal("overlapping provider calls", maximum.Load())
	}
	if observed[1].Sub(observed[0]) < 700*time.Millisecond {
		t.Fatal("slow cycle overlapped next cycle")
	}
	if observed[2].Sub(observed[1]) < 490*time.Millisecond {
		t.Fatal("missed tick caused a catch-up burst")
	}
}

func TestMetroArrivalUsesCompletedResponseReceiptNotCycleStart(t *testing.T) {
	s, _, template, _ := metroLiveFixture(t)
	var waits atomic.Int32
	zeroClocks := make(chan time.Time, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/estadoLinha/todos" {
			fmt.Fprint(w, `{"codigo":"200","resposta":{"azul":"Ok","amarela":"Ok","verde":"Ok","vermelha":"Ok"}}`)
			return
		}
		at, seconds := time.Now().Truncate(time.Second), "120"
		if waits.Add(1) == 2 {
			at = time.Now().Truncate(time.Second).Add(time.Second)
			zeroClocks <- at
			time.Sleep(time.Until(at.Add(25 * time.Millisecond)))
			seconds = "0"
		}
		json.NewEncoder(w).Encode(map[string]any{"codigo": "200", "resposta": []MetroWait{metroTestRow(at, "RM", "7", seconds)}})
	}))
	defer upstream.Close()
	client := NewMetroClient(upstream.Client(), &Store{}, s.Cache, "synthetic", "synthetic")
	client.Base = upstream.URL
	client.data = template
	client.lastPersist = time.Now()
	client.tokenValue = "cached-synthetic-token"
	client.expires = time.Now().Add(time.Hour)
	client.History = s.Patterns
	client.Refresh(context.Background())
	s.Cache.metroRuntime.flushCheckpoints(context.Background(), s.Patterns, time.Now())
	client.lastAttempt = time.Now().Add(-time.Second)
	latest := client.Refresh(context.Background())
	if len(latest.Trains) != 1 || latest.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("zero published during collection was rejected as future receipt", latest.Trains)
	}
	zeroAt := <-zeroClocks
	if latest.Status.CheckedAt == nil || latest.Status.CheckedAt.Before(zeroAt) || !client.lastAttempt.Before(zeroAt) {
		t.Fatal("cycle-start and completed-response receipt clocks were not kept distinct")
	}
}

func TestMetroCorrectionWithdrawsArrivalBeforeProgressSuspension(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	archive, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	s.Patterns = archive
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(s, d, data, now)
	arrived := *data
	arrived.Waits = []MetroWait{metroTestRow(now.Add(10*time.Second), "RM", "7", "0"), metroTestRow(now.Add(10*time.Second), "CS", "7", "100")}
	publishMetroTest(s, d, &arrived, now.Add(10*time.Second+100*time.Millisecond))
	if arrived.Trains[0].Calls[0].Arrival.Inferred == nil || arrived.Trains[0].NextIndex == nil || *arrived.Trains[0].NextIndex != 1 {
		t.Fatal("synthetic arrival did not advance estimated progress")
	}
	corrected := *data
	corrected.Waits = []MetroWait{metroTestRow(now.Add(11*time.Second), "RM", "7", "60"), metroTestRow(now.Add(11*time.Second), "CS", "7", "90")}
	publishMetroTest(s, d, &corrected, now.Add(11*time.Second+100*time.Millisecond))
	train := corrected.Trains[0]
	if train.Association != "suspended" || train.NextIndex != nil {
		t.Fatal("backwards estimate silently preserved live progress", train)
	}
	if train.Calls[0].Arrival.Inferred != nil {
		t.Fatal("contradicted inferred arrival survived progress suspension", train.Calls[0])
	}
	if arrived.Trains[0].Calls[0].Arrival.Inferred == nil {
		t.Fatal("correction mutated an earlier immutable publication")
	}
	if len(s.Cache.metroRuntime.pending) != 2 {
		t.Fatal("arrival and withdrawal proof revisions were not both retained", s.Cache.metroRuntime.pending)
	}
}
