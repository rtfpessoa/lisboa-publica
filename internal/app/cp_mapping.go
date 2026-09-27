package app

import (
	"context"
	"strings"
)

type cpIndex struct {
	Operator, Agency string
	Context          context.Context
	Data             *StaticData
	Trips            map[string]*ScheduledTrip
	Names            map[string]string
	Crosswalk        map[string]string
	BadHub           map[string]bool
	BadStop          map[string]bool
}

func indexCP(ctx context.Context, data *StaticData, updates []cpUpdate) *cpIndex {
	operator, agency := "cp", "N18KL"
	if data.Operator != "" {
		operator = data.Operator
		if p, ok := providerByID(operator); ok {
			agency = p.Agency
		}
	}
	index := &cpIndex{Operator: operator, Agency: agency, Context: ctx, Data: data, Trips: map[string]*ScheduledTrip{}, Names: map[string]string{}, Crosswalk: map[string]string{}, BadHub: map[string]bool{}, BadStop: map[string]bool{}}
	for n := range data.Schedule.Trips {
		if ctx.Err() != nil {
			break
		}
		t := &data.Schedule.Trips[n]
		if _, exists := index.Trips[t.ID]; exists {
			index.Trips[t.ID] = nil
		} else {
			index.Trips[t.ID] = t
		}
	}
	for id, name := range data.Schedule.StopNames {
		index.Names[id] = name
	}
	for _, s := range data.Stops {
		if ctx.Err() != nil {
			break
		}
		index.Names[s.SourceId] = s.Name
	}
	buildCPCrosswalk(index, updates)
	return index
}

func (i *cpIndex) addMapping(hub, stop string, reverse map[string]string) {
	if previous := i.Crosswalk[hub]; previous != "" && previous != stop {
		i.BadHub[hub], i.BadStop[previous], i.BadStop[stop] = true, true, true
	}
	if previous := reverse[stop]; previous != "" && previous != hub {
		i.BadStop[stop], i.BadHub[previous], i.BadHub[hub] = true, true, true
	}
	i.Crosswalk[hub], reverse[stop] = stop, hub
}

func cpTrip(i *cpIndex, u cpUpdate) *ScheduledTrip {
	var t *ScheduledTrip
	if i.Operator == "cm" {
		t = cmPredictionTrip(i, u)
	} else {
		t = ordinaryPredictionTrip(i, u)
	}
	return t
}
func ordinaryPredictionTrip(i *cpIndex, u cpUpdate) *ScheduledTrip {
	prefix := "[" + i.Data.PlanID + "][" + i.Agency + "]"
	var t *ScheduledTrip
	if strings.HasPrefix(u.Trip.ID, prefix) {
		t = i.Trips[strings.TrimPrefix(u.Trip.ID, prefix)]
	}
	if t != nil && !predictionRouteMatches(u, t, i.Agency) {
		t = nil
	}
	return t
}
func cmPredictionTrip(i *cpIndex, u cpUpdate) *ScheduledTrip {
	t := i.Trips[u.Trip.ID]
	if t != nil && !predictionRouteMatches(u, t, t.Agency) {
		t = nil
	}
	return t
}
func predictionRouteMatches(update cpUpdate, t *ScheduledTrip, agency string) bool {
	published, route := update.Trip.Route, t.Route
	if t.SourceRoute != "" {
		route = t.SourceRoute
	}
	return published == "" || published == route || published == "["+agency+"]"+route
}

func cpScheduled(relationship string) bool { return relationship == "" || relationship == "SCHEDULED" }

func uniqueCPSequence(t *ScheduledTrip, seq int) *StopTime {
	var found *StopTime
	for n := range journeyTimes(t) {
		if journeyTimes(t)[n].Sequence != seq {
			continue
		}
		if found != nil {
			return nil
		}
		found = &journeyTimes(t)[n]
	}
	return found
}

func (i *cpIndex) visit(t *ScheduledTrip, s cpStopUpdate) *StopTime {
	if i.BadHub[s.ID] {
		return nil
	}
	var v *StopTime
	if s.Sequence != nil {
		v = uniqueCPSequence(t, *s.Sequence)
	} else {
		v = uniqueCPStop(t, i.Crosswalk[s.ID])
	}
	if !i.validVisit(s, v) {
		return nil
	}
	return v
}

func uniqueCPStop(t *ScheduledTrip, stop string) *StopTime {
	if stop == "" {
		return nil
	}
	var v *StopTime
	for n := range journeyTimes(t) {
		if journeyTimes(t)[n].Stop != stop {
			continue
		}
		if v != nil {
			return nil
		}
		v = &journeyTimes(t)[n]
	}
	return v
}

func (i *cpIndex) validVisit(s cpStopUpdate, v *StopTime) bool {
	if v == nil || i.BadStop[v.Stop] {
		return false
	}
	mapped := i.Crosswalk[s.ID]
	return mapped == "" || mapped == v.Stop
}

func buildCPCrosswalk(index *cpIndex, updates []cpUpdate) {
	reverse := map[string]string{}
	for _, u := range updates {
		if index.Context.Err() != nil {
			return
		}
		t := cpTrip(index, u)
		if t == nil || !cpScheduled(u.Trip.Relationship) {
			continue
		}
		for _, s := range u.Stops {
			if index.Context.Err() != nil {
				return
			}
			if s.Sequence == nil || s.ID == "" || !cpScheduled(s.Relationship) {
				continue
			}
			v := uniqueCPSequence(t, *s.Sequence)
			if v == nil {
				continue
			}
			index.addMapping(s.ID, v.Stop, reverse)
		}
	}
}
