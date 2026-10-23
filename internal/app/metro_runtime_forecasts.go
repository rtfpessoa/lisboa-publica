package app

import (
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func (r *metroRuntime) projectOwn(data *MetroData, history *patterns.Service, now time.Time) {
	r.projectOwnWithClock(data, history, now, time.Now)
}

func (r *metroRuntime) projectOwnWithClock(data *MetroData, history *patterns.Service, now time.Time, clock func() time.Time) {
	r.mu.Lock()
	_ = r.bindMetroHistory(history)
	cut := r.fenceMetroForecasts(now)
	useHistory := r.metroForecastHistoryReady(history, cut)
	tracks := r.metroOwnForecastWork()
	r.mu.Unlock()
	r.prepareMetroForecastWork(tracks, history, now, useHistory)
	r.mu.Lock()
	defer r.mu.Unlock()
	// History lookups and lock acquisition may outlive the input evidence.
	now = clock().UTC()
	r.fenceMetroForecasts(now)
	if useHistory && !r.claimMetroSnapshot(cut) {
		discardMetroForecastHistory(tracks, now)
	}
	r.publishMetroForecastWork(data, tracks, now)
}

func (r *metroRuntime) metroForecastHistoryReady(history *patterns.Service, cut patterns.MetroCutSnapshot) bool {
	return history != nil && !r.historyCutMismatch && cut.Valid && !cut.Pending
}

func (r *metroRuntime) publishMetroForecastWork(data *MetroData, tracks []metroTrack, now time.Time) {
	for _, copy := range tracks {
		r.applyOwnForecastWork(copy, now)
	}
	r.forecastCacheUntil = time.Time{}
	data.Trains = r.current(now)
}
func (r *metroRuntime) prepareMetroForecastWork(tracks []metroTrack, history *patterns.Service, now time.Time, useHistory bool) {
	for n := range tracks {
		if useHistory {
			projectMetroOwnTrack(&tracks[n], history, now)
		} else {
			clearMetroOwnPredictions(&tracks[n])
		}
		projectMetroScheduled(&tracks[n], now)
		r.countOwnForecasts(tracks[n])
	}
}

func discardMetroForecastHistory(tracks []metroTrack, now time.Time) {
	for n := range tracks {
		clearMetroOwnPredictions(&tracks[n])
		projectMetroScheduled(&tracks[n], now)
	}
}

func (r *metroRuntime) metroOwnForecastWork() []metroTrack {
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
		arrival := copy.Train.Calls[n].OwnPrediction
		if arrival != nil && !metroCallPredictionFresh(arrival, track.Train.Association, now) {
			arrival = nil
		}
		departure := copy.Train.Calls[n].OwnDeparturePrediction
		if departure != nil && !metroCallPredictionFresh(departure, track.Train.Association, now) {
			departure = nil
		}
		track.Train.Calls[n].OwnPrediction = arrival
		track.Train.Calls[n].OwnDeparturePrediction = departure
	}
}
func metroOwnForecastWorkCurrent(t *metroTrack, copy metroTrack, now time.Time) bool {
	if t == nil || t.Train.Association != "supported" {
		return false
	}
	return t.Revision == copy.Revision && t.Profile == copy.Profile && t.Train.SourceUpdatedAt.Equal(copy.Train.SourceUpdatedAt) && t.Train.ValidUntil.Equal(copy.Train.ValidUntil) && now.Before(t.Train.ValidUntil) && reflect.DeepEqual(t.Points, copy.Points) && metroOwnForecastInputsMatch(t.Train.Calls, copy.Train.Calls)
}

// Checkpoint revisions can remain unchanged when archive writes are blocked.
// Fence asynchronous results against the actual visit evidence as well.
func metroOwnForecastInputsMatch(current, work []api.StopCall) bool {
	if len(current) != len(work) {
		return false
	}
	for n := range current {
		if current[n].Id != work[n].Id || !reflect.DeepEqual(current[n].Arrival, work[n].Arrival) || !reflect.DeepEqual(current[n].Departure, work[n].Departure) {
			return false
		}
	}
	return true
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
