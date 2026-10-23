package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

type metroCutDeadlineContext struct {
	context.Context
	once sync.Once
	cut  *patterns.MetroCut
}

func (c *metroCutDeadlineContext) Deadline() (time.Time, bool) {
	c.once.Do(func() { c.cut.MarkPending() })
	return c.Context.Deadline()
}

func TestMetroPatternEndpointsFenceLossDuringRead(t *testing.T) {
	for _, facade := range []bool{false, true} {
		t.Run(map[bool]string{false: "metro", true: "transport facade"}[facade], func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
			publishMetroTest(s, d, data, now)
			ctx := &metroCutDeadlineContext{Context: context.Background(), cut: s.Patterns.MetroCut()}
			stop := "metro:gtfs-rm"
			var value api.MetroPatterns
			if facade {
				response, err := s.GetTransportPatterns(ctx, api.GetTransportPatternsRequestObject{Params: api.GetTransportPatternsParams{OperatorId: "metro", StopId: &stop}})
				if err != nil {
					t.Fatal(err)
				}
				value = api.MetroPatterns(response.(api.GetTransportPatterns200JSONResponse))
			} else {
				response, err := s.GetMetroPatterns(ctx, api.GetMetroPatternsRequestObject{Params: api.GetMetroPatternsParams{StopId: &stop}})
				if err != nil {
					t.Fatal(err)
				}
				value = api.MetroPatterns(response.(api.GetMetroPatterns200JSONResponse))
			}
			official := false
			for _, forecast := range value.Forecasts {
				official = official || forecast.OfficialAt != nil
				if forecast.OwnAt != nil {
					t.Fatal("lost historical forecast appeared in pattern response")
				}
			}
			if value.Status != "paused" || !official || !s.Patterns.MetroCut().Snapshot().Pending {
				t.Fatalf("pattern response lost its cut or independent official value: %+v", value)
			}
		})
	}
}

func TestMetroRestoredCorrectionNeverReplacesOriginalCheckpoint(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	dir := t.TempDir()
	archive, err := patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	s.Patterns = archive
	expires := now.Add(time.Minute)
	event := &api.MetroEventEvidence{At: now, WindowStart: now, WindowEnd: now, Mode: "model_departure"}
	calls := []api.StopCall{{Id: "one", StopSequence: 0, Departure: api.CallTime{Kind: "inferred", Inferred: event}}, {Id: "two", StopSequence: 1, Departure: api.CallTime{Kind: "inferred", Inferred: event}}}
	train := api.MetroTrain{JourneyId: "metro:run:original", RouteId: "metro:r", Reference: "7", SourceUpdatedAt: now, ValidUntil: expires, Calls: calls}
	clock := now.UnixMilli()
	payload := metroCheckpointPayload{Version: 1, Train: train, Points: map[string]metroPoint{"first": {Clock: now}}, Proofs: map[string]metroEventProof{"original-proof": {Train: train, CallID: "one"}}, ModelDepartureSupport: map[string]map[int64]metroModelPublication{"one": {clock: {Lat: 1}}, "two": {clock: {Lat: 1}}}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["unknown_future_evidence"] = json.RawMessage(`{"retained":true}`)
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	_, err = archive.CommitMetroJourneys(context.Background(), []patterns.MetroJourneyCheckpoint{{Journey: train.JourneyId, Revision: 1, SourceAt: now, Payload: raw}}, now)
	if err != nil {
		t.Fatal(err)
	}
	frame, _, err := s.metroFrame(context.Background(), metroInterest{Journey: train.JourneyId})
	if err != nil || len(frame.Trains) != 1 || frame.Trains[0].Calls[0].Departure.Inferred != nil || frame.Trains[0].Calls[1].Departure.Inferred != nil {
		t.Fatalf("restored model departures were not fenced: %+v %v", frame, err)
	}
	r := s.Cache.metroRuntime
	r.archiveAvailable = true
	if r.correctHubModel("metro:r|7", hubPosition{At: clock, Lat: 2}) {
		t.Fatal("restored track accepted an unsupported correction")
	}
	r.queueCurrentCheckpoints()
	r.flushCheckpoints(context.Background(), archive, now.Add(time.Second))
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err = patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	restored, status, err := archive.MetroJourneyCheckpoint(context.Background(), train.JourneyId, now.Add(2*time.Second))
	if err != nil || status != "restored" || restored.Revision != 1 || !bytes.Equal(restored.Payload, raw) {
		t.Fatalf("restore/correction/reopen changed original evidence: %s %v", status, err)
	}
}

func TestMetroCheckpointRetryKeepsExactBatchUntilCommitOrExpiry(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "expiry"}[expire], func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			dir := t.TempDir()
			archive, err := patterns.Open(patterns.DefaultConfig(dir))
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			s.Patterns = archive
			data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
			publishMetroTest(s, d, data, now)
			changed := *data
			changed.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "110")}
			r := s.Cache.metroRuntime
			r.observe(&changed, d, archive, now.Add(time.Second))
			blocker := filepath.Join(dir, ".manifest.tmp")
			if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
				t.Fatal(err)
			}
			r.flushCheckpoints(context.Background(), archive, now.Add(time.Second))
			if r.checkpointRetry == nil || len(r.checkpointPending) == 0 || r.checkpointFenced {
				t.Fatal("proven pre-rename failure did not retain an exact retry")
			}
			pending := r.checkpointPending[0]
			bytesBefore := r.dirtyBytes
			track := r.tracks[pending.Journey]
			track.Train.Reason = "a later uncommitted change"
			r.queueCurrentCheckpoints()
			if r.dirtyBytes != bytesBefore || r.dirty[pending.Journey].Revision != pending.Revision {
				t.Fatal("in-flight checkpoint was replaced before acknowledgement")
			}
			if err := os.Remove(blocker); err != nil {
				t.Fatal(err)
			}
			at := now.Add(2 * time.Second)
			if expire {
				at = now.AddDate(0, 0, 8)
			}
			r.flushCheckpoints(context.Background(), archive, at)
			if r.checkpointRetry != nil || r.checkpointPending != nil || r.checkpointFenced {
				t.Fatal("finished retry retained its ticket or batch")
			}
			if expire {
				if _, ok := r.dirty[pending.Journey]; ok || !r.capture.Gap || !track.CheckpointUnavailable {
					t.Fatal("expired work was not honestly abandoned")
				}
			} else if track.CommittedRevision != pending.Revision {
				t.Fatal("exact pending revision was not acknowledged")
			}
		})
	}
}

func TestMetroInitialStreamRebuildsAfterBufferedFrameLosesCut(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	r.mu.Lock()
	track := r.tracks[data.Trains[0].JourneyId]
	expires := now.Add(time.Minute)
	track.Train.Calls[0].OwnPrediction = &api.CallTimeEvidence{At: now.Add(20 * time.Second), ValidUntil: &expires, ModelVersion: ptr("history")}
	r.forecastCut = s.Patterns.MetroCut().Snapshot()
	journey := track.Train.JourneyId
	r.mu.Unlock()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := context.WithValue(req.Context(), requestKey, req)
		response, err := s.StreamMetroLive(ctx, api.StreamMetroLiveRequestObject{})
		if err != nil {
			t.Error(err)
			return
		}
		s.Patterns.MetroCut().MarkPending()
		if err = response.VisitStreamMetroLiveResponse(w); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Get(server.URL + "?journey_id=" + url.QueryEscape(journey))
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(response.Body)
	var frame api.MetroLiveFrame
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			err = json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &frame)
			break
		}
	}
	response.Body.Close()
	if err != nil || len(frame.Trains) == 0 || len(frame.Trains[0].Calls) == 0 || frame.Trains[0].Calls[0].OwnPrediction != nil || frame.Trains[0].Calls[0].Arrival.Prediction == nil || frame.HistoryStatus != "paused" {
		t.Fatalf("buffered reset retained a lost historical prediction: %+v %v", frame, err)
	}
}

func TestMetroPublicationRejectsCandidateBuiltBeforeLoss(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	r.mu.Lock()
	track := r.tracks[data.Trains[0].JourneyId]
	expires := now.Add(time.Minute)
	track.Train.Calls[0].OwnPrediction = &api.CallTimeEvidence{At: now.Add(20 * time.Second), ValidUntil: &expires, ModelVersion: ptr("history")}
	r.forecastCut = s.Patterns.MetroCut().Snapshot()
	r.mu.Unlock()
	interest := metroInterest{Journey: track.Train.JourneyId}
	f, _, cut, safe, err := s.metroFrameWithCut(context.Background(), interest, false)
	if err != nil || safe || len(f.Trains) == 0 || len(f.Trains[0].Calls) == 0 || f.Trains[0].Calls[0].OwnPrediction == nil {
		t.Fatalf("candidate lacks historical forecast: %+v %v", f, err)
	}
	s.Patterns.MetroCut().MarkPending()
	if s.claimMetroPublication(cut, safe) {
		t.Fatal("pre-loss candidate was published after a cut")
	}
	fallback, _, fallbackCut, fallbackSafe, err := s.metroFrameWithCut(context.Background(), interest, true)
	if err != nil || !fallbackSafe || !s.claimMetroPublication(fallbackCut, fallbackSafe) || len(fallback.Trains) == 0 || len(fallback.Trains[0].Calls) == 0 || fallback.Trains[0].Calls[0].OwnPrediction != nil || fallback.Trains[0].Calls[0].Arrival.Prediction == nil || fallback.Revision == f.Revision {
		t.Fatalf("safe rebuilt frame lost official evidence or retained old history: %+v %v", fallback, err)
	}
}

func TestMetroFIFOLossAcknowledgesOnlyOrderedGapSample(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
		t.Fatal(err)
	}
	fifo, err := s.Patterns.AcquireMetroFIFO()
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()
	client := &MetroClient{Cache: s.Cache, History: s.Patterns}
	client.historyFIFO = fifo
	client.historyQueue = make(chan metroHistoryTask, 1)
	client.enqueuePatterns(data, d, now)
	client.enqueuePatterns(data, d, now.Add(time.Second))
	if !s.Patterns.MetroCut().Snapshot().Pending {
		t.Fatal("rejected input did not immediately fence history")
	}
	generation := client.deliverPatternHistory(<-client.historyQueue, 0)
	if !s.Patterns.MetroCut().Snapshot().Pending {
		t.Fatal("a pre-loss sample acknowledged a later cut")
	}
	client.enqueuePatterns(data, d, now.Add(31*time.Second))
	client.deliverPatternHistory(<-client.historyQueue, generation)
	if s.Patterns.MetroCut().Snapshot().Pending || client.historyBytes != 0 || s.Cache.metroRuntime.historyDeliveryGap {
		t.Fatal("ordered gap sample did not release the exact cut and queue accounting")
	}
	if !s.Cache.metroRuntime.capture.Gap {
		t.Fatal("pattern acknowledgement concealed independent capture gap")
	}
}

func TestMetroCutRebuildsFromIndependentOfficialAnchor(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	r := s.Cache.metroRuntime
	if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Minute)
	historical := &api.CallTimeEvidence{At: now.Add(10 * time.Second), ValidUntil: &expires, ModelVersion: ptr("historical")}
	official := &api.CallTimeEvidence{At: now.Add(30 * time.Second), ValidUntil: &expires, SourceUpdatedAt: &now}
	track := &metroTrack{Profile: "plan", Priors: []metroVisitPrior{{Dwell: 20, Run: 60}, {Dwell: 20}}, Train: api.MetroTrain{JourneyId: "episode", Association: "supported", SourceUpdatedAt: now, ValidUntil: expires, Calls: []api.StopCall{{Id: "first", OwnPrediction: historical, Arrival: api.CallTime{Prediction: official}}, {Id: "second"}}}}
	projectMetroScheduled(track, now)
	r.tracks["episode"], r.active["active"] = track, "episode"
	r.forecastCut = s.Patterns.MetroCut().Snapshot()
	s.Patterns.MetroCut().MarkPending()
	r.mu.Lock()
	trains := r.current(now)
	r.mu.Unlock()
	calls := trains[0].Calls
	if calls[0].OwnPrediction != nil || calls[0].Arrival.Prediction == nil || calls[0].OwnDeparturePrediction == nil || !calls[0].OwnDeparturePrediction.At.Equal(now.Add(50*time.Second)) || calls[1].OwnPrediction == nil || !calls[1].OwnPrediction.At.Equal(now.Add(110*time.Second)) {
		t.Fatalf("lost cut did not rebuild from independent official evidence: %+v", calls)
	}
}

func TestMetroArrivalProofIsFencedBeforeUnavailableAdmission(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	r := s.Cache.metroRuntime
	if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Minute)
	call := api.StopCall{Id: "visit", OwnPrediction: &api.CallTimeEvidence{At: now.Add(20 * time.Second), ValidUntil: &expires}, Arrival: api.CallTime{Inferred: &api.MetroEventEvidence{At: now}}}
	track := &metroTrack{Train: api.MetroTrain{JourneyId: "episode", Association: "supported", ValidUntil: expires, Calls: []api.StopCall{call}}}
	r.forecastCut = s.Patterns.MetroCut().Snapshot()
	s.Patterns.MetroCut().MarkPending()
	r.queueArrival(track, call, metroPoint{}, metroPoint{Clock: now})
	if len(track.Proofs) != 1 || len(r.pending) != 0 {
		t.Fatalf("unexpected proof or unavailable admission: %d %d", len(track.Proofs), len(r.pending))
	}
	for _, proof := range track.Proofs {
		if proof.Train.Calls[0].OwnPrediction != nil || proof.ForecastCut == nil {
			raw, _ := json.Marshal(proof)
			t.Fatalf("unavailable proof retained stale own prediction: %s", raw)
		}
	}
}

func TestMetroLegacyProofRecoveryHidesUnverifiedDepartures(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	expires := now.Add(time.Minute)
	evidence := &api.MetroEventEvidence{At: now, WindowStart: now.Add(-time.Second), WindowEnd: now, Mode: "model_departure"}
	actual := &api.CallTimeEvidence{At: now, SourceUrl: "https://example.invalid"}
	official := &api.CallTimeEvidence{At: now.Add(30 * time.Second), ValidUntil: &expires, SourceUrl: "https://example.invalid"}
	revisions := []api.MetroDepartureRevision{{Revision: 1, Status: "estimated", Evidence: evidence}}
	calls := []api.StopCall{
		{Id: "first", StopId: "metro:first", StopSequence: 0, Arrival: api.CallTime{Kind: "inferred", Inferred: evidence}, Departure: api.CallTime{Kind: "inferred", Inferred: evidence}, DepartureRevisions: &revisions, OwnPrediction: official},
		{Id: "second", StopId: "metro:second", StopSequence: 1, Departure: api.CallTime{Kind: "actual", Actual: actual}},
		{Id: "third", StopId: "metro:third", StopSequence: 2, Departure: api.CallTime{Kind: "prediction", Prediction: official}},
	}
	var records []patterns.MetroEventRecord
	for _, call := range calls {
		train := api.MetroTrain{JourneyId: "metro:run:legacy", SourceUpdatedAt: now, ValidUntil: expires, Calls: []api.StopCall{call}}
		raw, err := json.Marshal(metroEventProof{Train: train, CallID: call.Id})
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, patterns.MetroEventRecord{ID: call.Id, Journey: train.JourneyId, At: now, Payload: raw})
	}
	if err := s.Patterns.RecordMetroEvents(records, now); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		frame, _, err := s.metroFrame(context.Background(), metroInterest{Journey: "metro:run:legacy"})
		if err != nil || frame.Recovery == nil || frame.Recovery.Status != "partial" || len(frame.Trains) != 1 || len(frame.Trains[0].Calls) != 3 {
			t.Fatalf("legacy recovery unavailable: %+v %v", frame, err)
		}
		got := frame.Trains[0].Calls
		if got[0].Departure.Inferred != nil || got[0].DepartureRevisions != nil || got[0].OwnPrediction != nil || got[0].Arrival.Inferred == nil || got[1].Departure.Actual == nil || got[2].Departure.Prediction == nil {
			t.Fatalf("legacy recovery hid independent facts or exposed unverified departures: %+v", got)
		}
	}
	scoped, _, err := s.metroFrame(context.Background(), metroInterest{Journey: "metro:run:legacy", Stop: "metro:first"})
	if err != nil || len(scoped.Trains) != 1 || len(scoped.Trains[0].Calls) != 1 || scoped.Trains[0].Calls[0].Departure.Inferred != nil || scoped.Trains[0].Calls[0].DepartureRevisions != nil {
		t.Fatalf("scoped legacy recovery exposed unverified departure: %+v %v", scoped, err)
	}
}
