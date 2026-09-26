package app

import (
	"sort"
	"time"

	"lisboapublica/internal/api"
)

const (
	lastKnownLifetime = time.Hour
	inactiveAfter     = 5 * time.Minute
	maxLastKnown      = 500
	maxContinuityIDs  = 2000
)

type vehicleContinuity struct {
	Observed      time.Time `json:"observed"`
	Discontinuous bool      `json:"discontinuous"`
}

// sourceVerified is separate from observation age: a valid empty snapshot is known coverage.
func sourceVerified(d *LiveData, op api.Operator, now time.Time) bool {
	return d != nil && !d.Unverified && (op.Status == api.OperatorStatusOk || op.Status == api.OperatorStatusStale) && op.Error == nil && now.Sub(d.Collected) <= sourceFreshness
}

func newestVehicles(rows []api.Vehicle) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ObservedAt.Equal(rows[j].ObservedAt) {
			return rows[i].Id < rows[j].Id
		}
		return rows[i].ObservedAt.After(rows[j].ObservedAt)
	})
}

func boundLastKnown(rows []api.Vehicle, until time.Time, now time.Time) ([]api.Vehicle, time.Time) {
	out := make([]api.Vehicle, 0, len(rows))
	for _, v := range rows {
		if v.ObservedAt.Add(lastKnownLifetime).After(now) {
			out = append(out, v)
		}
	}
	newestVehicles(out)
	if len(out) > maxLastKnown {
		for _, v := range out[maxLastKnown:] {
			if expiry := v.ObservedAt.Add(lastKnownLifetime); expiry.After(until) {
				until = expiry
			}
		}
		// Copy the bounded prefix: reslicing alone retains evicted rows and their backing allocation.
		out = append([]api.Vehicle(nil), out[:maxLastKnown]...)
	}
	sortVehicles(out)
	return out, until
}

// nextLive keeps source membership, display retention and actual new history observations separate.
// Inputs and prior states are immutable. The returned Samples never enter the durable cache.
func nextLive(previous *LiveData, op api.Operator, rows []api.Vehicle, now time.Time) (*LiveData, map[string]*float64) {
	d := &LiveData{Vehicles: append([]api.Vehicle{}, rows...), Collected: now, Samples: []api.Vehicle{}, Continuity: map[string]vehicleContinuity{}}
	old := d.seedContinuity(previous, op)
	distances := map[string]*float64{}
	present, rejected := map[string]bool{}, map[string]bool{}
	for i := range d.Vehicles {
		v := &d.Vehicles[i]
		present[v.Id] = true
		if !d.acceptReport(v, old, distances) {
			rejected[v.Id] = true
			delete(present, v.Id)
		}
	}
	d.removeRejected(rejected)
	d.retainMissing(old, present)
	d.LastKnown, d.LastKnownTruncatedUntil = boundLastKnown(d.LastKnown, d.LastKnownTruncatedUntil, now)
	boundContinuity(d, now)
	sortVehicles(d.Vehicles)
	return d, distances
}

func boundContinuity(d *LiveData, now time.Time) {
	type entry struct {
		id string
		c  vehicleContinuity
	}
	entries := make([]entry, 0, len(d.Continuity))
	for id, c := range d.Continuity {
		if !c.Observed.Add(lastKnownLifetime).After(now) {
			delete(d.Continuity, id)
		} else {
			entries = append(entries, entry{id, c})
		}
	}
	if len(entries) <= maxContinuityIDs {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].c.Observed.Equal(entries[j].c.Observed) {
			return entries[i].id < entries[j].id
		}
		return entries[i].c.Observed.After(entries[j].c.Observed)
	})
	for _, e := range entries[maxContinuityIDs:] {
		if e.c.Observed.After(d.ReplayFloor) {
			d.ReplayFloor = e.c.Observed
		}
		delete(d.Continuity, e.id)
	}
}

func restoreLive(d *LiveData, now time.Time) *LiveData {
	result, _ := nextLive(d, api.Operator{Status: api.OperatorStatusLoading}, d.Vehicles, now)
	result.Collected = d.Collected
	result.Unverified = true
	result.Samples = []api.Vehicle{}
	floor := now.Add(providerClockSkew)
	if floor.After(result.ReplayFloor) {
		result.ReplayFloor = floor
	}
	for id, c := range result.Continuity {
		c.Discontinuous = true
		result.Continuity[id] = c
	}
	return result
}

// projectLive applies one clock and cap to omitted, stale and unverified source positions.
func projectLive(d *LiveData, op api.Operator, static *StaticData, now time.Time) ([]api.Vehicle, *int, *int, int, bool) {
	if d == nil {
		return []api.Vehicle{}, nil, nil, 0, false
	}
	verified := sourceVerified(d, op, now)
	current, previous := projectMembership(d, verified, static, now)
	reported, estimated := countPositionKinds(current)
	candidates := make([]api.Vehicle, 0, len(previous))
	for _, v := range previous {
		candidates = append(candidates, v)
	}
	candidates, until := boundLastKnown(candidates, d.LastKnownTruncatedUntil, now)
	for _, v := range candidates {
		current = append(current, projectVehicle(v, true, static))
	}
	sortVehicles(current)
	if !verified {
		return current, nil, nil, len(candidates), until.After(now)
	}
	return current, ptr(reported), ptr(estimated), len(candidates), until.After(now)
}

// historyVehicles is backwards compatible with direct internal fixtures/callers.
// Fetcher publications always set Samples, including an empty slice for repeats/restores.
func (d *LiveData) historyVehicles() []api.Vehicle {
	if d.Samples != nil {
		return d.Samples
	}
	return d.Vehicles
}
