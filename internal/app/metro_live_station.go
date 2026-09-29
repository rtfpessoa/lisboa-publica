package app

import (
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
	"strings"
	"time"
)

func (b *metroFrameBuilder) stationForecasts() {
	linked := linkedMetroForecastsAt(b.frame.Trains, b.now)
	for _, context := range b.contexts {
		if !b.includeForecastContext(context) {
			continue
		}
		if b.interest.Stop != "" {
			context.Calls = metroCallsAtStop(context.Calls, b.interest.Stop)
		}
		if context.Status == "admissible" && len(context.Calls) == 0 {
			continue
		}
		b.addForecastContext(context, linked)
	}
	if b.interest.Vehicle != "" && b.frame.SelectedJourneyId == nil {
		b.frame.AssociationReason = ptr(metroForecastAssociationReason(*b.frame.ForecastContexts))
	}
}
func (b *metroFrameBuilder) includeForecastContext(c api.MetroForecastContext) bool {
	if b.interest.Route != "" && !sameMetroRoute(b.static, b.interest.Route, c.RouteId) {
		return false
	}
	if b.interest.Vehicle != "" && !b.contextMatchesVehicle(c) {
		return false
	}
	return b.interest.Stop != "" || b.interest.Vehicle != ""
}
func (b *metroFrameBuilder) addForecastContext(context api.MetroForecastContext, linked map[string]bool) {
	if b.interest.Vehicle != "" {
		*b.frame.ForecastContexts = append(*b.frame.ForecastContexts, context)
	}
	if b.interest.Stop == "" {
		return
	}
	for _, c := range context.Calls {
		if !linked[context.RouteId+"|"+context.Reference+"|"+c.StopId+"|"+context.Destination] {
			b.frame.UnassociatedForecasts = append(b.frame.UnassociatedForecasts, c)
		}
	}
}
func metroForecastAssociationReason(contexts []api.MetroForecastContext) string {
	count, incompatible := 0, false
	for _, c := range contexts {
		if c.Status == "admissible" {
			count++
		} else if c.Status == "incompatible" {
			incompatible = true
		}
	}
	if count > 1 {
		return "Várias viagens possíveis"
	}
	if count == 0 && incompatible {
		return "Dados incompatíveis nesta direção"
	}
	return "Sem dados atuais para confirmar a viagem"
}

func (b *metroFrameBuilder) contextMatchesVehicle(context api.MetroForecastContext) bool {
	for _, v := range b.frame.Vehicles {
		if v.Id == b.interest.Vehicle && strings.TrimSpace(v.SourceId) == context.Reference && v.RouteId != nil && sameMetroRoute(b.static, *v.RouteId, context.RouteId) {
			return true
		}
	}
	for _, t := range b.frame.Trains {
		if t.JourneyId == b.interest.Journey && t.Reference == context.Reference && sameMetroRoute(b.static, t.RouteId, context.RouteId) {
			return true
		}
	}
	return false
}
func linkedMetroForecastsAt(trains []api.MetroTrain, now time.Time) map[string]bool {
	linked := map[string]bool{}
	for _, t := range trains {
		if metroCurrentDirection(t, now) {
			linkMetroCalls(linked, t)
		}
	}
	return linked
}
func linkMetroCalls(linked map[string]bool, t api.MetroTrain) {
	for _, c := range t.Calls {
		if c.Arrival.Prediction != nil || c.OwnPrediction != nil {
			linked[t.RouteId+"|"+t.Reference+"|"+c.StopId+"|"+t.Destination] = true
		}
	}
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
		if t.RouteId == p.Route && t.DirectionCode == direction && metroCurrentDirection(t, b.now) {
			*dir.Count++
		}
	}
	return dir
}
