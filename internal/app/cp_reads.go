package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

// ListCpPredictions reads cached CP calls; public queries never fetch upstream data.
func (s *Server) ListCpPredictions(ctx context.Context, _ api.ListCpPredictionsRequestObject) (api.ListCpPredictionsResponseObject, error) {
	filter, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, revision, err := s.cpReadState(ctx, &filter)
	if err != nil {
		return nil, err
	}
	trip := request(ctx).URL.Query().Get("trip_id")
	response, err := cpPage(ctx, state, filter, trip, revision)
	return response, err
}

func (s *Server) cpReadState(ctx context.Context, filter *Filter) (*State, string, error) {
	if filter.Revision == "" && request(ctx).URL.Query().Get("to") == "" {
		filter.To = filter.From.Add(2 * time.Hour)
	}
	state, revision, err := s.scheduleState(ctx, filter)
	if err == nil {
		err = validateCPFilter(*filter, state, request(ctx).URL.Query().Get("trip_id"))
	}
	return state, revision, err
}

func cpPage(ctx context.Context, state *State, filter Filter, trip, revision string) (api.ListCpPredictionsResponseObject, error) {
	rows, availability, expiry := cpRead(state, filter, trip)
	if !expiry.IsZero() && !time.Now().Before(expiry) {
		if filter.Revision != "" {
			return nil, fail(http.StatusGone, "prediction_revision_expired", "As previsões expiraram. Volte a carregar a primeira página.")
		}
		rows, availability = []api.CPPrediction{}, staleCPAvailability(availability)
	}
	page, data := paginate(rows, filter, revision)
	if err := attachPredictionVehicles(ctx, state, data); err != nil {
		return nil, err
	}
	return api.ListCpPredictions200JSONResponse{Data: data, Page: page, Availability: availability}, nil
}

func validateCPFilter(f Filter, state *State, trip string) error {
	for _, id := range f.Operators {
		if id != "cp" {
			return fail(http.StatusBadRequest, "operator", "As previsões deste endpoint pertencem à CP.")
		}
	}
	return validateCPSelection(f, state, trip)
}

func validateCPSelection(f Filter, state *State, trip string) error {
	var err error
	switch {
	case f.To.Sub(f.From) > 2*time.Hour:
		err = fail(http.StatusBadRequest, "window", "Selecione um intervalo até duas horas.")
	case f.Stop != "" && !staticHasStop(state.Static["cp"], "cp", f.Stop):
		err = fail(http.StatusNotFound, "stop_not_found", "Estação CP indisponível nesta revisão.")
	case trip != "" && (!strings.HasPrefix(trip, "cp:") || len(trip) > cpMaxIdentifierBytes):
		err = fail(http.StatusBadRequest, "trip", "ID de viagem CP inválido.")
	case f.Route != "" && !strings.HasPrefix(f.Route, "cp:"):
		err = fail(http.StatusBadRequest, "route", "Carreira CP inválida.")
	}
	return err
}

func cpRead(state *State, f Filter, trip string) ([]api.CPPrediction, api.CPPredictionAvailability, time.Time) {
	rows := []api.CPPrediction{}
	availability := api.CPPredictionAvailability{Status: api.CPPredictionAvailabilityStatusLoading, Message: "A carregar previsões CP…", SourceUrl: cpSourceURL}
	data := state.CP
	static := state.Static["cp"]
	if data == nil || static == nil || data.PlanID != static.PlanID {
		return rows, availability, time.Time{}
	}
	availability = data.Availability
	var expiry time.Time
	for _, row := range data.Rows {
		if !cpReadMatch(row, f, trip, static) {
			continue
		}
		if !state.Created.Before(row.ValidUntil) {
			continue
		}
		rows = append(rows, row)
		if expiry.IsZero() || row.ValidUntil.Before(expiry) {
			expiry = row.ValidUntil
		}
	}
	if len(rows) == 0 && len(data.Rows) > 0 && !cpHasFreshRow(data, state.Created) {
		availability = staleCPAvailability(availability)
	}
	return rows, availability, expiry
}

func cpReadMatch(row api.CPPrediction, f Filter, trip string, static *StaticData) bool {
	if trip != "" && trip != row.SourceTripId {
		return false
	}
	if f.Route != "" && f.Route != row.RouteId {
		return false
	}
	if f.Stop != "" && f.Stop != row.StopId && f.Stop != qualify("cp", static.Schedule.Parents[strings.TrimPrefix(row.StopId, "cp:")]) {
		return false
	}
	return !row.ExpectedAt.Before(f.From) && !row.ExpectedAt.After(f.To)
}

func cpHasFreshRow(data *CPData, now time.Time) bool {
	for _, row := range data.Rows {
		if now.Before(row.ValidUntil) {
			return true
		}
	}
	return false
}

func staleCPAvailability(a api.CPPredictionAvailability) api.CPPredictionAvailability {
	a.Status, a.Message = api.CPPredictionAvailabilityStatusStale, "Previsões antigas ou indisponíveis; consulte os horários planeados."
	return a
}
