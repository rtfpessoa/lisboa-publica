package app

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"lisboapublica/internal/patterns"
)

// Queue counters are protected by historyMu. Timings have a single worker but
// use atomics so logging cannot introduce another blocking lock.
type metroDiagnosticQueue struct {
	admitted, stopped, encoding, bytes, full, failed uint64
	candidates, mismatched, acknowledged, rejected   uint64
}

type metroDiagnosticStage struct {
	count atomic.Uint64
	nanos atomic.Uint64
}

type metroDeliveryDiagnostics struct {
	queue                                 metroDiagnosticQueue
	dequeued                              atomic.Uint64
	queueWait, interrupt, receipt, record metroDiagnosticStage
	project, worker                       metroDiagnosticStage
	gate                                  sync.Mutex
	lastFields                            time.Time
	now                                   func() time.Time
}

type metroDiagnosticStageID uint8

const (
	metroDiagnosticInterrupt metroDiagnosticStageID = iota
	metroDiagnosticReceipt
	metroDiagnosticRecord
	metroDiagnosticProject
)

func newMetroDeliveryDiagnostics() *metroDeliveryDiagnostics {
	return &metroDeliveryDiagnostics{now: time.Now}
}

func incrementMetroDiagnostic(value *uint64) {
	if *value != math.MaxUint64 {
		*value++
	}
}

func addMetroDiagnostic(value *atomic.Uint64, amount uint64) {
	for old := value.Load(); ; old = value.Load() {
		next := old + amount
		if next < old {
			next = math.MaxUint64
		}
		if value.CompareAndSwap(old, next) {
			return
		}
	}
}

func (d *metroDeliveryDiagnostics) start() time.Time {
	if d == nil {
		return time.Time{}
	}
	return d.now()
}

func (s *metroDiagnosticStage) observe(start, end time.Time) {
	if start.IsZero() || end.Before(start) {
		return
	}
	addMetroDiagnostic(&s.count, 1)
	addMetroDiagnostic(&s.nanos, uint64(end.Sub(start)))
}

func (d *metroDeliveryDiagnostics) startTask(task metroHistoryTask) time.Time {
	started := d.start()
	if d != nil {
		addMetroDiagnostic(&d.dequeued, 1)
		d.queueWait.observe(task.queuedAt, started)
	}
	return started
}

func (d *metroDeliveryDiagnostics) finishTask(started time.Time) {
	if d != nil {
		d.worker.observe(started, d.now())
	}
}

func (d *metroDeliveryDiagnostics) finishStage(stage metroDiagnosticStageID, started time.Time) {
	if d == nil {
		return
	}
	var target *metroDiagnosticStage
	switch stage {
	case metroDiagnosticInterrupt:
		target = &d.interrupt
	case metroDiagnosticReceipt:
		target = &d.receipt
	case metroDiagnosticRecord:
		target = &d.record
	case metroDiagnosticProject:
		target = &d.project
	default:
		return
	}
	target.observe(started, d.now())
}

// Called under historyMu, preserving the original short-circuited cut claim.
func (m *MetroClient) acknowledgePatternHistory(task metroHistoryTask, result patterns.MetroRecordResult) bool {
	if m.diagnostics != nil {
		incrementMetroDiagnostic(&m.diagnostics.queue.candidates)
	}
	if task.LossGeneration != m.historyLossGeneration {
		if m.diagnostics != nil {
			incrementMetroDiagnostic(&m.diagnostics.queue.mismatched)
		}
		return false
	}
	acknowledged := m.History.MetroCut().Acknowledge(result.Cut)
	if m.diagnostics != nil {
		if acknowledged {
			incrementMetroDiagnostic(&m.diagnostics.queue.acknowledged)
		} else {
			incrementMetroDiagnostic(&m.diagnostics.queue.rejected)
		}
	}
	return acknowledged
}

type metroDiagnosticSnapshot struct {
	fifo                          bool
	queueAvailable                bool
	queue                         metroDiagnosticQueue
	depth, bytes                  int
	cut                           patterns.MetroCutSnapshot
	dequeued                      uint64
	queueWait, interrupt, receipt [2]uint64
	record, project, worker       [2]uint64
}

func metroStageSnapshot(stage *metroDiagnosticStage) [2]uint64 {
	return [2]uint64{stage.count.Load(), stage.nanos.Load()}
}

func (m *MetroClient) metroDeliveryDiagnosticField(fifo bool) (zap.Field, bool) {
	d := m.diagnostics
	if d == nil || !m.Log.Core().Enabled(zap.DebugLevel) || !d.gate.TryLock() {
		return zap.Field{}, false
	}
	defer d.gate.Unlock()
	now := d.now()
	if !d.lastFields.IsZero() && now.Sub(d.lastFields) < time.Minute {
		return zap.Field{}, false
	}
	d.lastFields = now
	return zap.Object("delivery", m.metroDeliverySnapshot(fifo)), true
}

func (m *MetroClient) metroDeliverySnapshot(fifo bool) metroDiagnosticSnapshot {
	snapshot := metroDiagnosticSnapshot{fifo: fifo, cut: m.History.MetroCut().Snapshot()}
	if !fifo {
		return snapshot
	}
	diagnostics := m.diagnostics
	snapshot.dequeued = diagnostics.dequeued.Load()
	m.snapshotHistoryQueue(&snapshot)
	snapshot.queueWait = metroStageSnapshot(&diagnostics.queueWait)
	snapshot.interrupt = metroStageSnapshot(&diagnostics.interrupt)
	snapshot.receipt = metroStageSnapshot(&diagnostics.receipt)
	snapshot.record = metroStageSnapshot(&diagnostics.record)
	snapshot.project = metroStageSnapshot(&diagnostics.project)
	snapshot.worker = metroStageSnapshot(&diagnostics.worker)
	return snapshot
}

func (m *MetroClient) snapshotHistoryQueue(snapshot *metroDiagnosticSnapshot) {
	if !m.historyMu.TryLock() {
		return
	}
	snapshot.queueAvailable, snapshot.queue = true, m.diagnostics.queue
	snapshot.depth, snapshot.bytes = len(m.historyQueue), m.historyBytes
	m.historyMu.Unlock()
}

func (s metroDiagnosticSnapshot) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	mode := "standalone"
	if s.fifo {
		mode = "fifo"
	}
	enc.AddString("mode", mode)
	enc.AddBool("cut_valid", s.cut.Valid)
	enc.AddBool("cut_pending", s.cut.Pending)
	enc.AddUint64("cut_revision", s.cut.Revision)
	if !s.fifo {
		return nil
	}
	s.marshalQueue(enc)
	s.marshalStages(enc)
	return nil
}

func (s metroDiagnosticSnapshot) marshalQueue(enc zapcore.ObjectEncoder) {
	enc.AddBool("queue_available", s.queueAvailable)
	enc.AddUint64("dequeued", s.dequeued)
	if !s.queueAvailable {
		return
	}
	enc.AddInt("queue_depth", s.depth)
	enc.AddInt("admitted_bytes", s.bytes)
	s.queue.marshal(enc)
}

func (q metroDiagnosticQueue) marshal(enc zapcore.ObjectEncoder) {
	enc.AddUint64("admitted", q.admitted)
	enc.AddUint64("rejected_stopped", q.stopped)
	enc.AddUint64("rejected_encoding", q.encoding)
	enc.AddUint64("rejected_bytes", q.bytes)
	enc.AddUint64("rejected_full", q.full)
	enc.AddUint64("delivery_failed", q.failed)
	enc.AddUint64("ack_candidates", q.candidates)
	enc.AddUint64("ack_generation_mismatch", q.mismatched)
	enc.AddUint64("acknowledged", q.acknowledged)
	enc.AddUint64("ack_cut_rejected", q.rejected)
}

func (s metroDiagnosticSnapshot) marshalStages(enc zapcore.ObjectEncoder) {
	for _, stage := range []struct {
		count, nanos string
		value        [2]uint64
	}{
		{"queue_wait_count", "queue_wait_ns", s.queueWait},
		{"interrupt_count", "interrupt_ns", s.interrupt},
		{"receipt_count", "receipt_ns", s.receipt},
		{"record_count", "record_ns", s.record},
		{"project_count", "project_ns", s.project},
		{"worker_count", "worker_ns", s.worker},
	} {
		enc.AddUint64(stage.count, stage.value[0])
		enc.AddUint64(stage.nanos, stage.value[1])
	}
}
