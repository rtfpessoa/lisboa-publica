package app

import (
	"math"
	"sort"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// These samples describe the publisher's ETA-derived model, never physical GPS.
type metroOperationalSample struct {
	At     time.Time
	Metres float64
}
type metroOperationalMotion struct {
	Context                 string
	Generation              uint64
	BarrierAt               time.Time
	Publications            map[int64]metroModelPublication
	Samples                 []metroOperationalSample
	Axis                    patterns.Pattern
	Sign                    int
	PublishedAt, ReceivedAt time.Time
	Position                hubPosition
}

func (r *metroRuntime) observeHub(positions []hubPosition, received time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hubError, r.hubCorrections = "", nil
	if r.publication == nil || r.plan == nil {
		return
	}
	if r.operational == nil {
		r.operational = map[string]*metroOperationalMotion{}
	}
	routes := map[string]string{}
	for _, route := range r.plan.Routes {
		routes[route.SourceId] = route.Id
	}
	for _, position := range positions {
		r.observeHubPosition(position, routes, received)
	}
	r.expireOperationalMotions(received)
	// Reclassify the retained direct revision without renewing its original clock.
	r.observeSupported(r.publication, r.plan, received)
}
func (r *metroRuntime) observeHubPosition(p hubPosition, routes map[string]string, received time.Time) {
	reference := verifiedHubID(p.ID, "IA2N9")
	if p.Agency != "IA2N9" || !metroReference(reference) {
		return
	}
	route := routes[verifiedHubID(p.Route, "IA2N9")]
	axis := metroOperationalAxis(r.topology, route)
	if len(axis.Stops) < 2 {
		return
	}
	key := route + "|" + reference
	corrected := r.correctHubModel(key, p)
	if !r.validHubModelContext(p) {
		delete(r.operational, key)
		return
	}
	r.observeQualifiedHubPosition(metroHubObservation{key, route, axis, p, received, corrected})
}

type metroHubObservation struct {
	Key, Route string
	Axis       patterns.Pattern
	Position   hubPosition
	Received   time.Time
	Corrected  bool
}

func (r *metroRuntime) observeQualifiedHubPosition(observation metroHubObservation) {
	key, route, axis := observation.Key, observation.Route, observation.Axis
	p, received, corrected := observation.Position, observation.Received, observation.Corrected
	metres, distance := metroGeometryProgress(r.operationalAxisGeometry(route, received), p)
	at := time.UnixMilli(p.At).UTC()
	if !metroHubPositionFresh(at, received, distance) {
		delete(r.operational, key)
		return
	}
	motion := r.operationalMotion(key, axis)
	motion.setContext(metroHubModelContext(p))
	if corrected || !motion.PublishedAt.IsZero() && at.Before(motion.PublishedAt) {
		motion.cutPublication(p, at, received)
		return
	}
	motion.observe(metroOperationalSample{at, metres}, p, received)
}
func metroHubPositionFresh(at, received time.Time, distance float64) bool {
	return distance <= metroGeometryEnvelopeMetres && !at.After(received.Add(providerClockSkew)) && received.Sub(at) <= sourceFreshness
}
func (r *metroRuntime) operationalMotion(key string, axis patterns.Pattern) *metroOperationalMotion {
	motion := r.operational[key]
	if motion == nil || motion.Axis.Direction != axis.Direction || motion.Axis.Route != axis.Route {
		motion = &metroOperationalMotion{Axis: axis}
		r.operational[key] = motion
	}
	return motion
}
func (m *metroOperationalMotion) setContext(context string) {
	if m.Context == context {
		return
	}
	m.resetSamples()
	m.Context = context
}
func (m *metroOperationalMotion) resetSamples() { m.Samples = nil; m.Sign = 0; m.Generation++ }
func (m *metroOperationalMotion) cutPublication(p hubPosition, at, received time.Time) {
	m.BarrierAt = maxMetroTime(m.BarrierAt, maxMetroTime(m.PublishedAt, at))
	m.resetSamples()
	m.Position, m.PublishedAt, m.ReceivedAt = p, at, received
	m.rememberPublication(p)
}
func (r *metroRuntime) expireOperationalMotions(received time.Time) {
	for key, motion := range r.operational {
		if received.Sub(motion.PublishedAt) > sourceFreshness {
			delete(r.operational, key)
		}
	}
}

func metroOperationalAxis(topology patterns.Topology, route string) patterns.Pattern {
	paths := []patterns.Pattern{}
	for _, p := range topology.Patterns {
		if p.Route == route {
			paths = append(paths, p)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		if len(paths[i].Stops) != len(paths[j].Stops) {
			return len(paths[i].Stops) > len(paths[j].Stops)
		}
		return paths[i].Direction < paths[j].Direction
	})
	if len(paths) == 0 {
		return patterns.Pattern{}
	}
	return paths[0]
}

func (m *metroOperationalMotion) observe(s metroOperationalSample, p hubPosition, received time.Time) {
	m.rememberPublication(p)
	if !m.BarrierAt.IsZero() && !s.At.After(m.BarrierAt) {
		return
	}
	if !m.acceptSample(s, p, received) {
		return
	}
	m.Samples = append(m.Samples, s)
	if len(m.Samples) > 3 {
		m.Samples = m.Samples[len(m.Samples)-3:]
	}
	m.Position, m.PublishedAt, m.ReceivedAt = p, s.At, received
	m.Sign = metroOperationalSign(m.Samples)
}
func (m *metroOperationalMotion) acceptSample(s metroOperationalSample, p hubPosition, received time.Time) bool {
	if len(m.Samples) == 0 {
		return true
	}
	old := m.Samples[len(m.Samples)-1]
	if !s.At.After(old.At) {
		if s.At.Before(old.At) || s.Metres != old.Metres {
			m.rejectChangedSample(old, s, p, received)
		}
		return false
	}
	if metroSampleBreaksChain(old, s) {
		m.resetSamples()
	}
	return true
}
func (m *metroOperationalMotion) rejectChangedSample(old, s metroOperationalSample, p hubPosition, received time.Time) {
	m.resetSamples()
	m.BarrierAt = maxMetroTime(m.BarrierAt, maxMetroTime(old.At, s.At))
	m.Position, m.PublishedAt, m.ReceivedAt = p, s.At, received
}
func metroSampleBreaksChain(old, next metroOperationalSample) bool {
	elapsed := next.At.Sub(old.At)
	return elapsed > metroModelChainGap || math.Abs(next.Metres-old.Metres)/elapsed.Seconds() > metroMaximumModelSpeed
}

func metroOperationalSign(samples []metroOperationalSample) int {
	if len(samples) != 3 {
		return 0
	}
	a, b := samples[1].Metres-samples[0].Metres, samples[2].Metres-samples[1].Metres
	if a > metroDirectionStepMetres && b > metroDirectionStepMetres {
		return 1
	}
	if a < -metroDirectionStepMetres && b < -metroDirectionStepMetres {
		return -1
	}
	return 0
}

func (s metroContextSelection) motionChoice(scope string, valid []string) string {
	m := s.runtime.operational[scope]
	if m == nil || m.Sign == 0 || !s.now.Before(m.PublishedAt.Add(sourceFreshness)) {
		return ""
	}
	selected := ""
	for _, key := range valid {
		path := s.batch.paths[key]
		sign := metroPathAxisSign(m.Axis, path)
		if sign == m.Sign {
			if selected != "" {
				return ""
			}
			selected = key
		}
	}
	return selected
}
func metroPathAxisSign(axis, path patterns.Pattern) int {
	indices := map[string]int{}
	for n, code := range axis.Stops {
		indices[code] = n
	}
	a, first := indices[path.Stops[0]]
	b, last := indices[path.Stops[len(path.Stops)-1]]
	if !first || !last {
		return 0
	}
	sign := 0
	if b > a {
		sign = 1
	}
	if b < a {
		sign = -1
	}
	return sign
}

func (r *metroRuntime) operationalEvidence(t *metroTrack, now time.Time) {
	if t.Train.Association != "supported" {
		return
	}
	evidence := &api.MetroDirectionEvidence{State: "estimated", Reason: "Sentido estimado pelo contexto único das previsões; não confirma movimento físico"}
	m := r.operational[t.Train.RouteId+"|"+t.Train.Reference]
	generation := uint64(0)
	if m != nil {
		generation = m.Generation
	}
	if t.ModelMotion != m || t.ModelGeneration != generation {
		resetMetroModelSupport(t, m, generation)
	}
	if metroEvidenceMotionFresh(m, now) {
		projectMetroMotionEvidence(t, m, evidence)
	}
	t.Train.DirectionEvidence = evidence
}
func metroEvidenceMotionFresh(m *metroOperationalMotion, now time.Time) bool {
	return m != nil && len(m.Samples) > 0 && now.Before(m.PublishedAt.Add(sourceFreshness))
}
func projectMetroMotionEvidence(t *metroTrack, m *metroOperationalMotion, evidence *api.MetroDirectionEvidence) {
	published, received := m.PublishedAt, m.ReceivedAt
	evidence.ModelPublishedAt, evidence.ReceivedAt = &published, &received
	if m.Sign == 0 {
		return
	}
	t.FixedModelSign = m.Sign
	t.FixedModelSupport = map[int64]metroModelPublication{}
	for _, sample := range m.Samples {
		if stamp, ok := m.Publications[sample.At.UnixMilli()]; ok {
			t.FixedModelSupport[sample.At.UnixMilli()] = stamp
		}
	}
	first, confirmed := m.Samples[0].At, m.Samples[len(m.Samples)-1].At
	evidence.FirstMovementAt, evidence.ConfirmedAt = &first, &confirmed
	evidence.Reason = "Sentido estimado por três posições do modelo; fonte dependente das previsões"
}

type metroAxisCoordinate struct{ Lat, Lon float64 }

func metroGeometryProgress(geometry [][]float64, p hubPosition) (float64, float64) {
	best, progress, total := math.Inf(1), 0.0, 0.0
	candidates := [][2]float64{}
	for n := 1; n < len(geometry); n++ {
		a, b := geometry[n-1], geometry[n]
		if len(a) != 2 || len(b) != 2 {
			return 0, math.Inf(1)
		}
		length, fraction, distance := metroGeometryEdgeProjection(a, b, p)
		if length == 0 {
			continue
		}
		candidate := total + fraction*length
		candidates = append(candidates, [2]float64{distance, candidate})
		if distance < best {
			best, progress = distance, candidate
		}
		total += length
	}
	if metroGeometryAmbiguous(candidates, best, progress) {
		return 0, math.Inf(1)
	}
	return progress, best
}
func metroGeometryEdgeProjection(a, b []float64, p hubPosition) (float64, float64, float64) {
	scale := 111320 * math.Cos(a[1]*math.Pi/180)
	dx, dy := (b[0]-a[0])*scale, (b[1]-a[1])*111320
	length := math.Hypot(dx, dy)
	if length == 0 {
		return 0, 0, 0
	}
	x, y := (p.Lon-a[0])*scale, (p.Lat-a[1])*111320
	fraction := max(0.0, min(1.0, (x*dx+y*dy)/(length*length)))
	return length, fraction, math.Hypot(x-fraction*dx, y-fraction*dy)
}
func metroGeometryAmbiguous(candidates [][2]float64, best, progress float64) bool {
	for _, candidate := range candidates {
		if candidate[0] <= best+metroGeometryTieMetres && math.Abs(candidate[1]-progress) > metroGeometryAmbiguityMetres {
			return true
		}
	}
	return false
}

func resetMetroModelSupport(t *metroTrack, motion *metroOperationalMotion, generation uint64) {
	t.FixedModelSign = 0
	t.FixedModelSupport = nil
	t.ModelStops = nil
	t.ModelMotion, t.ModelGeneration = motion, generation
}

func maxMetroTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
