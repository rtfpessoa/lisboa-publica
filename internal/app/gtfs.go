package app

import (
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
type StopTime struct {
	Stop      string `json:"s"`
	Arrival   int    `json:"a"`
	Departure int    `json:"d"`
	Sequence  int    `json:"q"`
}

// UnmarshalJSON accepts compact cache records and the original field names.
func (s *StopTime) UnmarshalJSON(b []byte) error {
	type compact StopTime
	var wire struct {
		compact
		LegacyStop      string `json:"Stop"`
		LegacyArrival   int    `json:"Arrival"`
		LegacyDeparture int    `json:"Departure"`
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
	Times                               []StopTime
}

// Schedule combines planned trips, service calendars and station relationships.
type Schedule struct {
	Trips      []ScheduledTrip
	Calendars  map[string]Calendar
	Exceptions map[string]map[string]int
	Parents    map[string]string
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
func scheduled(d *StaticData, p string, from, to time.Time, route, stop string) ([]api.Trip, []api.Arrival) {
	out := []api.Trip{}
	arrivals := []api.Arrival{}
	if d == nil || d.Schedule == nil {
		return out, arrivals
	}
	day := time.Date(from.In(lisbon).Year(), from.In(lisbon).Month(), from.In(lisbon).Day(), serviceDayNoonHour, 0, 0, 0, lisbon).AddDate(0, 0, -3)
	for ; !day.After(to.In(lisbon).Add(serviceDayNoonHour * time.Hour)); day = day.AddDate(0, 0, 1) {
		date := day.Format("20060102")
		if date < d.ValidFrom || date > d.ValidUntil {
			continue
		}
		trips, visits := (scheduleQuery{d, p, Filter{From: from, To: to, Route: route, Stop: stop}}).day(day)
		out = append(out, trips...)
		arrivals = append(arrivals, visits...)
	}
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
	return out, arrivals
}

type scheduleQuery struct {
	data     *StaticData
	operator string
	filter   Filter
}

func (q scheduleQuery) day(day time.Time) ([]api.Trip, []api.Arrival) {
	d, p, from, to, route := q.data, q.operator, q.filter.From, q.filter.To, q.filter.Route
	out := []api.Trip{}
	arrivals := []api.Arrival{}
	date := day.Format("20060102")
	base := serviceStart(day)
	for _, t := range d.Schedule.Trips {
		rid := qualify(p, t.Route)
		if route != "" && route != rid {
			continue
		}
		if !d.Schedule.active(t.Service, day) || len(t.Times) == 0 {
			continue
		}
		id := qualify(p, date+":"+t.ID)
		depart := base.Add(time.Duration(t.Times[0].Departure) * time.Second)
		end := base.Add(time.Duration(t.Times[len(t.Times)-1].Arrival) * time.Second)
		if !depart.Before(from) && depart.Before(to) {
			out = append(out, api.Trip{Id: id, OperatorId: p, RouteId: rid, Headsign: t.Headsign, PlannedDeparture: depart, PlannedEnd: end, Kind: api.TripKindScheduled})
		}
		arrivals = append(arrivals, q.stopVisits(t, api.Trip{Id: id, RouteId: rid}, base)...)
	}
	return out, arrivals
}

func (q scheduleQuery) stopVisits(t ScheduledTrip, trip api.Trip, base time.Time) []api.Arrival {
	d, p, from, to, stop, id, rid := q.data, q.operator, q.filter.From, q.filter.To, q.filter.Stop, trip.Id, trip.RouteId
	arrivals := []api.Arrival{}

	if stop != "" {
		for _, v := range t.Times {
			sid := qualify(p, v.Stop)
			if stop != sid && stop != qualify(p, d.Schedule.Parents[v.Stop]) {
				continue
			}
			at := base.Add(time.Duration(v.Arrival) * time.Second)
			if at.Before(from) || !at.Before(to) {
				continue
			}
			arrivals = append(arrivals, api.Arrival{Id: id + ":" + v.Stop + ":" + strconv.Itoa(v.Sequence), OperatorId: p, StopId: sid, RouteId: rid, TripId: id, Headsign: t.Headsign, ScheduledAt: &at, SourceUrl: d.Source, Kind: api.ArrivalKindScheduled})
		}
	}
	return arrivals
}
