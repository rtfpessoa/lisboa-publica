package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"lisboapublica/internal/patterns"
)

type metroPlanBuilder struct {
	topology        patterns.Topology
	data            *MetroData
	static          *StaticData
	stations        map[string]MetroStation
	mapping, routes map[string]string
	seen            map[string]bool
}

func metroTopology(data *MetroData, static *StaticData) patterns.Topology {
	b := metroPlanBuilder{topology: patterns.Topology{Patterns: []patterns.Pattern{}, Segments: map[string]string{}, Stations: []patterns.Station{}}, data: data, static: static, stations: map[string]MetroStation{}, mapping: map[string]string{}, routes: map[string]string{}, seen: map[string]bool{}}
	b.addStations()
	if static == nil || static.Schedule == nil {
		return b.topology
	}
	b.mapStaticPlan()
	for _, trip := range static.Schedule.Trips {
		b.addTrip(trip)
	}
	b.finish()
	return b.topology
}

func (b *metroPlanBuilder) addStations() {
	for _, station := range b.data.Stations {
		b.stations[station.ID] = station
		b.topology.Stations = append(b.topology.Stations, patterns.Station{ID: station.ID, Name: station.Name})
	}
}

func (b *metroPlanBuilder) mapStaticPlan() {
	for _, stop := range b.static.Stops {
		id := metroStationID(b.static, b.data.Stations, b.stations, stop.Id)
		if _, ok := b.stations[id]; ok {
			b.mapping[stop.SourceId] = id
		}
	}
	routeIDs := metroRouteIDs(b.static)
	lines := map[string]string{"Az": "azul", "Am": "amarela", "Vd": "verde", "Vm": "vermelha"}
	for _, route := range b.static.Routes {
		b.routes[route.SourceId] = routeIDs[lines[route.ShortName]]
	}
}

func (b *metroPlanBuilder) tripDestination(trip ScheduledTrip) (string, string) {
	headsign, matches := "", 0
	for _, station := range b.data.Stations {
		if normalizeName(trip.Headsign) == normalizeName(station.Name) {
			headsign = station.ID
			matches++
		}
	}
	if matches != 1 {
		return "", ""
	}
	direction := ""
	for code, id := range destinations {
		if id == headsign {
			direction = code
		}
	}
	return headsign, direction
}

func (b *metroPlanBuilder) addTrip(trip ScheduledTrip) {
	route := b.routes[trip.Route]
	if route == "" {
		return
	}
	headsign, direction := b.tripDestination(trip)
	if direction == "" {
		return
	}
	p := patterns.Pattern{Route: route, Direction: direction, Destination: headsign, Stops: []string{}, Sequences: []int{}}
	times := append([]StopTime{}, localJourneyTimes(&trip)...)
	sort.Slice(times, func(i, j int) bool { return times[i].Sequence < times[j].Sequence })
	if !b.addVisits(&p, times) {
		return
	}
	b.addSegments(p, plannedSegmentEvidence(b.static, trip, times))
	raw, _ := json.Marshal(p)
	key := string(raw)
	if !b.seen[key] {
		b.seen[key] = true
		b.topology.Patterns = append(b.topology.Patterns, p)
	}
}

func (b *metroPlanBuilder) addVisits(p *patterns.Pattern, times []StopTime) bool {
	unique := map[string]bool{}
	for _, visit := range times {
		id := b.mapping[visit.Stop]
		if id == "" || unique[id] {
			return false
		}
		unique[id] = true
		p.Stops = append(p.Stops, id)
		p.Sequences = append(p.Sequences, visit.Sequence)
	}
	return len(p.Stops) >= 2
}

func (b *metroPlanBuilder) addSegments(p patterns.Pattern, evidence []string) {
	for i := 0; i+1 < len(p.Stops); i++ {
		key := patterns.SegmentID(patterns.Segment{Route: p.Route, Direction: p.Direction, Origin: p.Stops[i], Target: p.Stops[i+1]})
		prior, ok := b.topology.Segments[key]
		if ok && prior != evidence[i] {
			b.topology.Segments[key] = ""
		} else if !ok {
			b.topology.Segments[key] = evidence[i]
		}
	}
}

func (b *metroPlanBuilder) finish() {
	sort.Slice(b.topology.Patterns, func(i, j int) bool {
		a, _ := json.Marshal(b.topology.Patterns[i])
		z, _ := json.Marshal(b.topology.Patterns[j])
		return string(a) < string(z)
	})
	raw, _ := json.Marshal(struct {
		Plan     string
		Patterns []patterns.Pattern
		Segments map[string]string
	}{b.static.PlanID, b.topology.Patterns, b.topology.Segments})
	b.topology.Profile = fmt.Sprintf("metro-proxy-v2-%x", sha256.Sum256(raw))
}
