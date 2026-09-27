package app

import (
	"context"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"time"
)

// stageLive accepts intermediate samples without closing or acknowledging a durable batch.
func (s *Store) stageLive(live *LiveData, distances map[string]*float64) {
	if live == nil || s.HistoryInterval == 0 || s.historyStatus() != "collecting" {
		return
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	if s.collector == nil {
		s.collector = newHistoryCollector()
	}
	for _, vehicle := range live.historyVehicles() {
		s.collector.observe(vehicle, distances[vehicle.Id], live.Collected, s.HistoryInterval)
	}
}

// publishProvider requires PublishMu; memory advances independently of durable writes.
func (f *Fetcher) publishProvider(ctx context.Context, id string, live *LiveData, op api.Operator, distances map[string]*float64) {
	now := time.Now().UTC()
	immediate := f.stageProviderReporting(ctx, id, live, op)
	f.Store.stageLive(live, distances)
	if immediate || now.Sub(f.lastPersist[id]) >= livePersistenceInterval || f.Store.HistoryInterval == 0 {
		if f.lastPersist == nil {
			f.lastPersist = map[string]time.Time{}
		}
		if err := f.Store.Save(ctx, id, nil, live, op, distances); err != nil {
			f.Log.Error("live persistence failed", zap.String("operator", id), zap.Error(err))
		} else {
			f.lastPersist[id] = now
		}
	}
	if live == nil {
		state, _ := f.Cache.state("")
		live = state.Live[id]
	}
	f.Cache.update(id, nil, f.Store.reporting.projection(id, live), op)
}

func (f *Fetcher) stageProviderReporting(ctx context.Context, id string, live *LiveData, op api.Operator) bool {
	reportingLive := live
	if reportingLive == nil {
		state, _ := f.Cache.state("")
		reportingLive = state.Live[id]
	}
	transition, reportingErr := f.Store.reporting.stage(reportingLookup{ctx, f.Store.readReporting}, id, reportingLive, op, time.Now().UTC())
	if reportingErr != nil {
		f.Log.Warn("reporting state unavailable", zap.String("operator", id), zap.Error(reportingErr))
	}
	return transition || f.Store.reporting.immediate(id)
}
