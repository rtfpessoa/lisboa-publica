package app

import (
	"lisboapublica/internal/api"
	"time"
)

// Each scoped consumer owns its projection; short shared computation reuse
// never shares mutable call/provenance slices between frames.
func cloneMetroForecastContexts(contexts []api.MetroForecastContext) []api.MetroForecastContext {
	out := append([]api.MetroForecastContext{}, contexts...)
	for n := range out {
		out[n].Calls = cloneMetroTrain(api.MetroTrain{Calls: out[n].Calls}).Calls
		for j := range out[n].Calls {
			c := &out[n].Calls[j]
			if c.MetroForecast == nil {
				continue
			}
			copy := *c.MetroForecast
			copy.Anchors = append([]string{}, copy.Anchors...)
			copy.Limitations = append([]string{}, copy.Limitations...)
			copy.Platforms = append([]api.MetroPlatformForecast{}, copy.Platforms...)
			if copy.CandidateReferences != nil {
				references := append([]string{}, (*copy.CandidateReferences)...)
				copy.CandidateReferences = &references
			}
			c.MetroForecast = &copy
		}
	}
	return out
}

func (r *metroRuntime) forecastSnapshotExpiry(contexts []api.MetroForecastContext, now time.Time) time.Time {
	expiry := now.Add(500 * time.Millisecond)
	for _, id := range r.active {
		if t := r.tracks[id]; t != nil {
			expiry = minTime(expiry, t.Train.ValidUntil)
		}
	}
	for _, m := range r.operational {
		expiry = minTime(expiry, m.PublishedAt.Add(sourceFreshness))
	}
	for _, context := range contexts {
		for _, call := range context.Calls {
			points := []*api.CallTimeEvidence{call.Arrival.Prediction, call.Departure.Prediction, call.OwnPrediction, call.OwnDeparturePrediction}
			for _, p := range points {
				if p == nil {
					continue
				}
				expiry = minTime(expiry, p.At)
				if p.ValidUntil != nil {
					expiry = minTime(expiry, *p.ValidUntil)
				}
			}
		}
	}
	return expiry
}
