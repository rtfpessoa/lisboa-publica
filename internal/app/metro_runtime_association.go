package app

import (
	"crypto/sha256"
	"fmt"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"strconv"
	"strings"
	"time"
)

type metroTrackAdmission struct {
	key    string
	batch  *metroPointBatch
	data   *MetroData
	static *StaticData
	now    time.Time
}

func (r *metroRuntime) admitTrack(v metroTrackAdmission) *metroTrack {
	path := v.batch.paths[v.key]
	if len(path.Stops) > maxMetroPopupVisits {
		r.inventoryOverflow = true
		return nil
	}
	track := r.tracks[r.active[v.key]]
	if v.batch.rejected[v.key] {
		suspendRejectedMetroTrack(track)
		return nil
	}
	track = r.continuingTrack(track, v)
	if track == nil {
		track = r.admitNewEpisode(v, path)
	}
	return track
}
func suspendRejectedMetroTrack(t *metroTrack) {
	if t != nil {
		suspendMetroTrack(t, "Dados incompatíveis nesta direção")
	}
}
func (r *metroRuntime) admitNewEpisode(v metroTrackAdmission, path patterns.Pattern) *metroTrack {
	parts := strings.Split(v.key, "|")
	reference := parts[len(parts)-1]
	scope := path.Route + "|" + reference
	if closed := r.latestClosed(scope); closed != nil && !r.metroNewEpisodeEvidence(v.batch.groups[v.key], closed, path, v.now) {
		return nil
	}
	previous := r.predecessor(scope)
	track := r.startTrack(metroTrackStart{key: v.key, reference: reference, path: path, data: v.data, static: v.static, now: v.now})
	if previous != nil && track != nil && previous != track {
		r.handoffOperational(previous, track, v.now)
	}
	return track
}

func (r *metroRuntime) continuingTrack(track *metroTrack, v metroTrackAdmission) *metroTrack {
	if track == nil {
		return nil
	}
	latest := latestMetroWaitClock(v.batch.episodePoints(v.key), track.Train.SourceUpdatedAt)
	if !metroTrackClosed(track) && latest.Sub(track.Train.SourceUpdatedAt) <= 60*time.Second && track.Profile == r.topology.Profile && v.now.Before(track.Train.ValidUntil) {
		return track
	}
	suspendMetroTrack(track, "Continuidade interrompida")
	if !metroTrackClosed(track) {
		at := v.now
		track.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "superseded", At: &at, Reason: "Continuidade interrompida"}
	}
	r.queueCheckpoint(track)
	delete(r.active, v.key)
	return nil
}

type metroTrackStart struct {
	key, reference string
	path           patterns.Pattern
	data           *MetroData
	static         *StaticData
	now            time.Time
}

func (r *metroRuntime) startTrack(v metroTrackStart) *metroTrack {
	if !r.trackCapacity(v.now) {
		return nil
	}
	r.sequence++
	id := fmt.Sprintf("metro:run:%x", sha256.Sum256([]byte(r.session+strconv.FormatUint(r.sequence, 10))))[:34]
	calls := metroPathCalls(v.path, v.data, v.static, id)
	track := &metroTrack{Train: api.MetroTrain{JourneyId: id, Reference: v.reference, RouteId: v.path.Route, DirectionCode: v.path.Direction, Destination: metroDestinationName(v.data, v.path), Calls: calls, Association: "supported"}, ProviderDirection: v.path.Direction, Profile: r.topology.Profile, Codes: append([]string{}, v.path.Stops...), Points: map[string]metroPoint{}, Proofs: map[string]metroEventProof{}}
	track.Priors = metroSchedulePriors(v.data, v.static, v.path, v.now)
	track.Geometry = metroPathGeometry(v.data, v.static, v.path)
	track.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active", Reason: "Viagem inferida em curso"}
	track.Train.DirectionEvidence = &api.MetroDirectionEvidence{State: "context", Reason: "Direção do percurso admitido; movimento independente por confirmar"}
	track.Train.OriginKnown = ptr(false)
	r.updateTrackDirection(track, v.path)
	r.active[v.key] = id
	r.tracks[id] = track
	return track
}

// Metadata-only corrections can change the containing direction without
// changing the journey identity. Never mutate an in-flight lifecycle barrier.
func (r *metroRuntime) updateTrackDirection(track *metroTrack, path patterns.Pattern) {
	if track.BarrierRevision != 0 {
		return
	}
	track.Train.DirectionCode = canonicalMetroDirection(r.directionTopology, path)
	for n := range track.Train.Calls {
		track.Train.Calls[n].DirectionKey = ptr(track.Train.DirectionCode)
	}
}
func metroDestinationName(data *MetroData, path patterns.Pattern) string {
	name := ""
	for _, s := range data.Stations {
		if s.ID == path.Destination {
			name = s.Name
		}
	}
	return name
}
func (r *metroRuntime) trackCapacity(now time.Time) bool {
	if len(r.tracks) >= 1024 {
		r.prune(now)
		r.evictRetired()
	}
	if len(r.tracks) >= 1024 {
		r.inventoryOverflow = true
		return false
	}
	return true
}
func (r *metroRuntime) suspendAbsent(b *metroPointBatch) {
	for key, id := range r.active {
		r.suspendAbsentTrack(b, key, r.tracks[id], false)
	}
	for key, id := range r.candidates {
		r.suspendAbsentTrack(b, key, r.tracks[id], true)
	}
}

func (r *metroRuntime) suspendAbsentTrack(b *metroPointBatch, key string, track *metroTrack, candidate bool) {
	if track == nil || metroTrackClosed(track) || !candidate && track.BarrierRevision != 0 {
		return
	}
	reason := metroAbsentReason(b, key)
	if reason != "" {
		if candidate {
			r.suspendMetroCandidate(track, reason)
		} else {
			suspendMetroTrack(track, reason)
		}
	}
}

func metroAbsentReason(b *metroPointBatch, key string) string {
	if b.rejected[key] {
		return "Dados incompatíveis nesta direção"
	}
	if _, present := b.groups[key]; !present {
		return "Sem dados atuais para confirmar a viagem"
	}
	return ""
}

func (r *metroRuntime) metroNewEpisodeEvidence(points []metroPoint, closed *metroTrack, path patterns.Pattern, now time.Time) bool {
	fresh := false
	for _, p := range points {
		if p.Clock.After(closed.Train.SourceUpdatedAt) && p.Seconds != nil && *p.Seconds > 0 {
			fresh = true
		}
	}
	if !fresh {
		return false
	}
	if closed.Train.Lifecycle.State != "completed" {
		return true
	}
	m := r.operational[path.Route+"|"+closed.Train.Reference]
	if m == nil || len(m.Samples) != 3 || m.Sign == 0 || m.Sign != metroPathAxisSign(m.Axis, path) || !now.Before(m.PublishedAt.Add(sourceFreshness)) {
		return false
	}
	return m.Samples[0].At.After(closed.Train.SourceUpdatedAt)
}
