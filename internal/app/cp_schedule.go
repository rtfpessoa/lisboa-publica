package app

import (
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Only the national timing extrema are retained, not national stop-time rows.
type cpTripTiming struct {
	FirstSequence, LastSequence               int
	FirstArrival, FirstDeparture, LastArrival int
	Invalid                                   bool `json:",omitempty"`
}

func (t *ScheduledTrip) rememberCPTiming(row map[string]string, sequence int) {
	arrival, ea := parseClock(row["arrival_time"])
	departure, ed := parseClock(row["departure_time"])
	if t.CPTiming == nil {
		t.CPTiming = &cpTripTiming{FirstSequence: sequence, LastSequence: sequence, FirstArrival: arrival, FirstDeparture: departure, LastArrival: arrival}
	}
	timing := t.CPTiming
	if ea != nil || ed != nil || departure < arrival {
		timing.Invalid = true
	}
	if sequence < timing.FirstSequence {
		timing.FirstSequence, timing.FirstArrival, timing.FirstDeparture = sequence, arrival, departure
	} else if sequence == timing.FirstSequence && (timing.FirstDeparture != departure || timing.FirstArrival != arrival) {
		timing.Invalid = true
	}
	if sequence > timing.LastSequence {
		timing.LastSequence, timing.LastArrival = sequence, arrival
	} else if sequence == timing.LastSequence && timing.LastArrival != arrival {
		timing.Invalid = true
	}
}

func cpScheduledJoin(data *StaticData, trip *ScheduledTrip) bool {
	t := popupTripTiming(trip)
	if t == nil || !popupTimedInstanceAvailable(data) {
		return false
	}
	return !t.Invalid && t.FirstSequence < t.LastSequence && t.LastArrival >= t.FirstDeparture
}
func popupTimedInstanceAvailable(data *StaticData) bool {
	if data.CPHasFrequencies || data.Schedule != nil && data.Schedule.HasFrequencies {
		return false
	}
	ordinary := data.Operator != "" && data.Operator != "cp" && completePopupSchedule(data)
	return data.CPPredictionMetadata || ordinary
}

func apiDate(day time.Time) openapi_types.Date {
	return openapi_types.Date{Time: day.In(lisbon)}
}

func scheduledRouteName(data *StaticData, id string) string {
	for _, r := range data.Routes {
		if r.Id == id {
			return strings.TrimSpace(r.ShortName + " · " + r.LongName)
		}
	}
	return ""
}

func popupTripTiming(t *ScheduledTrip) *cpTripTiming {
	if t.CPTiming != nil {
		return t.CPTiming
	}
	timing := unpackArrivalTiming(t.ArrivalTiming)
	visits := journeyTimes(t)
	if timing != nil && len(visits) > 0 {
		timing.FirstSequence = visits[0].Sequence
		timing.LastSequence = visits[len(visits)-1].Sequence
	}
	return timing
}
