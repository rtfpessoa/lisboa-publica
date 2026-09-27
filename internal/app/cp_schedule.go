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
	if !data.CPPredictionMetadata || data.CPHasFrequencies || trip.CPTiming == nil {
		return false
	}
	t := trip.CPTiming
	return !t.Invalid && t.FirstSequence < t.LastSequence && t.LastArrival >= t.FirstDeparture
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
