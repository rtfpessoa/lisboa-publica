package app

import (
	"context"
	"lisboapublica/internal/api"
	"time"
)

// The existing TML request carries all agencies. Decode once and publish bounded
// per-operator snapshots; browsers never fetch ETA upstream.
func (f *Fetcher) refreshSharedPredictions(parent context.Context) {
	if !f.etaMu.TryLock() {
		return
	}
	defer f.etaMu.Unlock()
	ctx, cancel := context.WithTimeout(parent, providerRefreshInterval)
	defer cancel()
	state, _ := f.Cache.state("")
	if len(state.Static) == 0 {
		return
	}
	feed, fetchErr := f.collectSharedPredictions(ctx, state)
	empty := feed == nil
	now := time.Now().UTC()
	results := map[string]*CPData{}
	for operator, static := range state.Static {
		if static.Schedule == nil {
			continue
		}
		result := providerPredictionRead{ctx: ctx, operator: operator, static: static, feed: feed, err: fetchErr, state: state, now: now, empty: empty}.result()
		results[operator] = result
	}
	f.Cache.updateProviderPredictions(state.Static, results)
	f.recordFeedHistory(results)
}
func (c *Cache) updateProviderPredictions(static map[string]*StaticData, results map[string]*CPData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value := *c.current
	value.Predictions = map[string]*CPData{}
	for operator, data := range c.current.Predictions {
		value.Predictions[operator] = data
	}
	for operator, data := range results {
		if c.current.Static[operator] == static[operator] {
			value.Predictions[operator] = data
			if operator == "cp" {
				value.CP = data
			}
		}
	}
	c.publish(&value)
}
func predictionsFor(state *State, operator string) *CPData {
	if operator == "cp" {
		return state.CP
	}
	return state.Predictions[operator]
}

type providerPredictionRead struct {
	ctx      context.Context
	operator string
	static   *StaticData
	feed     *cpFeed
	err      error
	state    *State
	now      time.Time
	empty    bool
}

func (r providerPredictionRead) result() *CPData {
	var result *CPData
	err := r.err
	if err == nil {
		result, err = normalizeProviderPrediction(r.ctx, r.operator, r.static, r.feed, r.now, r.empty)
	}
	if err != nil {
		result = failedCP(predictionsFor(r.state, r.operator), r.static.PlanID, r.now)
		result.Availability.Message = "Previsões indisponíveis; os horários planeados mantêm-se disponíveis."
	}
	if r.feed != nil && r.feed.PartialOperators[r.operator] {
		result.Availability.Status = "partial"
		result.Availability.Message = "Previsões com cobertura parcial; os horários planeados mantêm-se disponíveis."
	}
	return result
}
func normalizeProviderPrediction(ctx context.Context, op string, static *StaticData, feed *cpFeed, now time.Time, empty bool) (*CPData, error) {
	if empty {
		return &CPData{PlanID: static.PlanID, Rows: []api.CPPrediction{}, Availability: api.CPPredictionAvailability{Status: "ok", Message: "Sem previsões atuais na fonte TML.", SourceUrl: cpSourceURL, CollectedAt: &now}}, nil
	}
	selected := *feed
	selected.OtherUpdates = nil
	selected.Updates = feed.OtherUpdates[op]
	if op == "cp" {
		selected.Updates = feed.Updates
	}
	return normalizeCP(ctx, &selected, static, now)
}
