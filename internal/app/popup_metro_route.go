package app

import (
	"strconv"

	"lisboapublica/internal/api"
)

// The Hub trip is a route/direction hint, not a dated train allocation.
func (r *journeyPopupRead) identifyMetroRoute() {
	if !r.metroRouteContextValid() {
		return
	}
	r.index = r.data.journeys("metro")
	trip := r.metroRouteTrip()
	if !r.metroRouteTripValid(trip) {
		return
	}
	r.trip, r.visits = trip, journeyTimes(trip)
	r.result.Association = "published_route"
	r.result.Message = ""
	r.result.Direction = popupDirectionLabel(r.index, trip)
	r.result.Destination = tripDestination(r.data.Schedule, trip)
	_, r.result.LineName, _ = lineForTrip(r.data, "metro", trip)
	r.result.Complete = r.data.Schedule.CompleteJourneys
	r.result.Coverage.ActualArrivals, r.result.Coverage.ActualDepartures = false, false
	r.result.Coverage.Message = "Percurso publicado para a linha e sentido; tempos deste comboio indisponíveis."
	r.setProgress()
	if r.result.Progress == "confirmed" {
		r.result.Progress = "estimated"
	}
}

func (r *journeyPopupRead) metroRouteContextValid() bool {
	if r.data == nil || r.data.Schedule == nil || r.vehicle.PlanId == nil {
		return false
	}
	return r.vehicle.RouteId != nil && *r.vehicle.PlanId == r.data.PlanID
}

func (r *journeyPopupRead) metroRouteTripValid(trip *ScheduledTrip) bool {
	if trip == nil {
		return false
	}
	visits := journeyTimes(trip)
	return orderedPopupVisits(visits) && len(visits) >= 2 && r.index.directionFor(trip) != nil && tripDestination(r.data.Schedule, trip) != ""
}

func (r *journeyPopupRead) metroRouteMatches(trip *ScheduledTrip) bool {
	return *r.vehicle.RouteId == qualify("metro", trip.Route) || *r.vehicle.RouteId == r.index.lineFor(trip)
}

func (r *journeyPopupRead) metroRouteTrip() *ScheduledTrip {
	destination := normalizeName(r.result.Destination)
	if r.vehicle.TripId != nil {
		return r.metroHintRouteTrip(destination)
	}
	return r.metroDestinationRouteTrip(destination)
}

func (r *journeyPopupRead) metroHintRouteTrip(destination string) *ScheduledTrip {
	matches := popupTripMatches(r.data, "metro", *r.vehicle.TripId)
	if len(matches) != 1 || !r.metroRouteMatches(matches[0]) {
		return nil
	}
	trip := matches[0]
	if destination != "" && destination != normalizeName(tripDestination(r.data.Schedule, trip)) {
		return nil
	}
	return trip
}

func (r *journeyPopupRead) metroDestinationRouteTrip(destination string) *ScheduledTrip {
	if destination == "" {
		return nil
	}
	var selected *ScheduledTrip
	for n := range r.data.Schedule.Trips {
		trip := &r.data.Schedule.Trips[n]
		if !r.metroRouteMatches(trip) || normalizeName(tripDestination(r.data.Schedule, trip)) != destination {
			continue
		}
		if selected != nil && !sameMetroRouteVisits(journeyTimes(selected), journeyTimes(trip)) {
			return nil
		}
		if selected == nil || trip.ID < selected.ID {
			selected = trip
		}
	}
	return selected
}

func sameMetroRouteVisits(a, b []StopTime) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n].Stop != b[n].Stop || a[n].Sequence != b[n].Sequence {
			return false
		}
	}
	return true
}

func (r *journeyPopupRead) readMetroRoutePage() error {
	if len(r.visits) > maxReadResults {
		return readResultLimit()
	}
	r.focusPage()
	start := min(r.filter.Offset, len(r.visits))
	end := min(start+r.filter.Limit, len(r.visits))
	r.result.Data = make([]api.StopCall, 0, end-start)
	for n := start; n < end; n++ {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		r.result.Data = append(r.result.Data, r.metroRouteCall(n))
	}
	r.result.Page = api.Page{Limit: r.filter.Limit, Offset: start, Total: len(r.visits), HasMore: end < len(r.visits), Revision: &r.revision}
	return nil
}

func (r *journeyPopupRead) metroRouteCall(n int) api.StopCall {
	visit := r.visits[n]
	call := api.StopCall{Id: "metro:route:" + r.trip.ID + ":" + strconv.Itoa(visit.Sequence), StopId: qualify("metro", visit.Stop), StopName: r.data.Schedule.StopNames[visit.Stop], StopSequence: visit.Sequence, LineKey: r.index.lineFor(r.trip), DirectionKey: r.index.directionFor(r.trip), Destination: r.result.Destination, Arrival: missingCallTime("Chegada não publicada para este comboio"), Departure: missingCallTime("Partida não publicada para este comboio"), Phase: r.visitPhase(n)}
	for _, stop := range r.data.Stops {
		if stop.Id == call.StopId {
			copy := stop
			call.Stop, call.StopStaticUpdatedAt = &copy, &r.data.Updated
			call.StopPlanId = optional(r.data.PlanID)
			break
		}
	}
	return call
}
