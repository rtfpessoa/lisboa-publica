package app

import (
	"sort"
	"time"
)

// A date-less descriptor is resolved as a planned instance, never an observed vehicle date.
func (i *cpIndex) instance(t *ScheduledTrip, u cpUpdate, at time.Time) (time.Time, string) {
	explicit, _, valid := i.descriptor(t, u)
	if !valid {
		return time.Time{}, "invalid"
	}
	if !cpScheduledJoin(i.Data, t) || !cpScheduled(u.Trip.Relationship) {
		return time.Time{}, ""
	}
	return i.scheduledInstance(t, u, explicit, at)
}

func (i *cpIndex) scheduledInstance(t *ScheduledTrip, u cpUpdate, day, at time.Time) (time.Time, string) {
	start, _ := parseClock(u.Trip.StartTime)
	if u.Trip.StartTime != "" && start != popupTripTiming(t).FirstDeparture {
		return time.Time{}, "invalid"
	}
	if !day.IsZero() {
		return i.publishedInstance(t, u, day, at)
	}
	return i.inferredInstance(t, u, at)
}

func (i *cpIndex) inferredInstance(t *ScheduledTrip, u cpUpdate, at time.Time) (time.Time, string) {
	span := popupTripTiming(t).LastArrival - popupTripTiming(t).FirstArrival
	if span >= secondsPerDay || span+2*cpMaxDeviation >= secondsPerDay || !cpBoundedDeviations(u) {
		return time.Time{}, ""
	}
	return i.uniqueDay(t, u, at)
}

func (i *cpIndex) descriptor(t *ScheduledTrip, u cpUpdate) (time.Time, int, bool) {
	var day time.Time
	var err error
	if u.Trip.Date != "" {
		day, err = time.ParseInLocation("20060102", u.Trip.Date, lisbon)
		if err != nil || !i.active(t, day) {
			return time.Time{}, 0, false
		}
	}
	start := 0
	if u.Trip.StartTime != "" {
		start, err = parseClock(u.Trip.StartTime)
	}
	return day, start, err == nil
}

func (i *cpIndex) publishedInstance(t *ScheduledTrip, u cpUpdate, day, at time.Time) (time.Time, string) {
	if !i.consistent(t, u, day, at) {
		return time.Time{}, "invalid"
	}
	return day, "published"
}

func cpBoundedDeviations(u cpUpdate) bool {
	for _, s := range u.Stops {
		if s.Arrival.Delay != nil && (*s.Arrival.Delay < -cpMaxDeviation || *s.Arrival.Delay > cpMaxDeviation) {
			return false
		}
	}
	return true
}

func (i *cpIndex) active(t *ScheduledTrip, day time.Time) bool {
	date := day.Format("20060102")
	return date >= i.Data.ValidFrom && date <= i.Data.ValidUntil && i.Data.Schedule.active(t.Service, day)
}

func (i *cpIndex) uniqueDay(t *ScheduledTrip, u cpUpdate, at time.Time) (time.Time, string) {
	local := at.In(lisbon)
	anchor := time.Date(local.Year(), local.Month(), local.Day(), cpCalendarAnchorHour, 0, 0, 0, lisbon)
	var found time.Time
	// Existing GTFS hours are bounded; deviations add at most one extra day.
	for delta := -(maxGTFSServiceHours/hoursPerDay + 1); delta <= 1; delta++ {
		if i.Context.Err() != nil {
			return time.Time{}, "invalid"
		}
		day := anchor.AddDate(0, 0, delta)
		if !i.active(t, day) || !i.consistent(t, u, day, at) {
			continue
		}
		if !found.IsZero() {
			return time.Time{}, ""
		}
		found = day
	}
	if found.IsZero() {
		return found, "invalid"
	}
	return found, "matched_schedule"
}

func (i *cpIndex) verifiedCalls(t *ScheduledTrip, u cpUpdate, day time.Time) (map[int]time.Time, bool) {
	calls := map[int]time.Time{}
	for _, s := range u.Stops {
		if i.Context.Err() != nil {
			return nil, false
		}
		v := i.visit(t, s)
		if v == nil || !cpScheduled(s.Relationship) {
			continue
		}
		expected, valid, consistent := cpVisitExpectation(s, v, day)
		if !valid {
			continue
		}
		if prior, exists := calls[v.Sequence]; exists && !prior.Equal(expected) {
			return nil, false
		}
		calls[v.Sequence] = expected
		if !consistent {
			return nil, false
		}
	}
	return calls, true
}

func cpEventConsistent(s cpStopUpdate, expected, planned time.Time) bool {
	if s.Arrival.Time == nil || s.Arrival.Delay == nil {
		return true
	}
	return expected.Equal(planned.Add(time.Duration(*s.Arrival.Delay) * time.Second))
}

func (i *cpIndex) consistent(t *ScheduledTrip, u cpUpdate, day, at time.Time) bool {
	calls, valid := i.verifiedCalls(t, u, day)
	return valid && cpCallsUpcoming(calls, at) && cpCallsOrdered(calls)
}

func cpCallsUpcoming(calls map[int]time.Time, at time.Time) bool {
	for _, expected := range calls {
		if !expected.Before(at) && !expected.After(at.Add(2*time.Hour)) {
			return true
		}
	}
	return false
}

func cpCallsOrdered(calls map[int]time.Time) bool {
	sequences := make([]int, 0, len(calls))
	for sequence := range calls {
		sequences = append(sequences, sequence)
	}
	sort.Ints(sequences)
	for n := 1; n < len(sequences); n++ {
		if calls[sequences[n]].Before(calls[sequences[n-1]]) {
			return false
		}
	}
	return true
}

func cpExpected(s cpStopUpdate, planned time.Time) (time.Time, bool) {
	if s.Arrival.Delay != nil && (*s.Arrival.Delay < -secondsPerDay || *s.Arrival.Delay > secondsPerDay) {
		return time.Time{}, false
	}
	if s.Arrival.Time != nil {
		return cpAbsolute(*s.Arrival.Time)
	}
	return cpDelayed(s.Arrival.Delay, planned)
}

func cpDelayed(delay *int, planned time.Time) (time.Time, bool) {
	if delay == nil || planned.IsZero() {
		return time.Time{}, false
	}
	return planned.Add(time.Duration(*delay) * time.Second), true
}

func cpAbsolute(seconds int64) (time.Time, bool) {
	if seconds <= 0 || seconds > cpLatestTimestamp {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}

func cpVisitExpectation(s cpStopUpdate, v *StopTime, day time.Time) (time.Time, bool, bool) {
	if s.Arrival.Time == nil && s.Arrival.Delay == nil {
		planned := serviceStart(day).Add(time.Duration(v.Departure) * time.Second)
		expected, valid := cpDepartureExpected(s, planned)
		return expected, valid, cpDepartureConsistent(s, expected, planned)
	}
	planned := serviceStart(day).Add(time.Duration(v.Arrival) * time.Second)
	expected, valid := cpExpected(s, planned)
	return expected, valid, cpEventConsistent(s, expected, planned)
}
func cpDepartureConsistent(s cpStopUpdate, expected, planned time.Time) bool {
	return s.Departure.Time == nil || s.Departure.Delay == nil || expected.Equal(planned.Add(time.Duration(*s.Departure.Delay)*time.Second))
}
