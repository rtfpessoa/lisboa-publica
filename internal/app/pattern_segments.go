package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
)

// Segment reuse requires identical published geometry and endpoint coordinates.
// Unknown or ambiguous geometry leaves whole-profile isolation in place.
func plannedSegmentEvidence(static *StaticData, trip ScheduledTrip, times []StopTime) []string {
	keys := make([]string, len(times))
	geometry := uniqueMetroTripGeometry(static, trip)
	if len(geometry) < 2 {
		return keys
	}
	stations := plannedStationCoordinates(static)
	indices := segmentVisitIndices(times, stations, geometry)
	if indices == nil {
		return keys
	}
	for i := 0; i+1 < len(times); i++ {
		if times[i+1].Sequence != times[i].Sequence+1 {
			continue
		}
		points := geometry[indices[i] : indices[i+1]+1]
		keys[i] = publishedSegmentKey(points, stations[times[i].Stop], stations[times[i+1].Stop])
	}
	return keys
}

func uniqueMetroTripGeometry(static *StaticData, trip ScheduledTrip) [][]float64 {
	var geometry [][]float64
	for _, shape := range static.Shapes {
		if shape.ShapeId != trip.Shape || shape.RouteId != qualify("metro", trip.Route) {
			continue
		}
		if geometry != nil && !samePublishedGeometry(geometry, shape.Geometry) {
			return nil
		}
		geometry = shape.Geometry
	}
	return geometry
}

func samePublishedGeometry(a, b [][]float64) bool {
	first, _ := json.Marshal(a)
	second, _ := json.Marshal(b)
	return string(first) == string(second)
}

func plannedStationCoordinates(static *StaticData) map[string][2]float64 {
	stations := map[string][2]float64{}
	for _, stop := range static.Stops {
		stations[stop.SourceId] = [2]float64{stop.Lon, stop.Lat}
	}
	return stations
}

func segmentVisitIndices(times []StopTime, stations map[string][2]float64, geometry [][]float64) []int {
	indices := make([]int, len(times))
	for i, visit := range times {
		point, exists := stations[visit.Stop]
		if !exists {
			return nil
		}
		index := uniqueNearbyGeometryIndex(geometry, point)
		if !forwardGeometryIndex(indices, i, index) {
			return nil
		}
		indices[i] = index
	}
	return indices
}

func forwardGeometryIndex(indices []int, i, index int) bool {
	if index < 0 {
		return false
	}
	return i == 0 || index > indices[i-1]
}

func uniqueNearbyGeometryIndex(geometry [][]float64, point [2]float64) int {
	best, second := math.Inf(1), math.Inf(1)
	index := -1
	for j, p := range geometry {
		if !validSegmentPoint(p) {
			return -1
		}
		distance := segmentPointDistance(p, point)
		if distance < best {
			second, best, index = best, distance, j
		} else if distance < second {
			second = distance
		}
	}
	if best > 100 || second-best < .01 {
		return -1
	}
	return index
}

func validSegmentPoint(point []float64) bool {
	if len(point) != 2 {
		return false
	}
	return !math.IsNaN(point[0]) && !math.IsNaN(point[1])
}

func segmentPointDistance(point []float64, station [2]float64) float64 {
	return math.Hypot((point[0]-station[0])*math.Cos(station[1]*math.Pi/180), point[1]-station[1]) * 111320
}

func publishedSegmentKey(points [][]float64, from, to [2]float64) string {
	raw, _ := json.Marshal(struct {
		Points   [][]float64
		From, To [2]float64
	}{points, from, to})
	return fmt.Sprintf("metro-published-segment-v1-%x", sha256.Sum256(raw))
}
