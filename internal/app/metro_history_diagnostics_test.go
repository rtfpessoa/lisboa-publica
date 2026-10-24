package app

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

type diagnosticQueueTest struct {
	name    string
	stopped bool
	full    bool
	bytes   bool
	invalid bool
	want    metroDiagnosticQueue
}

func TestMetroDiagnosticQueueOutcomes(t *testing.T) {
	tests := []diagnosticQueueTest{
		{name: "admitted", want: metroDiagnosticQueue{admitted: 1}},
		{name: "stopped first", stopped: true, full: true, bytes: true, invalid: true, want: metroDiagnosticQueue{stopped: 1}},
		{name: "invalid before full", full: true, invalid: true, want: metroDiagnosticQueue{encoding: 1}},
		{name: "bytes before full", full: true, bytes: true, want: metroDiagnosticQueue{bytes: 1}},
		{name: "full", full: true, want: metroDiagnosticQueue{full: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testDiagnosticQueueOutcome(t, test)
		})
	}
}

func testDiagnosticQueueOutcome(t *testing.T, test diagnosticQueueTest) {
	t.Helper()
	with := diagnosticQueueClient(test.stopped, test.full, test.bytes, true)
	without := diagnosticQueueClient(test.stopped, test.full, test.bytes, false)
	with.History, without.History = diagnosticQueueHistory(t), diagnosticQueueHistory(t)
	initial := with.History.MetroCut().Snapshot()
	data := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusOk}}
	if test.invalid {
		data.RawWaits = json.RawMessage("{")
	}
	now := time.Now()
	with.enqueuePatterns(data, nil, now)
	without.enqueuePatterns(data, nil, now)
	if got := with.diagnostics.queue; got != test.want {
		t.Fatalf("queue counters = %+v, want %+v", got, test.want)
	}
	if with.historyLossGeneration != without.historyLossGeneration || with.historyBytes != without.historyBytes || len(with.historyQueue) != len(without.historyQueue) {
		t.Fatal("diagnostics changed queue accounting or loss")
	}
	assertDiagnosticQueueCut(t, with, without, initial, test.want.admitted == 1)
	if test.want.admitted == 1 {
		assertDiagnosticAdmittedTask(t, <-with.historyQueue, <-without.historyQueue)
	}
}

func assertDiagnosticQueueCut(t *testing.T, with, without *MetroClient, initial patterns.MetroCutSnapshot, admitted bool) {
	t.Helper()
	withCut, withoutCut := with.History.MetroCut().Snapshot(), without.History.MetroCut().Snapshot()
	if withCut.Valid != withoutCut.Valid || withCut.Pending != withoutCut.Pending || withCut.Revision != withoutCut.Revision {
		t.Fatal("diagnostics changed the cut state")
	}
	if admitted {
		if withCut != initial {
			t.Fatal("admission changed the cut")
		}
	} else if !withCut.Pending || withCut.Revision != initial.Revision+1 {
		t.Fatal("rejected input did not mark a cut")
	}
}

func assertDiagnosticAdmittedTask(t *testing.T, with, without metroHistoryTask) {
	t.Helper()
	if with.queuedAt.IsZero() || without.queuedAt != (time.Time{}) || with.Bytes != without.Bytes || with.LossGeneration != without.LossGeneration || !reflect.DeepEqual(with.Data, without.Data) {
		t.Fatal("diagnostics changed the admitted task")
	}
}

func diagnosticQueueHistory(t *testing.T) *patterns.Service {
	t.Helper()
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	return history
}

func diagnosticQueueClient(stopped, full, bytes, enabled bool) *MetroClient {
	m := &MetroClient{Cache: NewCache()}
	m.historyQueue, m.historyStopped = make(chan metroHistoryTask, 1), stopped
	if enabled {
		m.diagnostics = newMetroDeliveryDiagnostics()
	}
	if full {
		m.historyQueue <- metroHistoryTask{}
	}
	if bytes {
		m.historyBytes = 8 << 20
	}
	return m
}

func TestMetroDiagnosticAcknowledgementOutcomes(t *testing.T) {
	for _, name := range []string{"acknowledged", "generation mismatch", "newer cut", "nonpending cut", "invalid cut", "failed", "unhealthy", "unsampled", "not appended"} {
		t.Run(name, func(t *testing.T) {
			s, _, _, _ := metroLiveFixture(t)
			m := &MetroClient{Cache: s.Cache, History: s.Patterns}
			m.diagnostics, m.historyBytes = newMetroDeliveryDiagnostics(), 1
			cut := s.Patterns.MetroCut().MarkPending()
			task := metroHistoryTask{Bytes: 1}
			result := patterns.MetroRecordResult{Kind: patterns.MetroGapSampled, Appended: true, Cut: cut}
			err, failed := diagnosticAckInput(name, m, &result)
			before := s.Patterns.MetroCut().Snapshot()
			m.finishPatternHistory(task, result, err, failed)
			got := m.diagnostics.queue
			want := diagnosticAckExpected(name)
			if got != want {
				t.Fatalf("ack counters = %+v, want %+v", got, want)
			}
			assertDiagnosticAckCut(t, name, before, s.Patterns.MetroCut().Snapshot())
		})
	}
}

func diagnosticAckInput(name string, m *MetroClient, result *patterns.MetroRecordResult) (error, bool) {
	switch name {
	case "generation mismatch":
		m.historyLossGeneration = 1
	case "newer cut":
		m.History.MetroCut().MarkPending()
	case "nonpending cut":
		result.Cut.Pending = false
	case "invalid cut":
		result.Cut.Valid = false
	case "failed":
		return errors.New("synthetic failure"), false
	case "unhealthy":
		return nil, true
	case "unsampled":
		result.Kind = patterns.MetroUnsampled
	case "not appended":
		result.Appended = false
	}
	return nil, false
}

func diagnosticAckExpected(name string) (want metroDiagnosticQueue) {
	switch name {
	case "acknowledged":
		want.candidates, want.acknowledged = 1, 1
	case "generation mismatch":
		want.candidates, want.mismatched = 1, 1
	case "newer cut", "nonpending cut", "invalid cut":
		want.candidates, want.rejected = 1, 1
	case "failed", "unhealthy":
		want.failed = 1
	}
	return want
}

func assertDiagnosticAckCut(t *testing.T, name string, before, after patterns.MetroCutSnapshot) {
	t.Helper()
	if name == "acknowledged" && (after.Pending || after.Revision != before.Revision) {
		t.Fatal("matching cut was not acknowledged")
	}
	if name != "acknowledged" && name != "failed" && name != "unhealthy" && after != before {
		t.Fatal("unmatched cut was altered")
	}
}

func TestMetroDiagnosticNewerQueueLossPreventsInflightAcknowledgement(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	fifo, err := s.Patterns.AcquireMetroFIFO()
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()
	m := &MetroClient{Cache: s.Cache, History: s.Patterns}
	m.historyFIFO, m.historyQueue, m.diagnostics = fifo, make(chan metroHistoryTask, 1), newMetroDeliveryDiagnostics()
	m.enqueuePatterns(data, static, now)
	first := <-m.historyQueue
	m.enqueuePatterns(data, static, now.Add(time.Second))
	s.Patterns.MetroCut().MarkPending()
	if err := fifo.InterruptMetroContinuity(); err != nil {
		t.Fatal(err)
	}
	receipt, topology := m.patternReceipt(&first.Data, first.Static, first.At)
	result, err := fifo.RecordMetro(receipt, topology)
	if err != nil || result.Kind != patterns.MetroGapSampled || !result.Appended {
		t.Fatalf("fixture did not admit a gap sample: %+v %v", result, err)
	}
	release, done := make(chan struct{}), make(chan struct{})
	go func() {
		<-release
		m.finishPatternHistory(first, result, nil, false)
		close(done)
	}()
	m.enqueuePatterns(data, static, now.Add(2*time.Second))
	newer := s.Patterns.MetroCut().Snapshot()
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight delivery did not finish")
	}
	if !newer.Pending || s.Patterns.MetroCut().Snapshot() != newer || m.diagnostics.queue.mismatched != 1 || m.diagnostics.queue.acknowledged != 0 {
		t.Fatal("older in-flight sample acknowledged the newer rejected input")
	}
	queued := <-m.historyQueue
	if m.historyBytes != queued.Bytes {
		t.Fatal("in-flight completion changed queued accounting")
	}
}

func TestMetroDiagnosticsExistingLogCadenceAndProjection(t *testing.T) {
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	fifo, err := history.AcquireMetroFIFO()
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()
	core, logs := observer.New(zap.DebugLevel)
	m := &MetroClient{Cache: NewCache(), History: history, Log: zap.New(core)}
	m.historyQueue, m.historyFIFO, m.diagnostics = make(chan metroHistoryTask, 1), fifo, newMetroDeliveryDiagnostics()
	now := time.Now()
	clock := now
	m.diagnostics.now = func() time.Time { return clock }
	data := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusOk}}
	for i := 0; i < 3; i++ {
		clock = now.Add(time.Duration(i) * 30 * time.Second)
		m.enqueuePatterns(data, nil, clock.UTC())
		m.deliverPatternHistory(<-m.historyQueue, 0)
	}
	entries := logs.FilterMessage("metro sample").All()
	if len(entries) != 3 {
		t.Fatalf("got %d existing sample entries, want 3", len(entries))
	}
	first := entries[0].ContextMap()["delivery"].(map[string]interface{})
	if first["mode"] != "fifo" || first["project_count"] != uint64(0) || first["worker_count"] != uint64(0) {
		t.Fatalf("first log claimed unfinished projection: %v", first)
	}
	if _, ok := entries[1].ContextMap()["delivery"]; ok {
		t.Fatal("diagnostic fields were repeated within one minute")
	}
	third := entries[2].ContextMap()["delivery"].(map[string]interface{})
	if third["project_count"] != uint64(2) || third["worker_count"] != uint64(2) || third["receipt_count"] != uint64(3) || third["record_count"] != uint64(3) {
		t.Fatalf("cumulative stages do not reflect completed work: %v", third)
	}
}

func TestMetroDiagnosticStandaloneAndDisabledDebug(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "info", true: "debug"}[enabled], func(t *testing.T) {
			history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			defer history.Close()
			level := zap.InfoLevel
			if enabled {
				level = zap.DebugLevel
			}
			core, logs := observer.New(level)
			m := &MetroClient{Cache: NewCache(), History: history, Log: zap.New(core)}
			m.diagnostics = newMetroDeliveryDiagnostics()
			data := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusOk}}
			now := time.Now().UTC()
			for _, at := range []time.Time{now, now, now.Add(-time.Second), now.Add(30 * time.Second)} {
				if err := m.recordPatterns(data, nil, at); err != nil {
					t.Fatal(err)
				}
			}
			entries := logs.FilterMessage("metro sample").All()
			if !enabled {
				if len(entries) != 0 {
					t.Fatal("disabled debug emitted a sample entry")
				}
				return
			}
			if len(entries) != 2 {
				t.Fatalf("receipt deduplication changed: %d entries", len(entries))
			}
			fields := entries[0].ContextMap()["delivery"].(map[string]interface{})
			if fields["mode"] != "standalone" {
				t.Fatalf("wrong delivery mode: %v", fields)
			}
			if _, ok := fields["receipt_count"]; ok {
				t.Fatal("standalone entry included FIFO worker metrics")
			}
		})
	}
}

func TestMetroDiagnosticSnapshotDoesNotWaitForQueue(t *testing.T) {
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	core, _ := observer.New(zap.DebugLevel)
	m := &MetroClient{History: history, Log: zap.New(core)}
	m.diagnostics = newMetroDeliveryDiagnostics()
	m.historyMu.Lock()
	done := make(chan zap.Field, 1)
	go func() {
		field, ok := m.metroDeliveryDiagnosticField(true)
		if ok {
			done <- field
		}
	}()
	select {
	case field := <-done:
		enc := zapcore.NewMapObjectEncoder()
		field.AddTo(enc)
		if enc.Fields["delivery"].(map[string]interface{})["queue_available"] != false {
			t.Fatal("locked queue was not marked unavailable")
		}
	case <-time.After(time.Second):
		m.historyMu.Unlock()
		t.Fatal("diagnostics blocked on the queue lock")
	}
	m.historyMu.Unlock()
}

func TestMetroDiagnosticFieldsBoundedAndPrivate(t *testing.T) {
	max := uint64(math.MaxUint64)
	stage := [2]uint64{max, max}
	s := metroDiagnosticSnapshot{fifo: true, queueAvailable: true, depth: math.MaxInt, bytes: math.MaxInt,
		queue:    metroDiagnosticQueue{max, max, max, max, max, max, max, max, max, max},
		cut:      patterns.MetroCutSnapshot{Nonce: [16]byte{'s', 'e', 'c', 'r', 'e', 't'}, Valid: true, Pending: true, Revision: max},
		dequeued: max, queueWait: stage, interrupt: stage, receipt: stage, record: stage, project: stage, worker: stage}
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	base, err := enc.EncodeEntry(zapcore.Entry{Message: "metro sample"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Free()
	full, err := enc.EncodeEntry(zapcore.Entry{Message: "metro sample"}, []zap.Field{zap.Object("delivery", s)})
	if err != nil {
		t.Fatal(err)
	}
	defer full.Free()
	if added := full.Len() - base.Len(); added > 2<<10 {
		t.Fatalf("added fields use %d bytes", added)
	}
	if strings.Contains(full.String(), "secret") || strings.Contains(full.String(), "nonce") {
		t.Fatal("cut nonce appeared in diagnostic output")
	}
}

func TestMetroDiagnosticStageSaturatesAndIgnoresInvalidTime(t *testing.T) {
	var stage metroDiagnosticStage
	stage.count.Store(math.MaxUint64 - 1)
	stage.nanos.Store(math.MaxUint64 - 2)
	now := time.Now()
	stage.observe(now, now.Add(10*time.Nanosecond))
	stage.observe(now, now.Add(-time.Second))
	stage.observe(time.Time{}, now)
	if stage.count.Load() != math.MaxUint64 || stage.nanos.Load() != math.MaxUint64 {
		t.Fatal("invalid time changed or overflowed saturated stage")
	}
}

func TestMetroDiagnosticStageAddsNoPerTaskAllocation(t *testing.T) {
	d := newMetroDeliveryDiagnostics()
	task := metroHistoryTask{queuedAt: time.Now()}
	allocations := testing.AllocsPerRun(1000, func() {
		started := d.startTask(task)
		stage := d.start()
		d.finishStage(metroDiagnosticReceipt, stage)
		d.finishTask(started)
	})
	if allocations != 0 {
		t.Fatalf("timing added %g allocations per task", allocations)
	}
}

func TestMetroDiagnosticWorkerStagesAndEarlyFailure(t *testing.T) {
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	fifo, err := history.AcquireMetroFIFO()
	if err != nil {
		t.Fatal(err)
	}
	defer fifo.Close()
	m := &MetroClient{Cache: NewCache(), History: history}
	m.historyFIFO, m.historyQueue, m.diagnostics = fifo, make(chan metroHistoryTask, 1), newMetroDeliveryDiagnostics()
	clock := time.Now()
	m.diagnostics.now = func() time.Time { clock = clock.Add(time.Millisecond); return clock }
	data := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusOk}}
	m.enqueuePatterns(data, nil, clock.UTC())
	m.deliverPatternHistory(<-m.historyQueue, 0)
	d := m.diagnostics
	for name, stage := range map[string]*metroDiagnosticStage{"queue": &d.queueWait, "receipt": &d.receipt, "record": &d.record, "projection": &d.project} {
		if stage.count.Load() != 1 || stage.nanos.Load() != uint64(time.Millisecond) {
			t.Fatalf("%s stage = %d calls, %d ns", name, stage.count.Load(), stage.nanos.Load())
		}
	}
	if d.dequeued.Load() != 1 || d.worker.count.Load() != 1 || d.worker.nanos.Load() != uint64(7*time.Millisecond) {
		t.Fatal("worker total did not include the completed projection")
	}
	// A missing lease fails the ordered interruption before receipt construction.
	m.historyFIFO = nil
	m.historyBytes = 1
	m.deliverPatternHistory(metroHistoryTask{Bytes: 1, LossGeneration: 1, queuedAt: clock}, 0)
	if d.interrupt.count.Load() != 1 || d.receipt.count.Load() != 1 || d.record.count.Load() != 1 || d.project.count.Load() != 1 || d.worker.count.Load() != 2 || d.dequeued.Load() != 2 {
		t.Fatal("early interruption failure fabricated later stage work")
	}
}
