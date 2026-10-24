package app

import (
	"strings"

	"lisboapublica/internal/api"
)

// Without a current path, neither a legacy destination code nor a stale path
// proves the line. Use only unambiguous current catalogue evidence.
func (l metroLocalContexts) unavailableArrival(row MetroWait) (api.Arrival, bool) {
	data := l.runtime.publication
	station, ok := uniqueMetroStation(data.Stations, func(s MetroStation) bool { return s.ID == row.Stop })
	if !ok {
		return api.Arrival{}, false
	}
	destination, named := uniqueMetroDestination(data.Destinations, row.Destination)
	line, _ := metroStationLineEvidence(station, destination, named, data.Stations)
	route := l.routes[line]
	if !validMetroRoute(l.runtime.plan.Routes, route, line) {
		route = ""
	}
	if !named {
		destination = "Destino publicado: " + row.Destination
	}
	return api.Arrival{OperatorId: "metro", StopId: metroPopupStationID(station, l.catalog), RouteId: route, Headsign: destination, Kind: api.ArrivalKindPrediction, SourceUrl: metroBase + "/tempoEspera/Estacao/todos"}, true
}

func validMetroRoute(routes []api.RouteDetail, id, line string) bool {
	shortName := map[string]string{"azul": "Az", "amarela": "Am", "verde": "Vd", "vermelha": "Vm"}[line]
	if id == "" || shortName == "" {
		return false
	}
	found := false
	for _, route := range routes {
		if route.Id == id {
			if route.ShortName != shortName {
				return false
			}
			found = true
		}
	}
	return found
}

func uniqueMetroDestination(catalog []MetroDestination, id string) (string, bool) {
	if id == "" {
		return "", false
	}
	name, count := "", 0
	for _, destination := range catalog {
		if destination.ID == id {
			name, count = strings.TrimSpace(destination.Name), count+1
		}
	}
	return name, count == 1 && name != ""
}

func uniqueMetroStation(stations []MetroStation, matches func(MetroStation) bool) (MetroStation, bool) {
	var result MetroStation
	count := 0
	for _, station := range stations {
		if matches(station) {
			result, count = station, count+1
		}
	}
	return result, count == 1
}

// An empty intersection of two valid memberships is a contradiction, not
// merely an ambiguous line. Callers must not override it with older evidence.
func metroStationLineEvidence(origin MetroStation, destination string, named bool, stations []MetroStation) (string, bool) {
	lines, ok := metroPublishedLines(origin.Lines)
	if !ok {
		return "", false
	}
	if named {
		if narrowMetroStationLines(lines, destination, stations) && len(lines) == 0 {
			return "", true
		}
	}
	if len(lines) == 1 {
		for line := range lines {
			return line, false
		}
	}
	return "", false
}

func narrowMetroStationLines(lines map[string]bool, destination string, stations []MetroStation) bool {
	name := normalizeName(destination)
	target, found := uniqueMetroStation(stations, func(s MetroStation) bool {
		return strings.TrimSpace(s.Name) != "" && normalizeName(strings.TrimSpace(s.Name)) == name
	})
	if !found {
		return false
	}
	targetLines, valid := metroPublishedLines(target.Lines)
	if !valid {
		return false
	}
	for line := range lines {
		if !targetLines[line] {
			delete(lines, line)
		}
	}
	return true
}

func metroPublishedLines(value string) (map[string]bool, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	lines := map[string]bool{}
	for _, member := range strings.Split(value, ",") {
		line := normalizeName(strings.TrimSpace(member))
		switch line {
		case "azul", "amarela", "verde", "vermelha":
			lines[line] = true
		default:
			return nil, false
		}
	}
	return lines, len(lines) > 0
}
