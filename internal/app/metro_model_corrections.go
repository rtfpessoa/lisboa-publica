package app

import (
	"sort"
	"strconv"
	"time"
)

func metroHubModelContext(p hubPosition) string {
	return p.Trip + "|" + p.Shape + "|" + p.Pattern + "|" + strconv.Itoa(metroPublishedDirection(p.Direction))
}

// A same/older-clock replacement is a correction, not a new movement sample.
// Only occurrences using that corrected clock lose support; later gaps leave
// earlier frozen occurrence estimates intact.
func (r *metroRuntime) correctHubModel(key string, p hubPosition) bool {
	stamp := metroPublicationStamp(p)
	corrected := metroKnownPublicationChanged(r.operational[key], p.At, stamp)
	for _, track := range r.tracks {
		if track.Train.RouteId+"|"+track.Train.Reference != key {
			continue
		}
		if withdrawCorrectedMetroDepartures(track, p.At, stamp) {
			corrected = true
		}
	}
	if corrected {
		r.captureHubCorrection(p)
	}
	return corrected
}
func metroKnownPublicationChanged(m *metroOperationalMotion, clock int64, stamp metroModelPublication) bool {
	if m == nil {
		return false
	}
	original, known := m.Publications[clock]
	return known && original != stamp
}
func withdrawCorrectedMetroDepartures(t *metroTrack, clock int64, stamp metroModelPublication) bool {
	corrected := false
	for n := range t.Train.Calls {
		call := &t.Train.Calls[n]
		original, known := t.ModelDepartureSupport[call.Id][clock]
		if !known || original == stamp || call.Departure.Inferred == nil {
			continue
		}
		corrected = true
		withdrawMetroDeparture(t, n, time.UnixMilli(clock).UTC(), "Estimativa retirada: publicação original de suporte corrigida")
		t.HistoricalCorrection = true
	}
	return corrected
}
func (r *metroRuntime) captureHubCorrection(p hubPosition) {
	if len(r.hubCorrections) < 64 {
		r.hubCorrections = append(r.hubCorrections, p)
	} else {
		r.markCaptureGap()
	}
}

type metroModelPublication struct {
	Lat, Lon float64
	Context  string
}

func metroPublicationStamp(p hubPosition) metroModelPublication {
	return metroModelPublication{p.Lat, p.Lon, metroHubModelContext(p)}
}

// A bounded clock-indexed revision dictionary distinguishes delayed identical
// publications from actual corrections. Unknown older rows cut prospective
// continuity but cannot contradict a retained historical occurrence.
func (m *metroOperationalMotion) rememberPublication(p hubPosition) {
	if m.Publications == nil {
		m.Publications = map[int64]metroModelPublication{}
	}
	m.Publications[p.At] = metroPublicationStamp(p)
	if len(m.Publications) <= 64 {
		return
	}
	clocks := make([]int64, 0, len(m.Publications))
	for clock := range m.Publications {
		clocks = append(clocks, clock)
	}
	sort.Slice(clocks, func(i, j int) bool { return clocks[i] < clocks[j] })
	delete(m.Publications, clocks[0])
}
