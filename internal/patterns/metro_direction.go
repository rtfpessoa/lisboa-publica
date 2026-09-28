package patterns

import (
	"math"
	"time"
)

// MetroAxisPosition is on a fixed canonical axis, not a trajectory whose sign
// was chosen from the destination being tested. Opaque Hub estimates are ineligible.
type MetroAxisPosition struct {
	Geometry                                             string
	SourceAt                                             time.Time
	Metres                                               float64
	NoiseMetres, ResolutionMetres                        float64
	Qualified, DirectionIndependent, Corrected, Fallback bool
}

// MetroDirectionConfirmation retains separate first-step and confirmation clocks.
type MetroDirectionConfirmation struct {
	Sign                         int
	FirstMovementAt, ConfirmedAt time.Time
	Geometry                     string
}

// MetroDirectionDetector admits only three-source-position fixed-axis chains.
type MetroDirectionDetector struct {
	last               *MetroAxisPosition
	sign, steps, known int
	first              time.Time
	geometry           string
}

// Reset discards all live continuity, including the previously known sign.
func (d *MetroDirectionDetector) Reset() { *d = MetroDirectionDetector{} }
func (d *MetroDirectionDetector) clearCandidate() {
	d.last = nil
	d.sign = 0
	d.steps = 0
	d.first = time.Time{}
}

// Observe processes original-source positions; repeated/render samples cannot advance it.
func (d *MetroDirectionDetector) Observe(p MetroAxisPosition) *MetroDirectionConfirmation {
	if !qualifiedMetroAxis(p) {
		d.clearCandidate()
		return nil
	}
	if d.geometry != p.Geometry {
		d.Reset()
		d.geometry = p.Geometry
	}
	if d.last == nil {
		copy := p
		d.last = &copy
		return nil
	}
	old := *d.last
	if !d.advance(p, old) {
		return nil
	}
	return d.confirm(p, old)
}
func (d *MetroDirectionDetector) advance(p, old MetroAxisPosition) bool {
	if !p.SourceAt.After(old.SourceAt) {
		if !p.SourceAt.Equal(old.SourceAt) || p.Metres != old.Metres {
			d.clearCandidate()
		}
		return false
	}
	copy := p
	d.last = &copy
	if p.SourceAt.Sub(old.SourceAt) > 60*time.Second {
		d.sign = 0
		d.steps = 0
		d.known = 0
		return false
	}
	return true
}
func (d *MetroDirectionDetector) confirm(p, old MetroAxisPosition) *MetroDirectionConfirmation {

	delta := p.Metres - old.Metres
	envelope := math.Max(math.Max(p.NoiseMetres, p.ResolutionMetres), math.Max(old.NoiseMetres, old.ResolutionMetres))
	if math.Abs(delta) <= envelope {
		d.sign = 0
		d.steps = 0
		return nil
	}
	sign := 1
	if delta < 0 {
		sign = -1
	}
	if sign != d.sign {
		d.sign = sign
		d.steps = 0
		d.first = p.SourceAt
	}
	d.steps++
	if d.steps < 2 {
		return nil
	}
	if d.known == sign {
		return nil
	}
	d.known = sign
	return &MetroDirectionConfirmation{Sign: sign, FirstMovementAt: d.first, ConfirmedAt: p.SourceAt, Geometry: p.Geometry}
}
func qualifiedMetroAxis(p MetroAxisPosition) bool {
	identity := p.Geometry != "" && !p.SourceAt.IsZero()
	numeric := finiteMetroMetres(p.Metres) && finiteMetroMetres(p.NoiseMetres) && p.NoiseMetres >= 0 && positiveMetroMetres(p.ResolutionMetres)
	return identity && numeric && independentMetroAxis(p)
}
func independentMetroAxis(p MetroAxisPosition) bool {
	return p.Qualified && p.DirectionIndependent && !p.Corrected && !p.Fallback
}
