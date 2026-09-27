package app

import (
	"context"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"time"
)

func (f *Fetcher) publishMetadata(ctx context.Context, id string, d *StaticData, rows []publishedMetadata) {
	copyData := mergePublishedMetadata(d, id, rows)
	explicit := mergePublishedMetadata(&StaticData{Models: map[string]Metadata{}}, id, rows)
	f.Store.PublishMu.Lock()
	defer f.Store.PublishMu.Unlock()
	op := f.Cache.operator(id)
	if e := f.Store.stageMetadataFacts(ctx, id, explicit.Models, factInput{Source: f.Hub + "/vehicles/metadata", ConfirmedAt: time.Now().UTC(), Priority: 1}); e != nil {
		f.Log.Warn("fleet facts unavailable", zap.String("operator", id), zap.Error(e))
	}
	current, _ := f.Cache.state("")
	projection, projectionErr := f.Store.factProjection(ctx, id, current.Live[id])
	if projectionErr != nil {
		f.Log.Warn("fleet fact projection unavailable", zap.String("operator", id), zap.Error(projectionErr))
	}
	if e := f.Store.Save(ctx, id, copyData, nil, op, nil); e == nil {
		f.Cache.update(id, copyData, projection, op)
	}
}

func normalizeVehicleAttributes(vehicles []api.Vehicle) []api.Vehicle {
	vehicles = append([]api.Vehicle(nil), vehicles...)
	for i := range vehicles {
		v := &vehicles[i]
		m := stableVehicleMetadata(*v)
		v.Model, v.LicensePlate, v.Typology, v.Propulsion = optional(m.Model), optional(m.Plate), optional(m.Typology), optional(m.Propulsion)
		v.SeatedCapacity, v.TotalCapacity = m.SeatedCapacity, m.TotalCapacity
	}
	return vehicles
}

func (f *Fetcher) prepareVehiclePublication(ctx context.Context, id string, state *State, vehicles []api.Vehicle, now time.Time) (*LiveData, map[string]*float64) {
	factsErr := f.Store.stageVehicleFacts(ctx, id, vehicles, state.Live[id], now)
	if factsErr != nil {
		f.Log.Warn("vehicle facts unavailable", zap.String("operator", id), zap.Error(factsErr))
	} else {
		// Resolve verified registration context before considering a movement pair.
		f.Store.enrichFacts(id, vehicles)
	}
	live, dist := nextLive(state.Live[id], state.Operators[id], vehicles, now)
	if factsErr == nil {
		f.Store.enrichFacts(id, live.Vehicles)
		f.Store.enrichFacts(id, live.Samples)
		f.Store.enrichFacts(id, live.LastKnown)
	}
	f.Log.Info("vehicle publication", zap.String("operator", id), zap.Int("published", len(vehicles)), zap.Int("accepted", len(live.Vehicles)), zap.Int("rejected_regressions", len(vehicles)-len(live.Vehicles)), zap.Int("retained", len(live.LastKnown)))

	return live, dist
}
