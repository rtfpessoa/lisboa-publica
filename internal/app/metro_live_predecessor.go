package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

const (
	metroPredecessorCacheLimit = 64
	metroPredecessorWorkers    = 4
	metroPredecessorTimeout    = 5 * time.Second
)

type metroPredecessorKey struct{ selected, previous, profile string }
type metroPredecessorCacheEntry struct {
	payload                 *metroCheckpointPayload
	record                  patterns.MetroJourneyCheckpoint
	status                  api.MetroPredecessorHistoryStatus
	retry, started, touched time.Time
	attempts                int
	generation              uint64
	inflight                bool
}

// A cached disk snapshot must never outlive a newer in-memory correction.
// Called only under the runtime owner lock.
func (r *metroRuntime) invalidateMetroPredecessor(journey string) {
	for key, entry := range r.predecessorCache {
		if key.previous != journey {
			continue
		}
		entry.generation++
		entry.payload = nil
		entry.retry = time.Time{}
	}
}

func (b *metroFrameBuilder) projectPredecessor() {
	selected, previousID := b.selectedPredecessor()
	if selected == nil {
		return
	}
	result := &api.MetroPredecessorHistory{JourneyId: previousID, Status: "unavailable", Reason: "Episódio anterior indisponível; ligação física não comprovada"}
	b.frame.Predecessor = result
	r := b.server.Cache.metroRuntime
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.tracks[selected.JourneyId]
	// Do not attach newer lineage to an older frame snapshot.
	if current == nil || !current.Train.SourceUpdatedAt.Equal(selected.SourceUpdatedAt) || !reflect.DeepEqual(current.Train.Lifecycle, selected.Lifecycle) {
		return
	}
	if prior := r.tracks[previousID]; prior != nil {
		b.projectHotPredecessor(current, prior, result)
		return
	}
	b.projectColdPredecessor(r, current, previousID, result)
}

func (b *metroFrameBuilder) selectedPredecessor() (*api.MetroTrain, string) {
	selectedID := textValue(b.frame.SelectedJourneyId)
	if selectedID == "" {
		return nil, ""
	}
	for n := range b.frame.Trains {
		selected := &b.frame.Trains[n]
		if selected.JourneyId != selectedID {
			continue
		}
		if selected.Lifecycle == nil || selected.Lifecycle.PredecessorJourneyId == nil {
			return nil, ""
		}
		previousID := *selected.Lifecycle.PredecessorJourneyId
		if previousID == "" || previousID == selectedID {
			return nil, ""
		}
		return selected, previousID
	}
	return nil, ""
}

func (b *metroFrameBuilder) projectHotPredecessor(current, prior *metroTrack, result *api.MetroPredecessorHistory) {
	if prior.Train.SourceUpdatedAt.Before(b.now.AddDate(0, 0, -7)) {
		result.Status, result.Reason = "expired", "Histórico do episódio anterior expirado"
		return
	}
	if metroCompatiblePredecessor(current, prior, b.now) {
		train := predecessorTrain(prior, b.now)
		result.Train, result.Status, result.Reason = &train, "available", "Episódio anterior estimado; a ligação não identifica a mesma unidade física"
	}
}

func (b *metroFrameBuilder) projectColdPredecessor(r *metroRuntime, current *metroTrack, previousID string, result *api.MetroPredecessorHistory) {
	if b.server.Patterns == nil {
		return
	}
	key := metroPredecessorKey{current.Train.JourneyId, previousID, current.Profile}
	entry := r.predecessorCacheEntry(key, b.now)
	if entry == nil || b.projectCachedPredecessor(current, entry, result) {
		return
	}
	if !b.predecessorLookupDue(entry, result) || r.predecessorWorkers >= metroPredecessorWorkers {
		return
	}
	entry.inflight, entry.started = true, b.now
	r.predecessorWorkers++
	generation := entry.generation
	result.Status, result.Reason = "pending", "A recuperar o episódio anterior"
	parent := b.interest.streamContext
	if parent == nil {
		parent = context.Background()
	}
	go r.readMetroPredecessor(parent, key, entry, generation, b.server.Patterns.MetroJourneyCheckpoint)
}

func (b *metroFrameBuilder) projectCachedPredecessor(current *metroTrack, entry *metroPredecessorCacheEntry, result *api.MetroPredecessorHistory) bool {
	if entry.payload == nil {
		return false
	}
	prior := &metroTrack{Train: entry.payload.Train, Profile: entry.payload.Profile, ProviderDirection: entry.payload.ProviderDirection, Codes: entry.payload.Codes, Revision: entry.record.Revision, CommittedRevision: entry.record.Revision, Generation: entry.record.Generation, CommittedAt: entry.record.CommittedAt, HistoricalOnly: true}
	if metroCompatiblePredecessor(current, prior, b.now) {
		train := predecessorTrain(prior, b.now)
		result.Train, result.Status, result.Reason = &train, "available", "Histórico guardado do episódio anterior estimado; ligação física não comprovada"
		return true
	}
	entry.payload = nil
	entry.status, entry.retry = "unavailable", b.now.Add(time.Minute)
	if prior.Train.SourceUpdatedAt.Before(b.now.AddDate(0, 0, -7)) {
		entry.status = "expired"
	}
	return false
}

func (b *metroFrameBuilder) predecessorLookupDue(entry *metroPredecessorCacheEntry, result *api.MetroPredecessorHistory) bool {
	if entry.inflight {
		if b.now.Before(entry.started.Add(metroPredecessorTimeout)) {
			result.Status, result.Reason = "pending", "A recuperar o episódio anterior"
		}
		return false
	}
	if !b.now.Before(entry.retry) {
		return true
	}
	result.Status = entry.status
	if entry.status == "expired" {
		result.Reason = "Histórico do episódio anterior expirado"
	} else if entry.status == "corrupt" {
		result.Reason = "Histórico do episódio anterior não verificável"
	}
	return false
}

func (r *metroRuntime) predecessorCacheEntry(key metroPredecessorKey, now time.Time) *metroPredecessorCacheEntry {
	if r.predecessorCache == nil {
		r.predecessorCache = map[metroPredecessorKey]*metroPredecessorCacheEntry{}
	}
	if entry := r.predecessorCache[key]; entry != nil {
		entry.touched = now
		return entry
	}
	if len(r.predecessorCache) >= metroPredecessorCacheLimit {
		var oldestKey metroPredecessorKey
		var oldest *metroPredecessorCacheEntry
		for k, e := range r.predecessorCache {
			if !e.inflight && (oldest == nil || e.touched.Before(oldest.touched)) {
				oldestKey, oldest = k, e
			}
		}
		if oldest == nil {
			return nil
		}
		delete(r.predecessorCache, oldestKey)
	}
	entry := &metroPredecessorCacheEntry{status: "unavailable", touched: now}
	r.predecessorCache[key] = entry
	return entry
}

func (r *metroRuntime) readMetroPredecessor(parent context.Context, key metroPredecessorKey, entry *metroPredecessorCacheEntry, generation uint64, lookup func(context.Context, string, time.Time) (patterns.MetroJourneyCheckpoint, string, error)) {
	ctx, cancel := context.WithDeadline(parent, entry.started.Add(metroPredecessorTimeout))
	defer cancel()
	record, status, err := lookup(ctx, key.previous, time.Now().UTC())
	finished := time.Now().UTC()
	payload := decodeMetroPredecessor(record, status, err, key.previous)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.predecessorWorkers--
	if r.predecessorCache[key] != entry {
		return
	}
	entry.inflight = false
	if entry.generation != generation {
		entry.retry = time.Time{}
		return
	}
	if ctx.Err() != nil || finished.After(entry.started.Add(metroPredecessorTimeout)) {
		err = context.DeadlineExceeded
		payload = nil
	}
	if payload != nil {
		entry.payload, entry.record, entry.status, entry.retry, entry.attempts = payload, record, "available", time.Time{}, 0
		return
	}
	finishMetroPredecessorFailure(entry, parent, status, err, finished)
}

func decodeMetroPredecessor(record patterns.MetroJourneyCheckpoint, status string, err error, journey string) *metroCheckpointPayload {
	if err != nil || status != "restored" || len(record.Payload) > 256<<10 {
		return nil
	}
	var payload metroCheckpointPayload
	if json.Unmarshal(record.Payload, &payload) != nil || payload.Version != 1 || payload.Train.JourneyId != journey {
		return nil
	}
	if !payload.Train.SourceUpdatedAt.Equal(record.SourceAt) || len(payload.Train.Calls) > maxMetroPopupVisits {
		return nil
	}
	return &payload
}

func finishMetroPredecessorFailure(entry *metroPredecessorCacheEntry, parent context.Context, status string, err error, finished time.Time) {
	entry.payload = nil
	entry.status, entry.retry = "unavailable", finished.Add(time.Minute)
	if errors.Is(parent.Err(), context.Canceled) {
		entry.retry = time.Time{}
		return
	}
	if status == "expired" {
		entry.status = "expired"
	} else if status == "corrupt" || err == nil && status == "restored" {
		entry.status = "corrupt"
	} else if err != nil {
		entry.attempts++
		delay := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}
		i := entry.attempts - 1
		if i >= len(delay) {
			i = len(delay) - 1
		}
		entry.retry = finished.Add(delay[i])
	}
}

// Matching is deliberately strict: unique full ordered paths only. A lifecycle
// edge is an estimated association, not a certificate of physical identity.
func metroCompatiblePredecessor(selected, prior *metroTrack, now time.Time) bool {
	if selected == nil || prior == nil {
		return false
	}
	return metroPredecessorIdentityMatches(selected, prior, now) && metroPredecessorLinkMatches(selected, prior) && metroPredecessorPathMatches(selected, prior)
}

func metroPredecessorIdentityMatches(selected, prior *metroTrack, now time.Time) bool {
	return metroPredecessorProfileMatches(selected, prior) && metroPredecessorTrainMatches(selected.Train, prior.Train) && metroPredecessorClockValid(prior.Train.SourceUpdatedAt, now)
}

func metroPredecessorProfileMatches(selected, prior *metroTrack) bool {
	return selected.Profile != "" && selected.Profile == prior.Profile && selected.ProviderDirection == prior.ProviderDirection
}

func metroPredecessorTrainMatches(selected, prior api.MetroTrain) bool {
	return selected.RouteId == prior.RouteId && selected.Reference == prior.Reference && selected.DirectionCode == prior.DirectionCode && selected.Destination == prior.Destination
}

func metroPredecessorClockValid(clock, now time.Time) bool {
	return !clock.IsZero() && !clock.Before(now.AddDate(0, 0, -7)) && !clock.After(now.Add(5*time.Minute))
}

func metroPredecessorLinkMatches(selected, prior *metroTrack) bool {
	if prior.BarrierBefore != nil || prior.Train.Lifecycle == nil || prior.Train.Lifecycle.SuccessorJourneyId == nil {
		return false
	}
	return *prior.Train.Lifecycle.SuccessorJourneyId == selected.Train.JourneyId
}

func metroPredecessorPathMatches(selected, prior *metroTrack) bool {
	if len(selected.Codes) == 0 || len(selected.Codes) != len(prior.Codes) || len(prior.Train.Calls) != len(prior.Codes) || len(selected.Train.Calls) != len(selected.Codes) || !reflect.DeepEqual(selected.Codes, prior.Codes) {
		return false
	}
	seen := map[string]bool{}
	for n, code := range prior.Codes {
		if code == "" || seen[code] || !metroPredecessorVisitMatches(selected.Train.Calls[n], prior.Train.Calls[n], n) {
			return false
		}
		seen[code] = true
	}
	return true
}

func metroPredecessorVisitMatches(selected, prior api.StopCall, n int) bool {
	return prior.StopSequence == n && selected.StopSequence == n && prior.StopId != "" && prior.StopId == selected.StopId
}

func predecessorTrain(t *metroTrack, now time.Time) api.MetroTrain {
	train := visibleMetroTrain(t)
	train.Association, train.NextIndex, train.CurrentIndex, train.VehicleId, train.ModelProjection = "suspended", nil, nil, nil, nil
	train.Reason = "Episódio anterior estimado; continuidade física não comprovada"
	train.DirectionEvidence = &api.MetroDirectionEvidence{State: "unknown", Reason: train.Reason}
	train.Persistence = metroTrainPersistence(t)
	for n := range train.Calls {
		c := &train.Calls[n]
		c.Phase, c.OwnPrediction, c.OwnDeparturePrediction, c.LastOfficialEstimate, c.VehicleRef = "unknown", nil, nil, nil, nil
		c.Arrival = predecessorCallTime(c.Arrival)
		c.Departure = predecessorCallTime(c.Departure)
		if t.HistoricalOnly {
			if c.Arrival.Inferred != nil {
				c.Arrival.Inferred.Persistence = "committed"
			}
			if c.Departure.Inferred != nil {
				c.Departure.Inferred.Persistence = "committed"
			}
		}
	}
	return train
}

// Preserve explicit retractions, while never exposing a prior episode's live
// prediction or schedule as a current passage.
func predecessorCallTime(value api.CallTime) api.CallTime {
	if value.Actual != nil {
		at := value.Actual.At
		return api.CallTime{Kind: "actual", At: &at, Actual: value.Actual, Reason: value.Reason}
	}
	if value.Inferred != nil {
		at := value.Inferred.At
		return api.CallTime{Kind: "inferred", At: &at, Inferred: value.Inferred, Reason: value.Reason}
	}
	if value.Kind == "unavailable" && value.Reason != "" {
		return missingCallTime(value.Reason)
	}
	return missingCallTime("Sem passagem confirmada")
}
