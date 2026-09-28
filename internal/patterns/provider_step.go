package patterns

import (
	"strings"
	"time"
)

func (p *providerState) step(r ProviderReceipt, c Config, sampled, replaying bool) {
	p.prepareProviderInput(r)
	p.retainProviderPaths(r)
	if r.Gap {
		p.reset()
	}
	for id, withdraw := range r.Cuts {
		p.cut(id, withdraw, c)
	}
	if r.Partial {
		p.replaceProviderPredictions(r)
	} else {
		p.observeFullProvider(r, c, sampled)
	}
	if sampled {
		p.finishProviderSample(r, c, replaying)
	}
}

func (p *providerState) prepareProviderInput(r ProviderReceipt) {
	p.LastInputAt = r.ReceivedAt
	p.Engine.Limited = p.Engine.Limited || r.Limited
	p.Engine.Outcomes = []Forecast{}
	for id, prediction := range p.Predictions {
		if !prediction.ValidUntil.After(r.ReceivedAt) {
			delete(p.Predictions, id)
		}
	}
	for stop, at := range p.PredictionSamples {
		if r.ReceivedAt.Sub(at) > 24*time.Hour {
			delete(p.PredictionSamples, stop)
		}
	}
}

func (p *providerState) retainProviderPaths(r ProviderReceipt) {
	needed := map[string]bool{}
	for _, row := range r.Rows {
		needed[row.Journey] = true
	}
	for _, track := range p.Tracks {
		needed[track.Path] = true
	}
	// Active tracks keep exact definitions across static revisions.
	for id := range p.Paths {
		if !needed[id] && len(p.Paths)+len(r.Journeys) > maxProviderPaths {
			delete(p.Paths, id)
		}
	}
	for id, path := range r.Journeys {
		if !validProviderPath(r.Operator, id, path) {
			continue
		}
		if _, exists := p.Paths[id]; exists || len(p.Paths) < maxProviderPaths {
			p.Paths[id] = path
		} else {
			p.Engine.Limited = true
		}
	}
}

func predictionNamespaceMatches(prediction ProviderPrediction, stop string) bool {
	if strings.HasPrefix(prediction.ID, "feed:") {
		return stop == "*"
	}
	return prediction.Stop == stop
}

func (p *providerState) replaceProviderPredictions(r ProviderReceipt) {
	for key, prediction := range p.Predictions {
		if predictionNamespaceMatches(prediction, r.PredictionStop) {
			delete(p.Predictions, key)
		}
	}
	if r.Error != "" {
		return
	}
	for _, prediction := range r.Predictions {
		if len(p.Predictions) >= maxProviderCalls {
			p.Engine.Limited = true
			break
		}
		if providerPredictionNamespaceAdmitted(prediction, r) {
			p.Predictions[prediction.ID] = prediction
		}
	}
}

func providerPredictionNamespaceAdmitted(prediction ProviderPrediction, r ProviderReceipt) bool {
	if prediction.Stop != r.PredictionStop && r.PredictionStop != "*" {
		return false
	}
	return strings.HasPrefix(prediction.Route, r.Operator+":")
}

func (p *providerState) fullProviderGap(r ProviderReceipt) bool {
	if r.Error != "" {
		return true
	}
	if p.LastReceipt.IsZero() {
		return false
	}
	return r.ReceivedAt.Sub(p.LastReceipt) > 60*time.Second || !r.ReceivedAt.After(p.LastReceipt)
}

func (p *providerState) observeFullProvider(r ProviderReceipt, c Config, sampled bool) {
	if p.fullProviderGap(r) {
		p.reset()
	}
	seen, duplicates := providerRowIdentities(r.Rows)
	for id := range p.Tracks {
		if !seen[id] || duplicates[id] {
			p.cut(id, duplicates[id], c)
		}
	}
	context := providerSample{r, c, sampled, duplicates}
	for _, row := range r.Rows {
		p.observeProviderRow(row, context)
	}
	p.LastReceipt, p.FullError = r.ReceivedAt, r.Error
}

func providerRowIdentities(rows []Observation) (map[string]bool, map[string]bool) {
	seen, duplicates := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		if seen[row.ID] {
			duplicates[row.ID] = true
		}
		seen[row.ID] = true
	}
	return seen, duplicates
}
