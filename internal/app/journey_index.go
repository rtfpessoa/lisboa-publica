package app

import (
	"lisboapublica/internal/api"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type journeyIndex struct {
	stops      journeyStopIndex
	directions map[string][]api.BoardDirection
	direction  map[*ScheduledTrip]*string
	routes     map[string]*journeyRouteCatalog
}

// Index lifetime follows the immutable schedule, not a browser or a live refresh.
type journeyIndexCache struct {
	once  sync.Once
	index *journeyIndex
}

func (d *StaticData) journeys(operator string) *journeyIndex {
	d.Schedule.popupIndex.once.Do(func() { d.Schedule.popupIndex.index = indexJourneys(d, operator) })
	return d.Schedule.popupIndex.index
}
func journeyTimes(t *ScheduledTrip) []StopTime {
	if len(t.JourneyTimes) > 0 {
		return t.JourneyTimes
	}
	return localJourneyTimes(t)
}
func lineForTrip(d *StaticData, operator string, t *ScheduledTrip) (string, string, string) {
	id := qualify(operator, t.Route)
	name, color := t.Route, "#666666"
	for _, r := range d.Routes {
		if r.Id == id || operator == "cm" && r.SourceId == t.Route {
			name, color = r.ShortName, r.Color
			break
		}
	}
	if operator == "metro" {
		lines := map[string]string{"Az": "Azul", "Am": "Amarela", "Vd": "Verde", "Vm": "Vermelha"}
		if n := lines[name]; n != "" {
			return "metro:line:" + strings.ToLower(n), "Linha " + n, color
		}
	}
	return id, name, color
}
func tripDestination(s *Schedule, t *ScheduledTrip) string {
	if t.Headsign != "" {
		return t.Headsign
	}
	times := journeyTimes(t)
	if len(times) > 0 {
		return s.StopNames[times[len(times)-1].Stop]
	}
	return ""
}

type journeyCatalogBuilder struct {
	data            *StaticData
	operator        string
	index           *journeyIndex
	representatives map[string][]*ScheduledTrip
}

func indexJourneys(d *StaticData, operator string) *journeyIndex {
	idx := &journeyIndex{directions: map[string][]api.BoardDirection{}, direction: map[*ScheduledTrip]*string{}, routes: map[string]*journeyRouteCatalog{}}
	builder := journeyCatalogBuilder{data: d, operator: operator, index: idx, representatives: map[string][]*ScheduledTrip{}}
	for _, t := range orderedJourneyTrips(d.Schedule) {
		builder.addTrip(t)
	}
	return idx
}
func orderedJourneyTrips(s *Schedule) []*ScheduledTrip {
	trips := make([]*ScheduledTrip, 0, len(s.Trips))
	for n := range s.Trips {
		trips = append(trips, &s.Trips[n])
	}
	sort.Slice(trips, func(i, j int) bool {
		a, b := journeyVisitCount(trips[i]), journeyVisitCount(trips[j])
		if a == b {
			return trips[i].ID < trips[j].ID
		}
		return a > b
	})
	return trips
}
func (b *journeyCatalogBuilder) addTrip(t *ScheduledTrip) {
	route := b.route(t)
	direction := b.tripDirection(t, route.line)
	if b.operator == "metro" {
		b.index.direction[t] = direction
	}
	b.addDirection(t, api.BoardDirection{LineKey: route.line, LineName: route.name, Color: route.color, DirectionKey: direction})
}
func (b *journeyCatalogBuilder) tripDirection(t *ScheduledTrip, line string) *string {
	var key *string
	if b.operator == "metro" && len(journeyTimes(t)) >= 2 {
		key = b.metroDirection(t, line)
	} else if t.Direction != nil {
		route := b.index.routes[t.Route]
		if route.direction[*t.Direction] == nil {
			route.direction[*t.Direction] = ptr(line + ":direction:" + strconv.Itoa(*t.Direction))
		}
		key = route.direction[*t.Direction]
	}
	return key
}
func (b *journeyCatalogBuilder) metroDirection(t *ScheduledTrip, line string) *string {
	matches := []*ScheduledTrip{}
	for _, r := range b.representatives[line] {
		if sameMetroOrientation(b.data.Schedule, journeyTimes(t), journeyTimes(r)) {
			matches = append(matches, r)
		}
	}
	var key *string
	if len(matches) == 1 {
		key = b.index.direction[matches[0]]
	}
	if len(matches) == 0 {
		key = ptr(line + ":towards:" + b.terminal(t))
		b.representatives[line] = append(b.representatives[line], t)
	}
	return key
}
func (b *journeyCatalogBuilder) terminal(t *ScheduledTrip) string {
	visits := journeyTimes(t)
	terminal := visits[len(visits)-1].Stop
	if parent := b.data.Schedule.Parents[terminal]; parent != "" {
		terminal = parent
	}
	return terminal
}
func (b *journeyCatalogBuilder) addDirection(t *ScheduledTrip, dir api.BoardDirection) {
	for _, known := range b.index.directions[dir.LineKey] {
		if equalDirection(known.DirectionKey, dir.DirectionKey) {
			return
		}
	}
	dir.Label = "Sentido não identificado"
	if dir.DirectionKey != nil {
		dir.Label = tripDestination(b.data.Schedule, t)
	}
	b.index.directions[dir.LineKey] = append(b.index.directions[dir.LineKey], dir)
}
func equalDirection(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// Child platforms can differ between variants. Require the entire short sequence
// to occur in the same order in the canonical route, including repeated visits.
func sameMetroOrientation(s *Schedule, a, b []StopTime) bool {
	if len(a) < 2 || len(a) > len(b) {
		return false
	}
	parent := func(id string) string {
		if p := s.Parents[id]; p != "" {
			return p
		}
		return id
	}
	n := 0
	for _, v := range b {
		if parent(v.Stop) == parent(a[n].Stop) {
			n++
			if n == len(a) {
				return true
			}
		}
	}
	return false
}

type journeyRouteCatalog struct {
	line, name, color string
	direction         [2]*string
}

func (b *journeyCatalogBuilder) route(trip *ScheduledTrip) *journeyRouteCatalog {
	route := b.index.routes[trip.Route]
	if route == nil {
		line, name, color := lineForTrip(b.data, b.operator, trip)
		route = &journeyRouteCatalog{line: line, name: name, color: color}
		b.index.routes[trip.Route] = route
	}
	return route
}

func (index *journeyIndex) lineFor(trip *ScheduledTrip) string { return index.routes[trip.Route].line }
func (index *journeyIndex) directionFor(trip *ScheduledTrip) *string {
	if direction, found := index.direction[trip]; found {
		return direction
	}
	if trip.Direction == nil {
		return nil
	}
	return index.routes[trip.Route].direction[*trip.Direction]
}

func popupTripMatches(data *StaticData, operator, qualified string) []*ScheduledTrip {
	id := strings.TrimPrefix(qualified, operator+":")
	matches := []*ScheduledTrip{}
	for n := range data.Schedule.Trips {
		trip := &data.Schedule.Trips[n]
		if trip.ID == id {
			matches = append(matches, trip)
		}
	}
	return matches
}
