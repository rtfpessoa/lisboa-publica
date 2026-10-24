package app

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/patterns"
)

type metroHistoryTask struct {
	Data           MetroData
	Static         *StaticData
	At             time.Time
	queuedAt       time.Time
	Bytes          int
	LossGeneration uint64
	encodingFailed bool
}
type metroHistoryDelivery struct {
	historyMu             sync.Mutex
	historyQueue          chan metroHistoryTask
	historyFIFO           *patterns.MetroFIFO
	historyStopped        bool
	historyBytes          int
	historyLossGeneration uint64
	diagnostics           *metroDeliveryDiagnostics
}

// Serialize the standalone-to-FIFO handoff against complete Refresh calls.
func (m *MetroClient) startHistoryDelivery(ctx context.Context) {
	if m.History == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fifo, err := m.History.AcquireMetroFIFO()
	m.prepareHistoryDelivery(fifo, err)
	if err == nil {
		go m.historyLoop(ctx)
	} else if m.Log != nil {
		m.Log.Warn("metro history delivery unavailable", zap.Error(err))
	}
}

func (m *MetroClient) prepareHistoryDelivery(fifo *patterns.MetroFIFO, err error) {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	if m.diagnostics == nil {
		m.diagnostics = newMetroDeliveryDiagnostics()
	}
	m.historyQueue = make(chan metroHistoryTask, 64)
	m.historyFIFO = fifo
	m.historyStopped = err != nil
	if err != nil {
		m.History.MetroCut().MarkPending()
		m.markHistoryDeliveryLost()
	}
}

func (m *MetroClient) enqueuePatterns(data *MetroData, static *StaticData, now time.Time) {
	m.historyMu.Lock()
	if m.historyQueue == nil {
		m.historyMu.Unlock()
		m.recordPatterns(data, static, now)
		return
	}
	task := m.patternHistoryTask(data, static, now)
	admitted := !m.historyStopped && m.admitPatternHistoryTask(task)
	if !admitted {
		if m.historyStopped && m.diagnostics != nil {
			incrementMetroDiagnostic(&m.diagnostics.queue.stopped)
		}
		m.historyLossGeneration++
		if m.History != nil {
			m.History.MetroCut().MarkPending()
		}
		m.markHistoryDeliveryLost()
	}
	m.historyMu.Unlock()
}
func (m *MetroClient) patternHistoryTask(data *MetroData, static *StaticData, now time.Time) metroHistoryTask {
	encoded, err := json.Marshal(data)
	size := len(encoded)
	if err != nil {
		size = 8<<20 + 1
	}
	task := metroHistoryTask{Data: *data, Static: static, At: now, Bytes: size, LossGeneration: m.historyLossGeneration, encodingFailed: err != nil}
	task.Data.Waits = append([]MetroWait{}, data.Waits...)
	if m.diagnostics != nil {
		task.queuedAt = m.diagnostics.now()
	}
	return task
}
func (m *MetroClient) admitPatternHistoryTask(task metroHistoryTask) bool {
	if m.historyBytes+task.Bytes > 8<<20 {
		m.countHistoryCapacityRejection(task.encodingFailed)
		return false
	}
	select {
	case m.historyQueue <- task:
		m.historyBytes += task.Bytes
		if m.diagnostics != nil {
			incrementMetroDiagnostic(&m.diagnostics.queue.admitted)
		}
		return true
	default:
		if m.diagnostics != nil {
			incrementMetroDiagnostic(&m.diagnostics.queue.full)
		}
		return false
	}
}

// Called under historyMu, after the original byte-budget admission check.
func (m *MetroClient) countHistoryCapacityRejection(encodingFailed bool) {
	if m.diagnostics == nil {
		return
	}
	if encodingFailed {
		incrementMetroDiagnostic(&m.diagnostics.queue.encoding)
		return
	}
	incrementMetroDiagnostic(&m.diagnostics.queue.bytes)
}
func (m *MetroClient) markHistoryDeliveryLost() {
	r := m.Cache.metroRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markCaptureGap()
	r.historyDeliveryGap, r.historyStatus = true, "paused"
}

func (m *MetroClient) historyLoop(ctx context.Context) {
	m.historyLoopWithDelivery(ctx, m.deliverPatternHistory)
}
func (m *MetroClient) historyLoopWithDelivery(ctx context.Context, deliver func(metroHistoryTask, uint64) uint64) {
	stopped, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			m.cancelHistoryDelivery()
		case <-stopped:
		}
	}()
	defer func() {
		close(stopped)
		<-watchDone
		m.cancelHistoryDelivery()
	}()
	generation := uint64(0)
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case task := <-m.historyQueue:
			generation = deliver(task, generation)
		}
	}
}
func (m *MetroClient) cancelHistoryDelivery() {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	if m.historyStopped {
		return
	}
	m.historyStopped = true
	if m.historyBytes != 0 {
		m.historyLossGeneration++
		m.History.MetroCut().MarkPending()
		m.markHistoryDeliveryLost()
	}
}
func (m *MetroClient) deliverPatternHistory(task metroHistoryTask, generation uint64) uint64 {
	d := m.diagnostics
	started := d.startTask(task)
	defer d.finishTask(started)
	if task.LossGeneration != generation {
		err := m.interruptPatternHistory()
		if err != nil {
			m.finishPatternHistory(task, patterns.MetroRecordResult{}, err, false)
			return task.LossGeneration
		}
		generation = task.LossGeneration
	}
	result, err, failed := m.recordPatternHistory(&task)
	m.finishPatternHistory(task, result, err, failed)
	m.reportPatternHistoryOutcome(err, failed)
	m.projectPatternHistory(&task.Data)
	return generation
}

func (m *MetroClient) interruptPatternHistory() error {
	start := m.diagnostics.start()
	err := m.historyFIFO.InterruptMetroContinuity()
	m.diagnostics.finishStage(metroDiagnosticInterrupt, start)
	return err
}

func (m *MetroClient) recordPatternHistory(task *metroHistoryTask) (patterns.MetroRecordResult, error, bool) {
	start := m.diagnostics.start()
	receipt, topology := m.patternReceipt(&task.Data, task.Static, task.At)
	m.diagnostics.finishStage(metroDiagnosticReceipt, start)
	start = m.diagnostics.start()
	result, err := m.historyFIFO.RecordMetro(receipt, topology)
	m.diagnostics.finishStage(metroDiagnosticRecord, start)
	return result, err, err != nil || receipt.Error != ""
}

func (m *MetroClient) reportPatternHistoryOutcome(err error, failed bool) {
	if failed {
		_ = m.interruptPatternHistory()
		if m.Log != nil {
			m.Log.Warn("metro pattern sample failed", zap.Error(err))
		}
	} else {
		m.logMetroSample(true)
	}
}

func (m *MetroClient) projectPatternHistory(data *MetroData) {
	start := m.diagnostics.start()
	m.Cache.metroRuntime.projectOwn(data, m.History, time.Now().UTC())
	m.diagnostics.finishStage(metroDiagnosticProject, start)
	m.logOwnForecastStats()
}
func (m *MetroClient) finishPatternHistory(task metroHistoryTask, result patterns.MetroRecordResult, err error, failed bool) {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	m.historyBytes -= task.Bytes
	if err != nil || failed {
		m.failPatternHistory(result)
		return
	}
	recovered := false
	if result.Kind == patterns.MetroGapSampled && result.Appended {
		recovered = m.acknowledgePatternHistory(task, result)
	}
	r := m.Cache.metroRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	if recovered {
		r.historyDeliveryGap = false
		r.historyCollecting(time.Now().UTC())
	}
}

// Called under historyMu; the service may already have marked this failure.
func (m *MetroClient) failPatternHistory(result patterns.MetroRecordResult) {
	if m.diagnostics != nil {
		incrementMetroDiagnostic(&m.diagnostics.queue.failed)
	}
	m.historyLossGeneration++
	if !result.FailureMarked {
		m.History.MetroCut().MarkPending()
	}
	m.markHistoryDeliveryLost()
}
