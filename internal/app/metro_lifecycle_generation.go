package app

import (
	"lisboapublica/internal/patterns"
	"time"
)

// Membership keeps every half of a lifecycle transition in the same admitted
// archive batch, including successive transitions before a writer catches up.
func (r *metroRuntime) queueLifecycleGroup(old, next *metroTrack) {
	if r.lifecycleGroups == nil {
		r.lifecycleGroups = map[string]map[string]bool{}
	}
	group := old.LifecycleGroup
	if group == "" {
		group = old.Train.JourneyId
	}
	members := r.lifecycleGroups[group]
	if members == nil {
		members = map[string]bool{}
	}
	members[old.Train.JourneyId] = true
	members[next.Train.JourneyId] = true
	r.lifecycleGroups[group] = members
	delete(r.lifecycleAdmissions, group)
	for id := range members {
		if t := r.tracks[id]; t != nil {
			t.LifecycleGroup = group
			t.CheckpointPending = true
		}
	}
	r.admitLifecycleGroup(group)
}
func (r *metroRuntime) admitLifecycleGroup(group string) {
	members := r.lifecycleGroups[group]
	if r.lifecycleGroupFrozen(group, members) {
		return
	}
	if r.fencedLifecycleGroup(group) {
		r.pauseFencedLifecycleGroup(members)
		return
	}
	records, hashes, ok := r.prepareLifecycleGroup(members)
	if !ok {
		r.failLifecycleGroup(members)
		return
	}
	if !r.lifecycleGroupChanged(hashes) && len(r.lifecycleAdmissions[group]) == len(members) {
		for id := range hashes {
			coverMetroRetractionWithCheckpoint(r.tracks[id])
		}
		return
	}
	r.recordLifecycleGroupAdmission(group, records, hashes)
}

func (r *metroRuntime) lifecycleGroupFrozen(group string, members map[string]bool) bool {
	for id := range members {
		if r.checkpointFrozen(id) {
			r.markChangedFrozenLifecycle(group, members)
			return true
		}
	}
	return false
}

func (r *metroRuntime) pauseFencedLifecycleGroup(members map[string]bool) {
	for id := range members {
		if t := r.tracks[id]; t != nil {
			t.CheckpointUnavailable = true
		}
	}
	r.historyStatus = "paused"
}

func (r *metroRuntime) recordLifecycleGroupAdmission(group string, records map[string]patterns.MetroJourneyCheckpoint, hashes map[string]string) {
	if r.lifecycleAdmissions == nil {
		r.lifecycleAdmissions = map[string]map[string]patterns.MetroJourneyCheckpoint{}
	}
	admitted := make(map[string]patterns.MetroJourneyCheckpoint, len(records))
	for id, record := range records {
		r.admitLifecycleCheckpoint(id, record, hashes[id])
		admitted[id] = record
	}
	r.lifecycleAdmissions[group] = admitted
}

func (r *metroRuntime) markChangedFrozenLifecycle(group string, members map[string]bool) {
	changed := len(r.lifecycleAdmissions[group]) != len(members)
	for id := range members {
		t := r.tracks[id]
		if t == nil || t.CheckpointPending {
			changed = true
			continue
		}
		_, hash, ok := r.encodeCheckpoint(t)
		changed = changed || !ok || hash != t.CheckpointHash
	}
	if !changed {
		return
	}
	delete(r.lifecycleAdmissions, group)
	for id := range members {
		if t := r.tracks[id]; t != nil {
			t.CheckpointPending = true
		}
	}
}
func (r *metroRuntime) fencedLifecycleGroup(group string) bool {
	if group == "" {
		return false
	}
	for id := range r.lifecycleGroups[group] {
		if t := r.tracks[id]; t != nil && t.HistoricalOnly {
			return true
		}
	}
	return false
}
func (r *metroRuntime) prepareLifecycleGroup(members map[string]bool) (map[string]patterns.MetroJourneyCheckpoint, map[string]string, bool) {
	records := map[string]patterns.MetroJourneyCheckpoint{}
	hashes := map[string]string{}
	bytes, count := r.dirtyBytes+r.pendingBytes, len(r.dirty)
	for id := range members {
		record, hash, ok := r.prepareLifecycleCheckpoint(id)
		if !ok {
			return nil, nil, false
		}
		old := r.dirty[id]
		if old.Journey == "" {
			count++
		}
		bytes += len(record.Payload) + metroCheckpointMetadataReserve - pendingMetroCheckpointBytes(old)
		records[id], hashes[id] = record, hash
	}
	return records, hashes, bytes <= 8<<20 && count <= 1024
}
func (r *metroRuntime) prepareLifecycleCheckpoint(id string) (patterns.MetroJourneyCheckpoint, string, bool) {
	t := r.tracks[id]
	if t == nil || t.HistoricalOnly || !r.archiveAvailable {
		return patterns.MetroJourneyCheckpoint{}, "", false
	}
	raw, hash, ok := r.encodeCheckpoint(t)
	record := patterns.MetroJourneyCheckpoint{Journey: id, Revision: t.Revision + 1, SourceAt: t.Train.SourceUpdatedAt, Payload: raw}
	return record, hash, ok
}
func (r *metroRuntime) lifecycleGroupChanged(hashes map[string]string) bool {
	for id, hash := range hashes {
		if r.tracks[id].CheckpointHash != hash || r.tracks[id].CheckpointUnavailable {
			return true
		}
	}
	return false
}
func (r *metroRuntime) admitLifecycleCheckpoint(id string, record patterns.MetroJourneyCheckpoint, hash string) {
	t := r.tracks[id]
	r.invalidateMetroPredecessor(id)
	t.CheckpointRetractionGeneration = t.RetractionGeneration
	r.dirtyBytes += pendingMetroCheckpointBytes(record) - pendingMetroCheckpointBytes(r.dirty[id])
	r.dirty[id] = record
	t.Revision, t.CheckpointHash, t.CheckpointUnavailable, t.CheckpointPending = record.Revision, hash, false, false
}

func (r *metroRuntime) failLifecycleGroup(members map[string]bool) {
	for id := range members {
		if t := r.tracks[id]; t != nil {
			delete(r.lifecycleAdmissions, t.LifecycleGroup)
		}
		if r.checkpointFrozen(id) {
			continue
		}
		if record, ok := r.dirty[id]; ok {
			r.dirtyBytes -= pendingMetroCheckpointBytes(record)
			delete(r.dirty, id)
		}
		if t := r.tracks[id]; t != nil {
			t.CheckpointUnavailable = true
			t.CheckpointHash = ""
		}
	}
	r.historyStatus = "paused"
}
func (r *metroRuntime) acknowledgeLifecycleGroups(batch []patterns.MetroJourneyCheckpoint, generation string) {
	committed := make(map[string]patterns.MetroJourneyCheckpoint, len(batch))
	for _, record := range batch {
		committed[record.Journey] = record
	}
	for group, members := range r.lifecycleGroups {
		if !r.lifecycleGroupCommitted(group, members, committed, generation) {
			continue
		}
		for id := range members {
			r.tracks[id].LifecycleGroup = ""
		}
		delete(r.lifecycleGroups, group)
		delete(r.lifecycleAdmissions, group)
	}
}

func (r *metroRuntime) lifecycleGroupCommitted(group string, members map[string]bool, committed map[string]patterns.MetroJourneyCheckpoint, generation string) bool {
	admitted := r.lifecycleAdmissions[group]
	if len(admitted) != len(members) || len(members) == 0 {
		return false
	}
	for id := range members {
		want, exists := admitted[id]
		actual, present := committed[id]
		if !exists || !present || want.Revision == 0 || !sameMetroCheckpoint(actual, want) {
			return false
		}
		if !lifecycleTrackCommitted(r.tracks[id], want.Revision, generation) {
			return false
		}
	}
	return true
}

func lifecycleTrackCommitted(t *metroTrack, revision uint64, generation string) bool {
	return t != nil && !t.CheckpointPending && t.CommittedRevision == revision && t.Revision == revision && t.Generation == generation
}

func (r *metroRuntime) historyCollecting(now time.Time) {
	r.retireEventAdmissionLoss(now)
	if r.historyLanesUnavailable() || !r.historyCutReady() {
		return
	}
	for _, t := range r.tracks {
		if t.CheckpointUnavailable || t.ProofOverflow {
			return
		}
	}
	r.historyStatus = "collecting"
}

func (r *metroRuntime) historyLanesUnavailable() bool {
	return r.capture.Gap || r.historyDeliveryGap || r.historyCutMismatch ||
		r.checkpointFenced || r.checkpointRetry != nil || r.eventLaneUnavailable()
}

func (r *metroRuntime) eventLaneUnavailable() bool {
	return r.eventWriteExhausted || r.eventAdmissionLost || len(r.failedEvents) != 0
}

func (r *metroRuntime) historyCutReady() bool {
	if r.historyCut == nil {
		return true
	}
	cut := r.historyCut.Snapshot()
	return cut.Valid && !cut.Pending
}

// Expiry abandons the complete pending generation, never an expired subset.
// The explicit gap permits bounded memory reclamation after prolonged failure.
func (r *metroRuntime) expireLifecycleGroups(now time.Time) {
	for group, members := range r.lifecycleGroups {
		frozen := false
		for id := range members {
			frozen = frozen || r.checkpointFrozen(id)
		}
		if frozen {
			continue
		}
		expired := false
		for id := range members {
			t := r.tracks[id]
			if t == nil || t.Train.SourceUpdatedAt.Before(now.AddDate(0, 0, -7)) {
				expired = true
			}
		}
		if !expired {
			continue
		}
		r.failLifecycleGroup(members)
		r.markCaptureGap()
		for id := range members {
			if t := r.tracks[id]; t != nil {
				t.LifecycleGroup = ""
			}
		}
		delete(r.lifecycleGroups, group)
		delete(r.lifecycleAdmissions, group)
	}
}
