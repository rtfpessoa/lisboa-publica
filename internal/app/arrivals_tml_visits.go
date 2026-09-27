package app

import (
	"fmt"
	"lisboapublica/internal/api"
	"time"
)

// A journey validates every visit before exposing only demanded stops.
type tmlArrivalJourney struct {
	batch         *tmlArrivalBatch
	index         *tmlArrivalIndex
	trip          *ScheduledTrip
	update        cpUpdate
	day, observed time.Time
}

func (j *tmlArrivalJourney) collect() {
	if !j.buildMappings() {
		return
	}
	if !cpCurrent(j.observed, j.batch.now) {
		j.rememberStale()
		return
	}
	day, valid := tmlArrivalDay(j.index, j.trip, j.update, j.batch.now)
	if valid {
		j.day = day
		j.collectVisits()
	} else {
		j.index.partial = true
	}
}

func (j *tmlArrivalJourney) buildMappings() bool {
	for _, s := range j.update.Stops {
		if j.batch.ctx.Err() != nil {
			return false
		}
		if !usableMappingVisit(s) {
			continue
		}
		v := uniqueCPSequence(j.trip, *s.Sequence)
		if v != nil && len(v.Stop) <= cpMaxStopBytes {
			if !j.addMapping(s.ID, v.Stop) {
				return false
			}
		}
	}
	return true
}

func usableMappingVisit(s cpStopUpdate) bool {
	return s.Sequence != nil && s.ID != "" && cpScheduled(s.Relationship)
}

func (j *tmlArrivalJourney) addMapping(source, stop string) bool {
	i, b := j.index, j.batch
	if i.reverse[stop] == "" {
		b.mappings++
	}
	if i.index.Crosswalk[source] == "" {
		b.mappings++
	}
	if b.mappings > arrivalMappingLimit {
		b.overflow = true
	} else {
		i.index.addMapping(source, stop, i.reverse)
	}
	return !b.overflow
}

func (j *tmlArrivalJourney) rememberStale() {
	for _, s := range j.update.Stops {
		if s.Sequence == nil {
			continue
		}
		v := uniqueCPSequence(j.trip, *s.Sequence)
		if v == nil || !j.index.demanded(j.batch.wanted, v.Stop) {
			continue
		}
		if !validArrivalUnix(j.update.Timestamp) || j.observed.After(j.batch.now.Add(providerClockSkew)) {
			j.index.partial = true
		} else {
			j.markStale(v.Stop)
		}
	}
}

func (j *tmlArrivalJourney) markStale(raw string) {
	for requested, data := range j.batch.wanted {
		if data != j.index.index.Data {
			continue
		}
		if j.index.stopMatches(raw, requested) && j.observed.After(j.index.stale[requested]) {
			j.index.stale[requested] = j.observed
		}
	}
}

func (j *tmlArrivalJourney) collectVisits() {
	for _, s := range j.update.Stops {
		if j.batch.ctx.Err() != nil {
			return
		}
		if s.Sequence == nil || !cpScheduled(s.Relationship) {
			continue
		}
		v := uniqueCPSequence(j.trip, *s.Sequence)
		if v == nil {
			j.index.partial = true
		} else if j.index.demanded(j.batch.wanted, v.Stop) {
			j.collectVisit(s, v)
		}
		if j.batch.overflow {
			return
		}
	}
}

func (j *tmlArrivalJourney) collectVisit(s cpStopUpdate, v *StopTime) {
	planned := j.plannedTime(v)
	expected, valid := arrivalExpectedTime(s, planned)
	if !valid {
		j.index.partial = true
	} else if !expected.Before(j.batch.now) && !expected.After(j.batch.now.Add(arrivalForecastHorizon)) {
		if j.batch.candidates >= arrivalCandidateLimit {
			j.batch.overflow = true
		} else {
			j.batch.candidates++
			row := j.row(v, planned, expected)
			j.index.candidates = append(j.index.candidates, tmlArrivalCandidate{row: row, sourceStop: s.ID})
		}
	}
}

func (j *tmlArrivalJourney) plannedTime(v *StopTime) *time.Time {
	if j.day.IsZero() {
		return nil
	}
	return ptr(serviceStart(j.day).Add(time.Duration(v.Arrival) * time.Second).UTC())
}

func arrivalExpectedTime(s cpStopUpdate, planned *time.Time) (time.Time, bool) {
	if s.Arrival.Time != nil && validArrivalUnix(*s.Arrival.Time) {
		return time.Unix(*s.Arrival.Time, 0).UTC(), true
	}
	if usableArrivalDelay(s, planned) {
		return planned.Add(time.Duration(*s.Arrival.Delay) * time.Second), true
	}
	return time.Time{}, false
}

func usableArrivalDelay(s cpStopUpdate, planned *time.Time) bool {
	return s.Arrival.Time == nil && s.Arrival.Delay != nil && planned != nil && validArrivalDelay(*s.Arrival.Delay)
}

func (j *tmlArrivalJourney) row(v *StopTime, planned *time.Time, expected time.Time) api.Arrival {
	i, u := j.index, j.update
	stop := qualify(i.provider.ID, v.Stop)
	date := "unknown"
	if !j.day.IsZero() {
		date = j.day.Format("20060102")
	}
	observed := j.observed
	expiry := observed.Add(sourceFreshness)
	row := api.Arrival{Id: fmt.Sprintf("%s|%s|%s|%s|%d", i.index.Data.PlanID, u.Trip.ID, date, stop, v.Sequence), OperatorId: i.provider.ID, StopId: stop, RouteId: qualify(i.provider.ID, j.trip.Route), TripId: qualify(i.provider.ID, j.trip.ID), Headsign: cleanCPName(j.trip.Headsign), Kind: "prediction", ExpectedAt: &expected, ScheduledAt: planned, ObservedAt: &observed, SourceUpdatedAt: &observed, ValidUntil: &expiry, SourceUrl: cpSourceURL, PlanId: optional(i.index.Data.PlanID), SourceTripId: optional(u.Trip.ID), StopSequence: &v.Sequence, RouteName: optional(tmlArrivalRouteName(i.index.Data, qualify(i.provider.ID, j.trip.Route)))}
	if !j.day.IsZero() {
		row.ServiceDate = ptr(apiDate(j.day))
		row.DateBasis = ptr(api.ArrivalDateBasisMatchedSchedule)
		if u.Trip.Date != "" {
			row.DateBasis = ptr(api.ArrivalDateBasisPublished)
		}
		row.VehicleId = tmlArrivalVehicle(j.batch.state, i, u, j.day, j.batch.now)
	}
	return row
}

func (i *tmlArrivalIndex) stopMatches(raw, requested string) bool {
	return qualify(i.provider.ID, raw) == requested || qualify(i.provider.ID, i.index.Data.Schedule.Parents[raw]) == requested
}
