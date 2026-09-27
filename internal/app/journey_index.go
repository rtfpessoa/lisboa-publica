package app

import (
	"lisboapublica/internal/api"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type journeyIndex struct {
	trips      map[string][]*ScheduledTrip
	stops      map[string][]*ScheduledTrip
	directions map[string][]api.BoardDirection
	direction  map[*ScheduledTrip]*string
	lines      map[*ScheduledTrip]string
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
	return t.Times
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
	idx := &journeyIndex{trips: map[string][]*ScheduledTrip{}, stops: map[string][]*ScheduledTrip{}, directions: map[string][]api.BoardDirection{}, direction: map[*ScheduledTrip]*string{}, lines: map[*ScheduledTrip]string{}}
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
		a, b := len(journeyTimes(trips[i])), len(journeyTimes(trips[j]))
		if a == b {
			return trips[i].ID < trips[j].ID
		}
		return a > b
	})
	return trips
}
func (b *journeyCatalogBuilder) addTrip(t *ScheduledTrip) {
	line, name, color := lineForTrip(b.data, b.operator, t)
	b.index.lines[t] = line
	key := qualify(b.operator, t.ID)
	b.index.trips[key] = append(b.index.trips[key], t)
	b.addStops(t)
	direction := b.tripDirection(t, line)
	b.index.direction[t] = direction
	b.addDirection(t, api.BoardDirection{LineKey: line, LineName: name, Color: color, DirectionKey: direction})
}
func (b *journeyCatalogBuilder) addStops(t *ScheduledTrip) {
	seen := map[string]bool{}
	for _, v := range t.Times {
		for _, stop := range []string{v.Stop, b.data.Schedule.Parents[v.Stop]} {
			if stop != "" && !seen[stop] {
				key := qualify(b.operator, stop)
				b.index.stops[key] = append(b.index.stops[key], t)
				seen[stop] = true
			}
		}
	}
}
func (b *journeyCatalogBuilder) tripDirection(t *ScheduledTrip, line string) *string {
	var key *string
	if b.operator == "metro" && len(journeyTimes(t)) >= 2 {
		key = b.metroDirection(t, line)
	} else if t.Direction != nil {
		key = ptr(line + ":direction:" + strconv.Itoa(*t.Direction))
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
