package app

import (
	"sort"
	"strings"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

type metroVisitPrior struct {
	Dwell, Run   int
	DwellSamples []int
}

// Only compatible published trips supply priors. Arrival-to-arrival history
// cannot identify dwell and running components separately.
func metroSchedulePriors(data *MetroData, static *StaticData, path patterns.Pattern, now time.Time) []metroVisitPrior {
	out := make([]metroVisitPrior, len(path.Stops))
	if static == nil || static.Schedule == nil {
		return out
	}
	b := metroPlanBuilder{data: data, static: static, stations: map[string]MetroStation{}, mapping: map[string]string{}, routes: map[string]string{}}
	b.addStations()
	b.mapStaticPlan()
	dwell, run := make([][]int, len(out)), make([][]int, len(out))
	for _, trip := range static.Schedule.Trips {
		collectMetroPriorDurations(metroPriorVisits(&b, trip, path, now), dwell, run)
	}
	for n := range out {
		out[n] = metroVisitPrior{Dwell: metroPriorMedian(dwell[n]), Run: metroPriorMedian(run[n]), DwellSamples: dwell[n]}
	}
	return out
}
func metroPriorVisits(b *metroPlanBuilder, trip ScheduledTrip, path patterns.Pattern, now time.Time) []StopTime {
	_, direction := b.tripDestination(trip)
	if b.routes[trip.Route] != path.Route || !b.static.Schedule.active(trip.Service, now.In(lisbon)) || direction != path.Direction {
		return nil
	}
	visits := localJourneyTimes(&trip)
	if !metroPriorPathMatches(b, visits, path) {
		return nil
	}
	return visits
}
func metroPriorPathMatches(b *metroPlanBuilder, visits []StopTime, path patterns.Pattern) bool {
	codes := make([]string, len(visits))
	for n, v := range visits {
		codes[n] = b.mapping[v.Stop]
	}
	return len(visits) == len(path.Stops) && strings.Join(codes, "|") == strings.Join(path.Stops, "|")
}
func collectMetroPriorDurations(visits []StopTime, dwell, run [][]int) {
	for n, v := range visits {
		if seconds := int(v.Departure - v.Arrival); seconds > 0 && seconds <= 300 {
			dwell[n] = append(dwell[n], seconds)
		}
		if n+1 >= len(visits) {
			continue
		}
		if seconds := int(visits[n+1].Arrival - v.Departure); seconds > 0 && seconds <= 600 {
			run[n] = append(run[n], seconds)
		}
	}
}

func metroPriorMedian(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sort.Ints(values)
	return values[len(values)/2]
}

func projectMetroScheduled(t *metroTrack, now time.Time) {
	if t.Train.Association != "supported" || !now.Before(t.Train.ValidUntil) {
		return
	}
	resetMetroPriorPredictions(t.Train.Calls)
	var anchor *metroPriorAnchor
	for n := range t.Train.Calls {
		if n >= len(t.Priors) {
			anchor = nil
			continue
		}
		anchor = projectMetroPriorVisit(t, &t.Train.Calls[n], n, t.Priors[n], anchor, now)
	}
}
func resetMetroPriorPredictions(calls []api.StopCall) {
	for n := range calls {
		c := &calls[n]
		c.OwnDeparturePrediction = nil
		if c.OwnPrediction == nil || c.OwnPrediction.ModelVersion == nil {
			continue
		}
		if strings.HasPrefix(*c.OwnPrediction.ModelVersion, "metro-schedule-prior-v1:") {
			c.OwnPrediction = nil
		}
	}
}

// A schedule estimate inherits the clock and expiry of its actual anchor, not
// the newest unrelated station publication in the same train episode.
type metroPriorAnchor struct {
	At              time.Time
	SourceUpdatedAt *time.Time
	ValidUntil      time.Time
}

func metroPriorArrival(t *metroTrack, c *api.StopCall, n int, anchor *metroPriorAnchor, now time.Time) *metroPriorAnchor {
	if c.Arrival.Inferred != nil {
		return metroPriorEventAnchor(t, n, c.Arrival.Inferred, true, now)
	}
	if c.OwnPrediction != nil {
		if own := metroPriorForecastAnchor(c.OwnPrediction, t.Train.ValidUntil, now); own != nil {
			return own
		}
		c.OwnPrediction = nil
	}
	if metroPriorAnchorFresh(anchor, now) && !anchor.At.Before(now) {
		c.OwnPrediction = metroPriorEvidence(t, *anchor)
		return anchor
	}
	return metroPriorForecastAnchor(c.Arrival.Prediction, t.Train.ValidUntil, now)
}
func projectMetroPriorVisit(t *metroTrack, c *api.StopCall, n int, prior metroVisitPrior, anchor *metroPriorAnchor, now time.Time) *metroPriorAnchor {
	anchor = metroPriorArrival(t, c, n, anchor, now)
	if c.Departure.Inferred != nil {
		return metroAfterRun(metroPriorEventAnchor(t, n, c.Departure.Inferred, false, now), prior.Run)
	}
	if anchor == nil {
		return nil
	}
	if metroPriorNeedsArrival(*c, anchor.At, now) {
		c.OwnPrediction = metroPriorEvidence(t, *anchor)
	}
	return projectMetroPriorDeparture(t, c, prior, *anchor, now)
}
func metroPriorNeedsArrival(c api.StopCall, anchor, now time.Time) bool {
	return c.OwnPrediction == nil && c.Arrival.Inferred == nil && c.Arrival.Prediction == nil && !anchor.Before(now)
}
func projectMetroPriorDeparture(t *metroTrack, c *api.StopCall, prior metroVisitPrior, anchor metroPriorAnchor, now time.Time) *metroPriorAnchor {
	dwell := prior.Dwell
	if c.Arrival.Inferred != nil {
		dwell = metroConditionalDwell(prior, now.Sub(anchor.At))
	}
	if dwell <= 0 {
		return nil
	}
	departure := anchor.At.Add(time.Duration(dwell) * time.Second)
	if departure.Before(now) {
		return nil
	}
	anchor.At = departure
	c.OwnDeparturePrediction = metroPriorEvidence(t, anchor)
	return metroAfterRun(&anchor, prior.Run)
}

func metroPriorAnchorFresh(anchor *metroPriorAnchor, now time.Time) bool {
	return anchor != nil && now.Before(anchor.ValidUntil)
}
func metroPriorForecastAnchor(e *api.CallTimeEvidence, episodeExpiry, now time.Time) *metroPriorAnchor {
	if e == nil || e.ValidUntil == nil || !now.Before(*e.ValidUntil) || e.At.Before(now) {
		return nil
	}
	return &metroPriorAnchor{At: e.At, SourceUpdatedAt: e.SourceUpdatedAt, ValidUntil: minTime(*e.ValidUntil, episodeExpiry)}
}
func metroPriorEventAnchor(t *metroTrack, n int, e *api.MetroEventEvidence, arrival bool, now time.Time) *metroPriorAnchor {
	clock := e.WindowEnd
	if !arrival && e.ConfirmedAt != nil && e.ConfirmedAt.After(clock) {
		clock = *e.ConfirmedAt
	}
	if arrival {
		clock = metroPriorArrivalClock(t, n, clock, now)
	}
	if clock.IsZero() || clock.After(now) {
		return nil
	}
	anchor := &metroPriorAnchor{At: e.At, SourceUpdatedAt: &clock, ValidUntil: minTime(clock.Add(sourceFreshness), t.Train.ValidUntil)}
	if !metroPriorAnchorFresh(anchor, now) {
		return nil
	}
	return anchor
}

func metroPriorArrivalClock(t *metroTrack, n int, clock, now time.Time) time.Time {
	if n >= len(t.Codes) {
		return clock
	}
	point, exists := t.Points[t.Codes[n]]
	if exists && point.Seconds != nil && *point.Seconds == 0 && point.Clock.After(clock) && !point.Clock.After(now) {
		return point.Clock
	}
	return clock
}
func metroAfterRun(anchor *metroPriorAnchor, seconds int) *metroPriorAnchor {
	if seconds <= 0 || anchor == nil {
		return nil
	}
	next := *anchor
	next.At = next.At.Add(time.Duration(seconds) * time.Second)
	return &next
}
func metroConditionalDwell(prior metroVisitPrior, elapsed time.Duration) int {
	remaining := []int{}
	for _, seconds := range prior.DwellSamples {
		if time.Duration(seconds)*time.Second > elapsed {
			remaining = append(remaining, seconds)
		}
	}
	return metroPriorMedian(remaining)
}
func metroPriorEvidence(t *metroTrack, anchor metroPriorAnchor) *api.CallTimeEvidence {
	expiry, episode := anchor.ValidUntil, t.Train.JourneyId
	version := "metro-schedule-prior-v1:" + t.Profile
	return &api.CallTimeEvidence{At: anchor.At, SourceUpdatedAt: anchor.SourceUpdatedAt, ValidUntil: &expiry, SourceUrl: "/api/v1/operators/metro", ModelVersion: &version, AssociationEpisode: &episode}
}

func (r *metroRuntime) visitPriors(path patterns.Pattern, now time.Time) []metroVisitPrior {
	if r.priorCache == nil {
		r.priorCache = map[string][]metroVisitPrior{}
	}
	key := path.Route + "|" + path.Direction + "|" + strings.Join(path.Stops, ",") + "|" + now.In(lisbon).Format("20060102")
	if priors, ok := r.priorCache[key]; ok {
		return priors
	}
	priors := metroSchedulePriors(r.publication, r.plan, path, now)
	if len(r.priorCache) < 256 {
		r.priorCache[key] = priors
	}
	return priors
}
func (r *metroRuntime) projectContextDepartures(contexts []api.MetroForecastContext, now time.Time) {
	for n := range contexts {
		c := &contexts[n]
		path := r.contextPriorPath(*c)
		if len(path.Stops) == 0 {
			continue
		}
		dwell := r.contextDwellPriors(path, now)
		for j := range c.Calls {
			r.projectContextPriorDeparture(&c.Calls[j], dwell, now)
		}
	}
}
func (r *metroRuntime) contextPriorPath(context api.MetroForecastContext) patterns.Pattern {
	if r.batch == nil {
		return patterns.Pattern{}
	}
	for _, path := range r.batch.paths {
		if path.Route == context.RouteId && metroDestinationName(r.publication, path) == context.Destination {
			return path
		}
	}
	return patterns.Pattern{}
}
func (r *metroRuntime) contextDwellPriors(path patterns.Pattern, now time.Time) map[string]int {
	priors := r.visitPriors(path, now)
	calls := metroPathCalls(path, r.publication, r.plan, "prior")
	out := map[string]int{}
	for n, c := range calls {
		if priors[n].Dwell > 0 {
			out[c.StopId] = priors[n].Dwell
		}
	}
	return out
}
func metroContextOwnAnchor(c api.StopCall, now time.Time) *api.CallTimeEvidence {
	p := c.OwnPrediction
	if p == nil || p.ValidUntil == nil || !now.Before(*p.ValidUntil) || p.At.Before(now) {
		p = c.Arrival.Prediction
	}
	if p == nil || p.ValidUntil == nil || !now.Before(*p.ValidUntil) || p.At.Before(now) {
		return nil
	}
	return p
}
func (r *metroRuntime) projectContextPriorDeparture(c *api.StopCall, dwell map[string]int, now time.Time) {
	if c.OwnDeparturePrediction != nil || dwell[c.StopId] <= 0 {
		return
	}
	p := metroContextOwnAnchor(*c, now)
	if p == nil || p.ValidUntil == nil {
		return
	}
	at := p.At.Add(time.Duration(dwell[c.StopId]) * time.Second)
	version := "metro-schedule-prior-v1:" + r.topology.Profile
	c.OwnDeparturePrediction = &api.CallTimeEvidence{At: at, SourceUpdatedAt: p.SourceUpdatedAt, ValidUntil: p.ValidUntil, SourceUrl: "/api/v1/operators/metro", ModelVersion: &version}
}
