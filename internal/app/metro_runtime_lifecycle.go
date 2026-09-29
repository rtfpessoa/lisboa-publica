package app

import (
	"lisboapublica/internal/api"
	"time"
)

func metroTrackClosed(t *metroTrack) bool {
	return t.Train.Lifecycle != nil && t.Train.Lifecycle.State != "active"
}
func visibleMetroTrain(t *metroTrack) api.MetroTrain {
	if t.BarrierBefore != nil {
		copy := cloneMetroTrain(*t.BarrierBefore)
		// Preserve the committed history while withholding an unsupported
		// current link during a mandatory terminal/reversal checkpoint.
		copy.Association, copy.NextIndex, copy.CurrentIndex, copy.ModelProjection = "suspended", nil, nil, nil
		copy.Reason = "Transição de viagem a aguardar gravação; associação atual suspensa"
		copy.DirectionEvidence = &api.MetroDirectionEvidence{State: "unknown", Reason: copy.Reason}
		return copy
	}
	return cloneMetroTrain(t.Train)
}
func (r *metroRuntime) latestClosed(scope string) *metroTrack {
	var latest *metroTrack
	for _, t := range r.tracks {
		if !metroTrackClosed(t) || t.HistoricalOnly || t.Train.RouteId+"|"+t.Train.Reference != scope {
			continue
		}
		if latest == nil || t.Train.SourceUpdatedAt.After(latest.Train.SourceUpdatedAt) {
			latest = t
		}
	}
	return latest
}
func (r *metroRuntime) closeAtTerminal(t *metroTrack, before api.MetroTrain) {
	n := len(t.Train.Calls) - 1
	if n < 0 || t.Train.Calls[n].Arrival.Inferred == nil {
		return
	}
	e := t.Train.Calls[n].Arrival.Inferred
	if before.Calls[n].Arrival.Inferred != nil || t.Train.Association != "supported" {
		return
	}
	t.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "completed", At: &e.At, Reason: "Chegada inferida à estação final; regresso por confirmar"}
	t.Train.Association = "suspended"
	t.Train.Reason = t.Train.Lifecycle.Reason
	t.Train.NextIndex = nil
	t.Train.CurrentIndex = nil
	t.Train.ModelProjection = nil
	r.stageLifecycle(t, before)
	for key, id := range r.active {
		if id == t.Train.JourneyId {
			delete(r.active, key)
		}
	}
}

// Freeze mandatory revisions until their generation commits. Later source
// progress cannot coalesce away a closure or one half of a handoff.
func (r *metroRuntime) stageLifecycle(t *metroTrack, before api.MetroTrain) bool {
	// Admission and lifecycle transitions are atomic in memory; disk is asynchronous.
	if metroTrackClosed(t) {
		t.Train.Association = "suspended"
		t.Train.NextIndex = nil
		t.Train.CurrentIndex = nil
		t.Train.ModelProjection = nil
	}
	r.queueCheckpoint(t)
	return true
}
func (r *metroRuntime) commitLifecycle(t *metroTrack, revision uint64) {
	if t.BarrierRevision == 0 || t.BarrierRevision != revision {
		return
	}
	t.BarrierBefore = nil
	t.BarrierRevision = 0
	if !metroTrackClosed(t) {
		return
	}
	for key, id := range r.active {
		if id == t.Train.JourneyId {
			delete(r.active, key)
		}
	}
	if t.PendingSuccessor != "" {
		for key, id := range r.candidates {
			if id == t.PendingSuccessor {
				r.active[key] = id
				delete(r.candidates, key)
			}
		}
		t.PendingSuccessor = ""
	}
}
func (r *metroRuntime) stageSuccessor(old, next *metroTrack, at, first time.Time) bool {
	setMetroSuccessorLifecycle(metroSuccessorLifecycle{old: old, next: next, at: at, first: first})
	r.handoffOperational(old, next, at)
	for key, id := range r.candidates {
		if id == next.Train.JourneyId {
			r.active[key] = id
			delete(r.candidates, key)
		}
	}
	return true
}

type metroSuccessorLifecycle struct {
	old, next *metroTrack
	at, first time.Time
}

func setMetroSuccessorLifecycle(v metroSuccessorLifecycle) {
	state := api.Superseded
	var at *time.Time
	if v.old.Train.Lifecycle != nil {
		at = v.old.Train.Lifecycle.At
	}
	if metroTrackClosed(v.old) {
		state = v.old.Train.Lifecycle.State
	}
	if at == nil {
		at = &v.at
	}
	v.old.Train.Lifecycle = &api.MetroJourneyLifecycle{State: state, Reason: "Movimento inverso confirmado; nova viagem admitida", At: at, SuccessorJourneyId: &v.next.Train.JourneyId, FirstMovementAt: &v.first, DirectionConfirmedAt: &v.at}
	v.old.Train.Association = "suspended"
	v.old.Train.NextIndex = nil
	v.old.Train.CurrentIndex = nil
	v.old.Train.ModelProjection = nil
	v.old.Train.Reason = v.old.Train.Lifecycle.Reason
	v.next.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active", Reason: "Viagem sucessora confirmada", PredecessorJourneyId: &v.old.Train.JourneyId, FirstMovementAt: &v.first, DirectionConfirmedAt: &v.at}
}

func (r *metroRuntime) cancelLifecycle(t *metroTrack, before api.MetroTrain) {
	if v, ok := r.dirty[t.Train.JourneyId]; ok {
		r.dirtyBytes -= pendingMetroCheckpointBytes(v)
		delete(r.dirty, t.Train.JourneyId)
	}
	t.Train = before
	t.BarrierBefore = nil
	t.BarrierRevision = 0
	t.CheckpointHash = ""
	suspendMetroTrack(t, "Transição atómica incompleta; associação atual suspensa")
	r.queueCheckpoint(t)
}

// Called under the runtime owner lock: observers see one complete handoff.
func (r *metroRuntime) handoffOperational(old, next *metroTrack, now time.Time) {
	at := now
	if !metroTrackClosed(old) {
		old.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "superseded", At: &at, Reason: "Novo episódio operacional estimado"}
	}
	old.Train.Lifecycle.SuccessorJourneyId = &next.Train.JourneyId
	old.Train.Association = "suspended"
	old.Train.NextIndex = nil
	old.Train.CurrentIndex = nil
	old.Train.ModelProjection = nil
	next.Train.Lifecycle.PredecessorJourneyId = &old.Train.JourneyId
	for key, id := range r.active {
		if id == old.Train.JourneyId {
			delete(r.active, key)
		}
	}
	r.queueLifecycleGroup(old, next)
}
