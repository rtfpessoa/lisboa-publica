package patterns

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func calibrationFixture(t *testing.T) MetroCalibrationDataset {
	t.Helper()
	raw, err := os.ReadFile("testdata/metro-calibration-synthetic.json")
	if err != nil {
		t.Fatal(err)
	}
	var in MetroCalibrationDataset
	if err = json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	return in
}
func TestMetroCalibrationFrozenHoldoutFirstMovement(t *testing.T) {
	in := calibrationFixture(t)
	out, err := AssessMetroCalibration(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.LiveEnabled || !out.ReviewRequired || out.Candidate.NoiseMetres != .25 || out.TrainingStoppedPairs != 1 || len(out.Outcomes) != 1 || out.Outcomes[0].Classification != "within_reference_window" || !out.Outcomes[0].DetectedAt.Equal(in.References[1].FirstMovementFrom) {
		t.Fatalf("unexpected assessment: %+v", out)
	}
	// Holdout progress cannot alter a threshold fitted from training journeys.
	in.Samples[4].ProgressMetres = 100
	other, err := AssessMetroCalibration(in)
	if err != nil {
		t.Fatal(err)
	}
	if other.Candidate != out.Candidate {
		t.Fatal("holdout leaked into training")
	}
}
func TestMetroCalibrationRejectsUnsupportedEvidence(t *testing.T) {
	cases := map[string]func(*MetroCalibrationDataset){
		"no independent evidence": func(d *MetroCalibrationDataset) { d.Kind = "observed" },
		"same source truth": func(d *MetroCalibrationDataset) {
			d.Kind = "observed"
			for i := range d.References {
				d.References[i].Kind = "same-source"
			}
		},
		"journey split leak":         func(d *MetroCalibrationDataset) { d.Samples[1].Split = "holdout" },
		"repeated original clock":    func(d *MetroCalibrationDataset) { d.Samples[1].SourceAt = d.Samples[0].SourceAt },
		"receipt before publication": func(d *MetroCalibrationDataset) { d.Samples[1].ReceivedAt = d.Samples[0].SourceAt },
		"missing raw evidence":       func(d *MetroCalibrationDataset) { d.Samples[0].EvidenceRef = "" },
		"no training stopped pair":   func(d *MetroCalibrationDataset) { d.Samples[0].Fallback = true },
		"missing geometry version":   func(d *MetroCalibrationDataset) { d.Geometry = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in := calibrationFixture(t)
			mutate(&in)
			if _, err := AssessMetroCalibration(in); err == nil {
				t.Fatal("unsupported evidence admitted")
			}
		})
	}
}
func TestMetroCalibrationCorrectedMovementIsNotDeparture(t *testing.T) {
	in := calibrationFixture(t)
	in.Samples[6].Corrected = true
	out, err := AssessMetroCalibration(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcomes[0].DetectedAt != nil || out.Outcomes[0].Classification != "not_detected" {
		t.Fatal("correction fabricated a departure", out.Outcomes)
	}
}

func TestMetroCalibrationObservedInputStillRequiresReview(t *testing.T) {
	in := calibrationFixture(t)
	in.Kind = "observed"
	for i := range in.References {
		in.References[i].Kind = "independent"
		in.References[i].Provenance = "Synthetic test assertion of independent provenance"
	}
	out, err := AssessMetroCalibration(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.LiveEnabled || !out.ReviewRequired {
		t.Fatal("offline assessment enabled live departures")
	}
}
func TestMetroCalibrationReportsTimingDisagreement(t *testing.T) {
	for _, classification := range []string{"early", "late"} {
		t.Run(classification, func(t *testing.T) {
			in := calibrationFixture(t)
			r := &in.References[1]
			if classification == "early" {
				r.FirstMovementFrom = r.FirstMovementFrom.Add(2 * time.Second)
				r.FirstMovementThrough = r.FirstMovementThrough.Add(2 * time.Second)
			} else {
				r.FirstMovementFrom = r.FirstMovementFrom.Add(-time.Second)
				r.FirstMovementThrough = r.FirstMovementFrom
			}
			out, err := AssessMetroCalibration(in)
			if err != nil {
				t.Fatal(err)
			}
			if out.Outcomes[0].Classification != classification {
				t.Fatal("timing disagreement omitted", out.Outcomes)
			}
		})
	}
}

func modelConsistencyFixture(t *testing.T) MetroCalibrationDataset {
	t.Helper()
	in := calibrationFixture(t)
	// Synthetic test declarations exercise the separate input contract. This
	// fixture is not retained real evidence and cannot enable live departures.
	in.Kind = "model_consistency"
	in.ModelProvenance = "synthetic test of a frozen model qualification bundle"
	for i := range in.References {
		in.References[i].Kind = "model_support"
		in.References[i].Provenance = "synthetic model-support interval"
	}
	return in
}

func TestMetroModelConsistencyDoesNotClaimPhysicalCalibration(t *testing.T) {
	in := modelConsistencyFixture(t)
	out, err := AssessMetroCalibration(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.LiveEnabled || !out.ReviewRequired || out.Purpose != "model_consistency" || out.PhysicalAccuracy != "not_measured" || out.Outcomes[0].Classification != "within_model_support_interval" {
		t.Fatalf("model support was promoted to physical truth or activation: %+v", out)
	}
	in.Samples[4].ProgressMetres = 100
	other, err := AssessMetroCalibration(in)
	if err != nil || other.Candidate != out.Candidate {
		t.Fatal("model holdout changed fitted parameters", other, err)
	}
}

func TestMetroModelConsistencyRetainsEvidenceGuards(t *testing.T) {
	for name, mutate := range map[string]func(*MetroCalibrationDataset){
		"missing qualification": func(in *MetroCalibrationDataset) { in.ModelProvenance = "" },
		"independent label":     func(in *MetroCalibrationDataset) { in.References[0].Kind = "independent" },
		"synthetic label":       func(in *MetroCalibrationDataset) { in.References[0].Kind = "synthetic" },
		"same journey holdout":  func(in *MetroCalibrationDataset) { in.Samples[0].Split = "holdout" },
		"repeated source clock": func(in *MetroCalibrationDataset) { in.Samples[1].SourceAt = in.Samples[0].SourceAt },
		"no stopped evidence":   func(in *MetroCalibrationDataset) { in.Samples[0].Valid = false },
	} {
		t.Run(name, func(t *testing.T) {
			in := modelConsistencyFixture(t)
			mutate(&in)
			if _, err := AssessMetroCalibration(in); err == nil {
				t.Fatal("unsupported model evidence accepted")
			}
		})
	}
	for _, guard := range []string{"correction", "fallback", "gap"} {
		t.Run(guard, func(t *testing.T) {
			in := modelConsistencyFixture(t)
			s := &in.Samples[6]
			switch guard {
			case "correction":
				s.Corrected = true
			case "fallback":
				s.Fallback = true
			case "gap":
				for i := 6; i < len(in.Samples); i++ {
					in.Samples[i].SourceAt = in.Samples[i].SourceAt.Add(61 * time.Second)
					in.Samples[i].ReceivedAt = in.Samples[i].SourceAt
				}
			}
			out, err := AssessMetroCalibration(in)
			if err != nil || out.Outcomes[0].DetectedAt != nil || out.LiveEnabled {
				t.Fatal("prohibited input produced a model departure", out, err)
			}
		})
	}
}
