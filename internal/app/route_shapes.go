package app

import (
	"context"
	"fmt"
	"lisboapublica/internal/api"
	"sort"
	"time"
)

// ListRouteShapes returns official geometry variants and per-operator availability.
func (s *Server) ListRouteShapes(ctx context.Context, _ api.ListRouteShapesRequestObject) (api.ListRouteShapesResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, err := s.Cache.state(filter.Revision)
	if err != nil {
		return nil, err
	}
	out := []api.RouteShape{}
	coverage := []api.GeometryCoverage{}
	for _, p := range providers {
		if !filter.selected(p.ID) {
			continue
		}
		data := state.Static[p.ID]
		coverage = append(coverage, geometryCoverage(p.ID, data))
		if data == nil {
			continue
		}
		for _, shape := range data.Shapes {
			if filter.Route == "" || shape.RouteId == filter.Route {
				out = append(out, shape)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	page, rows := paginate(out, filter, state.Revision)
	return api.ListRouteShapes200JSONResponse{Data: rows, Page: page, Coverage: coverage}, nil
}

func geometryCoverage(id string, data *StaticData) api.GeometryCoverage {
	coverage := api.GeometryCoverage{OperatorId: id, Status: api.GeometryCoverageStatusUnavailable, Message: "Percursos oficiais indisponíveis."}
	if data == nil {
		return coverage
	}
	coverage.UpdatedAt = data.GeometryUpdated
	if len(data.Shapes) > 0 {
		coverage.Status = api.GeometryCoverageStatusAvailable
		coverage.Message = "Percursos oficiais GTFS; inclui sentidos e variantes."
		covered := map[string]bool{}
		for _, shape := range data.Shapes {
			covered[shape.RouteId] = true
		}
		if data.GeometryPartial || len(covered) < len(data.Routes) {
			coverage.Status = api.GeometryCoverageStatusPartial
			coverage.Message = fmt.Sprintf("Percursos publicados em %d/%d carreiras; algumas variantes podem estar indisponíveis.", len(covered), len(data.Routes))
		}
		if data.GeometryError != nil && !data.GeometryPartial || data.GeometryUpdated == nil || time.Since(*data.GeometryUpdated) > 12*time.Hour {
			coverage.Status = api.GeometryCoverageStatusStale
			coverage.Message = "Percursos anteriores; atualização indisponível."
		}
	}
	if data.GeometryError != nil {
		coverage.Message += " " + *data.GeometryError
	}
	return coverage
}
