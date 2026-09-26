package app

import (
	"lisboapublica/internal/api"
	"time"
)

func (d *LiveData) seedContinuity(previous *LiveData, op api.Operator) map[string]api.Vehicle {
	old := map[string]api.Vehicle{}
	if previous == nil {
		return old
	}
	d.ReplayFloor = previous.ReplayFloor
	d.LastKnownTruncatedUntil = previous.LastKnownTruncatedUntil
	for id, c := range previous.Continuity {
		if c.Observed.Add(lastKnownLifetime).After(d.Collected) {
			d.Continuity[id] = c
		}
	}
	for _, v := range previous.LastKnown {
		old[v.Id] = v
	}
	for _, v := range previous.Vehicles {
		mergeLatest(old, v)
	}
	d.seedMissingLedger(old)
	if !sourceVerified(previous, op, d.Collected) {
		d.markDiscontinuous()
	}
	return old
}

func mergeLatest(rows map[string]api.Vehicle, v api.Vehicle) {
	if prev, ok := rows[v.Id]; !ok || v.ObservedAt.After(prev.ObservedAt) {
		rows[v.Id] = v
	}
}

func (d *LiveData) seedMissingLedger(old map[string]api.Vehicle) {
	for id, v := range old {
		if _, ok := d.Continuity[id]; !ok && v.ObservedAt.Add(lastKnownLifetime).After(d.Collected) {
			d.Continuity[id] = vehicleContinuity{Observed: v.ObservedAt, Discontinuous: true}
		}
	}
}

func (d *LiveData) markDiscontinuous() {
	for id, c := range d.Continuity {
		c.Discontinuous = true
		d.Continuity[id] = c
	}
}

func (d *LiveData) acceptReport(v *api.Vehicle, old map[string]api.Vehicle, distances map[string]*float64) bool {
	c, known := d.Continuity[v.Id]
	prev, hasPrev := old[v.Id]
	if known && v.ObservedAt.Before(c.Observed) {
		return false
	}
	if hasPrev && v.ObservedAt.Before(prev.ObservedAt) {
		return false
	}
	// Repeats retain the complete original report, including collection time.
	if hasPrev && !v.ObservedAt.After(prev.ObservedAt) {
		*v = prev
	}
	if known && !v.ObservedAt.After(c.Observed) {
		if c.Discontinuous || !v.ObservedAt.After(d.ReplayFloor) {
			v.SpeedKmh = nil
		}
	} else {
		d.recordNewReport(v, old, distances)
	}
	return true
}

func (d *LiveData) recordNewReport(v *api.Vehicle, old map[string]api.Vehicle, distances map[string]*float64) {
	v.SpeedKmh = nil
	eligible := v.ObservedAt.After(d.ReplayFloor) && v.ObservedAt.Add(lastKnownLifetime).After(d.Collected)
	if eligible {
		c, known := d.Continuity[v.Id]
		prev, hasPrev := old[v.Id]
		if known && !c.Discontinuous && hasPrev && prev.ObservedAt.After(d.ReplayFloor) {
			distance, speed := sampledDistance(prev, *v)
			distances[v.Id] = distance
			v.SpeedKmh = speed
		}
		d.Samples = append(d.Samples, *v)
	}
	d.Continuity[v.Id] = vehicleContinuity{Observed: v.ObservedAt, Discontinuous: !eligible}
}

func (d *LiveData) removeRejected(rejected map[string]bool) {
	if len(rejected) == 0 {
		return
	}
	kept := d.Vehicles[:0]
	for _, v := range d.Vehicles {
		if !rejected[v.Id] {
			kept = append(kept, v)
		}
	}
	d.Vehicles = kept
}

func (d *LiveData) retainMissing(old map[string]api.Vehicle, present map[string]bool) {
	for id, v := range old {
		if !present[id] {
			d.LastKnown = append(d.LastKnown, v)
			if c, ok := d.Continuity[id]; ok {
				c.Discontinuous = true
				d.Continuity[id] = c
			}
		}
	}
}

func projectMembership(d *LiveData, verified bool, static *StaticData, now time.Time) ([]api.Vehicle, map[string]api.Vehicle) {
	current := []api.Vehicle{}
	previous := map[string]api.Vehicle{}
	for _, v := range d.LastKnown {
		previous[v.Id] = v
	}
	for _, v := range d.Vehicles {
		if verified && now.Sub(v.ObservedAt) <= 180*time.Second {
			delete(previous, v.Id)
			current = append(current, projectVehicle(v, false, static))
		} else {
			mergeLatest(previous, v)
		}
	}
	return current, previous
}

func projectVehicle(v api.Vehicle, known bool, static *StaticData) api.Vehicle {
	v.LastKnown = known
	v.Stale = known
	v.LastKnownExpiresAt = ptr(v.ObservedAt.Add(lastKnownLifetime))
	v.InactiveAt = v.ObservedAt.Add(inactiveAfter)
	if known {
		v.SpeedKmh = nil
	}
	enrichVehicle(&v, static)
	return v
}

func countPositionKinds(rows []api.Vehicle) (int, int) {
	reported, estimated := 0, 0
	for _, v := range rows {
		if v.PositionKind == api.VehiclePositionKindEstimated {
			estimated++
		} else {
			reported++
		}
	}
	return reported, estimated
}
