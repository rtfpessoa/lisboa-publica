package app

import (
	"lisboapublica/internal/api"
	"strconv"
	"strings"
	"time"
)

func popupServiceActive(d *StaticData, t *ScheduledTrip, day time.Time) bool {
	key := day.In(lisbon).Format("20060102")
	return (d.ValidFrom == "" || key >= d.ValidFrom) && (d.ValidUntil == "" || key <= d.ValidUntil) && d.Schedule.active(t.Service, day)
}

type plannedPopupJourney struct {
	data     *StaticData
	operator string
	trip     *ScheduledTrip
	day      time.Time
	index    *journeyIndex
	now      time.Time
}

func (p plannedPopupJourney) call(v StopTime) api.StopCall {
	d, op, t, day, idx, now := p.data, p.operator, p.trip, p.day, p.index, p.now
	journey := journeyKey(op, d, t, day)
	at, dep := serviceStart(day).Add(time.Duration(v.Arrival)*time.Second).UTC(), serviceStart(day).Add(time.Duration(v.Departure)*time.Second).UTC()
	var arrival, departure *api.CallTimeEvidence
	if v.Arrival >= 0 {
		arrival = &api.CallTimeEvidence{At: at, SourceUrl: d.Source, CollectedAt: &d.Updated}
	}
	if v.Departure >= 0 {
		departure = &api.CallTimeEvidence{At: dep, SourceUrl: d.Source, CollectedAt: &d.Updated}
	}
	call := api.StopCall{Id: journey + ":" + strconv.Itoa(v.Sequence), JourneyId: &journey, StopId: qualify(op, v.Stop), StopName: d.Schedule.StopNames[v.Stop], StopSequence: v.Sequence, LineKey: idx.lineFor(t), DirectionKey: idx.directionFor(t), Destination: tripDestination(d.Schedule, t), ServiceLabel: optional(t.Label), Arrival: selectCallTime(nil, nil, arrival, false, now), Departure: selectCallTime(nil, nil, departure, false, now), Phase: "unknown"}
	for _, stop := range d.Stops {
		if stop.Id == call.StopId {
			copy := stop
			call.Stop = &copy
			call.StopStaticUpdatedAt = &d.Updated
			call.StopPlanId = optional(d.PlanID)
			break
		}
	}
	return call
}

func journeyKey(op string, d *StaticData, t *ScheduledTrip, day time.Time) string {
	plan := d.PlanID
	if t.Source != nil && t.Source.Plan != "" {
		plan = t.Source.Plan
	}
	return strings.Join([]string{op, plan, t.ID, day.In(lisbon).Format("2006-01-02")}, "|")
}
