package app

import (
	"math"
	"strconv"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func metroPathGeometry(data *MetroData, static *StaticData, path patterns.Pattern) [][][]float64 {
	out := make([][][]float64, len(path.Stops))
	b := metroPlanBuilder{data: data, static: static, stations: map[string]MetroStation{}, mapping: map[string]string{}, routes: map[string]string{}}
	b.addStations()
	b.mapStaticPlan()
	for _, trip := range static.Schedule.Trips {
		visits := localJourneyTimes(&trip)
		if b.routes[trip.Route] != path.Route || !metroPriorPathMatches(&b, visits, path) {
			continue
		}
		geometry := uniqueMetroTripGeometry(static, trip)
		progress, valid := metroGeometryVisitProgress(geometry, visits, plannedStationCoordinates(static))
		if !valid {
			continue
		}
		for n := 0; n+1 < len(visits); n++ {
			out[n] = metroGeometrySlice(geometry, progress[n], progress[n+1])
		}
		break
	}
	return out
}
func metroGeometryVisitProgress(geometry [][]float64, visits []StopTime, stations map[string][2]float64) ([]float64, bool) {
	progress := make([]float64, len(visits))
	if len(geometry) < 2 {
		return progress, false
	}
	for n, visit := range visits {
		coordinate, exists := stations[visit.Stop]
		metres, distance := metroGeometryProgress(geometry, hubPosition{Lat: coordinate[1], Lon: coordinate[0]})
		if !exists || !metroVisitProjectionOrdered(progress, n, metres, distance) {
			return progress, false
		}
		progress[n] = metres
	}
	return progress, true
}

func metroVisitProjectionOrdered(progress []float64, index int, metres, distance float64) bool {
	return distance <= metroGeometryEnvelopeMetres && (index == 0 || metres > progress[index-1])
}

func (r *metroRuntime) projectOperationalSegment(t *metroTrack, now time.Time) {
	if !metroSegmentTrackReady(t) {
		return
	}
	next := *t.Train.NextIndex
	if !metroSegmentIndexReady(t, next) {
		return
	}
	point, exists := t.Points[t.Codes[next]]
	projection := buildMetroSegmentProjection(t, point, exists, next, now)
	if projection != nil {
		t.Train.ModelProjection = projection
	}
}
func metroSegmentTrackReady(t *metroTrack) bool {
	return t.Train.Association == "supported" && t.Train.CurrentIndex == nil && t.Train.NextIndex != nil
}
func metroSegmentIndexReady(t *metroTrack, next int) bool {
	return next > 0 && next < len(t.Priors) && next-1 < len(t.Geometry)
}
func metroSegmentPointReady(point metroPoint, exists bool, duration int, geometry [][]float64) bool {
	if !exists || point.Seconds == nil {
		return false
	}
	return *point.Seconds > 0 && duration > 0 && *point.Seconds <= duration && len(geometry) >= 2
}
func buildMetroSegmentProjection(t *metroTrack, point metroPoint, exists bool, next int, now time.Time) *api.MetroModelProjection {
	duration, geometry := t.Priors[next-1].Run, t.Geometry[next-1]
	if !metroSegmentPointReady(point, exists, duration, geometry) {
		return nil
	}
	to := point.Clock.Add(time.Duration(*point.Seconds) * time.Second)
	from := to.Add(-time.Duration(duration) * time.Second)
	expiry := point.Clock.Add(metroProjectionLifetime)
	if !metroSegmentTimeReady(now, from, to, expiry) {
		return nil
	}
	return metroSegmentGeometryProjection(t, metroSegmentRendering{point, geometry, now, from, to, expiry})
}
func metroSegmentTimeReady(now, from, to, expiry time.Time) bool {
	return now.Before(expiry) && !now.Before(from) && now.Before(to)
}

type metroSegmentRendering struct {
	Point                 metroPoint
	Geometry              [][]float64
	Now, From, To, Expiry time.Time
}

func metroSegmentGeometryProjection(t *metroTrack, rendering metroSegmentRendering) *api.MetroModelProjection {
	point, geometry := rendering.Point, rendering.Geometry
	now, from, to, expiry := rendering.Now, rendering.From, rendering.To, rendering.Expiry
	a, b := geometry[0], geometry[len(geometry)-1]
	if len(a) != 2 || len(b) != 2 {
		return nil
	}
	uncertainty := float32(25 + max(0, now.Sub(point.Clock).Seconds())*25)
	return &api.MetroModelProjection{ModelVersion: "metro-schedule-segment-v1", GeometryVersion: t.Profile, SourceUpdatedAt: point.Clock, ValidUntil: minTime(expiry, to), FromAt: from, ToAt: to, FromLat: a[1], FromLon: a[0], ToLat: b[1], ToLon: b[0], Geometry: &geometry, UncertaintyMetres: &uncertainty}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Departure is a model event derived from the first newer positive progress
// after an original positive-to-zero arrival window, not physical motion.
type metroModelStop struct {
	ModelAt, DirectAt time.Time
	Metres            float64
}

func (r *metroRuntime) projectMetroExperimentalDepartures(t *metroTrack, now time.Time) {
	if !metroCurrentDirection(t.Train, now) {
		return
	}
	m := r.operational[t.Train.RouteId+"|"+t.Train.Reference]
	if m == nil || len(m.Samples) == 0 || m.PublishedAt.After(now) || !now.Before(m.PublishedAt.Add(sourceFreshness)) {
		return
	}
	if t.ModelStops == nil {
		t.ModelStops = map[string]metroModelStop{}
	}
	for n := 0; n+1 < len(t.Train.Calls); n++ {
		r.projectMetroModelDeparture(t, m, n, now)
	}
}
func (r *metroRuntime) projectMetroModelDeparture(t *metroTrack, m *metroOperationalMotion, n int, now time.Time) {
	call := &t.Train.Calls[n]
	if call.Arrival.Inferred == nil || call.Departure.Inferred != nil {
		return
	}
	stopped, exists := t.Points[t.Codes[n]]
	if metroPointAtStop(stopped, exists) {
		r.anchorMetroModelStop(t, m, n, stopped, now)
		return
	}
	anchor, anchored := t.ModelStops[call.Id]
	next, exists := t.Points[t.Codes[n+1]]
	if !metroDepartureProgressReady(t, m, metroDepartureProgress{anchor, anchored, next, exists, now}) {
		return
	}
	first := metroFirstDepartureSample(m, anchor, t.FixedModelSign)
	if !first.IsZero() {
		recordMetroModelDeparture(t, m, call, metroDepartureOccurrence{anchor, next, first})
	}
}
func metroPointAtStop(point metroPoint, exists bool) bool {
	return exists && point.Seconds != nil && *point.Seconds == 0
}
func (r *metroRuntime) anchorMetroModelStop(t *metroTrack, m *metroOperationalMotion, n int, stopped metroPoint, now time.Time) {
	call := &t.Train.Calls[n]
	station := uniqueMetroCode(r.publication, t.Codes[n])
	old := t.ModelStops[call.Id]
	if station == nil || !stopped.Clock.After(old.DirectAt) || m.PublishedAt.Before(call.Arrival.Inferred.WindowEnd) {
		return
	}
	coordinate, valid := metroStationCoordinate(*station)
	stationProgress, distance := metroGeometryProgress(r.operationalAxisGeometry(t.Train.RouteId, now), hubPosition{Lat: coordinate.Lat, Lon: coordinate.Lon})
	progress := m.Samples[len(m.Samples)-1].Metres
	if valid && distance <= metroGeometryEnvelopeMetres && math.Abs(stationProgress-progress) <= metroStopEnvelopeMetres {
		t.ModelStops[call.Id] = metroModelStop{m.PublishedAt, stopped.Clock, progress}
	}
}

type metroDepartureProgress struct {
	Anchor   metroModelStop
	Anchored bool
	Next     metroPoint
	Exists   bool
	Now      time.Time
}

func metroDepartureProgressReady(t *metroTrack, m *metroOperationalMotion, progress metroDepartureProgress) bool {
	anchor, anchored, next, exists, now := progress.Anchor, progress.Anchored, progress.Next, progress.Exists, progress.Now
	if !anchored || !metroDeparturePointReady(next, exists, anchor.DirectAt, now) {
		return false
	}
	return metroDepartureDirectionReady(t, m) && m.PublishedAt.Sub(anchor.ModelAt) <= metroModelChainGap
}
func metroDeparturePointReady(next metroPoint, exists bool, anchor, now time.Time) bool {
	if !exists || next.Seconds == nil {
		return false
	}
	return !next.Clock.After(now) && next.Clock.After(anchor) && *next.Seconds > 0
}
func metroDepartureDirectionReady(t *metroTrack, m *metroOperationalMotion) bool {
	if len(m.Samples) == 0 || t.FixedModelSign == 0 {
		return false
	}
	return t.FixedModelSign == metroPathAxisSign(m.Axis, patterns.Pattern{Stops: t.Codes}) && (m.Sign == 0 || m.Sign == t.FixedModelSign)
}
func metroFirstDepartureSample(m *metroOperationalMotion, anchor metroModelStop, sign int) time.Time {
	for _, sample := range m.Samples {
		if sample.At.After(anchor.ModelAt) && (sample.Metres-anchor.Metres)*float64(sign) > metroDepartureStepMetres {
			return sample.At
		}
	}
	return time.Time{}
}

type metroDepartureOccurrence struct {
	Anchor metroModelStop
	Next   metroPoint
	First  time.Time
}

func recordMetroModelDeparture(t *metroTrack, m *metroOperationalMotion, call *api.StopCall, occurrence metroDepartureOccurrence) {
	anchor, next, first := occurrence.Anchor, occurrence.Next, occurrence.First
	confirmation := m.PublishedAt
	evidence := metroModelDepartureEvidence(t, anchor, first, confirmation)
	call.Departure = api.CallTime{Kind: "inferred", At: &first, Inferred: evidence}
	if t.ModelDepartureSupport == nil {
		t.ModelDepartureSupport = map[string]map[int64]metroModelPublication{}
	}
	t.ModelDepartureSupport[call.Id] = metroDepartureSupport(t, m, anchor, first, confirmation)
	appendMetroDepartureRevision(call, "estimated", next.Clock, evidence.Reason, evidence)
}
func metroModelDepartureEvidence(t *metroTrack, anchor metroModelStop, first, confirmation time.Time) *api.MetroEventEvidence {
	return &api.MetroEventEvidence{At: first, WindowStart: anchor.ModelAt, WindowEnd: first, ConfirmedAt: &confirmation, Mode: "model_departure", SourceUrl: hubBase + "/vehicles/positions", ModelVersion: metroOperationalPolicy + ":" + t.Profile, Persistence: "pending", Reason: "Primeiro deslocamento claro do modelo após paragem suportada; sentido previamente suportado por três posições; precisão física não medida"}
}
func metroDepartureSupport(t *metroTrack, m *metroOperationalMotion, anchor metroModelStop, first, confirmation time.Time) map[int64]metroModelPublication {
	support := map[int64]metroModelPublication{}
	for clock, stamp := range t.FixedModelSupport {
		support[clock] = stamp
	}
	for _, at := range []time.Time{anchor.ModelAt, first, confirmation} {
		if stamp, ok := m.Publications[at.UnixMilli()]; ok {
			support[at.UnixMilli()] = stamp
		}
	}
	return support
}

func metroStationCoordinate(s MetroStation) (metroAxisCoordinate, bool) {
	lat, a := strconv.ParseFloat(s.Lat, 64)
	lon, b := strconv.ParseFloat(s.Lon, 64)
	return metroAxisCoordinate{lat, lon}, a == nil && b == nil
}

// Station anchors may lie inside simplified shape edges. Requiring a retained
// vertex at each station would discard otherwise valid published geometry.
func metroGeometrySlice(geometry [][]float64, from, to float64) [][]float64 {
	out := [][]float64{}
	total := 0.0
	for n := 1; n < len(geometry); n++ {
		a, b := geometry[n-1], geometry[n]
		length := segmentPointDistance(b, [2]float64{a[0], a[1]})
		end := total + length
		if length > 0 && end >= from && total <= to {
			startFraction := max(0.0, (from-total)/length)
			endFraction := min(1.0, (to-total)/length)
			if len(out) == 0 {
				out = append(out, []float64{a[0] + (b[0]-a[0])*startFraction, a[1] + (b[1]-a[1])*startFraction})
			}
			if endFraction >= startFraction {
				out = append(out, []float64{a[0] + (b[0]-a[0])*endFraction, a[1] + (b[1]-a[1])*endFraction})
			}
		}
		total = end
	}
	return out
}
