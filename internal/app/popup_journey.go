package app

import (
	"context"
	"lisboapublica/internal/api"
	"net/http"
	"strings"
	"time"
)

type popupJourneySources struct {
	server  *Server
	state   *State
	vehicle *api.Vehicle
	data    *StaticData
	index   *journeyIndex
}
type popupJourneyInstance struct {
	trip      *ScheduledTrip
	operator  string
	day, asOf time.Time
	visits    []StopTime
	current   *int
}
type popupJourneyPage struct {
	generation int64
	revision   string
	filter     Filter
	result     api.VehicleJourney
}
type journeyPopupRead struct {
	popupJourneySources
	popupJourneyInstance
	popupJourneyPage
	ctx context.Context
}

// GetVehicleJourney reads a safely identified service and independent stop times.
func (s *Server) GetVehicleJourney(ctx context.Context, r api.GetVehicleJourneyRequestObject) (api.GetVehicleJourneyResponseObject, error) {
	read, err := s.newJourneyPopupRead(ctx, r.VehicleId)
	if err != nil {
		return nil, err
	}
	read.identify()
	if read.trip != nil {
		err = read.readPage()
	}
	return api.GetVehicleJourney200JSONResponse(read.result), err
}
func (s *Server) newJourneyPopupRead(ctx context.Context, id string) (*journeyPopupRead, error) {
	f, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	state, generation, asOf, revision, err := s.journeyReadState(ctx, f)
	if err != nil {
		return nil, err
	}
	op, _, _ := strings.Cut(id, ":")
	vehicle := popupVehicle(state, op, id, asOf)
	read := &journeyPopupRead{popupJourneySources: popupJourneySources{server: s, state: state, vehicle: vehicle, data: state.Static[op]}, popupJourneyInstance: popupJourneyInstance{operator: op, asOf: asOf}, popupJourneyPage: popupJourneyPage{generation: generation, revision: revision, filter: f}, ctx: ctx}
	if vehicle == nil {
		err = fail(http.StatusNotFound, "vehicle_not_found", "Veículo indisponível nesta revisão.")
	} else {
		read.initialResult()
	}
	return read, err
}
func popupVehicle(state *State, op, id string, asOf time.Time) *api.Vehicle {
	var found *api.Vehicle
	rows := navigationVehicles(state, op, asOf)
	for n := range rows {
		if rows[n].Id == id {
			found = &rows[n]
			break
		}
	}
	return found
}
func (r *journeyPopupRead) initialResult() {
	r.result = api.VehicleJourney{Association: "unresolved", Message: "Sem associação segura à viagem; percurso e tempos indisponíveis.", LineName: r.vehicle.RouteName, Progress: "unknown", Data: []api.StopCall{}, Coverage: r.server.popupCoverage(r.state, r.operator)}
	if r.operator == "metro" {
		r.result.Destination = metroVehicleDestination(r.state, r.vehicle, r.asOf)
		r.result.Direction = r.result.Destination
	}
	r.result.Page, _ = paginate(r.result.Data, r.filter, r.revision)
}
func (r *journeyPopupRead) identify() {
	if r.data == nil || r.data.Schedule == nil || r.vehicle.TripId == nil {
		return
	}
	r.index = r.data.journeys(r.operator)
	candidates := popupTripMatches(r.data, r.operator, *r.vehicle.TripId)
	if len(candidates) != 1 {
		if len(candidates) > 1 {
			r.result.Association = "ambiguous"
		}
		return
	}
	t := candidates[0]
	if !r.matchesRoute(t) {
		return
	}
	r.result.Direction = popupDirectionLabel(r.index, t)
	r.result.Destination = tripDestination(r.data.Schedule, t)
	r.identifyInstance(t)
}
func (r *journeyPopupRead) matchesRoute(t *ScheduledTrip) bool {
	line, _, _ := lineForTrip(r.data, r.operator, t)
	// A syntactically exact Metro Hub trip is still an approximate assignment.
	return r.operator != "metro" && r.vehicle.RouteId != nil && (*r.vehicle.RouteId == qualify(r.operator, t.Route) || *r.vehicle.RouteId == line)
}
func popupDirectionLabel(index *journeyIndex, t *ScheduledTrip) string {
	for _, dir := range index.directions[index.lineFor(t)] {
		if dir.DirectionKey != nil && equalDirection(dir.DirectionKey, index.directionFor(t)) {
			return dir.Label
		}
	}
	return ""
}
func (r *journeyPopupRead) identifyInstance(t *ScheduledTrip) {
	if !r.instanceDescriptor(t) {
		return
	}
	day, err := time.ParseInLocation("2006-01-02", *r.vehicle.OperationalDate, lisbon)
	if err != nil || !popupServiceActive(r.data, t, day) {
		return
	}
	visits := journeyTimes(t)
	if !orderedPopupVisits(visits) {
		r.result.Association = "ambiguous"
		return
	}
	r.trip, r.day, r.visits = t, day, visits
	r.result.Association = "resolved"
	r.result.Message = ""
	r.result.Complete = r.data.Schedule.CompleteJourneys && len(t.JourneyTimes) > 0
	if !r.result.Complete {
		r.result.Message = "Percurso parcial: a rede guardada ainda não contém todas as visitas."
	}
	journey := journeyKey(r.operator, r.data, t, day)
	r.result.JourneyId = &journey
	r.setProgress()
}
func (r *journeyPopupRead) instanceDescriptor(t *ScheduledTrip) bool {
	plan := predictionTripPlan(r.data, t)
	return r.vehicle.PlanId != nil && *r.vehicle.PlanId == plan && r.vehicle.OperationalDate != nil && !r.data.Schedule.HasFrequencies
}
func orderedPopupVisits(visits []StopTime) bool {
	for n := 1; n < len(visits); n++ {
		if visits[n].Sequence <= visits[n-1].Sequence {
			return false
		}
	}
	return true
}
func (r *journeyPopupRead) setProgress() {
	if !popupProgressAdmissible(r.vehicle) {
		return
	}
	r.current = uniquePopupVisit(r.operator, r.visits, *r.vehicle.StopId)
	if r.current == nil {
		return
	}
	r.result.NextIndex = r.current
	if *r.vehicle.CurrentStatus == api.STOPPEDAT {
		r.result.NextIndex = nil
		if *r.current+1 < len(r.visits) {
			r.result.NextIndex = ptr(*r.current + 1)
		}
	}
	r.result.Progress = "confirmed"
	if r.vehicle.PositionKind == "estimated" {
		r.result.Progress = "estimated"
	}
}
func popupProgressAdmissible(v *api.Vehicle) bool {
	return !v.LastKnown && !v.Stale && time.Since(v.ObservedAt) <= sourceFreshness && v.StopId != nil && v.CurrentStatus != nil
}
func uniquePopupVisit(op string, visits []StopTime, stop string) *int {
	var found *int
	for n, v := range visits {
		if qualify(op, v.Stop) == stop {
			if found != nil {
				return nil
			}
			found = ptr(n)
		}
	}
	return found
}
func (r *journeyPopupRead) readPage() error {
	if len(r.visits) > maxReadResults {
		return readResultLimit()
	}
	r.focusPage()
	start := min(r.filter.Offset, len(r.visits))
	end := min(start+r.filter.Limit, len(r.visits))
	events, err := r.pageEvents(start, end)
	if err == nil {
		err = r.appendPage(start, end, events)
	}
	r.result.Page = api.Page{Limit: r.filter.Limit, Offset: r.filter.Offset, Total: len(r.visits), HasMore: end < len(r.visits), Revision: &r.revision}
	if r.result.Coverage.ActualArrivals || r.result.Coverage.ActualDepartures {
		r.result.Coverage.Message = "Tempos reais reportados onde disponíveis; as lacunas mantêm-se explícitas."
	}
	return err
}
func (r *journeyPopupRead) focusPage() {
	if request(r.ctx).URL.Query().Get("offset") == "" && r.result.NextIndex != nil {
		r.filter.Offset = (*r.result.NextIndex / r.filter.Limit) * r.filter.Limit
	}
}
func (r *journeyPopupRead) pageEvents(start, end int) ([]reportedStopEvent, error) {
	if start == end {
		return []reportedStopEvent{}, nil
	}
	return r.server.journeyEvents(r.ctx, *r.result.JourneyId, r.generation, r.asOf, r.visits[start].Sequence, r.visits[end-1].Sequence)
}
func (r *journeyPopupRead) appendPage(start, end int, events []reportedStopEvent) error {
	r.result.Data = make([]api.StopCall, 0, end-start)
	for n := start; n < end; n++ {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		call := r.visitCall(n, events)
		r.result.Coverage.ActualArrivals = r.result.Coverage.ActualArrivals || call.Arrival.Actual != nil
		r.result.Coverage.ActualDepartures = r.result.Coverage.ActualDepartures || call.Departure.Actual != nil
		r.result.Data = append(r.result.Data, call)
	}
	return nil
}
func (r *journeyPopupRead) visitCall(n int, events []reportedStopEvent) api.StopCall {
	call := (plannedPopupJourney{r.data, r.operator, r.trip, r.day, r.index, r.asOf}).call(r.visits[n])
	call.Phase = r.visitPhase(n)
	applyStoredEvents(&call, events, r.asOf)
	applyCPPredictions(&call, r.state, r.trip, r.day, r.asOf)
	r.applyVisitHistory(&call, n)
	return call
}
func (r *journeyPopupRead) visitPhase(n int) api.StopCallPhase {
	if r.current == nil {
		return "unknown"
	}
	if n < *r.current {
		return "previous"
	}
	if n == *r.current && *r.vehicle.CurrentStatus == api.STOPPEDAT {
		return "current"
	}
	return "future"
}
func (r *journeyPopupRead) applyVisitHistory(call *api.StopCall, n int) {
	if call.Phase == "current" {
		call.Arrival = pastCallTime(call.Arrival, r.asOf)
	}
	if call.Phase == "previous" {
		call.Arrival = pastCallTime(call.Arrival, r.asOf)
		call.Departure = pastCallTime(call.Departure, r.asOf)
	}
	if r.result.Complete && n == len(r.visits)-1 && call.Departure.Actual == nil && !conflictedCallTime(call.Departure) {
		call.Departure = missingCallTime("Fim da viagem")
	}
}
