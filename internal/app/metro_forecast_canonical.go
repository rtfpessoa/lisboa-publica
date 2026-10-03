package app

import (
	"lisboapublica/internal/api"
	"sort"
	"time"
)

func canonicalMetroForecastCalls(calls []api.StopCall) []api.StopCall {
	// Merge first so duplicate identity keeps the caller's (path) order, then a
	// stable total order restores published line order. Merge, conflict marking and
	// id distinguishing stay id-based and independent of this order.
	out := mergeMetroForecastCalls(calls)
	sortMetroForecastCalls(out)
	markMetroForecastConflicts(out)
	distinguishMetroForecastIDs(out)
	return out
}

// sortMetroForecastCalls orders visits by their position in the published path.
// Local-only calls carry no path sequence (zero) and fall back to the same total
// order so repeated reads of one publication stay byte-identical.
func sortMetroForecastCalls(calls []api.StopCall) {
	sort.SliceStable(calls, func(i, j int) bool {
		a, b := calls[i], calls[j]
		if a.StopSequence != b.StopSequence {
			return a.StopSequence < b.StopSequence
		}
		if a.StopName != b.StopName {
			return a.StopName < b.StopName
		}
		at, bt := metroCallOrderTime(a), metroCallOrderTime(b)
		if !at.Equal(bt) {
			return at.Before(bt)
		}
		if ap, bp := metroCallPlatform(a), metroCallPlatform(b); ap != bp {
			return ap < bp
		}
		return a.Id < b.Id
	})
}
func metroCallOrderTime(c api.StopCall) time.Time {
	if c.Arrival.At != nil {
		return *c.Arrival.At
	}
	if c.Arrival.Prediction != nil {
		return c.Arrival.Prediction.At
	}
	if c.Arrival.Inferred != nil {
		return c.Arrival.Inferred.At
	}
	return time.Time{}
}
func metroCallPlatform(c api.StopCall) string {
	if c.MetroForecast != nil && len(c.MetroForecast.Platforms) > 0 {
		return c.MetroForecast.Platforms[0].Platform
	}
	return ""
}

func metroForecastMergeKey(c api.StopCall) (string, bool) {
	p := c.Arrival.Prediction
	if p == nil || p.SourceUpdatedAt == nil || c.MetroForecast == nil {
		return "", false
	}
	key := c.LineKey + "|" + textValue(c.DirectionKey) + "|" + c.Destination + "|" + c.StopId + "|" + textValue(c.MetroForecast.SourceReference) + "|" + p.SourceUpdatedAt.Format(time.RFC3339Nano) + "|" + p.At.Format(time.RFC3339Nano)
	if c.MetroForecast.SourceReference == nil {
		key += "|" + c.Id
	}
	return key, true
}

func mergeMetroForecastCalls(calls []api.StopCall) []api.StopCall {
	out := []api.StopCall{}
	indices := map[string]int{}
	for _, c := range calls {
		key, usable := metroForecastMergeKey(c)
		if !usable {
			out = append(out, c)
			continue
		}
		if n, exists := indices[key]; exists {
			out[n].MetroForecast.Platforms = append(out[n].MetroForecast.Platforms, c.MetroForecast.Platforms...)
			continue
		}
		indices[key] = len(out)
		out = append(out, c)
	}
	return out
}

func markMetroForecastConflicts(calls []api.StopCall) {
	counts := map[string]int{}
	for _, c := range calls {
		if c.MetroForecast != nil && c.MetroForecast.SourceReference != nil {
			counts[c.StopId+"|"+*c.MetroForecast.SourceReference]++
		}
	}
	for n := range calls {
		c := &calls[n]
		if c.MetroForecast == nil || c.MetroForecast.SourceReference == nil {
			continue
		}
		if counts[c.StopId+"|"+*c.MetroForecast.SourceReference] > 1 {
			c.MetroForecast.Limitations = append(c.MetroForecast.Limitations, "station_prediction_conflict")
		}
	}
}

func distinguishMetroForecastIDs(calls []api.StopCall) {
	ids := map[string]int{}
	for _, c := range calls {
		ids[c.Id]++
	}
	for n := range calls {
		if ids[calls[n].Id] > 1 && calls[n].Arrival.Prediction != nil {
			calls[n].Id += ":conflict:" + calls[n].Arrival.Prediction.At.Format(time.RFC3339Nano)
		}
	}
}
