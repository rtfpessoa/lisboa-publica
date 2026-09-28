package app

import (
	"lisboapublica/internal/api"
	"sort"
	"time"
)

func (r *metroRuntime) current(now time.Time) []api.MetroTrain {
	out := []api.MetroTrain{}
	for _, id := range r.active {
		t := r.tracks[id]
		if t == nil || t.CommittedRevision == 0 || t.Train.SourceUpdatedAt.Before(now.AddDate(0, 0, -7)) {
			continue
		}
		if !now.Before(t.Train.ValidUntil) {
			if t.BarrierRevision == 0 {
				suspendMetroTrack(t, "Dados expirados")
			}
		}
		copy := visibleMetroTrain(t)
		copy.Persistence = metroTrainPersistence(t)
		out = append(out, copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JourneyId < out[j].JourneyId })
	return out
}
func (r *metroRuntime) retained(id string, now time.Time) (api.MetroTrain, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tracks[id]
	if !ok || t.CommittedRevision == 0 {
		return api.MetroTrain{}, false
	}
	if t.Train.SourceUpdatedAt.Before(now.AddDate(0, 0, -7)) {
		// Consultation must honor the original-age TTL even while ingestion is
		// stopped and therefore does not run the ordinary pruning pass.
		return api.MetroTrain{}, false
	}
	copy := visibleMetroTrain(t)
	copy.Persistence = metroTrainPersistence(t)
	copy.ModelProjection = nil
	copy.Association = "suspended"
	copy.NextIndex = nil
	copy.CurrentIndex = nil
	if !t.HistoricalOnly && !metroTrackClosed(t) {
		copy.Reason = "Última viagem selecionada; continuidade atual não comprovada"
	}
	return copy, true
}
