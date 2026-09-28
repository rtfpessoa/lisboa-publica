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
			suspendMetroTrack(track, "Referência ou plataforma contraditória")
		}
		return nil
	}
	r.retireReturnRuns(key, ref)
	if track != nil && (track.Profile != r.topology.Profile || now.After(track.Train.ValidUntil)) {
		suspendMetroTrack(track, "Continuidade interrompida")
		delete(r.active, key)
		track = nil
	}
	if track == nil {
		track = r.startTrack(metroTrackStart{key: key, reference: ref, path: path, data: data, static: static, now: now})
	}
	return track
}
func (r *metroRuntime) retireReturnRuns(key, ref string) {
	for oldKey, oldID := range r.active {
		old := r.tracks[oldID]
		if old != nil && old.Train.Reference == ref && oldKey != key {
			suspendMetroTrack(old, "Outro sentido publicado; selecione a nova viagem explicitamente")
			delete(r.active, oldKey)
		}
	}
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
	track := &metroTrack{Train: api.MetroTrain{JourneyId: id, Reference: v.reference, RouteId: v.path.Route, DirectionCode: v.path.Direction, Destination: metroDestinationName(v.data, v.path), Calls: calls, Association: "supported"}, ProviderDirection: v.path.Direction, Profile: r.topology.Profile, Codes: append([]string{}, v.path.Stops...), Points: map[string]metroPoint{}}
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
		if track == nil {
			continue
		}
		if _, ok := b.groups[key]; !ok {
			suspendMetroTrack(track, "Referência sem suporte atual")
		}
		if b.rejected[key] {
			suspendMetroTrack(track, "Referência contraditória")
		}
	}
}
