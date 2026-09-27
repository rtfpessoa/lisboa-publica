package app

import (
	"fmt"
	"lisboapublica/internal/api"
	"strings"
	"time"
)

// Reuse the four archives already collected for geometry; no visitor-driven fetches.
type cmJourneyImport struct {
	network  *StaticData
	plan     *hubPlan
	provider provider
	source   string
}

func (i cmJourneyImport) merge(blob []byte) error {
	d, err := readGTFS(blob, i.provider, i.plan.ID, fmt.Sprint(i.plan.From), fmt.Sprint(i.plan.Until), i.source, time.Now().UTC())
	if err != nil {
		return err
	}
	lines, err := cmJourneyLines(blob)
	if err == nil {
		err = i.mergeTrips(d, lines)
	}
	if err == nil {
		i.mergeCalendars(d)
		err = i.mergeStops(d)
	}
	return err
}
func cmJourneyLines(blob []byte) (map[string]string, error) {
	archive, err := openGTFS(blob)
	lines := map[string]string{}
	if err == nil {
		err = archive.read("routes.txt", func(row map[string]string) error { lines[row["route_id"]] = row["line_id"]; return nil })
	}
	return lines, err
}
func (i cmJourneyImport) namespace() string { return "[" + i.plan.ID + "][" + i.plan.Agency + "]" }
func (i cmJourneyImport) mergeTrips(d *StaticData, lines map[string]string) error {
	i.mergeStopLines(d, lines)
	sources := map[string]*scheduledTripSource{}
	for _, t := range d.Schedule.Trips {
		line := lines[t.Route]
		if line == "" {
			return fmt.Errorf("CM line mapping unavailable")
		}
		if sources[t.Route] == nil {
			sources[t.Route] = &scheduledTripSource{Route: t.Route, Plan: i.plan.ID, Agency: i.plan.Agency}
		}
		t.Source = sources[t.Route]
		t.ID = i.namespace() + t.ID
		t.Service = i.namespace() + t.Service
		t.Route = line
		i.network.Schedule.Trips = append(i.network.Schedule.Trips, t)
	}
	return nil
}
func (i cmJourneyImport) mergeCalendars(d *StaticData) {
	for id, c := range d.Schedule.Calendars {
		i.network.Schedule.Calendars[i.namespace()+id] = c
	}
	for id, e := range d.Schedule.Exceptions {
		i.network.Schedule.Exceptions[i.namespace()+id] = e
	}
	i.network.Schedule.HasFrequencies = i.network.Schedule.HasFrequencies || d.Schedule.HasFrequencies
}
func (i cmJourneyImport) mergeStops(d *StaticData) error {
	for id, n := range d.Schedule.StopNames {
		if old := i.network.Schedule.StopNames[id]; old != "" && old != n {
			return fmt.Errorf("CM stop identity conflict")
		}
		i.network.Schedule.StopNames[id] = n
	}
	for id, parent := range d.Schedule.Parents {
		i.network.Schedule.Parents[id] = parent
	}
	return nil
}

// CM mirrors Hub positions but omits the service date. Join only the same
// qualified vehicle, trip and source instant already present in the shared feed.
func enrichCMOperationalDates(vehicles []api.Vehicle, positions []hubPosition) {
	for n := range vehicles {
		v := &vehicles[n]
		if v.TripId == nil {
			continue
		}
		var matches []hubPosition
		for _, raw := range positions {
			if raw.ID == v.SourceId && qualify("cm", raw.Trip) == *v.TripId && time.Unix(raw.At, 0).Equal(v.ObservedAt) {
				matches = append(matches, raw)
			}
		}
		if len(matches) == 1 {
			raw := matches[0]
			v.OperationalDate = publishedServiceDate(raw.OperationalDate)
			prefix := "["
			if strings.HasPrefix(raw.Trip, prefix) {
				if end := strings.Index(raw.Trip, "]"); end > 1 {
					v.PlanId = optional(raw.Trip[1:end])
				}
			}
		}
	}
}

func vehiclePlanMatches(v *api.Vehicle, d *StaticData) bool {
	if v.PlanId == nil {
		return true
	}
	if *v.PlanId == d.PlanID {
		return true
	}
	if v.OperatorId != "cm" || v.TripId == nil || d.Schedule == nil {
		return false
	}
	trips := popupTripMatches(d, "cm", *v.TripId)
	return len(trips) == 1 && predictionTripPlan(d, trips[0]) == *v.PlanId
}
