package patterns

import "time"

type componentRequest struct {
	origin, target, platform, route, direction, condition string
	at, issued                                            time.Time
}

type componentContext struct {
	request                                                        componentRequest
	reference, compatibility, profile, oldest, recent, latest, day string
	hour, offset                                                   int32
}

type componentFallback struct{ old, general bool }

type componentTotals struct {
	sum             float64
	count           int64
	days, platforms map[string]bool
	oldest, newest  string
}

func (e *engine) componentContext(q componentRequest, c Config) componentContext {
	local := q.at.In(lisbon)
	_, offset := local.Zone()
	issued := q.issued.In(lisbon)
	return componentContext{
		request: q, reference: engineReference(e),
		compatibility: e.componentKey(Segment{Route: q.route, Direction: q.direction, Origin: q.origin, Target: q.target}),
		profile:       resolutionProfile(e.Profile),
		oldest:        issued.AddDate(0, -c.AggregateMonths, 0).Format("2006-01-02"),
		recent:        issued.AddDate(0, 0, -c.TrainingDays).Format("2006-01-02"),
		latest:        issued.Format("2006-01-02"), day: dayType(q.at),
		hour: int32(local.Hour()), offset: int32(offset),
	}
}

func (e *engine) componentSummary(q componentRequest, c Config) Component {
	result := Component{Origin: q.origin, Target: q.target, OriginAt: q.at}
	context := e.componentContext(q, c)
	// Prefer recent exact condition, old exact, recent general, then old general.
	passes := []componentFallback{{false, false}, {true, false}, {false, true}, {true, true}}
	for _, pass := range passes {
		totals := e.componentTotals(context, pass)
		if totals.count > 0 && len(totals.platforms) == 1 {
			return totals.result(result, pass)
		}
	}
	return result
}

func (e *engine) componentTotals(context componentContext, pass componentFallback) componentTotals {
	totals := componentTotals{days: map[string]bool{}, platforms: map[string]bool{}}
	for _, row := range e.Aggregates {
		if context.matches(row) && pass.matches(row, context) {
			totals.add(row)
		}
	}
	return totals
}

func (q componentContext) matches(a Aggregate) bool {
	checks := []bool{
		a.Kind == "component", a.Reference == q.reference,
		q.matchesPath(a), q.matchesTime(a), q.matchesKnowledge(a), q.matchesProfile(a),
	}
	for _, check := range checks {
		if !check {
			return false
		}
	}
	return true
}

func (q componentContext) matchesPath(a Aggregate) bool {
	if a.Route != q.request.route || a.Direction != q.request.direction {
		return false
	}
	return a.Stop == q.request.origin && a.Target == q.request.target && a.Platform == q.request.platform
}

func (q componentContext) matchesTime(a Aggregate) bool {
	if a.Hour != q.hour || a.Offset != q.offset || a.DayType != q.day {
		return false
	}
	return a.Date >= q.oldest && a.Date <= q.latest
}

func (q componentContext) matchesKnowledge(a Aggregate) bool {
	return a.KnownAt < q.request.issued.UnixNano() && a.Count > 0
}

func (q componentContext) matchesProfile(a Aggregate) bool {
	if a.Calendar != "" && a.Calendar != calendarVersion {
		return false
	}
	if a.Compatibility != "" {
		return a.Compatibility == q.compatibility
	}
	// Legacy same-profile fixtures cannot establish cross-profile compatibility.
	return resolutionProfile(a.Profile) == q.profile
}

func (p componentFallback) matches(a Aggregate, q componentContext) bool {
	if p.old != (a.Date < q.recent) {
		return false
	}
	return p.general || a.Condition == q.request.condition
}

func (t *componentTotals) add(a Aggregate) {
	t.platforms[a.TargetPlatform] = true
	t.sum += a.Sum
	t.count += a.Count
	t.days[a.Date] = true
	if t.oldest == "" || a.Date < t.oldest {
		t.oldest = a.Date
	}
	if t.newest == "" || a.Date > t.newest {
		t.newest = a.Date
	}
}

func (t componentTotals) result(result Component, pass componentFallback) Component {
	for platform := range t.platforms {
		result.targetPlatform = platform
	}
	result.Seconds = t.sum / float64(t.count)
	result.Samples, result.Days = t.count, len(t.days)
	result.HistoricalFallback, result.GeneralContext = pass.old, pass.general
	result.OldestDate, result.NewestDate = t.oldest, t.newest
	return result
}
