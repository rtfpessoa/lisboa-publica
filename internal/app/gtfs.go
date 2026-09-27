package app

import (
	"context"
	"encoding/json"
	"fmt"

	"sort"
	"strconv"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

// Calendar defines the valid service dates and weekdays of a GTFS service.
type Calendar struct {
	Start, End string
	Days       [7]bool
}

// StopTime records a stop visit in seconds from the GTFS service-day start.
// Clock values are bounded by maxGTFSServiceHours at ingestion; int32 preserves
// that range while avoiding eight bytes per retained visit on 64-bit servers.
type StopTime struct {
	Stop      string `json:"s"`
	Arrival   int32  `json:"a"`
	Departure int32  `json:"d"`
	Sequence  int    `json:"q"`
}

// UnmarshalJSON accepts compact cache records and the original field names.
func (s *StopTime) UnmarshalJSON(b []byte) error {
	type compact StopTime
	var wire struct {
		compact
		LegacyStop      string `json:"Stop"`
		LegacyArrival   int32  `json:"Arrival"`
		LegacyDeparture int32  `json:"Departure"`
		LegacySequence  int    `json:"Sequence"`
	}
	if e := json.Unmarshal(b, &wire); e != nil {
		return e
	}
	*s = StopTime(wire.compact)
	if s.Stop == "" {
		*s = StopTime{wire.LegacyStop, wire.LegacyArrival, wire.LegacyDeparture, wire.LegacySequence}
	}
	return nil
}

// ScheduledTrip holds the published stop sequence and service of a planned trip.
type ScheduledTrip struct {
	ID, Route, Service, Headsign, Shape string
	tripPopupMetadata
	Direction     *int          `json:"direction,omitempty"`
	JourneyTimes  []StopTime    `json:"journey_times,omitempty"`
	Label         string        `json:",omitempty"`
	ArrivalTiming uint64        `json:"arrival_timing,omitempty"`
	CPTiming      *cpTripTiming `json:"cp_timing,omitempty"`
	Times         []StopTime
	Endpoints     *tripEndpoints `json:"endpoints,omitempty"`
}

// Schedule combines planned trips, service calendars and station relationships.
type Schedule struct {
	popupIndex       journeyIndexCache
	Trips            []ScheduledTrip
	Calendars        map[string]Calendar
	Exceptions       map[string]map[string]int
	StopLines        map[string][]string `json:"stop_lines,omitempty"`
	StopNames        map[string]string   `json:"stop_names,omitempty"`
	CompleteJourneys bool                `json:"complete_journeys,omitempty"`
	HasFrequencies   bool                `json:"has_frequencies,omitempty"`
	Parents          map[string]string
}

type shapePoint struct {
	Lat, Lon float64
	Sequence int
}

func uniqueSorted(a []string) []string {
	sort.Strings(a)
	r := []string{}
	for _, s := range a {
		if len(r) == 0 || r[len(r)-1] != s {
			r = append(r, s)
		}
	}
	return r
}
func parseClock(s string) (int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid GTFS time %q", s)
	}
	h, e := strconv.Atoi(parts[0])
	if e != nil || h < 0 || h > maxGTFSServiceHours {
		return 0, fmt.Errorf("invalid GTFS hour")
	}
	m, e := strconv.Atoi(parts[1])
	if e != nil || m < 0 || m > secondsPerMinute-1 {
		return 0, fmt.Errorf("invalid GTFS minute")
	}
	sec, e := strconv.Atoi(parts[2])
	if e != nil || sec < 0 || sec > secondsPerMinute-1 {
		return 0, fmt.Errorf("invalid GTFS second")
	}
	return h*secondsPerHour + m*secondsPerMinute + sec, nil
}
func serviceStart(date time.Time) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), serviceDayNoonHour, 0, 0, 0, lisbon).Add(-serviceDayNoonHour * time.Hour)
}
func (s *Schedule) active(id string, date time.Time) bool {
	key := date.In(lisbon).Format("20060102")
	if v := s.Exceptions[id][key]; v != 0 {
		return v == 1
	}
	c, ok := s.Calendars[id]
	return ok && key >= c.Start && key <= c.End && c.Days[int(date.In(lisbon).Weekday())]
}
func (q scheduleQuery) run() ([]api.Trip, []api.Arrival, error) {
	if err := q.ctx.Err(); err != nil {
		return nil, nil, err
	}
	if q.data == nil || q.data.Schedule == nil {
		return []api.Trip{}, []api.Arrival{}, nil
	}
	return q.rangeResults()
}

func (q scheduleQuery) rangeResults() ([]api.Trip, []api.Arrival, error) {
	out, arrivals := []api.Trip{}, []api.Arrival{}
	from, to := q.filter.From, q.filter.To
	day := time.Date(from.In(lisbon).Year(), from.In(lisbon).Month(), from.In(lisbon).Day(), serviceDayNoonHour, 0, 0, 0, lisbon).AddDate(0, 0, -3)
	for ; !day.After(to.In(lisbon).Add(serviceDayNoonHour * time.Hour)); day = day.AddDate(0, 0, 1) {
		date := day.Format("20060102")
		if date < q.data.ValidFrom || date > q.data.ValidUntil {
			continue
		}
		trips, visits, err := q.day(day)
		if err != nil {
			return nil, nil, err
		}
		if len(out)+len(trips)+len(arrivals)+len(visits) > maxReadResults {
			return nil, nil, readResultLimit()
		}
		out = append(out, trips...)
		arrivals = append(arrivals, visits...)
	}
	sortScheduled(out, arrivals)
	return out, arrivals, q.ctx.Err()
}

func sortScheduled(out []api.Trip, arrivals []api.Arrival) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].PlannedDeparture.Equal(out[j].PlannedDeparture) {
			return out[i].Id < out[j].Id
		}
		return out[i].PlannedDeparture.Before(out[j].PlannedDeparture)
	})
	sort.Slice(arrivals, func(i, j int) bool {
		if arrivals[i].ScheduledAt.Equal(*arrivals[j].ScheduledAt) {
			return arrivals[i].Id < arrivals[j].Id
		}
		return arrivals[i].ScheduledAt.Before(*arrivals[j].ScheduledAt)
	})
}

type scheduleQuery struct {
	ctx      context.Context
	data     *StaticData
	operator string
	filter   Filter
}

func (q scheduleQuery) day(day time.Time) ([]api.Trip, []api.Arrival, error) {
	out, arrivals := []api.Trip{}, []api.Arrival{}
	var resultErr error
	for _, t := range q.data.Schedule.Trips {
		if err := q.ctx.Err(); err != nil {
			return nil, nil, err
		}
		t, included := q.localTrip(t, day)
		if !included {
			continue
		}
		trip := q.trip(t, day)
		visits, err := q.stopVisits(t, trip, day)
		if err != nil {
			return nil, nil, err
		}
		if q.includesDeparture(t, trip) {
			out = append(out, trip)
		}
		if len(out)+len(arrivals)+len(visits) > maxReadResults {
			resultErr = readResultLimit()
			break
		}
		arrivals = append(arrivals, visits...)
	}
	if resultErr == nil {
		resultErr = q.ctx.Err()
	}
	return out, arrivals, resultErr
}

func (q scheduleQuery) includesTrip(t ScheduledTrip, day time.Time) bool {
	return (q.filter.Route == "" || q.filter.Route == qualify(q.operator, t.Route)) && q.data.Schedule.active(t.Service, day) && journeyLocalCount(&t) > 0
}

func (q scheduleQuery) trip(t ScheduledTrip, day time.Time) api.Trip {
	base := serviceStart(day)
	return api.Trip{Id: qualify(q.operator, day.Format("20060102")+":"+t.ID), OperatorId: q.operator, RouteId: qualify(q.operator, t.Route), Headsign: t.Headsign, ServiceLabel: optional(cleanCPLabel(t.Label)), PlannedDeparture: base.Add(time.Duration(t.Times[0].Departure) * time.Second), PlannedEnd: base.Add(time.Duration(t.Times[len(t.Times)-1].Arrival) * time.Second), Kind: api.TripKindScheduled, ScheduledService: t.scheduledEndpoints(q.data.Source, day)}
}

func (q scheduleQuery) stopVisits(t ScheduledTrip, trip api.Trip, day time.Time) ([]api.Arrival, error) {
	base := serviceStart(day)
	d, p, from, to, stop := q.data, q.operator, q.filter.From, q.filter.To, q.filter.Stop
	arrivals := []api.Arrival{}

	if stop != "" {
		for _, v := range localJourneyTimes(&t) {
			if err := q.ctx.Err(); err != nil {
				return nil, err
			}
			sid := qualify(p, v.Stop)
			if stop != sid && stop != qualify(p, d.Schedule.Parents[v.Stop]) {
				continue
			}
			if v.Arrival < 0 {
				continue
			}
			at := base.Add(time.Duration(v.Arrival) * time.Second)
			if at.Before(from) || !at.Before(to) {
				continue
			}
			if len(arrivals) >= maxReadResults {
				return nil, readResultLimit()
			}
			arrival := q.plannedArrival(t, trip, day, v)
			arrivals = append(arrivals, arrival)
		}
	}
	return arrivals, q.ctx.Err()
}

func (q scheduleQuery) plannedArrival(t ScheduledTrip, trip api.Trip, day time.Time, v StopTime) api.Arrival {
	d, p, id, rid := q.data, q.operator, trip.Id, trip.RouteId
	sid := qualify(p, v.Stop)
	at := serviceStart(day).Add(time.Duration(v.Arrival) * time.Second)
	arrival := api.Arrival{Id: id + ":" + v.Stop + ":" + strconv.Itoa(v.Sequence), OperatorId: p, StopId: sid, RouteId: rid, TripId: id, Headsign: t.Headsign, ScheduledAt: &at, SourceUrl: d.Source, Kind: api.ArrivalKindScheduled}
	arrival.PlanId = optional(d.PlanID)
	arrival.SourceTripId = ptr(qualify(p, t.ID))
	date := apiDate(day)
	arrival.ServiceDate = &date
	arrival.StopSequence = ptr(v.Sequence)
	arrival.RouteName = optional(scheduledRouteName(d, trip.RouteId))
	arrival.ServiceLabel = optional(cleanCPLabel(t.Label))
	return arrival
}

func (q scheduleQuery) includesDeparture(t ScheduledTrip, trip api.Trip) bool {
	return q.filter.Stop == "" && t.Times[0].Departure >= 0 && t.Times[len(t.Times)-1].Arrival >= 0 && !trip.PlannedDeparture.Before(q.filter.From) && trip.PlannedDeparture.Before(q.filter.To)
}

func (q scheduleQuery) localTrip(trip ScheduledTrip, day time.Time) (ScheduledTrip, bool) {
	if !q.includesTrip(trip, day) {
		return trip, false
	}
	trip.Times = localJourneyTimes(&trip)
	trip.PackedTimes = nil
	return trip, len(trip.Times) > 0
}

// Metadata shared by every trip on the same explicit CM route/plan/agency.
type scheduledTripSource struct {
	Route, Plan, Agency string
}

type tripPopupMetadata struct {
	Source      *scheduledTripSource `json:"source_instance,omitempty"`
	PackedCount int                  `json:"packed_count,omitempty"`
	PackedTimes []byte               `json:"packed_times,omitempty"`
}
