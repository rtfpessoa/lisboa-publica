package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// The synthetic track keeps its source data and event evidence fixed while
// repeated delivery losses advance the archive's pending cut.
type metroPendingCheckpointFixture struct {
	dir      string
	history  *patterns.Service
	runtime  *metroRuntime
	track    *metroTrack
	now      time.Time
	firstCut patterns.MetroCutSnapshot
}

func newMetroPendingCheckpointFixture(t *testing.T) *metroPendingCheckpointFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(time.Hour)
	dir := t.TempDir()
	history, err := patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	f := &metroPendingCheckpointFixture{dir: dir, history: history, runtime: newMetroRuntime(), now: now}
	t.Cleanup(func() {
		if f.history != nil {
			_ = f.history.Close()
		}
	})
	r := f.runtime
	if err := r.bindMetroHistory(history); err != nil {
		t.Fatal(err)
	}
	r.archiveAvailable = true
	ready := history.MetroCut().Snapshot()
	r.forecastCut = ready
	forecast := &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: &expires, SourceUpdatedAt: &now}
	f.track = &metroTrack{Train: api.MetroTrain{
		JourneyId: "synthetic:track", Association: "suspended", SourceUpdatedAt: now, ValidUntil: expires,
		Calls: []api.StopCall{{Id: "visit", OwnPrediction: forecast, Arrival: api.CallTime{Kind: "prediction", Prediction: forecast}}},
	}}
	f.track.Proofs = map[string]metroEventProof{"earlier": {
		ForecastCut: metroCutOrigin(ready), Train: cloneMetroTrain(f.track.Train), CallID: "visit",
		Before: metroPoint{Clock: now.Add(-time.Second)}, After: metroPoint{Clock: now},
	}}
	r.tracks[f.track.Train.JourneyId] = f.track
	r.queueCheckpoint(f.track)
	r.flushCheckpoints(context.Background(), history, now)
	if f.track.CommittedRevision != 1 {
		t.Fatal("initial checkpoint was not committed")
	}
	f.firstCut = history.MetroCut().MarkPending()
	r.queueCheckpoint(f.track)
	r.flushCheckpoints(context.Background(), history, now.Add(time.Second))
	if f.track.CommittedRevision != 2 {
		t.Fatal("initial loss did not commit a fenced checkpoint")
	}
	return f
}

func (f *metroPendingCheckpointFixture) checkpoint(t *testing.T, at time.Time) (patterns.MetroJourneyCheckpoint, metroCheckpointPayload) {
	t.Helper()
	stored, state, err := f.history.MetroJourneyCheckpoint(context.Background(), f.track.Train.JourneyId, at)
	if err != nil || state != "restored" {
		t.Fatalf("checkpoint unavailable: state=%s err=%v", state, err)
	}
	var payload metroCheckpointPayload
	if err := json.Unmarshal(stored.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return stored, payload
}

func (f *metroPendingCheckpointFixture) persistTrackChange(t *testing.T) time.Time {
	t.Helper()
	later := f.now.Add(2 * time.Second)
	arrival := &api.MetroEventEvidence{At: later, WindowStart: f.now, WindowEnd: later, Mode: "inferred_arrival", Persistence: "pending"}
	f.track.Train.Calls[0].Arrival = api.CallTime{Kind: "inferred", At: &arrival.At, Inferred: arrival}
	f.track.Train.SourceUpdatedAt = later
	f.track.Train.Reason = "updated source evidence"
	f.track.Proofs["later"] = metroEventProof{Train: cloneMetroTrain(f.track.Train), CallID: "visit", Before: metroPoint{Clock: f.now}, After: metroPoint{Clock: later}}
	f.runtime.queueCheckpoint(f.track)
	if len(f.runtime.dirty) != 1 {
		t.Fatal("real track change was suppressed during a pending cut")
	}
	f.runtime.flushCheckpoints(context.Background(), f.history, later)
	return later
}

func (f *metroPendingCheckpointFixture) advancePendingCut(t *testing.T) {
	t.Helper()
	f.history.MetroCut().MarkPending()
	f.runtime.queueCheckpoint(f.track)
	if len(f.runtime.dirty) != 0 || f.track.Revision != 2 {
		t.Fatalf("unchanged checkpoint was rewritten for a later pending cut: dirty=%d revision=%d", len(f.runtime.dirty), f.track.Revision)
	}
	if f.history.MetroCut().Acknowledge(f.firstCut) {
		t.Fatal("an older cut acknowledged a newer loss")
	}
}

func TestMetroPendingCheckpointIgnoresCutChurn(t *testing.T) {
	f := newMetroPendingCheckpointFixture(t)
	f.advancePendingCut(t)
	stored, pending := f.checkpoint(t, f.now.Add(time.Second))
	if pending.ForecastCut != nil || pending.Train.Calls[0].OwnPrediction != nil || pending.Train.Calls[0].Arrival.Prediction == nil {
		t.Fatal("pending checkpoint did not fence only history-derived forecasts")
	}
	if !stored.SourceAt.Equal(f.now) || !reflect.DeepEqual(pending.Proofs, f.track.Proofs) {
		t.Fatal("pending checkpoint changed earlier event proof evidence")
	}
}

func TestMetroPendingCheckpointPersistsTrackChanges(t *testing.T) {
	f := newMetroPendingCheckpointFixture(t)
	f.advancePendingCut(t)
	later := f.persistTrackChange(t)
	stored, pending := f.checkpoint(t, later)
	if !stored.SourceAt.Equal(later) {
		t.Fatal("changed checkpoint lost its original source clock")
	}
	if pending.Train.Reason != f.track.Train.Reason || pending.Train.Calls[0].Arrival.Inferred == nil || !pending.Train.Calls[0].Arrival.Inferred.At.Equal(later) || !reflect.DeepEqual(pending.Proofs, f.track.Proofs) {
		t.Fatal("changed track or prior proof was not persisted intact")
	}
}

func TestMetroCheckpointRestoresProvenanceAfterPendingCut(t *testing.T) {
	f := newMetroPendingCheckpointFixture(t)
	f.advancePendingCut(t)
	later := f.persistTrackChange(t)
	latest := f.history.MetroCut().Snapshot()
	if !f.history.MetroCut().Acknowledge(latest) {
		t.Fatal("latest gap could not be acknowledged")
	}
	f.runtime.queueCheckpoint(f.track)
	if len(f.runtime.dirty) != 1 {
		t.Fatal("recovered cut did not queue exact provenance")
	}
	f.runtime.flushCheckpoints(context.Background(), f.history, f.now.Add(3*time.Second))
	if err := f.history.Close(); err != nil {
		t.Fatal(err)
	}
	f.history = nil
	history, err := patterns.Open(patterns.DefaultConfig(f.dir))
	if err != nil {
		t.Fatal(err)
	}
	f.history = history
	stored, recovered := f.checkpoint(t, f.now.Add(3*time.Second))
	if !reflect.DeepEqual(recovered.ForecastCut, metroCutOrigin(latest)) || !reflect.DeepEqual(recovered.Proofs, f.track.Proofs) || !stored.SourceAt.Equal(later) {
		t.Fatal("recovered checkpoint lost current provenance or retained evidence")
	}
}

func TestMetroCheckpointClaimRaceUsesSafeSnapshot(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	r := s.Cache.metroRuntime
	if err := r.bindMetroHistory(s.Patterns); err != nil {
		t.Fatal(err)
	}
	ready := s.Patterns.MetroCut().Snapshot()
	r.forecastCut = ready
	expires := now.Add(time.Hour)
	forecast := &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: &expires, SourceUpdatedAt: &now}
	track := &metroTrack{Train: api.MetroTrain{JourneyId: "claim-race", Association: "suspended", SourceUpdatedAt: now, ValidUntil: expires,
		Calls: []api.StopCall{{Id: "visit", OwnPrediction: forecast, Arrival: api.CallTime{Kind: "prediction", Prediction: forecast}}}}}
	track.Proofs = map[string]metroEventProof{"earlier": {ForecastCut: metroCutOrigin(ready), Train: cloneMetroTrain(track.Train)}}
	called := false
	raw, _, ok := r.encodeCheckpointWithClaim(track, func(cut patterns.MetroCutSnapshot) bool {
		called = true
		if cut != ready {
			t.Fatal("checkpoint did not claim the cut used to construct it")
		}
		s.Patterns.MetroCut().MarkPending()
		return r.claimMetroSnapshot(cut)
	})
	if !called || !ok {
		t.Fatal("cut race did not produce a safe checkpoint")
	}
	var payload metroCheckpointPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ForecastCut != nil || payload.Train.Calls[0].OwnPrediction != nil || payload.Train.Calls[0].Arrival.Prediction == nil {
		t.Fatal("cut race retained a historical forecast or lost the independent official forecast")
	}
	if !reflect.DeepEqual(payload.Proofs, track.Proofs) || !payload.Train.SourceUpdatedAt.Equal(now) {
		t.Fatal("cut race changed earlier proof evidence or the original source clock")
	}
}

func TestMetroPendingCutKeepsFrozenLifecycleAtomic(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = history.Close() }()
	r := newMetroRuntime()
	if err := r.bindMetroHistory(history); err != nil {
		t.Fatal(err)
	}
	r.archiveAvailable = true
	r.forecastCut = history.MetroCut().Snapshot()
	old, next := metroPredecessorFixture(now)
	old.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active"}
	next.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active"}
	r.tracks["old"], r.tracks["new"] = old, next
	r.queueCurrentCheckpoints()
	var group string
	r.flushCheckpointsWithCommit(context.Background(), history, now, func(ctx context.Context, batch []patterns.MetroJourneyCheckpoint, at time.Time, retry *patterns.MetroCheckpointAttempt) (patterns.MetroCheckpointCommit, error) {
		result, err := history.CommitMetroJourneysGuarded(ctx, batch, at, retry)
		if err != nil {
			return result, err
		}
		history.MetroCut().MarkPending()
		r.mu.Lock()
		r.stageSuccessor(old, next, now, now)
		group = old.LifecycleGroup
		r.mu.Unlock()
		return result, nil
	})
	if group == "" || old.LifecycleGroup != group || next.LifecycleGroup != group || metroTrainPersistence(old).State != "pending" || metroTrainPersistence(next).State != "pending" {
		t.Fatal("old in-flight checkpoint acknowledged a later lifecycle handoff")
	}
	if !history.MetroCut().Snapshot().Pending {
		t.Fatal("checkpoint acknowledgement cleared the later loss")
	}
	assertCheckpointHandoff(t, history, now, false)
	r.queueCurrentCheckpoints()
	r.flushCheckpoints(context.Background(), history, now.Add(time.Second))
	if old.LifecycleGroup != "" || next.LifecycleGroup != "" || metroTrainPersistence(old).State != "committed" || metroTrainPersistence(next).State != "committed" {
		t.Fatal("complete lifecycle group did not commit")
	}
	assertCheckpointHandoff(t, history, now.Add(time.Second), true)
	for _, id := range []string{"old", "new"} {
		record, state, err := history.MetroJourneyCheckpoint(context.Background(), id, now.Add(time.Second))
		if err != nil || state != "restored" || !record.SourceAt.Equal(now) {
			t.Fatalf("%s checkpoint lost source clock: %s %v", id, state, err)
		}
		var payload metroCheckpointPayload
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.ForecastCut != nil {
			t.Fatalf("%s checkpoint retained pending provenance: %v", id, err)
		}
	}
	history.MetroCut().MarkPending()
	r.queueCurrentCheckpoints()
	if len(r.dirty) != 0 {
		t.Fatal("another pending cut rewrote the completed lifecycle group")
	}
}

func TestMetroCheckpointRecoveryWithAndWithoutProvenance(t *testing.T) {
	for _, pending := range []bool{false, true} {
		name := "ready"
		if pending {
			name = "pending"
		}
		t.Run(name, func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
			publishMetroTest(s, d, data, now)
			if len(data.Trains) != 1 {
				t.Fatal("missing synthetic train")
			}
			id := data.Trains[0].JourneyId
			if pending {
				s.Patterns.MetroCut().MarkPending()
				s.Cache.metroRuntime.queueCurrentCheckpoints()
				s.Cache.metroRuntime.flushCheckpoints(context.Background(), s.Patterns, now)
			}
			record, state, err := s.Patterns.MetroJourneyCheckpoint(context.Background(), id, now)
			if err != nil || state != "restored" {
				t.Fatalf("checkpoint unavailable: %s %v", state, err)
			}
			var payload metroCheckpointPayload
			if err := json.Unmarshal(record.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if (payload.ForecastCut == nil) != pending {
				t.Fatal("checkpoint provenance did not match cut availability")
			}
			s.Cache.metroRuntime = newMetroRuntime()
			frame, _, err := s.metroFrame(context.Background(), metroInterest{Journey: id})
			if err != nil || len(frame.Trains) != 1 || frame.Recovery == nil || frame.Recovery.Status != "historical" {
				t.Fatalf("checkpoint recovery failed: %v", err)
			}
			train := frame.Trains[0]
			if train.Association != "suspended" || train.NextIndex != nil || train.CurrentIndex != nil || !train.SourceUpdatedAt.Equal(now) {
				t.Fatal("checkpoint recovery renewed live continuity or the source clock")
			}
			for _, call := range train.Calls {
				if call.OwnPrediction != nil || call.OwnDeparturePrediction != nil {
					t.Fatal("recovered checkpoint exposed a live historical prediction")
				}
			}
		})
	}
}
