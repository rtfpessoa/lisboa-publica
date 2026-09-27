package app

import (
	"lisboapublica/internal/api"
	"time"
)

func metroVehicleDestination(state *State, vehicle *api.Vehicle, now time.Time) string {
	if state.Metro == nil || state.Metro.Status.Status != "ok" {
		return ""
	}
	stations := map[string]MetroStation{}
	for _, station := range state.Metro.Stations {
		stations[station.ID] = station
	}
	destinationsSeen := map[string]bool{}
	for _, wait := range state.Metro.Waits {
		if wait.Train != vehicle.SourceId && wait.Train2 != vehicle.SourceId && wait.Train3 != vehicle.SourceId {
			continue
		}
		at, err := time.ParseInLocation("20060102150405", wait.At, lisbon)
		if err != nil || now.Sub(at) > sourceFreshness || at.After(now.Add(providerClockSkew)) {
			continue
		}
		if station, ok := stations[destinations[wait.Destination]]; ok && station.Name != "" {
			destinationsSeen[station.Name] = true
		}
	}
	if len(destinationsSeen) == 1 {
		for name := range destinationsSeen {
			return name
		}
	}
	return ""
}
