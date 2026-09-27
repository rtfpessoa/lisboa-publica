package app

import (
	"context"
	"lisboapublica/internal/api"
	"strconv"
)

// Only unique official codes and published trip stops may be cross-referenced.
func resolveMetroVehicleStop(v *api.Vehicle, state *State) {
	if !needsMetroStop(v, state) {
		return
	}
	d := state.Static["metro"]
	trip, _, err := scheduledVehicleTrip(context.Background(), *v, d)
	if err != nil || trip == nil {
		return
	}
	published := uniqueMetroCode(state.Metro, verifiedHubID(*v.SourceStopId, "IA2N9"))
	candidate := uniqueMetroTripStop(published, d, trip)
	if candidate != nil && uniqueMetroTarget(state.Metro, candidate, published) {
		v.StopId = ptr(candidate.Id)
		v.StopName = ptr(candidate.Name)
	}
}
func needsMetroStop(v *api.Vehicle, state *State) bool {
	return v.OperatorId == "metro" && v.StopId == nil && v.SourceStopId != nil && state.Metro != nil
}
func uniqueMetroCode(data *MetroData, code string) *MetroStation {
	var found *MetroStation
	for i := range data.Stations {
		station := &data.Stations[i]
		if station.ID != code {
			continue
		}
		if found != nil {
			return nil
		}
		found = station
	}
	return found
}
func metroTargetMatches(station *MetroStation, stop *api.Stop) bool {
	if station == nil {
		return false
	}
	lat, el := strconv.ParseFloat(station.Lat, 64)
	lon, eo := strconv.ParseFloat(station.Lon, 64)
	return el == nil && eo == nil && normalizeName(station.Name) == normalizeName(stop.Name) && abs(stop.Lat-lat) < metroStationTolerance && abs(stop.Lon-lon) < metroStationTolerance
}
func uniqueMetroTripStop(station *MetroStation, d *StaticData, trip *ScheduledTrip) *api.Stop {
	visits := map[string]bool{}
	for _, visit := range trip.Times {
		visits[qualify("metro", visit.Stop)] = true
	}
	var found *api.Stop
	for i := range d.Stops {
		stop := &d.Stops[i]
		if !visits[stop.Id] || !metroTargetMatches(station, stop) {
			continue
		}
		if found != nil && found.Id != stop.Id {
			return nil
		}
		found = stop
	}
	return found
}
func uniqueMetroTarget(data *MetroData, stop *api.Stop, station *MetroStation) bool {
	for i := range data.Stations {
		other := &data.Stations[i]
		if other.ID != station.ID && metroTargetMatches(other, stop) {
			return false
		}
	}
	return true
}
