package app

import (
	"time"

	"lisboapublica/internal/patterns"
)

func (f *Fetcher) recordArrivalHistory(operator, stop string, v arrivalSnapshot) {
	if f.Patterns == nil || operator == "metro" || v.availability.CollectedAt == nil {
		return
	}
	at := *v.availability.CollectedAt
	r := patterns.ProviderReceipt{Operator: operator, ReceivedAt: at, Partial: true, PredictionStop: stop, Rows: []patterns.Observation{}, Limited: v.availability.Status == "partial"}
	if v.availability.Status == "error" || v.availability.Status == "unavailable" {
		r.Error = "prediction_refresh_failed"
	}
	for _, a := range v.rows {
		if a.Kind != "prediction" || a.ExpectedAt == nil || a.ValidUntil == nil {
			continue
		}
		date := ""
		if a.ServiceDate != nil {
			date = a.ServiceDate.Time.Format("2006-01-02")
		}
		r.Predictions = append(r.Predictions, patterns.ProviderPrediction{ID: "stop:" + a.Id, Stop: a.StopId, Route: a.RouteId, Trip: a.TripId, Plan: historyString(a.PlanId), ServiceDate: date, Vehicle: historyString(a.VehicleId), Sequence: a.StopSequence, ExpectedAt: *a.ExpectedAt, ReceivedAt: at, ValidUntil: *a.ValidUntil, SourceAt: a.SourceUpdatedAt})
		if len(r.Predictions) >= 2000 {
			break
		}
	}
	f.enqueueProviderHistory(r)
}
func (f *Fetcher) recordFeedHistory(results map[string]*CPData) {
	if f.Patterns == nil {
		return
	}
	current, _ := f.Cache.state("")
	for operator, data := range results {
		if operator == "metro" || predictionsFor(current, operator) != data || data.Availability.CollectedAt == nil {
			continue
		}
		f.recordFeedSnapshot(operator, data)
	}
}

func (f *Fetcher) recordFeedSnapshot(operator string, data *CPData) {
	at := *data.Availability.CollectedAt
	r := patterns.ProviderReceipt{Operator: operator, ReceivedAt: at, Partial: true, PredictionStop: "*", Rows: []patterns.Observation{}, Limited: len(data.Rows) > 2000 || data.Availability.Status == "partial"}
	if data.Availability.Status == "error" || data.Availability.Status == "stale" {
		r.Error = "prediction_refresh_failed"
	}
	for _, a := range data.Rows {
		date := ""
		if a.ServiceDate != nil {
			date = a.ServiceDate.Time.Format("2006-01-02")
		}
		source := a.SourceUpdatedAt
		r.Predictions = append(r.Predictions, patterns.ProviderPrediction{ID: "feed:" + a.Id, Stop: a.StopId, Route: a.RouteId, Trip: a.SourceTripId, Plan: a.PlanId, ServiceDate: date, Sequence: &a.StopSequence, ExpectedAt: a.ExpectedAt, ReceivedAt: at, ValidUntil: a.ValidUntil, SourceAt: &source})
		if len(r.Predictions) >= 2000 {
			break
		}
	}
	// This is a previously normalized snapshot, not a fresh source timestamp.
	if !at.IsZero() && !at.After(time.Now().Add(providerClockSkew)) {
		f.enqueueProviderHistory(r)
	}
}
