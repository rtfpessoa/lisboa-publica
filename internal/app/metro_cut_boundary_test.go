package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestMetroCanceledWorkerRetainsAdmittedQueueAndMarksGap(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "no later refresh", true: "later refresh"}[late], func(t *testing.T) {
			worker := newMetroCanceledWorker(t)
			worker.cancel()
			worker.waitStopped(t)
			cut := worker.assertGapAndOwnership(t)
			if late {
				worker.assertLateRefresh(t, cut)
			}
			worker.finish(t)
		})
	}
}

type metroCanceledWorker struct {
	server   *Server
	static   *StaticData
	data     *MetroData
	now      time.Time
	client   *MetroClient
	first    metroHistoryTask
	second   metroHistoryTask
	cancel   context.CancelFunc
	release  chan struct{}
	done     chan struct{}
	released bool
}

func newMetroCanceledWorker(t *testing.T) *metroCanceledWorker {
	t.Helper()
	s, static, data, now := metroLiveFixture(t)
	fifo, err := s.Patterns.AcquireMetroFIFO()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fifo.Close() })
	m := &MetroClient{Cache: s.Cache, History: s.Patterns}
	m.historyFIFO = fifo
	m.historyQueue = make(chan metroHistoryTask, 2)
	first := m.patternHistoryTask(data, static, now)
	second := m.patternHistoryTask(data, static, now.Add(time.Second))
	if !m.admitPatternHistoryTask(first) || !m.admitPatternHistoryTask(second) {
		t.Fatal("fixture could not admit two tasks")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	worker := &metroCanceledWorker{server: s, static: static, data: data, now: now, client: m, first: first, second: second, cancel: cancel, release: make(chan struct{}), done: make(chan struct{})}
	t.Cleanup(func() {
		if !worker.released {
			worker.cancel()
			close(worker.release)
			<-worker.done
		}
	})
	entered := make(chan struct{})
	go func() {
		m.historyLoopWithDelivery(ctx, func(task metroHistoryTask, generation uint64) uint64 {
			close(entered)
			<-worker.release
			m.finishPatternHistory(task, patterns.MetroRecordResult{}, nil, false)
			return generation
		})
		close(worker.done)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start its first task")
	}
	return worker
}

func (w *metroCanceledWorker) waitStopped(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.client.historyMu.Lock()
		stopped, bytes := w.client.historyStopped, w.client.historyBytes
		w.client.historyMu.Unlock()
		if stopped {
			if bytes != w.first.Bytes+w.second.Bytes || len(w.client.historyQueue) != 1 {
				t.Fatalf("in-flight cancellation lost accounting: bytes=%d queued=%d", bytes, len(w.client.historyQueue))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation did not mark loss while delivery was blocked")
		}
		runtime.Gosched()
	}
}

func (w *metroCanceledWorker) assertGapAndOwnership(t *testing.T) patterns.MetroCutSnapshot {
	t.Helper()
	cut := w.server.Patterns.MetroCut().Snapshot()
	r := w.server.Cache.metroRuntime
	r.mu.Lock()
	gap, capture, status := r.historyDeliveryGap, r.capture.Gap, r.historyStatus
	r.mu.Unlock()
	if !cut.Pending || !gap || !capture || status != "paused" {
		t.Fatalf("in-flight cancellation concealed gap: cut=%+v", cut)
	}
	if _, err := w.server.Patterns.RecordMetro(patterns.Receipt{ReceivedAt: w.now.Add(2 * time.Second)}, patterns.Topology{}); err == nil || w.server.Patterns.MetroCut().Snapshot() != cut {
		t.Fatal("retained FIFO did not reject direct history")
	}
	return cut
}

func (w *metroCanceledWorker) assertLateRefresh(t *testing.T, cut patterns.MetroCutSnapshot) {
	t.Helper()
	w.client.enqueuePatterns(w.data, w.static, w.now.Add(3*time.Second))
	next := w.server.Patterns.MetroCut().Snapshot()
	if w.client.historyBytes != w.first.Bytes+w.second.Bytes || len(w.client.historyQueue) != 1 || !next.Pending || next.Revision <= cut.Revision {
		t.Fatal("late refresh bypassed stopped queue")
	}
}

func (w *metroCanceledWorker) finish(t *testing.T) {
	t.Helper()
	close(w.release)
	w.released = true
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not retire in-flight task")
	}
	if w.client.historyBytes != w.second.Bytes || len(w.client.historyQueue) != 1 || !w.server.Patterns.MetroCut().Snapshot().Pending {
		t.Fatal("retirement lost queued evidence or acknowledged gap")
	}
}

func TestMetroStandaloneRefreshCompletesBeforeFIFOLeaseTransition(t *testing.T) {
	s, _, data, now := metroLiveFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/estadoLinha/todos":
			close(entered)
			<-release
			_, _ = w.Write([]byte(`{"codigo":200,"resposta":{"amarela":"ok","amarela_curta":"normal","azul":"ok","azul_curta":"normal","verde":"ok","verde_curta":"normal","vermelha":"ok","vermelha_curta":"normal"}}`))
		case "/tempoEspera/Estacao/todos":
			_, _ = fmt.Fprintf(w, `{"codigo":200,"resposta":[{"stop_id":"RM","cais":"1","hora":"%s","comboio":"7","tempoChegada1":120,"destino":"54"}]}`, now.In(lisbon).Format("20060102150405"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	m := &MetroClient{Client: upstream.Client(), Store: &Store{}, Cache: s.Cache, ClientID: "test", Secret: "test", Base: upstream.URL, History: s.Patterns}
	m.tokenValue, m.expires = "test-token", time.Now().Add(time.Hour)
	m.data, m.lastPersist = data, time.Now()
	refreshDone := make(chan *MetroData, 1)
	go func() { refreshDone <- m.Refresh(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("standalone refresh did not reach the provider")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handoffStarted, handoffDone := make(chan struct{}), make(chan struct{})
	go func() { close(handoffStarted); m.startHistoryDelivery(ctx); close(handoffDone) }()
	<-handoffStarted
	select {
	case <-handoffDone:
		close(release)
		t.Fatal("FIFO ownership overtook an in-flight standalone refresh")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case result := <-refreshDone:
		if result == nil || result.Status.Status != "ok" {
			t.Fatal("standalone refresh failed before handoff", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("standalone refresh did not complete")
	}
	select {
	case <-handoffDone:
	case <-time.After(5 * time.Second):
		t.Fatal("FIFO ownership did not start after standalone refresh")
	}
	if s.Patterns.MetroSampleStats().SampledAt.IsZero() {
		t.Fatal("standalone sample was not recorded before FIFO ownership")
	}
	cut := s.Patterns.MetroCut().Snapshot()
	if _, err := s.Patterns.RecordMetro(patterns.Receipt{ReceivedAt: now.Add(time.Minute)}, patterns.Topology{}); err == nil || s.Patterns.MetroCut().Snapshot() != cut {
		t.Fatal("direct record bypassed FIFO ownership or changed its cut")
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.historyMu.Lock()
		stopped := m.historyStopped
		m.historyMu.Unlock()
		if stopped {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("history worker did not stop")
}

func metroBoundaryFixture(t *testing.T) (*Server, string, time.Time) {
	t.Helper()
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	r.mu.Lock()
	track := r.tracks[data.Trains[0].JourneyId]
	expiry := now.Add(time.Minute)
	track.Train.Calls[0].OwnPrediction = &api.CallTimeEvidence{At: now.Add(20 * time.Second), ValidUntil: &expiry, ModelVersion: ptr("history")}
	r.forecastCut = s.Patterns.MetroCut().Snapshot()
	journey := track.Train.JourneyId
	r.mu.Unlock()
	return s, journey, now
}

func TestMetroProofLossBetweenEncodingAndClaim(t *testing.T) {
	for _, mode := range []string{"admitted", "unavailable", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			s, journey, now := metroBoundaryFixture(t)
			r := s.Cache.metroRuntime
			r.mu.Lock()
			defer r.mu.Unlock()
			track := r.tracks[journey]
			call := track.Train.Calls[0]
			call.Arrival.Inferred = &api.MetroEventEvidence{At: now, WindowStart: now, WindowEnd: now}
			track.Train.Calls[0] = call
			track.Proofs, track.ProofBytes = nil, 0
			r.archiveAvailable = mode != "unavailable"
			if mode == "capacity" {
				for n := 0; n < 1024; n++ {
					r.pending[fmt.Sprintf("occupied-%d", n)] = patterns.MetroEventRecord{}
				}
			}
			called := false
			claim := func(cut patterns.MetroCutSnapshot) bool {
				called = true
				if len(track.Proofs) != 0 {
					t.Fatal("proof was inserted before the generation claim")
				}
				s.Patterns.MetroCut().MarkPending()
				return r.claimMetroSnapshot(cut)
			}
			r.queueArrivalWithClaim(track, call, metroPoint{}, metroPoint{Clock: now}, claim)
			if !called || len(track.Proofs) != 1 {
				t.Fatal("proof candidate was not constructed and claimed")
			}
			for _, proof := range track.Proofs {
				if proof.ForecastCut != nil || proof.Train.Calls[0].OwnPrediction != nil || proof.Train.Calls[0].Arrival.Prediction == nil {
					t.Fatal("rebuilt proof lost official evidence or retained the old forecast")
				}
			}
			if (mode == "admitted") != (len(r.pending) == 1) {
				t.Fatalf("unexpected pending admission for %s: %d", mode, len(r.pending))
			}
		})
	}
}

func TestMetroHTTP304RebuildsWhenCutChangesBeforeReturn(t *testing.T) {
	s, journey, _ := metroBoundaryFixture(t)
	interest := metroInterest{Journey: journey}
	initial, _, err := s.metroFrame(context.Background(), interest)
	if err != nil {
		t.Fatal(err)
	}
	oldETag := `"` + initial.Revision + `"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := context.WithValue(req.Context(), requestKey, req)
		ctx = context.WithValue(ctx, writerKey, w)
		var result api.GetMetroLiveResponseObject
		var err error
		if req.URL.Query().Get("loss") == "1" {
			claim := func(cut patterns.MetroCutSnapshot, safe bool) bool {
				if !safe {
					s.Patterns.MetroCut().MarkPending()
				}
				return s.claimMetroPublication(cut, safe)
			}
			result, err = s.getMetroLiveWithClaim(ctx, claim)
		} else {
			result, err = s.GetMetroLive(ctx, api.GetMetroLiveRequestObject{})
		}
		if err != nil {
			t.Error(err)
			return
		}
		_ = result.VisitGetMetroLiveResponse(w)
	}))
	defer server.Close()
	for _, loss := range []bool{false, true} {
		address := server.URL + "?journey_id=" + url.QueryEscape(journey)
		if loss {
			address += "&loss=1"
		}
		request, _ := http.NewRequest(http.MethodGet, address, nil)
		request.Header.Set("If-None-Match", oldETag)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if !loss {
			response.Body.Close()
			if response.StatusCode != http.StatusNotModified {
				t.Fatalf("unchanged frame did not return 304: %d", response.StatusCode)
			}
			continue
		}
		var frame api.MetroLiveFrame
		err = json.NewDecoder(response.Body).Decode(&frame)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("ETag") == oldETag || len(frame.Trains) == 0 || frame.Trains[0].Calls[0].OwnPrediction != nil || frame.Trains[0].Calls[0].Arrival.Prediction == nil {
			t.Fatalf("loss returned an old 304 or stale frame: status=%d frame=%+v err=%v", response.StatusCode, frame, err)
		}
	}
}

func TestMetroFacadeClaimsAfterResponseConversion(t *testing.T) {
	s, _, _ := metroBoundaryFixture(t)
	stop := "metro:gtfs-rm"
	marked := false
	claim := func(cut patterns.MetroCutSnapshot, safe bool) bool {
		if !safe {
			marked = true
			s.Patterns.MetroCut().MarkPending()
		}
		return s.claimMetroPublication(cut, safe)
	}
	response, err := s.metroTransportPatternsWithClaim(context.Background(), api.GetTransportPatternsRequestObject{Params: api.GetTransportPatternsParams{OperatorId: "metro", StopId: &stop}}, claim)
	if err != nil {
		t.Fatal(err)
	}
	value := api.MetroPatterns(response.(api.GetTransportPatterns200JSONResponse))
	official := false
	for _, forecast := range value.Forecasts {
		official = official || forecast.OfficialAt != nil
		if forecast.OwnAt != nil {
			t.Fatal("facade published an old historical forecast")
		}
	}
	if !marked || value.Status != "paused" || !official {
		t.Fatalf("facade failed to fence converted output: %+v", value)
	}
}

func TestMetroSubsequentStreamFrameRebuildsAtCut(t *testing.T) {
	s, journey, now := metroBoundaryFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := context.WithValue(req.Context(), requestKey, req)
		response, err := s.StreamMetroLive(ctx, api.StreamMetroLiveRequestObject{})
		if err != nil {
			t.Error(err)
			return
		}
		r := response.(metroStreamResponse)
		defer r.release()
		stream := metroStreamWriter{writer: w, control: http.NewResponseController(w)}
		if !stream.send("reset", r.first) {
			return
		}
		runtime := s.Cache.metroRuntime
		runtime.mu.Lock()
		track := runtime.tracks[journey]
		track.Train.Calls[0].OwnPrediction.At = now.Add(25 * time.Second)
		runtime.forecastCacheUntil = time.Time{}
		runtime.mu.Unlock()
		claim := func(cut patterns.MetroCutSnapshot, safe bool) bool {
			if !safe {
				s.Patterns.MetroCut().MarkPending()
			}
			return s.claimMetroPublication(cut, safe)
		}
		revision := r.revision
		if !r.publishFrameWithClaim(&stream, &revision, claim) {
			t.Error("subsequent stream frame failed")
		}
	}))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "?journey_id=" + url.QueryEscape(journey))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	frames := []api.MetroLiveFrame{}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			var frame api.MetroLiveFrame
			if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &frame); err != nil {
				t.Fatal(err)
			}
			frames = append(frames, frame)
		}
	}
	if scanner.Err() != nil || len(frames) != 2 || frames[0].Trains[0].Calls[0].OwnPrediction == nil || frames[1].Trains[0].Calls[0].OwnPrediction != nil || frames[1].Trains[0].Calls[0].Arrival.Prediction == nil || frames[1].HistoryStatus != "paused" {
		t.Fatalf("stream did not rebuild the post-loss frame: %+v %v", frames, scanner.Err())
	}
}

func TestMetroEventOnlyRecoveryStreamHidesUnverifiedDeparture(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	expiry := now.Add(time.Minute)
	inferred := &api.MetroEventEvidence{At: now, WindowStart: now, WindowEnd: now, Mode: "model_departure"}
	actual := &api.CallTimeEvidence{At: now, SourceUrl: "https://example.invalid"}
	revisions := []api.MetroDepartureRevision{{Revision: 1, Status: "estimated", Evidence: inferred}}
	call := api.StopCall{Id: "visit", StopId: "metro:gtfs-rm", Arrival: api.CallTime{Kind: "inferred", Inferred: inferred}, Departure: api.CallTime{Kind: "inferred", Inferred: inferred, Actual: actual}, DepartureRevisions: &revisions}
	train := api.MetroTrain{JourneyId: "metro:run:event-only", SourceUpdatedAt: now, ValidUntil: expiry, Calls: []api.StopCall{call}}
	raw, err := json.Marshal(metroEventProof{Train: train, CallID: call.Id})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Patterns.RecordMetroEvents([]patterns.MetroEventRecord{{ID: "proof", Journey: train.JourneyId, At: now, Payload: raw}}, now); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := context.WithValue(req.Context(), requestKey, req)
		response, err := s.StreamMetroLive(ctx, api.StreamMetroLiveRequestObject{})
		if err != nil {
			t.Error(err)
			return
		}
		if err = response.VisitStreamMetroLiveResponse(w); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "?journey_id=" + url.QueryEscape(train.JourneyId))
	if err != nil {
		t.Fatal(err)
	}
	var frame api.MetroLiveFrame
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			err = json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &frame)
			break
		}
	}
	response.Body.Close()
	if err != nil || len(frame.Trains) != 1 || len(frame.Trains[0].Calls) != 1 || frame.Recovery == nil || frame.Recovery.Status != "partial" {
		t.Fatalf("event-only stream recovery missing: %+v %v", frame, err)
	}
	got := frame.Trains[0].Calls[0]
	if got.Arrival.Inferred == nil || got.Departure.Actual == nil || got.Departure.Inferred != nil || got.DepartureRevisions != nil {
		t.Fatalf("stream concealed independent evidence or exposed unverified departure: %+v", got)
	}
}

func TestMetroPatternsFinalClaimScrubsReconstructibleForecast(t *testing.T) {
	for _, endpoint := range []string{"metro", "transport"} {
		t.Run(endpoint, func(t *testing.T) {
			s, _, now := metroBoundaryFixture(t)
			own, official, source := now.Add(40*time.Second), now.Add(time.Minute), now
			bound, errorBound := now.Add(30*time.Second), 9.0
			forecast := api.MetroPatternForecast{OwnAt: &own, LowerAt: &bound, UpperAt: &own, OfficialAt: &official, SourceAt: &source,
				Components: []api.MetroPatternComponent{{OriginAt: now, Seconds: 40}}, CalibrationSamples: 5,
				ReferenceLower: &bound, ReferenceUpper: &own, ErrorLower: &errorBound, ErrorUpper: &errorBound, Selected: true, Evaluated: true, Result: "success"}
			candidate := api.MetroPatterns{Status: "collecting", Forecasts: []api.MetroPatternForecast{forecast}}
			claim := func(cut patterns.MetroCutSnapshot, safe bool) bool {
				if !safe {
					s.Patterns.MetroCut().MarkPending()
				}
				return s.claimMetroPublication(cut, safe)
			}
			var value api.MetroPatterns
			if endpoint == "metro" {
				response, err := s.getMetroPatternsWith(context.Background(), api.GetMetroPatternsRequestObject{}, func(context.Context, api.GetMetroPatternsRequestObject) (api.MetroPatterns, error) {
					return candidate, nil
				}, claim)
				if err != nil {
					t.Fatal(err)
				}
				value = api.MetroPatterns(response.(api.GetMetroPatterns200JSONResponse))
			} else {
				response, err := s.metroTransportPatternsWith(context.Background(), api.GetTransportPatternsRequestObject{Params: api.GetTransportPatternsParams{OperatorId: "metro"}}, func(context.Context, api.GetMetroPatternsRequestObject) (api.GetMetroPatternsResponseObject, error) {
					return api.GetMetroPatterns200JSONResponse(candidate), nil
				}, claim)
				if err != nil {
					t.Fatal(err)
				}
				value = api.MetroPatterns(response.(api.GetTransportPatterns200JSONResponse))
			}
			f := value.Forecasts[0]
			if value.Status != "paused" || f.OwnAt != nil || f.LowerAt != nil || f.UpperAt != nil || len(f.Components) != 0 || f.CalibrationSamples != 0 || f.ReferenceLower != nil || f.ReferenceUpper != nil || f.ErrorLower != nil || f.ErrorUpper != nil || f.Selected || f.Evaluated || f.Result != "lost_support" || f.Unavailable != "history_interrupted" || f.OfficialAt == nil || !f.OfficialAt.Equal(official) || f.SourceAt == nil || !f.SourceAt.Equal(source) {
				t.Fatalf("cut leaked own forecast or removed official evidence: %+v", f)
			}
		})
	}
}
