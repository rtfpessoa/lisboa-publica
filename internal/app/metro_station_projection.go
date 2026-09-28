package app

import (
	"strconv"
	"strings"

	"lisboapublica/internal/api"
)

func metroStationCandidateMatches(station MetroStation, stop api.Stop) bool {
	lat, latErr := strconv.ParseFloat(station.Lat, numericBitSize)
	lon, lonErr := strconv.ParseFloat(station.Lon, numericBitSize)
	nameMatches := strings.HasPrefix(normalizeName(stop.Name), normalizeName(station.Name)) || compactMetroName(stop.Name) == compactMetroName(station.Name)
	return station.Name != "" && latErr == nil && lonErr == nil && nameMatches && abs(stop.Lat-lat) < metroStationTolerance && abs(stop.Lon-lon) < metroStationTolerance
}

func metroStopCatalog(static *StaticData) map[string]api.Stop {
	catalog := map[string]api.Stop{}
	for _, stop := range static.Stops {
		catalog[stop.Id] = stop
	}
	return catalog
}

// Platforms only collapse through a complete, acyclic published parent chain.
func metroStationRoot(stop api.Stop, catalog map[string]api.Stop) (api.Stop, bool) {
	visited := map[string]bool{}
	valid := true
	for stop.ParentId != nil && *stop.ParentId != "" && valid {
		visited[stop.Id] = true
		parent, exists := catalog[*stop.ParentId]
		valid = exists && !visited[parent.Id] && strings.HasPrefix(parent.Id, "metro:")
		stop = parent
	}
	return stop, valid
}

func metroPopupStationID(station MetroStation, catalog map[string]api.Stop) string {
	roots := map[string]bool{}
	invalid := false
	for _, stop := range catalog {
		if !metroStationCandidateMatches(station, stop) {
			continue
		}
		root, valid := metroStationRoot(stop, catalog)
		if valid && metroStationCandidateMatches(station, root) {
			roots[root.Id] = true
		} else {
			invalid = true
		}
	}
	if len(roots) == 1 && !invalid {
		for id := range roots {
			return id
		}
	}
	return "metro:" + station.ID
}
