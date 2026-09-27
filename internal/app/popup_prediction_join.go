package app

import (
	"lisboapublica/internal/api"
	"strings"
	"time"
)

func applyCPPredictions(call *api.StopCall, state *State, t *ScheduledTrip, day, now time.Time) {
	operator, _, _ := strings.Cut(call.StopId, ":")
	predictions := predictionsFor(state, operator)
	if predictions == nil {
		return
	}
	for _, row := range appendCPEvents(predictions) {
		if !(popupVisitForecast{call, state.Static[operator], t, day, operator}).matches(row) {
			continue
		}
		pred := &api.CallTimeEvidence{At: row.ExpectedAt, DelaySeconds: row.DelaySeconds, SourceUrl: row.SourceUrl, SourceUpdatedAt: &row.SourceUpdatedAt, CollectedAt: &row.CollectedAt, ValidUntil: &row.ValidUntil}
		if row.ExpectedDepartureAt == nil && !conflictedCallTime(call.Arrival) {
			call.Arrival = selectCallTime(call.Arrival.Actual, pred, call.Arrival.Schedule, call.Phase == "previous", now)
		}
		if row.ExpectedDepartureAt != nil && !conflictedCallTime(call.Departure) {
			dep := *pred
			dep.At = *row.ExpectedDepartureAt
			call.Departure = selectCallTime(call.Departure.Actual, &dep, call.Departure.Schedule, call.Phase == "previous", now)
		}
	}
}

func appendCPEvents(data *CPData) []api.CPPrediction {
	rows := make([]api.CPPrediction, 0, len(data.Rows)+len(data.Departures))
	rows = append(rows, data.Rows...)
	return append(rows, data.Departures...)
}

// A source can publish a valid station forecast without a uniquely dated
// planned instance. Preserve it as a separate call, never graft it onto a vehicle.

type popupVisitForecast struct {
	call     *api.StopCall
	data     *StaticData
	trip     *ScheduledTrip
	day      time.Time
	operator string
}

func (p popupVisitForecast) matches(row api.CPPrediction) bool {
	call, d, t, day, op := p.call, p.data, p.trip, p.day, p.operator
	identity := row.SourceTripId == qualify(op, t.ID) && row.PlanId == predictionTripPlan(d, t)
	visit := row.StopSequence == call.StopSequence && row.StopId == call.StopId
	return identity && visit && popupForecastDateMatches(row, day)
}
func popupForecastDateMatches(row api.CPPrediction, day time.Time) bool {
	return row.ServiceDate != nil && row.ServiceDate.Format("2006-01-02") == day.Format("2006-01-02")
}
