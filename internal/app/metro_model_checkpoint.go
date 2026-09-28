package app

import "lisboapublica/internal/patterns"

// Frozen parameters and the most recent original-source inputs are historical
// evidence only. Recovery never installs detector state or direction candidates.
type metroCheckpointModel struct {
	Calibration    patterns.MetroMovementCalibration       `json:"calibration"`
	Axis           []patterns.MetroModelStation            `json:"axis"`
	SegmentSeconds []float64                               `json:"segment_seconds"`
	EvidenceSHA256 string                                  `json:"evidence_sha256"`
	ReviewedBy     string                                  `json:"reviewed_by"`
	Samples        map[string]patterns.MetroMovementSample `json:"samples"`
}

func (r *metroRuntime) checkpointModel(t *metroTrack) *metroCheckpointModel {
	model, ok := r.trackModel(t)
	if !ok {
		return nil
	}
	evidence := &metroCheckpointModel{Calibration: model.Calibration, Axis: model.Admission.Axis, SegmentSeconds: model.Admission.SegmentSeconds, EvidenceSHA256: model.Admission.EvidenceSHA256, ReviewedBy: model.Admission.ReviewedBy}
	if t.Movement != nil {
		evidence.Samples = t.Movement.Last
	}
	return evidence
}
