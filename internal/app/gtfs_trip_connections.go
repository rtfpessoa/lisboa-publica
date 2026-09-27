package app

import "sort"

func (g *gtfsReader) connectTrips() {
	g.data.Schedule.Trips = make([]ScheduledTrip, 0, len(g.trips))
	for _, t := range g.trips {
		t.compactArrivalTiming(g.provider)
		sort.Slice(t.JourneyTimes, func(i, j int) bool { return t.JourneyTimes[i].Sequence < t.JourneyTimes[j].Sequence })
		g.retainTripVisits(t)
		if len(t.Times) == 0 {
			continue
		}
		sort.Slice(t.Times, func(i, j int) bool { return t.Times[i].Sequence < t.Times[j].Sequence })
		// Retained revisions need visits, not unused append capacity.
		if cap(t.Times) > len(t.Times) {
			visits := make([]StopTime, len(t.Times))
			copy(visits, t.Times)
			t.Times = visits
		}
		g.retainTripRoute(t)
		g.data.Schedule.Trips = append(g.data.Schedule.Trips, *t)
	}
}

// The local and complete sequence coincide for most providers. Retain one
// sequence in that case; outside-area visits still keep the complete sequence.
func (g *gtfsReader) retainTripVisits(t *ScheduledTrip) {
	full := append([]StopTime(nil), t.JourneyTimes...)
	localCount := 0
	for _, visit := range full {
		if g.stops[visit.Stop] != nil {
			localCount++
		}
	}
	if localCount == len(full) {
		t.Times, t.JourneyTimes = full, nil
		return
	}
	t.JourneyTimes = full
	t.Times = make([]StopTime, 0, localCount)
	for _, visit := range full {
		if g.stops[visit.Stop] != nil {
			t.Times = append(t.Times, visit)
		}
	}
}

func (g *gtfsReader) connectTripShapes() {
	for _, trip := range g.trips {
		if trip.Shape != "" && len(g.shapes[trip.Shape]) >= 2 && (g.shapeForRoute[trip.Route] == "" || trip.Shape < g.shapeForRoute[trip.Route]) {
			g.shapeForRoute[trip.Route] = trip.Shape
		}
	}
}

func (g *gtfsReader) retainTripRoute(trip *ScheduledTrip) {
	if g.routeStops[trip.Route] == nil {
		g.routeStops[trip.Route] = map[string]bool{}
	}
	for _, visit := range trip.Times {
		g.routeStops[trip.Route][visit.Stop] = true
	}
	if g.provider.ID == "cm" {
		trip.PackedCount = len(trip.Times)
		trip.PackedTimes = packVisits(trip.Times)
		trip.Times = nil
	}
}
