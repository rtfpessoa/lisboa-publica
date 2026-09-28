package app

import (
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"strings"
	"time"
)

func (b *metroFrameBuilder) stationForecasts() {
	if b.interest.Stop == "" {
		return
	}
	linked := linkedMetroForecasts(b.frame.Trains)
	for _, a := range predictedArrivals(b.state, b.now, b.now.Add(2*time.Hour), "", b.interest.Stop) {
		ref := strings.TrimPrefix(a.TripId, "metro:")
		if linked[ref+"|"+a.StopId+"|"+a.Headsign] {
			continue
		}
		expiry := a.ObservedAt.Add(sourceFreshness)
		b.frame.UnassociatedForecasts = append(b.frame.UnassociatedForecasts, api.StopCall{Id: a.Id, StopId: a.StopId, ServiceLabel: ptr(ref), LineKey: a.RouteId, DirectionKey: b.forecastDirection(a), Destination: a.Headsign, Phase: "unknown", Arrival: api.CallTime{Kind: "prediction", At: a.ExpectedAt, Prediction: &api.CallTimeEvidence{At: *a.ExpectedAt, SourceUrl: a.SourceUrl, SourceUpdatedAt: a.ObservedAt, ValidUntil: &expiry}}, Departure: missingCallTime("Sem dados de partida")})
	}
}
func linkedMetroForecasts(trains []api.MetroTrain) map[string]bool {
	linked := map[string]bool{}
	for _, t := range trains {
		if t.Association == "supported" {
			linkMetroCalls(linked, t)
		}
	}
	return linked
}
func linkMetroCalls(linked map[string]bool, t api.MetroTrain) {
	for _, c := range t.Calls {
		if c.Arrival.Prediction != nil {
			linked[t.Reference+"|"+c.StopId+"|"+t.Destination] = true
		}
	}
}
func (b *metroFrameBuilder) forecastDirection(a api.Arrival) *string {
	if b.state.Metro == nil || b.static == nil {
		return nil
	}
	candidates := map[string]bool{}
	for _, path := range b.state.Metro.Topology.Patterns {
		if sameMetroRoute(b.static, path.Route, a.RouteId) && b.pathDestinationMatches(path, a.Headsign) {
			candidates[canonicalMetroDirection(b.state.Metro.Topology, path)] = true
		}
	}
	if len(candidates) == 1 {
		for code := range candidates {
			return ptr(code)
		}
	}
	return nil
}
func (b *metroFrameBuilder) pathDestinationMatches(path patterns.Pattern, headsign string) bool {
	for _, station := range b.state.Metro.Stations {
		if station.ID == path.Destination && station.Name == headsign {
			return true
		}
	}
	return false
}

// The direction catalogue uses the same admitted topology as the inventory.
func (b *metroFrameBuilder) stationDirections() {
	if b.interest.Stop == "" || b.state.Metro == nil || b.static == nil {
		return
	}
	seen := map[string]bool{}
	topology := b.state.Metro.Topology
	for _, p := range topology.Patterns {
		calls := metroPathCalls(p, b.state.Metro, b.static, "metro:catalogue")
		direction := canonicalMetroDirection(topology, p)
		key := p.Route + "|" + direction
		if len(metroCallsAtStop(calls, b.interest.Stop)) == 0 || seen[key] {
			continue
		}
		seen[key] = true
		b.frame.Directions = append(b.frame.Directions, b.boardDirection(p, direction))
	}
}
func (b *metroFrameBuilder) boardDirection(p patterns.Pattern, direction string) api.BoardDirection {
	dir := api.BoardDirection{LineKey: p.Route, DirectionKey: ptr(direction), Label: "Destino " + p.Destination, Count: ptr(0)}
	for _, station := range b.state.Metro.Stations {
		if station.ID == destinations[direction] {
			dir.Label = station.Name
		}
	}
	for _, route := range b.static.Routes {
		if sameMetroRoute(b.static, route.Id, p.Route) {
			dir.LineName = route.LongName
			dir.Color = route.Color
			break
		}
	}
	for _, t := range b.frame.Trains {
		if t.RouteId == p.Route && t.DirectionCode == direction && t.Association == "supported" {
			*dir.Count++
		}
	}
	return dir
}
