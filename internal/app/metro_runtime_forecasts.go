package app

import (
	"strings"
	"sync/atomic"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func (r *metroRuntime) projectOwn(data *MetroData, history *patterns.Service, now time.Time) {
	tracks := r.metroOwnForecastWork()
	for n := range tracks {
		projectMetroOwnTrack(&tracks[n], history, now)
		projectMetroScheduled(&tracks[n], now)
		r.countOwnForecasts(tracks[n])
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, copy := range tracks {
		r.applyOwnForecastWork(copy, now)
	}
	r.forecastCacheUntil = time.Time{}
	data.Trains = r.current(now)
}
func (r *metroRuntime) metroOwnForecastWork() []metroTrack {
	r.mu.Lock()
	defer r.mu.Unlock()
	tracks := []metroTrack{}
	for _, id := range r.active {
		if t := r.tracks[id]; t != nil {
			copy := *t
			copy.Train = cloneMetroTrain(t.Train)
			tracks = append(tracks, copy)
		}
	}
	return tracks
}
func (r *metroRuntime) applyOwnForecastWork(copy metroTrack, now time.Time) {
	track := r.tracks[copy.Train.JourneyId]
	if !metroOwnForecastWorkCurrent(track, copy, now) {
		return
	}
	for n := range track.Train.Calls {
		track.Train.Calls[n].OwnPrediction = copy.Train.Calls[n].OwnPrediction
		track.Train.Calls[n].OwnDeparturePrediction = copy.Train.Calls[n].OwnDeparturePrediction
	}
}
func metroOwnForecastWorkCurrent(t *metroTrack, copy metroTrack, now time.Time) bool {
	if t == nil || t.Train.Association != "supported" {
		return false
	}
	return t.Revision == copy.Revision && t.Profile == copy.Profile && t.Train.SourceUpdatedAt.Equal(copy.Train.SourceUpdatedAt) && now.Before(t.Train.ValidUntil)
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
	if history == nil {
		return
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

// countOwnForecasts records how many call values the own-forecast work produced.
func (r *metroRuntime) countOwnForecasts(track metroTrack) {
	atomic.AddInt64(&r.ownQueries, 1)
	for n := range track.Train.Calls {
		own := track.Train.Calls[n].OwnPrediction
		if own == nil || own.ModelVersion == nil {
			continue
		}
		if strings.HasPrefix(*own.ModelVersion, "metro-schedule-prior-v1:") {
			atomic.AddInt64(&r.ownSchedule, 1)
			continue
		}
		atomic.AddInt64(&r.ownHistorical, 1)
	}
}
