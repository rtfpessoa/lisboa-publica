package patterns

import (
	"math"
	"strings"
	"time"
)

func validProviderPath(operator, id string, path ProviderJourney) bool {
	if id != ProviderJourneyID(path) || !strings.HasPrefix(path.Route, operator+":") {
		return false
	}
	if len(path.Visits) < 2 || len(path.Visits) > 512 || path.Direction == "" {
		return false
	}
	return validProviderPathVisits(operator, path)
}

func validProviderPathVisits(operator string, path ProviderJourney) bool {
	for i, visit := range path.Visits {
		if !validProviderVisit(operator, visit) {
			return false
		}
		if i > 0 && visit.Sequence <= path.Visits[i-1].Sequence {
			return false
		}
	}
	return true
}

func validProviderVisit(operator string, visit ProviderVisit) bool {
	if !strings.HasPrefix(visit.Stop, operator+":") || visit.Name == "" {
		return false
	}
	if !finitePosition(visit.Lat, visit.Lon) {
		return false
	}
	return visit.Lat >= 38 && visit.Lat <= 40 && visit.Lon >= -10 && visit.Lon <= -8
}

func finiteCoordinate(value float64) bool  { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func finitePosition(lat, lon float64) bool { return finiteCoordinate(lat) && finiteCoordinate(lon) }

func freshObservation(v Observation, at time.Time) bool {
	if !reportedObservationIdentity(v) || v.PositionKind != "reported" || v.ObservedAt.IsZero() {
		return false
	}
	age := at.Sub(v.ObservedAt)
	return age >= 0 && age <= 90*time.Second && finitePosition(v.Lat, v.Lon)
}

func reportedObservationIdentity(v Observation) bool {
	fields := []string{v.ID, v.SourceID, v.SourceURL, v.Trip, v.Journey, v.Status, v.Stop}
	for _, field := range fields {
		if field == "" {
			return false
		}
	}
	return true
}

func observationIndex(path ProviderJourney, v Observation, track *providerTrack) int {
	found := -1
	for i, visit := range path.Visits {
		if visit.Stop != v.Stop || !visitNearTrackedSignal(path, track, i) {
			continue
		}
		if found >= 0 {
			return -1
		}
		found = i
	}
	return found
}

func visitNearTrackedSignal(path ProviderJourney, track *providerTrack, index int) bool {
	if track == nil || len(track.Group.Signals) == 0 {
		return true
	}
	last := track.Group.Signals[len(track.Group.Signals)-1]
	previous := providerSignalIndex(path, last)
	return index >= previous && index <= previous+1
}
