package patterns

import (
	"math"
	"strconv"
	"strings"
	"time"
)

func (e *engine) applyMetroPresence(sample metroSample) {
	for key := range sample.presence.conflict {
		delete(sample.presence.present, key)
	}
	for key := range e.Groups {
		if !sample.presence.present[key] {
			delete(e.Groups, key)
		}
	}
	for key, signals := range sample.signals {
		if !sample.presence.present[key] {
			continue
		}
		g := e.Groups[key]
		if g == nil {
			g = newMetroGroup(key, sample.now)
			e.Groups[key] = g
		}
		g.Signals = append(g.Signals, signals...)
	}
}

func newMetroGroup(key string, now time.Time) *group {
	parts := strings.Split(key, "|")
	return &group{ID: digest([]string{key, now.Format(time.RFC3339Nano)})[:24], Train: parts[0], Direction: parts[1], Route: parts[2], Pairs: map[string]bool{}}
}

func (e *engine) supportMetroGroups(sample metroSample) {
	for key, g := range e.Groups {
		if len(g.Signals) > 80 {
			delete(e.Groups, key)
			continue
		}
		pairs := associate(g.Signals, sample.topology, g.Route, g.Direction)
		if contradictedMetroPairs(g.Pairs, pairs) {
			e.withdraw(g)
			delete(e.Groups, key)
			continue
		}
		e.supportMetroTriples(g, pairs, sample)
	}
}

func metroPairKey(a, b int) string { return strconv.Itoa(a) + ":" + strconv.Itoa(b) }

func contradictedMetroPairs(supported map[string]bool, pairs map[int]int) bool {
	valid := map[string]bool{}
	for a, b := range pairs {
		if c, exists := pairs[b]; exists {
			valid[metroPairKey(a, b)] = true
			valid[metroPairKey(b, c)] = true
		}
	}
	for key := range supported {
		if !valid[key] {
			return true
		}
	}
	return false
}

func (e *engine) supportMetroTriples(g *group, pairs map[int]int, sample metroSample) {
	for a, b := range pairs {
		c, exists := pairs[b]
		if !exists {
			continue
		}
		g.Active = true
		for _, index := range []int{a, b, c} {
			e.supportMetroSignal(g, index, sample)
		}
		for _, pair := range [][2]int{{a, b}, {b, c}} {
			e.supportMetroComponent(g, pair, sample)
		}
	}
}

func (e *engine) supportMetroSignal(g *group, index int, sample metroSample) {
	s := &g.Signals[index]
	if s.Supported {
		return
	}
	s.Supported = true
	if hourKey(s.L) == hourKey(s.U) {
		agg := baseAggregate(aggregateRequest{s.L, g.Route, g.Direction, s.Stop, s.Platform, e.Profile, sample.receipt.condition(g.Route), "signals"}, sample.config)
		agg.Count, agg.KnownAt = 1, sample.now.UnixNano()
		e.add(agg)
	}
	e.evaluateReference(g, *s, sample.now, sample.config)
}

func (e *engine) supportMetroComponent(g *group, pair [2]int, sample metroSample) {
	key := metroPairKey(pair[0], pair[1])
	if g.Pairs[key] {
		return
	}
	g.Pairs[key] = true
	origin, target := g.Signals[pair[0]], g.Signals[pair[1]]
	if hourKey(origin.L) != hourKey(origin.U) {
		return
	}
	lower := math.Max(0, target.L.Sub(origin.U).Seconds())
	upper := target.U.Sub(origin.L).Seconds()
	mid := (lower + upper) / 2
	agg := baseAggregate(aggregateRequest{origin.L, g.Route, g.Direction, origin.Stop, origin.Platform, e.Profile, sample.receipt.condition(g.Route), "component"}, sample.config)
	agg.Compatibility = e.componentKey(Segment{Route: g.Route, Direction: g.Direction, Origin: origin.Stop, Target: target.Stop})
	agg.Target, agg.TargetPlatform = target.Stop, target.Platform
	agg.Bucket = int32(math.Floor(mid / float64(sample.config.BinSeconds)))
	agg.Count, agg.Sum, agg.LowerSum, agg.UpperSum, agg.KnownAt = 1, mid, lower, upper, sample.now.UnixNano()
	e.add(agg)
}
