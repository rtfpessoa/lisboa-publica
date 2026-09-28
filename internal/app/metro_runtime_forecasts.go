package app

import (
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"time"
)

func (r *metroRuntime) projectOwn(data *MetroData, history *patterns.Service, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.active {
		if t := r.tracks[id]; t != nil && t.BarrierRevision == 0 {
			projectMetroOwnTrack(t, history, now)
		}
	}
	data.Trains = r.current(now)
}
func projectMetroOwnTrack(t *metroTrack, history *patterns.Service, now time.Time) {
	for n := range t.Train.Calls {
		t.Train.Calls[n].OwnPrediction = nil
	}
	if t.Train.Association != "supported" {
		return
	}
	signals := []patterns.MetroPopupSignal{}
	for n, c := range t.Train.Calls {
		if c.Arrival.Inferred != nil {
			signals = append(signals, patterns.MetroPopupSignal{Stop: t.Codes[n], Start: c.Arrival.Inferred.WindowStart, End: c.Arrival.Inferred.WindowEnd})
		}
	}
	forecasts := history.MetroPopupForecasts(patterns.MetroPopupForecastQuery{Train: t.Train.Reference, Route: t.Train.RouteId, Direction: t.ProviderDirection, Profile: t.Profile, Signals: signals, Now: now})
	for n, code := range t.Codes {
		if candidate := uniqueMetroOwnForecast(forecasts, code); candidate != nil {
			t.Train.Calls[n].OwnPrediction = &api.CallTimeEvidence{At: *candidate.OwnAt, SourceUrl: "/api/v1/metro/patterns", SourceUpdatedAt: candidate.SourceAt, ValidUntil: candidate.OwnValidUntil, ModelVersion: ptr(candidate.Profile), AssociationEpisode: ptr(candidate.Episode)}
		}
	}
}
func uniqueMetroOwnForecast(forecasts []patterns.Forecast, code string) *patterns.Forecast {
	var candidate *patterns.Forecast
	ambiguous := false
	for _, f := range forecasts {
		if f.Stop != code {
			continue
		}
		if candidate != nil && !candidate.OwnAt.Equal(*f.OwnAt) {
			ambiguous = true
		}
		copy := f
		candidate = &copy
	}
	if ambiguous {
		return nil
	}
	return candidate
}
