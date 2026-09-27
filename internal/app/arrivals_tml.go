package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

type tmlArrivalCandidate struct {
	row        api.Arrival
	sourceStop string
}
type tmlArrivalIndex struct {
	provider   provider
	index      *cpIndex
	reverse    map[string]string
	candidates []tmlArrivalCandidate
	partial    bool
	stale      map[string]time.Time
	cancelled  map[string]bool
}
type tmlArrivalBatch struct {
	ctx                  context.Context
	state                *State
	now                  time.Time
	wanted               map[string]*StaticData
	indices              map[string]*tmlArrivalIndex
	mappings, candidates int
	overflow             bool
}

func newTMLArrivalBatch(ctx context.Context, state *State, wanted map[string]*StaticData, now time.Time) *tmlArrivalBatch {
	b := &tmlArrivalBatch{ctx: ctx, state: state, wanted: wanted, now: now, indices: map[string]*tmlArrivalIndex{}}
	for stop, d := range wanted {
		id, _, _ := strings.Cut(stop, ":")
		if state.Static[id] != d {
			delete(wanted, stop)
			continue
		}
		p, _ := providerByID(id)
		if b.indices[id] == nil {
			b.indices[id] = &tmlArrivalIndex{provider: p, index: &cpIndex{Context: ctx, Data: d, Crosswalk: map[string]string{}, BadHub: map[string]bool{}, BadStop: map[string]bool{}}, reverse: map[string]string{}, stale: map[string]time.Time{}, cancelled: map[string]bool{}}
		}
	}
	return b
}
func tmlArrivalTrip(i *tmlArrivalIndex, u cpUpdate) *ScheduledTrip {
	d := i.index.Data
	if !usableArrivalSchedule(d) {
		return nil
	}
	prefix := "[" + d.PlanID + "][" + i.provider.Agency + "]"
	var trip *ScheduledTrip
	if strings.HasPrefix(u.Trip.ID, prefix) {
		trip = exactArrivalTrip(d.Schedule, strings.TrimPrefix(u.Trip.ID, prefix))
		if trip != nil && !arrivalTripRouteMatches(i, trip, u) {
			trip = nil
		}
	}
	return trip
}

func usableArrivalSchedule(d *StaticData) bool {
	return d != nil && d.Schedule != nil && d.PlanID != ""
}

func exactArrivalTrip(schedule *Schedule, id string) *ScheduledTrip {
	n := sort.Search(len(schedule.Trips), func(n int) bool { return schedule.Trips[n].ID >= id })
	if n < len(schedule.Trips) && schedule.Trips[n].ID == id {
		return &schedule.Trips[n]
	}
	return nil
}

func arrivalTripRouteMatches(i *tmlArrivalIndex, t *ScheduledTrip, u cpUpdate) bool {
	return len(t.Route) <= cpMaxIdentifierBytes && (u.Trip.Route == "" || u.Trip.Route == t.Route || u.Trip.Route == "["+i.provider.Agency+"]"+t.Route)
}

func (b *tmlArrivalBatch) visit(e cpEntity) {
	if b.ctx.Err() != nil || b.overflow {
		return
	}
	for _, i := range b.indices {
		b.visitIndex(i, e)
	}
}

func (b *tmlArrivalBatch) visitIndex(i *tmlArrivalIndex, e cpEntity) {
	t := tmlArrivalTrip(i, e.Update)
	if t == nil {
		if strings.Contains(e.Update.Trip.ID, "["+i.provider.Agency+"]") {
			i.partial = true
		}
	} else if !usableArrivalUpdate(e.Update) {
		i.partial = true
	} else if e.Deleted || !cpScheduled(e.Update.Trip.Relationship) {
		b.cancelTrip(i, e.Update.Trip.ID)
	} else {
		j := tmlArrivalJourney{batch: b, index: i, trip: t, update: e.Update, observed: time.Unix(e.Update.Timestamp, 0).UTC()}
		j.collect()
	}
}

func usableArrivalUpdate(u cpUpdate) bool {
	return len(u.Vehicle.ID) <= cpMaxIdentifierBytes && boundedCPUpdate(u) && len(u.Stops) <= cpMaxInputRows
}

func (b *tmlArrivalBatch) cancelTrip(i *tmlArrivalIndex, id string) {
	if len(i.cancelled) >= arrivalCandidateLimit {
		b.overflow = true
	} else {
		i.cancelled[id] = true
		i.partial = true
	}
}

func validArrivalDelay(n int) bool { return n >= -cpMaxDeviation && n <= cpMaxDeviation }

func tmlArrivalRouteName(d *StaticData, id string) string {
	for _, r := range d.Routes {
		if r.Id == id {
			if len(r.ShortName)+len(r.LongName) > cpMaxNameBytes {
				return cleanCPName(r.ShortName)
			}
			return cleanCPName(r.ShortName + " · " + r.LongName)
		}
	}
	return ""
}

func tmlAbsoluteCompatible(t *ScheduledTrip, u cpUpdate, day time.Time) bool {
	for _, s := range u.Stops {
		if v := arrivalAbsoluteVisit(t, s); v != nil && !arrivalAbsoluteWithin(s, v, day) {
			return false
		}
	}
	return true
}

func (i *tmlArrivalIndex) demanded(wanted map[string]*StaticData, raw string) bool {
	if wanted[qualify(i.provider.ID, raw)] == i.index.Data {
		return true
	}
	parent := i.index.Data.Schedule.Parents[raw]
	return parent != "" && wanted[qualify(i.provider.ID, parent)] == i.index.Data
}
