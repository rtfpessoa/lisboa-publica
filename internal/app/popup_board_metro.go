package app

import (
	"lisboapublica/internal/api"
	"time"
)

func metroBoardCalls(state *State, f Filter) []api.StopCall {
	calls := []api.StopCall{}
	for _, a := range predictedArrivals(state, f.From, f.To, "", f.Stop) {
		if a.ObservedAt == nil || !time.Now().Before(a.ObservedAt.Add(sourceFreshness)) {
			continue
		}
		line, key := metroBoardDirection(state.Static["metro"], f.Stop, a)
		expiry := a.ObservedAt.Add(sourceFreshness)
		prediction := &api.CallTimeEvidence{At: *a.ExpectedAt, SourceUrl: a.SourceUrl, SourceUpdatedAt: a.ObservedAt, ValidUntil: &expiry}
		call := api.StopCall{Id: a.Id, StopId: f.Stop, LineKey: line, DirectionKey: key, Destination: a.Headsign, Arrival: selectCallTime(nil, prediction, nil, false, f.From), Departure: missingCallTime("Partida não publicada"), Phase: "future"}
		if callInWindow(call, f.From, f.To) {
			calls = append(calls, call)
		}
	}
	return calls
}
func metroBoardDirection(d *StaticData, stop string, a api.Arrival) (string, *string) {
	line := a.RouteId
	var key *string
	if d == nil || d.Schedule == nil {
		return line, key
	}
	idx := d.journeys("metro")
	matches := map[string]*string{}
	for _, t := range idx.stopTrips(d, "metro", stop) {
		if normalizeName(tripDestination(d.Schedule, t)) == normalizeName(a.Headsign) {
			line = idx.lineFor(t)
			if idx.directionFor(t) != nil {
				matches[*idx.directionFor(t)] = idx.directionFor(t)
			}
		}
	}
	if len(matches) == 1 {
		for _, v := range matches {
			key = v
		}
	}
	if line == a.RouteId {
		line = metroBoardLine(d, a.RouteId)
	}
	return line, key
}
func metroBoardLine(d *StaticData, id string) string {
	for _, r := range d.Routes {
		if r.Id == id {
			line, _, _ := lineForTrip(d, "metro", &ScheduledTrip{Route: r.SourceId})
			return line
		}
	}
	return id
}
