package patterns

import "time"

type metroForecastChain struct {
	current, platform, reason string
	at                        time.Time
	validUntil                *time.Time
	seen                      map[string]bool
	components                []Component
}

func latestSupportedSignal(g *group) *signal {
	latest := -1
	for i, s := range g.Signals {
		if s.Supported && (latest < 0 || s.U.After(g.Signals[latest].U)) {
			latest = i
		}
	}
	if latest < 0 {
		return nil
	}
	return &g.Signals[latest]
}

func (b metroForecastBuilder) beginChain(g *group) (metroForecastChain, bool) {
	chain := metroForecastChain{}
	if !g.Active {
		return chain, false
	}
	latest := latestSupportedSignal(g)
	if latest == nil {
		return chain, false
	}
	anchor := b.topology.next(plannedOrigin{Stop: latest.Stop, Direction: g.Direction, Route: g.Route})
	if anchor == "" {
		return chain, false
	}
	chain.current, chain.seen, chain.components = anchor, map[string]bool{anchor: true}, []Component{}
	value := officialFor(b.receipt.Rows, anchor, g.Train, g.Direction, b.receipt.ReceivedAt)
	if value == nil {
		chain.reason = "missing_fresh_future_anchor"
	} else {
		chain.at, chain.platform = value.At, value.Platform
		expiry := value.Source.Add(90 * time.Second)
		chain.validUntil = &expiry
	}
	return chain, true
}

func (b metroForecastBuilder) groupForecasts(g *group) []Forecast {
	out := []Forecast{}
	chain, valid := b.beginChain(g)
	if !valid {
		return out
	}
	for depth := 0; depth < 80; depth++ {
		target := b.topology.next(plannedOrigin{Stop: chain.current, Direction: g.Direction, Route: g.Route})
		if target == "" || chain.seen[target] {
			break
		}
		chain.seen[target] = true
		point := b.advanceChain(g, &chain, target)
		out = append(out, b.makeForecast(g, point))
		if point.official != nil && point.official.First {
			point.function = "waiting"
			out = append(out, b.makeForecast(g, point))
		}
	}
	return out
}

func (b metroForecastBuilder) advanceChain(g *group, chain *metroForecastChain, target string) metroForecastPoint {
	value := officialFor(b.receipt.Rows, target, g.Train, g.Direction, b.receipt.ReceivedAt)
	targetPlatform := ""
	if value != nil {
		targetPlatform = value.Platform
	}
	own := b.advanceComponents(g, chain, target)
	point := metroForecastPoint{target: target, platform: targetPlatform, function: "onward", official: value, own: own, validUntil: chain.validUntil, components: chain.components, reason: chain.reason}
	chain.current, chain.platform = target, targetPlatform
	// Missing official platform may use the unique modeled target platform next.
	if chain.platform == "" && own != nil {
		chain.platform = chain.components[len(chain.components)-1].targetPlatform
	}
	return point
}

func (b metroForecastBuilder) advanceComponents(g *group, chain *metroForecastChain, target string) *time.Time {
	if chain.reason != "" {
		return nil
	}
	request := componentRequest{chain.current, target, chain.platform, g.Route, g.Direction, b.receipt.condition(g.Route), chain.at, b.receipt.ReceivedAt}
	component := b.engine.componentSummary(request, b.config)
	if component.Samples == 0 {
		chain.reason = "insufficient_compatible_components"
		return nil
	}
	chain.components = append(chain.components, component)
	chain.at = chain.at.Add(time.Duration(component.Seconds * float64(time.Second)))
	value := chain.at
	return &value
}
