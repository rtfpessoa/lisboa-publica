package app

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lisboapublica/internal/api"
)

var cmPublishedPatternID = regexp.MustCompile(`^\[[A-Za-z0-9_-]+\]\[[A-Za-z0-9_-]+\][A-Za-z0-9_.-]+$`)

func publishedCMPattern(raw string) *string {
	if len(raw) > 253 || !cmPublishedPatternID.MatchString(raw) {
		return nil
	}
	return ptr(qualify("cm", raw))
}

func cmVehiclePath(v api.Vehicle, d *StaticData) *CMPath {
	if v.OperatorId != "cm" || v.PatternId == nil || v.RouteId == nil || d == nil {
		return nil
	}
	i := sort.Search(len(d.CMPaths), func(i int) bool { return d.CMPaths[i].ID >= *v.PatternId })
	if i == len(d.CMPaths) || d.CMPaths[i].ID != *v.PatternId || d.CMPaths[i].Line != *v.RouteId {
		return nil
	}
	return &d.CMPaths[i]
}
func cmPathShape(d *StaticData, path *CMPath) *api.RouteShape {
	for i := range d.Shapes {
		shape := &d.Shapes[i]
		if shape.Id == path.Shape && shape.OperatorId == "cm" && shape.RouteId == path.Line {
			return shape
		}
	}
	return nil
}
func appendCMPublishedCalls(ctx context.Context, result *vehicleCallResult, v api.Vehicle, d *StaticData) error {
	path := cmVehiclePath(v, d)
	if path == nil {
		return ctx.Err()
	}
	shape := cmPathShape(d, path)
	if shape == nil {
		return ctx.Err()
	}
	rows, err := cmPublishedRows(ctx, d, path, shape)
	if err == nil && len(rows) > 0 {
		result.Rows = rows
		result.Coverage = "complete_published_route"
		result.Availability = "available"
		result.Shape = shape
	}
	return err
}
func cmPublishedRows(ctx context.Context, d *StaticData, path *CMPath, shape *api.RouteShape) ([]api.VehicleCall, error) {
	stops := callStops(d)
	rows := make([]api.VehicleCall, 0, len(path.Visits))
	for _, visit := range path.Visits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := qualify("cm", visit.Stop)
		stop, exists := stops[id]
		if !exists || strings.TrimSpace(stop.Name) == "" {
			return nil, nil
		}
		rows = append(rows, api.VehicleCall{Id: id + ":" + strconv.Itoa(visit.Sequence), StopId: id, StopName: stop.Name, Stop: &stop, StopSequence: ptr(visit.Sequence), Kind: "published_route", SourceUrl: shape.SourceUrl, StopStaticUpdatedAt: ptr(d.Updated)})
	}
	return rows, nil
}
