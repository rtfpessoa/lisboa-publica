package app

import (
	"math"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

type metroTrackMovement struct {
	Direction  patterns.MetroDirectionDetector
	Departures map[string]*patterns.MetroDepartureDetector
	Last       map[string]patterns.MetroMovementSample
	Candidates map[string]*patterns.MetroModelDeparture
}

func (r *metroRuntime) trackModel(t *metroTrack) (patterns.MetroQualifiedModel, bool) {
	model, ok := r.models[t.Profile+"|"+t.ProviderDirection]
	if !ok {
		return model, false
	}
	positions := map[string]int{}
	for n, p := range model.Admission.Axis {
		positions[p.Stop] = n
	}
	return model, metroModelPath(positions, t.Codes)
}
func metroModelPath(positions map[string]int, codes []string) bool {
	indices, ok := metroModelIndices(positions, codes)
	if !ok || len(indices) < 2 {
		return false
	}
	step := indices[1] - indices[0]
	if step != 1 && step != -1 {
		return false
	}
	return metroConsecutiveModelIndices(indices, step)
}
func metroModelIndices(positions map[string]int, codes []string) ([]int, bool) {
	indices := []int{}
	for _, code := range codes {
		index, exists := positions[code]
		if !exists {
			return nil, false
		}
		indices = append(indices, index)
	}
	return indices, true
}
func metroConsecutiveModelIndices(indices []int, step int) bool {
	for n := 1; n < len(indices); n++ {
		if indices[n]-indices[n-1] != step {
			return false
		}
	}
	return true
}

func (r *metroRuntime) applyMetroMovement(t *metroTrack, points map[string]metroPoint, now time.Time) {
	t.Train.ModelProjection = nil
	model, ok := r.trackModel(t)
	if !ok || t.Train.Association != "supported" {
		t.Movement = nil
		return
	}
	if t.Movement == nil {
		t.Movement = &metroTrackMovement{Departures: map[string]*patterns.MetroDepartureDetector{}, Last: map[string]patterns.MetroMovementSample{}, Candidates: map[string]*patterns.MetroModelDeparture{}}
	}
	adapter := metroWaitMovementAdapter{track: t, model: model, points: points, now: now}
	for n := range t.Train.Calls {
		sample, projection, ok := adapter.sample(n)
		if !ok {
			continue
		}
		if projection != nil {
			t.Train.ModelProjection = projection
		}
		observeMetroDeparture(t, n, sample, model.Calibration)
		adapter.observeDirection(n, sample)

	}
	publishMetroDepartures(t)
}

// Source station anchors are independent of the interpolated path sign.
func (a metroWaitMovementAdapter) observeDirection(n int, sample patterns.MetroMovementSample) {
	if !sample.Stopped {
		return
	}
	axis := a.axis(a.track.Codes[n])
	calibration := a.model.Calibration
	p := patterns.MetroAxisPosition{Geometry: sample.Geometry, SourceAt: sample.SourceAt, Metres: axis.Metres, NoiseMetres: calibration.NoiseMetres, ResolutionMetres: calibration.ResolutionMetres, Qualified: sample.Valid, DirectionIndependent: true}
	if c := a.track.Movement.Direction.Observe(p); c != nil {
		a.track.Train.DirectionEvidence = &api.MetroDirectionEvidence{State: "confirmed", ConfirmedAt: &c.ConfirmedAt, FirstMovementAt: &c.FirstMovementAt, GeometryVersion: &c.Geometry, Reason: "Três posições de origem; dois deslocamentos coerentes no eixo fixo"}
	}
}

func observeMetroDeparture(t *metroTrack, n int, s patterns.MetroMovementSample, c patterns.MetroMovementCalibration) {
	call := &t.Train.Calls[n]
	d := t.Movement.Departures[call.Id]
	if d == nil {
		d = &patterns.MetroDepartureDetector{Calibration: c}
		t.Movement.Departures[call.Id] = d
	}
	if old, exists := t.Movement.Last[call.Id]; exists && metroMovementCorrection(old, s) {
		withdrawMetroDeparture(t, n, s.SourceAt, "Correção do modelo incompatível com a partida estimada")
		*d = patterns.MetroDepartureDetector{Calibration: c}
		s.Corrected = true
		delete(t.Movement.Candidates, call.Id)
	}
	t.Movement.Last[call.Id] = s
	if e := d.Observe(s); e != nil {
		t.Movement.Candidates[call.Id] = e
	}

}
func publishMetroDepartures(t *metroTrack) {
	if t.Train.DirectionEvidence == nil || t.Train.DirectionEvidence.State != "confirmed" || t.Movement == nil {
		return
	}
	for n := range t.Train.Calls {
		call := &t.Train.Calls[n]
		e := t.Movement.Candidates[call.Id]
		if e == nil || call.Departure.Inferred != nil {
			continue
		}
		evidence := &api.MetroEventEvidence{At: e.At, WindowStart: e.LastStoppedAt, WindowEnd: e.At, Mode: "model_departure", SourceUrl: metroBase + "/tempoEspera/Estacao/todos", ModelVersion: e.ModelVersion, Persistence: "pending", Reason: "Primeiro movimento do modelo após paragem suportada; precisão física não medida"}
		call.Departure = api.CallTime{Kind: "inferred", At: &e.At, Inferred: evidence}
		appendMetroDepartureRevision(call, "estimated", e.At, evidence.Reason, evidence)
		delete(t.Movement.Candidates, call.Id)
	}
}
func metroMovementCorrection(old, s patterns.MetroMovementSample) bool {
	if s.SourceAt.Before(old.SourceAt) {
		return true
	}
	if s.SourceAt.Equal(old.SourceAt) {
		return s.ProgressMetres != old.ProgressMetres
	}
	return s.ProgressMetres < old.ProgressMetres
}
func appendMetroDepartureRevision(c *api.StopCall, status api.MetroDepartureRevisionStatus, at time.Time, reason string, e *api.MetroEventEvidence) {
	revisions := []api.MetroDepartureRevision{}
	if c.DepartureRevisions != nil {
		revisions = append(revisions, (*c.DepartureRevisions)...)
	}
	// Checkpoint size/count admission remains the final bound; refuse unbounded
	// same-visit corrections rather than silently dropping evidence.
	if len(revisions) >= 256 {
		c.Departure = missingCallTime("Histórico de correções excede a capacidade")
		return
	}
	if e != nil {
		copy := *e
		e = &copy
	}
	revisions = append(revisions, api.MetroDepartureRevision{Revision: len(revisions) + 1, Status: status, SourceAt: at, Reason: reason, Evidence: e})
	c.DepartureRevisions = &revisions
}
func withdrawMetroDeparture(t *metroTrack, n int, at time.Time, reason string) {
	c := &t.Train.Calls[n]
	if c.Departure.Inferred == nil {
		return
	}
	appendMetroDepartureRevision(c, "withdrawn", at, reason, c.Departure.Inferred)
	c.Departure = missingCallTime("Estimativa retirada")
}

type metroWaitMovementAdapter struct {
	track  *metroTrack
	model  patterns.MetroQualifiedModel
	points map[string]metroPoint
	now    time.Time
}

func (a metroWaitMovementAdapter) axis(code string) patterns.MetroModelStation {
	for _, p := range a.model.Admission.Axis {
		if p.Stop == code {
			return p
		}
	}
	return patterns.MetroModelStation{}
}
func (a metroWaitMovementAdapter) segmentSeconds(code, target string) float64 {
	for n, p := range a.model.Admission.Axis {
		if p.Stop == code && n+1 < len(a.model.Admission.Axis) && a.model.Admission.Axis[n+1].Stop == target {
			return a.model.Admission.SegmentSeconds[n]
		}
		if p.Stop == target && n+1 < len(a.model.Admission.Axis) && a.model.Admission.Axis[n+1].Stop == code {
			return a.model.Admission.SegmentSeconds[n]
		}
	}
	return 0
}
func (a metroWaitMovementAdapter) sample(n int) (patterns.MetroMovementSample, *api.MetroModelProjection, bool) {
	call := a.track.Train.Calls[n]
	arrival := call.Arrival.Inferred
	if arrival == nil {
		return patterns.MetroMovementSample{}, nil, false
	}
	code := a.track.Codes[n]
	point, exists := a.points[code]
	sample := patterns.MetroMovementSample{Journey: a.track.Train.JourneyId, Visit: call.Id, Geometry: a.model.Calibration.Geometry, Transform: a.model.Calibration.Transform}
	if exists && point.Seconds != nil && *point.Seconds == 0 && !point.Clock.Before(arrival.At) {
		sample.SourceAt = point.Clock
		sample.Valid = a.fresh(point.Clock)
		sample.Stopped = true
		return sample, nil, sample.Valid
	}
	return a.movingSample(n, arrival.At, sample)
}
func (a metroWaitMovementAdapter) movingSample(n int, arrival time.Time, sample patterns.MetroMovementSample) (patterns.MetroMovementSample, *api.MetroModelProjection, bool) {
	if n+1 >= len(a.track.Codes) {
		return sample, nil, false
	}
	code, target := a.track.Codes[n], a.track.Codes[n+1]
	next, exists := a.points[target]
	duration := a.segmentSeconds(code, target)
	if !exists || next.Seconds == nil || duration <= 0 {
		return sample, nil, false
	}
	if !a.movementClock(next.Clock, arrival) {
		return sample, nil, false
	}

	return a.segmentSample(n, next, duration, sample)
}
func (a metroWaitMovementAdapter) segmentSample(n int, next metroPoint, duration float64, sample patterns.MetroMovementSample) (patterns.MetroMovementSample, *api.MetroModelProjection, bool) {
	code, target := a.track.Codes[n], a.track.Codes[n+1]
	fraction := 1 - float64(*next.Seconds)/duration
	if fraction < 0 {
		sample.Corrected = true
		fraction = 0
	}
	if fraction > 1 {
		return sample, nil, false
	}
	from, to := a.axis(code), a.axis(target)
	sample.SourceAt = next.Clock
	sample.Valid = true
	sample.ProgressMetres = math.Abs(to.Metres-from.Metres) * fraction
	if fraction == 1 {
		return sample, nil, true
	}
	return sample, a.projection(metroSegmentProjection{point: next, fraction: fraction, duration: duration, from: from, to: to}), true
}

type metroSegmentProjection struct {
	point              metroPoint
	fraction, duration float64
	from, to           patterns.MetroModelStation
}

func (a metroWaitMovementAdapter) projection(v metroSegmentProjection) *api.MetroModelProjection {
	start := v.point.Clock.Add(-time.Duration(v.fraction * v.duration * float64(time.Second)))
	end := v.point.Clock.Add(time.Duration(*v.point.Seconds) * time.Second)
	return &api.MetroModelProjection{ModelVersion: a.model.Calibration.Version, GeometryVersion: a.model.Calibration.Geometry, SourceUpdatedAt: v.point.Clock, ValidUntil: v.point.Clock.Add(sourceFreshness), FromAt: start, ToAt: end, FromLat: v.from.Lat, FromLon: v.from.Lon, ToLat: v.to.Lat, ToLon: v.to.Lon}
}
func (a metroWaitMovementAdapter) movementClock(clock, arrival time.Time) bool {
	return clock.After(arrival) && clock.Sub(arrival) <= 60*time.Second && a.fresh(clock)
}

func (a metroWaitMovementAdapter) fresh(at time.Time) bool {
	return !at.After(a.now) && a.now.Sub(at) <= 60*time.Second
}
