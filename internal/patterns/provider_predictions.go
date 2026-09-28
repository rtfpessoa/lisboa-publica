package patterns

import "time"

func freshPrediction(p ProviderPrediction, now time.Time) bool {
	at := p.ReceivedAt
	if p.SourceAt != nil {
		at = *p.SourceAt
	}
	if at.IsZero() || p.ValidUntil.IsZero() {
		return false
	}
	age := now.Sub(at)
	if age < 0 || age > 90*time.Second {
		return false
	}
	return p.ExpectedAt.After(now) && p.ExpectedAt.Sub(now) <= 2*time.Hour && p.ValidUntil.After(now)
}

func (p *providerState) prediction(track *providerTrack, path ProviderJourney, index int, now time.Time) *ProviderPrediction {
	v, visit := track.Previous, path.Visits[index]
	if !p.uniquePredictionJourney(v) {
		return nil
	}
	var found *ProviderPrediction
	for _, candidate := range p.Predictions {
		if !freshPrediction(candidate, now) || !predictionIdentityMatches(candidate, v, visit) {
			continue
		}
		if !predictionVisitMatches(candidate, path, visit) {
			continue
		}
		if found != nil {
			return nil
		}
		value := candidate
		found = &value
	}
	return found
}

func (p *providerState) uniquePredictionJourney(v Observation) bool {
	matches := 0
	for _, other := range p.Tracks {
		if other.Previous.Route == v.Route && other.Previous.Trip == v.Trip {
			matches++
		}
	}
	return matches == 1
}

func optionalPredictionIdentity(actual, expected string) bool {
	return actual == "" || actual == expected
}

func predictionIdentityMatches(p ProviderPrediction, v Observation, visit ProviderVisit) bool {
	if p.Stop != visit.Stop || p.Route != v.Route || p.Trip != v.Trip {
		return false
	}
	checks := []bool{optionalPredictionIdentity(p.Vehicle, v.ID), optionalPredictionIdentity(p.Plan, v.Plan), optionalPredictionIdentity(p.ServiceDate, v.OperationalDate)}
	for _, valid := range checks {
		if !valid {
			return false
		}
	}
	return true
}

func predictionVisitMatches(p ProviderPrediction, path ProviderJourney, visit ProviderVisit) bool {
	if p.Sequence != nil {
		return *p.Sequence == visit.Sequence
	}
	// Unsequenced predictions cannot select between repeated visits.
	matches := 0
	for _, candidate := range path.Visits {
		if candidate.Stop == visit.Stop {
			matches++
		}
	}
	return matches == 1
}
