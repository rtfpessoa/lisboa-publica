package app

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"lisboapublica/internal/api"
)

// CMPath retains one verified published sequence per explicit plan/agency pattern.
// Shape references share the geometry held in the same immutable static revision.
type CMPath struct {
	ID     string        `json:"id"`
	Line   string        `json:"line"`
	Shape  string        `json:"shape"`
	Visits []CMPathVisit `json:"visits"`
}
type CMPathVisit struct {
	Stop     string `json:"stop"`
	Sequence int    `json:"sequence"`
}
type cmPattern struct {
	Path                             CMPath
	Representative, Route, Direction string
	Lookup                           map[int]int
	Ambiguous                        bool
}
type cmPatternTrip struct {
	Pattern *cmPattern
	Seen    []uint64
}
type cmPatternReader struct {
	Prefix   string
	Plan     *hubPlan
	Patterns map[string]*cmPattern
	Trips    map[string]*cmPatternTrip
	Lines    map[string]string
	Stops    map[string]string
	Shapes   map[string]bool
}

func readCMPatterns(a gtfsArchive, plan *hubPlan, shapes []api.RouteShape) ([]CMPath, error) {
	for _, name := range []string{"routes.txt", "stops.txt", "trips.txt", "stop_times.txt"} {
		if a[name] == nil {
			return nil, fmt.Errorf("CM pattern table unavailable")
		}
	}
	m := cmPatternReader{Prefix: "cm:[" + plan.ID + "][" + plan.Agency + "]", Plan: plan, Patterns: map[string]*cmPattern{}, Trips: map[string]*cmPatternTrip{}, Lines: map[string]string{}, Stops: map[string]string{}, Shapes: map[string]bool{}}
	for _, s := range shapes {
		m.Shapes[s.Id] = true
	}
	if err := m.read(a); err != nil {
		return nil, err
	}
	out := []CMPath{}
	for _, p := range m.Patterns {
		if !p.Ambiguous {
			out = append(out, p.Path)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (m *cmPatternReader) read(a gtfsArchive) error {
	tables := []struct {
		name  string
		visit func(map[string]string) error
	}{{"routes.txt", m.route}, {"stops.txt", m.stop}, {"trips.txt", m.trip}, {"stop_times.txt", m.representativeVisit}}
	var err error
	for _, table := range tables {
		if err = a.read(table.name, table.visit); err != nil {
			break
		}
	}
	if err == nil {
		m.preparePaths()
		err = a.read("stop_times.txt", m.verifyVisit)
	}
	if err == nil {
		m.verifyComplete()
	}
	return err
}
func (m *cmPatternReader) route(row map[string]string) error {
	id, line := row["route_id"], row["line_id"]
	if id == "" || line == "" || m.Lines[id] != "" {
		return fmt.Errorf("invalid CM pattern route")
	}
	m.Lines[strings.Clone(id)] = qualify("cm", line)
	return nil
}
func (m *cmPatternReader) stop(row map[string]string) error {
	id := row["stop_id"]
	if id == "" || m.Stops[id] != "" {
		return fmt.Errorf("invalid CM pattern stop")
	}
	id = strings.Clone(id)
	m.Stops[id] = id
	return nil
}
func (m *cmPatternReader) trip(row map[string]string) error {
	if err := m.validateTrip(row); err != nil {
		return err
	}
	p := m.pattern(row)
	id := strings.Clone(row["trip_id"])
	if p.Representative == "" || id < p.Representative {
		p.Representative = id
	}
	m.Trips[id] = &cmPatternTrip{Pattern: p}
	return nil
}
func (m *cmPatternReader) validateTrip(row map[string]string) error {
	id, pattern := row["trip_id"], row["pattern_id"]
	if id == "" || pattern == "" || len(m.Prefix+pattern) > 256 || m.Trips[id] != nil || m.Lines[row["route_id"]] == "" {
		return fmt.Errorf("invalid CM pattern trip")
	}
	direction := row["direction_id"]
	if direction != "" && direction != "0" && direction != "1" {
		return fmt.Errorf("invalid CM pattern direction")
	}
	return nil
}
func (m *cmPatternReader) pattern(row map[string]string) *cmPattern {
	route, direction := row["route_id"], row["direction_id"]
	shape := m.patternShape(row)
	p := m.Patterns[row["pattern_id"]]
	if p == nil {
		pattern := strings.Clone(row["pattern_id"])
		p = &cmPattern{Path: CMPath{ID: m.Prefix + pattern, Line: m.Lines[route], Shape: shape}, Route: strings.Clone(route), Direction: strings.Clone(direction)}
		m.Patterns[pattern] = p
	}
	if p.Route != route || p.Direction != direction || p.Path.Shape != shape || !m.Shapes[shape] {
		p.Ambiguous = true
	}
	return p
}
func (m *cmPatternReader) patternShape(row map[string]string) string {
	direction := row["direction_id"]
	if direction == "" {
		direction = "unknown"
	}
	return "cm:" + url.PathEscape(m.Plan.ID) + "/" + url.PathEscape(m.Plan.Agency) + "/" + url.PathEscape(row["route_id"]) + "/" + url.PathEscape(row["shape_id"]) + "/" + direction
}
func cmVisitSequence(row map[string]string) (int, error) {
	seq, err := strconv.Atoi(row["stop_sequence"])
	if err != nil || seq < 0 {
		return 0, fmt.Errorf("invalid CM pattern sequence")
	}
	return seq, nil
}
func (m *cmPatternReader) representativeVisit(row map[string]string) error {
	trip := m.Trips[row["trip_id"]]
	stop := m.Stops[row["stop_id"]]
	if trip == nil || stop == "" {
		return fmt.Errorf("unknown CM pattern trip or stop")
	}
	seq, err := cmVisitSequence(row)
	if err != nil {
		return err
	}
	if row["trip_id"] == trip.Pattern.Representative {
		if len(trip.Pattern.Path.Visits) >= maxReadResults {
			return readResultLimit()
		}
		trip.Pattern.Path.Visits = append(trip.Pattern.Path.Visits, CMPathVisit{Stop: stop, Sequence: seq})
	}
	return nil
}
func (m *cmPatternReader) preparePaths() {
	for _, p := range m.Patterns {
		sort.Slice(p.Path.Visits, func(i, j int) bool { return p.Path.Visits[i].Sequence < p.Path.Visits[j].Sequence })
		if len(p.Path.Visits) == 0 {
			p.Ambiguous = true
		}
		p.Lookup = map[int]int{}
		for i, v := range p.Path.Visits {
			if _, exists := p.Lookup[v.Sequence]; exists {
				p.Ambiguous = true
			}
			p.Lookup[v.Sequence] = i
		}
		p.Path.Visits = append([]CMPathVisit(nil), p.Path.Visits...)
	}
}
func (m *cmPatternReader) verifyVisit(row map[string]string) error {
	trip := m.Trips[row["trip_id"]]
	if trip == nil {
		return fmt.Errorf("unknown CM pattern trip")
	}
	seq, err := cmVisitSequence(row)
	if err != nil {
		return err
	}
	p := trip.Pattern
	i, exists := p.Lookup[seq]
	if !exists || p.Path.Visits[i].Stop != row["stop_id"] {
		p.Ambiguous = true
		return nil
	}
	if trip.Seen == nil {
		trip.Seen = make([]uint64, (len(p.Path.Visits)+63)/64)
	}
	bit := uint64(1) << uint(i%64)
	if trip.Seen[i/64]&bit != 0 {
		p.Ambiguous = true
	}
	trip.Seen[i/64] |= bit
	return nil
}
func (m *cmPatternReader) verifyComplete() {
	for _, trip := range m.Trips {
		for i := range trip.Pattern.Path.Visits {
			if i/64 >= len(trip.Seen) || trip.Seen[i/64]&(uint64(1)<<uint(i%64)) == 0 {
				trip.Pattern.Ambiguous = true
				break
			}
		}
	}
}
