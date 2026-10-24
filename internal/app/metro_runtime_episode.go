package app

import (
	"slices"
	"time"

	"lisboapublica/internal/api"
)

// A point before a known passage must not renew the episode without a
// supported downstream anchor. Known visits retain separate correction handling.
func metroEpisodeHasUpstream(t *metroTrack, current map[string]metroPoint) bool {
	if t == nil {
		return false
	}
	if len(t.Codes) != len(t.Train.Calls) {
		return true
	}
	bound := -1
	for n, call := range t.Train.Calls {
		if call.Arrival.Inferred != nil || call.Arrival.Actual != nil {
			bound = n
		}
	}
	for n := 0; n < bound; n++ {
		if _, exists := current[t.Codes[n]]; exists {
			return true
		}
	}
	return false
}

// scopeMetroEpisode keeps an established directed episode when a separate
// upstream forecast can only occur after its remaining supported calls.
func (r *metroRuntime) scopeMetroEpisode(key string, batch *metroPointBatch, current map[string]metroPoint, now time.Time) ([]metroPoint, bool) {
	t := r.tracks[r.active[key]]
	path := batch.paths[key]
	if !r.metroEpisodeScopeEligible(t, path.Stops, path.Direction, now) {
		return nil, false
	}
	bound, valid := metroEpisodeUsableBound(t, current)
	if !valid {
		return nil, false
	}
	indices, valid := metroEpisodeIndices(t.Codes)
	if !valid {
		return nil, false
	}
	last, anchor := metroEpisodeRemaining(t, current, indices, bound, now)
	if last.IsZero() || anchor.IsZero() || !metroEpisodeUpstreamSeparate(t, current, bound, last, now) {
		return nil, false
	}
	return metroEpisodeScopedPoints(batch.groups[key], indices, bound, anchor)
}

func (r *metroRuntime) metroEpisodeScopeEligible(t *metroTrack, stops []string, direction string, now time.Time) bool {
	if !metroEpisodeStateAvailable(t) || !r.metroEpisodePathMatches(t, stops, direction, now) {
		return false
	}
	clock := t.Train.SourceUpdatedAt
	return !clock.IsZero() && !clock.After(now) && now.Sub(clock) <= 60*time.Second
}

func metroEpisodeStateAvailable(t *metroTrack) bool {
	if t == nil || t.HistoricalOnly || t.BarrierRevision != 0 || t.BarrierBefore != nil {
		return false
	}
	return t.PendingSuccessor == "" && t.LifecycleGroup == "" && !t.ProofOverflow && !metroTrackClosed(t)
}

func (r *metroRuntime) metroEpisodePathMatches(t *metroTrack, stops []string, direction string, now time.Time) bool {
	if !metroQualifiedDirection(t, now) || t.Profile != r.topology.Profile || t.ProviderDirection != direction {
		return false
	}
	return slices.Equal(t.Codes, stops) && len(t.Codes) == len(t.Train.Calls)
}

func metroEpisodeUsableBound(t *metroTrack, current map[string]metroPoint) (int, bool) {
	bound := metroEpisodePassageBound(t)
	if bound < 0 || t.Train.NextIndex == nil || *t.Train.NextIndex < bound {
		return -1, false
	}
	point, exists := current[t.Codes[bound]]
	return bound, !exists || !metroArrivalContradicted(t.Train.Calls[bound], point)
}

func metroEpisodeIndices(codes []string) (map[string]int, bool) {
	indices := make(map[string]int, len(codes))
	for n, code := range codes {
		if _, exists := indices[code]; exists {
			return nil, false
		}
		indices[code] = n
	}
	return indices, true
}

func metroEpisodeScopedPoints(points []metroPoint, indices map[string]int, bound int, anchor time.Time) ([]metroPoint, bool) {
	kept := make([]metroPoint, 0, len(points))
	for _, point := range points {
		n, ok := indices[point.Stop]
		if !ok {
			return nil, false
		}
		if n >= bound && !point.Clock.After(anchor) {
			kept = append(kept, point)
		}
	}
	return kept, len(kept) > 0
}

func metroEpisodePassageBound(t *metroTrack) int {
	bound := -1
	var previous time.Time
	for n, call := range t.Train.Calls {
		if call.Arrival.Actual != nil {
			return -1 // No current Metro adapter provides source-verified actual events.
		}
		proof := call.Arrival.Inferred
		if proof == nil {
			continue
		}
		if !metroEpisodeArrivalValid(proof, t.Train.SourceUpdatedAt) || !previous.IsZero() && !proof.WindowEnd.After(previous) {
			return -1
		}
		previous, bound = proof.WindowEnd, n
	}
	if bound < 0 || !metroEpisodeArrivalProof(t, bound) {
		return -1
	}
	return bound
}

func metroEpisodeArrivalValid(arrival *api.MetroEventEvidence, source time.Time) bool {
	if arrival == nil || arrival.Mode != "inferred_arrival" || arrival.ModelVersion != metroArrivalModel {
		return false
	}
	if !arrival.At.Equal(arrival.WindowEnd) || !arrival.WindowEnd.After(arrival.WindowStart) {
		return false
	}
	return !arrival.WindowEnd.After(source)
}

func metroEpisodeArrivalProof(t *metroTrack, n int) bool {
	_, ok := metroEpisodeArrivalAfter(t, n)
	return ok
}

func metroEpisodeArrivalAfter(t *metroTrack, n int) (metroPoint, bool) {
	call := t.Train.Calls[n]
	arrival := call.Arrival.Inferred
	if !metroEpisodeArrivalValid(arrival, t.Train.SourceUpdatedAt) {
		return metroPoint{}, false
	}
	var matched metroEventProof
	found := false
	for _, proof := range t.Proofs {
		if !metroEpisodeProofMatches(t, n, proof) {
			continue
		}
		if found && !metroSameEpisodeTransition(matched, proof) {
			return metroPoint{}, false
		}
		matched, found = proof, true
	}
	return matched.After, found
}

func metroEpisodeProofMatches(t *metroTrack, n int, proof metroEventProof) bool {
	call := t.Train.Calls[n]
	before, after := proof.Before, proof.After
	if proof.Train.JourneyId != t.Train.JourneyId || proof.CallID != call.Id {
		return false
	}
	if before.Stop != t.Codes[n] || after.Stop != before.Stop || before.Platform == "" || before.Platform != after.Platform {
		return false
	}
	return metroEpisodeTransitionMatches(call.Arrival.Inferred, before, after)
}

func metroEpisodeTransitionMatches(arrival *api.MetroEventEvidence, before, after metroPoint) bool {
	if before.Seconds == nil || *before.Seconds <= 0 || after.Seconds == nil || *after.Seconds != 0 {
		return false
	}
	return before.Clock.Equal(arrival.WindowStart) && after.Clock.Equal(arrival.WindowEnd) && after.Clock.Sub(before.Clock) <= 60*time.Second
}

func metroSameEpisodeTransition(a, b metroEventProof) bool {
	return a.Before.Platform == b.Before.Platform && equalMetroSeconds(a.Before.Seconds, b.Before.Seconds) && a.After.Platform == b.After.Platform
}

// Withhold the association, but do not hide a tightly supported correction to
// an earlier visit. A reused reference can never establish physical identity.
func (s metroContextSelection) retractEpisodeCorrections(scope, key string, current map[string]metroPoint) {
	evidence, ok := s.episodeCorrectionEvidence(scope, key, current)
	if !ok {
		return
	}
	for n := 0; n <= evidence.bound; n++ {
		point, found := current[evidence.track.Codes[n]]
		if !found || !metroArrivalContradicted(evidence.track.Train.Calls[n], point) {
			continue
		}
		after, proven := metroEpisodeArrivalAfter(evidence.track, n)
		if !proven || !metroCorrectionClockValid(evidence.track, after, point, s.now) {
			continue
		}
		if metroEpisodeCorrectionOrdered(point, evidence.remaining, evidence.track.Codes[evidence.bound:], s.now) {
			s.runtime.retractMetroArrival(evidence.track, n, point, after, true)
		}
	}
}

type metroCorrectionEvidence struct {
	track     *metroTrack
	remaining map[string]metroPoint
	bound     int
}

func (s metroContextSelection) episodeCorrectionEvidence(scope, key string, current map[string]metroPoint) (metroCorrectionEvidence, bool) {
	if len(s.batch.references[scope]) != 1 {
		return metroCorrectionEvidence{}, false
	}
	t := s.runtime.tracks[s.runtime.active[key]]
	path := s.batch.paths[key]
	if !s.runtime.metroEpisodeScopeEligible(t, path.Stops, path.Direction, s.now) {
		return metroCorrectionEvidence{}, false
	}
	if motion := s.runtime.operational[scope]; metroOrderingMotionFresh(motion, s.now) && motion.Sign != metroPathAxisSign(motion.Axis, path) {
		return metroCorrectionEvidence{}, false
	}
	bound := metroEpisodePassageBound(t)
	if bound < 0 || t.Train.NextIndex == nil || *t.Train.NextIndex < bound {
		return metroCorrectionEvidence{}, false
	}
	indices, valid := metroEpisodeIndices(t.Codes)
	if !valid {
		return metroCorrectionEvidence{}, false
	}
	remaining, valid := metroCorrectionRemaining(t, current, indices, bound, s.now)
	return metroCorrectionEvidence{track: t, remaining: remaining, bound: bound}, valid
}

func metroCorrectionRemaining(t *metroTrack, current map[string]metroPoint, indices map[string]int, bound int, now time.Time) (map[string]metroPoint, bool) {
	remaining := map[string]metroPoint{}
	for stop, point := range current {
		n, exists := indices[stop]
		if !exists {
			return nil, false
		}
		if n >= bound && (n != bound || !metroArrivalContradicted(t.Train.Calls[n], point)) {
			remaining[stop] = point
		}
	}
	last, anchor := metroEpisodeRemaining(t, remaining, indices, bound, now)
	if last.IsZero() || anchor.IsZero() {
		return nil, false
	}
	for stop, point := range remaining {
		if point.Clock.After(anchor) {
			delete(remaining, stop)
		}
	}
	return remaining, !conflictingMetroPoints(t, remaining, now)
}

func metroCorrectionClockValid(t *metroTrack, after, point metroPoint, now time.Time) bool {
	if point.Platform != after.Platform || !point.Clock.After(after.Clock) || point.Clock.Sub(after.Clock) > 60*time.Second {
		return false
	}
	return !point.Clock.Before(t.Train.SourceUpdatedAt) && !point.Clock.After(now) && now.Sub(point.Clock) < sourceFreshness
}

func metroEpisodeCorrectionOrdered(point metroPoint, current map[string]metroPoint, downstream []string, now time.Time) bool {
	at := point.Clock.Add(time.Duration(*point.Seconds) * time.Second)
	if at.Before(now) {
		return false
	}
	for _, code := range downstream {
		if next, ok := current[code]; ok && next.Seconds != nil {
			nextAt := next.Clock.Add(time.Duration(*next.Seconds) * time.Second)
			if !nextAt.Before(now) && at.After(nextAt) {
				return false
			}
		}
	}
	return true
}

func metroEpisodeRemaining(t *metroTrack, current map[string]metroPoint, indices map[string]int, bound int, now time.Time) (time.Time, time.Time) {
	anchor, valid := metroEpisodeAnchor(t, current, indices, bound, now)
	if !valid || anchor.IsZero() {
		return time.Time{}, time.Time{}
	}
	window := metroEpisodeWindow{indices: indices, bound: bound, anchor: anchor, now: now}
	return metroEpisodeLastEstimate(t, current, window), anchor
}

func metroEpisodeAnchor(t *metroTrack, current map[string]metroPoint, indices map[string]int, bound int, now time.Time) (time.Time, bool) {
	var anchor time.Time
	for stop, point := range current {
		n, ok := indices[stop]
		if !ok || point.Clock.After(now) || point.Clock.Sub(t.Train.SourceUpdatedAt) > 60*time.Second {
			return time.Time{}, false
		}
		if n < bound {
			continue
		}
		old, exists := t.Points[stop]
		if !exists {
			continue
		}
		if !metroEpisodePointContinues(old, point) {
			return time.Time{}, false
		}
		if metroEpisodeAnchorPoint(t, n, old, point, now) && point.Clock.After(anchor) {
			anchor = point.Clock
		}
	}
	return anchor, true
}

func metroEpisodeAnchorPoint(t *metroTrack, n int, old, point metroPoint, now time.Time) bool {
	return n >= *t.Train.NextIndex && old.Seconds != nil && point.Seconds != nil && now.Sub(point.Clock) <= 60*time.Second
}

type metroEpisodeWindow struct {
	indices     map[string]int
	bound       int
	anchor, now time.Time
}

func metroEpisodeLastEstimate(t *metroTrack, current map[string]metroPoint, window metroEpisodeWindow) time.Time {
	var last time.Time
	for stop, point := range current {
		if window.indices[stop] < window.bound || point.Clock.After(window.anchor) {
			continue
		}
		at, valid := metroEpisodeEstimate(t, point, window.now)
		if valid && at.After(last) {
			last = at
		}
	}
	return last
}

func metroEpisodeEstimate(t *metroTrack, point metroPoint, now time.Time) (time.Time, bool) {
	if point.Seconds == nil {
		return time.Time{}, false
	}
	at := point.Clock.Add(time.Duration(*point.Seconds) * time.Second)
	old, exists := t.Points[point.Stop]
	newZero := exists && old.Seconds != nil && *old.Seconds > 0 && *point.Seconds == 0 && point.Clock.After(old.Clock)
	return at, !at.Before(now) || newZero
}

func metroEpisodePointContinues(old, point metroPoint) bool {
	if old.Platform == "" || old.Platform != point.Platform || point.Clock.Before(old.Clock) || point.Clock.Sub(old.Clock) > 60*time.Second || metroPointClockConflict(old, point) {
		return false
	}
	if old.Seconds == nil || point.Seconds == nil {
		return true
	}
	oldAt := old.Clock.Add(time.Duration(*old.Seconds) * time.Second)
	newAt := point.Clock.Add(time.Duration(*point.Seconds) * time.Second)
	delta := newAt.Sub(oldAt)
	return delta >= -30*time.Second && delta <= 30*time.Second
}

func metroEpisodeUpstreamSeparate(t *metroTrack, current map[string]metroPoint, bound int, last, now time.Time) bool {
	found := false
	var previous time.Time
	for n := 0; n < bound; n++ {
		point, exists := current[t.Codes[n]]
		if !exists {
			continue
		}
		if !metroEpisodeUnknownUpstream(t, n, bound, point, now) {
			return false
		}
		at := point.Clock.Add(time.Duration(*point.Seconds) * time.Second)
		if !at.After(last) || !previous.IsZero() && at.Before(previous) {
			return false
		}
		previous, found = at, true
	}
	return found || t.ScopedContinuity
}

func metroEpisodeUnknownUpstream(t *metroTrack, n, bound int, point metroPoint, now time.Time) bool {
	call := t.Train.Calls[n]
	if _, known := t.Points[point.Stop]; known || call.Arrival.Inferred != nil || call.Arrival.Actual != nil || call.LastOfficialEstimate != nil {
		return false
	}
	if point.Seconds == nil || *point.Seconds <= 0 || point.Clock.After(now) {
		return false
	}
	return point.Clock.After(t.Train.Calls[bound].Arrival.Inferred.WindowEnd)
}
