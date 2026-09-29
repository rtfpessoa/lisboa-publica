package app

import "sort"

// Revisions are platform-specific. Only exact station-level equivalence can
// supply movement/event support; uncertain stations retain separate forecasts.
func latestMetroPoints(points []metroPoint) (map[string]metroPoint, bool) {
	platforms, bad := latestMetroPlatformPoints(points)
	current := map[string]metroPoint{}
	conflicts := map[string]bool{}
	keys := []string{}
	for key := range platforms {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := platforms[key]
		if bad[key] {
			conflicts[p.Stop] = true
			continue
		}
		if mergeMetroStationPoint(current, p) {
			conflicts[p.Stop] = true
		}
	}
	for stop := range conflicts {
		delete(current, stop)
	}
	return current, len(conflicts) > 0
}

func latestMetroPlatformPoints(points []metroPoint) (map[string]metroPoint, map[string]bool) {
	platforms := map[string]metroPoint{}
	bad := map[string]bool{}
	for _, p := range points {
		key := p.Stop + "|" + p.Platform
		old, exists := platforms[key]
		if !exists || p.Clock.After(old.Clock) {
			platforms[key] = p
			bad[key] = false
			continue
		}
		if !p.Clock.Equal(old.Clock) {
			continue
		}
		if metroPlatformValueConflict(old, p) {
			bad[key] = true
		}
		if old.Seconds == nil && p.Seconds != nil {
			platforms[key] = p
		}
	}
	return platforms, bad
}

func metroPlatformValueConflict(old, p metroPoint) bool {
	return old.Seconds != nil && p.Seconds != nil && !equalMetroSeconds(old.Seconds, p.Seconds)
}

// Return a conflict without allowing an absent platform value to erase support.
func mergeMetroStationPoint(current map[string]metroPoint, p metroPoint) bool {
	old, exists := current[p.Stop]
	if exists && old.Seconds != nil && p.Seconds == nil {
		return false
	}
	if !exists || old.Seconds == nil {
		current[p.Stop] = p
		return false
	}
	return !old.Clock.Equal(p.Clock) || !equalMetroSeconds(old.Seconds, p.Seconds)
}
