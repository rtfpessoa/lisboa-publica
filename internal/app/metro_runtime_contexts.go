package app

import (
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"sort"
	"strconv"
	"strings"
	"time"
)

// selectedContexts classifies each path once and preserves a supported prior
// context. A second direction is a possible forecast context, not a conflict
// and not a reason to retire a journey. A new ambiguous reference stays unlinked.
func (r *metroRuntime) selectedContexts(b *metroPointBatch, now time.Time) []string {
	selected := []string{}
	scopes := []string{}
	for scope := range b.references {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	selection := metroContextSelection{runtime: r, batch: b, now: now}
	for _, scope := range scopes {
		if key := selection.choose(scope); key != "" {
			selected = append(selected, key)
		}
	}
	return selected
}

type metroContextSelection struct {
	runtime *metroRuntime
	batch   *metroPointBatch
	now     time.Time
}

func (s metroContextSelection) candidates(scope string) ([]string, []string) {
	valid, present := []string{}, []string{}
	for key := range s.batch.references[scope] {
		points, exists := s.batch.groups[key]
		if !exists || s.batch.rejected[key] {
			continue
		}
		current, conflict := latestMetroPoints(points)
		probe := &metroTrack{Codes: s.batch.paths[key].Stops, Points: map[string]metroPoint{}}
		if conflict || conflictingMetroPoints(probe, current, s.now) {
			s.batch.rejected[key] = true
			continue
		}
		present = append(present, key)
		if metroContextHasWait(current) {
			valid = append(valid, key)
		}
	}
	sort.Strings(valid)
	return valid, present
}
func metroContextHasWait(points map[string]metroPoint) bool {
	for _, p := range points {
		if p.Seconds != nil {
			return true
		}
	}
	return false
}
func (s metroContextSelection) prior(scope string) string {
	prior := ""
	for key, id := range s.runtime.active {
		t := s.runtime.tracks[id]
		if t == nil || t.Train.RouteId+"|"+t.Train.Reference != scope || !s.now.Before(t.Train.ValidUntil) {
			continue
		}
		if prior != "" {
			return ""
		}
		prior = key
	}
	return prior
}
func (s metroContextSelection) supportedPrior(key string) bool {
	if key == "" || s.batch.rejected[key] || !metroQualifiedDirection(s.runtime.tracks[s.runtime.active[key]], s.now) {
		return false
	}
	points, exists := s.batch.groups[key]
	if !exists {
		return false
	}
	current, conflict := latestMetroPoints(points)
	return !conflict && !conflictingMetroPoints(s.runtime.tracks[s.runtime.active[key]], current, s.now)
}
func (s metroContextSelection) choose(scope string) string {
	valid, present := s.candidates(scope)
	prior := s.prior(scope)
	if key := s.motionChoice(scope, valid); key != "" {
		return key
	}
	selected := ""
	if !metroOrderingMotionFresh(s.runtime.operational[scope], s.now) {
		selected = s.forecastChoice(scope, prior, valid, present)
	}
	if selected == "" {
		s.suspend(scope, len(valid))
	}
	return selected
}
func (s metroContextSelection) forecastChoice(scope, prior string, valid, present []string) string {
	if s.supportedPrior(prior) {
		return prior
	}
	if s.uniqueValidChoice(scope, prior, valid) {
		return valid[0]
	}
	selected := ""
	if s.uniquePresentChoice(scope, prior, valid, present) {
		selected = present[0]
	}
	return selected
}
func (s metroContextSelection) uniqueValidChoice(scope, prior string, valid []string) bool {
	return len(valid) == 1 && (prior == "" || valid[0] == prior) && !s.rejectedAlternative(scope, valid[0])
}
func (s metroContextSelection) uniquePresentChoice(scope, prior string, valid, present []string) bool {
	return prior == "" && len(valid) == 0 && len(present) == 1 && len(s.batch.references[scope]) == 1
}

func (s metroContextSelection) rejectedAlternative(scope, selected string) bool {
	for key := range s.batch.references[scope] {
		if key == selected || !s.batch.rejected[key] {
			continue
		}
		for _, p := range s.batch.groups[key] {
			if p.Seconds != nil && s.now.Before(p.Clock.Add(sourceFreshness)) && !p.Clock.Add(time.Duration(*p.Seconds)*time.Second).Before(s.now) {
				return true
			}
		}
	}
	return false
}
func (s metroContextSelection) suspend(scope string, count int) {
	for key, id := range s.runtime.active {
		t := s.runtime.tracks[id]
		if t == nil || t.Train.RouteId+"|"+t.Train.Reference != scope {
			continue
		}
		reason := "Sem dados atuais para confirmar a viagem"
		if count > 1 {
			reason = "Várias viagens possíveis"
		}
		if s.batch.rejected[key] {
			reason = "Dados incompatíveis nesta direção"
		}
		suspendMetroTrack(t, reason)
	}
}

func (r *metroRuntime) forecastContexts(now time.Time) []api.MetroForecastContext {
	if now.Before(r.forecastCacheUntil) {
		return cloneMetroForecastContexts(r.forecastCache)
	}
	out := []api.MetroForecastContext{}
	if r.batch == nil || r.publication == nil || r.plan == nil || r.publication.Status.Status != "ok" {
		return out
	}
	out = append(out, r.localForecastContexts(now)...)
	keys := make([]string, 0, len(r.batch.paths))
	for key := range r.batch.paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		context := r.pathForecastContext(key, now)
		if context.Status == "incompatible" || len(context.Calls) > 0 {
			out = append(out, context)
		}
	}

	r.projectContextDepartures(out, now)
	r.reconcileForecastTracks(out, now)
	r.forecastCache = cloneMetroForecastContexts(out)
	r.forecastCacheUntil = r.forecastSnapshotExpiry(out, now)
	return out
}

func (r *metroRuntime) pathForecastContext(key string, now time.Time) api.MetroForecastContext {
	path := r.batch.paths[key]
	parts := strings.Split(key, "|")
	direction := canonicalMetroDirection(r.topology, path)
	context := api.MetroForecastContext{Reference: parts[len(parts)-1], RouteId: path.Route, DirectionCode: &direction, Destination: metroDestinationName(r.publication, path), Status: "admissible", Calls: []api.StopCall{}}
	if r.batch.rejected[key] {
		context.Reason = "Suporte de percurso ou movimento indisponível; previsões locais preservadas"
	}
	context.OriginKnown = ptr(false)
	calls := metroPathCalls(path, r.publication, r.plan, "metro:forecast:"+key)
	points := metroLatestForecastPoints(r.batch.groups[key])
	for n, code := range path.Stops {
		call := calls[n]
		call.JourneyId = nil
		call.ServiceLabel, call.DirectionKey = ptr(context.Reference), &direction
		own := r.contextOwnPrediction(key, code, now, false)
		context.Calls = append(context.Calls, r.metroContextVisitCalls(metroForecastVisit{call: call, code: code, scope: key, own: own, departure: r.contextOwnPrediction(key, code, now, true)}, points, now)...)
	}

	context.Calls = canonicalMetroForecastCalls(context.Calls)

	return context
}

type metroForecastVisit struct {
	call      api.StopCall
	code      string
	scope     string
	own       *api.CallTimeEvidence
	departure *api.CallTimeEvidence
}

func (r *metroRuntime) metroContextVisitCalls(visit metroForecastVisit, points []metroPoint, now time.Time) []api.StopCall {
	base, own := visit.call, visit.own
	calls := []api.StopCall{}
	for _, p := range points {
		if p.Stop != visit.code || p.Seconds == nil {
			continue
		}
		if c, ok := metroSourceForecast(base, p, now); ok {
			c.Id = metroForecastPointID(visit.scope, p)
			c.MetroForecast = metroForecastEvidence(textValue(base.ServiceLabel), p)
			if source, ok := r.batch.provenance[c.Id]; ok {
				c.MetroForecast.SourceRevisionId = ptr(source.Revision)
				c.MetroForecast.SourceSlot = ptr(source.Slot)
			}
			c.OwnPrediction = own
			c.OwnDeparturePrediction = visit.departure
			calls = append(calls, c)
		}
	}
	if len(calls) == 0 && own != nil {
		base.OwnPrediction = own
		base.OwnDeparturePrediction = visit.departure
		base.MetroForecast = &api.MetroForecastAssociation{SourceReference: base.ServiceLabel, Method: "published", Anchors: []string{}, Platforms: []api.MetroPlatformForecast{}, Limitations: []string{}}
		calls = append(calls, base)
	}
	return calls
}

func metroSourceForecast(c api.StopCall, p metroPoint, now time.Time) (api.StopCall, bool) {
	at, expiry := p.Clock.Add(time.Duration(*p.Seconds)*time.Second), p.Clock.Add(sourceFreshness)
	if !now.Before(expiry) || at.Before(now) {
		return c, false
	}
	c.Arrival = api.CallTime{Kind: "prediction", At: &at, Prediction: &api.CallTimeEvidence{At: at, SourceUpdatedAt: &p.Clock, ValidUntil: &expiry, SourceUrl: metroBase + "/tempoEspera/Estacao/todos"}}
	return c, true
}

// A missing unique whole path cannot erase a valid local platform forecast. It
// never admits a journey; only a uniquely mapped canonical direction is exposed.
func (r *metroRuntime) localForecastContexts(now time.Time) []api.MetroForecastContext {
	local := metroLocalContexts{contexts: map[string]*api.MetroForecastContext{}, stations: map[string]MetroStation{}, runtime: r, now: now, routes: metroRouteIDs(r.plan), catalog: metroStopCatalog(r.plan)}
	for _, station := range r.publication.Stations {
		local.stations[station.ID] = station
	}
	latest := map[string]time.Time{}
	for _, f := range r.batch.localOnly {
		if f.Clock.After(latest[metroLocalForecastKey(f)]) {
			latest[metroLocalForecastKey(f)] = f.Clock
		}
	}
	for _, forecast := range r.batch.localOnly {
		if forecast.Valid && forecast.Clock.Equal(latest[metroLocalForecastKey(forecast)]) {
			local.add(forecast)
		}
	}
	return local.sorted()
}

func metroLocalForecastKey(f metroLocalForecast) string {
	key := metroWaitRowKey(f.Row) + "|" + f.Reference
	if f.Reference == "" {
		key += "|" + strconv.Itoa(f.Slot)
	}
	return key
}

type metroLocalContexts struct {
	runtime  *metroRuntime
	now      time.Time
	contexts map[string]*api.MetroForecastContext
	stations map[string]MetroStation
	routes   map[string]string
	catalog  map[string]api.Stop
}

func (l metroLocalContexts) add(f metroLocalForecast) {
	at, expiry := f.Clock.Add(time.Duration(f.Seconds)*time.Second), f.Clock.Add(sourceFreshness)
	if at.Before(l.now) || !l.now.Before(expiry) {
		return
	}
	station, exists := l.stations[f.Row.Stop]
	if !exists {
		return
	}
	arrival := metroArrival(f.Row, l.stations, l.routes, metroPopupStationID(station, l.catalog))
	if arrival.RouteId == "" {
		return
	}
	context := l.context(f, arrival)
	context.Calls = append(context.Calls, metroLocalForecastCall(f, station, arrival, context))
}

func metroLocalForecastCall(f metroLocalForecast, station MetroStation, arrival api.Arrival, context *api.MetroForecastContext) api.StopCall {
	at, expiry := f.Clock.Add(time.Duration(f.Seconds)*time.Second), f.Clock.Add(sourceFreshness)
	call := api.StopCall{Id: metroForecastID(f.Row, f.Reference), StopId: arrival.StopId, StopName: station.Name, ServiceLabel: ptr(f.Reference), LineKey: arrival.RouteId, DirectionKey: context.DirectionCode, Destination: context.Destination, Phase: "unknown", Departure: missingCallTime("Sem dados de partida")}
	call.Arrival = api.CallTime{Kind: "prediction", At: &at, Prediction: &api.CallTimeEvidence{At: at, SourceUpdatedAt: &f.Clock, ValidUntil: &expiry, SourceUrl: arrival.SourceUrl}}
	point := metroPoint{f.Row.Stop, f.Row.Platform, f.Clock, ptr(f.Seconds)}
	call.MetroForecast = metroForecastEvidence(f.Reference, point)
	call.MetroForecast.SourceRevisionId = ptr(metroWaitRevision(f.Row))
	call.MetroForecast.SourceSlot = ptr(f.Slot + 1)
	call.Id = metroForecastCallID(arrival.RouteId+"|"+f.Row.Destination+"|"+f.Reference, point)
	if f.Reference == "" {
		call.ServiceLabel = nil
		call.Id += ":slot:" + strconv.Itoa(f.Slot)
	}
	return call
}

func (l metroLocalContexts) context(f metroLocalForecast, arrival api.Arrival) *api.MetroForecastContext {
	key := arrival.RouteId + "|" + f.Reference + "|" + f.Row.Destination
	if c := l.contexts[key]; c != nil {
		return c
	}
	c := &api.MetroForecastContext{Reference: f.Reference, RouteId: arrival.RouteId, DirectionCode: localMetroDirection(l.runtime.topology, arrival.RouteId, f.Row.Destination), Destination: arrival.Headsign, Status: "admissible", Reason: "Previsão local; percurso completo por confirmar", Calls: []api.StopCall{}}
	l.contexts[key] = c
	return c
}
func localMetroDirection(topology patterns.Topology, route, destination string) *string {
	candidates := map[string]bool{}
	for _, path := range topology.Patterns {
		if path.Route == route && path.Direction == destination {
			candidates[canonicalMetroDirection(topology, path)] = true
		}
	}
	if len(candidates) != 1 {
		return nil
	}
	for code := range candidates {
		return ptr(code)
	}
	return nil
}
func (l metroLocalContexts) sorted() []api.MetroForecastContext {
	keys := []string{}
	for key := range l.contexts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []api.MetroForecastContext{}
	for _, key := range keys {
		c := l.contexts[key]
		c.Calls = canonicalMetroForecastCalls(c.Calls)
		out = append(out, *c)
	}
	return out
}

// view returns one coherent publication. Journal acknowledgements are visible
// without another source request. Fresh identities disclose pending persistence.
func (r *metroRuntime) view(now time.Time) (*MetroData, *StaticData, []api.MetroForecastContext, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.publication == nil {
		return nil, r.plan, []api.MetroForecastContext{}, r.historyStatus
	}
	data := *r.publication
	data.Topology, data.Trains, data.InventoryOverflow = r.topology, r.current(now), r.inventoryOverflow
	return &data, r.plan, r.forecastContexts(now), r.historyStatus
}
