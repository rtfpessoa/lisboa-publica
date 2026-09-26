package app

import (
	"context"
	"lisboapublica/internal/api"
	"net/http"
	"sort"
	"strings"
)

// ListTrips lists planned departures from one immutable network revision.
func (s *Server) ListTrips(ctx context.Context, _ api.ListTripsRequestObject) (api.ListTripsResponseObject, error) {
	filter, state, revision, err := s.scheduleFilter(ctx)
	if err != nil {
		return nil, err
	}
	// Trips ignore stop selectors, as before; only arrivals use them.
	filter.Stop = ""
	out, err := collectScheduledTrips(ctx, state, filter)
	if err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListTrips200JSONResponse{Data: data, Page: page}, nil
}

// ListArrivals lists arrivals at a known stop in the requested immutable revision.
func (s *Server) ListArrivals(ctx context.Context, _ api.ListArrivalsRequestObject) (api.ListArrivalsResponseObject, error) {
	filter, state, revision, err := s.scheduleFilter(ctx)
	if err != nil {
		return nil, err
	}
	out, err := collectScheduledArrivals(ctx, state, filter)
	if err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListArrivals200JSONResponse{Data: data, Page: page}, nil
}

func (s *Server) scheduleFilter(ctx context.Context) (Filter, *State, string, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return filter, nil, "", err
	}
	state, revision, err := s.scheduleState(ctx, &filter)
	return filter, state, revision, err
}

func collectScheduledTrips(ctx context.Context, state *State, filter Filter) ([]api.Trip, error) {
	out := []api.Trip{}
	for p, d := range state.Static {
		if !filter.selected(p) {
			continue
		}
		trips, _, err := (scheduleQuery{ctx: ctx, data: d, operator: p, filter: filter}).run()
		if err != nil {
			return nil, err
		}
		if len(out)+len(trips) > maxReadResults {
			return nil, readResultLimit()
		}
		out = append(out, trips...)
	}
	sortScheduled(out, nil)
	return out, ctx.Err()
}

func collectScheduledArrivals(ctx context.Context, state *State, filter Filter) ([]api.Arrival, error) {
	if filter.Stop == "" {
		return nil, fail(http.StatusBadRequest, "stop_required", "Selecione uma paragem.")
	}
	operator, err := arrivalOperator(state, filter)
	if err != nil {
		return nil, err
	}
	return arrivalsForOperator(ctx, state, filter, operator)
}

func arrivalsForOperator(ctx context.Context, state *State, filter Filter, operator string) ([]api.Arrival, error) {
	out := []api.Arrival{}
	if operator == "metro" {
		out = append(out, predictedArrivals(state, filter.From, filter.To, filter.Route, filter.Stop)...)
	}
	_, arrivals, err := (scheduleQuery{ctx: ctx, data: state.Static[operator], operator: operator, filter: filter}).run()
	if err != nil {
		return nil, err
	}
	if len(out)+len(arrivals) > maxReadResults {
		return nil, readResultLimit()
	}
	out = append(out, arrivals...)
	sortArrivals(out)
	return out, ctx.Err()
}

func sortArrivals(out []api.Arrival) {
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i].ScheduledAt, out[j].ScheduledAt
		if ai == nil {
			ai = out[i].ExpectedAt
		}
		if aj == nil {
			aj = out[j].ExpectedAt
		}
		if ai.Equal(*aj) {
			return out[i].Id < out[j].Id
		}
		return ai.Before(*aj)
	})
}

func arrivalOperator(state *State, filter Filter) (string, error) {
	operator, _, _ := strings.Cut(filter.Stop, ":")
	if !filter.selected(operator) {
		return "", fail(http.StatusBadRequest, "stop_operator", "A paragem não pertence aos operadores selecionados.")
	}
	if staticHasStop(state.Static[operator], operator, filter.Stop) || metroHasStation(state.Metro, operator, filter.Stop) {
		return operator, nil
	}
	return "", fail(http.StatusNotFound, "stop_not_found", "Paragem indisponível nesta revisão da rede.")
}

func staticHasStop(d *StaticData, operator, id string) bool {
	if d == nil {
		return false
	}
	for _, stop := range d.Stops {
		if stop.Id == id {
			return true
		}
	}
	return scheduleHasParent(d.Schedule, operator, id)
}

func scheduleHasParent(schedule *Schedule, operator, id string) bool {
	if schedule == nil {
		return false
	}
	for _, parent := range schedule.Parents {
		if parent != "" && qualify(operator, parent) == id {
			return true
		}
	}
	return false
}

func metroHasStation(data *MetroData, operator, id string) bool {
	if operator != "metro" || data == nil {
		return false
	}
	for _, station := range data.Stations {
		if qualify("metro", station.ID) == id {
			return true
		}
	}
	return false
}
