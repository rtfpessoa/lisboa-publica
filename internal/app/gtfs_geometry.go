package app

import (
	"lisboapublica/internal/api"
	"time"
)

func stripGeometry(d *StaticData) {
	d.Shapes = nil
	d.GeometryUpdated = nil
	d.GeometryPartial = false
	for i := range d.Routes {
		d.Routes[i].Geometry = nil
	}
}

// A stale fallback is allowed only for the same plan and still-referenced routes/shapes.
func prepareGTFSGeometry(d, old *StaticData, op api.Operator) {
	if !geometryCacheFits(d, staticHealth(op, d)) {
		stripGeometry(d)
		d.GeometryError = ptr("Percursos excedem o limite de armazenamento por atualização.")
	}
	if len(d.Shapes) > 0 {
		return
	}
	message := d.GeometryError
	if message == nil {
		message = ptr("Percursos oficiais indisponíveis; horários disponíveis.")
	}
	if old != nil && old.PlanID == d.PlanID && d.Schedule != nil {
		d.reuseReferencedGeometry(old, op.Id)
	}

	d.GeometryPartial = false
	d.GeometryError = message
	if !geometryCacheFits(d, staticHealth(op, d)) {
		stripGeometry(d)
		d.GeometryError = ptr("Percursos indisponíveis: excedem o limite de armazenamento por atualização.")
	}
}

func (d *StaticData) reuseReferencedGeometry(old *StaticData, operator string) {
	refs := map[string]bool{}
	routes := map[string]bool{}
	for _, r := range d.Routes {
		routes[r.Id] = true
	}
	for _, trip := range d.Schedule.Trips {
		if trip.Shape != "" {
			refs[qualify(operator, trip.Route)+"\x00"+trip.Shape] = true
		}
	}
	for _, shape := range old.Shapes {
		if shape.PlanId == d.PlanID && routes[shape.RouteId] && refs[shape.RouteId+"\x00"+shape.ShapeId] {
			d.Shapes = append(d.Shapes, shape)
		}
	}
	if len(d.Shapes) > 0 {
		d.GeometryUpdated = old.GeometryUpdated
		d.attachRepresentativeGeometry()
	}
}

func (d *StaticData) attachRepresentativeGeometry() {
	for i := range d.Routes {
		for _, shape := range d.Shapes {
			if shape.RouteId == d.Routes[i].Id {
				coords := shape.Geometry
				d.Routes[i].Geometry = &coords
				break
			}
		}
	}
}

// Pre-overlay caches have never attempted geometry. Refresh those once through
// the existing collector; recorded absence/errors still use the normal TTL.
func reusableStaticCache(p provider, state *State) bool {
	op := state.Operators[p.ID]
	fresh := op.StaticStatus == "ok" && op.StaticUpdatedAt != nil && time.Since(*op.StaticUpdatedAt) < staticCacheLifetime
	data := state.Static[p.ID]
	legacyEndpoints := legacyCPMetadata(p, data)
	return fresh && !legacyEndpoints && !legacyRailFerryGeometry(p, data)
}

func legacyRailFerryGeometry(p provider, d *StaticData) bool {
	if p.Mode != "train" && p.Mode != "ferry" {
		return false
	}
	return d != nil && len(d.Shapes) == 0 && d.GeometryUpdated == nil && d.GeometryError == nil
}

func legacyCPMetadata(p provider, data *StaticData) bool {
	if p.ID != "cp" || data == nil {
		return false
	}
	return !data.CPJourneyEndpoints || !data.CPPredictionMetadata
}
