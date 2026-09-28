package app

import (
	"strconv"
	"strings"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func providerJourney(v api.Vehicle, d *StaticData, stops map[string]api.Stop) (patterns.ProviderJourney, bool) {
	path := patterns.ProviderJourney{Route: historyString(v.RouteId), Plan: historyString(v.PlanId), Pattern: historyString(v.PatternId)}
	if d == nil || path.Route == "" || v.TripId == nil {
		return path, false
	}
	visits, valid := publishedJourneyVisits(v, d, &path)
	if !valid {
		return path, false
	}
	visits = retainedJourneyRun(v, visits, stops)
	valid = appendJourneyVisits(v.OperatorId, visits, stops, &path)
	if valid && path.Headsign == "" {
		path.Headsign = path.Visits[len(path.Visits)-1].Name
	}
	return path, valid
}

func publishedJourneyVisits(v api.Vehicle, d *StaticData, path *patterns.ProviderJourney) ([]StopTime, bool) {
	if v.OperatorId == "cm" && v.PatternId != nil {
		return cmJourneyVisits(v, d, path)
	}
	return scheduledJourneyVisits(v, d, path)
}

func cmJourneyVisits(v api.Vehicle, d *StaticData, path *patterns.ProviderJourney) ([]StopTime, bool) {
	published := cmVehiclePath(v, d)
	if published == nil {
		return nil, false
	}
	shape := cmPathShape(d, published)
	if shape == nil {
		return nil, false
	}
	path.Headsign = shape.Headsign
	path.Direction = journeyDirection(shape.DirectionId, "pattern:"+published.ID)
	visits := make([]StopTime, 0, len(published.Visits))
	for _, visit := range published.Visits {
		visits = append(visits, StopTime{Stop: visit.Stop, Sequence: visit.Sequence})
	}
	return visits, true
}

func journeyDirection(direction *int, fallback string) string {
	if direction != nil {
		return strconv.Itoa(*direction)
	}
	return fallback
}

func scheduledJourneyVisits(v api.Vehicle, d *StaticData, path *patterns.ProviderJourney) ([]StopTime, bool) {
	if !hasVehicleSchedule(v, d) {
		return nil, false
	}
	day, valid := scheduledServiceDay(*v.OperationalDate, d)
	if !valid {
		return nil, false
	}
	trip := exactArrivalTrip(d.Schedule, strings.TrimPrefix(*v.TripId, v.OperatorId+":"))
	if trip == nil || !vehicleTripMatches(v, trip, d, day) {
		return nil, false
	}
	path.Headsign = trip.Headsign
	path.Direction = journeyDirection(trip.Direction, "destination:"+trip.Headsign)
	return journeyTimes(trip), true
}

func hasRetainedVisit(operator string, visit StopTime, stops map[string]api.Stop) bool {
	_, ok := stops[qualify(operator, visit.Stop)]
	return ok
}

func allJourneyVisitsRetained(operator string, visits []StopTime, stops map[string]api.Stop) bool {
	for _, visit := range visits {
		if !hasRetainedVisit(operator, visit, stops) {
			return false
		}
	}
	return true
}

// Local coverage may contain a contiguous portion of a published journey.
// Select its unique run around the source stop without bridging omitted visits.
func retainedJourneyRun(v api.Vehicle, visits []StopTime, stops map[string]api.Stop) []StopTime {
	if allJourneyVisitsRetained(v.OperatorId, visits, stops) {
		return visits
	}
	var selected []StopTime
	for begin := 0; begin < len(visits); {
		start, end, contains := nextRetainedRun(v, visits, stops, begin)
		if contains {
			if selected != nil {
				return nil
			}
			selected = visits[start:end]
		}
		begin = end + 1
	}
	return selected
}

func nextRetainedRun(v api.Vehicle, visits []StopTime, stops map[string]api.Stop, begin int) (int, int, bool) {
	for begin < len(visits) {
		if hasRetainedVisit(v.OperatorId, visits[begin], stops) {
			break
		}
		begin++
	}
	end, contains := begin, false
	for end < len(visits) {
		if !hasRetainedVisit(v.OperatorId, visits[end], stops) {
			break
		}
		if qualify(v.OperatorId, visits[end].Stop) == historyString(v.StopId) {
			contains = true
		}
		end++
	}
	return begin, end, contains
}

func appendJourneyVisits(operator string, visits []StopTime, stops map[string]api.Stop, path *patterns.ProviderJourney) bool {
	if len(visits) < 2 || len(visits) > 512 {
		return false
	}
	for _, visit := range visits {
		id := qualify(operator, visit.Stop)
		stop, exists := stops[id]
		if !exists || stop.Name == "" {
			return false
		}
		path.Visits = append(path.Visits, patterns.ProviderVisit{Stop: id, Name: stop.Name, Sequence: visit.Sequence, Lat: stop.Lat, Lon: stop.Lon})
	}
	return true
}
