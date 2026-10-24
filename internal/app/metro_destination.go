package app

import (
	"strings"

	"lisboapublica/internal/patterns"
)

// metroDestinationResolver prefers one current catalogue assignment. Cached
// resolutions are scoped to one immutable Metro publication.
type metroDestinationResolver struct {
	data     *MetroData
	cache    map[string]metroDestinationResolution
	topology patterns.Topology
	routes   map[metroDestinationRoute]string
}

type metroDestinationRoute struct{ stop, direction string }

type metroDestinationResolution struct {
	name   string
	named  bool
	legacy bool
}

func newMetroDestinationResolver(data *MetroData, topology patterns.Topology) *metroDestinationResolver {
	return &metroDestinationResolver{data: data, topology: topology, cache: map[string]metroDestinationResolution{}, routes: map[metroDestinationRoute]string{}}
}

func (r *metroDestinationResolver) resolve(code string) metroDestinationResolution {
	if resolved, ok := r.cache[code]; ok {
		return resolved
	}
	resolved := r.resolveUncached(code)
	r.cache[code] = resolved
	return resolved
}

func (r *metroDestinationResolver) resolveUncached(code string) metroDestinationResolution {
	if code == "" {
		return metroDestinationResolution{}
	}
	name, unique := uniqueMetroDestination(r.data.Destinations, code)
	stationID, legacyCode := destinations[code]
	station, stationUnique := uniqueMetroStation(r.data.Stations, func(s MetroStation) bool { return s.ID == stationID })
	if metroDestinationCodePresent(r.data.Destinations, code) {
		// A present but duplicate or empty assignment is not a missing one.
		legacy := unique && legacyCode && stationUnique && normalizeName(strings.TrimSpace(station.Name)) == normalizeName(name)
		return metroDestinationResolution{name: name, named: unique, legacy: legacy}
	}
	return r.resolveLegacyDestination(station, stationUnique, legacyCode)
}

func metroDestinationCodePresent(entries []MetroDestination, code string) bool {
	for _, entry := range entries {
		if entry.ID == code {
			return true
		}
	}
	return false
}

func (r *metroDestinationResolver) resolveLegacyDestination(station MetroStation, stationUnique, legacyCode bool) metroDestinationResolution {
	if !legacyCode {
		return metroDestinationResolution{}
	}
	if !stationUnique || strings.TrimSpace(station.Name) == "" {
		// An empty catalogue cannot contradict the legacy line mapping, though
		// a missing station cannot provide a destination name.
		return metroDestinationResolution{legacy: len(r.data.Destinations) == 0}
	}
	if metroDestinationReassigned(r.data.Destinations, station.Name) {
		return metroDestinationResolution{}
	}
	return metroDestinationResolution{name: station.Name, named: true, legacy: true}
}

func metroDestinationReassigned(entries []MetroDestination, stationName string) bool {
	for _, entry := range entries {
		if entry.ID != "" && normalizeName(strings.TrimSpace(entry.Name)) == normalizeName(strings.TrimSpace(stationName)) {
			return true
		}
	}
	return false
}

// A unique route through an admitted path can resolve a transfer station. A
// path contradicting the current name cannot supply line evidence.
func (r *metroDestinationResolver) route(wait MetroWait, destination metroDestinationResolution) string {
	key := metroDestinationRoute{stop: wait.Stop, direction: wait.Destination}
	if route, ok := r.routes[key]; ok {
		return route
	}
	route := r.routeUncached(wait, destination)
	r.routes[key] = route
	return route
}

func (r *metroDestinationResolver) routeUncached(wait MetroWait, destination metroDestinationResolution) string {
	if !destination.named && !destination.legacy {
		return ""
	}
	route := ""
	for _, path := range r.topology.Patterns {
		if path.Direction != wait.Destination || !metroPathContains(path, wait.Stop) {
			continue
		}
		if destination.named {
			station, unique := uniqueMetroStation(r.data.Stations, func(s MetroStation) bool { return s.ID == path.Destination })
			if !unique || normalizeName(strings.TrimSpace(station.Name)) != normalizeName(destination.name) {
				return ""
			}
		}
		if route != "" && route != path.Route {
			return ""
		}
		route = path.Route
	}
	return route
}

// A path may support a journey or pattern route only when the same current
// evidence also permits the generic arrival to use that route.
func (r *metroDestinationResolver) admittedRoute(wait MetroWait, routeIDs map[string]string) string {
	route := r.route(wait, r.resolve(wait.Destination))
	if route != "" && metroArrival(wait, routeIDs, r, "").RouteId == route {
		return route
	}
	return ""
}

// Direction groups must be valid at every station in a path. This single
// catalogue is shared by journeys, forecasts and station tabs so a short
// turn cannot inherit an endpoint that contradicts another station.
func metroDirectionTopology(data *MetroData, static *StaticData, topology patterns.Topology) patterns.Topology {
	admitted := topology
	admitted.Patterns = nil
	resolver, routeIDs := newMetroDestinationResolver(data, topology), metroRouteIDs(static)
	for _, path := range topology.Patterns {
		valid := true
		for _, stop := range path.Stops {
			if resolver.admittedRoute(MetroWait{Stop: stop, Destination: path.Direction}, routeIDs) != path.Route {
				valid = false
				break
			}
		}
		if valid {
			admitted.Patterns = append(admitted.Patterns, path)
		}
	}
	return admitted
}
