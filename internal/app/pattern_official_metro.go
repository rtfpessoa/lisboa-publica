package app

import (
	"encoding/json"
	"lisboapublica/internal/patterns"
	"time"
)

func metroOfficialCache(state *State, stop string, now time.Time) []patterns.Forecast {
	out := []patterns.Forecast{}
	data := state.Metro
	if !metroOfficialCacheReady(data, stop, now) {
		return out
	}
	var rows []patterns.Row
	raw, _ := json.Marshal(data.Waits)
	if json.Unmarshal(raw, &rows) != nil {
		return out
	}
	topology := metroTopology(data, state.Static["metro"])
	selection := metroOfficialSelection{rows: map[string]patterns.Forecast{}, ambiguous: map[string]bool{}}
	for _, row := range rows {
		if row.Stop != stop {
			continue
		}
		f, valid := metroCacheRow(data, topology, row, now)
		if valid {
			selection.add(f)
		}
	}
	for key, f := range selection.rows {
		if !selection.ambiguous[key] {
			out = append(out, f)
		}
	}
	return out
}

func metroOfficialCacheReady(data *MetroData, stop string, now time.Time) bool {
	if data == nil || stop == "" || data.Status.Status != "ok" {
		return false
	}
	return data.Status.CheckedAt != nil && freshOfficialClock(*data.Status.CheckedAt, now)
}

func metroCacheRoute(topology patterns.Topology, row patterns.Row) string {
	route := ""
	for _, path := range topology.Patterns {
		if path.Direction != row.Destination || !metroPathContains(path, row.Stop) {
			continue
		}
		if route != "" && route != path.Route {
			return "metro:published-destination:" + row.Destination
		}
		route = path.Route
	}
	if route == "" {
		route = "metro:published-destination:" + row.Destination
	}
	return route
}

func metroPathContains(path patterns.Pattern, stop string) bool {
	for _, visit := range path.Stops {
		if visit == stop {
			return true
		}
	}
	return false
}

func metroCacheRow(data *MetroData, topology patterns.Topology, row patterns.Row, now time.Time) (patterns.Forecast, bool) {
	train, point, source := patterns.MetroOfficialPoint(row, now)
	if point == nil {
		return patterns.Forecast{}, false
	}
	p := patterns.ProviderPrediction{ID: row.Stop + "|" + row.Platform + "|" + row.Destination + "|" + train, Stop: row.Stop, Route: metroCacheRoute(topology, row), Trip: train, ExpectedAt: *point, SourceAt: source}
	f := officialCacheForecast(p, now)
	f.Platform, f.Direction = row.Platform, row.Destination
	for _, station := range data.Stations {
		if station.ID == row.Stop {
			f.StopName = station.Name
		}
		if station.ID == destinations[row.Destination] {
			f.DestinationName = station.Name
		}
	}
	return f, true
}

// Reject conflicting simultaneous reports rather than selecting by iteration order.
type metroOfficialSelection struct {
	rows      map[string]patterns.Forecast
	ambiguous map[string]bool
}

func (s *metroOfficialSelection) add(f patterns.Forecast) {
	key := f.Platform + "|" + f.Direction
	old, exists := s.rows[key]
	if !exists || f.SourceAt.After(*old.SourceAt) {
		s.rows[key], s.ambiguous[key] = f, false
	} else if f.SourceAt.Equal(*old.SourceAt) && conflictingOfficialValues(old, f) {
		s.ambiguous[key] = true
	}
}

func conflictingOfficialValues(a, b patterns.Forecast) bool {
	return a.Train != b.Train || !a.OfficialAt.Equal(*b.OfficialAt)
}
