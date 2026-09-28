package patterns

import (
	"fmt"
	"math"
)

func (c *metroCalibrationCollection) fit(in MetroCalibrationDataset) (MetroCalibrationAssessment, error) {
	out := MetroCalibrationAssessment{Kind: in.Kind, ReviewRequired: true, TrainingJourneys: len(c.train), HoldoutJourneys: len(c.holdout), Outcomes: []MetroCalibrationOutcome{}}
	out.Candidate = MetroMovementCalibration{Version: in.Version, Geometry: in.Geometry, Transform: in.Transform, ResolutionMetres: in.ResolutionMetres}
	for _, k := range c.keys {
		g := c.groups[k]
		if g[0].Split == "train" {
			fitMetroStoppedVisit(&out, g, c.refs[k])
		}
	}
	if err := out.Candidate.Validate(); err != nil {
		return MetroCalibrationAssessment{}, err
	}
	if out.TrainingStoppedPairs == 0 {
		return MetroCalibrationAssessment{}, fmt.Errorf("training requires original admissible stopped pairs within independently referenced stop windows")
	}
	return out, nil
}
func fitMetroStoppedVisit(out *MetroCalibrationAssessment, g []MetroCalibrationSample, r MetroCalibrationReference) {
	for i := 1; i < len(g); i++ {
		a, b := g[i-1], g[i]
		if metroStoppedPair(a, b, r) {
			// Backwards jitter contributes to the envelope; the detector rejects backwards progress separately.
			out.Candidate.NoiseMetres = math.Max(out.Candidate.NoiseMetres, math.Abs(b.ProgressMetres-a.ProgressMetres))
			out.TrainingStoppedPairs++
		}
	}
}
func (c *metroCalibrationCollection) assessHoldout(in MetroCalibrationDataset, candidate MetroMovementCalibration) []MetroCalibrationOutcome {
	out := []MetroCalibrationOutcome{}
	for _, k := range c.keys {
		g := c.groups[k]
		if g[0].Split == "holdout" {
			out = append(out, assessMetroVisit(in, candidate, g, c.refs[k]))
		}
	}
	return out
}
func assessMetroVisit(in MetroCalibrationDataset, candidate MetroMovementCalibration, g []MetroCalibrationSample, r MetroCalibrationReference) MetroCalibrationOutcome {
	d := MetroDepartureDetector{Calibration: candidate}
	result := MetroCalibrationOutcome{Journey: r.Journey, Visit: r.Visit, Classification: "not_detected", ReferenceFrom: r.FirstMovementFrom, ReferenceThrough: r.FirstMovementThrough}
	for _, s := range g {
		event := d.Observe(MetroMovementSample{Journey: s.Journey, Visit: s.Visit, Geometry: in.Geometry, Transform: in.Transform, SourceAt: s.SourceAt, ProgressMetres: s.ProgressMetres, Valid: s.Valid, Stopped: s.Stopped, Corrected: s.Corrected, Fallback: s.Fallback})
		if event != nil {
			result.DetectedAt = &event.At
			result.Classification = classifyMetroMovement(*event, r)
			break
		}
	}
	return result
}
func classifyMetroMovement(event MetroModelDeparture, r MetroCalibrationReference) string {
	if event.At.Before(r.FirstMovementFrom) {
		return "early"
	}
	if event.At.After(r.FirstMovementThrough) {
		return "late"
	}
	return "within_reference_window"
}
