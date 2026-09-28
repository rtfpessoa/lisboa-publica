package app

import (
	"lisboapublica/internal/api"
	"sort"
	"time"
)

func (r *metroRuntime) prune(now time.Time) {
	for key, id := range r.active {
		if t := r.tracks[id]; t == nil || now.After(t.Train.ValidUntil) {
			delete(r.active, key)
		}
	}
	for id, t := range r.tracks {
		if now.Sub(t.Train.SourceUpdatedAt) > 7*24*time.Hour {
			delete(r.tracks, id)
			for key, v := range r.active {
				if v == id {
					delete(r.active, key)
				}
			}
		}
	}
}
func cloneMetroTrain(t api.MetroTrain) api.MetroTrain {
	t.Calls = append([]api.StopCall{}, t.Calls...)
	for n := range t.Calls {
		c := &t.Calls[n]
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
func (r *metroRuntime) current(now time.Time) []api.MetroTrain {
	out := []api.MetroTrain{}
	for _, id := range r.active {
		t := r.tracks[id]
		if t == nil {
			continue
		}
		if !now.Before(t.Train.ValidUntil) {
			suspendMetroTrack(t, "Dados expirados")
		}
		out = append(out, cloneMetroTrain(t.Train))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JourneyId < out[j].JourneyId })
	return out
}
func (r *metroRuntime) retained(id string) (api.MetroTrain, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tracks[id]
	if !ok {
		return api.MetroTrain{}, false
	}
	copy := cloneMetroTrain(t.Train)
	copy.Association = "suspended"
	copy.NextIndex = nil
	copy.CurrentIndex = nil
	copy.Reason = "Última viagem selecionada; continuidade atual não comprovada"
	return copy, true
}
func (r *metroRuntime) evictRetired() {
	active := map[string]bool{}
	for _, id := range r.active {
		active[id] = true
	}
	for _, record := range r.pending {
		active[record.Journey] = true
	}
	var oldest *metroTrack
	for _, t := range r.tracks {
		if active[t.Train.JourneyId] {
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
