package app

import (
	"context"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func TestMetroQualifiedWaitAdapterFirstMovementCorrectionAndRecovery(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "120")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	tack := r.tracks[data.Trains[0].JourneyId]
	// Synthetic frozen configuration tests the runtime adapter only. Production
	// installation goes through QualifyMetroModel and reviewed retained evidence.
	model := patterns.MetroQualifiedModel{Admission: patterns.MetroModelAdmission{Axis: []patterns.MetroModelStation{{Stop: "RM", Lat: 38.75, Lon: -9.14, Metres: 0}, {Stop: "CS", Lat: 38.71, Lon: -9.14, Metres: 1000}}, SegmentSeconds: []float64{120}}, Calibration: patterns.MetroMovementCalibration{Version: "synthetic-model", Geometry: "synthetic-axis", Transform: patterns.MetroWaitTransform, NoiseMetres: 2, ResolutionMetres: 3}}
	r.models[tack.Profile+"|54"] = model
	tack.Train.DirectionEvidence = &api.MetroDirectionEvidence{State: "confirmed", Reason: "synthetic prior qualified direction"}
	stop := *data
	stop.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "0"), metroTestRow(now.Add(time.Second), "CS", "7", "120")}
	publishMetroTest(s, d, &stop, now.Add(time.Second))
	move := stop
	move.Waits = []MetroWait{metroTestRow(now.Add(2*time.Second), "CS", "7", "119")}
	publishMetroTest(s, d, &move, now.Add(2*time.Second))
	c := move.Trains[0].Calls[0]
	if c.Departure.Inferred == nil || !c.Departure.Inferred.At.Equal(now.Add(2*time.Second)) || c.Departure.Inferred.Mode != "model_departure" || move.Trains[0].ModelProjection == nil {
		t.Fatal("first movement missing", c, move.Trains)
	}
	if c.Departure.Inferred.Persistence != "committed" {
		t.Fatal("departure commit not acknowledged")
	}
	// A corrected same-visit geometry progress withdraws the main value, while
	// retaining its original estimate and evidence as revision history.
	corrected := move
	corrected.Waits = []MetroWait{metroTestRow(now.Add(3*time.Second), "CS", "7", "120")}
	// Zero progress is still a model sample; it cannot be omitted as an ETA gap.
	publishMetroTest(s, d, &corrected, now.Add(3*time.Second))

	c = tack.Train.Calls[0]
	if c.Departure.Inferred != nil || c.Departure.Reason != "Estimativa retirada" || c.DepartureRevisions == nil || len(*c.DepartureRevisions) != 2 {
		t.Fatal("withdrawal lost", c)
	}
	id := tack.Train.JourneyId
	s.Cache.metroRuntime = newMetroRuntime()
	f, _, err := s.metroFrame(context.Background(), metroInterest{Journey: id})
	if err != nil || f.Trains[0].Calls[0].Departure.Reason != "Estimativa retirada" || len(*f.Trains[0].Calls[0].DepartureRevisions) != 2 || f.Trains[0].ModelProjection != nil {
		t.Fatal("correction recovery failed", f, err)
	}
}
func TestMetroWaitAdapterRequiresSupportAndOriginalClock(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "0")}
	publishMetroTest(s, d, data, now)
	tck := s.Cache.metroRuntime.tracks[data.Trains[0].JourneyId]
	adapter := metroWaitMovementAdapter{track: tck, points: tck.Points, now: now}
	if _, _, ok := adapter.sample(0); ok {
		t.Fatal("isolated zero admitted as a stop")
	}
}
