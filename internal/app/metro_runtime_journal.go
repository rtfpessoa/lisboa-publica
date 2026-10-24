package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"strings"
	"time"
)

type metroEventFailure struct {
	Record  patterns.MetroEventRecord
	Attempt uint64
}

func (r *metroRuntime) queueArrival(t *metroTrack, c api.StopCall, before, after metroPoint) {
	r.queueArrivalWithClaim(t, c, before, after, r.claimMetroSnapshot)
}
func (r *metroRuntime) queueArrivalWithClaim(t *metroTrack, c api.StopCall, before, after metroPoint, claim func(patterns.MetroCutSnapshot) bool) {
	raw, err := r.retainArrivalProofWithClaim(t, c, before, after, claim)

	if !r.archiveAvailable {
		r.markEventAdmissionLoss(after.Clock)
		if c.Arrival.Inferred != nil {
			c.Arrival.Inferred.Persistence = "unavailable"
		}
		return
	}
	if err != nil || len(raw) > 64<<10 || len(r.pending) >= 1024 || r.pendingBytes+r.dirtyBytes+len(raw) > 8<<20 {
		r.markEventAdmissionLoss(after.Clock)
		if c.Arrival.Inferred != nil {
			c.Arrival.Inferred.Persistence = "unavailable"
		}
		r.historyStatus = "paused"
		return
	}
	record := patterns.MetroEventRecord{ID: fmt.Sprintf("%s:arrival:%x", c.Id, sha256.Sum256(raw)), Journey: t.Train.JourneyId, At: after.Clock, Payload: raw}
	if old, exists := r.pending[record.ID]; exists {
		r.pendingBytes -= len(old.Payload)
	}
	r.pendingBytes += len(raw)
	r.pending[record.ID] = record
	if c.Arrival.Inferred == nil {
		if r.pendingCorrections == nil {
			r.pendingCorrections = map[string]bool{}
		}
		r.pendingCorrections[record.ID] = true
	}
}
func (r *metroRuntime) markEventAdmissionLoss(at time.Time) {
	r.eventAdmissionLost = true
	if at.IsZero() {
		r.eventLossUnknown = true
	} else if at.After(r.eventAdmissionAt) {
		r.eventAdmissionAt = at
	}
}
func (r *metroRuntime) retireEventAdmissionLoss(now time.Time) bool {
	if !r.eventAdmissionLost || r.eventLossUnknown || !r.eventAdmissionAt.Before(now.AddDate(0, 0, -7)) {
		return false
	}
	r.eventAdmissionLost, r.eventAdmissionAt = false, time.Time{}
	return true
}
func (r *metroRuntime) retainArrivalProofWithClaim(t *metroTrack, c api.StopCall, before, after metroPoint, claim func(patterns.MetroCutSnapshot) bool) ([]byte, error) {
	cut := r.historySnapshot()
	proof := r.arrivalProofCandidate(t, c, before, after, cut, false)
	raw, err := json.Marshal(proof)
	if err == nil && !claim(cut) {
		proof = r.arrivalProofCandidate(t, c, before, after, patterns.MetroCutSnapshot{}, true)
		raw, err = json.Marshal(proof)
	}
	if err == nil {
		if t.Proofs == nil {
			t.Proofs = map[string]metroEventProof{}
		}
		r.storeArrivalProof(t, c.Id, proof, raw)
	}
	return raw, err
}
func (r *metroRuntime) arrivalProofCandidate(t *metroTrack, c api.StopCall, before, after metroPoint, cut patterns.MetroCutSnapshot, safe bool) metroEventProof {
	copy := r.snapshotMetroTrack(t, cut, safe, time.Now().UTC())
	call := c
	found := false
	for _, candidate := range copy.Train.Calls {
		if candidate.Id == c.Id {
			call = candidate
			found = true
			break
		}
	}
	if !found {
		call.OwnPrediction, call.OwnDeparturePrediction = nil, nil
	}
	copy.Train.Calls = []api.StopCall{call}
	copy.Train.NextIndex = nil
	return metroEventProof{ForecastCut: metroCutOrigin(cut), Train: copy.Train, CallID: c.Id, Before: before, After: after}
}
func (r *metroRuntime) storeArrivalProof(t *metroTrack, call string, proof metroEventProof, raw []byte) {
	id := fmt.Sprintf("%s:arrival:%x", call, sha256.Sum256(raw))
	if _, exists := t.Proofs[id]; exists {
		return
	}
	if t.ProofBytes+len(raw) > 256<<10 || len(t.Proofs) >= 1024 {
		t.ProofOverflow, t.CheckpointUnavailable = true, true
		r.historyStatus = "paused"
		return
	}
	t.Proofs[id] = proof
	t.ProofBytes += len(raw)
}

func (r *metroRuntime) flushEvents(history *patterns.Service, now time.Time) {
	if history == nil {
		return
	}
	r.flushEventsWithWrite(now, history.RecordMetroEvents)
}
func (r *metroRuntime) flushEventsWithWrite(now time.Time, write func([]patterns.MetroEventRecord, time.Time) error) {
	r.mu.Lock()
	batch, retired := r.eventBatch(now)
	if retired {
		r.historyCollecting(now)
	}
	if len(batch) == 0 {
		r.mu.Unlock()
		return
	}
	if r.eventWriteSequence == ^uint64(0) {
		r.eventWriteExhausted, r.historyStatus = true, "paused"
		r.mu.Unlock()
		return
	}
	r.eventWriteSequence++
	attempt := r.eventWriteSequence
	r.mu.Unlock()
	err := write(batch, now)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.failEventBatch(batch, attempt)
		return
	}
	for _, record := range batch {
		r.commitEvent(record, attempt)
	}
	r.historyCollecting(now)
}
func (r *metroRuntime) failEventBatch(batch []patterns.MetroEventRecord, attempt uint64) {
	for _, record := range batch {
		if pending, ok := r.pending[record.ID]; ok && samePendingEvent(pending, record) {
			if r.failedEvents == nil {
				r.failedEvents = map[string]metroEventFailure{}
			}
			if failed, exists := r.failedEvents[record.ID]; !exists || failed.Attempt < attempt {
				r.failedEvents[record.ID] = metroEventFailure{Record: record, Attempt: attempt}
			}
			r.historyStatus = "paused"
		}
	}
}
func (r *metroRuntime) eventBatch(now time.Time) ([]patterns.MetroEventRecord, bool) {
	batch := make([]patterns.MetroEventRecord, 0, len(r.pending))
	retired := false
	for id, v := range r.pending {
		if v.At.Before(now.AddDate(0, 0, -7)) {
			delete(r.pending, id)
			delete(r.pendingCorrections, id)
			r.pendingBytes -= len(v.Payload)
			r.expireEvent(v)
			retired = r.clearFailedEvent(v) || retired
			continue
		}
		if r.pendingCorrections[id] {
			t := r.tracks[v.Journey]
			if t == nil || t.UncommittedRetraction {
				continue
			}
		}
		batch = append(batch, v)
	}
	return batch, retired
}
func (r *metroRuntime) expireEvent(v patterns.MetroEventRecord) {
	if t := r.tracks[v.Journey]; t != nil {
		for n := range t.Train.Calls {
			if event := t.Train.Calls[n].Arrival.Inferred; event != nil && event.WindowEnd.Equal(v.At) {
				event.Persistence = "unavailable"
			}
		}
	}
}
func (r *metroRuntime) commitEvent(record patterns.MetroEventRecord, attempt uint64) {
	latest, exists := r.pending[record.ID]
	if !exists || !samePendingEvent(latest, record) {
		return
	}
	if failed, ok := r.failedEvents[record.ID]; ok && samePendingEvent(failed.Record, record) && failed.Attempt > attempt {
		return
	}
	delete(r.pending, record.ID)
	delete(r.pendingCorrections, record.ID)
	r.pendingBytes -= len(record.Payload)
	r.clearFailedEvent(record)
	if t := r.tracks[record.Journey]; t != nil {
		commitMetroCallEvents(t, record)
	}
}
func samePendingEvent(a, b patterns.MetroEventRecord) bool {
	return a.ID == b.ID && a.Journey == b.Journey && a.At.Equal(b.At) && bytes.Equal(a.Payload, b.Payload)
}
func (r *metroRuntime) clearFailedEvent(record patterns.MetroEventRecord) bool {
	if failed, ok := r.failedEvents[record.ID]; ok && samePendingEvent(failed.Record, record) {
		delete(r.failedEvents, record.ID)
		return true
	}
	return false
}
func commitMetroCallEvents(t *metroTrack, record patterns.MetroEventRecord) {
	for n := range t.Train.Calls {
		c := &t.Train.Calls[n]
		if strings.HasPrefix(record.ID, c.Id+":arrival:") && c.Arrival.Inferred != nil && c.Arrival.Inferred.WindowEnd.Equal(record.At) {
			c.Arrival.Inferred.Persistence = "committed"
		}
	}
}
func (r *metroRuntime) runJournal(ctx context.Context, history *patterns.Service) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer r.flushEvents(history, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.flushInputs(ctx, history, now)
			r.flushEvents(history, now)
			r.mu.Lock()
			r.queueCurrentCheckpoints()
			r.mu.Unlock()
			r.flushCheckpoints(ctx, history, now)
		}
	}
}
