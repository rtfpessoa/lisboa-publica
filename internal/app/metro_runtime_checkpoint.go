package app

import (
	"bytes"
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
	ForecastCut           *metroCutProvenance                        `json:"forecast_cut,omitempty"`
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
	if t.HistoricalOnly || t.BarrierRevision != 0 || t.LifecycleGroup != "" {
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
	if t.HistoricalOnly {
		t.CheckpointUnavailable = true
		return
	}
	if r.checkpointFrozen(t.Train.JourneyId) {
		t.CheckpointPending = true
		return
	}

	raw, hash, ok := r.encodeCheckpoint(t)
	if !ok {
		r.pauseCheckpoint(t)
		return
	}
	if hash == t.CheckpointHash {
		t.CheckpointPending = false
		coverMetroRetractionWithCheckpoint(t)
		return
	}
	old := r.dirty[t.Train.JourneyId]
	size := len(raw) + metroCheckpointMetadataReserve
	if !r.checkpointCapacity(old, size) {
		r.pauseCheckpoint(t)
		return
	}

	r.invalidateMetroPredecessor(t.Train.JourneyId)
	t.Revision++
	t.CheckpointRetractionGeneration = t.RetractionGeneration
	t.CheckpointUnavailable = false
	t.CheckpointPending = false
	t.CheckpointHash = hash
	r.dirtyBytes += size - pendingMetroCheckpointBytes(old)
	r.dirty[t.Train.JourneyId] = patterns.MetroJourneyCheckpoint{Journey: t.Train.JourneyId, Revision: t.Revision, SourceAt: t.Train.SourceUpdatedAt, Payload: raw}
}

// The queued or committed payload already represents this exact state.
func coverMetroRetractionWithCheckpoint(t *metroTrack) {
	t.CheckpointRetractionGeneration = t.RetractionGeneration
	if t.CommittedRevision == t.Revision && t.CommittedRevision > 0 {
		t.UncommittedRetraction = false
	}
}

func (r *metroRuntime) encodeCheckpoint(t *metroTrack) ([]byte, string, bool) {
	return r.encodeCheckpointWithClaim(t, r.claimMetroSnapshot)
}
func (r *metroRuntime) encodeCheckpointWithClaim(t *metroTrack, claim func(patterns.MetroCutSnapshot) bool) ([]byte, string, bool) {
	cut := r.historySnapshot()
	raw, err := r.marshalMetroCheckpoint(t, cut, false)
	if err == nil && !claim(cut) {
		raw, err = r.marshalMetroCheckpoint(t, patterns.MetroCutSnapshot{}, true)
	}
	if err != nil || len(raw) > (256<<10)-metroCheckpointMetadataReserve {
		return nil, "", false
	}
	return raw, fmt.Sprintf("%x", sha256.Sum256(raw)), true
}
func (r *metroRuntime) marshalMetroCheckpoint(t *metroTrack, cut patterns.MetroCutSnapshot, safe bool) ([]byte, error) {
	copy := r.snapshotMetroTrack(t, cut, safe, time.Now().UTC()).Train
	copy.Persistence, copy.VehicleId = nil, nil
	// A pending cut cannot authorize historical forecasts. Omitting its transient
	// revision keeps unchanged checkpoints stable while losses continue.
	var provenance *metroCutProvenance
	if !safe && !cut.Pending {
		provenance = metroCutOrigin(cut)
	}
	return json.Marshal(metroCheckpointPayload{ForecastCut: provenance, Version: 1, Train: copy, Profile: t.Profile, ProviderDirection: t.ProviderDirection, Codes: t.Codes, Points: t.Points, Proofs: t.Proofs, ModelDepartureSupport: t.ModelDepartureSupport, Model: r.checkpointModel(t)})
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
	r.flushCheckpointsWithCommit(ctx, history, now, history.CommitMetroJourneysGuarded)
}

func (r *metroRuntime) flushCheckpointsWithCommit(ctx context.Context, history *patterns.Service, now time.Time, commit func(context.Context, []patterns.MetroJourneyCheckpoint, time.Time, *patterns.MetroCheckpointAttempt) (patterns.MetroCheckpointCommit, error)) {
	r.writer.Lock()
	defer r.writer.Unlock()
	batch, retry := r.checkpointBatchForFlush(now)
	if len(batch) == 0 {
		return
	}
	if retry != nil && metroCheckpointBatchExpired(batch, now) {
		r.abandonExpiredCheckpoint(history, batch, retry, now)
		return
	}
	result, err := commit(ctx, batch, now, retry)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.failCheckpointBatch(batch, result)
		return
	}
	r.commitCheckpointBatch(batch, result.Generation, now)
	r.checkpointPending, r.checkpointRetry = nil, nil
	r.historyCollecting(now)
}

func (r *metroRuntime) checkpointBatchForFlush(now time.Time) ([]patterns.MetroJourneyCheckpoint, *patterns.MetroCheckpointAttempt) {
	r.mu.Lock()
	if r.checkpointFenced {
		r.mu.Unlock()
		return nil, nil
	}
	if r.checkpointPending == nil {
		r.checkpointPending = r.checkpointBatch(now)
	}
	batch, retry := r.checkpointPending, r.checkpointRetry
	r.mu.Unlock()
	if len(batch) == 0 {
		r.mu.Lock()
		r.checkpointPending = nil
		r.mu.Unlock()
	}
	return batch, retry
}

func (r *metroRuntime) abandonExpiredCheckpoint(history *patterns.Service, batch []patterns.MetroJourneyCheckpoint, retry *patterns.MetroCheckpointAttempt, now time.Time) {
	err := history.AbandonMetroCheckpointAttempt(retry)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.checkpointFenced = true
		r.historyStatus = "paused"
		return
	}
	r.checkpointPending, r.checkpointRetry = nil, nil
	r.expireCheckpointAttempt(batch, now)
}

func (r *metroRuntime) failCheckpointBatch(batch []patterns.MetroJourneyCheckpoint, result patterns.MetroCheckpointCommit) {
	r.historyStatus = "paused"
	if result.Retryable && result.Retry != nil {
		r.checkpointRetry = result.Retry
		return
	}
	r.checkpointFenced = true
	for _, record := range batch {
		if t := r.tracks[record.Journey]; t != nil {
			t.CheckpointUnavailable = true
		}
	}
}
func metroCheckpointBatchExpired(batch []patterns.MetroJourneyCheckpoint, now time.Time) bool {
	for _, record := range batch {
		if record.SourceAt.Before(now.AddDate(0, 0, -7)) {
			return true
		}
	}
	return false
}
func (r *metroRuntime) expireCheckpointAttempt(batch []patterns.MetroJourneyCheckpoint, now time.Time) {
	for _, record := range batch {
		if !record.SourceAt.Before(now.AddDate(0, 0, -7)) {
			continue
		}
		if latest, ok := r.dirty[record.Journey]; ok && latest.Revision == record.Revision && latest.SourceAt.Equal(record.SourceAt) && bytes.Equal(latest.Payload, record.Payload) {
			r.dirtyBytes -= pendingMetroCheckpointBytes(latest)
			delete(r.dirty, record.Journey)
		}
		if t := r.tracks[record.Journey]; t != nil {
			t.CheckpointUnavailable, t.CheckpointHash = true, ""
		}
	}
	r.historyStatus = "paused"
	r.markCaptureGap()
	r.expireLifecycleGroups(now)
}
func (r *metroRuntime) checkpointFrozen(id string) bool {
	for _, record := range r.checkpointPending {
		if record.Journey == id {
			return true
		}
	}
	return false
}
func (r *metroRuntime) checkpointBatch(now time.Time) []patterns.MetroJourneyCheckpoint {
	r.expireLifecycleGroups(now)
	batch := make([]patterns.MetroJourneyCheckpoint, 0, len(r.dirty))
	for _, v := range r.dirty {
		if t := r.tracks[v.Journey]; t != nil && (t.HistoricalOnly || r.fencedLifecycleGroup(t.LifecycleGroup)) {
			continue
		}
		if !v.SourceAt.Before(now.AddDate(0, 0, -7)) {
			batch = append(batch, v)
		}
	}
	sort.Slice(batch, func(i, j int) bool { return batch[i].Journey < batch[j].Journey })
	return batch
}
func (r *metroRuntime) commitCheckpointBatch(batch []patterns.MetroJourneyCheckpoint, generation string, now time.Time) {
	for _, record := range batch {
		r.commitCheckpoint(record, generation, now)
	}
	r.acknowledgeLifecycleGroups(batch, generation)
}

func sameMetroCheckpoint(a, b patterns.MetroJourneyCheckpoint) bool {
	return a.Revision == b.Revision && a.SourceAt.Equal(b.SourceAt) && bytes.Equal(a.Payload, b.Payload)
}

func (r *metroRuntime) commitCheckpoint(record patterns.MetroJourneyCheckpoint, generation string, now time.Time) {
	t := r.tracks[record.Journey]
	if t != nil && t.HistoricalOnly {
		return
	}
	if !r.removeMatchingDirtyCheckpoint(record) || t == nil || record.Revision <= t.CommittedRevision {
		return
	}
	r.markCheckpointCommitted(t, record, generation, now)
}

// A newer dirty revision remains queued; a conflicting same-revision payload
// must never be acknowledged as the committed batch.
func (r *metroRuntime) removeMatchingDirtyCheckpoint(record patterns.MetroJourneyCheckpoint) bool {
	latest, exists := r.dirty[record.Journey]
	if !exists || latest.Revision != record.Revision {
		return true
	}
	if !sameMetroCheckpoint(latest, record) {
		return false
	}
	r.dirtyBytes -= pendingMetroCheckpointBytes(latest)
	delete(r.dirty, record.Journey)
	return true
}

func (r *metroRuntime) markCheckpointCommitted(t *metroTrack, record patterns.MetroJourneyCheckpoint, generation string, now time.Time) {
	if record.Revision == t.Revision {
		_, hash, ok := r.encodeCheckpoint(t)
		t.CheckpointPending = !ok || hash != t.CheckpointHash
		if t.CheckpointRetractionGeneration == t.RetractionGeneration {
			t.UncommittedRetraction = false
		}
	}
	r.invalidateMetroPredecessor(record.Journey)
	t.CommittedRevision, t.Generation, t.CommittedAt = record.Revision, generation, now.UTC()
	r.commitLifecycle(t, record.Revision)
	commitMetroDepartureEvidence(t, record.Payload)
}

func metroTrainPersistence(t *metroTrack) *api.MetroJourneyPersistence {
	state := api.MetroJourneyPersistenceStatePending
	if t.CommittedRevision > 0 && t.CommittedRevision == t.Revision && !t.CheckpointPending && t.LifecycleGroup == "" {
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
