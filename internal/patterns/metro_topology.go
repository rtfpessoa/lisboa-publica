package patterns

func (t Topology) route(stop, direction string) string {
	routes := map[string]bool{}
	for _, path := range t.Patterns {
		if path.Direction == direction && plannedPatternContains(path, stop) {
			routes[path.Route] = true
		}
	}
	if len(routes) != 1 {
		return ""
	}
	for route := range routes {
		return route
	}
	return ""
}

func plannedPatternContains(path Pattern, stop string) bool {
	for _, visit := range path.Stops {
		if visit == stop {
			return true
		}
	}
	return false
}

func (t Topology) next(origin plannedOrigin) string {
	stop, direction, route := origin.Stop, origin.Direction, origin.Route
	next := ""
	for _, path := range t.Patterns {
		if path.Direction != direction || path.Route != route {
			continue
		}
		candidates, valid := adjacentPatternVisits(path, stop)
		if !valid {
			return ""
		}
		for _, candidate := range candidates {
			if next != "" && next != candidate {
				return ""
			}
			next = candidate
		}
	}
	return next
}

func adjacentPatternVisits(path Pattern, stop string) ([]string, bool) {
	next := []string{}
	for i, visit := range path.Stops {
		if visit != stop {
			continue
		}
		if !consecutivePatternVisit(path, i) {
			return nil, false
		}
		next = append(next, path.Stops[i+1])
	}
	return next, true
}

func consecutivePatternVisit(path Pattern, index int) bool {
	if index+1 >= len(path.Stops) || len(path.Sequences) != len(path.Stops) {
		return false
	}
	return path.Sequences[index+1] == path.Sequences[index]+1
}

func (t Topology) destination(direction string) string {
	for _, p := range t.Patterns {
		if p.Direction == direction {
			return p.Destination
		}
	}
	return ""
}
