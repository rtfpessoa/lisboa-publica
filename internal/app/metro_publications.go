package app

import (
	"encoding/json"
	"lisboapublica/internal/api"
	"strings"
	"time"
)

type metroPublication struct {
	clock           time.Time
	seconds         int
	valid, conflict bool
}

// Newest original evidence masks older ETAs before window filtering.
func latestMetroPublications(waits []MetroWait, stationID string, asOf time.Time) map[string]metroPublication {
	latest := map[string]metroPublication{}
	for _, wait := range waits {
		if wait.Stop != stationID {
			continue
		}
		clock, err := time.ParseInLocation("20060102150405", wait.At, lisbon)
		if err != nil || asOf.Sub(clock) > sourceFreshness || clock.After(asOf.Add(providerClockSkew)) {
			continue
		}
		mergeMetroPublication(latest, wait, clock)
	}
	return latest
}
func mergeMetroPublication(latest map[string]metroPublication, wait MetroWait, clock time.Time) {
	for i, ref := range []string{wait.Train, wait.Train2, wait.Train3} {
		ref = strings.TrimSpace(ref)
		if !metroReference(ref) {
			continue
		}
		seconds, valid := metroWaitSeconds([]json.RawMessage{wait.Wait1, wait.Wait2, wait.Wait3}[i])
		id := metroForecastID(wait, ref)
		previous, exists := latest[id]
		if !exists || clock.After(previous.clock) {
			latest[id] = metroPublication{clock: clock, seconds: seconds, valid: valid}
		} else if metroPublicationConflict(previous, clock, seconds, valid) {
			previous.conflict = true
			latest[id] = previous
		}
	}
}
func metroPublicationConflict(previous metroPublication, clock time.Time, seconds int, valid bool) bool {
	return clock.Equal(previous.clock) && (valid != previous.valid || valid && seconds != previous.seconds)
}

type metroArrivalSelection struct {
	data                   *MetroData
	stations               map[string]MetroStation
	routeIDs               map[string]string
	asOf, from, to         time.Time
	route, stop, stationID string
	latest                 map[string]metroPublication
}

func currentMetroArrivals(q metroArrivalSelection) map[string]api.Arrival {
	seen := map[string]api.Arrival{}
	for _, wait := range q.data.Waits {
		if wait.Stop != q.stationID {
			continue
		}
		for _, arrival := range platformArrivals(wait, q.stations, q.routeIDs, q.asOf, q.from, q.to, q.route, q.stop) {
			publication := q.latest[arrival.Id]
			if publication.valid && !publication.conflict && publication.clock.Equal(*arrival.ObservedAt) {
				seen[arrival.Id] = arrival
			}
		}
	}
	return seen
}
