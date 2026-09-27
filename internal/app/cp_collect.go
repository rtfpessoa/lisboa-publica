package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"lisboapublica/internal/api"
)

func (f *Fetcher) cpLoop(ctx context.Context) {
	for {
		f.refreshCP(ctx)
		timer := time.NewTimer(providerRefreshInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (f *Fetcher) refreshCP(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, providerRefreshInterval)
	defer cancel()
	state, _ := f.Cache.state("")
	static := state.Static["cp"]
	if static == nil {
		return
	}
	result, err := f.cpResponse(ctx, static)
	now := time.Now().UTC()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		result = failedCP(state.CP, static.PlanID, now)
		// Do not expose upstream URLs/payloads or alter CP position-source health.
		f.Log.Warn("CP predictions unavailable")
	}
	f.Cache.updateCP(static, result)
}

func (f *Fetcher) fetchCP(ctx context.Context) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", f.Hub+"/realtime/eta/gtfs", nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "LisboaPublica/1.0 (independent transit dashboard)")
	response, err := f.Client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	return readCPResponse(response)
}

func readCPResponse(response *http.Response) ([]byte, bool, error) {
	if response.StatusCode == http.StatusNoContent {
		return nil, true, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("CP upstream status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, providerJSONBytes+1))
	if int64(len(body)) > providerJSONBytes {
		err = fmt.Errorf("CP response capacity")
	}
	return body, false, err
}

func failedCP(previous *CPData, plan string, now time.Time) *CPData {
	out := &CPData{PlanID: plan, Rows: []api.CPPrediction{}}
	if previous != nil && previous.PlanID == plan {
		*out = *previous
	}
	out.Availability = api.CPPredictionAvailability{Status: api.CPPredictionAvailabilityStatusError, Message: "Previsões CP indisponíveis. Os horários planeados mantêm-se disponíveis.", CollectedAt: &now, SourceUrl: cpSourceURL}
	return out
}

func (c *Cache) updateCP(static *StaticData, data *CPData) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current.Static["cp"] != static {
		return false
	}
	value := *c.current
	value.CP = data
	c.publish(&value)
	return true
}

func (f *Fetcher) cpResponse(ctx context.Context, static *StaticData) (*CPData, error) {
	blob, empty, err := f.fetchCP(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if empty {
		return &CPData{PlanID: static.PlanID, Rows: []api.CPPrediction{}, Availability: api.CPPredictionAvailability{Status: api.CPPredictionAvailabilityStatusOk, Message: "Sem previsões atuais na fonte TML · CP.", CollectedAt: &now, SourceUrl: cpSourceURL}}, nil
	}
	feed, err := decodeCPFeedContext(ctx, blob)
	var result *CPData
	if err == nil {
		result, err = normalizeCP(ctx, feed, static, time.Now().UTC())
	}
	return result, err
}
