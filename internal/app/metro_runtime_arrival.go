package app

import (
	"lisboapublica/internal/api"
	"time"
)

type metroPointProjection struct {
	runtime  *metroRuntime
	track    *metroTrack
	now      time.Time
	earliest *time.Time
}

func (p *metroPointProjection) apply(n int, point metroPoint) bool {
	p.updateClock(point)
	if point.Seconds == nil {
		return true
	}
	if !p.inferArrival(n, point) {
		return false
	}
	p.updateClock(point)
	c := &p.track.Train.Calls[n]
	if *point.Seconds == 0 && c.Arrival.Inferred != nil {
		p.track.Train.CurrentIndex = ptr(n)
	}
	p.predictArrival(n, point)
	return true
}
func (p *metroPointProjection) updateClock(point metroPoint) {
	if point.Clock.After(p.track.Train.SourceUpdatedAt) {
		p.track.Train.SourceUpdatedAt = point.Clock
		p.track.Train.ValidUntil = point.Clock.Add(sourceFreshness)
	}
}
func (p *metroPointProjection) inferArrival(n int, point metroPoint) bool {
	c := &p.track.Train.Calls[n]
	old, ok := p.track.Points[point.Stop]
	if !ok {
		return true
	}
	if point.Clock.Before(old.Clock) || metroPointClockConflict(old, point) {
		suspendMetroTrack(p.track, "Relógio regressivo ou correção incompatível")
		return false
	}
	if metroArrivalTransition(old, point, p.now) && c.Arrival.Inferred == nil {
		evidence := &api.MetroEventEvidence{At: point.Clock, WindowStart: old.Clock, WindowEnd: point.Clock, Mode: "inferred_arrival", SourceUrl: metroBase + "/tempoEspera/Estacao/todos", ModelVersion: metroArrivalModel, Persistence: "pending", Reason: "Transição publicada de espera positiva para zero; hora física aproximada"}
		c.Arrival = api.CallTime{Kind: "inferred", At: &evidence.At, Inferred: evidence}
		p.runtime.queueArrival(p.track, *c, old, point)
	}
	return true
}
func metroArrivalTransition(old, point metroPoint, now time.Time) bool {
	transition := old.Seconds != nil && *old.Seconds > 0 && *point.Seconds == 0
	clock := point.Clock.After(old.Clock) && point.Clock.Sub(old.Clock) <= 60*time.Second && !point.Clock.After(now)
	return transition && clock
}
func (p *metroPointProjection) predictArrival(n int, point metroPoint) {
	c := &p.track.Train.Calls[n]
	expected := point.Clock.Add(time.Duration(*point.Seconds) * time.Second)
	expiry := point.Clock.Add(sourceFreshness)
	if c.Arrival.Inferred == nil && !expected.Before(p.now) && expiry.After(p.now) {
		c.Arrival = api.CallTime{Kind: "prediction", At: &expected, Prediction: &api.CallTimeEvidence{At: expected, SourceUrl: metroBase + "/tempoEspera/Estacao/todos", SourceUpdatedAt: &point.Clock, ValidUntil: &expiry}}
	}
	if p.nextCandidate(point, expected, expiry) {
		p.earliest = &expected
		p.track.Train.NextIndex = ptr(n)
	}
}
func (p *metroPointProjection) nextCandidate(point metroPoint, expected, expiry time.Time) bool {
	fresh := expiry.After(p.now) && !expected.Before(p.now)
	earliest := p.earliest == nil || expected.Before(*p.earliest)
	return *point.Seconds >= 0 && fresh && earliest
}
