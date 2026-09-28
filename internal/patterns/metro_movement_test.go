package patterns

import (
	"testing"
	"time"
)

func TestMetroFirstAdmissibleMovementNoRadiusExit(t *testing.T) {
	// Synthetic frozen parameters; these are not calibrated Metro deployment values.
	d := MetroDepartureDetector{Calibration: MetroMovementCalibration{"synthetic-v1", "geometry", "transform", 1, 2}}
	clock := time.Now().UTC()
	s := MetroMovementSample{Journey: "run", Visit: "visit", Geometry: "geometry", Transform: "transform", SourceAt: clock, ProgressMetres: 100, Valid: true, Stopped: true}
	if d.Observe(s) != nil {
		t.Fatal("stop is not departure")
	}
	s.Stopped = false
	s.SourceAt = clock.Add(time.Second)
	s.ProgressMetres = 101
	if d.Observe(s) != nil {
		t.Fatal("jitter admitted")
	}
	s.SourceAt = clock.Add(2 * time.Second)
	s.ProgressMetres = 103
	event := d.Observe(s)
	if event == nil || !event.At.Equal(s.SourceAt) || !event.LastStoppedAt.Equal(clock) {
		t.Fatal("first movement was deferred", event)
	}
	s.SourceAt = clock.Add(3 * time.Second)
	s.ProgressMetres = 110
	if d.Observe(s) != nil {
		t.Fatal("duplicate departure")
	}
}
func TestMetroMovementRejectsUncalibratedFallbackCorrectionAndGap(t *testing.T) {
	for _, scenario := range []string{"uncalibrated", "fallback", "correction", "clock", "geometry", "gap", "backwards"} {
		t.Run(scenario, func(t *testing.T) {
			d := MetroDepartureDetector{Calibration: MetroMovementCalibration{"synthetic-v1", "geometry", "transform", 1, 2}}
			clock := time.Now()
			s := MetroMovementSample{Journey: "run", Visit: "visit", Geometry: "geometry", Transform: "transform", SourceAt: clock, ProgressMetres: 100, Valid: true, Stopped: true}
			d.Observe(s)
			s.SourceAt = clock.Add(time.Second)
			s.ProgressMetres = 105
			s.Stopped = false
			switch scenario {
			case "uncalibrated":
				d.Calibration.Version = ""
			case "fallback":
				s.Fallback = true
			case "correction":
				s.Corrected = true
			case "clock":
				s.SourceAt = clock
			case "geometry":
				s.Geometry = "other"
			case "gap":
				s.SourceAt = clock.Add(61 * time.Second)
			case "backwards":
				s.ProgressMetres = 95
			}
			if d.Observe(s) != nil {
				t.Fatal("unsupported model departure")
			}
		})
	}
}

func TestMetroMovementNewVisitAfterInvalidSample(t *testing.T) {
	d := MetroDepartureDetector{Calibration: MetroMovementCalibration{"synthetic-v1", "geometry", "transform", 1, 2}}
	now := time.Now()
	s := MetroMovementSample{Journey: "run", Visit: "one", Geometry: "geometry", Transform: "transform", SourceAt: now, ProgressMetres: 100, Valid: true, Stopped: true}
	d.Observe(s)
	s.Stopped = false
	s.SourceAt = now.Add(time.Second)
	s.ProgressMetres = 103
	if d.Observe(s) == nil {
		t.Fatal("first departure missing")
	}
	s.Valid = false
	d.Observe(s)
	s.Valid = true
	s.Visit = "two"
	s.SourceAt = now.Add(10 * time.Second)
	s.ProgressMetres = 200
	s.Stopped = true
	d.Observe(s)
	s.Stopped = false
	s.SourceAt = now.Add(11 * time.Second)
	s.ProgressMetres = 203
	if d.Observe(s) == nil {
		t.Fatal("new visit suppressed by previous departure")
	}
}
