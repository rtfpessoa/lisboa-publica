package app

import (
	"maps"
	"time"

	"lisboapublica/internal/api"
)

const maxPendingHistory = 20_000

type historicalRecord struct {
	Vehicle      api.Vehicle
	First        time.Time
	Distance     *float64
	SpeedSamples int
}

type historyBucket struct {
	Vehicle                       api.Vehicle
	First, Start                  time.Time
	SpeedSum, DistanceSum         float64
	SpeedSamples, DistanceSamples int
}

type historyKey struct {
	Vehicle, Route, Trip, Kind string
	Start                      int64
}

type historyCollector struct {
	Pending  map[historyKey]historyBucket
	Observed map[string]time.Time
}

func newHistoryCollector() *historyCollector {
	return &historyCollector{Pending: map[historyKey]historyBucket{}, Observed: map[string]time.Time{}}
}

// propose stages a bounded update; Save publishes it only after the transaction commits.
func (c *historyCollector) propose(operator string, live *LiveData, distances map[string]*float64, interval time.Duration) (*historyCollector, []historicalRecord) {
	next := &historyCollector{Pending: maps.Clone(c.Pending), Observed: maps.Clone(c.Observed)}
	for _, vehicle := range live.historyVehicles() {
		next.observe(vehicle, distances[vehicle.Id], live.Collected, interval)
	}
	records := next.close(operator, live.Collected, interval)
	next.forget(live.Collected, interval)
	return next, records
}

func (c *historyCollector) observe(vehicle api.Vehicle, distance *float64, collected time.Time, interval time.Duration) {
	start := vehicle.ObservedAt.Truncate(interval)
	if !start.Add(interval + sourceFreshness).After(collected) {
		return
	}
	if !vehicle.ObservedAt.After(c.Observed[vehicle.Id]) {
		return
	}
	if _, known := c.Observed[vehicle.Id]; !known && len(c.Observed) >= maxPendingHistory {
		return
	}
	c.Observed[vehicle.Id] = vehicle.ObservedAt
	key := historyKey{Vehicle: vehicle.Id, Start: start.UnixNano(), Route: stringValue(vehicle.RouteId), Trip: stringValue(vehicle.TripId), Kind: string(vehicle.PositionKind)}
	bucket, exists := c.Pending[key]
	if !exists && len(c.Pending) >= maxPendingHistory {
		return
	}
	if !exists {
		bucket.First, bucket.Start = vehicle.ObservedAt, start
	}
	bucket.Vehicle = vehicle
	bucket.accept(distance)
	c.Pending[key] = bucket
}

func (b *historyBucket) accept(distance *float64) {
	if b.Vehicle.SpeedKmh != nil && b.Vehicle.PositionKind == api.VehiclePositionKindReported {
		b.SpeedSum += *b.Vehicle.SpeedKmh
		b.SpeedSamples++
	}
	if distance != nil && b.Vehicle.PositionKind == api.VehiclePositionKindReported {
		b.DistanceSum += *distance
		b.DistanceSamples++
	}
}

func (c *historyCollector) close(operator string, collected time.Time, interval time.Duration) []historicalRecord {
	records := []historicalRecord{}
	for key, bucket := range c.Pending {
		if bucket.Vehicle.OperatorId != operator || bucket.Start.Add(interval+sourceFreshness).After(collected) {
			continue
		}
		record := historicalRecord{Vehicle: bucket.Vehicle, First: bucket.First, SpeedSamples: bucket.SpeedSamples}
		record.Vehicle.SpeedKmh = nil
		if bucket.SpeedSamples > 0 {
			record.Vehicle.SpeedKmh = ptr(bucket.SpeedSum / float64(bucket.SpeedSamples))
		}
		if bucket.DistanceSamples > 0 {
			record.Distance = ptr(bucket.DistanceSum)
		}
		records = append(records, record)
		delete(c.Pending, key)
	}
	return records
}

func (c *historyCollector) forget(collected time.Time, interval time.Duration) {
	cutoff := collected.Add(-2*interval - sourceFreshness)
	for id, observed := range c.Observed {
		if observed.Before(cutoff) {
			delete(c.Observed, id)
		}
	}
}

func rawHistory(live *LiveData, distances map[string]*float64) []historicalRecord {
	records := make([]historicalRecord, 0, len(live.Vehicles))
	for _, vehicle := range live.historyVehicles() {
		count := 0
		if vehicle.SpeedKmh != nil {
			count = 1
		}
		records = append(records, historicalRecord{Vehicle: vehicle, First: vehicle.ObservedAt, Distance: distances[vehicle.Id], SpeedSamples: count})
	}
	return records
}
