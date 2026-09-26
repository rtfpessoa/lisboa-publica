package app

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"time"

	"lisboapublica/internal/api"
)

func (g *gtfsReader) routeShapes(agency string) ([]api.RouteShape, error) {
	out := []api.RouteShape{}
	known := map[string]bool{}
	coordinates := map[string][][]float64{}
	for _, id := range g.shapeTrips() {
		trip := g.trips[id]
		variant := g.shapeVariant(id, agency)
		key := variant.Id
		if known[key] {
			continue
		}
		known[key] = true
		if len(out) >= maxGeometryVariants {
			return nil, fmt.Errorf("shape variant limit exceeded")
		}
		if coordinates[trip.Shape] == nil {
			coordinates[trip.Shape] = simplifyShape(g.shapes[trip.Shape])
		}
		variant.Geometry = coordinates[trip.Shape]
		out = append(out, variant)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out, nil
}

func readCMShapes(blob []byte, plan *hubPlan, p provider, source string, now time.Time) ([]api.RouteShape, error) {
	data, err := readCMNetwork(blob, plan, p, source, now)
	if err != nil {
		return nil, err
	}
	return data.Shapes, nil
}

// readCMNetwork reads geometry and optional fleet metadata; the CM line catalog remains authoritative.
func readCMNetwork(blob []byte, plan *hubPlan, p provider, source string, now time.Time) (*StaticData, error) {
	archive, err := openGTFS(blob)
	if err != nil {
		return nil, err
	}
	reader := &gtfsReader{provider: p, data: &StaticData{PlanID: plan.ID, Source: source, Updated: now, Models: map[string]Metadata{}}, routes: map[string]*api.RouteDetail{}, trips: map[string]*ScheduledTrip{}, shapes: map[string][]shapePoint{}, directions: map[string]*int{}}
	tables := []struct {
		name  string
		visit func(map[string]string) error
	}{
		{"routes.txt", func(row map[string]string) error {
			if row["line_id"] == "" {
				return fmt.Errorf("CM GTFS line_id unavailable")
			}
			if err := reader.route(row); err != nil {
				return err
			}
			reader.routes[row["route_id"]].Id = qualify(p.ID, row["line_id"])
			return nil
		}},
		{"trips.txt", reader.trip}, {"shapes.txt", reader.shape},
		{"vehicles.txt", func(row map[string]string) error {
			if id := row["vehicle_id"]; id != "" {
				reader.data.Models["["+plan.Agency+"]"+id] = metadataRow(row)
			}
			return nil
		}},
	}
	for _, table := range tables {
		if err := archive.read(table.name, table.visit); err != nil {
			return nil, err
		}
	}
	variants, err := reader.routeShapes(plan.Agency)
	if err == nil && len(variants) == 0 {
		err = fmt.Errorf("CM plan contains no published geometry variants")
	}
	reader.data.Shapes = variants
	return reader.data, err
}

func (g *gtfsReader) shapeVariant(id, agency string) api.RouteShape {
	trip := g.trips[id]
	route := g.routes[trip.Route]
	direction := "unknown"
	if g.directions[id] != nil {
		direction = strconv.Itoa(*g.directions[id])
	}
	key := qualify(g.provider.ID, url.PathEscape(g.data.PlanID)+"/"+url.PathEscape(agency)+"/"+url.PathEscape(trip.Route)+"/"+url.PathEscape(trip.Shape)+"/"+direction)
	return api.RouteShape{Id: key, OperatorId: g.provider.ID, RouteId: route.Id, ShapeId: trip.Shape, DirectionId: g.directions[id], Headsign: trip.Headsign, Color: route.Color, PlanId: g.data.PlanID, SourceUrl: g.data.Source, UpdatedAt: g.data.Updated}
}

func parseShapePoint(row map[string]string) (shapePoint, error) {
	values := [2]float64{}
	bounds := [2]float64{90, 180}
	for i, name := range []string{"shape_pt_lat", "shape_pt_lon"} {
		value, err := strconv.ParseFloat(row[name], numericBitSize)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > bounds[i] {
			return shapePoint{}, fmt.Errorf("invalid shape coordinates")
		}
		values[i] = value
	}
	sequence, err := strconv.Atoi(row["shape_pt_sequence"])
	return shapePoint{Lat: values[0], Lon: values[1], Sequence: sequence}, err
}

func (g *gtfsReader) shapeTrips() []string {
	trips := []string{}
	for id, trip := range g.trips {
		if g.routes[trip.Route] != nil && len(g.shapes[trip.Shape]) >= 2 && (g.provider.ID == "cm" || len(g.routeStops[trip.Route]) > 0) {
			trips = append(trips, id)
		}
	}
	sort.Strings(trips)
	return trips
}
