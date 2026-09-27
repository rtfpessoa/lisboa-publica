package app

import (
	"lisboapublica/internal/api"
	"time"
)

func validateBoardPredictionRevision(state *State, op string, f Filter) error {
	if f.Revision == "" {
		return nil
	}
	expired := boardProviderForecastExpired(state, op, f)
	if op == "metro" {
		expired = expired || boardMetroForecastExpired(state, f)
	}
	var err error
	if expired {
		err = fail(410, "prediction_revision_expired", "As previsões expiraram. Atualize a primeira página.")
	}
	return err
}
func boardProviderForecastExpired(state *State, op string, f Filter) bool {
	data, d := predictionsFor(state, op), state.Static[op]
	if data == nil || d == nil || d.Schedule == nil {
		return false
	}
	r := popupForecastRead{state: state, data: d, operator: op, filter: f}
	for _, row := range appendCPEvents(data) {
		if r.expired(row) {
			return true
		}
	}
	return false
}
func (r popupForecastRead) expired(row api.CPPrediction) bool {
	return r.matchesStop(row) && r.inWindow(row) && r.state.Created.Before(row.ValidUntil) && !time.Now().Before(row.ValidUntil)
}
func boardMetroForecastExpired(state *State, f Filter) bool {
	for _, row := range predictedArrivals(state, f.From, f.To, "", f.Stop) {
		if row.ObservedAt != nil && !time.Now().Before(row.ObservedAt.Add(sourceFreshness)) {
			return true
		}
	}
	return false
}
