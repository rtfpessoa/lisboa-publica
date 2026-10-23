package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestMetroRecoveredBarriersPreserveUnresolvedHistoryLanes(t *testing.T) {
	for _, lane := range []string{"proof overflow", "checkpoint unavailable", "event write"} {
		for _, barrier := range []string{"capture", "standalone", "FIFO"} {
			t.Run(lane+"/"+barrier, func(t *testing.T) {
				s, d, data, now := metroLiveFixture(t)
				if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
					t.Fatal(err)
				}
				r := s.Cache.metroRuntime
				r.mu.Lock()
				r.archiveAvailable, r.historyStatus = true, "paused"
				if lane == "event write" {
					putPendingLaneEvent(r, "event", now)
				} else {
					r.tracks["unavailable"] = &metroTrack{ProofOverflow: lane == "proof overflow", CheckpointUnavailable: lane == "checkpoint unavailable", Train: api.MetroTrain{JourneyId: "unavailable", SourceUpdatedAt: now}}
				}
				r.mu.Unlock()
				if lane == "event write" {
					r.flushEventsWithWrite(now, func([]patterns.MetroEventRecord, time.Time) error { return errors.New("synthetic event write failure") })
				}
				recoverMetroHistoryBarrier(t, barrier, s, d, data, now)
				r.mu.Lock()
				if r.historyStatus != "paused" || (lane == "event write" && len(r.failedEvents) != 1) {
					r.mu.Unlock()
					t.Fatal("unrelated recovery hid an unresolved lane")
				}
				r.mu.Unlock()
				if lane == "event write" {
					r.flushEvents(s.Patterns, now.Add(time.Second))
				} else {
					r.mu.Lock()
					r.prune(now.AddDate(0, 0, 8))
					r.mu.Unlock()
				}
				r.mu.Lock()
				defer r.mu.Unlock()
				if r.historyStatus != "collecting" || len(r.failedEvents) != 0 {
					t.Fatal("successful event retry or track retirement did not resolve the lane")
				}
			})
		}
	}
}

func putPendingLaneEvent(r *metroRuntime, id string, now time.Time) {
	record := patterns.MetroEventRecord{ID: id, Journey: "journey", At: now, Payload: json.RawMessage(`{"proof":true}`)}
	r.pending[id] = record
	r.pendingBytes += len(record.Payload)
}

func recoverMetroHistoryBarrier(t *testing.T, barrier string, s *Server, d *StaticData, data *MetroData, now time.Time) {
	t.Helper()
	r := s.Cache.metroRuntime
	m := &MetroClient{Cache: s.Cache, History: s.Patterns}
	switch barrier {
	case "capture":
		r.mu.Lock()
		r.markCaptureGap()
		input := metroCapturedInput{GapBefore: true, GapVersion: r.capture.GapVersion, ReceivedAt: now}
		raw, err := json.Marshal(input)
		if err != nil {
			r.mu.Unlock()
			t.Fatal(err)
		}
		r.admitCapturedInput(input, raw, "gap", now)
		r.mu.Unlock()
		r.flushInputs(context.Background(), s.Patterns, now)
	case "standalone":
		s.Patterns.InterruptMetroContinuity()
		r.mu.Lock()
		r.historyDeliveryGap = true
		r.mu.Unlock()
		if err := m.recordPatterns(data, d, now); err != nil {
			t.Fatal(err)
		}
		if s.Patterns.MetroCut().Snapshot().Pending {
			t.Fatal("standalone gap was not acknowledged")
		}
	case "FIFO":
		fifo, err := s.Patterns.AcquireMetroFIFO()
		if err != nil {
			t.Fatal(err)
		}
		defer fifo.Close()
		m.historyFIFO = fifo
		s.Patterns.MetroCut().MarkPending()
		if err = fifo.InterruptMetroContinuity(); err != nil {
			t.Fatal(err)
		}
		r.mu.Lock()
		r.historyDeliveryGap = true
		r.mu.Unlock()
		receipt, topology := m.patternReceipt(data, d, now)
		result, err := fifo.RecordMetro(receipt, topology)
		if err != nil {
			t.Fatal(err)
		}
		m.historyBytes = 1
		m.finishPatternHistory(metroHistoryTask{Bytes: 1}, result, nil, false)
		if s.Patterns.MetroCut().Snapshot().Pending {
			t.Fatal("FIFO gap was not acknowledged")
		}
	}
}

func TestMetroEventFailureClearsOnlyMatchingBatch(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	r := s.Cache.metroRuntime
	if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	putPendingLaneEvent(r, "first", now)
	r.mu.Unlock()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		r.flushEventsWithWrite(now, func([]patterns.MetroEventRecord, time.Time) error { close(started); <-release; return nil })
		close(done)
	}()
	<-started
	r.mu.Lock()
	putPendingLaneEvent(r, "newer", now.Add(time.Second))
	r.mu.Unlock()
	r.flushEventsWithWrite(now.Add(time.Second), func([]patterns.MetroEventRecord, time.Time) error { return errors.New("later failure") })
	close(release)
	<-done
	r.mu.Lock()
	if r.historyStatus != "paused" || len(r.failedEvents) != 2 || r.failedEvents["newer"].Record.ID != "newer" {
		r.mu.Unlock()
		t.Fatal("older success cleared a newer failed event")
	}
	r.mu.Unlock()
	r.flushEvents(s.Patterns, now.Add(2*time.Second))
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.failedEvents) != 0 || r.historyStatus != "collecting" {
		t.Fatal("matching retry did not clear newer failure")
	}
}

func TestMetroFailedEventRetirementReevaluatesHistory(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	r := s.Cache.metroRuntime
	if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	putPendingLaneEvent(r, "old", now)
	r.mu.Unlock()
	r.flushEventsWithWrite(now, func([]patterns.MetroEventRecord, time.Time) error { return errors.New("failure") })
	r.flushEventsWithWrite(now.AddDate(0, 0, 7).Add(time.Nanosecond), func([]patterns.MetroEventRecord, time.Time) error { t.Fatal("expired event was written"); return nil })
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) != 0 || len(r.failedEvents) != 0 || r.pendingBytes != 0 || r.historyStatus != "collecting" {
		t.Fatal("explicit expiry did not retire the failed event lane")
	}
}

func TestMetroOlderSuccessDoesNotClearNewerFailureOfSameEvent(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	r := s.Cache.metroRuntime
	if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	putPendingLaneEvent(r, "same", now)
	r.mu.Unlock()
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		r.flushEventsWithWrite(now, func([]patterns.MetroEventRecord, time.Time) error { close(started); <-release; return nil })
		close(done)
	}()
	<-started
	r.flushEventsWithWrite(now, func([]patterns.MetroEventRecord, time.Time) error { return errors.New("newer failure") })
	close(release)
	<-done
	r.mu.Lock()
	if r.historyStatus != "paused" || len(r.failedEvents) != 1 || len(r.pending) != 1 {
		r.mu.Unlock()
		t.Fatal("older success acknowledged a newer failure of the same event")
	}
	r.mu.Unlock()
	r.flushEvents(s.Patterns, now.Add(time.Second))
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.failedEvents) != 0 || len(r.pending) != 0 || r.historyStatus != "collecting" {
		t.Fatal("matching newer retry did not resolve failure")
	}
}

func TestMetroRealEventWriteFailureRetriesExactPendingRecord(t *testing.T) {
	dir := t.TempDir()
	history, err := patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	r := newMetroRuntime()
	if err = r.bindMetroHistory(history); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	putPendingLaneEvent(r, "proof", now)
	blocker := filepath.Join(dir, ".manifest.tmp")
	if err = os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	r.flushEvents(history, now)
	if r.historyStatus != "paused" || len(r.failedEvents) != 1 || len(r.pending) != 1 {
		t.Fatal("archive error did not preserve the failed record")
	}
	if err = os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	var retryErr error
	r.flushEventsWithWrite(now.Add(time.Second), func(batch []patterns.MetroEventRecord, at time.Time) error {
		retryErr = history.RecordMetroEvents(batch, at)
		return retryErr
	})
	if retryErr != nil || len(r.failedEvents) != 0 || len(r.pending) != 0 {
		t.Fatalf("exact event retry did not recover: status=%s failed=%d pending=%d error=%v", r.historyStatus, len(r.failedEvents), len(r.pending), retryErr)
	}
	if !history.MetroCut().Snapshot().Pending || r.historyStatus != "paused" {
		t.Fatal("event retry concealed the separate archive cut")
	}
	cache := NewCache()
	cache.metroRuntime = r
	client := &MetroClient{Cache: cache, History: history}
	data := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusOk}}
	if err := client.recordPatterns(data, nil, now.Add(31*time.Second)); err != nil {
		t.Fatal(err)
	}
	if r.historyStatus != "collecting" || history.MetroCut().Snapshot().Pending {
		t.Fatal("matching history barrier did not complete recovery")
	}
	records, err := history.MetroJourneyEvents(context.Background(), "journey", now.Add(time.Second))
	if err != nil || len(records) != 1 || records[0].ID != "proof" {
		t.Fatalf("retried event not durable: %v %+v", err, records)
	}
}

func TestMetroEventAdmissionLossSurvivesUnrelatedBarrier(t *testing.T) {
	for _, barrier := range []string{"capture", "standalone", "FIFO"} {
		t.Run(barrier, func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
				t.Fatal(err)
			}
			r := s.Cache.metroRuntime
			point := metroPoint{Clock: now}
			evidence := &api.MetroEventEvidence{At: now, WindowEnd: now, Persistence: "pending"}
			call := api.StopCall{Id: "visit", Arrival: api.CallTime{Inferred: evidence}}
			track := &metroTrack{Train: api.MetroTrain{JourneyId: "journey", SourceUpdatedAt: now, Calls: []api.StopCall{call}}}
			r.mu.Lock()
			r.archiveAvailable = true
			r.tracks["journey"] = track
			for n := 0; n < 1024; n++ {
				putPendingLaneEvent(r, fmt.Sprintf("full:%d", n), now)
			}
			r.queueArrival(track, call, point, point)
			if !r.eventAdmissionLost || evidence.Persistence != "unavailable" || r.historyStatus != "paused" {
				r.mu.Unlock()
				t.Fatal("capacity rejection was not recorded as an unavailable occurrence")
			}
			r.pending, r.pendingBytes = map[string]patterns.MetroEventRecord{}, 0
			r.mu.Unlock()
			recoverMetroHistoryBarrier(t, barrier, s, d, data, now)
			r.mu.Lock()
			if r.historyStatus != "paused" || evidence.Persistence != "unavailable" {
				r.mu.Unlock()
				t.Fatal("unrelated recovery concealed event admission loss")
			}
			r.prune(now.AddDate(0, 0, 7))
			if !r.eventAdmissionLost || r.historyStatus != "paused" {
				r.mu.Unlock()
				t.Fatal("event loss retired at the protected boundary")
			}
			r.prune(now.AddDate(0, 0, 7).Add(time.Nanosecond))
			defer r.mu.Unlock()
			if r.eventAdmissionLost || r.historyStatus != "collecting" || evidence.Persistence != "unavailable" {
				t.Fatal("event admission loss did not retire safely")
			}
		})
	}
}

func TestMetroUnknownEventAdmissionClockFailsClosed(t *testing.T) {
	r := newMetroRuntime()
	r.historyStatus = "paused"
	r.markEventAdmissionLoss(time.Time{})
	r.markEventAdmissionLoss(time.Now().UTC())
	r.historyCollecting(time.Now().UTC().AddDate(0, 0, 100))
	if !r.eventAdmissionLost || r.historyStatus != "paused" {
		t.Fatal("unknown loss clock was retired without evidence")
	}
}

func TestMetroAdmissionLossRetainsNewestOriginalClock(t *testing.T) {
	r := newMetroRuntime()
	first := time.Now().UTC()
	second := first.Add(time.Hour)
	r.historyStatus = "paused"
	r.markEventAdmissionLoss(first)
	r.markEventAdmissionLoss(second)
	r.historyCollecting(first.AddDate(0, 0, 7).Add(time.Nanosecond))
	if !r.eventAdmissionLost || r.historyStatus != "paused" {
		t.Fatal("older expiry concealed a newer lost event")
	}
	r.historyCollecting(second.AddDate(0, 0, 7).Add(time.Nanosecond))
	if r.eventAdmissionLost || r.historyStatus != "collecting" {
		t.Fatal("newest loss did not expire")
	}
}
