package app

import (
	"context"
	"lisboapublica/internal/api"
	"sort"
	"strings"
	"time"
)

// Keep the network and requested-stop publication frozen under the existing
// bounded arrival lease. No popup request contacts the upstream provider.
func (s *Server) popupArrivalSnapshot(ctx context.Context, state *State, op string, f Filter, rev string) (arrivalSnapshot, string, error) {
	d := state.Static[op]
	if op == "cp" || op == "metro" || d == nil {
		return arrivalSnapshot{}, rev, nil
	}
	now := time.Now().UTC()
	raw := request(ctx).URL.Query().Get("revision")
	if parts := strings.Split(raw, "|"); len(parts) == 3 && parts[0] == "b" {
		f.Revision = parts[2]
		view, err := s.Cache.arrivals.page(f, now)
		if err == nil && view.static != d {
			err = fail(410, "revision_expired", "A rede mudou. Atualize a primeira página.")
		}
		return view, raw, err
	}
	view := s.Cache.arrivals.request(f.Stop, d, now)
	view.rows = append([]api.Arrival(nil), view.rows...)
	if err := attachArrivalVehicles(ctx, state, view.rows); err != nil {
		return view, "", err
	}
	setArrivalPlannedCoverage(&view, f)
	token, err := s.Cache.arrivals.pin(f, view, now)
	return view, "b|" + rev + "|" + token, err
}

func popupArrivalCoverage(coverage api.PopupCoverage, view arrivalSnapshot) api.PopupCoverage {
	if view.static == nil {
		return coverage
	}
	coverage.Message = arrivalStatusMessage(view.availability.Status, view.static.Schedule != nil, arrivalHasKind(view.rows, "prediction")) + " Tempos reais anteriores sem fonte comprovada."
	coverage.SourceUpdatedAt = view.availability.SourceUpdatedAt
	if view.availability.Status == "loading" && view.static.Schedule == nil {
		coverage.Status = "loading"
	}
	if view.availability.Status == "stale" || view.availability.Status == "error" {
		coverage.Status = "stale"
	}
	if len(view.rows) > 0 && coverage.Status == "unavailable" {
		coverage.Status = "partial"
	}
	return coverage
}

func appendPopupArrivals(state *State, operator string, f Filter, view arrivalSnapshot, calls []api.StopCall) []api.StopCall {
	for _, row := range view.rows {
		if !arrivalInWindow(row, f, time.Now()) {
			continue
		}
		call := popupPublishedCall(state, operator, f, row, view)
		matched := mergePublishedPopupCall(calls, call, f.From)
		if !matched {
			calls = append(calls, call)
		}
	}
	sort.Slice(calls, func(i, j int) bool {
		a, b := nextCallTime(calls[i]), nextCallTime(calls[j])
		if a.Equal(b) {
			return calls[i].Id < calls[j].Id
		}
		return a.Before(b)
	})
	return calls
}

func popupPublishedCall(state *State, operator string, f Filter, row api.Arrival, view arrivalSnapshot) api.StopCall {
	call := api.StopCall{Id: row.Id, StopId: row.StopId, LineKey: row.RouteId, Destination: row.Headsign, Phase: "future", VehicleRef: row.VehicleRef, Arrival: missingCallTime("Chegada não publicada"), Departure: missingCallTime("Partida não publicada")}
	identifyPublishedPopupCall(&call, state.Static[operator], operator, f, row)
	var forecast, schedule *api.CallTimeEvidence
	if row.ScheduledAt != nil {
		schedule = &api.CallTimeEvidence{At: *row.ScheduledAt, SourceUrl: row.SourceUrl, CollectedAt: view.availability.CollectedAt}
	}
	if row.ExpectedAt != nil {
		forecast = &api.CallTimeEvidence{At: *row.ExpectedAt, SourceUrl: row.SourceUrl, SourceUpdatedAt: row.ObservedAt, CollectedAt: view.availability.CollectedAt, ValidUntil: row.ValidUntil}
	}
	call.Arrival = selectCallTime(nil, forecast, schedule, false, f.From)
	return call
}

func addPopupDirections(dirs []api.BoardDirection, calls []api.StopCall, d *StaticData, op string) []api.BoardDirection {
	known := map[string]bool{}
	for _, dir := range dirs {
		known[boardSelection(dir.LineKey, dir.DirectionKey)] = true
	}
	for _, call := range calls {
		selection := boardSelection(call.LineKey, call.DirectionKey)
		if known[selection] {
			continue
		}
		known[selection] = true
		dirs = append(dirs, publishedPopupDirection(d, op, call))
	}
	sort.Slice(dirs, func(i, j int) bool {
		return boardSelection(dirs[i].LineKey, dirs[i].DirectionKey) < boardSelection(dirs[j].LineKey, dirs[j].DirectionKey)
	})
	return dirs
}

func mergePublishedPopupCall(calls []api.StopCall, call api.StopCall, now time.Time) bool {
	for n := range calls {
		existing := &calls[n]
		if samePublishedPopupVisit(*existing, call) {
			if call.Arrival.Prediction != nil {
				existing.Arrival = selectCallTime(existing.Arrival.Actual, call.Arrival.Prediction, existing.Arrival.Schedule, false, now)
			}
			if call.VehicleRef != nil {
				existing.VehicleRef = call.VehicleRef
			}
			return true
		}
	}
	return false
}
func samePublishedPopupVisit(a, b api.StopCall) bool {
	identity := samePopupInstance(a, b)
	forecast := a.Arrival.Prediction != nil && b.Arrival.Prediction != nil
	if forecast {
		forecast = a.Arrival.Prediction.At.Equal(b.Arrival.Prediction.At) && a.Arrival.Prediction.SourceUrl == b.Arrival.Prediction.SourceUrl && a.LineKey == b.LineKey
	}
	return identity || forecast
}
func identifyPublishedPopupCall(call *api.StopCall, d *StaticData, op string, f Filter, row api.Arrival) {
	if d == nil || d.Schedule == nil {
		return
	}
	idx := d.journeys(op)
	call.DirectionKey = publishedPopupOrientation(idx, d, f, row)
	attachPublishedPopupJourney(call, idx, d, op, row)
}
func publishedPopupOrientation(idx *journeyIndex, d *StaticData, f Filter, row api.Arrival) *string {
	keys := map[string]*string{}
	for _, t := range idx.stopTrips(d, row.OperatorId, f.Stop) {
		if idx.lines[t] == row.RouteId && normalizeName(tripDestination(d.Schedule, t)) == normalizeName(row.Headsign) {
			key := idx.direction[t]
			keys[boardSelection(row.RouteId, key)] = key
		}
	}
	var key *string
	if len(keys) == 1 {
		for _, v := range keys {
			key = v
		}
	}
	return key
}
func attachPublishedPopupJourney(call *api.StopCall, idx *journeyIndex, d *StaticData, op string, row api.Arrival) {
	if row.PlanId == nil || row.SourceTripId == nil || row.ServiceDate == nil || row.StopSequence == nil {
		return
	}
	trips := idx.trips[qualify(op, *row.SourceTripId)]
	if len(trips) != 1 {
		return
	}
	t := trips[0]
	if *row.PlanId != predictionTripPlan(d, t) || idx.lines[t] != row.RouteId {
		return
	}
	if !publishedPopupVisit(t, op, row) {
		return
	}
	key := journeyKey(op, d, t, row.ServiceDate.Time)
	call.JourneyId = &key
	call.StopSequence = *row.StopSequence
}
func publishedPopupVisit(t *ScheduledTrip, op string, row api.Arrival) bool {
	for _, v := range journeyTimes(t) {
		if v.Sequence == *row.StopSequence && qualify(op, v.Stop) == row.StopId {
			return true
		}
	}
	return false
}
func publishedPopupDirection(d *StaticData, op string, call api.StopCall) api.BoardDirection {
	dir := api.BoardDirection{LineKey: call.LineKey, LineName: call.LineKey, Color: "#666666", Label: "Sentido não identificado", DirectionKey: call.DirectionKey}
	if d != nil {
		populatePublishedLine(&dir, d)
		populatePublishedDirection(&dir, d, op)
	}
	return dir
}
func populatePublishedLine(dir *api.BoardDirection, d *StaticData) {
	for _, route := range d.Routes {
		if route.Id == dir.LineKey {
			dir.LineName = route.ShortName
			dir.Color = route.Color
			break
		}
	}
}
func populatePublishedDirection(dir *api.BoardDirection, d *StaticData, op string) {
	if dir.DirectionKey == nil || d.Schedule == nil {
		return
	}
	for _, known := range d.journeys(op).directions[dir.LineKey] {
		if equalDirection(known.DirectionKey, dir.DirectionKey) {
			dir.Label = known.Label
			break
		}
	}
}

func samePopupInstance(a, b api.StopCall) bool {
	return a.JourneyId != nil && b.JourneyId != nil && *a.JourneyId == *b.JourneyId && a.StopSequence == b.StopSequence
}
