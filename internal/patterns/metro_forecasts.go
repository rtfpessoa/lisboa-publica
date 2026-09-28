package patterns

import (
	"sort"
	"strings"
	"time"
)

type metroForecastBuilder struct {
	engine   *engine
	receipt  Receipt
	topology Topology
	config   Config
	names    map[string]string
}

type metroForecastPoint struct {
	target, platform, function, reason string
	official                           *official
	own                                *time.Time
	validUntil                         *time.Time
	components                         []Component
}

func (e *engine) forecasts(r Receipt, t Topology, c Config) []Forecast {
	b := metroForecastBuilder{e, r, t, c, map[string]string{}}
	for _, station := range t.Stations {
		b.names[station.ID] = station.Name
	}
	keys := make([]string, 0, len(e.Groups))
	for key := range e.Groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []Forecast{}
	for _, key := range keys {
		out = append(out, b.groupForecasts(e.Groups[key])...)
	}
	return b.appendColdStart(out)
}

func (b metroForecastBuilder) makeForecast(g *group, p metroForecastPoint) Forecast {
	now := b.receipt.ReceivedAt
	id := digest([]string{g.ID, g.Train, g.Direction, g.Route, p.platform, p.target, p.function, now.Format(time.RFC3339Nano)})[:24]
	f := Forecast{ID: id, Episode: g.ID, IssuedAt: now, Route: g.Route, Direction: g.Direction, Stop: p.target, StopName: b.names[p.target], DestinationName: b.names[b.topology.destination(g.Direction)], Platform: p.platform, Train: g.Train, Function: p.function, Mode: forecastMode, Profile: b.engine.Profile, Condition: b.receipt.condition(g.Route), OwnAt: p.own, OwnValidUntil: p.validUntil, Unavailable: p.reason, Components: append([]Component{}, p.components...), Result: "pending"}
	if p.official != nil {
		f.OfficialAt, f.SourceAt = &p.official.At, &p.official.Source
	}
	b.calibrate(&f)
	return f
}

func (b metroForecastBuilder) calibrate(f *Forecast) {
	b.engine.calibrateForecast(f, b.receipt.ReceivedAt, b.config)
}

func waitingForecastKey(f Forecast) string {
	return f.Stop + "|" + f.Platform + "|" + f.Train + "|" + f.Direction
}

func (b metroForecastBuilder) appendColdStart(out []Forecast) []Forecast {
	seen := map[string]bool{}
	for _, f := range out {
		if f.Function == "waiting" {
			seen[waitingForecastKey(f)] = true
		}
	}
	for _, row := range b.receipt.Rows {
		f, valid := b.coldStartForecast(row)
		if !valid {
			continue
		}
		key := waitingForecastKey(f)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

func (b metroForecastBuilder) coldStartForecast(row Row) (Forecast, bool) {
	now := b.receipt.ReceivedAt
	slot := firstFutureSlot(row, now)
	if slot < 0 {
		return Forecast{}, false
	}
	train := strings.TrimSpace([]string{row.Train, row.Train2, row.Train3}[slot])
	route := b.topology.route(row.Stop, row.Destination)
	value := officialFor(b.receipt.Rows, row.Stop, train, row.Destination, now)
	if value == nil || !value.First {
		return Forecast{}, false
	}
	g := &group{Train: train, Route: route, Direction: row.Destination}
	reason := "association_not_supported"
	if supported := b.engine.Groups[groupKey(groupIdentity{Train: train, Direction: row.Destination, Route: route})]; supported != nil && supported.Active {
		g, reason = supported, "official_anchor_only"
	}
	p := metroForecastPoint{target: row.Stop, platform: row.Platform, function: "waiting", official: value, reason: reason}
	return b.makeForecast(g, p), true
}
