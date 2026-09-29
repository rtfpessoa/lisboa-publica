package app

import (
	"fmt"
	"sort"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// Keep conflicting equal-clock values and independent platform clocks. Only a
// newer publication for the same platform can supersede its previous rows.
func metroLatestForecastPoints(points []metroPoint) []metroPoint {
	latest := map[string]time.Time{}
	for _, p := range points {
		key := p.Stop + "|" + p.Platform
		if p.Clock.After(latest[key]) {
			latest[key] = p.Clock
		}
	}
	out := []metroPoint{}
	seen := map[string]bool{}
	for _, p := range points {
		if p.Seconds == nil || !p.Clock.Equal(latest[p.Stop+"|"+p.Platform]) {
			continue
		}
		key := metroForecastPointID("", p)
		if !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return metroForecastPointID("", out[i]) < metroForecastPointID("", out[j]) })
	return out
}

func metroForecastPointID(scope string, p metroPoint) string {
	return fmt.Sprintf("metro:forecast:%s:%s:%s:%s:%d", scope, p.Stop, p.Platform, p.Clock.Format(time.RFC3339Nano), *p.Seconds)
}

// Display row identity is independent of ETA revisions and invented train IDs.
func metroForecastCallID(scope string, p metroPoint) string {
	return "metro:forecast:" + scope + ":" + p.Stop + ":" + p.Platform
}

func metroForecastEvidence(reference string, p metroPoint) *api.MetroForecastAssociation {
	at := p.Clock.Add(time.Duration(*p.Seconds) * time.Second)
	e := &api.MetroForecastAssociation{Method: "unknown", Anchors: []string{}, Platforms: []api.MetroPlatformForecast{{Platform: p.Platform, SourceUpdatedAt: p.Clock, At: &at, ValidUntil: p.Clock.Add(sourceFreshness)}}, Limitations: []string{}}
	if metroReference(reference) {
		e.SourceReference = ptr(reference)
		e.Method = "published"
	}
	return e
}

func metroPathOriginKnown(topology patterns.Topology, path patterns.Pattern) bool {
	for _, p := range topology.Patterns {
		if p.Route == path.Route && p.Direction == path.Direction && metroPathEmbeddings(path.Stops, p.Stops) == 1 && p.Stops[0] != path.Stops[0] {
			return false
		}
	}
	return true
}

func (r *metroRuntime) contextOwnPrediction(key, code string, now time.Time) *api.CallTimeEvidence {
	t := r.tracks[r.active[key]]
	if t == nil || t.Train.Association != "supported" || t.BarrierRevision != 0 {
		return nil
	}
	for n, stop := range t.Codes {
		if stop != code || n >= len(t.Train.Calls) {
			continue
		}
		p := t.Train.Calls[n].OwnPrediction
		if p != nil && p.ValidUntil != nil && now.Before(*p.ValidUntil) && !p.At.Before(now) {
			copy := *p
			return &copy
		}
	}
	return nil
}
