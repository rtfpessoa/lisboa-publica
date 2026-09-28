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
	key, b, data, static, now := v.key, v.batch, v.data, v.static, v.now
	path := b.paths[key]
	if len(path.Stops) > maxMetroPopupVisits {
		r.inventoryOverflow = true
		return nil
	}
	parts := strings.Split(key, "|")
	ref := parts[len(parts)-1]
	track := r.tracks[r.active[key]]
	if b.rejected[key] {
		if track != nil {
			suspendMetroTrack(track, "Dados incompatíveis nesta direção")
		}
		return nil
	}
	track = r.continuingTrack(track, v)

	if track == nil && r.latestClosed(path.Route+"|"+ref) != nil {
		return nil
	}
	if track == nil {
		track = r.startTrack(metroTrackStart{key: key, reference: ref, path: path, data: data, static: static, now: now})
	}
	return track
}

func (r *metroRuntime) continuingTrack(track *metroTrack, v metroTrackAdmission) *metroTrack {
	if track == nil {
		return nil
	}
	if track.Profile == r.topology.Profile && !v.now.After(track.Train.ValidUntil) {
		return track
	}
	suspendMetroTrack(track, "Continuidade interrompida")
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
	track.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "active", Reason: "Viagem inferida em curso"}
	track.Train.DirectionEvidence = &api.MetroDirectionEvidence{State: "context", Reason: "Direção do percurso admitido; movimento independente por confirmar"}
	track.Train.DirectionCode = canonicalMetroDirection(r.topology, v.path)
	for n := range track.Train.Calls {
		track.Train.Calls[n].DirectionKey = ptr(track.Train.DirectionCode)
	}
	r.active[v.key] = id
	r.tracks[id] = track
	return track
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
		track := r.tracks[id]
		if track == nil || track.BarrierRevision != 0 || metroTrackClosed(track) {
			continue
		}
		if _, ok := b.groups[key]; !ok {
			suspendMetroTrack(track, "Sem dados atuais para confirmar a viagem")
		}
		if b.rejected[key] {
			suspendMetroTrack(track, "Dados incompatíveis nesta direção")
		}
	}
}
