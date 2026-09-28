package app

import (
	"lisboapublica/internal/api"
	"time"
)

func latestMetroPoints(points []metroPoint) (map[string]metroPoint, bool) {
	current := map[string]metroPoint{}
	conflict := false
	for _, p := range points {
		if old, ok := current[p.Stop]; ok {
			if old.Platform != p.Platform || metroPointClockConflict(old, p) {
				conflict = true
			}
			if !p.Clock.After(old.Clock) {
				continue
			}
		}
		current[p.Stop] = p
	}
	return current, conflict
}
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
	t.Train.CurrentIndex = nil
	t.Train.NextIndex = nil
	projection := metroPointProjection{runtime: r, track: t, now: now}
	for n := range t.Train.Calls {
		c := &t.Train.Calls[n]
		if c.Arrival.Kind == "prediction" {
			c.Arrival = missingCallTime("Sem previsão atual")
		}
		if p, found := current[t.Codes[n]]; found && !projection.apply(n, p) {
			return
		}
	}
	if t.Train.NextIndex == nil && t.Train.SourceUpdatedAt.Add(sourceFreshness).After(now) {
		t.Train.NextIndex = prior
	}
	if metroRegressiveIndex(prior, t.Train.NextIndex) {
		regressMetroTrack(t, now)
		return
	}
	metroCallPhases(&t.Train)
	t.Points = current
}
func metroCallPhases(t *api.MetroTrain) {
	if t.NextIndex == nil {
		return
	}
	for n := range t.Calls {
		c := &t.Calls[n]
		if n < *t.NextIndex {
			c.Phase = "previous"
		} else {
			c.Phase = "future"
		}
		if t.CurrentIndex != nil && n == *t.CurrentIndex {
			c.Phase = "current"
		}
	}
}
