package app

import "time"

// A uniquely supported service day never becomes an observed operating date.
func tmlArrivalDay(i *tmlArrivalIndex, t *ScheduledTrip, u cpUpdate, now time.Time) (time.Time, bool) {
	timing := unpackArrivalTiming(t.ArrivalTiming)
	if !usableArrivalTiming(i.index.Data, timing, u) {
		return time.Time{}, false
	}
	proof := arrivalDayProof{index: i, trip: t, update: u}
	if u.Trip.Date != "" {
		return proof.publishedDay()
	}
	return proof.inferredDay(timing, now)
}

func usableArrivalTiming(d *StaticData, t *cpTripTiming, u cpUpdate) bool {
	valid := d.ArrivalMetadata && !d.HasFrequencies && t != nil
	if valid {
		valid = !t.Invalid && t.LastArrival >= t.FirstDeparture
	}
	if valid && u.Trip.StartTime != "" {
		start, err := parseClock(u.Trip.StartTime)
		valid = err == nil && start == t.FirstDeparture
	}
	return valid
}

type arrivalDayProof struct {
	index  *tmlArrivalIndex
	trip   *ScheduledTrip
	update cpUpdate
}

func (p arrivalDayProof) active(day time.Time) bool {
	d, date := p.index.index.Data, day.Format("20060102")
	return (d.ValidFrom == "" || date >= d.ValidFrom) && (d.ValidUntil == "" || date <= d.ValidUntil) && d.Schedule.active(p.trip.Service, day)
}

func (p arrivalDayProof) publishedDay() (time.Time, bool) {
	day, err := time.ParseInLocation("20060102", p.update.Trip.Date, lisbon)
	return day, err == nil && p.active(day) && tmlAbsoluteCompatible(p.trip, p.update, day)
}

func (p arrivalDayProof) inferredDay(t *cpTripTiming, now time.Time) (time.Time, bool) {
	if t.LastArrival-t.FirstArrival+2*cpMaxDeviation >= secondsPerDay {
		return time.Time{}, false
	}
	var found time.Time
	matches, viable := 0, 0
	for delta := -(maxGTFSServiceHours/hoursPerDay + 1); delta <= 1; delta++ {
		if p.index.index.Context.Err() != nil {
			return time.Time{}, false
		}
		day := now.In(lisbon).AddDate(0, 0, delta)
		if !p.active(day) {
			continue
		}
		if p.absoluteDay(day) {
			viable++
		}
		if p.provenDay(day) {
			matches++
			found = day
		}
	}
	return inferredArrivalDay(found, matches, viable)
}

func inferredArrivalDay(found time.Time, matches, viable int) (time.Time, bool) {
	if matches > 1 {
		return time.Time{}, false
	}
	if matches == 1 {
		return found, true
	}
	return time.Time{}, viable == 1
}

func arrivalAbsoluteVisit(t *ScheduledTrip, s cpStopUpdate) *StopTime {
	if s.Sequence == nil || s.Arrival.Time == nil || !cpScheduled(s.Relationship) {
		return nil
	}
	return uniqueCPSequence(t, *s.Sequence)
}

func (p arrivalDayProof) absoluteDay(day time.Time) bool {
	absolute, compatible := false, true
	for _, s := range p.update.Stops {
		if v := arrivalAbsoluteVisit(p.trip, s); v != nil {
			absolute = true
			if !arrivalAbsoluteWithin(s, v, day) {
				compatible = false
				break
			}
		}
	}
	return absolute && compatible
}

func arrivalAbsoluteWithin(s cpStopUpdate, v *StopTime, day time.Time) bool {
	planned := serviceStart(day).Add(time.Duration(v.Arrival) * time.Second)
	at := time.Unix(*s.Arrival.Time, 0)
	return validArrivalUnix(*s.Arrival.Time) && !at.Before(planned.Add(-cpMaxDeviation*time.Second)) && !at.After(planned.Add(cpMaxDeviation*time.Second))
}

func (p arrivalDayProof) provenDay(day time.Time) bool {
	proof, consistent := false, true
	for _, s := range p.update.Stops {
		if s.Arrival.Delay == nil {
			continue
		}
		if v := arrivalAbsoluteVisit(p.trip, s); v != nil {
			proof = true
			if !arrivalDelayAgrees(s, v, day) {
				consistent = false
				break
			}
		}
	}
	return proof && consistent
}

func arrivalDelayAgrees(s cpStopUpdate, v *StopTime, day time.Time) bool {
	return validArrivalUnix(*s.Arrival.Time) && validArrivalDelay(*s.Arrival.Delay) && serviceStart(day).Add(time.Duration(v.Arrival+*s.Arrival.Delay)*time.Second).Unix() == *s.Arrival.Time
}
