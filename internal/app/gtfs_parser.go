package app

import (
	"fmt"

	"sort"
	"strconv"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

type gtfsReader struct {
	provider      provider
	data          *StaticData
	routes        map[string]*api.RouteDetail
	stops         map[string]*api.Stop
	shapes        map[string][]shapePoint
	trips         map[string]*ScheduledTrip
	shapeForRoute map[string]string
	routeStops    map[string]map[string]bool
}

func readGTFS(blob []byte, p provider, planID, from, until, source string, now time.Time) (*StaticData, error) {
	archive, err := openGTFS(blob)
	if err != nil {
		return nil, err
	}
	data := &StaticData{Models: map[string]Metadata{}, PlanID: planID, ValidFrom: from, ValidUntil: until, Source: source, Updated: now, Schedule: &Schedule{Calendars: map[string]Calendar{}, Exceptions: map[string]map[string]int{}, Parents: map[string]string{}}}
	reader := &gtfsReader{provider: p, data: data, routes: map[string]*api.RouteDetail{}, stops: map[string]*api.Stop{}, shapes: map[string][]shapePoint{}, trips: map[string]*ScheduledTrip{}, shapeForRoute: map[string]string{}, routeStops: map[string]map[string]bool{}}
	tables := []struct {
		name  string
		visit func(map[string]string) error
	}{
		{"routes.txt", reader.route},
		{"stops.txt", reader.stop},
		{"calendar.txt", reader.calendar},
		{"calendar_dates.txt", reader.exception},
		{"trips.txt", reader.trip},
		{"stop_times.txt", reader.stopTime},
		{"shapes.txt", reader.shape},
		{"vehicles.txt", reader.vehicle},
	}
	for _, table := range tables {
		if err := archive.read(table.name, table.visit); err != nil {
			return nil, err
		}
	}
	reader.connectTrips()
	reader.buildRoutes()
	reader.buildStops()
	return reader.result()
}
func (g *gtfsReader) route(m map[string]string) error {
	id := m["route_id"]
	if id == "" {
		return fmt.Errorf("route missing ID")
	}
	color := m["route_color"]
	if len(color) != hexColorDigits {
		color = strings.TrimPrefix(g.provider.Color, "#")
	}
	r := &api.RouteDetail{Id: qualify(g.provider.ID, id), SourceId: id, OperatorId: g.provider.ID, ShortName: m["route_short_name"], LongName: m["route_long_name"], Color: "#" + color, StopIds: []string{}, PlanId: optional(g.data.PlanID)}
	if r.ShortName == "" {
		r.ShortName = r.LongName
	}
	g.routes[id] = r
	return nil
}

func (g *gtfsReader) stop(m map[string]string) error {
	lat, e := strconv.ParseFloat(m["stop_lat"], numericBitSize)
	if e != nil {
		return fmt.Errorf("invalid GTFS stop latitude")
	}
	lon, e := strconv.ParseFloat(m["stop_lon"], numericBitSize)
	if e != nil {
		return fmt.Errorf("invalid GTFS stop longitude")
	}
	if !validPosition(lat, lon) {
		return nil
	}
	id := m["stop_id"]
	if id == "" {
		return fmt.Errorf("stop missing ID")
	}
	parent := m["parent_station"]
	s := &api.Stop{Id: qualify(g.provider.ID, id), SourceId: id, OperatorId: g.provider.ID, Name: m["stop_name"], Lat: lat, Lon: lon, RouteIds: []string{}}
	if parent != "" {
		s.ParentId = ptr(qualify(g.provider.ID, parent))
		g.data.Schedule.Parents[id] = parent
	}
	g.stops[id] = s
	return nil
}

func (g *gtfsReader) calendar(m map[string]string) error {
	c := Calendar{Start: m["start_date"], End: m["end_date"]}
	for i, n := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		c.Days[i] = m[n] == "1"
	}
	g.data.Schedule.Calendars[m["service_id"]] = c
	return nil
}

func (g *gtfsReader) exception(m map[string]string) error {
	id := m["service_id"]
	if g.data.Schedule.Exceptions[id] == nil {
		g.data.Schedule.Exceptions[id] = map[string]int{}
	}
	v, e := strconv.Atoi(m["exception_type"])
	if e != nil || v < 1 || v > 2 {
		return fmt.Errorf("invalid calendar exception")
	}
	g.data.Schedule.Exceptions[id][m["date"]] = v
	return nil
}

func (g *gtfsReader) trip(m map[string]string) error {
	if g.routes[m["route_id"]] == nil {
		return fmt.Errorf("trip has unknown route")
	}
	id := m["trip_id"]
	g.trips[id] = &ScheduledTrip{ID: id, Route: m["route_id"], Service: m["service_id"], Headsign: m["trip_headsign"], Shape: m["shape_id"]}
	return nil
}

func (g *gtfsReader) stopTime(m map[string]string) error {
	t := g.trips[m["trip_id"]]
	if t == nil {
		return fmt.Errorf("stop_time has unknown trip")
	}
	if g.stops[m["stop_id"]] == nil {
		return nil
	}
	a, e := parseClock(m["arrival_time"])
	if e != nil {
		return e
	}
	dep, e := parseClock(m["departure_time"])
	if e != nil {
		return e
	}
	seq, e := strconv.Atoi(m["stop_sequence"])
	if e != nil {
		return e
	}
	t.Times = append(t.Times, StopTime{g.stops[m["stop_id"]].SourceId, a, dep, seq})
	return nil
}

func (g *gtfsReader) shape(m map[string]string) error {
	lat, e := strconv.ParseFloat(m["shape_pt_lat"], numericBitSize)
	if e != nil {
		return e
	}
	lon, e := strconv.ParseFloat(m["shape_pt_lon"], numericBitSize)
	if e != nil {
		return e
	}
	seq, e := strconv.Atoi(m["shape_pt_sequence"])
	if e != nil {
		return e
	}
	g.shapes[m["shape_id"]] = append(g.shapes[m["shape_id"]], shapePoint{lat, lon, seq})
	return nil
}

func (g *gtfsReader) vehicle(m map[string]string) error {
	if id := m["vehicle_id"]; id != "" {
		if g.provider.ID == "mobi" {
			id = strings.TrimPrefix(id, g.provider.Code+"-")
		}
		g.data.Models[id] = Metadata{strings.TrimSpace(m["make"] + " " + m["model"]), m["license_plate"]}
	}
	return nil
}

func (g *gtfsReader) connectTrips() {
	for _, t := range g.trips {
		if len(t.Times) == 0 {
			continue
		}
		sort.Slice(t.Times, func(i, j int) bool { return t.Times[i].Sequence < t.Times[j].Sequence })
		g.data.Schedule.Trips = append(g.data.Schedule.Trips, *t)
		if g.routeStops[t.Route] == nil {
			g.routeStops[t.Route] = map[string]bool{}
		}
		for _, v := range t.Times {
			g.routeStops[t.Route][v.Stop] = true
		}
		if g.shapeForRoute[t.Route] == "" && t.Shape != "" {
			g.shapeForRoute[t.Route] = t.Shape
		}
	}
}

func (g *gtfsReader) buildRoutes() {
	for id, r := range g.routes {
		ids := g.routeStops[id]
		if len(ids) == 0 {
			continue
		}
		for sid := range ids {
			r.StopIds = append(r.StopIds, qualify(g.provider.ID, sid))
			g.stops[sid].RouteIds = append(g.stops[sid].RouteIds, r.Id)
			if par := g.data.Schedule.Parents[sid]; g.stops[par] != nil {
				g.stops[par].RouteIds = append(g.stops[par].RouteIds, r.Id)
			}
		}
		sort.Strings(r.StopIds)
		pts := g.shapes[g.shapeForRoute[id]]
		sort.Slice(pts, func(i, j int) bool { return pts[i].Sequence < pts[j].Sequence })
		if len(pts) > 1 {
			coords := make([][]float64, 0, len(pts))
			for _, pt := range pts {
				coords = append(coords, []float64{pt.Lon, pt.Lat})
			}
			r.Geometry = &coords
		}
		g.data.Routes = append(g.data.Routes, *r)
	}

}

func (g *gtfsReader) buildStops() {
	for _, s := range g.stops {
		s.RouteIds = uniqueSorted(s.RouteIds)
		if len(s.RouteIds) > 0 {
			g.data.Stops = append(g.data.Stops, *s)
		}
	}

}

func (g *gtfsReader) result() (*StaticData, error) {

	sort.Slice(g.data.Routes, func(i, j int) bool { return g.data.Routes[i].Id < g.data.Routes[j].Id })
	sort.Slice(g.data.Stops, func(i, j int) bool { return g.data.Stops[i].Id < g.data.Stops[j].Id })
	sort.Slice(g.data.Schedule.Trips, func(i, j int) bool { return g.data.Schedule.Trips[i].ID < g.data.Schedule.Trips[j].ID })
	if len(g.data.Routes) == 0 || len(g.data.Stops) == 0 {
		return nil, fmt.Errorf("GTFS contains no Lisbon services")
	}
	return g.data, nil

}
