package patterns

import "time"

type providerForecastChain struct {
	track                 *providerTrack
	path                  ProviderJourney
	origin                int
	profile, mode, reason string
	at                    time.Time
	anchor                *ProviderPrediction
	components            []Component
}

func (b *providerForecastBuilder) beginTrackForecasts(track *providerTrack) (providerForecastChain, bool) {
	g := track.Group
	chain := providerForecastChain{track: track, path: b.state.Paths[track.Path]}
	if !g.Active || len(g.Signals) == 0 || !freshObservation(track.Previous, b.now) {
		return chain, false
	}
	last := g.Signals[len(g.Signals)-1]
	chain.origin = providerSignalIndex(chain.path, last)
	if chain.origin < 0 || chain.origin+1 >= len(chain.path.Visits) {
		return chain, false
	}
	chain.profile = providerProfile(chain.path, b.config)
	b.state.Engine.Profile = chain.profile
	chain.at = last.L.Add(last.U.Sub(last.L) / 2)
	chain.anchor = b.state.prediction(track, chain.path, chain.origin+1, b.now)
	chain.mode = providerMode
	if chain.anchor == nil {
		chain.mode = "published-stop+published-stop-components/v1"
	} else {
		chain.at = chain.anchor.ExpectedAt
	}
	chain.components = []Component{}
	return chain, true
}

func (b *providerForecastBuilder) appendTrackForecasts(out []Forecast, track *providerTrack) []Forecast {
	chain, valid := b.beginTrackForecasts(track)
	if !valid {
		return out
	}
	for target := chain.origin + 1; target < len(chain.path.Visits); target++ {
		if len(out) >= maxProviderCalls/2 {
			b.state.Engine.Limited = true
			break
		}
		out = append(out, b.trackForecast(&chain, target))
	}
	return out
}

func (b *providerForecastBuilder) trackForecast(chain *providerForecastChain, target int) Forecast {
	own, reason := b.advanceTrackComponents(chain, target)
	details := []Component{}
	if own != nil {
		if b.componentRefs+len(chain.components) > maxProviderComponentRefs/2 {
			own, reason = nil, "component_summary_limit"
			b.state.Engine.Limited = true
		} else {
			details = chain.components
			b.componentRefs += len(details)
		}
	}
	f := b.trackForecastPoint(chain, target, own, reason, details)
	b.state.Engine.calibrateForecast(&f, b.now, b.config)
	return f
}

func (b *providerForecastBuilder) advanceTrackComponents(chain *providerForecastChain, target int) (*time.Time, string) {
	if chain.anchor != nil && target == chain.origin+1 {
		return nil, "official_anchor_only"
	}
	if chain.reason != "" {
		return nil, chain.reason
	}
	from, visit := chain.path.Visits[target-1], chain.path.Visits[target]
	q := componentRequest{from.Stop, visit.Stop, visitPlatform(from), chain.path.Route, chain.path.Direction, "unknown", chain.at, b.now}
	component := b.state.Engine.componentSummary(q, b.config)
	if component.Samples == 0 {
		chain.reason = "insufficient_compatible_components"
	} else {
		chain.at = chain.at.Add(time.Duration(component.Seconds * float64(time.Second)))
		chain.components = append(chain.components, component)
		if !chain.at.After(b.now) {
			chain.reason = "estimated_arrival_already_past"
		}
	}
	if chain.reason != "" {
		return nil, chain.reason
	}
	value := chain.at
	return &value, ""
}

func (b providerForecastBuilder) trackForecastPoint(chain *providerForecastChain, target int, own *time.Time, reason string, details []Component) Forecast {
	visit, g := chain.path.Visits[target], chain.track.Group
	source := chain.track.Previous.ObservedAt
	f := Forecast{ProviderTrip: chain.track.Previous.Trip, ID: digest([]any{g.ID, visit.Stop, visit.Sequence, b.now, "onward"})[:24], Result: "pending", Episode: g.ID, IssuedAt: b.now, Route: chain.path.Route, Direction: chain.path.Direction, Stop: visit.Stop, StopName: visit.Name, DestinationName: chain.path.Headsign, Platform: visitPlatform(visit), Train: chain.track.Previous.ID, Function: "onward", Mode: chain.mode, Profile: chain.profile, Condition: "unknown", SourceAt: &source, OwnAt: own, Unavailable: reason, Components: details}
	if prediction := b.state.prediction(chain.track, chain.path, target, b.now); prediction != nil {
		point := prediction.ExpectedAt
		f.OfficialAt, f.SourceAt = &point, prediction.SourceAt
	}
	return f
}
