package app

import (
	"strings"
	"sync"
)

// Boards only need the selected station. Avoid retaining every stop/trip pair
// from all national schedules in every immutable network revision.
type journeyStopIndex struct {
	mu     sync.Mutex
	byStop map[string][]*ScheduledTrip
	bytes  int
}

func (idx *journeyIndex) stopTrips(data *StaticData, operator, stop string) []*ScheduledTrip {
	idx.stops.mu.Lock()
	defer idx.stops.mu.Unlock()
	if trips, found := idx.stops.byStop[stop]; found {
		return trips
	}
	source := strings.TrimPrefix(stop, operator+":")
	trips := []*ScheduledTrip{}
	for n := range data.Schedule.Trips {
		trip := &data.Schedule.Trips[n]
		if tripVisitsStop(data.Schedule, trip, source) {
			trips = append(trips, trip)
		}
	}
	idx.stops.retain(stop, trips)
	return trips
}

func tripVisitsStop(schedule *Schedule, trip *ScheduledTrip, stop string) bool {
	for _, visit := range localJourneyTimes(trip) {
		if visit.Stop == stop || schedule.Parents[visit.Stop] == stop {
			return true
		}
	}
	return false
}

func (index *journeyStopIndex) retain(stop string, trips []*ScheduledTrip) {
	const budget = 4 << 20
	size := len(trips)*8 + len(stop)
	if size > budget {
		return
	}
	if len(index.byStop) >= 32 || index.bytes+size > budget {
		index.byStop = nil
		index.bytes = 0
	}
	if index.byStop == nil {
		index.byStop = map[string][]*ScheduledTrip{}
	}
	index.byStop[stop] = trips
	index.bytes += size
}
