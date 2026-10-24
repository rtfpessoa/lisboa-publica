package patterns

import (
	"maps"
	"slices"
)

// cloneMetroEngine isolates a proposed sample from the admitted state. Keep
// mutable reference fields independent, including process-local recovery state.
func cloneMetroEngine(original *engine) *engine {
	copy := *original
	copy.ColdDays = maps.Clone(original.ColdDays)
	copy.RecoveryThrough = maps.Clone(original.RecoveryThrough)
	copy.Conditions = maps.Clone(original.Conditions)
	copy.ReportSeen = maps.Clone(original.ReportSeen)
	copy.Aggregates = maps.Clone(original.Aggregates)
	copy.Previous = maps.Clone(original.Previous)
	copy.Selections = maps.Clone(original.Selections)
	copy.DirtyDays = maps.Clone(original.DirtyDays)
	copy.Topology = cloneMetroTopology(original.Topology)
	copy.Cases = cloneMetroForecasts(original.Cases)
	copy.Live = cloneMetroForecasts(original.Live)
	copy.Outcomes = cloneMetroForecasts(original.Outcomes)
	copy.Groups = cloneMetroGroups(original.Groups)
	return &copy
}

func cloneMetroGroups(original map[string]*group) map[string]*group {
	if original == nil {
		return nil
	}
	copy := maps.Clone(original)
	for key, originalGroup := range original {
		if originalGroup == nil {
			continue
		}
		cloned := *originalGroup
		cloned.Signals = slices.Clone(originalGroup.Signals)
		cloned.Pairs = maps.Clone(originalGroup.Pairs)
		copy[key] = &cloned
	}
	return copy
}

func cloneMetroTopology(original Topology) Topology {
	copy := original
	copy.Segments = maps.Clone(original.Segments)
	copy.Stations = slices.Clone(original.Stations)
	copy.Patterns = slices.Clone(original.Patterns)
	for i := range copy.Patterns {
		copy.Patterns[i].Stops = slices.Clone(original.Patterns[i].Stops)
		copy.Patterns[i].Sequences = slices.Clone(original.Patterns[i].Sequences)
	}
	return copy
}

func cloneMetroForecasts(original []Forecast) []Forecast {
	copy := slices.Clone(original)
	for i := range copy {
		f := &copy[i]
		f.OwnValidUntil = cloneMetroPointer(f.OwnValidUntil)
		f.SourceAt = cloneMetroPointer(f.SourceAt)
		f.OfficialAt = cloneMetroPointer(f.OfficialAt)
		f.OwnAt = cloneMetroPointer(f.OwnAt)
		f.LowerAt = cloneMetroPointer(f.LowerAt)
		f.UpperAt = cloneMetroPointer(f.UpperAt)
		f.ReferenceLower = cloneMetroPointer(f.ReferenceLower)
		f.ReferenceUpper = cloneMetroPointer(f.ReferenceUpper)
		f.ErrorLower = cloneMetroPointer(f.ErrorLower)
		f.ErrorUpper = cloneMetroPointer(f.ErrorUpper)
		f.Components = slices.Clone(f.Components)
	}
	return copy
}

func cloneMetroPointer[T any](original *T) *T {
	if original == nil {
		return nil
	}
	copy := *original
	return &copy
}
