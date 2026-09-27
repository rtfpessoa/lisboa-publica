package app

import (
	"lisboapublica/internal/api"
	"time"
)

func missingCallTime(reason string) api.CallTime {
	return api.CallTime{Kind: "unavailable", Reason: reason}
}

func selectCallTime(actual, prediction, schedule *api.CallTimeEvidence, past bool, now time.Time) api.CallTime {
	out := api.CallTime{Kind: "unavailable", Reason: "Sem registo real", Actual: actual}
	if actual != nil {
		out.Kind = "actual"
		out.At = &actual.At
		out.Reason = ""
		return out
	}
	if past {
		return out
	}
	out.Schedule = schedule
	if prediction != nil && !prediction.At.Before(now) && prediction.ValidUntil != nil && time.Now().Before(*prediction.ValidUntil) {
		out.Prediction = prediction
		out.Kind = "prediction"
		out.At = &prediction.At
		out.Reason = ""
		return out
	}
	if schedule != nil && !schedule.At.Before(now) {
		out.Kind = "schedule"
		out.At = &schedule.At
		out.Reason = ""
	}
	return out
}

func callInWindow(c api.StopCall, from, to time.Time) bool {
	for _, v := range []*time.Time{c.Arrival.At, c.Departure.At} {
		if v != nil && !v.Before(from) && v.Before(to) {
			return true
		}
	}
	return false
}

func nextCallTime(c api.StopCall) time.Time {
	a, b := c.Arrival.At, c.Departure.At
	if a == nil && b == nil {
		return time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if a == nil {
		return *b
	}
	if b == nil || a.Before(*b) {
		return *a
	}
	return *b
}

func conflictedCallTime(value api.CallTime) bool {
	return value.Reason == "Registos reais contraditórios na fonte"
}

func pastCallTime(value api.CallTime, now time.Time) api.CallTime {
	if conflictedCallTime(value) {
		return value
	}
	return selectCallTime(value.Actual, nil, nil, true, now)
}
