package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"lisboapublica/internal/patterns"
)

type metroInputCapture struct {
	Queue            []patterns.MetroEventRecord
	Bytes            int
	Fingerprint      string
	Gap              bool
	GapVersion       uint64
	GapQueuedVersion uint64
	Sequence         uint64
	Failures         int
	RetryAt          time.Time
}
type metroCapturedModel struct {
	Generation              uint64
	Position                hubPosition
	PublishedAt, ReceivedAt time.Time
	UnderlyingInputAt       *time.Time
}
type metroCapturedInput struct {
	WaitAvailability string
	WaitError        string
	HubError         string
	Version          int
	Profile          string
	Plan             string
	ReceivedAt       time.Time
	Sequence         uint64
	GapBefore        bool
	GapVersion       uint64
	Waits            []MetroWait
	Models           []metroCapturedModel
	Corrections      []hubPosition
}

func (r *metroRuntime) captureChanged(now time.Time) {
	if !r.archiveAvailable || r.publication == nil {
		return
	}
	models := r.capturedModels()
	waits := metroCanonicalWaits(r.publication.Waits)
	fingerprint := r.captureFingerprint(waits, models)
	if !r.captureNeedsPublication(fingerprint) {
		return
	}
	r.capture.Sequence++
	input := r.capturedInput(now, waits, models)
	raw, _ := json.Marshal(input)
	r.admitCapturedInput(input, raw, fingerprint, now)
}
func (r *metroRuntime) capturedModels() []metroCapturedModel {
	keys := []string{}
	for key := range r.operational {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	models := []metroCapturedModel{}
	for _, key := range keys {
		m := r.operational[key]
		models = append(models, metroCapturedModel{Generation: m.Generation, Position: m.Position, PublishedAt: m.PublishedAt, ReceivedAt: m.ReceivedAt})
	}
	return models
}
func (r *metroRuntime) captureFingerprint(waits []MetroWait, models []metroCapturedModel) string {
	identityModels := metroCaptureIdentityModels(models)
	identity, _ := json.Marshal(struct {
		Profile          string
		WaitAvailability string
		WaitError        string
		HubError         string
		Waits            []MetroWait
		Models           []metroCapturedModel
		Corrections      []hubPosition
	}{r.topology.Profile, string(r.publication.Status.Status), r.publication.Status.Message, r.hubError, waits, identityModels, r.hubCorrections})
	return fmt.Sprintf("%x", sha256.Sum256(identity))
}
func metroCaptureIdentityModels(models []metroCapturedModel) []metroCapturedModel {
	identity := append([]metroCapturedModel{}, models...)
	for n := range identity {
		identity[n].PublishedAt, identity[n].ReceivedAt = time.Time{}, time.Time{}
		identity[n].Position.At, identity[n].Position.ReceivedAt = 0, nil
	}
	return identity
}
func (r *metroRuntime) captureNeedsPublication(fingerprint string) bool {
	return fingerprint != r.capture.Fingerprint || r.capture.Gap && r.capture.GapQueuedVersion != r.capture.GapVersion
}
func (r *metroRuntime) capturedInput(now time.Time, waits []MetroWait, models []metroCapturedModel) metroCapturedInput {
	plan := ""
	if r.plan != nil {
		plan = r.plan.PlanID
	}
	return metroCapturedInput{WaitAvailability: string(r.publication.Status.Status), WaitError: r.publication.Status.Message, HubError: r.hubError, Version: 1, Profile: r.topology.Profile, Plan: plan, ReceivedAt: now, Sequence: r.capture.Sequence, GapBefore: r.capture.Gap, GapVersion: r.capture.GapVersion, Waits: waits, Models: models, Corrections: r.hubCorrections}
}
func (r *metroRuntime) admitCapturedInput(input metroCapturedInput, raw []byte, fingerprint string, now time.Time) {
	if len(raw) > 256<<10 || len(r.capture.Queue) >= 64 || r.capture.Bytes+len(raw) > 8<<20 {
		r.markCaptureGap()
		r.historyStatus = "paused"
		return
	}
	record := patterns.MetroEventRecord{ID: fmt.Sprintf("metro:inputs:%s:%d", r.session, r.capture.Sequence), Journey: "metro:inputs", At: now, Payload: raw}
	r.capture.Queue = append(r.capture.Queue, record)
	r.capture.Bytes += len(raw)
	r.capture.Fingerprint = fingerprint
	if input.GapBefore {
		r.capture.GapQueuedVersion = input.GapVersion
	}
}

func (r *metroRuntime) flushInputs(ctx context.Context, history *patterns.Service, now time.Time) {
	if history == nil {
		return
	}
	batch := r.pendingCaptureBatch(now)
	if len(batch) == 0 {
		return
	}
	err := history.RecordMetroInputs(ctx, batch, now)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil && r.retryCaptureWrite(now) {
		return
	}
	r.capture.Failures = 0
	for _, record := range batch {
		r.acknowledgeCapturedInput(record, err == nil)
	}
}
func (r *metroRuntime) pendingCaptureBatch(now time.Time) []patterns.MetroEventRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Before(r.capture.RetryAt) {
		return nil
	}
	return append([]patterns.MetroEventRecord{}, r.capture.Queue...)
}
func (r *metroRuntime) retryCaptureWrite(now time.Time) bool {
	r.markCaptureGap()
	r.capture.Failures++
	r.capture.RetryAt = now.Add(time.Duration(min(30, 1<<min(r.capture.Failures, 5))) * time.Second)
	r.historyStatus = "paused"
	return r.capture.Failures < 3
}
func (r *metroRuntime) acknowledgeCapturedInput(record patterns.MetroEventRecord, written bool) {
	if written {
		r.acknowledgeCaptureGap(record.Payload)
	}
	if len(r.capture.Queue) > 0 && r.capture.Queue[0].ID == record.ID {
		r.capture.Bytes -= len(record.Payload)
		r.capture.Queue = r.capture.Queue[1:]
	}
}
func (r *metroRuntime) acknowledgeCaptureGap(payload []byte) {
	var input metroCapturedInput
	if json.Unmarshal(payload, &input) == nil && input.GapBefore && input.GapVersion == r.capture.GapVersion {
		r.capture.Gap = false
	}
}

func (r *metroRuntime) markCaptureGap() { r.capture.Gap = true; r.capture.GapVersion++ }

func metroCanonicalWaits(waits []MetroWait) []MetroWait {
	type keyedWait struct {
		Row MetroWait
		Key string
	}
	keyed := make([]keyedWait, len(waits))
	for n, row := range waits {
		raw, _ := json.Marshal(row)
		keyed[n] = keyedWait{row, string(raw)}
	}
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].Key < keyed[j].Key })
	out := make([]MetroWait, len(keyed))
	for n, row := range keyed {
		out[n] = row.Row
	}
	return out
}
