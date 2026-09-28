package patterns

import "testing"

// Declarations in this synthetic contract control are not production evidence.
func modelAdmissionFixture(t *testing.T) MetroModelAdmission {
	in := MetroModelAdmission{Profile: "synthetic", Direction: "54", ReviewedBy: "synthetic-contract-control", Dataset: modelConsistencyFixture(t), Axis: []MetroModelStation{{Stop: "A", Lat: 38.7, Lon: -9.1, Metres: 0}, {Stop: "B", Lat: 38.71, Lon: -9.1, Metres: 1000}}, SegmentSeconds: []float64{120}}
	in.Dataset.Transform = MetroWaitTransform
	in.Dataset.Geometry = MetroModelGeometryVersion(in.Axis)
	in.EvidenceSHA256 = MetroModelEvidenceSHA256(in.Dataset)
	return in
}
func TestMetroModelAdmissionReplayBindingAndReview(t *testing.T) {
	in := modelAdmissionFixture(t)
	model, err := QualifyMetroModel(in)
	if err != nil || model.Calibration.NoiseMetres != .25 {
		t.Fatal(model, err)
	}
	for _, kind := range []string{"synthetic", "unreviewed", "hash", "geometry", "holdout", "training", "duration", "transform", "empty"} {
		t.Run(kind, func(t *testing.T) {
			v := modelAdmissionFixture(t)
			switch kind {
			case "synthetic":
				v.Dataset.Kind = "synthetic"
			case "unreviewed":
				v.ReviewedBy = ""
			case "hash":
				v.EvidenceSHA256 = "wrong"
			case "geometry":
				v.Axis[1].Metres = 0
			case "holdout":
				v.Dataset.Samples[6].Corrected = true
			case "training":
				v.Dataset.Samples[2].Corrected = true
			case "duration":
				v.SegmentSeconds[0] = 0
			case "transform":
				v.Dataset.Transform = "opaque-hub"
			case "empty":
				v.Dataset.Samples = nil
			}
			if kind != "hash" {
				v.EvidenceSHA256 = MetroModelEvidenceSHA256(v.Dataset)
			}
			if _, err := QualifyMetroModel(v); err == nil {
				t.Fatal("unsupported admission", kind)
			}
		})
	}
}
