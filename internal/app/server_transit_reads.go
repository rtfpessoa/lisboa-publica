package app

import (
	"context"
	"lisboapublica/internal/api"
	"net/http"
	"sort"
	"time"
)

func routeSummary(r api.RouteDetail) api.Route {
	return api.Route{Id: r.Id, SourceId: r.SourceId, OperatorId: r.OperatorId, ShortName: r.ShortName, LongName: r.LongName, Color: r.Color, StopIds: r.StopIds, PlanId: r.PlanId}
}

// ListOperators lists provider capabilities and source freshness.
func (s *Server) ListOperators(ctx context.Context, _ api.ListOperatorsRequestObject) (api.ListOperatorsResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, err := s.Cache.state(filter.Revision)
	if err != nil {
		return nil, err
	}
	out := []api.Operator{}
	now := time.Now()
	for _, p := range providers {
		out = append(out, projectOperator(state, p.ID, now))
	}
	page, data := paginate(out, filter, state.Revision)
	return api.ListOperators200JSONResponse{Data: data, Page: page}, nil
}

func projectOperator(state *State, id string, now time.Time) api.Operator {
	v := state.Operators[id]
	_, v.ReportedPositions, v.EstimatedPositions, v.LastKnownPositions, v.LastKnownTruncated = projectLive(state.Live[id], v, state.Static[id], now)
	markOperatorFreshness(&v, now)
	return v
}

func markOperatorFreshness(v *api.Operator, now time.Time) {
	if v.Status == "ok" && (v.LiveUpdatedAt == nil || now.Sub(*v.LiveUpdatedAt) > 90*time.Second) {
		v.Status = api.OperatorStatusStale
	}
	if v.ObservedAt != nil && now.Sub(*v.ObservedAt) > 180*time.Second && v.Status == "ok" {
		v.Status = api.OperatorStatusStale
	}
	if v.StaticStatus == "ok" && (v.StaticUpdatedAt == nil || now.Sub(*v.StaticUpdatedAt) > 12*time.Hour) {
		v.StaticStatus = api.OperatorStaticStatusStale
	}
}

// ListRoutes searches published routes within an immutable collection.
func (s *Server) ListRoutes(ctx context.Context, _ api.ListRoutesRequestObject) (api.ListRoutesResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, err := s.Cache.state(filter.Revision)
	if err != nil {
		return nil, err
	}
	out := []api.Route{}
	for p, d := range state.Static {
		if !filter.selected(p) {
			continue
		}
		for _, r := range d.Routes {
			if filter.Q != "" && !nameSearch(r.ShortName+" "+r.LongName+" "+r.SourceId+" "+passengerRouteSearchName(r), filter.Q) {
				continue
			}
			out = append(out, routeSummary(r))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	page, data := paginate(out, filter, state.Revision)
	return api.ListRoutes200JSONResponse{Data: data, Page: page}, nil
}

// GetRoute returns a qualified route and its published geometry.
func (s *Server) GetRoute(ctx context.Context, r api.GetRouteRequestObject) (api.GetRouteResponseObject, error) {
	state, _ := s.Cache.state("")
	for _, d := range state.Static {
		for _, v := range d.Routes {
			if v.Id == r.RouteId {
				return api.GetRoute200JSONResponse(v), nil
			}
		}
	}
	return nil, fail(http.StatusNotFound, "not_found", "Carreira não encontrada.")
}

// ListStops searches published stops within an immutable collection.
func (s *Server) ListStops(ctx context.Context, _ api.ListStopsRequestObject) (api.ListStopsResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, err := s.Cache.state(filter.Revision)
	if err != nil {
		return nil, err
	}
	out := []api.Stop{}
	for p, d := range state.Static {
		if !filter.selected(p) {
			continue
		}
		for _, v := range d.Stops {
			if filter.Route != "" && !contains(v.RouteIds, filter.Route) {
				continue
			}
			if filter.Q != "" && !nameSearch(v.Name+" "+v.SourceId, filter.Q) {
				continue
			}
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	page, data := paginate(out, filter, state.Revision)
	return api.ListStops200JSONResponse{Data: data, Page: page}, nil
}

// ListVehicles lists reported or explicitly estimated positions.
func (s *Server) ListVehicles(ctx context.Context, _ api.ListVehiclesRequestObject) (api.ListVehiclesResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, asOf, revision, err := s.vehicleState(filter, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if filter.Stop != "" {
		_, err = arrivalOperator(state, filter)
	}
	var out []api.Vehicle
	if err == nil {
		out, err = listedVehicles(ctx, state, filter, asOf)
	}
	sortVehicles(out)
	page, data := paginate(out, filter, revision)
	return api.ListVehicles200JSONResponse{Data: data, Page: page}, err
}
