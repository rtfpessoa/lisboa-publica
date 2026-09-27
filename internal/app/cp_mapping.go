package app

import (
	"context"
	"strings"
)

type cpIndex struct {
	Context   context.Context
	Data      *StaticData
	Trips     map[string]*ScheduledTrip
	Names     map[string]string
	Crosswalk map[string]string
	BadHub    map[string]bool
	BadStop   map[string]bool
}

func indexCP(ctx context.Context, data *StaticData, updates []cpUpdate) *cpIndex {
	index := &cpIndex{Context: ctx, Data: data, Trips: map[string]*ScheduledTrip{}, Names: map[string]string{}, Crosswalk: map[string]string{}, BadHub: map[string]bool{}, BadStop: map[string]bool{}}
	for n := range data.Schedule.Trips {
		if ctx.Err() != nil {
			break
		}
		t := &data.Schedule.Trips[n]
		index.Trips[t.ID] = t
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
	prefix := "[" + i.Data.PlanID + "][N18KL]"
	if !strings.HasPrefix(u.Trip.ID, prefix) {
		return nil
	}
	t := i.Trips[strings.TrimPrefix(u.Trip.ID, prefix)]
	if t == nil {
		return nil
	}
	if u.Trip.Route != "" && u.Trip.Route != t.Route && u.Trip.Route != "[N18KL]"+t.Route {
		return nil
	}
	return t
}

func cpScheduled(relationship string) bool { return relationship == "" || relationship == "SCHEDULED" }

func uniqueCPSequence(t *ScheduledTrip, seq int) *StopTime {
	var found *StopTime
	for n := range t.Times {
		if t.Times[n].Sequence != seq {
			continue
		}
		if found != nil {
			return nil
		}
		found = &t.Times[n]
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
	for n := range t.Times {
		if t.Times[n].Stop != stop {
			continue
		}
		if v != nil {
			return nil
		}
		v = &t.Times[n]
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
