package app

import (
	"lisboapublica/internal/api"
	"time"
)

func metroVehicleDestination(state *State, vehicle *api.Vehicle, now time.Time) string {
	if state.Metro == nil || state.Metro.Status.Status != "ok" {
		return ""
	}
	destinationsSeen := map[string]bool{}
	resolver := newMetroDestinationResolver(state.Metro, state.Metro.Topology)
	for _, wait := range state.Metro.Waits {
		if wait.Train != vehicle.SourceId && wait.Train2 != vehicle.SourceId && wait.Train3 != vehicle.SourceId {
			continue
		}
		at, err := time.ParseInLocation("20060102150405", wait.At, lisbon)
		if err != nil || now.Sub(at) > sourceFreshness || at.After(now.Add(providerClockSkew)) {
			continue
		}
		if destination := resolver.resolve(wait.Destination); destination.named {
			destinationsSeen[destination.name] = true
		}
	}
	if len(destinationsSeen) == 1 {
		for name := range destinationsSeen {
			return name
		}
	}
	return ""
}
