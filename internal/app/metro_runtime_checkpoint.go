package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"sort"
	"time"
)

// The checkpoint retains topology interpretation and all event proofs, but its
// point samples are history only. They are never installed into live continuity.
type metroCheckpointPayload struct {
	ModelDepartureSupport map[string]map[int64]metroModelPublication `json:"model_departure_support,omitempty"`
	Model                 *metroCheckpointModel                      `json:"model,omitempty"`
	Version               int                                        `json:"version"`
	Train                 api.MetroTrain                             `json:"train"`
	Profile               string                                     `json:"profile"`
	ProviderDirection     string                                     `json:"provider_direction"`
	Codes                 []string                                   `json:"codes"`
	Points                map[string]metroPoint                      `json:"points"`
	Proofs                map[string]metroEventProof                 `json:"proofs"`
}

// Reserve encoded identity/clock/generation overhead as part of the pending
// limit. Payload-only accounting can produce a batch the archive cannot admit.
const metroCheckpointMetadataReserve = 1024

func pendingMetroCheckpointBytes(v patterns.MetroJourneyCheckpoint) int {
	if v.Journey == "" {
		return 0
	}
	return len(v.Payload) + metroCheckpointMetadataReserve
}

func (r *metroRuntime) queueCurrentCheckpoints() {
	for group := range r.lifecycleGroups {
		r.admitLifecycleGroup(group)
	}
	for _, t := range r.tracks {
		r.queueCheckpoint(t)
	}
}
func (r *metroRuntime) queueCheckpoint(t *metroTrack) {
	if t.HistoricalOnly && !t.HistoricalCorrection || t.BarrierRevision != 0 || t.LifecycleGroup != "" {
		return
	}
	if !r.checkpointEligible(t) {
		return
	}
	r.queueEligibleCheckpoint(t)
}
func (r *metroRuntime) checkpointEligible(t *metroTrack) bool {
	if t.ProofOverflow {
		r.pauseCheckpoint(t)
		return false
	}
	if !r.archiveAvailable {
		t.CheckpointUnavailable = true
		return false
	}
	return !t.Train.SourceUpdatedAt.IsZero()
}
func (r *metroRuntime) queueEligibleCheckpoint(t *metroTrack) {

	raw, hash, ok := r.encodeCheckpoint(t)
	if !ok {
		r.pauseCheckpoint(t)
		return
	}
	if hash == t.CheckpointHash {
		return
	}
	old := r.dirty[t.Train.JourneyId]
	size := len(raw) + metroCheckpointMetadataReserve
	if !r.checkpointCapacity(old, size) {
		r.pauseCheckpoint(t)
		return
	}

	t.Revision++
	t.CheckpointUnavailable = false
	t.CheckpointHash = hash
	r.dirtyBytes += size - pendingMetroCheckpointBytes(old)
	r.dirty[t.Train.JourneyId] = patterns.MetroJourneyCheckpoint{Journey: t.Train.JourneyId, Revision: t.Revision, SourceAt: t.Train.SourceUpdatedAt, Payload: raw}
}

func (r *metroRuntime) encodeCheckpoint(t *metroTrack) ([]byte, string, bool) {
	copy := cloneMetroTrain(t.Train)
	copy.Persistence, copy.VehicleId = nil, nil
	raw, err := json.Marshal(metroCheckpointPayload{Version: 1, Train: copy, Profile: t.Profile, ProviderDirection: t.ProviderDirection, Codes: t.Codes, Points: t.Points, Proofs: t.Proofs, ModelDepartureSupport: t.ModelDepartureSupport, Model: r.checkpointModel(t)})
	if err != nil || len(raw) > (256<<10)-metroCheckpointMetadataReserve {
		return nil, "", false
	}
	return raw, fmt.Sprintf("%x", sha256.Sum256(raw)), true
}
func (r *metroRuntime) checkpointCapacity(old patterns.MetroJourneyCheckpoint, size int) bool {
	if len(r.dirty) >= 1024 && old.Journey == "" {
		return false
	}
	return r.dirtyBytes+r.pendingBytes-pendingMetroCheckpointBytes(old)+size <= 8<<20
}
func (r *metroRuntime) pauseCheckpoint(t *metroTrack) {
	t.CheckpointUnavailable = true
	r.historyStatus = "paused"
}

func (r *metroRuntime) flushCheckpoints(ctx context.Context, history *patterns.Service, now time.Time) {
	if history == nil {
		return
	}
	r.writer.Lock()
	defer r.writer.Unlock()
	r.mu.Lock()
	batch := r.checkpointBatch(now)

	r.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	generation, err := history.CommitMetroJourneys(ctx, batch, now)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.historyStatus = "paused"
		return
	}
	r.historyCollecting(now)
	r.commitCheckpointBatch(batch, generation, now)
}
func (r *metroRuntime) checkpointBatch(now time.Time) []patterns.MetroJourneyCheckpoint {
	r.expireLifecycleGroups(now)
	batch := make([]patterns.MetroJourneyCheckpoint, 0, len(r.dirty))
	for _, v := range r.dirty {
		if !v.SourceAt.Before(now.AddDate(0, 0, -7)) {
			batch = append(batch, v)
		}
	}
	sort.Slice(batch, func(i, j int) bool { return batch[i].Journey < batch[j].Journey })
	return batch
}
func (r *metroRuntime) commitCheckpointBatch(batch []patterns.MetroJourneyCheckpoint, generation string, now time.Time) {
	for _, v := range batch {
		if latest, exists := r.dirty[v.Journey]; exists && latest.Revision == v.Revision {
			r.dirtyBytes -= pendingMetroCheckpointBytes(latest)
			delete(r.dirty, v.Journey)
		}
		if t := r.tracks[v.Journey]; t != nil && v.Revision > t.CommittedRevision {
			t.CommittedRevision, t.Generation, t.CommittedAt = v.Revision, generation, now.UTC()
			r.commitLifecycle(t, v.Revision)
			commitMetroDepartureEvidence(t, v.Payload)
		}
	}
	r.acknowledgeLifecycleGroups(generation)
}

func metroTrainPersistence(t *metroTrack) *api.MetroJourneyPersistence {
	state := api.MetroJourneyPersistenceStatePending
	if t.CommittedRevision > 0 && t.CommittedRevision == t.Revision {
		state = api.MetroJourneyPersistenceStateCommitted
	}
	if t.CheckpointUnavailable {
		state = api.MetroJourneyPersistenceStateUnavailable
	}
	p := &api.MetroJourneyPersistence{State: state, Revision: int64(t.Revision), CommittedRevision: int64(t.CommittedRevision), Generation: optional(t.Generation)}
	if !t.CommittedAt.IsZero() {
		p.CommittedAt = ptr(t.CommittedAt)
	}
	return p
}

func commitMetroDepartureEvidence(t *metroTrack, raw []byte) {
	var p metroCheckpointPayload
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	for n := range t.Train.Calls {
		c := &t.Train.Calls[n]
		if n >= len(p.Train.Calls) || p.Train.Calls[n].Id != c.Id {
			continue
		}
		commitMetroDepartureCall(c, p.Train.Calls[n])
	}
}
func commitMetroDepartureCall(c *api.StopCall, stored api.StopCall) {
	if c.Departure.Inferred != nil && stored.Departure.Inferred != nil && c.Departure.Inferred.At.Equal(stored.Departure.Inferred.At) {
		c.Departure.Inferred.Persistence = "committed"
	}
	if c.DepartureRevisions == nil || stored.DepartureRevisions == nil {
		return
	}
	for k := range *c.DepartureRevisions {
		revision := &(*c.DepartureRevisions)[k]
		if k >= len(*stored.DepartureRevisions) {
			continue
		}
		if revision.Revision == (*stored.DepartureRevisions)[k].Revision && revision.Evidence != nil {
			revision.Evidence.Persistence = "committed"
		}
	}
}
