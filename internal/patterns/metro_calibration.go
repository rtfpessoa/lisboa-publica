package patterns

import (
	"time"
)

// MetroCalibrationDataset is an offline collection contract, not an occurrence adapter.
type MetroCalibrationDataset struct {
	Kind                 string                      `json:"kind"`
	Version              string                      `json:"version"`
	Geometry             string                      `json:"geometry"`
	Transform            string                      `json:"transform"`
	SourceProvenance     string                      `json:"source_provenance"`
	ResolutionMetres     float64                     `json:"resolution_metres"`
	ResolutionProvenance string                      `json:"resolution_provenance"`
	Samples              []MetroCalibrationSample    `json:"samples"`
	References           []MetroCalibrationReference `json:"references"`
}
type MetroCalibrationSample struct {
	Journey        string    `json:"journey"`
	Visit          string    `json:"visit"`
	Split          string    `json:"split"`
	EvidenceRef    string    `json:"evidence_ref"`
	SourceAt       time.Time `json:"source_at"`
	ReceivedAt     time.Time `json:"received_at"`
	ProgressMetres float64   `json:"progress_metres"`
	Valid          bool      `json:"valid"`
	Stopped        bool      `json:"stopped"`
	Corrected      bool      `json:"corrected"`
	Fallback       bool      `json:"fallback"`
}
type MetroCalibrationReference struct {
	Journey              string    `json:"journey"`
	Visit                string    `json:"visit"`
	Kind                 string    `json:"kind"`
	Provenance           string    `json:"provenance"`
	StoppedFrom          time.Time `json:"stopped_from"`
	StoppedThrough       time.Time `json:"stopped_through"`
	FirstMovementFrom    time.Time `json:"first_movement_from"`
	FirstMovementThrough time.Time `json:"first_movement_through"`
}
type MetroCalibrationOutcome struct {
	Journey          string     `json:"journey"`
	Visit            string     `json:"visit"`
	Classification   string     `json:"classification"`
	DetectedAt       *time.Time `json:"detected_at"`
	ReferenceFrom    time.Time  `json:"reference_from"`
	ReferenceThrough time.Time  `json:"reference_through"`
}
type MetroCalibrationAssessment struct {
	Kind                 string                    `json:"kind"`
	Candidate            MetroMovementCalibration  `json:"candidate"`
	LiveEnabled          bool                      `json:"live_enabled"`
	ReviewRequired       bool                      `json:"review_required"`
	TrainingStoppedPairs int                       `json:"training_stopped_pairs"`
	TrainingJourneys     int                       `json:"training_journeys"`
	HoldoutJourneys      int                       `json:"holdout_journeys"`
	Outcomes             []MetroCalibrationOutcome `json:"holdout_outcomes"`
}

// AssessMetroCalibration never enables live departures. Provenance is an operator assertion.
// Entire journeys belong to one split; holdout labels never influence the fitted threshold.
func AssessMetroCalibration(in MetroCalibrationDataset) (MetroCalibrationAssessment, error) {
	collection, err := prepareMetroCalibration(in)
	if err != nil {
		return MetroCalibrationAssessment{}, err
	}
	out, err := collection.fit(in)
	if err != nil {
		return MetroCalibrationAssessment{}, err
	}
	out.Outcomes = collection.assessHoldout(in, out.Candidate)
	return out, nil
}
