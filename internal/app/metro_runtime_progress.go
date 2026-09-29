package app

import (
	"lisboapublica/internal/api"
	"time"
)

func metroPointClockConflict(old, p metroPoint) bool {
	return old.Clock.Equal(p.Clock) && !equalMetroSeconds(old.Seconds, p.Seconds)
}
func conflictingMetroPoints(t *metroTrack, current map[string]metroPoint, now time.Time) bool {
	zeros := 0
	for code, p := range current {
		if p.Seconds != nil && *p.Seconds == 0 {
			zeros++
		}
		if old, ok := t.Points[code]; ok && (p.Clock.Before(old.Clock) || metroPointClockConflict(old, p)) {
			return true
		}
	}
	return zeros > 1 || conflictingMetroOrder(t.Codes, current, now)
}
func conflictingMetroOrder(codes []string, current map[string]metroPoint, now time.Time) bool {
	var previous *time.Time
	for _, code := range codes {
		p, ok := current[code]
		if !ok || p.Seconds == nil {
			continue
		}
		expected := p.Clock.Add(time.Duration(*p.Seconds) * time.Second)
		if expected.Before(now) {
			continue
		}
		if previous != nil && expected.Before(*previous) {
			return true
		}
		previous = &expected
	}
	return false
}
func firstMetroCurrent(t *metroTrack, current map[string]metroPoint, now time.Time) *int {
	for n, code := range t.Codes {
		p, ok := current[code]
		if ok && p.Seconds != nil && !p.Clock.Add(time.Duration(*p.Seconds)*time.Second).Before(now) {
			return ptr(n)
		}
	}
	return nil
}

// Retract a coherent newer positive wait before suspending backwards continuity.
func (r *metroRuntime) retractArrivals(t *metroTrack, current map[string]metroPoint) {
	for n := range t.Train.Calls {
		c := &t.Train.Calls[n]
		p, found := current[t.Codes[n]]
		if found && metroArrivalContradicted(*c, p) {
			withdrawMetroDeparture(t, n, p.Clock, "Chegada de suporte retirada por correção da mesma visita")
			delete(t.ModelStops, c.Id)
			c.Arrival = missingCallTime("Chegada inferida retirada: previsão posterior incompatível")
			if old, ok := t.Points[p.Stop]; ok {
				r.queueArrival(t, *c, old, p)
			}
		}
	}
}
func metroArrivalContradicted(c api.StopCall, p metroPoint) bool {
	return p.Seconds != nil && *p.Seconds > 0 && c.Arrival.Inferred != nil && p.Clock.After(c.Arrival.Inferred.WindowEnd)
}
func metroRegressiveIndex(prior, current *int) bool {
	return prior != nil && current != nil && *current < *prior
}
func regressMetroTrack(t *metroTrack, now time.Time) {
	suspendMetroTrack(t, "Progresso regressivo; nova continuidade necessária")
	t.Train.ValidUntil = now
}
func (r *metroRuntime) projectPoints(t *metroTrack, current map[string]metroPoint, now time.Time) {
	t.Train.Association = "supported"
	t.Train.Reason = "Associação inferida; referência não identifica a unidade física"
	prior := t.Train.NextIndex
	t.Train.CurrentIndex, t.Train.NextIndex = nil, nil
	if !r.projectCurrentMetroVisits(t, current, now) {
		return
	}
	normalizeMetroNextIndex(t, prior, now)
	if metroRegressiveIndex(prior, t.Train.NextIndex) {
		regressMetroTrack(t, now)
		return
	}
	if len(t.Train.Calls) > 0 && t.Train.Calls[0].Arrival.Inferred != nil {
		t.Train.OriginKnown = ptr(true)
	}
	metroCallPhases(&t.Train)
	t.Points = current
}
func (r *metroRuntime) projectCurrentMetroVisits(t *metroTrack, current map[string]metroPoint, now time.Time) bool {
	projection := metroPointProjection{runtime: r, track: t, now: now}
	for n := range t.Train.Calls {
		call := &t.Train.Calls[n]
		if call.Arrival.Kind == "prediction" {
			call.Arrival = missingCallTime("Sem previsão atual")
		}
		if point, found := current[t.Codes[n]]; found && !projection.apply(n, point) {
			return false
		}
	}
	return true
}
func normalizeMetroNextIndex(t *metroTrack, prior *int, now time.Time) {
	if t.Train.NextIndex == nil && t.Train.SourceUpdatedAt.Add(sourceFreshness).After(now) {
		t.Train.NextIndex = prior
	}
	if t.Train.CurrentIndex == nil {
		return
	}
	next := *t.Train.CurrentIndex + 1
	t.Train.NextIndex = nil
	if next < len(t.Train.Calls) {
		t.Train.NextIndex = &next
	}
}

func metroCallPhases(t *api.MetroTrain) {
	if t.NextIndex == nil {
		return
	}
	for n := range t.Calls {
		c := &t.Calls[n]
		if n < *t.NextIndex && (c.Arrival.Inferred != nil || c.Arrival.Actual != nil) {
			c.Phase = "previous"
		} else if n >= *t.NextIndex {
			c.Phase = "future"
		} else {
			c.Phase = "unknown"
		}
		if t.CurrentIndex != nil && n == *t.CurrentIndex {
			c.Phase = "current"
		}
	}
}

func metroRegressiveCurrent(t *metroTrack, points map[string]metroPoint, now time.Time) bool {
	current := firstMetroCurrent(t, points, now)
	if current != nil && t.Train.CurrentIndex != nil && *current == *t.Train.CurrentIndex {
		p := points[t.Codes[*current]]
		if p.Seconds != nil && *p.Seconds == 0 {
			return false
		}
	}
	return metroRegressiveIndex(t.Train.NextIndex, current)
}
