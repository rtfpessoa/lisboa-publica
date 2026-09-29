package app

import (
	"math/rand"
	"reflect"
	"slices"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func redDirectionInputs(t *testing.T) (MetroData, StaticData) {
	t.Helper()
	var data MetroData
	var static StaticData
	capturedMetroJSON(t, "red-direction-direct.json", &data)
	capturedMetroJSON(t, "red-direction-static.json", &static)
	return data, static
}

func TestMetroRedActualMinimalDirectionRegression(t *testing.T) {
	data, static := redDirectionInputs(t)
	topology := metroTopology(&data, &static)
	topology.Patterns = slices.DeleteFunc(topology.Patterns, func(p patterns.Pattern) bool {
		return p.Route != "metro:4_0" || p.Direction == "60" && len(p.Stops) == 7
	})
	data.Waits = slices.DeleteFunc(data.Waits, func(row MetroWait) bool {
		return row.Stop != "AP" || row.Train != "7D" && row.Train2 != "7D" && row.Train3 != "7D"
	})
	if len(data.Waits) != 2 || len(topology.Patterns) != 3 {
		t.Fatal("retained minimization changed")
	}
	now := *data.Status.CheckedAt
	r := newMetroRuntime()
	r.topology = topology
	b := collectMetroPoints(&data, topology, now)
	selection := metroContextSelection{r, b, now}
	valid, _ := selection.candidates("metro:4_0|7D")
	if len(valid) != 2 || selection.choose("metro:4_0|7D") != "" {
		t.Fatalf("compatible suffix erased possible direction: %v", valid)
	}
}

func TestMetroRedActualForecastRetentionAndPermutations(t *testing.T) {
	data, static := redDirectionInputs(t)
	var baseline []api.MetroForecastContext
	for seed := int64(0); seed < 8; seed++ {
		input := data
		input.Waits = append([]MetroWait{}, data.Waits...)
		rand.New(rand.NewSource(seed)).Shuffle(len(input.Waits), func(i, j int) { input.Waits[i], input.Waits[j] = input.Waits[j], input.Waits[i] })
		r := newMetroRuntime()
		r.observe(&input, &static, nil, *data.Status.CheckedAt)
		contexts := r.forecastContexts(*data.Status.CheckedAt)
		if seed == 0 {
			baseline = contexts
		} else if !reflect.DeepEqual(baseline, contexts) {
			t.Fatal("row order changed forecasts")
		}
		for _, ref := range []string{"2D", "4D", "5D", "6D", "7D"} {
			directions := map[string]bool{}
			for _, c := range contexts {
				if c.Reference == ref && len(c.Calls) > 0 && c.DirectionCode != nil {
					directions[*c.DirectionCode] = true
				}
			}
			if !directions["38"] || !directions["60"] {
				t.Fatalf("lost %s direction forecasts: %v", ref, directions)
			}
		}
	}
}

func TestMetroEquivalentPlatformPointsDoNotConflict(t *testing.T) {
	now := time.Now().UTC()
	points := []metroPoint{{"SS", "S26O", now, ptr(409)}, {"SS", "S27O", now, ptr(409)}}
	_, conflict := latestMetroPoints(points)
	if conflict {
		t.Fatal("identical station predictions became a platform conflict")
	}
}

func TestMetroForecastOnlyVehicleLinkIsUnavailable(t *testing.T) {
	now := time.Now().UTC()
	b := metroFrameBuilder{now: now}
	v := api.Vehicle{SourceId: "5D", RouteId: ptr("metro:r"), ObservedAt: now}
	train := api.MetroTrain{Reference: "5D", RouteId: "metro:r", Association: "supported", ValidUntil: now.Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "context"}}
	if b.vehicleMatches(v, train) {
		t.Fatal("forecast-only context admitted a current vehicle link")
	}
}

func TestMetroForecastPlatformsPreserveScopedConflictsAndClocks(t *testing.T) {
	for _, value := range []string{"120", "140", "null"} {
		t.Run(value, func(t *testing.T) {
			s, static, data, now := metroLiveFixture(t)
			first := metroTestRow(now, "RM", "7", "120")
			second := metroTestRow(now, "RM", "7", value)
			second.Platform = "2"
			data.Waits = []MetroWait{first, second, metroTestRow(now, "CS", "7", "240")}
			publishMetroTest(s, static, data, now)
			contexts := s.Cache.metroRuntime.forecastContexts(now)
			if len(contexts) != 1 || contexts[0].Status != "admissible" {
				t.Fatal("station conflict erased whole context", contexts)
			}
			calls := metroCallsAtStop(contexts[0].Calls, "metro:gtfs-rm")
			if value == "140" {
				if len(calls) != 2 || len(calls[0].MetroForecast.Limitations) == 0 {
					t.Fatal("different predictions were silently combined", calls)
				}
			} else if len(calls) != 1 {
				t.Fatal("equivalent or missing platform prediction lost", calls)
			}
			if value == "120" && len(calls[0].MetroForecast.Platforms) != 2 {
				t.Fatal("platform provenance lost")
			}
			if len(metroCallsAtStop(contexts[0].Calls, "metro:gtfs-cs")) != 1 {
				t.Fatal("unrelated station forecast lost")
			}
		})
	}
}

func TestMetroAnonymousPredictionAndOrderAdmission(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "", "120")}
	publishMetroTest(s, static, data, now)
	contexts := s.Cache.metroRuntime.forecastContexts(now)
	if len(contexts) != 1 || len(contexts[0].Calls) != 1 || contexts[0].Calls[0].ServiceLabel != nil || contexts[0].Calls[0].MetroForecast.SourceReference != nil || len(data.Trains) != 0 {
		t.Fatal("anonymous forecast lost or fabricated identity", contexts)
	}
	for _, tc := range []struct {
		cohort, slots    []string
		complete, unique bool
	}{
		{[]string{"6D", "5D", "2D"}, []string{"6D", "", "2D"}, true, true},
		{[]string{"6D", "5D", "7D", "2D"}, []string{"6D", "", "2D"}, true, false},
		{[]string{"6D", "5D", "2D"}, []string{"6D", "", "2D"}, false, false},
		{[]string{"6D", "5D", "2D"}, []string{"2D", "6D"}, true, false},
		{[]string{"6D", "5D", "5D", "2D"}, []string{"6D", "", "2D"}, true, false},
	} {
		match, ok := metroOrderForecastMatch(tc.cohort, tc.slots, tc.complete)
		if ok != tc.unique || ok && match[1] != "5D" {
			t.Fatal("incorrect synthetic cohort admission", tc, match, ok)
		}
	}
}

func TestMetroForecastOnlyPriorCannotSelectAmongDirections(t *testing.T) {
	data, static := redDirectionInputs(t)
	now := *data.Status.CheckedAt
	topology := metroTopology(&data, &static)
	b := collectMetroPoints(&data, topology, now)
	r := newMetroRuntime()
	r.topology = topology
	key := "metro:4_0|38|4D"
	// Reconstructed private-state control, not an observed physical allocation.
	r.active[key] = "synthetic-prior"
	r.tracks["synthetic-prior"] = &metroTrack{Train: api.MetroTrain{JourneyId: "synthetic-prior", Reference: "4D", RouteId: "metro:4_0", Association: "supported", ValidUntil: now.Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "context"}}, Codes: b.paths[key].Stops, Points: map[string]metroPoint{}}
	selection := metroContextSelection{r, b, now}
	if got := selection.choose("metro:4_0|4D"); got != "" || r.tracks["synthetic-prior"].Train.Association != "suspended" {
		t.Fatal("forecast-only prior retained current support", got)
	}
}

func TestMetroRejectedAlternativeCannotConfirmOppositeDirection(t *testing.T) {
	data, static := redDirectionInputs(t)
	now := *data.Status.CheckedAt
	topology := metroTopology(&data, &static)
	b := collectMetroPoints(&data, topology, now)
	b.rejected["metro:4_0|60|4D"] = true
	r := newMetroRuntime()
	r.topology = topology
	selection := metroContextSelection{r, b, now}
	if got := selection.choose("metro:4_0|4D"); got != "" {
		t.Fatal("rejected support made opposite direction current", got)
	}
}

func TestMetroContextRetainsSupportedOwnOnlyForecast(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120"), metroTestRow(now, "CS", "7", "null")}
	publishMetroTest(s, static, data, now)
	r := s.Cache.metroRuntime
	track := r.tracks[data.Trains[0].JourneyId]
	// Synthetic already-admitted own estimate; existing engine admission is
	// tested separately. Context-only direction must not erase this value.
	own := &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: ptr(now.Add(90 * time.Second)), ModelVersion: ptr("synthetic-own"), AssociationEpisode: ptr("synthetic-episode")}
	track.Train.Calls[1].OwnPrediction = own
	contexts := r.forecastContexts(now)
	calls := metroCallsAtStop(contexts[0].Calls, "metro:gtfs-cs")
	if len(calls) != 1 || calls[0].OwnPrediction == nil || calls[0].Arrival.Prediction != nil || calls[0].JourneyId != nil {
		t.Fatal("own-only context value lost or promoted to linkage", calls)
	}
	if calls[0].OwnPrediction.At != own.At || calls[0].OwnPrediction.ModelVersion == nil {
		t.Fatal("own provenance changed")
	}
}

func TestMetroIndependentPlatformClocksAndAnonymousRevisions(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	old := metroTestRow(now, "RM", "7", "120")
	next := metroTestRow(now.Add(time.Second), "RM", "7", "119")
	next.Platform = "2"
	data.Waits = []MetroWait{old, next}
	publishMetroTest(s, static, data, now.Add(time.Second))
	calls := s.Cache.metroRuntime.forecastContexts(now.Add(time.Second))[0].Calls
	if len(calls) != 2 || calls[0].MetroForecast.Platforms[0].SourceUpdatedAt.Equal(calls[1].MetroForecast.Platforms[0].SourceUpdatedAt) {
		t.Fatal("independent clocks merged or renewed", calls)
	}
	old.Train = ""
	next = old
	next.At = now.Add(time.Second).In(lisbon).Format("20060102150405")
	next.Wait1 = []byte("null")
	data.Waits = []MetroWait{old, next}
	publishMetroTest(s, static, data, now.Add(2*time.Second))
	contexts := s.Cache.metroRuntime.forecastContexts(now.Add(2 * time.Second))
	if len(contexts) != 0 {
		t.Fatal("new missing anonymous wait revived older prediction", contexts)
	}
}

func TestMetroSourceGapWithdrawsQualifiedContinuityWithoutCompletion(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, static, data, now)
	track := s.Cache.metroRuntime.tracks[data.Trains[0].JourneyId]
	track.Train.DirectionEvidence = &api.MetroDirectionEvidence{State: "confirmed", Reason: "synthetic admitted prior"}
	data.Waits = []MetroWait{metroTestRow(now.Add(61*time.Second), "RM", "7", "100")}
	publishMetroTest(s, static, data, now.Add(61*time.Second))
	if metroQualifiedDirection(track, now.Add(61*time.Second)) || track.Train.Lifecycle.State != "active" || track.Train.Calls[0].Arrival.Inferred != nil {
		t.Fatal("source gap retained linkage or fabricated closure/event", track.Train)
	}
}

func TestMetroQualifiedOwnOnlyForecastIsNotDuplicatedAsUnassociated(t *testing.T) {
	now := time.Now().UTC()
	own := &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: ptr(now.Add(90 * time.Second))}
	call := api.StopCall{Id: "own-only", StopId: "metro:station", OwnPrediction: own}
	train := api.MetroTrain{Reference: "7", RouteId: "metro:r", Destination: "Terminal", Association: "supported", ValidUntil: now.Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "confirmed", Reason: "synthetic qualified direction"}, Calls: []api.StopCall{call}}
	b := metroFrameBuilder{metroFrameRequest: metroFrameRequest{interest: metroInterest{Stop: call.StopId}}, now: now, frame: api.MetroLiveFrame{Trains: []api.MetroTrain{train}, ForecastContexts: &[]api.MetroForecastContext{}}, contexts: []api.MetroForecastContext{{Reference: train.Reference, RouteId: train.RouteId, Destination: train.Destination, Status: "admissible", Calls: []api.StopCall{call}}}}
	b.stationForecasts()
	if len(b.frame.UnassociatedForecasts) != 0 {
		t.Fatal("qualified own-only row duplicated", b.frame.UnassociatedForecasts)
	}
}

func TestMetroMandatoryCheckpointCannotRestoreQualifiedOldLink(t *testing.T) {
	now := time.Now().UTC()
	prior := api.MetroTrain{Association: "supported", ValidUntil: now.Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "confirmed", Reason: "synthetic qualified prior"}, Lifecycle: &api.MetroJourneyLifecycle{State: "active"}, Calls: []api.StopCall{{Id: "history", Arrival: missingCallTime("historical")}}}
	track := &metroTrack{Train: prior, BarrierBefore: &prior, BarrierRevision: 1}
	visible := visibleMetroTrain(track)
	if metroCurrentDirection(visible, now) || visible.Lifecycle.State != "active" || len(visible.Calls) != 1 {
		t.Fatal("pending transition restored linkage or changed committed history", visible)
	}
	if prior.Association != "supported" || prior.DirectionEvidence.State != "confirmed" {
		t.Fatal("visibility mutated immutable prior")
	}
}

func TestMetroFailedMandatoryCheckpointWithholdsLinkAndKeepsHistory(t *testing.T) {
	r := newMetroRuntime()
	now := time.Now().UTC()
	prior := api.MetroTrain{Association: "supported", SourceUpdatedAt: now, ValidUntil: now.Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "confirmed", Reason: "synthetic admitted direction"}, Lifecycle: &api.MetroJourneyLifecycle{State: "active"}, Calls: []api.StopCall{{Id: "history", Arrival: missingCallTime("historical")}}}
	track := &metroTrack{Train: cloneMetroTrain(prior), Points: map[string]metroPoint{}}
	track.Train.Lifecycle = &api.MetroJourneyLifecycle{State: "completed"}
	if r.stageLifecycle(track, prior) || metroQualifiedDirection(track, now) || track.Train.Lifecycle.State != "active" || len(track.Train.Calls) != 1 {
		t.Fatal("unavailable mandatory commit restored unsupported link or changed history", track.Train)
	}
}

func TestMetroForecastRowIdentitySurvivesSourceRevision(t *testing.T) {
	s, static, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, static, data, now)
	before := s.Cache.metroRuntime.forecastContexts(now)[0].Calls[0]
	data.Waits = []MetroWait{metroTestRow(now.Add(time.Second), "RM", "7", "130")}
	publishMetroTest(s, static, data, now.Add(time.Second))
	after := s.Cache.metroRuntime.forecastContexts(now.Add(time.Second))[0].Calls[0]
	if before.Id != after.Id || !after.Arrival.Prediction.At.After(before.Arrival.Prediction.At) {
		t.Fatal("revision changed row identity or rewrote source ETA", before, after)
	}
}
