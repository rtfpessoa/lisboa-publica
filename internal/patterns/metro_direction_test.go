package patterns

import (
	"testing"
	"time"
)

// Synthetic controls exercise the gate; they do not qualify a live transform.
func TestMetroDirectionThreeOriginalAxisPositions(t *testing.T) {
	at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	sample := func(n int, m float64) MetroAxisPosition {
		return MetroAxisPosition{Geometry: "synthetic-axis", SourceAt: at.Add(time.Duration(n) * time.Second), Metres: m, NoiseMetres: 2, ResolutionMetres: 3, Qualified: true, DirectionIndependent: true}
	}
	var d MetroDirectionDetector
	if d.Observe(sample(0, 0)) != nil || d.Observe(sample(1, 10)) != nil {
		t.Fatal("confirmed before three positions")
	}
	// Render/repeated fetches do not advance confirmation.
	if d.Observe(sample(1, 10)) != nil {
		t.Fatal("duplicate counted")
	}
	c := d.Observe(sample(2, 20))
	if c == nil || c.Sign != 1 || !c.FirstMovementAt.Equal(at.Add(time.Second)) || !c.ConfirmedAt.Equal(at.Add(2*time.Second)) {
		t.Fatal(c)
	}
	if d.Observe(sample(3, 20)) != nil || d.Observe(sample(4, 10)) != nil {
		t.Fatal("stop or first reverse step confirmed")
	}
	c = d.Observe(sample(5, 0))
	if c == nil || c.Sign != -1 {
		t.Fatal("reverse not confirmed", c)
	}
}
func TestMetroDirectionExcludedEvidenceBreaksChain(t *testing.T) {
	for _, kind := range []string{"assumed", "fallback", "correction", "gap", "subthreshold", "opposite", "geometry", "regression"} {
		t.Run(kind, func(t *testing.T) {
			at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
			p := MetroAxisPosition{Geometry: "synthetic", SourceAt: at, Metres: 0, NoiseMetres: 2, ResolutionMetres: 3, Qualified: true, DirectionIndependent: true}
			var d MetroDirectionDetector
			d.Observe(p)
			p.SourceAt = at.Add(time.Second)
			p.Metres = 10
			switch kind {
			case "assumed":
				p.DirectionIndependent = false
			case "fallback":
				p.Fallback = true
			case "correction":
				p.Corrected = true
			case "gap":
				p.SourceAt = at.Add(61 * time.Second)
			case "subthreshold":
				p.Metres = 2
			case "opposite":
				p.Metres = -10
			case "geometry":
				p.Geometry = "different"
			case "regression":
				p.SourceAt = at.Add(-time.Second)
			}
			if d.Observe(p) != nil {
				t.Fatal("premature")
			}
			p.SourceAt = p.SourceAt.Add(time.Second)
			p.Metres = 20
			p.DirectionIndependent = true
			p.Corrected = false
			p.Fallback = false
			if d.Observe(p) != nil {
				t.Fatal("invalid chain confirmed")
			}
		})
	}
}
