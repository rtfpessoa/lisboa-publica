package patterns

// Checkpoints are bounded caches, not authority for retired archive days.
func validProviderCheckpoint(states map[string]*providerState) bool {
	if len(states) > len(operatorOrder)-1 {
		return false
	}
	for op, p := range states {
		if !validProviderState(op, p) {
			return false
		}
	}
	return true
}

func validProviderState(op string, p *providerState) bool {
	if !knownOperator(op) || op == "metro" || p == nil {
		return false
	}
	if !validProviderEngine(op, p.Engine) || !validProviderBuffers(p) {
		return false
	}
	for id, path := range p.Paths {
		if !validProviderPath(op, id, path) {
			return false
		}
	}
	return true
}

func validBoundedMap[K comparable, V any](values map[K]V, limit int) bool {
	return values != nil && len(values) <= limit
}

func validProviderEngine(op string, e *engine) bool {
	if e == nil || e.Operator != op || e.Capacity != maxProviderAggregates {
		return false
	}
	if !validProviderTraining(e) {
		return false
	}
	return len(e.Cases) <= maxProviderCalls
}

func validProviderTraining(e *engine) bool {
	checks := []bool{
		validBoundedMap(e.Aggregates, maxProviderAggregates),
		e.DirtyDays != nil,
		len(e.ColdDays) <= maxProviderAggregates,
		validBoundedMap(e.Selections, maxProviderAggregates),
		validBoundedMap(e.ReportSeen, maxProviderAggregates),
	}
	return allCheckpointChecks(checks)
}

func validProviderBuffers(p *providerState) bool {
	checks := []bool{
		validBoundedMap(p.Paths, maxProviderPaths),
		validBoundedMap(p.Tracks, maxProviderTracks),
		validBoundedMap(p.Predictions, maxProviderCalls),
		validBoundedMap(p.PredictionSamples, maxProviderCalls),
		len(p.PendingCuts) <= maxProviderTracks,
	}
	return allCheckpointChecks(checks)
}

func allCheckpointChecks(checks []bool) bool {
	for _, valid := range checks {
		if !valid {
			return false
		}
	}
	return true
}
