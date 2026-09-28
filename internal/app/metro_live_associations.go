package app

import (
	"lisboapublica/internal/api"
	"time"
)

func (b *metroFrameBuilder) associateVehicles() {
	counts := map[string]int{}
	for _, t := range b.frame.Trains {
		if t.Association == "supported" && b.now.Before(t.ValidUntil) {
			counts[t.RouteId+"|"+t.Reference]++
		}
	}
	for _, v := range navigationVehicles(b.state, "metro", b.now) {
		if !b.includeVehicle(v) {
			continue
		}
		v.VehicleRef = nil // Scoped frames own their atomic records instead of global navigation cursors.
		b.frame.Vehicles = append(b.frame.Vehicles, v)
		b.linkVehicle(v, counts)
	}
}
func (b *metroFrameBuilder) includeVehicle(v api.Vehicle) bool {
	if b.interest.Route == "" || v.Id == b.interest.Vehicle {
		return true
	}
	return v.RouteId != nil && sameMetroRoute(b.static, *v.RouteId, b.interest.Route)
}
func (b *metroFrameBuilder) linkVehicle(v api.Vehicle, counts map[string]int) {
	for n := range b.frame.Trains {
		t := &b.frame.Trains[n]
		if counts[t.RouteId+"|"+t.Reference] != 1 || !b.vehicleMatches(v, *t) {
			continue
		}
		t.VehicleId = ptr(v.Id)
		if b.interest.Journey == "" && b.interest.Vehicle == v.Id {
			b.frame.SelectedJourneyId = ptr(t.JourneyId)
		}
	}
}
func (b *metroFrameBuilder) vehicleMatches(v api.Vehicle, t api.MetroTrain) bool {
	fresh := !v.LastKnown && !v.Stale && b.now.Sub(v.ObservedAt) <= sourceFreshness
	supported := t.Association == "supported" && b.now.Before(t.ValidUntil)
	if !fresh || !supported || v.RouteId == nil {
		return false
	}
	return v.SourceId == t.Reference && sameMetroRoute(b.static, *v.RouteId, t.RouteId)

}
func (b *metroFrameBuilder) scopeCalls() {
	selected := textValue(b.frame.SelectedJourneyId)
	for n := range b.frame.Trains {
		t := &b.frame.Trains[n]
		if t.Association == "supported" && (!b.now.Before(t.ValidUntil) || b.frame.Status.Status != "ok") {
			t.Association = "suspended"
			t.NextIndex = nil
			t.CurrentIndex = nil
			t.Reason = "Suporte atual indisponível"
		}
		for k := range t.Calls {
			expireMetroCall(&t.Calls[k], t.Association, b.now)
		}
		if b.interest.Stop != "" {
			t.Calls = metroCallsAtStop(t.Calls, b.interest.Stop)
		} else if t.JourneyId != selected {
			t.Calls = []api.StopCall{}
		}
	}
}
func expireMetroCall(c *api.StopCall, association api.MetroTrainAssociation, now time.Time) {
	if c.Arrival.Prediction != nil && !metroCallPredictionFresh(c.Arrival.Prediction, association, now) {
		if c.Arrival.Kind == "prediction" {
			c.Arrival = missingCallTime("Sem previsão atual")
		}
	}
	if association != "supported" || c.OwnPrediction != nil && !metroCallPredictionFresh(c.OwnPrediction, association, now) {
		c.OwnPrediction = nil
	}
}
func metroCallPredictionFresh(p *api.CallTimeEvidence, association api.MetroTrainAssociation, now time.Time) bool {
	return association == "supported" && p.ValidUntil != nil && now.Before(*p.ValidUntil) && !now.After(p.At)
}
func metroCallsAtStop(calls []api.StopCall, id string) []api.StopCall {
	out := []api.StopCall{}
	for _, c := range calls {
		if c.StopId == id {
			out = append(out, c)
		}
	}
	return out
}
