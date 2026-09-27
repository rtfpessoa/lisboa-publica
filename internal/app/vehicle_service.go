package app

import (
	"strconv"
	"time"

	"lisboapublica/internal/api"
)

func publishedStopStatus(raw *string) *api.VehicleCurrentStatus {
	if raw == nil {
		return nil
	}
	status := api.VehicleCurrentStatus(*raw)
	if !status.Valid() {
		return nil
	}
	return &status
}

func boundedStopReference(raw *string) *string {
	if raw == nil || len(*raw) == 0 || len(*raw) > 128 {
		return nil
	}
	return raw
}

func publishedServiceDate(raw int) *string {
	date, err := time.Parse("20060102", strconv.Itoa(raw))
	if err != nil {
		return nil
	}
	return ptr(date.Format("2006-01-02"))
}

func (f *Fetcher) currentStatic(operator string) *StaticData {
	state, _ := f.Cache.state("")
	return state.Static[operator]
}

// Resolve only while capturing an observation, never from a later mutable plan.
func enrichStopReference(v *api.Vehicle, data *StaticData, verifiedPlan bool) {
	if data == nil || !verifiedPlan || v.SourceStopId == nil {
		return
	}
	raw := *v.SourceStopId
	if p, ok := providerByID(v.OperatorId); ok && p.Agency != "" {
		raw = verifiedHubID(raw, p.Agency)
	}
	for _, stop := range data.Stops {
		if stop.SourceId == raw && stop.OperatorId == v.OperatorId {
			v.StopId, v.StopName = ptr(stop.Id), ptr(stop.Name)
			break
		}
	}
}

func enrichScheduledService(v *api.Vehicle, data *StaticData) {
	if !eligibleCPSchedule(v, data) {
		return
	}
	day, valid := scheduledServiceDay(*v.OperationalDate, data)
	if !valid {
		return
	}
	for _, trip := range data.Schedule.Trips {
		if *v.TripId == qualify(v.OperatorId, trip.ID) && *v.RouteId == qualify(v.OperatorId, trip.Route) && data.Schedule.active(trip.Service, day) {
			v.ScheduledService = trip.scheduledEndpoints(data.Source, day)
			v.ServiceLabel = optional(cleanCPLabel(trip.Label))
			break
		}
	}
}

func eligibleCPSchedule(v *api.Vehicle, data *StaticData) bool {
	if v.OperatorId != "cp" || data == nil || data.Schedule == nil {
		return false
	}
	return v.PlanId != nil && *v.PlanId == data.PlanID && v.TripId != nil && v.OperationalDate != nil && v.RouteId != nil
}

func scheduledServiceDay(value string, data *StaticData) (time.Time, bool) {
	day, err := time.ParseInLocation("2006-01-02", value, lisbon)
	key := day.Format("20060102")
	return day, err == nil && key >= data.ValidFrom && key <= data.ValidUntil
}

func (f *Fetcher) captureHubService(v *api.Vehicle, raw hubPosition, trip string) {
	v.CurrentStatus = publishedStopStatus(raw.Status)
	v.SourceStopId = boundedStopReference(raw.Stop)
	v.OperationalDate = publishedServiceDate(raw.OperationalDate)
	data := f.currentStatic(v.OperatorId)
	enrichStopReference(v, data, v.PlanId != nil && data != nil && *v.PlanId == data.PlanID && trip != raw.Trip)
	if v.OperatorId == "metro" {
		state, _ := f.Cache.state("")
		resolveMetroVehicleStop(v, state)
	}
	enrichScheduledService(v, data)
}
