package app

import (
	"sort"
	"strings"
	"time"
)

// Successors are private candidates. Forecast coexistence never selects one;
// admission requires the same fixed-axis three-source-position confirmation.
func (r *metroRuntime) observeSuccessorCandidates(b *metroPointBatch, data *MetroData, static *StaticData, now time.Time) {
	keys := []string{}
	for key := range b.groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		r.observeSuccessor(metroSuccessorObservation{key: key, batch: b, data: data, static: static, now: now})
	}
}

type metroSuccessorObservation struct {
	key    string
	batch  *metroPointBatch
	data   *MetroData
	static *StaticData
	now    time.Time
}

func (r *metroRuntime) observeSuccessor(v metroSuccessorObservation) {
	if r.active[v.key] != "" {
		return
	}
	if !r.successorHasWait(v) {
		return
	}
	path := v.batch.paths[v.key]
	parts := strings.Split(v.key, "|")
	ref := parts[len(parts)-1]
	old := r.predecessor(path.Route + "|" + ref)
	if old == nil || old.BarrierRevision != 0 || old.ProviderDirection == path.Direction {
		return
	}
	if _, ok := r.models[r.topology.Profile+"|"+path.Direction]; !ok {
		return
	}
	next := r.successorCandidate(v, ref)
	if next == nil {
		return
	}
	r.applyPoints(next, v.batch.groups[v.key], v.now)
	if confirmedMetroSuccessor(old, next) {
		e := next.Train.DirectionEvidence
		r.stageSuccessor(old, next, *e.ConfirmedAt, *e.FirstMovementAt)
	}
}

func (r *metroRuntime) successorHasWait(v metroSuccessorObservation) bool {
	current, conflict := latestMetroPoints(v.batch.groups[v.key])
	if !v.batch.rejected[v.key] && !conflict && metroContextHasWait(current) {
		return true
	}
	if candidate := r.tracks[r.candidates[v.key]]; candidate != nil {
		r.suspendMetroCandidate(candidate, "Sem dados atuais para confirmar a viagem")
	}
	return false
}

func (r *metroRuntime) successorCandidate(v metroSuccessorObservation, ref string) *metroTrack {
	if t := r.tracks[r.candidates[v.key]]; t != nil {
		latest := latestMetroWaitClock(v.batch.groups[v.key], t.Train.SourceUpdatedAt)
		valid := !metroTrackClosed(t) && t.Profile == r.topology.Profile && v.now.Before(t.Train.ValidUntil) && latest.Sub(t.Train.SourceUpdatedAt) <= 60*time.Second
		if r.candidateInFlight(t) {
			t.CandidateInterrupted = t.CandidateInterrupted || !valid
			return nil
		}
		if valid && !t.CandidateInterrupted {
			r.updateTrackDirection(t, v.batch.paths[v.key])
			return t
		}
		r.retireMetroCandidate(v.key, t)
	}
	t := r.startTrack(metroTrackStart{key: v.key, reference: ref, path: v.batch.paths[v.key], data: v.data, static: v.static, now: v.now})
	if t != nil {
		delete(r.active, v.key)
		r.candidates[v.key] = t.Train.JourneyId
	}
	return t
}

// A private candidate cannot carry transition evidence across a profile change.
func (r *metroRuntime) fenceMetroCandidateProfiles() {
	for key, id := range r.candidates {
		if t := r.tracks[id]; t != nil {
			t.CandidateInterrupted = t.CandidateInterrupted || t.Profile != r.topology.Profile
			if t.CandidateInterrupted && !r.candidateInFlight(t) {
				r.retireMetroCandidate(key, t)
			}
		}
	}
}

func (r *metroRuntime) suspendMetroCandidate(t *metroTrack, reason string) {
	if r.candidateInFlight(t) {
		t.CandidateInterrupted = true
		return
	}
	suspendMetroTrack(t, reason)
}

func (r *metroRuntime) candidateInFlight(t *metroTrack) bool {
	if t.BarrierRevision != 0 || t.LifecycleGroup != "" {
		return true
	}
	for _, old := range r.tracks {
		if old != nil && old.PendingSuccessor != "" && old.PendingSuccessor == t.Train.JourneyId {
			return true
		}
	}
	return false
}

func (r *metroRuntime) retireMetroCandidate(key string, t *metroTrack) {
	suspendMetroTrack(t, "Continuidade interrompida")
	delete(r.candidates, key)
}
func confirmedMetroSuccessor(old, next *metroTrack) bool {
	e := next.Train.DirectionEvidence
	if e == nil || e.State != "confirmed" {
		return false
	}
	if e.ConfirmedAt == nil || e.FirstMovementAt == nil {
		return false
	}
	return !e.ConfirmedAt.Before(old.Train.SourceUpdatedAt)
}

func (r *metroRuntime) predecessor(scope string) *metroTrack {
	for _, id := range r.active {
		if t := r.tracks[id]; t != nil && t.Train.RouteId+"|"+t.Train.Reference == scope {
			return t
		}
	}
	return r.latestClosed(scope)
}
