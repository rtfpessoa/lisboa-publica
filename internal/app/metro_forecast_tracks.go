package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

type metroForecastTrack struct {
	Call  api.StopCall
	Until time.Time
}

// Source forecast continuity is separate from operational train identity.
func (r *metroRuntime) reconcileForecastTracks(contexts []api.MetroForecastContext, now time.Time) {
	calls := metroContextCalls(contexts)
	partitions := metroForecastPartitions(r.forecastTracks)
	matches, counts := metroForecastMatches(calls, partitions, now)
	next := map[string]metroForecastTrack{}
	for n, call := range calls {
		anchor := metroForecastAnchor(*call)
		if !metroForecastHasClock(anchor) {
			continue
		}
		call.Id = r.metroForecastIdentity(call, anchor, matches[n], counts)
		r.forecastCandidates(call, now)
		next[call.Id] = metroForecastTrack{*call, *anchor.ValidUntil}
	}
	r.forecastTracks = next
}
func metroContextCalls(contexts []api.MetroForecastContext) []*api.StopCall {
	calls := []*api.StopCall{}
	for n := range contexts {
		for j := range contexts[n].Calls {
			calls = append(calls, &contexts[n].Calls[j])
		}
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].Id < calls[j].Id })
	return calls
}
func metroForecastPartitions(tracks map[string]metroForecastTrack) map[string]map[string]metroForecastTrack {
	partitions := map[string]map[string]metroForecastTrack{}
	for id, track := range tracks {
		key := metroForecastPartition(track.Call)
		if partitions[key] == nil {
			partitions[key] = map[string]metroForecastTrack{}
		}
		partitions[key][id] = track
	}
	return partitions
}
func metroForecastMatches(calls []*api.StopCall, partitions map[string]map[string]metroForecastTrack, now time.Time) ([][]string, map[string]int) {
	matches := make([][]string, len(calls))
	counts := map[string]int{}
	for n, call := range calls {
		matches[n] = metroForecastMatch(*call, partitions[metroForecastPartition(*call)], now)
		for _, id := range matches[n] {
			counts[id]++
		}
	}
	return matches, counts
}
func metroForecastMatch(call api.StopCall, tracks map[string]metroForecastTrack, now time.Time) []string {
	for id, old := range tracks {
		if now.Before(old.Until) && metroForecastSamePublication(old.Call, call) {
			return []string{id}
		}
	}
	matches := []string{}
	for id, old := range tracks {
		if now.Before(old.Until) && metroForecastContinues(old.Call, call) {
			matches = append(matches, id)
		}
	}
	return matches
}
func metroForecastHasClock(anchor *api.CallTimeEvidence) bool {
	return anchor != nil && anchor.ValidUntil != nil && anchor.SourceUpdatedAt != nil
}
func (r *metroRuntime) metroForecastIdentity(call *api.StopCall, anchor *api.CallTimeEvidence, matches []string, counts map[string]int) string {
	if len(matches) == 1 && counts[matches[0]] == 1 {
		return matches[0]
	}
	r.forecastSequence++
	input := fmt.Sprintf("%s|%d|%s|%s|%s", r.session, r.forecastSequence, call.Id, anchor.SourceUpdatedAt.Format(time.RFC3339Nano), anchor.At.Format(time.RFC3339Nano))
	return fmt.Sprintf("metro:forecast:%x", sha256.Sum256([]byte(input)))
}

func metroForecastContinues(a, b api.StopCall) bool {
	if metroForecastPartition(a) != metroForecastPartition(b) {
		return false
	}
	ap, bp := metroForecastAnchor(a), metroForecastAnchor(b)
	if !metroForecastClockContinues(ap, bp) {
		return false
	}
	ar, br := textValue(a.ServiceLabel), textValue(b.ServiceLabel)
	delta := bp.At.Sub(ap.At)
	return metroForecastOwnershipCompatible(ar, br) && delta >= -30*time.Second && delta <= 30*time.Second
}
func metroForecastClockContinues(a, b *api.CallTimeEvidence) bool {
	if a == nil || b == nil {
		return false
	}
	return a.SourceUpdatedAt != nil && b.SourceUpdatedAt != nil && !b.SourceUpdatedAt.Before(*a.SourceUpdatedAt)
}
func metroForecastOwnershipCompatible(a, b string) bool { return a == "" || b == "" || a == b }

func metroForecastPartition(c api.StopCall) string {
	platforms := []string{}
	if c.MetroForecast != nil {
		for _, p := range c.MetroForecast.Platforms {
			platforms = append(platforms, p.Platform)
		}
	}
	sort.Strings(platforms)
	return strings.Join([]string{c.StopId, c.LineKey, textValue(c.DirectionKey), c.Destination, strings.Join(platforms, ",")}, "|")
}

func (r *metroRuntime) forecastCandidates(c *api.StopCall, now time.Time) {
	if c.MetroForecast == nil || c.MetroForecast.SourceReference != nil {
		return
	}
	if c.Arrival.Prediction == nil || c.DirectionKey == nil {
		return
	}
	candidates := []string{}
	for _, id := range r.active {
		t := r.tracks[id]
		if metroCandidateTrain(t, c, now) {
			candidates = append(candidates, metroTrainCandidates(t, c)...)
		}
	}
	sort.Strings(candidates)
	candidates = r.constrainForecastCandidates(c, candidates, now)
	c.MetroForecast.CandidateReferences = &candidates
	c.MetroForecast.UnknownCandidate = ptr(true)
	c.MetroForecast.Limitations = append(c.MetroForecast.Limitations, "unseen_train_possible", "candidate_window_uncalibrated")
}
func metroCandidateTrain(t *metroTrack, c *api.StopCall, now time.Time) bool {
	if t == nil || c.DirectionKey == nil {
		return false
	}
	return metroCurrentDirection(t.Train, now) && t.Train.RouteId == c.LineKey && t.Train.DirectionCode == *c.DirectionKey
}
func metroTrainCandidates(t *metroTrack, c *api.StopCall) []string {
	candidates := []string{}
	for n, visit := range t.Train.Calls {
		if visit.StopId != c.StopId || !metroVisitUpcoming(t.Train, n) {
			continue
		}
		prediction := visit.OwnPrediction
		if prediction == nil {
			prediction = visit.Arrival.Prediction
		}
		if metroForecastWithinWindow(prediction, c.Arrival.Prediction) {
			candidates = append(candidates, t.Train.Reference)
		}
	}
	return candidates
}
func metroForecastWithinWindow(a, b *api.CallTimeEvidence) bool {
	if a == nil || b == nil {
		return false
	}
	delta := a.At.Sub(b.At)
	return delta >= -30*time.Second && delta <= 30*time.Second
}

func (r *metroRuntime) constrainForecastCandidates(c *api.StopCall, candidates []string, now time.Time) []string {
	if !metroForecastConstraintReady(c, r.batch) {
		return candidates
	}
	var target *metroLocalForecast
	for n := range r.batch.localOnly {
		f := &r.batch.localOnly[n]
		if f.Reference == "" && f.Valid && metroWaitRevision(f.Row) == *c.MetroForecast.SourceRevisionId && f.Slot+1 == *c.MetroForecast.SourceSlot {
			target = f
			break
		}
	}

	if target == nil {
		return candidates
	}
	refs := []string{target.Row.Train, target.Row.Train2, target.Row.Train3}
	out := []string{}
	for _, ref := range candidates {
		if r.metroCandidateInOrder(c, ref, refs, target.Slot, now) {
			out = append(out, ref)
		}
	}
	return out
}
func (r *metroRuntime) metroCandidateInOrder(c *api.StopCall, ref string, refs []string, slot int, now time.Time) bool {
	motion := r.metroCohortMotion(c, ref, now)
	if !metroOrderingMotionFresh(motion, now) {
		return true
	}
	for n, anchor := range refs {
		if n == slot || !metroReference(anchor) {
			continue
		}
		order, constrained := metroMotionOrder(motion, r.metroCohortMotion(c, anchor, now), now)
		if !constrained {
			continue
		}
		metroRecordForecastAnchor(c, anchor)
		if n < slot && order > 0 || n > slot && order < 0 {
			return false
		}
	}
	return true
}
func metroForecastConstraintReady(c *api.StopCall, batch *metroPointBatch) bool {
	p := c.Arrival.Prediction
	if p == nil || p.SourceUpdatedAt == nil || batch == nil {
		return false
	}
	return c.MetroForecast.SourceRevisionId != nil && c.MetroForecast.SourceSlot != nil
}
func metroOrderingMotionFresh(m *metroOperationalMotion, now time.Time) bool {
	return m != nil && m.Sign != 0 && now.Before(m.PublishedAt.Add(sourceFreshness))
}
func metroMotionOrder(candidate, anchor *metroOperationalMotion, now time.Time) (int, bool) {
	if !metroMotionsComparable(candidate, anchor, now) {
		return 0, false
	}
	delta := math.Abs(anchor.PublishedAt.Sub(candidate.PublishedAt).Seconds())
	progress := metroSignedProgress(candidate) - metroSignedProgress(anchor)
	// Overlapping noise bounds supply no strict ordering constraint.
	if math.Abs(progress) <= 6+50*delta {
		return 0, false
	}
	order := -1
	if progress > 0 {
		order = 1
	}
	return order, true
}
func metroMotionsComparable(candidate, anchor *metroOperationalMotion, now time.Time) bool {
	if !metroOrderingMotionFresh(anchor, now) {
		return false
	}
	delta := math.Abs(anchor.PublishedAt.Sub(candidate.PublishedAt).Seconds())
	return anchor.Sign == candidate.Sign && anchor.Axis.Direction == candidate.Axis.Direction && delta <= 1
}

func metroSignedProgress(m *metroOperationalMotion) float64 {
	return m.Samples[len(m.Samples)-1].Metres * float64(m.Sign)
}
func metroRecordForecastAnchor(c *api.StopCall, reference string) {
	if c.MetroForecast != nil && !metroHasAnchor(c.MetroForecast.Anchors, reference) {
		c.MetroForecast.Anchors = append(c.MetroForecast.Anchors, reference)
	}
}

func metroWaitRevision(row MetroWait) string {
	raw, _ := json.Marshal(row)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func metroForecastSamePublication(a, b api.StopCall) bool {
	if metroForecastPartition(a) != metroForecastPartition(b) || a.MetroForecast == nil || b.MetroForecast == nil {
		return false
	}
	am, bm := a.MetroForecast, b.MetroForecast
	return metroOptionalTextEqual(am.SourceRevisionId, bm.SourceRevisionId) && metroOptionalSlotEqual(am.SourceSlot, bm.SourceSlot)
}
func metroOptionalTextEqual(a, b *string) bool { return a != nil && b != nil && *a == *b }
func metroOptionalSlotEqual(a, b *int) bool    { return a != nil && b != nil && *a == *b }

func metroForecastAnchor(c api.StopCall) *api.CallTimeEvidence {
	if c.Arrival.Prediction != nil {
		return c.Arrival.Prediction
	}
	return c.OwnPrediction
}

func (r *metroRuntime) metroCohortMotion(c *api.StopCall, reference string, now time.Time) *metroOperationalMotion {
	for _, id := range r.active {
		t := r.tracks[id]
		if !metroCandidateTrain(t, c, now) || t.Train.Reference != reference {
			continue
		}
		if !metroTrainReachesStop(t.Train, c.StopId) {
			continue
		}
		m := r.operational[c.LineKey+"|"+reference]
		if m != nil && len(m.Samples) > 0 {
			return m
		}
	}
	return nil
}
func metroTrainReachesStop(train api.MetroTrain, stop string) bool {
	for n, visit := range train.Calls {
		if visit.StopId == stop && metroVisitUpcoming(train, n) {
			return true
		}
	}
	return false
}

func metroHasAnchor(anchors []string, reference string) bool {
	for _, a := range anchors {
		if a == reference {
			return true
		}
	}
	return false
}

func metroVisitUpcoming(train api.MetroTrain, index int) bool {
	return train.CurrentIndex != nil && index == *train.CurrentIndex || train.NextIndex != nil && index >= *train.NextIndex
}
