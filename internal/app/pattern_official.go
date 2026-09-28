package app

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/patterns"
)

func (s *Server) patternReadFailure(v *patterns.View, err error) {
	v.Status = "degraded"
	v.Message = "Histórico experimental indisponível. As previsões oficiais recentes mantêm-se independentes."
	for i := range v.Forecasts {
		v.Forecasts[i].OwnAt = nil
		v.Forecasts[i].LowerAt = nil
		v.Forecasts[i].UpperAt = nil
		v.Forecasts[i].Unavailable = "archive_unavailable"
	}
	s.Log.Warn("experimental pattern read unavailable", zap.Error(err))
}
func officialCacheForecast(p patterns.ProviderPrediction, now time.Time) patterns.Forecast {
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(p.ID+"|"+p.ExpectedAt.Format(time.RFC3339Nano))))
	platform := ""
	if p.Sequence != nil {
		platform = "visit:" + strconv.Itoa(*p.Sequence)
	}
	return patterns.Forecast{ID: "cache-" + id[:24], Result: "live_official", IssuedAt: now, Route: p.Route, Direction: "unassociated", Stop: p.Stop, Platform: platform, Train: p.Trip, Function: "waiting", Mode: "official-cache/v1", Profile: "official-cache/v1", Condition: "unknown", SourceAt: p.SourceAt, OfficialAt: &p.ExpectedAt, Unavailable: "association_not_supported", Components: []patterns.Component{}}
}

// Current official values stay visible without joining them into an earlier
// sampled case. This response-only fallback never changes issuance/calibration.
func appendOfficialCache(v *patterns.View, rows []patterns.Forecast) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].OfficialAt.Equal(*rows[j].OfficialAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].OfficialAt.Before(*rows[j].OfficialAt)
	})
	for _, f := range rows {
		if officialCacheRepresented(v.Forecasts, f) {
			continue
		}
		if len(v.Forecasts) >= 2000 {
			v.Message += " Resumo de previsões limitado pela capacidade."
			break
		}
		v.Forecasts = append(v.Forecasts, f)
	}
}

func officialCacheRepresented(rows []patterns.Forecast, f patterns.Forecast) bool {
	for _, old := range rows {
		if old.Function != "waiting" || old.OfficialAt == nil {
			continue
		}
		if old.Route != f.Route || old.Platform != f.Platform {
			continue
		}
		if old.Direction == f.Direction || f.Direction == "unassociated" {
			return true
		}
	}
	return false
}

func officialCacheEligible(p patterns.ProviderPrediction, operator, stop string, now time.Time) bool {
	if p.Stop != stop || !strings.HasPrefix(p.Route, operator+":") {
		return false
	}
	clock := p.ReceivedAt
	if p.SourceAt != nil {
		clock = *p.SourceAt
	}
	if !freshOfficialClock(clock, now) {
		return false
	}
	return futureOfficialPoint(p.ExpectedAt, p.ValidUntil, now)
}

func freshOfficialClock(clock, now time.Time) bool {
	return !clock.IsZero() && !clock.After(now) && now.Sub(clock) <= sourceFreshness
}

func futureOfficialPoint(point, validUntil, now time.Time) bool {
	return point.After(now) && point.Sub(now) <= 2*time.Hour && validUntil.After(now)
}

func providerOfficialCache(cache *Cache, state *State, operator, stop string, now time.Time) []patterns.Forecast {
	out := []patterns.Forecast{}
	static := state.Static[operator]
	if static == nil || stop == "" {
		return out
	}
	name := cachedPatternStopName(static, stop)
	out = append(out, sharedOfficialCache(state, static, officialStopSelection{operator, stop, name}, now)...)
	if operator == "cm" {
		out = append(out, cmOfficialCache(cache, static, stop, name, now)...)
	}
	return out
}

func cachedPatternStopName(static *StaticData, stop string) string {
	for _, row := range static.Stops {
		if row.Id == stop {
			return row.Name
		}
	}
	return ""
}

type officialStopSelection struct{ operator, stop, name string }

func sharedOfficialCache(state *State, static *StaticData, selection officialStopSelection, now time.Time) []patterns.Forecast {
	out := []patterns.Forecast{}
	data := predictionsFor(state, selection.operator)
	if data == nil || data.PlanID != static.PlanID {
		return out
	}
	for _, row := range data.Rows {
		source := row.SourceUpdatedAt
		p := patterns.ProviderPrediction{ID: row.Id, Route: row.RouteId, Trip: row.SourceTripId, Stop: row.StopId, Sequence: &row.StopSequence, ExpectedAt: row.ExpectedAt, ReceivedAt: row.CollectedAt, SourceAt: &source, ValidUntil: row.ValidUntil}
		if !officialCacheEligible(p, selection.operator, selection.stop, now) {
			continue
		}
		f := officialCacheForecast(p, now)
		f.StopName, f.DestinationName = selection.name, row.DestinationName
		out = append(out, f)
	}
	return out
}

// Register existing bounded stop interest without a synchronous source fetch.
func cmOfficialCache(cache *Cache, static *StaticData, stop, name string, now time.Time) []patterns.Forecast {
	out := []patterns.Forecast{}
	snapshot := cache.arrivals.request(stop, static, now)
	if snapshot.availability.CollectedAt == nil {
		return out
	}
	for _, row := range snapshot.rows {
		if row.Kind != "prediction" || row.ExpectedAt == nil || row.ValidUntil == nil {
			continue
		}
		p := patterns.ProviderPrediction{ID: row.Id, Route: row.RouteId, Trip: row.TripId, Stop: row.StopId, Sequence: row.StopSequence, ExpectedAt: *row.ExpectedAt, ReceivedAt: *snapshot.availability.CollectedAt, SourceAt: row.SourceUpdatedAt, ValidUntil: *row.ValidUntil}
		if !officialCacheEligible(p, "cm", stop, now) {
			continue
		}
		f := officialCacheForecast(p, now)
		f.StopName, f.DestinationName = name, row.Headsign
		out = append(out, f)
	}
	return out
}
