package app

import (
	"context"
	"encoding/json"
	"sync"
	"time"
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
	historyBytes          int
	historyLossGeneration uint64
}

func (m *MetroClient) enqueuePatterns(data *MetroData, static *StaticData, now time.Time) {
	m.historyMu.Lock()
	if m.historyQueue == nil {
		m.historyMu.Unlock()
		m.recordPatterns(data, static, now)
		return
	}
	task := m.patternHistoryTask(data, static, now)
	admitted := m.admitPatternHistoryTask(task)
	if !admitted {
		m.historyLossGeneration++
	}
	m.historyMu.Unlock()
	if !admitted {
		m.markHistoryDeliveryLost()
	}
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
	generation := uint64(0)
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-m.historyQueue:
			generation = m.deliverPatternHistory(task, generation)
		}
	}
}
func (m *MetroClient) deliverPatternHistory(task metroHistoryTask, generation uint64) uint64 {
	if task.LossGeneration != generation {
		m.History.InterruptMetroContinuity()
		generation = task.LossGeneration
	}
	err := m.recordPatterns(&task.Data, task.Static, task.At)
	if err != nil {
		m.History.InterruptMetroContinuity()
	}
	m.Cache.metroRuntime.projectOwn(&task.Data, m.History, time.Now().UTC())
	m.acknowledgePatternHistory(task, generation, err)
	return generation
}
func (m *MetroClient) acknowledgePatternHistory(task metroHistoryTask, generation uint64, err error) {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	m.historyBytes -= task.Bytes
	recovered := err == nil && generation == m.historyLossGeneration
	r := m.Cache.metroRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	r.historyDeliveryGap = !recovered
	if !recovered {
		r.historyStatus = "paused"
	}
}
