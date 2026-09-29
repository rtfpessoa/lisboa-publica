package app

import (
	"encoding/json"
	"lisboapublica/internal/patterns"
	"math"
	"strconv"
	"strings"
	"time"
)

type metroOperationalAxisData struct {
	Geometry [][]float64
	Profile  string
}

func (r *metroRuntime) validHubModelContext(p hubPosition) bool {
	id, plan := verifiedHubTrip(p.Trip, "IA2N9", r.plan.PlanID)
	if !metroSamePlan(plan, r.plan.PlanID) {
		return false
	}
	trip := metroUniqueScheduledTrip(r.plan.Schedule.Trips, id)
	if trip == nil {
		return false
	}
	return metroHubTripMatches(p, trip, r.plan.PlanID)
}
func metroSamePlan(plan *string, expected string) bool { return plan != nil && *plan == expected }
func metroUniqueScheduledTrip(trips []ScheduledTrip, id string) *ScheduledTrip {
	var found *ScheduledTrip
	for n := range trips {
		if trips[n].ID != id {
			continue
		}
		if found != nil {
			return nil
		}
		found = &trips[n]
	}
	return found
}
func metroHubTripMatches(p hubPosition, trip *ScheduledTrip, plan string) bool {
	if trip.Route != verifiedHubID(p.Route, "IA2N9") || trip.Direction == nil {
		return false
	}
	if metroPublishedDirection(p.Direction) != *trip.Direction {
		return false
	}
	shape, shapePlan := verifiedHubTrip(p.Shape, "IA2N9", plan)
	patternMatches := trip.Pattern == "" || verifiedHubID(p.Pattern, "IA2N9") == trip.Pattern
	return metroSamePlan(shapePlan, plan) && shape == trip.Shape && patternMatches
}

func metroPublishedDirection(raw json.RawMessage) int {
	var value *int
	if json.Unmarshal(raw, &value) == nil && value != nil {
		return *value
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if n, err := strconv.Atoi(text); err == nil {
			return n
		}
	}
	return -1
}
func (r *metroRuntime) operationalAxisGeometry(route string, now time.Time) [][]float64 {
	if r.operationalAxes == nil {
		r.operationalAxes = map[string]metroOperationalAxisData{}
	}
	if cached, ok := r.operationalAxes[route]; ok && cached.Profile == r.topology.Profile {
		return cached.Geometry
	}
	axis := metroOperationalAxis(r.topology, route)
	segments := metroPathGeometry(r.publication, r.plan, axis)
	geometry := [][]float64{}
	for n := 0; n+1 < len(axis.Stops); n++ {
		if n >= len(segments) || len(segments[n]) < 2 {
			geometry = nil
			break
		}
		points := segments[n]
		if len(geometry) > 0 {
			points = points[1:]
		}
		geometry = append(geometry, points...)
	}
	if len(geometry) < 2 {
		geometry = metroRawAxisGeometry(r.publication, r.plan, axis)
	}
	r.operationalAxes[route] = metroOperationalAxisData{geometry, r.topology.Profile}
	return geometry
}

func metroRawAxisGeometry(data *MetroData, static *StaticData, path patterns.Pattern) [][]float64 {
	b := metroPlanBuilder{data: data, static: static, stations: map[string]MetroStation{}, mapping: map[string]string{}, routes: map[string]string{}}
	b.topology.Stations = []patterns.Station{}
	b.addStations()
	b.mapStaticPlan()
	for _, trip := range static.Schedule.Trips {
		if b.routes[trip.Route] != path.Route {
			continue
		}
		visits := localJourneyTimes(&trip)
		codes := []string{}
		for _, visit := range visits {
			codes = append(codes, b.mapping[visit.Stop])
		}
		if strings.Join(codes, "|") != strings.Join(path.Stops, "|") {
			continue
		}
		geometry := uniqueMetroTripGeometry(static, trip)
		if metroGeometrySupportsVisits(geometry, visits, plannedStationCoordinates(static)) {
			return geometry
		}
	}
	return nil
}
func metroGeometrySupportsVisits(geometry [][]float64, visits []StopTime, stations map[string][2]float64) bool {
	previous := -1.0
	qualified := 0
	for _, visit := range visits {
		coordinate, exists := stations[visit.Stop]
		if !exists {
			return false
		}
		metres, distance := metroGeometryProgress(geometry, hubPosition{Lon: coordinate[0], Lat: coordinate[1]})
		if math.IsInf(distance, 1) {
			continue
		}
		if distance > metroGeometryEnvelopeMetres || metres <= previous {
			return false
		}
		previous = metres
		qualified++
	}
	return qualified >= 3
}
