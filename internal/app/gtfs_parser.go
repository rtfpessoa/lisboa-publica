package app

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"

	"sort"
	"strconv"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

type gtfsReader struct {
	provider       provider
	data           *StaticData
	routes         map[string]*api.RouteDetail
	stops          map[string]*api.Stop
	shapes         map[string][]shapePoint
	trips          map[string]*ScheduledTrip
	shapeForRoute  map[string]string
	directions     map[string]*int
	pointCount     int
	badShapes      map[string]bool
	geometryFailed bool
	routeStops     map[string]map[string]bool
	endpointNames  map[string]string
}

func readGTFS(blob []byte, p provider, planID, from, until, source string, now time.Time) (*StaticData, error) {
	archive, err := openGTFS(blob)
	if err != nil {
		return nil, err
	}
	data := &StaticData{CPJourneyEndpoints: p.ID == "cp", Models: map[string]Metadata{}, PlanID: planID, ValidFrom: from, ValidUntil: until, Source: source, Updated: now, Schedule: &Schedule{Calendars: map[string]Calendar{}, Exceptions: map[string]map[string]int{}, Parents: map[string]string{}}}
	reader := &gtfsReader{provider: p, data: data, routes: map[string]*api.RouteDetail{}, stops: map[string]*api.Stop{}, shapes: map[string][]shapePoint{}, trips: map[string]*ScheduledTrip{}, shapeForRoute: map[string]string{}, directions: map[string]*int{}, routeStops: map[string]map[string]bool{}, endpointNames: map[string]string{}}
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
		{"vehicles.txt", reader.vehicle},
	}
	for _, table := range tables {
		if err := archive.read(table.name, table.visit); err != nil {
			return nil, err
		}
	}
	if err := reader.readGeometry(archive); err != nil {
		return nil, err
	}
	reader.finishGeometry(now)

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
	if err := g.rememberStop(m); err != nil {
		return err
	}
	lat, lon, err := gtfsStopCoordinates(m)
	if err != nil {
		return err
	}
	return g.addLocalStop(m, lat, lon)
}

func gtfsStopCoordinates(m map[string]string) (float64, float64, error) {
	lat, e := strconv.ParseFloat(m["stop_lat"], numericBitSize)
	if e != nil {
		return 0, 0, fmt.Errorf("invalid GTFS stop latitude")
	}
	lon, e := strconv.ParseFloat(m["stop_lon"], numericBitSize)
	if e != nil {
		return 0, 0, fmt.Errorf("invalid GTFS stop longitude")
	}
	return lat, lon, nil
}

func (g *gtfsReader) addLocalStop(m map[string]string, lat, lon float64) error {
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
	if raw := m["direction_id"]; raw != "" {
		direction, err := strconv.Atoi(raw)
		if err != nil || (direction != 0 && direction != 1) {
			return fmt.Errorf("invalid shape direction")
		}
		g.directions[id] = ptr(direction)
	}
	g.trips[id] = &ScheduledTrip{ID: id, Route: m["route_id"], Service: m["service_id"], Headsign: m["trip_headsign"], Shape: m["shape_id"]}
	return nil
}

func (g *gtfsReader) stopTime(m map[string]string) error {
	t := g.trips[m["trip_id"]]
	if t == nil {
		return fmt.Errorf("stop_time has unknown trip")
	}
	seq, e := strconv.Atoi(m["stop_sequence"])
	if e != nil || seq < 0 {
		return fmt.Errorf("invalid stop sequence")
	}
	g.rememberEndpoint(t, m["stop_id"], seq)
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
	t.Times = append(t.Times, StopTime{g.stops[m["stop_id"]].SourceId, a, dep, seq})
	return nil
}

func (g *gtfsReader) shape(m map[string]string) error {
	g.pointCount++
	if g.pointCount > maxGeometryPoints {
		return g.geometryPointOverflow()
	}
	if g.geometryFailed {
		return nil
	}
	return g.appendShapePoint(m)
}

func (g *gtfsReader) geometryPointOverflow() error {
	if g.provider.ID == "cm" {
		return fmt.Errorf("shape point limit exceeded")
	}
	g.geometryFailed = true
	g.data.GeometryError = ptr("Percursos excedem o limite de pontos.")
	g.shapes = map[string][]shapePoint{}
	return nil
}

func (g *gtfsReader) appendShapePoint(m map[string]string) error {
	id := m["shape_id"]
	if g.badShapes == nil {
		g.badShapes = map[string]bool{}
	}
	point, err := parseShapePoint(m)
	if id == "" || err != nil || point.Sequence < 0 {
		return g.rejectShapePoint(id)
	}
	if !g.badShapes[id] {
		g.shapes[id] = append(g.shapes[id], point)
	}
	return nil
}

func (g *gtfsReader) rejectShapePoint(id string) error {
	if g.provider.ID == "cm" {
		return fmt.Errorf("invalid CM shape point")
	}
	g.badShapes[id] = true
	delete(g.shapes, id)
	return nil
}

func (g *gtfsReader) validateShapes() {
	for id, points := range g.shapes {
		sort.Slice(points, func(i, j int) bool { return points[i].Sequence < points[j].Sequence })
		usable := false
		unique := points[:0]
		for _, point := range points {
			if len(unique) > 0 && point.Sequence == unique[len(unique)-1].Sequence {
				prev := unique[len(unique)-1]
				if prev.Lat != point.Lat || prev.Lon != point.Lon {
					g.badShapes[id] = true
				}
				continue
			}
			if point.Lat != points[0].Lat || point.Lon != points[0].Lon {
				usable = true
			}
			unique = append(unique, point)
		}
		if !usable || g.badShapes[id] {
			delete(g.shapes, id)
		} else {
			g.shapes[id] = unique
		}
	}
}

func (g *gtfsReader) markPartialGeometry() {
	covered := map[string]bool{}
	for _, shape := range g.data.Shapes {
		covered[shape.RouteId] = true
	}
	partial := len(covered) < len(g.data.Routes)
	for _, trip := range g.data.Schedule.Trips {
		if trip.Shape == "" || len(g.shapes[trip.Shape]) < 2 {
			partial = true
		}
	}
	if partial {
		g.data.GeometryPartial = true
		g.data.GeometryError = ptr("Algumas carreiras ou variantes não têm percurso utilizável.")
	}
}

func (g *gtfsReader) vehicle(m map[string]string) error {
	if id := m["vehicle_id"]; id != "" {
		if g.provider.ID == "mobi" {
			id = strings.TrimPrefix(id, g.provider.Code+"-")
		}
		g.data.Models[id] = metadataRow(m)
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
		if t.Shape != "" && len(g.shapes[t.Shape]) >= 2 && (g.shapeForRoute[t.Route] == "" || t.Shape < g.shapeForRoute[t.Route]) {
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

func fatalGTFSGeometryError(err error) bool {
	return errors.Is(err, errGTFSIntegrity) || errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrFormat) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, errGTFSResource)
}

func (g *gtfsReader) readGeometry(archive gtfsArchive) error {
	if err := archive.read("shapes.txt", g.shape); err != nil {
		if fatalGTFSGeometryError(err) {
			return err
		}
		g.geometryFailed = true
		g.data.GeometryError = ptr("Tabela de percursos inválida; horários disponíveis.")
	}
	return nil
}

func (g *gtfsReader) finishGeometry(now time.Time) {

	if g.geometryFailed {
		g.shapes = map[string][]shapePoint{}
	} else {
		g.validateShapes()
	}
	g.connectTrips()
	g.buildRoutes()
	g.buildStops()
	variants, err := g.routeShapes(g.provider.Agency)
	if err != nil {
		g.geometryFailed = true
		g.data.GeometryError = ptr("Percursos excedem o limite de variantes.")
		stripGeometry(g.data)
	} else {
		g.data.Shapes = variants
	}
	if len(g.data.Shapes) > 0 {
		g.data.GeometryUpdated = ptr(now)
	}
	if !g.geometryFailed {
		g.markPartialGeometry()
	}
}
