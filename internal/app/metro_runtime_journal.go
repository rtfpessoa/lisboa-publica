package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"strings"
	"time"
)

func (r *metroRuntime) queueArrival(t *metroTrack, c api.StopCall, before, after metroPoint) {
	raw, err := r.retainArrivalProof(t, c, before, after)

	if !r.archiveAvailable {
		if c.Arrival.Inferred != nil {
			c.Arrival.Inferred.Persistence = "unavailable"
		}
		return
	}
	if err != nil || len(raw) > 64<<10 || len(r.pending) >= 1024 || r.pendingBytes+r.dirtyBytes+len(raw) > 8<<20 {
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
}
func (r *metroRuntime) retainArrivalProof(t *metroTrack, c api.StopCall, before, after metroPoint) ([]byte, error) {
	if t.Proofs == nil {
		t.Proofs = map[string]metroEventProof{}
	}
	proofTrain := cloneMetroTrain(t.Train)
	proofTrain.Calls = []api.StopCall{c}
	proofTrain.NextIndex = nil
	proof := metroEventProof{Train: proofTrain, CallID: c.Id, Before: before, After: after}
	raw, err := json.Marshal(proof)
	if err == nil {
		r.storeArrivalProof(t, c.Id, proof, raw)
	}
	return raw, err
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
	r.mu.Lock()
	batch := r.eventBatch(now)
	r.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	err := history.RecordMetroEvents(batch, now)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.historyStatus = "paused"
		return
	}
	r.historyStatus = "collecting"
	for _, record := range batch {
		r.commitEvent(record)
	}
}
func (r *metroRuntime) eventBatch(now time.Time) []patterns.MetroEventRecord {
	batch := make([]patterns.MetroEventRecord, 0, len(r.pending))
	for id, v := range r.pending {
		if v.At.Before(now.AddDate(0, 0, -7)) {
			delete(r.pending, id)
			r.pendingBytes -= len(v.Payload)
			r.expireEvent(v)
			continue
		}
		batch = append(batch, v)
	}
	return batch
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
func (r *metroRuntime) commitEvent(record patterns.MetroEventRecord) {
	delete(r.pending, record.ID)
	r.pendingBytes -= len(record.Payload)
	if t := r.tracks[record.Journey]; t != nil {
		commitMetroCallEvents(t, record)
	}
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
			r.flushEvents(history, now)
			r.mu.Lock()
			r.queueCurrentCheckpoints()
			r.mu.Unlock()
			r.flushCheckpoints(ctx, history, now)
		}
	}
}
