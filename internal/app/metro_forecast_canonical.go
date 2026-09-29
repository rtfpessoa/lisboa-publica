package app

import (
	"lisboapublica/internal/api"
	"sort"
	"time"
)

func canonicalMetroForecastCalls(calls []api.StopCall) []api.StopCall {
	// Source row identity and anonymous slot identity are deliberately distinct.
	sort.Slice(calls, func(i, j int) bool { return calls[i].Id < calls[j].Id })
	out := mergeMetroForecastCalls(calls)
	markMetroForecastConflicts(out)
	distinguishMetroForecastIDs(out)
	return out
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
