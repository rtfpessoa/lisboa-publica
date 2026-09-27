package app

import (
	"lisboapublica/internal/api"
	"strings"
	"time"
)

type arrivalVehicleJourney struct{ operator, plan, trip, date string }

func (j arrivalVehicleJourney) matches(v api.Vehicle) bool {
	return v.OperatorId == j.operator && matchesArrivalValue(v.PlanId, j.plan) && matchesArrivalValue(v.TripId, j.trip) && matchesArrivalValue(v.OperationalDate, j.date)
}

func matchesArrivalValue(v *string, want string) bool { return v != nil && *v == want }
func freshArrivalVehicle(v api.Vehicle, now time.Time) bool {
	return !v.Stale && !v.LastKnown && cpCurrent(v.ObservedAt, now)
}

func tmlArrivalVehicle(state *State, i *tmlArrivalIndex, u cpUpdate, day, now time.Time) *string {
	live := state.Live[i.provider.ID]
	if u.Vehicle.ID == "" || live == nil || live.Unverified {
		return nil
	}
	prefix := "[" + i.index.Data.PlanID + "][" + i.provider.Agency + "]"
	journey := arrivalVehicleJourney{operator: i.provider.ID, plan: i.index.Data.PlanID, trip: qualify(i.provider.ID, strings.TrimPrefix(u.Trip.ID, prefix)), date: day.Format("2006-01-02")}
	return uniqueTMLArrivalVehicle(live, journey, verifiedHubID(u.Vehicle.ID, i.provider.Agency), now)
}

func uniqueTMLArrivalVehicle(live *LiveData, j arrivalVehicleJourney, source string, now time.Time) *string {
	var found *string
	for _, v := range live.Vehicles {
		if v.SourceId != source || !j.matches(v) || !freshArrivalVehicle(v, now) {
			continue
		}
		if found != nil {
			return nil
		}
		found = ptr(v.Id)
	}
	return found
}

func currentArrivalVehicle(state *State, a api.Arrival, now time.Time) bool {
	live := state.Live[a.OperatorId]
	if !usableArrivalVehicleContext(live, a) {
		return false
	}
	j := arrivalVehicleJourney{operator: a.OperatorId, plan: *a.PlanId, trip: a.TripId, date: a.ServiceDate.Time.Format("2006-01-02")}
	found := false
	for _, v := range live.Vehicles {
		if v.Id != *a.VehicleId {
			continue
		}
		if found || !j.matches(v) || !freshArrivalVehicle(v, now) || !arrivalVehicleRoute(v, a.RouteId) {
			return false
		}
		found = true
	}
	return found
}

func usableArrivalVehicleContext(live *LiveData, a api.Arrival) bool {
	return live != nil && !live.Unverified && a.VehicleId != nil && a.PlanId != nil && a.ServiceDate != nil
}
func arrivalVehicleRoute(v api.Vehicle, route string) bool {
	return v.RouteId == nil || *v.RouteId == route
}
