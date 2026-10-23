package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestMetroHandoffWaitsForItsOwnFrozenCheckpointBatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	dir := t.TempDir()
	history, err := patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = history.Close() }()
	r := newMetroRuntime()
	if err := r.bindMetroHistory(history); err != nil {
		t.Fatal(err)
	}
	r.archiveAvailable = true
	old, next := metroPredecessorFixture(now)
	old.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active"}
	next.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active"}
	r.tracks["old"], r.tracks["new"] = old, next
	r.queueCurrentCheckpoints()
	committed, release, done := make(chan error, 1), make(chan struct{}), make(chan struct{})
	go func() {
		r.flushCheckpointsWithCommit(context.Background(), history, now, func(ctx context.Context, batch []patterns.MetroJourneyCheckpoint, at time.Time, retry *patterns.MetroCheckpointAttempt) (patterns.MetroCheckpointCommit, error) {
			result, err := history.CommitMetroJourneysGuarded(ctx, batch, at, retry)
			committed <- err
			<-release
			return result, err
		})
		close(done)
	}()
	released := false
	defer func() {
		if !released {
			close(release)
			<-done
		}
	}()
	select {
	case err := <-committed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint commit did not finish")
	}
	r.mu.Lock()
	r.stageSuccessor(old, next, now, now)
	group := old.LifecycleGroup
	if group == "" || len(r.lifecycleAdmissions[group]) != 0 || metroTrainPersistence(old).State != api.MetroJourneyPersistenceStatePending || metroTrainPersistence(next).State != api.MetroJourneyPersistenceStatePending {
		r.mu.Unlock()
		t.Fatal("frozen old batch falsely admitted or acknowledged the new handoff")
	}
	r.mu.Unlock()
	close(release)
	released = true
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("old checkpoint ack blocked")
	}
	r.mu.Lock()
	if old.Generation == "" || old.Generation != next.Generation || old.LifecycleGroup != group || next.LifecycleGroup != group || metroTrainPersistence(old).State != api.MetroJourneyPersistenceStatePending || metroTrainPersistence(next).State != api.MetroJourneyPersistenceStatePending {
		r.mu.Unlock()
		t.Fatal("old checkpoint ack falsely committed the newer handoff")
	}
	r.mu.Unlock()
	assertCheckpointHandoff(t, history, now, false)
	r.mu.Lock()
	r.queueCurrentCheckpoints()
	r.mu.Unlock()
	r.flushCheckpoints(context.Background(), history, now.Add(time.Second))
	r.mu.Lock()
	if old.LifecycleGroup != "" || next.LifecycleGroup != "" || metroTrainPersistence(old).State != api.MetroJourneyPersistenceStateCommitted || metroTrainPersistence(next).State != api.MetroJourneyPersistenceStateCommitted {
		r.mu.Unlock()
		t.Fatal("group acknowledgement did not follow its exact payload")
	}
	r.mu.Unlock()
	assertCheckpointHandoff(t, history, now.Add(time.Second), true)
	if err := history.Close(); err != nil {
		t.Fatal(err)
	}
	history, err = patterns.Open(patterns.DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	assertCheckpointHandoff(t, history, now.Add(time.Second), true)
}

func assertCheckpointHandoff(t *testing.T, history *patterns.Service, now time.Time, linked bool) {
	t.Helper()
	var generation string
	for _, id := range []string{"old", "new"} {
		record, _, err := history.MetroJourneyCheckpoint(context.Background(), id, now)
		if err != nil {
			t.Fatal(err)
		}
		if generation == "" {
			generation = record.Generation
		} else if generation != record.Generation {
			t.Fatal("half handoff committed in different generations")
		}
		var payload metroCheckpointPayload
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		var relation *string
		if id == "old" {
			relation = payload.Train.Lifecycle.SuccessorJourneyId
		} else {
			relation = payload.Train.Lifecycle.PredecessorJourneyId
		}
		if linked != (relation != nil) {
			t.Fatalf("%s relationship mismatch: %+v", id, payload.Train.Lifecycle)
		}
		if linked && ((id == "old" && *relation != "new") || (id == "new" && *relation != "old")) {
			t.Fatal("wrong linked journey")
		}
	}
}

func TestMetroFrozenLifecycleTracksNewerState(t *testing.T) {
	for _, fixture := range []struct {
		name            string
		changed, queued bool
	}{{"unchanged", false, true}, {"changed and queued", true, true}, {"changed before queue", true, false}} {
		t.Run(fixture.name, func(t *testing.T) {
			changed := fixture.changed
			s, _, _, now := metroLiveFixture(t)
			r := newMetroRuntime()
			if err := r.bindMetroHistory(s.Patterns); err != nil {
				t.Fatal(err)
			}
			r.archiveAvailable = true
			old, next := metroPredecessorFixture(now)
			old.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active"}
			next.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active"}
			r.tracks["old"], r.tracks["new"] = old, next
			r.stageSuccessor(old, next, now, now)
			committed, release, done := make(chan error, 1), make(chan struct{}), make(chan struct{})
			go func() {
				r.flushCheckpointsWithCommit(context.Background(), s.Patterns, now, func(ctx context.Context, batch []patterns.MetroJourneyCheckpoint, at time.Time, retry *patterns.MetroCheckpointAttempt) (patterns.MetroCheckpointCommit, error) {
					result, err := s.Patterns.CommitMetroJourneysGuarded(ctx, batch, at, retry)
					committed <- err
					<-release
					return result, err
				})
				close(done)
			}()
			released := false
			defer func() {
				if !released {
					close(release)
					<-done
				}
			}()
			select {
			case err := <-committed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint commit did not finish")
			}
			r.mu.Lock()
			if changed {
				old.Train.Reason = "newer source state"
			}
			if fixture.queued {
				r.queueCurrentCheckpoints()
			}
			if changed && fixture.queued && (metroTrainPersistence(old).State != api.MetroJourneyPersistenceStatePending || metroTrainPersistence(next).State != api.MetroJourneyPersistenceStatePending) {
				r.mu.Unlock()
				t.Fatal("newer frozen group state not pending")
			}
			r.mu.Unlock()
			close(release)
			released = true
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint ack blocked")
			}
			r.mu.Lock()
			if changed != (old.LifecycleGroup != "" && next.LifecycleGroup != "") {
				r.mu.Unlock()
				t.Fatal("checkpoint ack did not match current grouped payload")
			}
			if changed {
				r.queueCurrentCheckpoints()
			}
			r.mu.Unlock()
			if changed {
				r.flushCheckpoints(context.Background(), s.Patterns, now.Add(time.Second))
				r.mu.Lock()
				defer r.mu.Unlock()
				if old.LifecycleGroup != "" || next.LifecycleGroup != "" || metroTrainPersistence(old).State != api.MetroJourneyPersistenceStateCommitted {
					t.Fatal("newer group was not committed")
				}
			}
		})
	}
}

func TestMetroEventBatchExpiryBoundaryAndAcknowledgement(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	now = now.Truncate(time.Hour)
	r := s.Cache.metroRuntime
	r.archiveAvailable = true
	track := &metroTrack{Train: api.MetroTrain{JourneyId: "journey"}}
	r.tracks[track.Train.JourneyId] = track
	boundary := now.AddDate(0, 0, -7)
	for _, v := range []struct {
		id string
		at time.Time
	}{{"old", boundary.Add(-time.Nanosecond)}, {"edge", boundary}, {"fresh", now}} {
		callID := v.id
		track.Train.Calls = append(track.Train.Calls, api.StopCall{Id: callID, Arrival: api.CallTime{Inferred: &api.MetroEventEvidence{WindowEnd: v.at, Persistence: "pending"}}})
		record := patterns.MetroEventRecord{ID: callID + ":arrival:proof", Journey: track.Train.JourneyId, At: v.at, Payload: json.RawMessage(`{"proof":true}`)}
		r.pending[record.ID] = record
		r.pendingBytes += len(record.Payload)
	}
	r.flushEvents(s.Patterns, now)
	if len(r.pending) != 0 || r.pendingBytes != 0 || track.Train.Calls[0].Arrival.Inferred.Persistence != "unavailable" || track.Train.Calls[1].Arrival.Inferred.Persistence != "committed" || track.Train.Calls[2].Arrival.Inferred.Persistence != "committed" {
		t.Fatalf("expired, boundary and fresh events mishandled: %+v pending=%d bytes=%d", track.Train.Calls, len(r.pending), r.pendingBytes)
	}
	events, err := s.Patterns.MetroJourneyEvents(context.Background(), track.Train.JourneyId, now)
	if err != nil || len(events) != 2 {
		t.Fatalf("committed event recovery: %v, %+v", err, events)
	}
	r.flushEvents(s.Patterns, now.Add(time.Nanosecond))
	if len(r.pending) != 0 || r.pendingBytes != 0 {
		t.Fatal("expiry caused a retry loop")
	}
}
