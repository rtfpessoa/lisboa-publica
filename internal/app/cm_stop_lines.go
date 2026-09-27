package app

import "strings"

// Exact GTFS route membership narrows a CM station read before decompressing
// visits. Native published-pattern coverage is not used as a timetable filter.
func (importer cmJourneyImport) mergeStopLines(data *StaticData, lines map[string]string) {
	schedule := importer.network.Schedule
	if schedule.StopLines == nil {
		schedule.StopLines = map[string][]string{}
	}
	for _, route := range data.Routes {
		line := lines[route.SourceId]
		for _, qualified := range route.StopIds {
			stop := strings.TrimPrefix(qualified, importer.provider.ID+":")
			retainStopLine(schedule, stop, line)
			if parent := data.Schedule.Parents[stop]; parent != "" {
				retainStopLine(schedule, parent, line)
			}
		}
	}
}

func retainStopLine(schedule *Schedule, stop, line string) {
	for _, known := range schedule.StopLines[stop] {
		if known == line {
			return
		}
	}
	schedule.StopLines[stop] = append(schedule.StopLines[stop], line)
}

func popupStopIncludesRoute(schedule *Schedule, stop, route string) bool {
	if schedule.StopLines == nil {
		return true
	}
	for _, line := range schedule.StopLines[stop] {
		if line == route {
			return true
		}
	}
	return false
}
