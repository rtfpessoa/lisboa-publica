package app

import (
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestMetroRetractionAcknowledgementIgnoresTransientForecastChanges(t *testing.T) {
	now := time.Now().UTC()
	old, _ := metroPredecessorFixture(now)
	r := newMetroRuntime()
	r.archiveAvailable = true
	r.tracks["old"] = old
	old.Train.Calls[0].Arrival = missingCallTime("arrival retracted")
	markMetroRetraction(old)
	r.queueCheckpoint(old)
	saved, ok := r.dirty["old"]
	if !ok {
		t.Fatal("correction was not queued")
	}
	old.Train.Calls[1].OwnPrediction = &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: ptr(now.Add(time.Minute))}
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{saved}, "generation", now)
	old.Train.Calls[1].OwnPrediction = nil
	r.queueCheckpoint(old)
	if old.UncommittedRetraction {
		t.Fatal("acknowledged correction remained pinned after unrelated forecast change")
	}
	if _, ok := r.dirty["old"]; ok {
		t.Fatal("unchanged content unexpectedly queued")
	}
	r.evictRetired()
	if r.tracks["old"] != nil {
		t.Fatal("acknowledged retired track could not be evicted")
	}
}

func TestMetroRetractionOlderAcknowledgementCannotClearNewCorrection(t *testing.T) {
	now := time.Now().UTC()
	old, _ := metroPredecessorFixture(now)
	r := newMetroRuntime()
	r.archiveAvailable = true
	r.tracks["old"] = old
	old.Train.Calls[0].Arrival = missingCallTime("first correction")
	markMetroRetraction(old)
	r.queueCheckpoint(old)
	first := r.dirty["old"]
	old.Train.Calls[1].Arrival = missingCallTime("newer correction")
	markMetroRetraction(old)
	// The newer correction has not been successfully queued yet.
	r.archiveAvailable = false
	r.queueCheckpoint(old)
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{first}, "first-generation", now)
	if !old.UncommittedRetraction {
		t.Fatal("older acknowledgement cleared a newer correction")
	}
	r.evictRetired()
	if r.tracks["old"] == nil {
		t.Fatal("uncommitted correction was evicted")
	}
	r.archiveAvailable = true
	r.queueCheckpoint(old)
	second := r.dirty["old"]
	if second.Revision <= first.Revision {
		t.Fatal("newer correction was not queued")
	}
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{second}, "second-generation", now.Add(time.Second))
	if old.UncommittedRetraction {
		t.Fatal("newer acknowledged correction remained pinned")
	}
}

func TestMetroRetractionGroupAcknowledgementIgnoresTransientChange(t *testing.T) {
	now := time.Now().UTC()
	old, next := metroPredecessorFixture(now)
	r := newMetroRuntime()
	r.archiveAvailable = true
	r.tracks["old"], r.tracks["new"] = old, next
	old.Train.Calls[0].Arrival = missingCallTime("arrival retracted")
	markMetroRetraction(old)
	cached := r.predecessorCacheEntry(metroPredecessorKey{selected: "new", previous: "old", profile: "profile"}, now)
	cached.payload = &metroCheckpointPayload{Version: 1}
	r.queueLifecycleGroup(old, next)
	if cached.payload != nil {
		t.Fatal("lifecycle revision left stale predecessor cache")
	}
	batch := r.checkpointBatch(now)
	if len(batch) != 2 {
		t.Fatalf("group checkpoint not admitted: %d", len(batch))
	}
	old.Train.Reason = "transient unrelated update"
	r.commitCheckpointBatch(batch, "generation", now)
	if old.UncommittedRetraction {
		t.Fatal("committed lifecycle correction remained pinned")
	}
}

func TestMetroRetractionEquivalentCheckpointCoversNewGeneration(t *testing.T) {
	now := time.Now().UTC()
	old, _ := metroPredecessorFixture(now)
	r := newMetroRuntime()
	r.archiveAvailable = true
	r.tracks["old"] = old
	old.Train.Calls[0].Arrival = missingCallTime("arrival retracted")
	markMetroRetraction(old)
	r.queueCheckpoint(old)
	first := r.dirty["old"]
	markMetroRetraction(old) // A later correction produces the same stored state.
	r.queueCheckpoint(old)
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{first}, "generation", now)
	if old.UncommittedRetraction {
		t.Fatal("identical in-flight checkpoint did not cover newer generation")
	}
	markMetroRetraction(old)
	r.queueCheckpoint(old)
	if old.UncommittedRetraction {
		t.Fatal("identical committed checkpoint did not cover newer generation")
	}
}

func TestMetroRetractionEquivalentLifecycleCheckpointCoversNewGeneration(t *testing.T) {
	now := time.Now().UTC()
	old, next := metroPredecessorFixture(now)
	r := newMetroRuntime()
	r.archiveAvailable = true
	r.tracks["old"], r.tracks["new"] = old, next
	old.Train.Calls[0].Arrival = missingCallTime("arrival retracted")
	markMetroRetraction(old)
	r.queueLifecycleGroup(old, next)
	markMetroRetraction(old)
	r.admitLifecycleGroup(old.LifecycleGroup)
	r.commitCheckpointBatch(r.checkpointBatch(now), "generation", now)
	if old.UncommittedRetraction {
		t.Fatal("identical in-flight lifecycle checkpoint did not cover newer generation")
	}
}

func TestMetroRetractionOlderAcknowledgementCannotClearNewQueuedCorrection(t *testing.T) {
	now := time.Now().UTC()
	old, _ := metroPredecessorFixture(now)
	r := newMetroRuntime()
	r.archiveAvailable = true
	r.tracks["old"] = old
	old.Train.Calls[0].Arrival = missingCallTime("first correction")
	markMetroRetraction(old)
	r.queueCheckpoint(old)
	first := r.dirty["old"]
	old.Train.Calls[1].Arrival = missingCallTime("newer correction")
	markMetroRetraction(old)
	r.queueCheckpoint(old)
	second := r.dirty["old"]
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{first}, "first-generation", now)
	if !old.UncommittedRetraction || r.dirty["old"].Revision != second.Revision {
		t.Fatal("older acknowledgement erased the newer queued correction")
	}
	r.commitCheckpointBatch([]patterns.MetroJourneyCheckpoint{second}, "second-generation", now.Add(time.Second))
	if old.UncommittedRetraction {
		t.Fatal("latest committed correction remained pinned")
	}
}
