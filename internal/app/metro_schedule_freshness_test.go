package app

import (
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func TestMetroSchedulePreservesAnchorClockAcrossVisits(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	source, expiry := now.Add(-45*time.Second), now.Add(45*time.Second)
	anchor := &api.CallTimeEvidence{At: now.Add(20 * time.Second), SourceUpdatedAt: &source, ValidUntil: &expiry, ModelVersion: ptr("historical")}
	track := &metroTrack{Profile: "plan", Priors: []metroVisitPrior{{Dwell: 20, Run: 60}, {Dwell: 20}}, Train: api.MetroTrain{JourneyId: "episode", Association: "supported", SourceUpdatedAt: now, ValidUntil: now.Add(90 * time.Second), Calls: []api.StopCall{{OwnPrediction: anchor}, {}}}}
	projectMetroScheduled(track, now)
	for _, value := range []*api.CallTimeEvidence{track.Train.Calls[0].OwnDeparturePrediction, track.Train.Calls[1].OwnPrediction, track.Train.Calls[1].OwnDeparturePrediction} {
		if value == nil || value.SourceUpdatedAt == nil || !value.SourceUpdatedAt.Equal(source) || value.ValidUntil == nil || !value.ValidUntil.Equal(expiry) {
			t.Fatalf("derived estimate renewed anchor evidence: %+v", value)
		}
	}
	projectMetroScheduled(track, expiry)
	if track.Train.Calls[0].OwnDeparturePrediction != nil || track.Train.Calls[1].OwnPrediction != nil || track.Train.Calls[1].OwnDeparturePrediction != nil {
		t.Fatal("expired upstream evidence produced a downstream estimate")
	}
}

func TestMetroScheduleRejectsExpiredAnchorAndUsesFreshOfficial(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	oldSource, oldExpiry := now.Add(-90*time.Second), now
	freshSource, freshExpiry := now.Add(-5*time.Second), now.Add(85*time.Second)
	track := &metroTrack{Profile: "plan", Priors: []metroVisitPrior{{Dwell: 20, Run: 60}, {Dwell: 20}}, Train: api.MetroTrain{Association: "supported", SourceUpdatedAt: now, ValidUntil: now.Add(90 * time.Second), Calls: []api.StopCall{{OwnPrediction: &api.CallTimeEvidence{At: now.Add(50 * time.Second), SourceUpdatedAt: &oldSource, ValidUntil: &oldExpiry}, Arrival: api.CallTime{Prediction: &api.CallTimeEvidence{At: now.Add(30 * time.Second), SourceUpdatedAt: &freshSource, ValidUntil: &freshExpiry}}}, {}}}}
	projectMetroScheduled(track, now)
	departure := track.Train.Calls[0].OwnDeparturePrediction
	if departure == nil || !departure.At.Equal(now.Add(50*time.Second)) || departure.SourceUpdatedAt == nil || !departure.SourceUpdatedAt.Equal(freshSource) || departure.ValidUntil == nil || !departure.ValidUntil.Equal(freshExpiry) {
		t.Fatalf("fresh official evidence did not replace expired anchor: %+v", departure)
	}
	track.Train.Calls[0].Arrival.Prediction = nil
	projectMetroScheduled(track, now)
	if track.Train.Calls[0].OwnDeparturePrediction != nil || track.Train.Calls[1].OwnPrediction != nil {
		t.Fatal("unavailable anchor produced forecast")
	}
}

func TestMetroScheduleEventAnchorRequiresCurrentSameVisitEvidence(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	arrived := now.Add(-95 * time.Second)
	confirmed := now.Add(-time.Second)
	track := &metroTrack{Profile: "plan", Codes: []string{"station"}, Points: map[string]metroPoint{}, Priors: []metroVisitPrior{{Dwell: 120, DwellSamples: []int{120}}}, Train: api.MetroTrain{Association: "supported", SourceUpdatedAt: now, ValidUntil: now.Add(90 * time.Second), Calls: []api.StopCall{{Arrival: api.CallTime{Inferred: &api.MetroEventEvidence{At: arrived, WindowEnd: arrived}}}}}}
	projectMetroScheduled(track, now)
	if track.Train.Calls[0].OwnDeparturePrediction != nil {
		t.Fatal("unrelated train clock renewed an old arrival")
	}
	track.Points["station"] = metroPoint{Stop: "station", Clock: confirmed, Seconds: ptr(0)}
	projectMetroScheduled(track, now)
	departure := track.Train.Calls[0].OwnDeparturePrediction
	if departure == nil || departure.SourceUpdatedAt == nil || !departure.SourceUpdatedAt.Equal(confirmed) || !departure.At.Equal(arrived.Add(120*time.Second)) {
		t.Fatalf("fresh same-station stop did not support conditional dwell: %+v", departure)
	}
}

func TestMetroContextPriorRejectsExpiredOfficialFallback(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	call := api.StopCall{OwnPrediction: &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: ptr(now)}, Arrival: api.CallTime{Prediction: &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: ptr(now)}}}
	if metroContextOwnAnchor(call, now) != nil {
		t.Fatal("context revived expired official prediction")
	}
}

func TestMetroForecastWorkRejectsChangedVisitEvidenceWhileArchivePaused(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	track := &metroTrack{Profile: "plan", Revision: 0, Points: map[string]metroPoint{"A": {Stop: "A", Clock: now, Seconds: ptr(10)}}, Train: api.MetroTrain{JourneyId: "episode", Association: "supported", SourceUpdatedAt: now, ValidUntil: now.Add(time.Minute), Calls: []api.StopCall{{Id: "visit", Arrival: api.CallTime{Prediction: &api.CallTimeEvidence{At: now.Add(10 * time.Second)}}}}}}
	work := *track
	work.Train = cloneMetroTrain(track.Train)
	if !metroOwnForecastWorkCurrent(track, work, now) {
		t.Fatal("unchanged inputs rejected")
	}
	track.Train.Calls[0].Arrival.Prediction.At = now.Add(20 * time.Second)
	if metroOwnForecastWorkCurrent(track, work, now) {
		t.Fatal("changed station forecast accepted with unchanged archive revision and train clock")
	}
	track.Train.Calls[0].Arrival = work.Train.Calls[0].Arrival
	track.Points = map[string]metroPoint{"A": {Stop: "A", Clock: now, Seconds: ptr(20)}}
	if metroOwnForecastWorkCurrent(track, work, now) {
		t.Fatal("changed same-station evidence accepted")
	}
}

func TestMetroSchedulePreservesMissingSourceClockAndDepartureConfirmation(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Minute)
	track := &metroTrack{Profile: "plan", Priors: []metroVisitPrior{{Dwell: 20, Run: 60}, {Dwell: 20}}, Train: api.MetroTrain{Association: "supported", SourceUpdatedAt: now, ValidUntil: expiry, Calls: []api.StopCall{{OwnPrediction: &api.CallTimeEvidence{At: now.Add(10 * time.Second), ValidUntil: &expiry}}, {}}}}
	projectMetroScheduled(track, now)
	if estimate := track.Train.Calls[1].OwnPrediction; estimate == nil || estimate.SourceUpdatedAt != nil {
		t.Fatalf("unknown source clock was invented or forecast lost: %+v", estimate)
	}
	confirmed, first := now.Add(-time.Second), now.Add(-10*time.Second)
	track.Train.Calls[0].OwnPrediction = nil
	track.Train.Calls[0].Departure.Inferred = &api.MetroEventEvidence{At: first, WindowEnd: first, ConfirmedAt: &confirmed}
	projectMetroScheduled(track, now)
	estimate := track.Train.Calls[1].OwnPrediction
	if estimate == nil || estimate.SourceUpdatedAt == nil || !estimate.SourceUpdatedAt.Equal(confirmed) || estimate.ValidUntil == nil || !estimate.ValidUntil.Equal(expiry) {
		t.Fatalf("departure confirmation lineage lost: %+v", estimate)
	}
}

func TestMetroForecastCommitChecksClockAfterWork(t *testing.T) {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name            string
		anchorExpires   time.Duration
		episodeExpires  time.Duration
		finish          time.Duration
		wantAssociation api.MetroTrainAssociation
		wantForecast    bool
	}{
		{"fresh", 10 * time.Second, time.Minute, 2 * time.Second, "supported", true},
		{"anchor expired", time.Second, time.Minute, 2 * time.Second, "supported", false},
		{"episode expired", time.Minute, time.Second, 2 * time.Second, "suspended", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expiry := start.Add(tc.anchorExpires)
			track := &metroTrack{
				Profile: "plan",
				Priors:  []metroVisitPrior{{Dwell: 20, Run: 60}, {Dwell: 20}},
				Train: api.MetroTrain{JourneyId: "episode", Association: "supported", SourceUpdatedAt: start,
					ValidUntil: start.Add(tc.episodeExpires), Calls: []api.StopCall{
						{Arrival: api.CallTime{Prediction: &api.CallTimeEvidence{At: start.Add(30 * time.Second), ValidUntil: &expiry}}},
						{},
					}},
			}
			runtime := newMetroRuntime()
			runtime.tracks["episode"] = track
			runtime.active["active"] = "episode"
			var data MetroData
			runtime.projectOwnWithClock(&data, nil, start, func() time.Time { return start.Add(tc.finish) })
			if len(data.Trains) != 1 || data.Trains[0].Association != tc.wantAssociation {
				t.Fatalf("unexpected publication after work: %+v", data.Trains)
			}
			calls := data.Trains[0].Calls
			gotForecast := calls[0].OwnDeparturePrediction != nil && calls[1].OwnPrediction != nil
			if gotForecast != tc.wantForecast || (!tc.wantForecast && (calls[0].OwnDeparturePrediction != nil || calls[1].OwnPrediction != nil)) {
				t.Fatalf("unexpected forecast after work: %+v", calls)
			}
		})
	}
}
