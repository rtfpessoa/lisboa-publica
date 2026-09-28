package app

import (
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"time"
)

type metroRecoveryMiss struct {
	Result api.MetroJourneyRecovery
	Until  time.Time
}

// Recovery consultation cannot reactivate history, queue writes, renew source
// age or make the bounded hot inventory grow beyond its existing limit.
func (r *metroRuntime) retainCheckpoint(p metroCheckpointPayload, record patterns.MetroJourneyCheckpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tracks[p.Train.JourneyId]; exists {
		return
	}
	if len(r.tracks) >= 1024 {
		r.evictRetired()
	}
	if len(r.tracks) >= 1024 {
		return
	}
	r.tracks[p.Train.JourneyId] = &metroTrack{Train: cloneMetroTrain(p.Train), Profile: p.Profile, ProviderDirection: p.ProviderDirection, Codes: append([]string{}, p.Codes...), Points: map[string]metroPoint{}, Revision: record.Revision, CommittedRevision: record.Revision, Generation: record.Generation, CommittedAt: record.CommittedAt, HistoricalOnly: true}
}

// An unavailable legacy pin must not scan a week of event payloads on every
// 500 ms stream projection. The negative cache does not contain source data.
func (r *metroRuntime) recoveryMiss(id string, now time.Time) (api.MetroJourneyRecovery, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, exists := r.recoveryMisses[id]
	if !exists || !now.Before(v.Until) {
		delete(r.recoveryMisses, id)
		return api.MetroJourneyRecovery{}, false
	}
	return v.Result, true
}
func (r *metroRuntime) retainRecoveryMiss(result api.MetroJourneyRecovery, now time.Time) {
	if result.RequestedJourneyId == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recoveryMisses == nil {
		r.recoveryMisses = map[string]metroRecoveryMiss{}
	}
	for id, v := range r.recoveryMisses {
		if !now.Before(v.Until) {
			delete(r.recoveryMisses, id)
		}
	}
	if len(r.recoveryMisses) >= 1024 {
		oldest := ""
		var until time.Time
		for id, v := range r.recoveryMisses {
			if oldest == "" || v.Until.Before(until) {
				oldest, until = id, v.Until
			}
		}
		delete(r.recoveryMisses, oldest)
	}
	ttl := 5 * time.Second
	if result.Status == "unavailable" || result.Status == "expired" {
		ttl = time.Minute
	}
	r.recoveryMisses[*result.RequestedJourneyId] = metroRecoveryMiss{result, now.Add(ttl)}
}
