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
	for id := range members {
		if t := r.tracks[id]; t != nil {
			t.LifecycleGroup = group
		}
	}
	r.admitLifecycleGroup(group)
}
func (r *metroRuntime) admitLifecycleGroup(group string) {
	members := r.lifecycleGroups[group]
	records, hashes, ok := r.prepareLifecycleGroup(members)
	if !ok {
		r.failLifecycleGroup(members)
		return
	}
	if !r.lifecycleGroupChanged(hashes) {
		return
	}
	for id, record := range records {
		r.admitLifecycleCheckpoint(id, record, hashes[id])
	}
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
	if t == nil || !r.archiveAvailable {
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
	r.dirtyBytes += pendingMetroCheckpointBytes(record) - pendingMetroCheckpointBytes(r.dirty[id])
	r.dirty[id] = record
	t.Revision, t.CheckpointHash, t.CheckpointUnavailable = record.Revision, hash, false
}

func (r *metroRuntime) failLifecycleGroup(members map[string]bool) {
	for id := range members {
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
func (r *metroRuntime) acknowledgeLifecycleGroups(generation string) {
	for group, members := range r.lifecycleGroups {
		complete := true
		for id := range members {
			t := r.tracks[id]
			if t == nil || t.CommittedRevision != t.Revision || t.Generation != generation {
				complete = false
			}
		}
		if !complete {
			continue
		}
		for id := range members {
			r.tracks[id].LifecycleGroup = ""
		}
		delete(r.lifecycleGroups, group)
	}
}
func (r *metroRuntime) historyCollecting(now time.Time) {
	if r.capture.Gap || r.historyDeliveryGap {
		return
	}
	for _, t := range r.tracks {
		if t.LifecycleGroup != "" && t.CheckpointUnavailable {
			return
		}
	}
	r.historyStatus = "collecting"
}

// Expiry abandons the complete pending generation, never an expired subset.
// The explicit gap permits bounded memory reclamation after prolonged failure.
func (r *metroRuntime) expireLifecycleGroups(now time.Time) {
	for group, members := range r.lifecycleGroups {
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
	}
}
