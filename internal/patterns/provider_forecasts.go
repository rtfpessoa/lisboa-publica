package patterns

import (
	"sort"
	"strconv"
	"time"
)

type providerForecastBuilder struct {
	state         *providerState
	now           time.Time
	config        Config
	componentRefs int
}

func (p *providerState) forecasts(now time.Time, c Config) []Forecast {
	b := providerForecastBuilder{state: p, now: now, config: c}
	out := []Forecast{}
	ids := []string{}
	for id := range p.Tracks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out = b.appendTrackForecasts(out, p.Tracks[id])
	}
	next := nextPublishedServices(out)
	b.addUnassociatedPredictions(next, out)
	return b.appendWaitingForecasts(out, next)
}

func forecastComparisonPoint(f Forecast) *time.Time {
	if f.OfficialAt != nil {
		return f.OfficialAt
	}
	return f.OwnAt
}

func earlierPublishedService(candidate, old Forecast, exists bool) bool {
	if !exists {
		return true
	}
	point, previous := forecastComparisonPoint(candidate), forecastComparisonPoint(old)
	return point != nil && (previous == nil || point.Before(*previous))
}

func nextPublishedServices(rows []Forecast) map[string]Forecast {
	next := map[string]Forecast{}
	for _, f := range rows {
		key := f.Route + "|" + f.Stop + "|" + f.Platform + "|" + f.Direction
		old, exists := next[key]
		if earlierPublishedService(f, old, exists) {
			next[key] = f
		}
	}
	return next
}

func representedProviderOfficial(rows []Forecast, p ProviderPrediction) bool {
	for _, f := range rows {
		if f.Stop == p.Stop && f.OfficialAt != nil && f.OfficialAt.Equal(p.ExpectedAt) {
			return true
		}
	}
	return false
}

func (b providerForecastBuilder) addUnassociatedPredictions(next map[string]Forecast, rows []Forecast) {
	for _, prediction := range b.state.Predictions {
		if !freshPrediction(prediction, b.now) || representedProviderOfficial(rows, prediction) {
			continue
		}
		f := b.unassociatedForecast(prediction)
		key := f.Route + "|" + f.Stop + "|" + f.Platform + "|unassociated"
		old, exists := next[key]
		if !exists || old.OfficialAt == nil || f.OfficialAt.Before(*old.OfficialAt) {
			next[key] = f
		}
	}
}

func (b providerForecastBuilder) unassociatedForecast(prediction ProviderPrediction) Forecast {
	sequence := ""
	if prediction.Sequence != nil {
		sequence = "visit:" + strconv.Itoa(*prediction.Sequence)
	}
	point := prediction.ExpectedAt
	profile := "reported-stop-unassociated-s" + strconv.Itoa(int(b.config.SampleInterval/time.Second)) + "-b" + strconv.Itoa(b.config.BinSeconds)
	return Forecast{ProviderTrip: prediction.Trip, ID: digest([]any{prediction.ID, b.now})[:24], Result: "pending", IssuedAt: b.now, Route: prediction.Route, Direction: "unassociated", Stop: prediction.Stop, Platform: sequence, Train: prediction.Trip, Mode: providerMode, Profile: profile, Condition: "unknown", SourceAt: prediction.SourceAt, OfficialAt: &point, Unavailable: "association_not_supported", Components: []Component{}}
}

func (b providerForecastBuilder) appendWaitingForecasts(out []Forecast, next map[string]Forecast) []Forecast {
	keys := []string{}
	for key := range next {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(out) >= maxProviderCalls {
			b.state.Engine.Limited = true
			break
		}
		f := next[key]
		f.Function, f.ID = "waiting", digest([]any{f.ID, "waiting"})[:24]
		b.state.Engine.calibrateForecast(&f, b.now, b.config)
		out = append(out, f)
	}
	return out
}
