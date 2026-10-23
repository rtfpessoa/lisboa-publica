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
	Bytes          int
	LossGeneration uint64
}
type metroHistoryDelivery struct {
	historyMu             sync.Mutex
	historyQueue          chan metroHistoryTask
	historyFIFO           *patterns.MetroFIFO
	historyStopped        bool
	historyBytes          int
	historyLossGeneration uint64
}

// Serialize the standalone-to-FIFO handoff against complete Refresh calls.
func (m *MetroClient) startHistoryDelivery(ctx context.Context) {
	if m.History == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	fifo, err := m.History.AcquireMetroFIFO()
	m.historyMu.Lock()
	m.historyQueue = make(chan metroHistoryTask, 64)
	m.historyFIFO = fifo
	m.historyStopped = err != nil
	if err != nil {
		m.History.MetroCut().MarkPending()
		m.markHistoryDeliveryLost()
	}
	m.historyMu.Unlock()
	if err == nil {
		go m.historyLoop(ctx)
	} else if m.Log != nil {
		m.Log.Warn("metro history delivery unavailable", zap.Error(err))
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
	task := metroHistoryTask{Data: *data, Static: static, At: now, Bytes: size, LossGeneration: m.historyLossGeneration}
	task.Data.Waits = append([]MetroWait{}, data.Waits...)
	return task
}
func (m *MetroClient) admitPatternHistoryTask(task metroHistoryTask) bool {
	if m.historyBytes+task.Bytes > 8<<20 {
		return false
	}
	select {
	case m.historyQueue <- task:
		m.historyBytes += task.Bytes
		return true
	default:
		return false
	}
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
	if task.LossGeneration != generation {
		if err := m.historyFIFO.InterruptMetroContinuity(); err != nil {
			m.finishPatternHistory(task, patterns.MetroRecordResult{}, err, false)
			return task.LossGeneration
		}
		generation = task.LossGeneration
	}
	receipt, topology := m.patternReceipt(&task.Data, task.Static, task.At)
	result, err := m.historyFIFO.RecordMetro(receipt, topology)
	failed := err != nil || receipt.Error != ""
	m.finishPatternHistory(task, result, err, failed)
	if failed {
		_ = m.historyFIFO.InterruptMetroContinuity()
		if m.Log != nil {
			m.Log.Warn("metro pattern sample failed", zap.Error(err))
		}
	} else {
		m.logMetroSample()
	}
	m.Cache.metroRuntime.projectOwn(&task.Data, m.History, time.Now().UTC())
	m.logOwnForecastStats()
	return generation
}
func (m *MetroClient) finishPatternHistory(task metroHistoryTask, result patterns.MetroRecordResult, err error, failed bool) {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	m.historyBytes -= task.Bytes
	if err != nil || failed {
		m.historyLossGeneration++
		if !result.FailureMarked {
			m.History.MetroCut().MarkPending()
		}
		m.markHistoryDeliveryLost()
		return
	}
	recovered := result.Kind == patterns.MetroGapSampled && result.Appended && task.LossGeneration == m.historyLossGeneration && m.History.MetroCut().Acknowledge(result.Cut)
	r := m.Cache.metroRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	if recovered {
		r.historyDeliveryGap = false
		r.historyCollecting(time.Now().UTC())
	}
}
