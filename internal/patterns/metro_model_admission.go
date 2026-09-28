package patterns

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const MetroWaitTransform = "metro-wait-segment-v1"

// MetroModelAdmission is a reviewed configuration, never a provider capability.
// Geometry is an explicit station-axis model; it is not an independent GPS trace.
type MetroModelAdmission struct {
	Profile        string                  `json:"profile"`
	Direction      string                  `json:"direction"`
	ReviewedBy     string                  `json:"reviewed_by"`
	EvidenceSHA256 string                  `json:"evidence_sha256"`
	Dataset        MetroCalibrationDataset `json:"dataset"`
	Axis           []MetroModelStation     `json:"axis"`
	SegmentSeconds []float64               `json:"segment_seconds"`
}

// MetroModelStation fixes a published station on the reviewed canonical axis.
type MetroModelStation struct {
	Stop   string  `json:"stop"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Metres float64 `json:"metres"`
}

// MetroQualifiedModel pairs reviewed binding with fitted replay parameters.
type MetroQualifiedModel struct {
	Admission   MetroModelAdmission
	Calibration MetroMovementCalibration
}

// MetroModelEvidenceSHA256 hashes the canonical Go JSON dataset encoding.
func MetroModelEvidenceSHA256(dataset MetroCalibrationDataset) string {
	raw, _ := json.Marshal(dataset)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// MetroModelGeometryVersion binds ordered station coordinates and axis metres.
func MetroModelGeometryVersion(axis []MetroModelStation) string {
	raw, _ := json.Marshal(axis)
	sum := sha256.Sum256(raw)
	return "metro-station-axis-v1-" + hex.EncodeToString(sum[:])
}

// QualifyMetroModel validates reviewed original-input replay before live admission.
func QualifyMetroModel(in MetroModelAdmission) (MetroQualifiedModel, error) {
	if err := validateMetroModelBinding(in); err != nil {
		return MetroQualifiedModel{}, err
	}
	if err := validateMetroModelAxis(in); err != nil {
		return MetroQualifiedModel{}, err
	}
	return assessQualifiedMetroModel(in)
}
func validateMetroModelBinding(in MetroModelAdmission) error {
	reviewed := in.Profile != "" && in.Direction != "" && in.ReviewedBy != ""
	transform := in.Dataset.Kind == "model_consistency" && in.Dataset.Transform == MetroWaitTransform
	if !reviewed || !transform {
		return fmt.Errorf("requires reviewed original-publication model binding and known transform")
	}
	if in.EvidenceSHA256 != MetroModelEvidenceSHA256(in.Dataset) {
		return fmt.Errorf("model evidence checksum mismatch")
	}
	return nil
}
func assessQualifiedMetroModel(in MetroModelAdmission) (MetroQualifiedModel, error) {
	out, err := AssessMetroCalibration(in.Dataset)
	if err != nil {
		return MetroQualifiedModel{}, err
	}
	supported := supportedMetroModelReplay(in.Dataset, out) && metroModelProhibitedInputs(out.Candidate)
	if !supported {
		return MetroQualifiedModel{}, fmt.Errorf("requires consistent training/holdout and prohibited-input replay")
	}
	return MetroQualifiedModel{Admission: in, Calibration: out.Candidate}, nil
}

func validateMetroModelAxis(in MetroModelAdmission) error {
	if len(in.Axis) < 2 || len(in.Axis) > 256 || len(in.SegmentSeconds) != len(in.Axis)-1 {
		return fmt.Errorf("invalid bounded station axis")
	}
	if in.Dataset.Geometry != MetroModelGeometryVersion(in.Axis) {
		return fmt.Errorf("geometry revision mismatch")
	}
	return validateMetroAxisEvidence(in)
}
func validateMetroAxisEvidence(in MetroModelAdmission) error {
	if !validMetroAxisStations(in.Axis) {
		return fmt.Errorf("invalid unique ordered station axis")
	}
	for _, v := range in.SegmentSeconds {
		if !positiveMetroMetres(v) || v > 600 {
			return fmt.Errorf("invalid frozen segment duration")
		}
	}
	return nil
}

func validMetroAxisStations(axis []MetroModelStation) bool {
	seen := map[string]bool{}
	for n, p := range axis {
		if p.Stop == "" || seen[p.Stop] || !validMetroAxisCoordinate(p) {
			return false
		}
		seen[p.Stop] = true
		if n > 0 && p.Metres <= axis[n-1].Metres {
			return false
		}
	}
	return true
}
func validMetroAxisCoordinate(p MetroModelStation) bool {
	numeric := finiteMetroMetres(p.Metres) && finiteMetroMetres(p.Lat) && finiteMetroMetres(p.Lon)
	return numeric && math.Abs(p.Lat) <= 90 && math.Abs(p.Lon) <= 180
}

func supportedMetroModelReplay(in MetroCalibrationDataset, out MetroCalibrationAssessment) bool {
	if len(out.Outcomes) == 0 {
		return false
	}
	for _, v := range out.Outcomes {
		if v.Classification != "within_model_support_interval" {
			return false
		}
	}
	refs := map[string]MetroCalibrationReference{}
	for _, r := range in.References {
		refs[r.Journey+"|"+r.Visit] = r
	}
	detectors := map[string]*MetroDepartureDetector{}
	for _, s := range in.Samples {
		if s.Split != "train" {
			continue
		}
		key := s.Journey + "|" + s.Visit
		d := detectors[key]
		if d == nil {
			d = &MetroDepartureDetector{Calibration: out.Candidate}
			detectors[key] = d
		}
		event := d.Observe(MetroMovementSample{Journey: s.Journey, Visit: s.Visit, Geometry: in.Geometry, Transform: in.Transform, SourceAt: s.SourceAt, ProgressMetres: s.ProgressMetres, Valid: s.Valid, Stopped: s.Stopped, Corrected: s.Corrected, Fallback: s.Fallback})
		if event != nil && classifyMetroMovement(*event, refs[key]) == "within_reference_window" {
			return true
		}
	}
	return false
}

// These controls are separate from retained evidence and cannot qualify it.
func metroModelProhibitedInputs(c MetroMovementCalibration) bool {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, kind := range []string{"repeated", "gap", "corrected", "fallback", "regression", "unqualified", "restart"} {
		d := MetroDepartureDetector{Calibration: c}
		p := MetroMovementSample{Journey: "control", Visit: "stop", Geometry: c.Geometry, Transform: c.Transform, SourceAt: at, Valid: true, Stopped: true}
		d.Observe(p)
		p.Stopped = false
		p.ProgressMetres = math.Max(c.NoiseMetres, c.ResolutionMetres) + 1
		p.SourceAt = at.Add(time.Second)
		applyMetroProhibitedCase(&d, &p, kind, at)

		if d.Observe(p) != nil {
			return false
		}
	}
	return true
}

func applyMetroProhibitedCase(d *MetroDepartureDetector, p *MetroMovementSample, kind string, at time.Time) {
	switch kind {
	case "repeated":
		p.SourceAt = at
	case "gap":
		p.SourceAt = at.Add(61 * time.Second)
	case "corrected":
		p.Corrected = true
	case "fallback":
		p.Fallback = true
	case "regression":
		p.SourceAt = at.Add(-time.Second)
	case "unqualified":
		p.Valid = false
	case "restart":
		*d = MetroDepartureDetector{Calibration: d.Calibration}
	}
}
