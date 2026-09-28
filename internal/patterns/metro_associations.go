package patterns

func associate(signals []signal, t Topology, route, direction string) map[int]int {
	overlap := overlappingMetroSignals(signals)
	proposed, incoming := map[int]int{}, map[int]int{}
	for i, a := range signals {
		if overlap[i] {
			continue
		}
		next := t.next(plannedOrigin{Stop: a.Stop, Direction: direction, Route: route})
		if next == "" {
			continue
		}
		target, unique := uniqueFollowingSignal(signals, overlap, i, next)
		if unique {
			proposed[i] = target
			incoming[target]++
		}
	}
	for origin, target := range proposed {
		if incoming[target] != 1 {
			delete(proposed, origin)
		}
	}
	return proposed
}

func overlappingMetroSignals(signals []signal) map[int]bool {
	overlap := map[int]bool{}
	for i, a := range signals {
		for j := i + 1; j < len(signals); j++ {
			b := signals[j]
			if a.Stop != b.Stop && !a.U.Before(b.L) && !b.U.Before(a.L) {
				overlap[i], overlap[j] = true, true
			}
		}
	}
	return overlap
}

func uniqueFollowingSignal(signals []signal, overlap map[int]bool, origin int, next string) (int, bool) {
	target, count := -1, 0
	for i, candidate := range signals {
		if !overlap[i] && candidate.Stop == next && candidate.L.After(signals[origin].U) {
			target = i
			count++
		}
	}
	return target, count == 1
}
