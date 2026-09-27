package app

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

type callsRevision struct {
	Reference   string `json:"r"`
	Predictions bool   `json:"p"`
}

// GetVehicleCalls uses only a retained cache state. No visitor-triggered upstream IO.
func (s *Server) GetVehicleCalls(ctx context.Context, r api.GetVehicleCallsRequestObject) (api.GetVehicleCallsResponseObject, error) {
	f, err := s.filter(ctx, false)
	if err != nil {
		return nil, err
	}
	if r.Params.Limit == nil {
		f.Limit = 20
	}
	now := time.Now().UTC()
	cursor, err := s.callsSelector(r, f, now)
	var page api.VehicleCallsPage
	if err == nil {
		page, err = s.callsPage(ctx, r, f, cursor)
	}
	return api.GetVehicleCalls200JSONResponse(page), err
}

func (s *Server) callsSelector(r api.GetVehicleCallsRequestObject, f Filter, now time.Time) (callsRevision, error) {
	reference := textValue(r.Params.Reference)
	cursor := callsRevision{Reference: reference}
	var err error
	if f.Revision != "" {
		err = decodeNavigation(f.Revision, "c1.", 4096, &cursor)
		if err == nil && reference != "" && reference != cursor.Reference {
			err = fail(http.StatusBadRequest, "revision", "A referência mudou entre páginas.")
		}
	}
	if err == nil && cursor.Reference == "" {
		cursor.Reference, err = s.latestNavigationReference(r.VehicleId, now)
	}
	return cursor, err
}
func (s *Server) latestNavigationReference(id string, now time.Time) (string, error) {
	state, err := s.Cache.state("")
	now = time.Now().UTC()
	if err != nil {
		return "", err
	}
	operator, _, _ := strings.Cut(id, ":")
	for _, v := range navigationVehicles(state, operator, now) {
		if v.Id != id {
			continue
		}
		if ref := vehicleReference(state, now, v); ref != nil {
			return ref.Reference, nil
		}
	}
	return "", fail(http.StatusNotFound, "vehicle_not_found", "Veículo indisponível.")
}

type callsRead struct {
	State           *State
	AsOf            time.Time
	Filter          Filter
	Cursor          callsRevision
	Result          vehicleCallResult
	IncludeGeometry bool
}

func (s *Server) callsPage(ctx context.Context, r api.GetVehicleCallsRequestObject, f Filter, cursor callsRevision) (api.VehicleCallsPage, error) {
	state, asOf, v, err := s.navigationState(cursor.Reference, r.VehicleId, time.Now().UTC())
	read := callsRead{IncludeGeometry: r.Params.IncludeGeometry != nil && *r.Params.IncludeGeometry && f.Offset == 0, State: state, AsOf: asOf, Filter: f, Cursor: cursor, Result: vehicleCallResult{Rows: []api.VehicleCall{}}}
	if err == nil {
		read.Result, err = buildVehicleCalls(ctx, state, v, asOf, true)
	}
	if err == nil {
		err = resolveCallsExpiry(ctx, &read, v, time.Now().UTC())
	}
	read.Result.IncludeGeometry = read.IncludeGeometry
	out := callsResponse(read.Result, v, f, read.Cursor)
	if err == nil {
		out, err = finishCallsPage(ctx, read, out)
	}
	return out, err
}
func callsResponse(result vehicleCallResult, v api.Vehicle, f Filter, cursor callsRevision) api.VehicleCallsPage {
	page, rows := paginate(result.Rows, f, encodeNavigation("c1.", cursor))
	out := api.VehicleCallsPage{Vehicle: v, Data: rows, Page: page, Availability: result.Availability, Progress: result.Progress, Coverage: "regional_subset"}
	if result.Coverage != "" {
		out.Coverage = result.Coverage
	}
	if result.IncludeGeometry {
		out.Geometry = result.Shape
	}
	if cursor.Predictions && !result.Expiry.IsZero() {
		out.ValidUntil = &result.Expiry
	}
	return out
}
func finishCallsPage(ctx context.Context, read callsRead, out api.VehicleCallsPage) (api.VehicleCallsPage, error) {
	now := time.Now().UTC()
	err := validateCallsReturn(ctx, out.Vehicle, now)
	if err == nil && read.Cursor.Predictions && !read.Result.Expiry.IsZero() && !now.Before(read.Result.Expiry) {
		err = resolveCallsExpiry(ctx, &read, out.Vehicle, now)
		read.Result.IncludeGeometry = read.IncludeGeometry
		out = callsResponse(read.Result, out.Vehicle, read.Filter, read.Cursor)
	}
	if err == nil {
		err = validateCallsReturn(ctx, out.Vehicle, time.Now().UTC())
	}
	return out, err
}
func validateCallsReturn(ctx context.Context, v api.Vehicle, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !now.Before(v.ObservedAt.Add(lastKnownLifetime)) {
		return fail(http.StatusGone, "reference_expired", "Esta ligação expirou. Atualize os dados de origem.")
	}
	return nil
}
func resolveCallsExpiry(ctx context.Context, read *callsRead, v api.Vehicle, now time.Time) error {
	if read.Filter.Revision == "" {
		read.Cursor.Predictions = read.Result.Expiry.IsZero() || now.Before(read.Result.Expiry)
	}
	if read.Cursor.Predictions && !read.Result.Expiry.IsZero() && !now.Before(read.Result.Expiry) {
		return fail(http.StatusGone, "prediction_revision_expired", "As previsões expiraram. Atualize as chegadas de origem.")
	}
	var err error
	if !read.Cursor.Predictions {
		read.Result, err = buildVehicleCalls(ctx, read.State, v, read.AsOf, false)
	}
	return err
}

func scheduledVehicleTrip(ctx context.Context, v api.Vehicle, d *StaticData) (*ScheduledTrip, time.Time, error) {
	if !hasVehicleSchedule(v, d) {
		return nil, time.Time{}, nil
	}
	day, valid := scheduledServiceDay(*v.OperationalDate, d)
	var found *ScheduledTrip
	var err error
	if valid {
		for i := range d.Schedule.Trips {
			if err = ctx.Err(); err != nil {
				break
			}
			t := &d.Schedule.Trips[i]
			if vehicleTripMatches(v, t, d, day) {
				found = t
				break
			}
		}
	}
	return found, day, err
}
func hasVehicleSchedule(v api.Vehicle, d *StaticData) bool {
	return d != nil && d.Schedule != nil && serviceFor(v).complete() && textValue(v.PlanId) == d.PlanID
}
func vehicleTripMatches(v api.Vehicle, t *ScheduledTrip, d *StaticData, day time.Time) bool {
	return qualify(v.OperatorId, t.ID) == textValue(v.TripId) && d.Schedule.active(t.Service, day) && (v.RouteId == nil || *v.RouteId == qualify(v.OperatorId, t.Route))
}

type vehicleCallResult struct {
	Coverage        api.VehicleCallsPageCoverage
	Shape           *api.RouteShape
	IncludeGeometry bool
	Rows            []api.VehicleCall
	Availability    api.VehicleCallsPageAvailability
	Progress        api.VehicleCallsPageProgress
	Expiry          time.Time
	First           int
	Known           bool
}

// Kept as a small seam for fixtures and response construction.
func vehicleCalls(ctx context.Context, state *State, v api.Vehicle, asOf time.Time, predictions bool) ([]api.VehicleCall, api.VehicleCallsPageAvailability, api.VehicleCallsPageProgress, time.Time, error) {
	result, err := buildVehicleCalls(ctx, state, v, asOf, predictions)
	return result.Rows, result.Availability, result.Progress, result.Expiry, err
}
func buildVehicleCalls(ctx context.Context, state *State, v api.Vehicle, asOf time.Time, predictions bool) (vehicleCallResult, error) {
	result := vehicleCallResult{Rows: []api.VehicleCall{}, Availability: "unidentified_service", Progress: "unknown"}
	d := state.Static[v.OperatorId]
	if v.PlanId != nil && d != nil && *v.PlanId != d.PlanID {
		result.Availability = "plan_mismatch"
		return result, nil
	}
	if v.OperatorId == "cm" && v.PatternId != nil {
		err := appendCMPublishedCalls(ctx, &result, v, d)
		if len(result.Rows) == 0 {
			publishedStopFallback(&result, v, d)
		}
		return result, err
	}
	trip, day, err := scheduledVehicleTrip(ctx, v, d)
	if err == nil && trip != nil {
		err = appendPlannedCalls(ctx, &result, scheduledCallSource{v, d, trip, day})
	}
	if err == nil && predictions && canUseCPPredictions(state, v, d) {
		err = appendPredictedCalls(ctx, &result, state, v, asOf)
	}
	if err == nil {
		publishedStopFallback(&result, v, d)
	}
	sort.SliceStable(result.Rows, func(i, j int) bool { return callSequence(result.Rows[i]) < callSequence(result.Rows[j]) })
	return result, err
}

type scheduledCallSource struct {
	Vehicle api.Vehicle
	Data    *StaticData
	Trip    *ScheduledTrip
	Day     time.Time
}

func appendPlannedCalls(ctx context.Context, result *vehicleCallResult, source scheduledCallSource) error {
	v, d, trip, day := source.Vehicle, source.Data, source.Trip, source.Day
	result.First, result.Known = vehicleProgress(v, localJourneyTimes(trip))
	if result.Known {
		result.Progress = "known"
	}
	var err error
	stops := callStops(d)
	for _, visit := range localJourneyTimes(trip) {
		if err = ctx.Err(); err != nil {
			break
		}
		if result.Known && visit.Sequence < result.First {
			continue
		}
		if len(result.Rows) >= maxReadResults {
			err = readResultLimit()
			break
		}
		result.Rows = append(result.Rows, plannedVehicleCall(v, d, visit, day, stops))
	}
	result.Availability = "available"
	return err
}
func plannedVehicleCall(v api.Vehicle, d *StaticData, visit StopTime, day time.Time, stops map[string]api.Stop) api.VehicleCall {
	stopID := qualify(v.OperatorId, visit.Stop)
	at := serviceStart(day).Add(time.Duration(visit.Arrival) * time.Second)
	call := api.VehicleCall{Id: stopID + ":" + strconv.Itoa(visit.Sequence), StopId: stopID, StopName: stopID, StopSequence: ptr(visit.Sequence), Kind: "scheduled", ScheduledAt: &at, SourceUrl: d.Source, StopPlanId: optional(d.PlanID), StopStaticUpdatedAt: ptr(d.Updated)}
	if stop, ok := stops[stopID]; ok {
		call.Stop = &stop
		call.StopName = stop.Name
	}
	return call
}
func callStops(d *StaticData) map[string]api.Stop {
	stops := map[string]api.Stop{}
	if d != nil {
		for _, stop := range d.Stops {
			stops[stop.Id] = stop
		}
	}
	return stops
}
func canUseCPPredictions(state *State, v api.Vehicle, d *StaticData) bool {
	return v.OperatorId == "cp" && serviceFor(v).complete() && state.CP != nil && d != nil && state.CP.PlanID == d.PlanID
}
func predictionForVehicle(p api.CPPrediction, v api.Vehicle, asOf time.Time) bool {
	return p.PlanId == textValue(v.PlanId) && p.SourceTripId == textValue(v.TripId) && p.ServiceDate != nil && p.ServiceDate.Format("2006-01-02") == textValue(v.OperationalDate) && p.ValidUntil.After(asOf) && !p.ExpectedAt.Before(asOf)
}

type predictedCallSource struct {
	State   *State
	Vehicle api.Vehicle
	AsOf    time.Time
	Stops   map[string]api.Stop
}

func appendPredictedCalls(ctx context.Context, result *vehicleCallResult, state *State, v api.Vehicle, asOf time.Time) error {
	source := predictedCallSource{state, v, asOf, callStops(state.Static[v.OperatorId])}
	var err error
	for i := 0; i < len(state.CP.Rows) && err == nil; i++ {
		err = appendPrediction(ctx, result, source, state.CP.Rows[i])
	}
	if !result.Expiry.IsZero() {
		result.Availability = "available"
		if state.CP.Availability.Status != "ok" {
			result.Availability = "partial"
		}
	}
	return err
}
func appendPrediction(ctx context.Context, result *vehicleCallResult, source predictedCallSource, p api.CPPrediction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if result.Known && p.StopSequence < result.First {
		return nil
	}
	if predictionForVehicle(p, source.Vehicle, source.AsOf) {
		result.Rows = replaceCall(result.Rows, predictedVehicleCall(p, source.Stops, source.State.Static[source.Vehicle.OperatorId]))
		if result.Expiry.IsZero() || p.ValidUntil.Before(result.Expiry) {
			result.Expiry = p.ValidUntil
		}
	}
	var err error
	if len(result.Rows) > maxReadResults {
		err = readResultLimit()
	}
	return err
}
func predictedVehicleCall(p api.CPPrediction, stops map[string]api.Stop, d *StaticData) api.VehicleCall {
	call := api.VehicleCall{Id: p.Id, StopId: p.StopId, StopName: p.StopName, StopSequence: ptr(p.StopSequence), Kind: "predicted", ScheduledAt: p.ScheduledAt, ExpectedAt: &p.ExpectedAt, SourceUpdatedAt: &p.SourceUpdatedAt, DelaySeconds: p.DelaySeconds, SourceUrl: p.SourceUrl, StopPlanId: optional(d.PlanID), StopStaticUpdatedAt: ptr(d.Updated)}
	if stop, ok := stops[p.StopId]; ok {
		call.Stop = &stop
	}
	return call
}
func publishedStopFallback(result *vehicleCallResult, v api.Vehicle, d *StaticData) {
	if len(result.Rows) > 0 || v.StopId == nil {
		return
	}
	if stop, ok := callStops(d)[*v.StopId]; ok {
		result.Rows = append(result.Rows, api.VehicleCall{Id: stop.Id, StopId: stop.Id, StopName: stop.Name, Stop: &stop, Kind: "published_route", SourceUrl: v.SourceUrl, StopPlanId: optional(d.PlanID), StopStaticUpdatedAt: ptr(d.Updated)})
		result.Availability = "next_stop_only"
	}
}

func vehicleProgress(v api.Vehicle, visits []StopTime) (int, bool) {
	if v.LastKnown || v.StopId == nil || v.CurrentStatus == nil {
		return 0, false
	}
	sequence, found := 0, 0
	for _, visit := range visits {
		if qualify(v.OperatorId, visit.Stop) == *v.StopId {
			sequence = visit.Sequence
			found++
		}
	}
	if found != 1 {
		return 0, false
	}
	if *v.CurrentStatus == api.VehicleCurrentStatus("STOPPED_AT") {
		sequence++
	}
	return sequence, true
}

func replaceCall(rows []api.VehicleCall, call api.VehicleCall) []api.VehicleCall {
	for i := range rows {
		if rows[i].StopId == call.StopId && rows[i].StopSequence != nil && call.StopSequence != nil && *rows[i].StopSequence == *call.StopSequence {
			rows[i] = call
			return rows
		}
	}
	return append(rows, call)
}
func callSequence(call api.VehicleCall) int {
	if call.StopSequence == nil {
		return 0
	}
	return *call.StopSequence
}
