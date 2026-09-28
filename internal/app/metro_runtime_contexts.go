package app

import (
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"sort"
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
	if key == "" || s.batch.rejected[key] {
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
	if s.supportedPrior(prior) {
		return prior
	}
	if prior == "" && len(valid) == 1 {
		return valid[0]
	}
	if prior == "" && len(valid) == 0 && len(present) == 1 && len(s.batch.references[scope]) == 1 {
		return present[0]
	}
	s.suspend(scope, len(valid))
	return ""
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

	return out
}

func (r *metroRuntime) pathForecastContext(key string, now time.Time) api.MetroForecastContext {
	path := r.batch.paths[key]
	parts := strings.Split(key, "|")
	direction := canonicalMetroDirection(r.topology, path)
	context := api.MetroForecastContext{Reference: parts[len(parts)-1], RouteId: path.Route, DirectionCode: &direction, Destination: metroDestinationName(r.publication, path), Status: "admissible", Calls: []api.StopCall{}}
	if r.batch.rejected[key] {
		context.Status = "incompatible"
		context.Reason = "Dados incompatíveis nesta direção"
		return context
	}
	points, _ := latestMetroPoints(r.batch.groups[key])
	calls := metroPathCalls(path, r.publication, r.plan, "metro:forecast:"+key)
	for n, code := range path.Stops {
		p, found := points[code]
		if !found || p.Seconds == nil {
			continue
		}
		if c, ok := metroSourceForecast(calls[n], p, now); ok {
			c.JourneyId = nil
			c.ServiceLabel, c.DirectionKey = ptr(context.Reference), &direction
			context.Calls = append(context.Calls, c)
		}
	}
	return context
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
	for _, forecast := range r.batch.localOnly {
		local.add(forecast)
	}
	return local.sorted()
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
	call := api.StopCall{Id: metroForecastID(f.Row, f.Reference), StopId: arrival.StopId, StopName: station.Name, ServiceLabel: ptr(f.Reference), LineKey: arrival.RouteId, DirectionKey: context.DirectionCode, Destination: context.Destination, Phase: "unknown", Departure: missingCallTime("Sem dados de partida")}
	call.Arrival = api.CallTime{Kind: "prediction", At: &at, Prediction: &api.CallTimeEvidence{At: at, SourceUpdatedAt: &f.Clock, ValidUntil: &expiry, SourceUrl: arrival.SourceUrl}}
	context.Calls = append(context.Calls, call)
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
		sort.Slice(c.Calls, func(i, j int) bool { return c.Calls[i].Id < c.Calls[j].Id })
		out = append(out, *c)
	}
	return out
}

// view returns one coherent publication. Journal acknowledgements are visible
// without another source request, while provisional identities remain private.
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
