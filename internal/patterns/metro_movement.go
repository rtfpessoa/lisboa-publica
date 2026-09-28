package patterns

import (
	"fmt"
	"math"
	"time"
)

// MetroMovementCalibration must come from a frozen geometry/replay assessment.
// The publication-only live adapter has no such calibration and leaves departures unknown.
type MetroMovementCalibration struct {
	Version          string  `json:"version"`
	Geometry         string  `json:"geometry"`
	Transform        string  `json:"transform"`
	NoiseMetres      float64 `json:"noise_metres"`
	ResolutionMetres float64 `json:"resolution_metres"`
}

func (c MetroMovementCalibration) Validate() error {
	versioned := c.Version != "" && c.Geometry != "" && c.Transform != ""
	noise := finiteMetroMetres(c.NoiseMetres) && c.NoiseMetres >= 0
	if !versioned || !noise || !positiveMetroMetres(c.ResolutionMetres) {
		return fmt.Errorf("invalid frozen movement calibration")
	}
	return nil
}

type MetroMovementSample struct {
	Journey, Visit, Geometry, Transform string
	SourceAt                            time.Time
	ProgressMetres                      float64
	Valid, Stopped, Corrected, Fallback bool
}
type MetroModelDeparture struct {
	At, LastStoppedAt time.Time
	ModelVersion      string
}

// MetroDepartureDetector deliberately requires admissible modeled movement rather than radius exit.
type MetroDepartureDetector struct {
	Calibration    MetroMovementCalibration
	stopped, last  *MetroMovementSample
	emitted        bool
	journey, visit string
}

func (d *MetroDepartureDetector) Observe(s MetroMovementSample) *MetroModelDeparture {
	if !d.admissible(s) {
		d.clearContinuity()
		return nil
	}
	d.bind(s)
	if !d.advance(s) {
		return nil
	}
	return d.departure(s)
}
func (d *MetroDepartureDetector) clearContinuity() { d.stopped = nil; d.last = nil }
func (d *MetroDepartureDetector) admissible(s MetroMovementSample) bool {
	binding := s.Geometry == d.Calibration.Geometry && s.Transform == d.Calibration.Transform
	identity := s.Journey != "" && s.Visit != "" && !s.SourceAt.IsZero()
	return d.Calibration.Validate() == nil && admissibleMetroMovement(s) && binding && identity
}
func admissibleMetroMovement(s MetroMovementSample) bool {
	return s.Valid && !s.Corrected && !s.Fallback && finiteMetroMetres(s.ProgressMetres)
}
func (d *MetroDepartureDetector) bind(s MetroMovementSample) {
	if s.Journey != d.journey || s.Visit != d.visit {
		d.journey, d.visit = s.Journey, s.Visit
		d.clearContinuity()
		d.emitted = false
	}
}
func (d *MetroDepartureDetector) advance(s MetroMovementSample) bool {
	if d.last != nil && !s.SourceAt.After(d.last.SourceAt) {
		if !s.SourceAt.Equal(d.last.SourceAt) || s.ProgressMetres != d.last.ProgressMetres {
			d.clearContinuity()
		}
		return false
	}
	if d.last != nil && (s.SourceAt.Sub(d.last.SourceAt) > 60*time.Second || s.ProgressMetres < d.last.ProgressMetres) {
		d.clearContinuity()
		return false
	}
	copy := s
	d.last = &copy
	return true
}
func (d *MetroDepartureDetector) departure(s MetroMovementSample) *MetroModelDeparture {
	if s.Stopped {
		copy := s
		d.stopped = &copy
		return nil
	}
	if !d.moved(s) {
		return nil
	}
	d.emitted = true
	return &MetroModelDeparture{At: s.SourceAt, LastStoppedAt: d.stopped.SourceAt, ModelVersion: d.Calibration.Version}
}
func (d *MetroDepartureDetector) moved(s MetroMovementSample) bool {
	if d.stopped == nil || d.emitted {
		return false
	}
	return s.ProgressMetres-d.stopped.ProgressMetres > math.Max(d.Calibration.NoiseMetres, d.Calibration.ResolutionMetres)
}
