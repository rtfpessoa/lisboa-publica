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
	for _, vehicle := range live.Vehicles {
		s.collector.observe(vehicle, distances[vehicle.Id], live.Collected, s.HistoryInterval)
	}
}

// publishProvider requires PublishMu; memory advances independently of durable writes.
func (f *Fetcher) publishProvider(ctx context.Context, id string, live *LiveData, op api.Operator, distances map[string]*float64) {
	f.Store.stageLive(live, distances)
	now := time.Now().UTC()
	if now.Sub(f.lastPersist[id]) >= livePersistenceInterval || f.Store.HistoryInterval == 0 {
		if f.lastPersist == nil {
			f.lastPersist = map[string]time.Time{}
		}
		f.lastPersist[id] = now
		if err := f.Store.Save(ctx, id, nil, live, op, distances); err != nil {
			f.Log.Error("live persistence failed", zap.String("operator", id), zap.Error(err))
		}
	}
	f.Cache.update(id, nil, live, op)
}
