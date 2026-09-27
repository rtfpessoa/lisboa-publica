package app

import "sort"

func (g *gtfsReader) connectTrips() {
	for _, t := range g.trips {
		t.compactArrivalTiming(g.provider)
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
		g.data.Schedule.Trips = append(g.data.Schedule.Trips, *t)
		if g.routeStops[t.Route] == nil {
			g.routeStops[t.Route] = map[string]bool{}
		}
		for _, v := range t.Times {
			g.routeStops[t.Route][v.Stop] = true
		}
		if t.Shape != "" && len(g.shapes[t.Shape]) >= 2 && (g.shapeForRoute[t.Route] == "" || t.Shape < g.shapeForRoute[t.Route]) {
			g.shapeForRoute[t.Route] = t.Shape
		}
	}
}
