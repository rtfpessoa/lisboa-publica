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
	line := unavailableMetroLine(station, destination, named, data.Stations)
	route := l.routes[line]
	if !unavailableMetroRoute(l.runtime.plan.Routes, route, line) {
		return api.Arrival{}, false
	}
	if !named {
		destination = "Destino publicado: " + row.Destination
	}
	return api.Arrival{OperatorId: "metro", StopId: metroPopupStationID(station, l.catalog), RouteId: route, Headsign: destination, Kind: api.ArrivalKindPrediction, SourceUrl: metroBase + "/tempoEspera/Estacao/todos"}, true
}

func unavailableMetroRoute(routes []api.RouteDetail, id, line string) bool {
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

func unavailableMetroLine(origin MetroStation, destination string, named bool, stations []MetroStation) string {
	lines, ok := metroPublishedLines(origin.Lines)
	if !ok {
		return ""
	}
	if named {
		narrowMetroStationLines(lines, destination, stations)
	}
	if len(lines) == 1 {
		for line := range lines {
			return line
		}
	}
	return ""
}

func narrowMetroStationLines(lines map[string]bool, destination string, stations []MetroStation) {
	name := normalizeName(destination)
	target, found := uniqueMetroStation(stations, func(s MetroStation) bool {
		return strings.TrimSpace(s.Name) != "" && normalizeName(strings.TrimSpace(s.Name)) == name
	})
	if !found {
		return
	}
	targetLines, valid := metroPublishedLines(target.Lines)
	if !valid {
		return
	}
	for line := range lines {
		if !targetLines[line] {
			delete(lines, line)
		}
	}
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
