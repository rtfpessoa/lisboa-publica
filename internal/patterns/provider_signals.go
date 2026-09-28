package patterns

func (p *providerState) appendProviderSignal(row providerSampleRow, q providerSample) bool {
	v := row.observation
	s := signal{Stop: v.Stop, Platform: visitPlatform(row.path.Visits[row.index]), L: row.track.Previous.ObservedAt, U: v.ObservedAt}
	g := row.track.Group
	if !nextProviderSignal(row.path, g, s, row.index) {
		p.cut(v.ID, false, q.config)
		return false
	}
	g.Signals = append(g.Signals, s)
	if len(g.Signals) >= 3 {
		p.supportProviderSignals(row, q)
	}
	return true
}

func nextProviderSignal(path ProviderJourney, g *group, s signal, index int) bool {
	if len(g.Signals) == 0 {
		return true
	}
	last := g.Signals[len(g.Signals)-1]
	previous := providerSignalIndex(path, last)
	return index == previous+1 && last.U.Before(s.L)
}

func providerSignalIndex(path ProviderJourney, s signal) int {
	index := -1
	for i, visit := range path.Visits {
		if visit.Stop == s.Stop && visitPlatform(visit) == s.Platform {
			index = i
		}
	}
	return index
}

func (p *providerState) supportProviderSignals(row providerSampleRow, q providerSample) {
	g := row.track.Group
	g.Active = true
	for i := range g.Signals {
		p.supportProviderSignal(row, q, i)
		if i > 0 {
			p.supportProviderComponent(row, q, i)
		}
	}
	if len(g.Signals) > 3 {
		g.Signals = g.Signals[len(g.Signals)-3:]
	}
	if len(g.Pairs) > 512 {
		g.Pairs = map[string]bool{}
	}
}

func (p *providerState) supportProviderSignal(row providerSampleRow, q providerSample, index int) {
	g := row.track.Group
	if g.Signals[index].Supported {
		return
	}
	g.Signals[index].Supported = true
	s := g.Signals[index]
	profile := providerProfile(row.path, q.config)
	a := baseAggregate(aggregateRequest{s.U, row.path.Route, row.path.Direction, s.Stop, s.Platform, profile, "unknown", "signals"}, q.config)
	a.InputFrom, a.Count, a.KnownAt = s.L.UnixNano(), 1, q.receipt.ReceivedAt.UnixNano()
	if hourKey(s.L) == hourKey(s.U) {
		p.Engine.add(a)
	}
	p.Engine.evaluateReference(g, s, q.receipt.ReceivedAt, q.config)
}

func (p *providerState) supportProviderComponent(row providerSampleRow, q providerSample, index int) {
	g := row.track.Group
	from, to := g.Signals[index-1], g.Signals[index]
	key := digest([]any{from, to})
	if g.Pairs[key] {
		return
	}
	g.Pairs[key] = true
	if hourKey(from.L) != hourKey(from.U) {
		return
	}
	low, high := to.L.Sub(from.U).Seconds(), to.U.Sub(from.L).Seconds()
	if low <= 0 || high > 7200 {
		return
	}
	profile := providerProfile(row.path, q.config)
	a := baseAggregate(aggregateRequest{from.U, row.path.Route, row.path.Direction, from.Stop, from.Platform, profile, "unknown", "component"}, q.config)
	a.InputFrom, a.Target, a.TargetPlatform = from.L.UnixNano(), to.Stop, to.Platform
	a.Count, a.Sum, a.LowerSum, a.UpperSum, a.KnownAt = 1, (low+high)/2, low, high, q.receipt.ReceivedAt.UnixNano()
	p.Engine.add(a)
}
