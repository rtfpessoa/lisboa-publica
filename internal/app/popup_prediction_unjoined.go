package app

import (
	"lisboapublica/internal/api"
	"strconv"
	"strings"
	"time"
)

type popupForecastRead struct {
	state    *State
	data     *StaticData
	operator string
	filter   Filter
	index    *journeyIndex
}

func appendUnjoinedPredictions(state *State, op string, f Filter, calls []api.StopCall) []api.StopCall {
	predictions, d := predictionsFor(state, op), state.Static[op]
	if predictions == nil || d == nil || d.Schedule == nil {
		return calls
	}
	read := popupForecastRead{state: state, data: d, operator: op, filter: f, index: d.journeys(op)}
	extra := map[string]api.StopCall{}
	for _, row := range appendCPEvents(predictions) {
		read.appendUnjoined(extra, calls, row)
	}
	for _, call := range extra {
		if callInWindow(call, f.From, f.To) {
			calls = append(calls, call)
		}
	}
	return calls
}
func (r popupForecastRead) matchesStop(row api.CPPrediction) bool {
	parent := r.data.Schedule.Parents[strings.TrimPrefix(row.StopId, r.operator+":")]
	return row.StopId == r.filter.Stop || qualify(r.operator, parent) == r.filter.Stop
}
func (r popupForecastRead) inWindow(row api.CPPrediction) bool {
	return !row.ExpectedAt.Before(r.filter.From) && row.ExpectedAt.Before(r.filter.To)
}
func (r popupForecastRead) appendUnjoined(extra map[string]api.StopCall, calls []api.StopCall, row api.CPPrediction) {
	if !r.matchesStop(row) || !r.inWindow(row) || !time.Now().Before(row.ValidUntil) {
		return
	}
	trips := r.index.trips[row.SourceTripId]
	if len(trips) != 1 {
		return
	}
	t := trips[0]
	if r.joined(calls, t, row) {
		return
	}
	key := unjoinedForecastKey(row)
	call, ok := extra[key]
	if !ok {
		call = api.StopCall{Id: key, StopId: row.StopId, StopName: row.StopName, StopSequence: row.StopSequence, LineKey: r.index.lines[t], DirectionKey: r.index.direction[t], Destination: row.DestinationName, ServiceLabel: row.ServiceLabel, Phase: "future", Arrival: missingCallTime("Chegada não publicada"), Departure: missingCallTime("Partida não publicada")}
	}
	forecast := popupForecastEvidence(row)
	if row.ExpectedDepartureAt != nil {
		call.Departure = selectCallTime(nil, forecast, nil, false, r.filter.From)
	} else {
		call.Arrival = selectCallTime(nil, forecast, nil, false, r.filter.From)
	}
	extra[key] = call
}
func (r popupForecastRead) joined(calls []api.StopCall, t *ScheduledTrip, row api.CPPrediction) bool {
	if row.ServiceDate == nil {
		return false
	}
	key := journeyKey(r.operator, r.data, t, row.ServiceDate.Time)
	for _, call := range calls {
		if call.JourneyId != nil && *call.JourneyId == key && call.StopSequence == row.StopSequence {
			return true
		}
	}
	return false
}
func unjoinedForecastKey(row api.CPPrediction) string {
	date := "unknown"
	if row.ServiceDate != nil {
		date = row.ServiceDate.Format("2006-01-02")
	}
	return strings.Join([]string{"forecast", row.PlanId, row.SourceTripId, date, row.StopId, strconv.Itoa(row.StopSequence)}, "|")
}
func popupForecastEvidence(row api.CPPrediction) *api.CallTimeEvidence {
	return &api.CallTimeEvidence{At: row.ExpectedAt, DelaySeconds: row.DelaySeconds, SourceUrl: row.SourceUrl, SourceUpdatedAt: &row.SourceUpdatedAt, CollectedAt: &row.CollectedAt, ValidUntil: &row.ValidUntil}
}
