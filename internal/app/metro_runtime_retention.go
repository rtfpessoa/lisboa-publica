package app

import (
	"lisboapublica/internal/api"
	"time"
)

func (r *metroRuntime) prune(now time.Time) {
	r.expireLifecycleGroups(now)
	pruneMetroContexts(r.active, r.tracks, now)
	pruneMetroContexts(r.candidates, r.tracks, now)
	for id, t := range r.tracks {
		if now.Sub(t.Train.SourceUpdatedAt) > 7*24*time.Hour && t.LifecycleGroup == "" {
			r.forgetTrack(id)
		}
	}
}
func pruneMetroContexts(contexts map[string]string, tracks map[string]*metroTrack, now time.Time) {
	for key, id := range contexts {
		if t := tracks[id]; t == nil || now.After(t.Train.ValidUntil) {
			delete(contexts, key)
		}
	}
}
func (r *metroRuntime) forgetTrack(id string) {
	if pending, exists := r.dirty[id]; exists {
		r.dirtyBytes -= pendingMetroCheckpointBytes(pending)
		delete(r.dirty, id)
	}
	delete(r.tracks, id)
	for key, v := range r.active {
		if v == id {
			delete(r.active, key)
		}
	}
}

func cloneMetroTrain(t api.MetroTrain) api.MetroTrain {
	if t.Lifecycle != nil {
		copy := *t.Lifecycle
		t.Lifecycle = &copy
	}
	if t.DirectionEvidence != nil {
		copy := *t.DirectionEvidence
		t.DirectionEvidence = &copy
	}
	t.Calls = append([]api.StopCall{}, t.Calls...)
	for n := range t.Calls {
		c := &t.Calls[n]
		if c.DepartureRevisions != nil {
			copy := append([]api.MetroDepartureRevision{}, (*c.DepartureRevisions)...)
			for k := range copy {
				if copy[k].Evidence != nil {
					e := *copy[k].Evidence
					copy[k].Evidence = &e
				}
			}
			c.DepartureRevisions = &copy
		}
		if c.Arrival.Inferred != nil {
			e := *c.Arrival.Inferred
			c.Arrival.Inferred = &e
		}
		if c.Departure.Inferred != nil {
			e := *c.Departure.Inferred
			c.Departure.Inferred = &e
		}
	}
	return t
}

func (r *metroRuntime) evictRetired() {
	active := map[string]bool{}
	for _, id := range r.candidates {
		active[id] = true
	}
	for _, id := range r.active {
		active[id] = true
	}
	for _, record := range r.pending {
		active[record.Journey] = true
	}
	for id := range r.dirty {
		active[id] = true
	}
	var oldest *metroTrack
	for _, t := range r.tracks {
		if active[t.Train.JourneyId] || t.LifecycleGroup != "" {
			continue
		}
		if oldest == nil || t.Train.SourceUpdatedAt.Before(oldest.Train.SourceUpdatedAt) {
			oldest = t
		}
	}
	if oldest != nil {
		delete(r.tracks, oldest.Train.JourneyId)
	}
}
