package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMetroTerminalClosureRequiresSupportedArrivalAndCommit(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "CS", "7", "10")}
	publishMetroTest(s, d, data, now)
	id := data.Trains[0].JourneyId
	next := *data
	next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "CS", "7", "0")}
	r := s.Cache.metroRuntime
	r.observe(&next, d, s.Patterns, now.Add(time.Second))
	// Closure is frozen until its complete generation commits.
	if len(next.Trains) != 1 || next.Trains[0].Lifecycle.State != "active" {
		t.Fatal("provisional lifecycle exposed", next.Trains)
	}
	r.mu.Lock()
	track := r.tracks[id]
	revision := track.BarrierRevision
	r.mu.Unlock()
	if revision == 0 {
		t.Fatal("missing mandatory revision")
	}
	delayed := next
	delayed.Waits = []MetroWait{metroTestRow(now.Add(2*time.Second), "CS", "7", "30")}
	r.observe(&delayed, d, s.Patterns, now.Add(2*time.Second))
	if track.BarrierRevision != revision {
		t.Fatal("mandatory closure was coalesced")
	}
	r.flushCheckpoints(context.Background(), s.Patterns, now.Add(2*time.Second))
	r.mu.Lock()
	active := r.current(now.Add(2 * time.Second))
	r.mu.Unlock()
	if len(active) != 0 {
		t.Fatal("completed journey active", active)
	}
	retained, ok := r.retained(id, now.Add(2*time.Second))
	if !ok || retained.Lifecycle.State != "completed" || retained.Calls[1].Arrival.Inferred == nil {
		t.Fatal("timeline lost", retained)
	}
	// Later reused-reference forecasts cannot reopen the completed episode.
	publishMetroTest(s, d, &delayed, now.Add(2*time.Second))
	if len(delayed.Trains) != 0 {
		t.Fatal("completed episode reopened")
	}
	s.Cache.metroRuntime = newMetroRuntime()
	f, _, err := s.metroFrame(context.Background(), metroInterest{Journey: id})
	if err != nil || len(f.Trains) != 1 || f.Trains[0].Lifecycle.State != "completed" || f.Trains[0].CurrentIndex != nil {
		t.Fatal("closure did not restore", f, err)
	}
}
func TestMetroTerminalExcludesIsolatedZeroCountdownAndIntermediate(t *testing.T) {
	for _, kind := range []string{"isolated-zero", "countdown", "intermediate"} {
		t.Run(kind, func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			stop, value := "CS", "10"
			if kind == "isolated-zero" {
				value = "0"
			}
			if kind == "intermediate" {
				stop = "RM"
			}
			data.Waits = []MetroWait{metroTestRow(now, stop, "7", value)}
			publishMetroTest(s, d, data, now)
			if kind == "intermediate" {
				data.Waits = []MetroWait{metroTestRow(now.Add(time.Second), stop, "7", "0")}
				publishMetroTest(s, d, data, now.Add(time.Second))
			}
			if len(data.Trains) != 1 || data.Trains[0].Lifecycle.State != "active" {
				t.Fatal("unsupported closure")
			}
			if kind == "countdown" {
				r := s.Cache.metroRuntime
				r.mu.Lock()
				r.current(now.Add(11 * time.Second))
				state := r.tracks[data.Trains[0].JourneyId].Train.Lifecycle.State
				r.mu.Unlock()
				if state != "active" {
					t.Fatal("countdown closed journey")
				}
			}
		})
	}
}
func TestMetroSuccessorAtomicGenerationAndCrashRecovery(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	old := r.tracks[data.Trains[0].JourneyId]
	next := r.startTrack(metroTrackStart{key: "metro:r|reverse|7", reference: "7", path: r.topology.Patterns[0], data: data, static: d, now: now})
	delete(r.active, "metro:r|reverse|7")
	r.candidates["metro:r|reverse|7"] = next.Train.JourneyId
	next.Train.SourceUpdatedAt = now.Add(2 * time.Second)
	next.Train.ValidUntil = now.Add(time.Minute)
	if !r.stageSuccessor(old, next, now.Add(2*time.Second), now.Add(time.Second)) {
		t.Fatal("handoff not staged")
	}
	if current := r.current(now); len(current) != 1 || current[0].JourneyId != old.Train.JourneyId {
		t.Fatal("premature or double association", current)
	}
	r.flushCheckpoints(context.Background(), s.Patterns, now.Add(2*time.Second))
	current := r.current(now.Add(2 * time.Second))
	if len(current) != 1 || current[0].JourneyId != next.Train.JourneyId {
		t.Fatal("handoff not atomic", current)
	}
	a, _, err := s.Patterns.MetroJourneyCheckpoint(context.Background(), old.Train.JourneyId, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Patterns.MetroJourneyCheckpoint(context.Background(), next.Train.JourneyId, now.Add(2*time.Second))
	if err != nil || a.Generation != b.Generation {
		t.Fatal("different commit generations", err)
	}
	var p metroCheckpointPayload
	if json.Unmarshal(a.Payload, &p) != nil || p.Train.Lifecycle.SuccessorJourneyId == nil || *p.Train.Lifecycle.SuccessorJourneyId != next.Train.JourneyId {
		t.Fatal("missing durable relationship")
	}
	if p.Train.Lifecycle.FirstMovementAt.Equal(*p.Train.Lifecycle.DirectionConfirmedAt) {
		t.Fatal("timing clocks conflated")
	}
}

func TestMetroMandatoryClosureCommitFailureRetainsPriorState(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "CS", "7", "10")}
	publishMetroTest(s, d, data, now)
	id := data.Trains[0].JourneyId
	if err := s.Patterns.Close(); err != nil {
		t.Fatal(err)
	}
	next := *data
	next.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "CS", "7", "0")}
	r := s.Cache.metroRuntime
	r.observe(&next, d, s.Patterns, now.Add(time.Second))
	r.flushCheckpoints(context.Background(), s.Patterns, now.Add(time.Second))
	r.mu.Lock()
	current := r.current(now.Add(time.Second))
	r.mu.Unlock()
	if len(current) != 1 || current[0].JourneyId != id || current[0].Lifecycle.State != "active" || r.tracks[id].BarrierRevision == 0 {
		t.Fatal("failed commit exposed closure", current)
	}
}

func TestMetroHealthyBaselineAdmissionDelay(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := s.Cache.metroRuntime
	started := time.Now()
	r.observe(data, d, s.Patterns, now)
	go r.runJournal(ctx, s.Patterns)
	deadline := started.Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		current := r.current(time.Now())
		r.mu.Unlock()
		if len(current) == 1 {
			t.Logf("synthetic baseline receipt-to-selectable latency: %s (separate from frame-to-DOM)", time.Since(started))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("healthy baseline was not admitted within bounded flush target plus fixture storage allowance")
}
