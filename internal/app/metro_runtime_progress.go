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

type metroArrivalCorrection struct {
	index         int
	point, before metroPoint
}

// Validate every correction before withdrawing any retained occurrence.
func (r *metroRuntime) retractArrivals(t *metroTrack, current map[string]metroPoint, now time.Time) bool {
	corrections := []metroArrivalCorrection{}
	var evidence *metroCorrectionEvidence
	for n := range t.Train.Calls {
		p, found := current[t.Codes[n]]
		if !found || !metroArrivalContradicted(t.Train.Calls[n], p) {
			continue
		}
		if evidence == nil {
			context, ok := r.metroOrdinaryCorrectionEvidence(t, current, now)
			if !ok {
				return false
			}
			evidence = &context
		}
		before, ok := metroEpisodeArrivalAfter(t, n)
		if !ok || !metroCorrectionClockValid(t, before, p, now) || !metroEpisodeCorrectionOrdered(p, evidence.remaining, t.Codes[evidence.bound:], now) {
			return false
		}
		corrections = append(corrections, metroArrivalCorrection{n, p, before})
	}
	for _, correction := range corrections {
		r.retractMetroArrival(t, correction.index, correction.point, correction.before)
	}
	return true
}

func (r *metroRuntime) metroOrdinaryCorrectionEvidence(t *metroTrack, current map[string]metroPoint, now time.Time) (metroCorrectionEvidence, bool) {
	if r.batch == nil {
		return metroCorrectionEvidence{}, false
	}
	key := metroTrackKey(metroTrackIdentity{t.Train.RouteId, t.ProviderDirection, t.Train.Reference})
	scope := t.Train.RouteId + "|" + t.Train.Reference
	selection := metroContextSelection{runtime: r, batch: r.batch, now: now}
	evidence, ok := selection.episodeCorrectionEvidence(scope, key, current)
	return evidence, ok && evidence.track == t
}
func (r *metroRuntime) retractMetroArrival(t *metroTrack, n int, p, old metroPoint) {
	c := &t.Train.Calls[n]
	if !metroArrivalContradicted(*c, p) {
		return
	}
	markMetroRetraction(t)
	withdrawMetroDeparture(t, n, p.Clock, "Chegada de suporte retirada por correção da mesma visita")
	delete(t.ModelStops, c.Id)
	c.Arrival = missingCallTime("Chegada inferida retirada: previsão posterior incompatível")
	c.LastOfficialEstimate = nil
	r.queueArrival(t, *c, old, p)
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
	var previous *api.CallTimeEvidence
	if prior != nil && *prior >= 0 && *prior < len(t.Train.Calls) {
		previous = t.Train.Calls[*prior].Arrival.Prediction
	}
	t.Train.CurrentIndex, t.Train.NextIndex = nil, nil
	if !r.projectCurrentMetroVisits(t, current, now) {
		return
	}
	normalizeMetroNextIndex(t, prior, previous, current, now)
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

// A missing row is not proof that a station was passed. Retain a still-fresh
// earlier estimate with its original clock, unless a later arrival was observed.
func normalizeMetroNextIndex(t *metroTrack, prior *int, previous *api.CallTimeEvidence, current map[string]metroPoint, now time.Time) {
	if t.Train.CurrentIndex != nil {
		next := *t.Train.CurrentIndex + 1
		t.Train.NextIndex = nil
		if next < len(t.Train.Calls) {
			t.Train.NextIndex = &next
		}
		return
	}
	if prior == nil || *prior < 0 || *prior >= len(t.Train.Calls) || *prior >= len(t.Codes) || previous == nil || !metroCallPredictionFresh(previous, t.Train.Association, now) {
		return
	}
	if _, exists := current[t.Codes[*prior]]; exists {
		return
	}
	if t.Train.NextIndex != nil && *t.Train.NextIndex <= *prior {
		return
	}
	at := previous.At
	t.Train.Calls[*prior].Arrival = api.CallTime{Kind: "prediction", At: &at, Prediction: previous}
	t.Train.NextIndex = ptr(*prior)
}

func metroCallPhases(t *api.MetroTrain) {
	for n := range t.Calls {
		c := &t.Calls[n]
		c.Phase = "unknown"
		if t.NextIndex != nil {
			if n < *t.NextIndex && (c.Arrival.Inferred != nil || c.Arrival.Actual != nil) {
				c.Phase = "previous"
			} else if n >= *t.NextIndex {
				c.Phase = "future"
			}
		} else if t.CurrentIndex != nil && n < *t.CurrentIndex && (c.Arrival.Inferred != nil || c.Arrival.Actual != nil) {
			c.Phase = "previous"
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
