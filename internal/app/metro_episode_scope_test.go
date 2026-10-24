package app

import (
	"encoding/json"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// Based on the 2026-10-24 captured 24C source clocks and waits; no physical identity is implied.
func metroEpisodeScopeFixture() (*metroRuntime, *metroPointBatch, *metroTrack, time.Time) {
	now := time.Date(2026, 10, 24, 19, 59, 6, 0, time.UTC)
	passed := time.Date(2026, 10, 24, 19, 58, 26, 0, time.UTC)
	downstream := time.Date(2026, 10, 24, 19, 59, 3, 0, time.UTC)
	path := patterns.Pattern{Route: "metro:green", Direction: "50", Destination: "TE", Stops: []string{"CS", "AL", "CG", "TE"}, Sequences: []int{1, 2, 3, 4}}
	topology := patterns.Topology{Profile: "scope-fixture", Patterns: []patterns.Pattern{path}}
	codes := append([]string{}, path.Stops...)
	calls := []api.StopCall{}
	for _, code := range codes {
		calls = append(calls, api.StopCall{Id: "run:" + code, StopId: code, Arrival: missingCallTime("unknown"), Departure: missingCallTime("unknown")})
	}
	calls[1].Arrival = api.CallTime{Kind: "inferred", At: &passed, Inferred: &api.MetroEventEvidence{At: passed, WindowStart: passed.Add(-6 * time.Second), WindowEnd: passed, Mode: "inferred_arrival", ModelVersion: metroArrivalModel}}
	track := &metroTrack{
		Train:   api.MetroTrain{JourneyId: "run", Reference: "24C", RouteId: path.Route, Association: "supported", SourceUpdatedAt: downstream, ValidUntil: downstream.Add(sourceFreshness), Calls: calls, NextIndex: ptr(2), DirectionEvidence: &api.MetroDirectionEvidence{State: "estimated"}, Lifecycle: &api.MetroJourneyLifecycle{State: "active"}},
		Profile: topology.Profile, ProviderDirection: path.Direction, Codes: codes,
		Points: map[string]metroPoint{"CG": {Stop: "CG", Platform: "G17O", Clock: downstream, Seconds: ptr(142)}, "TE": {Stop: "TE", Platform: "TE5O", Clock: downstream, Seconds: ptr(247)}},
		Proofs: map[string]metroEventProof{"arrival": {Train: api.MetroTrain{JourneyId: "run"}, CallID: "run:AL", Before: metroPoint{Stop: "AL", Platform: "AL3ALO", Clock: passed.Add(-6 * time.Second), Seconds: ptr(8)}, After: metroPoint{Stop: "AL", Platform: "AL3ALO", Clock: passed, Seconds: ptr(0)}}},
	}
	key := "metro:green|50|24C"
	batch := &metroPointBatch{groups: map[string][]metroPoint{key: {{Stop: "CS", Platform: "CS27O", Clock: now.Add(-time.Second), Seconds: ptr(1978)}, {Stop: "CG", Platform: "G17O", Clock: downstream, Seconds: ptr(142)}, {Stop: "TE", Platform: "TE5O", Clock: downstream, Seconds: ptr(247)}}}, paths: map[string]patterns.Pattern{key: path}, references: map[string]map[string]bool{"metro:green|24C": {key: true}}, rejected: map[string]bool{}}
	r := newMetroRuntime()
	r.topology, r.directionTopology = topology, topology
	r.tracks["run"], r.active[key] = track, "run"
	r.batch = batch
	return r, batch, track, now
}

func TestMetroEpisodeScopeCapturedThirdSlot(t *testing.T) {
	r, _, _, now := metroEpisodeScopeFixture()
	data := &MetroData{Destinations: []MetroDestination{{ID: "50", Name: "TE"}}, Stations: []MetroStation{{ID: "CS", Name: "CS", Lines: "[Verde]"}, {ID: "AL", Name: "AL", Lines: "[Verde]"}, {ID: "CG", Name: "CG", Lines: "[Verde]"}, {ID: "TE", Name: "TE", Lines: "[Verde]"}}}
	static := &StaticData{Routes: []api.RouteDetail{{Id: "metro:green", ShortName: "Vd"}}}
	data.Waits = []MetroWait{
		{Stop: "CS", Platform: "CS27O", At: "20261024205905", Destination: "50", Train3: "24C", Wait3: json.RawMessage("1978")},
		{Stop: "CG", Platform: "G17O", At: "20261024205903", Destination: "50", Train: "24C", Wait1: json.RawMessage("142")},
		{Stop: "TE", Platform: "TE5O", At: "20261024205903", Destination: "50", Train: "24C", Wait1: json.RawMessage("247")},
	}
	batch := collectMetroPoints(data, r.directionTopology, static, now)
	r.batch = batch
	key := "metro:green|50|24C"
	if len(batch.groups[key]) != 3 {
		t.Fatalf("captured slots did not produce one named context: %+v", batch.groups[key])
	}
	if selected := r.selectedContexts(batch, now); len(selected) != 1 || selected[0] != key || len(batch.episodePoints(key)) != 2 {
		t.Fatalf("captured source sequence was not scoped: %v", selected)
	}
}

func TestMetroEpisodeScopeContinuesWithFreshDownstream(t *testing.T) {
	r, first, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	original := append([]metroPoint{}, first.groups[key]...)
	for step := 0; step <= 4; step++ {
		sampleAt := now.Add(time.Duration(step) * 30 * time.Second)
		batch := &metroPointBatch{groups: map[string][]metroPoint{key: {}}, paths: first.paths, references: first.references, rejected: map[string]bool{}}
		for _, point := range original {
			point.Clock = point.Clock.Add(time.Duration(step) * 30 * time.Second)
			point.Seconds = ptr(*point.Seconds - step*30)
			batch.groups[key] = append(batch.groups[key], point)
		}
		r.batch = batch
		if selected := r.selectedContexts(batch, sampleAt); len(selected) != 1 || selected[0] != key {
			t.Fatalf("fresh downstream support lost at update %d: %v", step, selected)
		}
		admitted := r.admitTrack(metroTrackAdmission{key: key, batch: batch, now: sampleAt})
		if admitted != track {
			t.Fatalf("episode changed at update %d", step)
		}
		r.applyPoints(track, batch.episodePoints(key), sampleAt)
		if track.Train.Association != "supported" || track.Train.Calls[1].Arrival.Inferred == nil || !track.Train.SourceUpdatedAt.Equal(original[1].Clock.Add(time.Duration(step)*30*time.Second)) {
			t.Fatalf("continuity or clock lost at update %d: %+v", step, track.Train)
		}
	}
}

func TestMetroEpisodeScopeNewPointCannotRenewStaleAnchor(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	now = now.Add(56 * time.Second) // CG's original source clock is 59 seconds old.
	key := "metro:green|50|24C"
	// A different prior point had a newer clock but is absent from this update.
	track.Train.SourceUpdatedAt = now.Add(-time.Second)
	track.Train.ValidUntil = now.Add(time.Minute)
	anchor := track.Train.SourceUpdatedAt
	delete(track.Points, "TE")
	batch.groups[key][0].Clock = now.Add(-time.Second)
	batch.groups[key][2].Clock = now.Add(-2 * time.Second)
	if selected := r.selectedContexts(batch, now); len(selected) != 1 || selected[0] != key {
		t.Fatalf("fresh retained anchor did not remain usable: %v", selected)
	}
	r.applyPoints(track, batch.episodePoints(key), now)
	if !track.Train.SourceUpdatedAt.Equal(anchor) {
		t.Fatal("a newly seen downstream row renewed the old episode")
	}
	if _, exists := track.Points["TE"]; exists {
		t.Fatal("an uncorroborated newer downstream point became an anchor")
	}
	r.plan = &StaticData{PlanID: "fixture"}
	r.publication = &MetroData{Stations: []MetroStation{{ID: "CS", Name: "CS"}, {ID: "AL", Name: "AL"}, {ID: "CG", Name: "CG"}, {ID: "TE", Name: "TE"}}}
	track.Train.Destination = "TE"
	track.Train.Calls[3].StopId = "metro:TE"
	track.Train.Calls[3].OwnPrediction = &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: ptr(now.Add(time.Minute))}
	context := r.pathForecastContext(key, now)
	b := metroFrameBuilder{metroFrameRequest: metroFrameRequest{interest: metroInterest{Stop: "metro:TE"}}, now: now, frame: api.MetroLiveFrame{Trains: []api.MetroTrain{track.Train}, ForecastContexts: &[]api.MetroForecastContext{}}, contexts: []api.MetroForecastContext{context}}
	b.stationForecasts()
	if len(b.frame.UnassociatedForecasts) != 1 || b.frame.UnassociatedForecasts[0].Arrival.Prediction == nil || b.frame.UnassociatedForecasts[0].OwnPrediction != nil {
		t.Fatalf("uncorroborated downstream source forecast was hidden or inherited the episode model: calls=%+v contexts=%+v", b.frame.UnassociatedForecasts, context)
	}
	// Both upstream and original anchor disappear; the excluded point alone cannot extend it.
	nextAt := now.Add(time.Second)
	next := &metroPointBatch{groups: map[string][]metroPoint{key: {batch.groups[key][2]}}, paths: batch.paths, references: batch.references, rejected: map[string]bool{}}
	next.groups[key][0].Clock = nextAt
	r.batch = next
	if selected := r.selectedContexts(next, nextAt); len(selected) != 0 || track.Train.Association != "suspended" || !track.Train.SourceUpdatedAt.Equal(anchor) {
		t.Fatalf("episode survived after its last supported anchor expired: %v", selected)
	}
}

func TestMetroEpisodeScopeDoesNotRestoreSupersededPoint(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	delete(track.Points, "TE")
	newer := batch.groups[key][2]
	newer.Clock = newer.Clock.Add(time.Second)
	newer.Seconds = ptr(*newer.Seconds - 1)
	batch.groups[key] = append(batch.groups[key], newer)
	if selected := r.selectedContexts(batch, now); len(selected) != 1 {
		t.Fatalf("supported downstream anchor lost: %v", selected)
	}
	r.applyPoints(track, batch.episodePoints(key), now)
	if _, exists := track.Points["TE"]; exists || !batch.episodeExcludes(key, "TE") {
		t.Fatal("a superseded station point became an anchor or inherited the episode model")
	}
	forecasts := metroLatestForecastPoints(batch.groups[key])
	for _, point := range forecasts {
		if point.Stop == "TE" && point.Clock.Equal(newer.Clock) {
			return
		}
	}
	t.Fatal("the independent latest source forecast was lost")
}

func TestMetroEpisodeScopeDoesNotInferFromSupersededZero(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	older := batch.groups[key][2]
	older.Seconds = ptr(0)
	prior := older
	prior.Clock, prior.Seconds = older.Clock.Add(-2*time.Second), ptr(2)
	track.Points["TE"] = prior
	newer := older
	newer.Clock, newer.Seconds = older.Clock.Add(time.Second), nil
	batch.groups[key][2] = older
	batch.groups[key] = append(batch.groups[key], newer)
	if selected := r.selectedContexts(batch, now); len(selected) != 1 {
		t.Fatalf("supported downstream anchor lost: %v", selected)
	}
	r.applyPoints(track, batch.episodePoints(key), now)
	if track.Train.Calls[3].Arrival.Inferred != nil {
		t.Fatal("a superseded zero created an inferred terminal arrival")
	}
}

func TestMetroEpisodeScopeDoesNotAttachModelToOtherPlatform(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	delete(track.Points, "TE")
	earlier := batch.groups[key][2]
	earlier.Seconds = nil
	newer := batch.groups[key][2]
	newer.Platform, newer.Clock = "TE6O", earlier.Clock.Add(time.Second)
	batch.groups[key][2] = earlier
	batch.groups[key] = append(batch.groups[key], newer)
	if selected := r.selectedContexts(batch, now); len(selected) != 1 || !batch.episodeExcludes(key, "TE") {
		t.Fatal("an excluded newer platform could inherit the episode model")
	}
}

func TestMetroEpisodeScopeSameClockAnchorCanAdvance(t *testing.T) {
	r, first, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	delete(track.Points, "TE")
	original := append([]metroPoint{}, first.groups[key]...)
	for step := 0; step <= 4; step++ {
		sampleAt := now.Add(time.Duration(step) * 30 * time.Second)
		batch := &metroPointBatch{groups: map[string][]metroPoint{key: {}}, paths: first.paths, references: first.references, rejected: map[string]bool{}}
		for _, point := range original {
			point.Clock = point.Clock.Add(time.Duration(step) * 30 * time.Second)
			point.Seconds = ptr(*point.Seconds - step*30)
			batch.groups[key] = append(batch.groups[key], point)
		}
		r.batch = batch
		if selected := r.selectedContexts(batch, sampleAt); len(selected) != 1 || selected[0] != key {
			t.Fatalf("same-clock anchor failed at update %d: %v", step, selected)
		}
		r.applyPoints(track, batch.episodePoints(key), sampleAt)
		if track.Train.Association != "supported" || track.Points["TE"].Seconds == nil {
			t.Fatalf("co-observed downstream point lost at update %d", step)
		}
	}
	sampleAt := now.Add(145 * time.Second)
	batch := &metroPointBatch{groups: map[string][]metroPoint{key: {
		{Stop: "CS", Platform: "CS27O", Clock: sampleAt, Seconds: ptr(1832)},
		{Stop: "CG", Platform: "G17O", Clock: sampleAt, Seconds: ptr(0)},
		{Stop: "TE", Platform: "TE5O", Clock: sampleAt, Seconds: ptr(102)},
	}}, paths: first.paths, references: first.references, rejected: map[string]bool{}}
	r.batch = batch
	if selected := r.selectedContexts(batch, sampleAt); len(selected) != 1 {
		t.Fatalf("coherent new arrival lost the episode: %v", selected)
	}
	r.applyPoints(track, batch.episodePoints(key), sampleAt)
	if track.Train.Association != "supported" || track.Train.Calls[1].Phase != "previous" || track.Train.Calls[2].Phase != "current" || track.Train.Calls[3].Phase != "future" {
		t.Fatal("coherent zero transition regressed station phases")
	}
}

func TestMetroEpisodeScopeContinuesWhenSeparateUpstreamDisappears(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	if selected := r.selectedContexts(batch, now); len(selected) != 1 {
		t.Fatal("initial bounded episode was not selected")
	}
	r.applyPoints(track, batch.episodePoints(key), now)
	nextAt := now.Add(20 * time.Second)
	next := &metroPointBatch{groups: map[string][]metroPoint{key: append([]metroPoint{}, batch.groups[key][1:]...)}, paths: batch.paths, references: batch.references, rejected: map[string]bool{}}
	for n := range next.groups[key] {
		next.groups[key][n].Clock = next.groups[key][n].Clock.Add(20 * time.Second)
		next.groups[key][n].Seconds = ptr(*next.groups[key][n].Seconds - 20)
	}
	r.batch = next
	if selected := r.selectedContexts(next, nextAt); len(selected) != 1 || selected[0] != key {
		t.Fatalf("fresh retained anchors could not continue without the upstream row: %v", selected)
	}
	r.applyPoints(track, next.episodePoints(key), nextAt)
	if track.Train.Association != "supported" || !track.ScopedContinuity || !track.Train.SourceUpdatedAt.Equal(now.Add(17*time.Second)) {
		t.Fatal("bounded continuing episode lost its fresh anchor")
	}
}

func TestMetroScopedContinuityFencesOldForecastWork(t *testing.T) {
	_, _, track, now := metroEpisodeScopeFixture()
	copy := *track
	copy.Train = cloneMetroTrain(track.Train)
	track.ScopedContinuity = true
	if metroOwnForecastWorkCurrent(track, copy, now) {
		t.Fatal("forecast work from before the scoped boundary was accepted")
	}
}

func TestMetroScopedContinuityDoesNotBypassAmbiguousPrior(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	track.ScopedContinuity = true
	delete(track.Points, "TE")
	other := *track
	other.Train = cloneMetroTrain(track.Train)
	other.Train.JourneyId = "other"
	r.tracks["other"] = &other
	r.active["metro:green|opposite|24C"] = "other"
	batch.groups[key] = []metroPoint{batch.groups[key][2]}
	if selected := r.selectedContexts(batch, now); len(selected) != 0 || track.Train.Association != "suspended" {
		t.Fatalf("ambiguous prior bypassed the scoped anchor fence: %v", selected)
	}
}

func TestMetroScopedEpisodeExpiresAtItsValidityBoundary(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	track.ScopedContinuity = true
	track.Train.ValidUntil = now
	batch.groups[key] = []metroPoint{batch.groups[key][2]}
	batch.groups[key][0].Clock = now
	if selected := r.selectedContexts(batch, now); len(selected) != 1 {
		t.Fatalf("fresh independent forecast was lost at expiry: %v", selected)
	}
	data := &MetroData{Stations: []MetroStation{{ID: "CS"}, {ID: "AL"}, {ID: "CG"}, {ID: "TE"}}}
	static := &StaticData{Schedule: &Schedule{}}
	admitted := r.admitTrack(metroTrackAdmission{key: key, batch: batch, data: data, static: static, now: now})
	if admitted == track {
		t.Fatal("expired scoped episode was renewed at its exact validity boundary")
	}
}

func TestMetroStationForecastDedupRequiresMatchingSourceEstimate(t *testing.T) {
	now := time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC)
	prediction := &api.CallTimeEvidence{At: now.Add(time.Minute), SourceUpdatedAt: &now, ValidUntil: ptr(now.Add(time.Minute))}
	call := api.StopCall{Id: "linked", StopId: "station", Arrival: api.CallTime{Kind: "prediction", Prediction: prediction}}
	train := api.MetroTrain{RouteId: "line", Reference: "7", Association: "supported", SourceUpdatedAt: now.Add(10 * time.Second), ValidUntil: now.Add(time.Minute), NextIndex: ptr(0), DirectionEvidence: &api.MetroDirectionEvidence{State: "estimated"}, Calls: []api.StopCall{call}}
	changedClock := *prediction
	changedClock.SourceUpdatedAt = ptr(now.Add(time.Second))
	changedValue := *prediction
	changedValue.At = prediction.At.Add(time.Second)
	rows := []api.StopCall{call, call, call}
	rows[1].Id, rows[1].Arrival.Prediction = "other-clock", &changedClock
	rows[2].Id, rows[2].Arrival.Prediction = "other-value", &changedValue
	b := metroFrameBuilder{metroFrameRequest: metroFrameRequest{interest: metroInterest{Stop: "station"}}, now: now, frame: api.MetroLiveFrame{Trains: []api.MetroTrain{train}, ForecastContexts: &[]api.MetroForecastContext{}}, contexts: []api.MetroForecastContext{{RouteId: "line", Reference: "7", Status: "admissible", Calls: rows}}}
	b.stationForecasts()
	if len(b.frame.UnassociatedForecasts) != 2 {
		t.Fatal("station board hid a distinct clock or same-clock correction", b.frame.UnassociatedForecasts)
	}
}

func TestMetroEpisodeScopeDoesNotHidePassageCorrection(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	batch.groups[key] = append(batch.groups[key], metroPoint{Stop: "AL", Platform: "AL3ALO", Clock: now, Seconds: ptr(30)})
	if selected := r.selectedContexts(batch, now); len(selected) != 0 {
		t.Fatalf("contradictory passage retained an association: %v", selected)
	}
	if track.Train.Calls[1].Arrival.Inferred != nil || track.Train.Association != "suspended" {
		t.Fatal("new same-visit correction was hidden or regressive progress was accepted")
	}
}

func TestMetroEpisodeScopeCorrectsRetainedBoundPointBeforeSuspending(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	track.Points["AL"] = track.Proofs["arrival"].After
	batch.groups[key] = append(batch.groups[key], metroPoint{Stop: "AL", Platform: "AL3ALO", Clock: now, Seconds: ptr(30)})
	if selected := r.selectedContexts(batch, now); len(selected) != 0 || track.Train.Association != "suspended" || track.Train.Calls[1].Arrival.Inferred != nil {
		t.Fatalf("retained bound correction was hidden: %v %+v", selected, track.Train)
	}
}

func metroEpisodeEarlierCorrectionFixture() (*metroRuntime, *metroPointBatch, *metroTrack, time.Time) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	path := batch.paths[key]
	path.Stops = []string{"X", "CS", "AL", "CG", "TE"}
	batch.paths[key] = path
	track.Codes = append([]string{}, path.Stops...)
	track.Train.Calls = append([]api.StopCall{{Id: "run:X", StopId: "X"}}, track.Train.Calls...)
	track.Train.NextIndex = ptr(3)
	passed := now.Add(-50 * time.Second)
	track.Train.Calls[1].Arrival = api.CallTime{Kind: "inferred", At: &passed, Inferred: &api.MetroEventEvidence{At: passed, WindowStart: passed.Add(-6 * time.Second), WindowEnd: passed, Mode: "inferred_arrival", ModelVersion: metroArrivalModel}}
	track.Proofs["earlier"] = metroEventProof{Train: api.MetroTrain{JourneyId: "run"}, CallID: "run:CS", Before: metroPoint{Stop: "CS", Platform: "CS27O", Clock: passed.Add(-6 * time.Second), Seconds: ptr(8)}, After: metroPoint{Stop: "CS", Platform: "CS27O", Clock: passed, Seconds: ptr(0)}}
	batch.groups[key] = []metroPoint{{Stop: "X", Platform: "X1", Clock: now, Seconds: ptr(1978)}, {Stop: "CS", Platform: "CS27O", Clock: now, Seconds: ptr(30)}, batch.groups[key][1], batch.groups[key][2]}
	return r, batch, track, now
}

func TestMetroEpisodeScopeEarlierCorrectionSuspendsAndWithdraws(t *testing.T) {
	r, batch, track, now := metroEpisodeEarlierCorrectionFixture()
	clock := track.Train.SourceUpdatedAt
	if selected := r.selectedContexts(batch, now); len(selected) != 0 || track.Train.Association != "suspended" {
		t.Fatalf("contradictory prior episode was not suspended: %v", selected)
	}
	if track.Train.Calls[1].Arrival.Inferred != nil || track.Train.Calls[2].Arrival.Inferred == nil || !track.Train.SourceUpdatedAt.Equal(clock) || !track.UncommittedRetraction {
		t.Fatalf("validated earlier correction was hidden or renewed episode: %+v", track.Train)
	}
}

func TestMetroEpisodeScopeRetainedEarlierCorrectionUsesTransitionProof(t *testing.T) {
	r, batch, track, now := metroEpisodeEarlierCorrectionFixture()
	key := "metro:green|50|24C"
	batch.groups[key] = batch.groups[key][1:] // No unrelated upstream point is needed to enforce the boundary.
	track.Points["CS"] = track.Proofs["earlier"].After
	clock := track.Train.SourceUpdatedAt
	if selected := r.selectedContexts(batch, now); len(selected) != 0 || track.Train.Association != "suspended" {
		t.Fatalf("earlier correction retained association: %v", selected)
	}
	if track.Train.Calls[1].Arrival.Inferred != nil || track.Train.Calls[2].Arrival.Inferred == nil || !track.Train.SourceUpdatedAt.Equal(clock) || !track.UncommittedRetraction {
		t.Fatal("the supported correction was hidden or renewed the episode")
	}
}

func TestMetroEpisodeScopeCorrectionWithoutPointHasEventProof(t *testing.T) {
	r, batch, track, now := metroEpisodeEarlierCorrectionFixture()
	r.archiveAvailable = true
	original := track.Proofs["earlier"]
	original.Train.Calls = []api.StopCall{track.Train.Calls[1]}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	old := patterns.MetroEventRecord{Journey: "run", At: original.After.Clock, Payload: raw}
	if _, exists := track.Points["CS"]; exists {
		t.Fatal("test requires the earlier source point to be absent")
	}
	r.selectedContexts(batch, now)
	if len(r.pending) != 1 {
		t.Fatalf("correction event not queued: %d", len(r.pending))
	}
	records := []patterns.MetroEventRecord{old}
	for _, record := range r.pending {
		var proof metroEventProof
		if err := json.Unmarshal(record.Payload, &proof); err != nil || proof.CallID != "run:CS" || proof.Train.Calls[0].Arrival.Inferred != nil || !proof.Before.Clock.Equal(original.After.Clock) || !proof.After.Clock.Equal(now) {
			t.Fatalf("correction event did not retain the exact transition: %+v %v", proof, err)
		}
		records = append(records, record)
	}
	b := metroFrameBuilder{metroFrameRequest: metroFrameRequest{interest: metroInterest{Journey: "run"}}, now: now, frame: api.MetroLiveFrame{Recovery: &api.MetroJourneyRecovery{}}}
	b.restoreLegacyRecords(records)
	if len(b.frame.Trains) != 1 || len(b.frame.Trains[0].Calls) != 1 || b.frame.Trains[0].Calls[0].Arrival.Inferred != nil {
		t.Fatal("legacy event recovery resurrected the corrected arrival")
	}
}

func TestMetroEpisodeScopeDoesNotApplyAmbiguousEarlierCorrections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*metroRuntime, *metroPointBatch, *metroTrack, time.Time)
	}{
		{"missing transition proof", func(_ *metroRuntime, _ *metroPointBatch, t *metroTrack, _ time.Time) { delete(t.Proofs, "earlier") }},
		{"wrong platform", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ time.Time) {
			b.groups["metro:green|50|24C"][1].Platform = "other"
		}},
		{"late correction", func(_ *metroRuntime, b *metroPointBatch, t *metroTrack, now time.Time) {
			arrival := t.Train.Calls[1].Arrival.Inferred
			arrival.At = now.Add(-61 * time.Second)
			arrival.WindowEnd = arrival.At
			arrival.WindowStart = arrival.At.Add(-6 * time.Second)
			p := t.Proofs["earlier"]
			p.Before.Clock, p.After.Clock = arrival.WindowStart, arrival.WindowEnd
			t.Proofs["earlier"] = p
		}},
		{"older source clock", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, now time.Time) {
			b.groups["metro:green|50|24C"][1].Clock = now.Add(-4 * time.Second)
		}},
		{"unanchored downstream", func(_ *metroRuntime, _ *metroPointBatch, t *metroTrack, _ time.Time) {
			t.Points = map[string]metroPoint{}
		}},
		{"future cycle estimate", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ time.Time) {
			b.groups["metro:green|50|24C"][1].Seconds = ptr(500)
		}},
		{"source gap", func(_ *metroRuntime, _ *metroPointBatch, t *metroTrack, now time.Time) {
			t.Train.SourceUpdatedAt = now.Add(-61 * time.Second)
		}},
		{"competing direction", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ time.Time) {
			b.references["metro:green|24C"]["metro:green|opposite|24C"] = true
		}},
		{"opposite motion", func(r *metroRuntime, b *metroPointBatch, _ *metroTrack, now time.Time) {
			r.operational = map[string]*metroOperationalMotion{"metro:green|24C": {PublishedAt: now, Sign: -1, Axis: b.paths["metro:green|50|24C"]}}
		}},
		{"conflicting duplicate platforms", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ time.Time) {
			key := "metro:green|50|24C"
			p := b.groups[key][1]
			p.Platform, p.Seconds = "other", ptr(31)
			b.groups[key] = append(b.groups[key], p)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, batch, track, now := metroEpisodeEarlierCorrectionFixture()
			tc.mutate(r, batch, track, now)
			r.selectedContexts(batch, now)
			if track.Train.Calls[1].Arrival.Inferred == nil || track.UncommittedRetraction {
				t.Fatal("uncertain observation withdrew an inferred passage")
			}
		})
	}
}

func TestMetroEpisodeScopePlatformAndMotionAmbiguity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*metroRuntime, *metroPointBatch, *metroTrack, time.Time)
	}{
		{"contradictory duplicate platforms", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ time.Time) {
			key := "metro:green|50|24C"
			p := b.groups[key][1]
			p.Platform, p.Seconds = "other", ptr(143)
			b.groups[key] = append(b.groups[key], p)
		}},
		{"conflicting latest revisions beyond anchor", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ time.Time) {
			key := "metro:green|50|24C"
			p := b.groups[key][2]
			p.Clock = p.Clock.Add(time.Second)
			b.groups[key] = append(b.groups[key], p)
			p.Seconds = ptr(*p.Seconds + 1)
			b.groups[key] = append(b.groups[key], p)
		}},
		{"expired downstream", func(_ *metroRuntime, b *metroPointBatch, tr *metroTrack, now time.Time) {
			key := "metro:green|50|24C"
			for n := 1; n < len(b.groups[key]); n++ {
				b.groups[key][n].Clock = now.Add(-61 * time.Second)
				p := tr.Points[b.groups[key][n].Stop]
				p.Clock = b.groups[key][n].Clock
				tr.Points[p.Stop] = p
			}
		}},
		{"opposite modeled motion", func(r *metroRuntime, b *metroPointBatch, _ *metroTrack, now time.Time) {
			r.operational = map[string]*metroOperationalMotion{"metro:green|24C": {PublishedAt: now, Sign: -1, Axis: b.paths["metro:green|50|24C"]}}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, batch, track, now := metroEpisodeScopeFixture()
			tc.mutate(r, batch, track, now)
			clock := track.Train.SourceUpdatedAt
			if selected := r.selectedContexts(batch, now); len(selected) != 0 || track.Train.Association != "suspended" || !track.Train.SourceUpdatedAt.Equal(clock) {
				t.Fatalf("ambiguous evidence renewed or selected episode: %v", selected)
			}
		})
	}
}

func TestMetroEpisodeScopeEquivalentDuplicatePlatforms(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	point := batch.groups[key][1]
	point.Platform = "ZZZ"
	batch.groups[key] = append(batch.groups[key], point)
	if selected := r.selectedContexts(batch, now); len(selected) != 1 || selected[0] != key {
		t.Fatalf("equivalent same-clock, same-wait platforms lost established support: %v", selected)
	}
	r.applyPoints(track, batch.episodePoints(key), now)
	if track.Train.Association != "supported" || track.Train.Calls[1].Phase != "previous" || track.Train.Calls[2].Phase != "future" {
		t.Fatal("equivalent platform forecasts changed episode progress")
	}
}

func TestMetroEpisodeScopeAllowsTerminalTransition(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	track.Train.NextIndex = ptr(3)
	track.Points = map[string]metroPoint{"TE": {Stop: "TE", Platform: "TE5O", Clock: now.Add(-3 * time.Second), Seconds: ptr(1)}}
	batch.groups[key] = []metroPoint{batch.groups[key][0], {Stop: "TE", Platform: "TE5O", Clock: now.Add(-time.Second), Seconds: ptr(0)}}
	if selected := r.selectedContexts(batch, now); len(selected) != 1 || selected[0] != key {
		t.Fatalf("new terminal transition lost to unrelated upstream forecast: %v", selected)
	}
	r.applyPoints(track, batch.episodePoints(key), now)
	if track.Train.Calls[3].Arrival.Inferred == nil || track.Train.Lifecycle == nil || track.Train.Lifecycle.State != "completed" {
		t.Fatal("supported terminal transition was not recorded and closed")
	}
}

func TestMetroEpisodeScopeRejectsUnprovenUpstreamBeforeRenewal(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*metroPointBatch, *metroTrack)
	}{
		{"missing ETA", func(b *metroPointBatch, _ *metroTrack) { b.groups["metro:green|50|24C"][0].Seconds = nil }},
		{"not a later cycle", func(b *metroPointBatch, _ *metroTrack) { b.groups["metro:green|50|24C"][0].Seconds = ptr(100) }},
		{"missing proof", func(_ *metroPointBatch, tr *metroTrack) { tr.Proofs = nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, batch, track, now := metroEpisodeScopeFixture()
			tc.mutate(batch, track)
			clock := track.Train.SourceUpdatedAt
			if selected := r.selectedContexts(batch, now); len(selected) != 0 || track.Train.Association != "suspended" || !track.Train.SourceUpdatedAt.Equal(clock) {
				t.Fatalf("unproven upstream data renewed the episode: %v %+v", selected, track.Train)
			}
		})
	}
}

func TestMetroEpisodeScopePreservesCapturedDownstream(t *testing.T) {
	r, batch, track, now := metroEpisodeScopeFixture()
	key := "metro:green|50|24C"
	selected := r.selectedContexts(batch, now)
	if len(selected) != 1 || selected[0] != key || track.Train.Association != "supported" {
		t.Fatalf("the supported downstream episode was suspended: selected=%v association=%s", selected, track.Train.Association)
	}
	if len(batch.groups[key]) != 3 || batch.groups[key][0].Stop != "CS" {
		t.Fatal("the separate upstream forecast was removed from the raw batch")
	}
	if got := batch.episodePoints(key); len(got) != 2 || got[0].Stop != "CG" || got[1].Stop != "TE" {
		t.Fatalf("incorrect episode view: %+v", got)
	}
	// Excluded upstream input cannot renew the episode, update it, or inherit its model.
	upstreamAt, validUntil := now.Add(10*time.Minute), now.Add(time.Minute)
	track.Train.Calls[0].OwnPrediction = &api.CallTimeEvidence{At: upstreamAt, ValidUntil: &validUntil}
	admitted := r.admitTrack(metroTrackAdmission{key: key, batch: batch, now: now})
	if admitted != track {
		t.Fatal("admission did not preserve the selected episode")
	}
	r.applyPoints(admitted, batch.episodePoints(key), now)
	if track.Train.Association != "supported" || track.Train.NextIndex == nil || *track.Train.NextIndex != 2 || track.Train.Calls[1].Arrival.Inferred == nil {
		t.Fatalf("projection lost the episode or inferred passage: %+v", track.Train)
	}
	if track.Train.Calls[1].Phase != "previous" || track.Train.Calls[2].Phase != "future" {
		t.Fatalf("previous and upcoming station phases are wrong: %+v", track.Train.Calls)
	}
	if _, exists := track.Points["CS"]; exists || !track.Train.SourceUpdatedAt.Equal(now.Add(-3*time.Second)) {
		t.Fatal("upstream input leaked into the episode or renewed its clock")
	}
	r.plan = &StaticData{PlanID: "fixture"}
	r.publication = &MetroData{Stations: []MetroStation{{ID: "CS", Name: "CS"}, {ID: "AL", Name: "AL"}, {ID: "CG", Name: "CG"}, {ID: "TE", Name: "TE"}}}
	context := r.pathForecastContext(key, now)
	seen := false
	for _, call := range context.Calls {
		if call.StopName == "CS" && call.Arrival.Prediction != nil {
			seen = true
			if call.OwnPrediction != nil || call.OwnDeparturePrediction != nil {
				t.Fatal("independent upstream forecast inherited the episode's model")
			}
		}
	}
	if !seen {
		t.Fatal("independent upstream forecast was hidden")
	}
	if _, exists := linkedMetroForecastsAt([]api.MetroTrain{track.Train}, now)["metro:green|24C|CS|"]; exists {
		t.Fatal("a behind-visit model hid the separate station forecast")
	}
}

func TestMetroEpisodeScopeRejectsAmbiguousEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*metroRuntime, *metroPointBatch, *metroTrack, *time.Time)
	}{
		{"missing passage proof", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) { tr.Proofs = nil }},
		{"incompatible proof", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) {
			p := tr.Proofs["arrival"]
			p.After.Seconds = ptr(1)
			tr.Proofs["arrival"] = p
		}},
		{"stale continuity", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, now *time.Time) {
			*now = now.Add(61 * time.Second)
			tr.Train.ValidUntil = now.Add(time.Minute)
		}},
		{"expired episode", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, now *time.Time) { tr.Train.ValidUntil = *now }},
		{"suspended episode", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) { suspendMetroTrack(tr, "gap") }},
		{"pending lifecycle barrier", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) { tr.BarrierRevision = 1 }},
		{"closed episode", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) {
			tr.Train.Lifecycle.State = "completed"
		}},
		{"changed path", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) {
			tr.Codes[2], tr.Codes[3] = tr.Codes[3], tr.Codes[2]
		}},
		{"unknown direction", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) {
			tr.Train.DirectionEvidence.State = "unknown"
		}},
		{"no shared downstream support", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) {
			tr.Points = map[string]metroPoint{}
		}},
		{"downstream platform changed", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ *time.Time) {
			b.groups["metro:green|50|24C"][1].Platform = "other"
		}},
		{"downstream clock regressed", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ *time.Time) {
			b.groups["metro:green|50|24C"][1].Clock = b.groups["metro:green|50|24C"][1].Clock.Add(-time.Second)
		}},
		{"same clock changed wait", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ *time.Time) {
			b.groups["metro:green|50|24C"][1].Seconds = ptr(143)
		}},
		{"future source clock", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, now *time.Time) {
			b.groups["metro:green|50|24C"][1].Clock = now.Add(time.Second)
		}},
		{"downstream estimate jumped", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ *time.Time) {
			p := &b.groups["metro:green|50|24C"][1]
			p.Clock = p.Clock.Add(time.Second)
			p.Seconds = ptr(180)
		}},
		{"duplicate conflicting stop", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ *time.Time) {
			key := "metro:green|50|24C"
			p := b.groups[key][1]
			p.Seconds = ptr(143)
			b.groups[key] = append(b.groups[key], p)
		}},
		{"upstream previously associated", func(_ *metroRuntime, _ *metroPointBatch, tr *metroTrack, _ *time.Time) {
			tr.Points["CS"] = metroPoint{Stop: "CS", Platform: "CS27O"}
		}},
		{"upstream invalid time", func(_ *metroRuntime, b *metroPointBatch, _ *metroTrack, _ *time.Time) {
			b.groups["metro:green|50|24C"][0].Seconds = nil
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, batch, track, now := metroEpisodeScopeFixture()
			tc.mutate(r, batch, track, &now)
			key := "metro:green|50|24C"
			current, conflict := latestMetroPoints(batch.groups[key])
			if conflict {
				if selected := r.selectedContexts(batch, now); len(selected) != 0 || !batch.rejected[key] {
					t.Fatal("conflicting station was accepted")
				}
				return
			}
			if _, ok := r.scopeMetroEpisode(key, batch, current, now); ok {
				t.Fatal("ambiguous evidence admitted a scoped episode")
			}
		})
	}
}
