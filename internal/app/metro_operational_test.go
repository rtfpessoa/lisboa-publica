package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

type metroCapturedFrame struct {
	ReceivedAt time.Time     `json:"received_at"`
	Positions  []hubPosition `json:"positions"`
}

func TestMetroActualModelClockAndContextReplay(t *testing.T) {
	data, static := redDirectionInputs(t)
	var frames []metroCapturedFrame
	capturedMetroJSON(t, "metro-model-positions-20260929.json", &frames)
	if len(frames) != 12 {
		t.Fatal("actual capture count changed")
	}
	r := newMetroRuntime()
	r.observe(&data, &static, nil, frames[0].ReceivedAt)
	admitted := 0
	for _, frame := range frames {
		for _, p := range frame.Positions {
			if r.validHubModelContext(p) {
				admitted++
			}
		}
		r.observeHub(frame.Positions, frame.ReceivedAt)
	}
	for _, route := range static.Routes {
		axis := metroOperationalAxis(r.topology, route.Id)
		geometry := r.operationalAxisGeometry(route.Id, frames[0].ReceivedAt)
		t.Logf("route %s axis visits %d geometry %d", route.Id, len(axis.Stops), len(geometry))
	}
	if admitted == 0 || len(r.operational) == 0 {
		t.Fatal("real millisecond clocks or published model context rejected every row", admitted, len(r.operational))
	}
	moving := 0
	for _, m := range r.operational {
		if m.Sign != 0 {
			moving++
		}
		if m.PublishedAt.After(m.ReceivedAt.Add(providerClockSkew)) {
			t.Fatal("incorrect millisecond interpretation")
		}
	}
	t.Logf("Actual replay: %d context-admissible rows; %d retained model references; %d three-position model directions. Physical direction is not independently measured.", admitted, len(r.operational), moving)
	p := frames[0].Positions[0]
	p.Shape = "[RSRHS][IA2N9]incompatible"
	if r.validHubModelContext(p) {
		t.Fatal("contradictory geometry context accepted")
	}
}

func TestMetroOperationalMotionRequiresThreePositionsAndResetsContext(t *testing.T) {
	at := time.Now().UTC()
	m := &metroOperationalMotion{}
	for n := 0; n < 3; n++ {
		m.observe(metroOperationalSample{at.Add(time.Duration(n) * time.Second), float64(n * 10)}, hubPosition{}, at.Add(time.Duration(n)*time.Second))
		if n < 2 && m.Sign != 0 {
			t.Fatal("premature movement sign")
		}
	}
	if m.Sign != 1 {
		t.Fatal("coherent modeled movement not recognized")
	}
	m.observe(metroOperationalSample{at.Add(63 * time.Second), 40}, hubPosition{}, at.Add(63*time.Second))
	if m.Sign != 0 || len(m.Samples) != 1 {
		t.Fatal("source gap bridged")
	}
}

func TestMetroConditionalDwellAndDepartureAnchors(t *testing.T) {
	now := time.Now().UTC()
	prior := metroVisitPrior{Dwell: 20, Run: 60, DwellSamples: []int{15, 20, 40}}
	arrival := now.Add(-25 * time.Second)
	expiry := now.Add(time.Minute)
	track := &metroTrack{Profile: "synthetic", Priors: []metroVisitPrior{prior, {Dwell: 20, DwellSamples: []int{20}}}, Train: api.MetroTrain{Association: "supported", ValidUntil: expiry, SourceUpdatedAt: now, Calls: []api.StopCall{{Id: "first", Arrival: api.CallTime{Kind: "inferred", Inferred: &api.MetroEventEvidence{At: arrival}}}, {Id: "next"}}}}
	projectMetroScheduled(track, now)
	if track.Train.Calls[0].OwnDeparturePrediction == nil || !track.Train.Calls[0].OwnDeparturePrediction.At.Equal(arrival.Add(40*time.Second)) {
		t.Fatal("elapsed dwell not conditioned", track.Train.Calls[0])
	}
	departed := now.Add(-5 * time.Second)
	track.Train.Calls[0].Departure = api.CallTime{Kind: "inferred", Inferred: &api.MetroEventEvidence{At: departed}}
	projectMetroScheduled(track, now)
	if track.Train.Calls[0].OwnDeparturePrediction != nil || track.Train.Calls[1].OwnPrediction == nil || !track.Train.Calls[1].OwnPrediction.At.Equal(departed.Add(time.Minute)) {
		t.Fatal("departure occurrence ignored", track.Train.Calls)
	}
	if metroConditionalDwell(prior, time.Minute) != 0 {
		t.Fatal("unsupported tail fabricated")
	}
}

func TestMetroLifecycleCapacityNeverQueuesHalfTransition(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	old := r.tracks[data.Trains[0].JourneyId]
	next := r.startTrack(metroTrackStart{key: "metro:r|reverse|7", reference: "7", path: r.topology.Patterns[0], data: data, static: d, now: now})
	next.Train.SourceUpdatedAt = now
	next.Train.ValidUntil = now.Add(time.Minute)
	r.pendingBytes = 8 << 20
	r.handoffOperational(old, next, now)
	if _, ok := r.dirty[old.Train.JourneyId]; ok {
		t.Fatal("predecessor half admitted")
	}
	if _, ok := r.dirty[next.Train.JourneyId]; ok {
		t.Fatal("successor half admitted")
	}
	if old.Train.Association != "suspended" || next.Train.Association != "supported" || !old.CheckpointUnavailable || !next.CheckpointUnavailable {
		t.Fatal("disk capacity rolled back memory or concealed durability")
	}
	r.evictRetired()
	r.prune(now.Add(time.Second))
	if r.tracks[old.Train.JourneyId] == nil || r.tracks[next.Train.JourneyId] == nil {
		t.Fatal("failed complete lifecycle generation evicted one member")
	}
	r.pendingBytes = 0
	r.queueCurrentCheckpoints()
	r.flushCheckpoints(context.Background(), s.Patterns, now)
	if old.Generation == "" || old.Generation != next.Generation {
		t.Fatal("retry did not commit complete generation")
	}
}

func TestMetroCaptureDuplicateClocksGapAcknowledgementAndBounds(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	r := s.Cache.metroRuntime
	r.observe(data, d, s.Patterns, now)
	first := len(r.capture.Queue)
	r.observe(data, d, s.Patterns, now.Add(time.Second))
	if len(r.capture.Queue) != first {
		t.Fatal("unchanged receipt produced capture")
	}
	r.markCaptureGap()
	r.captureChanged(now.Add(2 * time.Second))
	if !r.capture.Gap {
		t.Fatal("queued gap was treated as durable")
	}
	r.historyCollecting(now)
	if r.historyStatus == "collecting" {
		t.Fatal("unrelated acknowledgement concealed gap")
	}
	r.flushInputs(context.Background(), s.Patterns, now.Add(2*time.Second))
	if r.capture.Gap || len(r.capture.Queue) != 0 {
		t.Fatal("matching capture did not acknowledge gap")
	}
	for n := 0; n < 70; n++ {
		data.Waits = []MetroWait{metroTestRow(now.Add(time.Duration(n+3)*time.Second), "RM", "7", "120")}
		r.observe(data, d, s.Patterns, now.Add(time.Duration(n+3)*time.Second))
	}
	if len(r.capture.Queue) > 64 || r.capture.Bytes > 8<<20 || !r.capture.Gap {
		t.Fatal("unbounded queue or missing capacity gap")
	}
	t.Logf("Capture queue peak: %d records, %d bytes; 256 KiB record maximum.", len(r.capture.Queue), r.capture.Bytes)
}

func TestMetroAnonymousSlotShiftRetainsEvidenceTrack(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	row := metroTestRow(now, "RM", "", "120")
	row.Train2 = "8"
	row.Wait2 = json.RawMessage("240")
	data.Waits = []MetroWait{row}
	publishMetroTest(s, d, data, now)
	contexts := s.Cache.metroRuntime.forecastContexts(now)
	var anonymous string
	for _, c := range contexts {
		for _, visit := range c.Calls {
			if visit.ServiceLabel == nil {
				anonymous = visit.Id
			}
		}
	}
	row.At = now.Add(time.Second).In(lisbon).Format("20060102150405")
	row.Train = "7"
	row.Wait1 = json.RawMessage("20")
	row.Train2 = ""
	row.Wait2 = json.RawMessage("119")
	data.Waits = []MetroWait{row}
	publishMetroTest(s, d, data, now.Add(time.Second))
	contexts = s.Cache.metroRuntime.forecastContexts(now.Add(time.Second))
	found := false
	for _, c := range contexts {
		for _, visit := range c.Calls {
			if visit.ServiceLabel == nil {
				found = visit.Id == anonymous
				if visit.MetroForecast.UnknownCandidate == nil || !*visit.MetroForecast.UnknownCandidate {
					t.Fatal("unseen candidate discarded")
				}
			}
		}
	}
	if !found {
		t.Fatal("unique anonymous slot shift lost evidence identity")
	}
}

func TestMetroNoDepartureFromFutureOrMissingMovement(t *testing.T) {
	now := time.Now().UTC()
	r := newMetroRuntime()
	r.operational = map[string]*metroOperationalMotion{"metro:r|7": {PublishedAt: now, ReceivedAt: now, Samples: []metroOperationalSample{{now, 0}}, Sign: 0}}
	track := &metroTrack{Codes: []string{"A", "B"}, Train: api.MetroTrain{RouteId: "metro:r", Reference: "7", Association: "supported", ValidUntil: now.Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "estimated"}, Calls: []api.StopCall{{Id: "a", Arrival: api.CallTime{Inferred: &api.MetroEventEvidence{At: now.Add(-time.Second)}}}, {Id: "b"}}}, Points: map[string]metroPoint{"B": {"B", "1", now.Add(time.Second), ptr(0)}}}
	r.projectMetroExperimentalDepartures(track, now)
	if track.Train.Calls[0].Departure.Inferred != nil {
		t.Fatal("future/missing model movement produced departure")
	}
}

func TestMetroFirstClearMovementDeclaresExperimentalDeparture(t *testing.T) {
	now := time.Now().UTC()
	stopped := now.Add(-10 * time.Second)
	first := now.Add(-time.Second)
	r := newMetroRuntime()
	m := &metroOperationalMotion{Axis: patterns.Pattern{Stops: []string{"A", "B"}}, PublishedAt: now, Sign: 0, Samples: []metroOperationalSample{{stopped, 100}, {first, 108}, {now, 108}}}
	r.operational = map[string]*metroOperationalMotion{"metro:r|7": m}
	track := &metroTrack{Codes: []string{"A", "B"}, FixedModelSign: 1, ModelStops: map[string]metroModelStop{"a": {ModelAt: stopped, DirectAt: stopped, Metres: 100}}, Train: api.MetroTrain{RouteId: "metro:r", Reference: "7", Association: "supported", ValidUntil: now.Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "estimated"}, Calls: []api.StopCall{{Id: "a", Arrival: api.CallTime{Inferred: &api.MetroEventEvidence{At: stopped}}}, {Id: "b"}}}, Points: map[string]metroPoint{"B": {Stop: "B", Clock: now, Seconds: ptr(60)}}}
	track.FixedModelSupport = map[int64]metroModelPublication{}
	for n := 1; n <= 3; n++ {
		p := hubPosition{At: stopped.Add(-time.Duration(n) * time.Second).UnixMilli(), Lon: float64(n)}
		track.FixedModelSupport[p.At] = metroPublicationStamp(p)
	}
	for _, at := range []time.Time{stopped, first, now} {
		m.rememberPublication(hubPosition{At: at.UnixMilli()})
	}
	r.tracks["run"] = track
	r.projectMetroExperimentalDepartures(track, now)
	departure := track.Train.Calls[0].Departure.Inferred
	if departure == nil || !departure.At.Equal(first) || departure.ConfirmedAt == nil || !departure.ConfirmedAt.Equal(now) || departure.Mode != "model_departure" {
		t.Fatal("first clear movement or separate confirmation lost", departure)
	}
	if len(track.ModelDepartureSupport["a"]) != 6 {
		t.Fatal("direction qualification stamps omitted from occurrence support")
	}
	corrected := hubPosition{At: stopped.Add(-time.Second).UnixMilli(), Lon: 10}
	r.correctHubModel("metro:r|7", corrected)
	if track.Train.Calls[0].Departure.Inferred != nil {
		t.Fatal("corrected direction sample did not withdraw dependent departure")
	}
}

func TestMetroLifecycleExpiryDropsWholeGenerationWithGap(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	old := r.tracks[data.Trains[0].JourneyId]
	next := r.startTrack(metroTrackStart{key: "metro:r|reverse|7", reference: "7", path: r.topology.Patterns[0], data: data, static: d, now: now})
	next.Train.SourceUpdatedAt = now
	old.Train.SourceUpdatedAt = now.Add(-8 * 24 * time.Hour)
	r.handoffOperational(old, next, now)
	if batch := r.checkpointBatch(now); len(batch) != 0 {
		t.Fatal("expired lifecycle committed surviving subset", len(batch))
	}
	if len(r.lifecycleGroups) != 0 || !r.capture.Gap || old.LifecycleGroup != "" || next.LifecycleGroup != "" {
		t.Fatal("unbounded expired group or hidden lost generation")
	}
}

func TestMetroModelResetInvalidatesProspectiveStopsWithoutWithdrawingHistory(t *testing.T) {
	now := time.Now().UTC()
	e := &api.MetroEventEvidence{At: now.Add(-time.Minute), Mode: "model_departure", ModelVersion: metroOperationalPolicy + ":synthetic", Persistence: "committed"}
	track := &metroTrack{Profile: "synthetic", FixedModelSign: 1, ModelStops: map[string]metroModelStop{"pending": {ModelAt: now}}, Train: api.MetroTrain{Association: "supported", RouteId: "metro:r", Reference: "7", Calls: []api.StopCall{{Departure: api.CallTime{Kind: "inferred", Inferred: e}}}}}
	motion := &metroOperationalMotion{PublishedAt: now, Generation: 2, Samples: []metroOperationalSample{{now, 20}}}
	r := newMetroRuntime()
	r.operational = map[string]*metroOperationalMotion{"metro:r|7": motion}
	r.operationalEvidence(track, now)
	if track.FixedModelSign != 0 || len(track.ModelStops) != 0 || track.ModelMotion != motion {
		t.Fatal("old prospective support survived new motion generation")
	}
	if track.Train.Calls[0].Departure.Inferred != e {
		t.Fatal("uncorrected historical occurrence withdrawn by a later gap")
	}
}

func TestMetroNullDirectionAndSourceHealthRecovery(t *testing.T) {
	for _, raw := range []string{"null", "", `"unknown"`, `{}`} {
		if metroPublishedDirection(json.RawMessage(raw)) != -1 {
			t.Fatal("missing direction became direction zero", raw)
		}
	}
	if metroPublishedDirection(json.RawMessage(`"0"`)) != 0 || metroPublishedDirection(json.RawMessage(`1`)) != 1 {
		t.Fatal("published numeric/string direction rejected")
	}
	s, d, data, now := metroLiveFixture(t)
	r := s.Cache.metroRuntime
	r.observe(data, d, s.Patterns, now)
	r.hubError = "synthetic unavailable"
	r.captureChanged(now.Add(time.Second))
	failed := len(r.capture.Queue)
	r.flushInputs(context.Background(), s.Patterns, now.Add(time.Second))
	r.hubError = ""
	r.captureChanged(now.Add(2 * time.Second))
	if failed < 2 || len(r.capture.Queue) != 1 {
		t.Fatal("unchanged-input source recovery was suppressed")
	}
	status := api.MetroStatus{Status: "ok", CheckedAt: &now, LineStateUpdatedAt: ptr(now.Add(-2 * time.Minute))}
	if metroLineStateFresh(status) {
		t.Fatal("expired state admitted as current condition")
	}
	status.LineStateUpdatedAt = ptr(now)
	if !metroLineStateFresh(status) {
		t.Fatal("fresh independent state rejected")
	}
}

func TestMetroRepeatedCurrentZeroKeepsSupportedJourney(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "10"), metroTestRow(now, "CS", "7", "90")}
	publishMetroTest(s, d, data, now)
	for n := 1; n <= 3; n++ {
		at := now.Add(time.Duration(n) * time.Second).Truncate(time.Second)
		data.Waits = []MetroWait{metroTestRow(at, "RM", "7", "0"), metroTestRow(at, "CS", "7", "90")}
		publishMetroTest(s, d, data, at)
		if len(data.Trains) != 1 || data.Trains[0].Association != "supported" || data.Trains[0].CurrentIndex == nil || *data.Trains[0].CurrentIndex != 0 {
			t.Fatal("repeated supported zero regressed", data.Trains)
		}
	}
}

func TestMetroEarlierUnsupportedVisitsStayUnknown(t *testing.T) {
	train := api.MetroTrain{NextIndex: ptr(2), OriginKnown: ptr(true), Calls: []api.StopCall{{}, {}, {}}}
	metroCallPhases(&train)
	if train.Calls[0].Phase != "unknown" || train.Calls[1].Phase != "unknown" || train.Calls[2].Phase != "future" {
		t.Fatal("static origin invented passage", train.Calls)
	}
	train.Calls[1].Arrival.Inferred = &api.MetroEventEvidence{}
	metroCallPhases(&train)
	if train.Calls[1].Phase != "previous" {
		t.Fatal("supported previous occurrence hidden")
	}
}

func TestMetroConflictingNamedForecastIDsDoNotChurnOnReads(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "120"), metroTestRow(now, "RM", "7", "125")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	before := r.forecastContexts(now)
	after := r.forecastContexts(now.Add(time.Second))
	if len(before) == 0 || len(before[0].Calls) < 2 || len(after[0].Calls) != len(before[0].Calls) {
		t.Fatal("fixture must preserve competing named predictions", before, after)
	}
	for n := range before[0].Calls {
		if before[0].Calls[n].Id != after[0].Calls[n].Id {
			t.Fatal("unchanged conflict changed source identity")
		}
	}
}

func TestMetroHistoryLossHasOrderedIndependentGeneration(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	client := &MetroClient{Cache: s.Cache, History: s.Patterns}
	client.historyQueue = make(chan metroHistoryTask, 1)
	client.enqueuePatterns(data, d, now)
	client.enqueuePatterns(data, d, now.Add(time.Second))
	before := <-client.historyQueue
	client.enqueuePatterns(data, d, now.Add(2*time.Second))
	after := <-client.historyQueue
	if before.LossGeneration != 0 || after.LossGeneration != 1 || !s.Cache.metroRuntime.historyDeliveryGap {
		t.Fatal("loss was not ordered between admitted receipts")
	}
	r := s.Cache.metroRuntime
	r.capture.Gap = false
	r.historyCollecting(now)
	if r.historyStatus == "collecting" {
		t.Fatal("capture acknowledgement concealed learning gap")
	}
}

func TestMetroShortTurnAnchorCannotExcludeDownstreamCandidate(t *testing.T) {
	now := time.Now().UTC()
	r := newMetroRuntime()
	r.operational = map[string]*metroOperationalMotion{}
	c := &api.StopCall{LineKey: "metro:r", DirectionKey: ptr("forward"), StopId: "target", MetroForecast: &api.MetroForecastAssociation{Anchors: []string{}}}
	for _, ref := range []string{"7", "8"} {
		stop := "target"
		if ref == "8" {
			stop = "short-turn"
		}
		r.tracks[ref] = &metroTrack{Train: api.MetroTrain{RouteId: "metro:r", Reference: ref, DirectionCode: "forward", Association: "supported", ValidUntil: now.Add(time.Minute), DirectionEvidence: &api.MetroDirectionEvidence{State: "estimated"}, NextIndex: ptr(0), Calls: []api.StopCall{{StopId: stop, Phase: "future"}}}}
		r.active[ref] = ref
		metres := 100.0
		if ref == "7" {
			metres = 200
		}
		r.operational["metro:r|"+ref] = &metroOperationalMotion{Sign: 1, PublishedAt: now, Axis: patterns.Pattern{Direction: "axis"}, Samples: []metroOperationalSample{{now, metres}}}
	}
	if !r.metroCandidateInOrder(c, "7", []string{"8", "", ""}, 1, now) || len(c.MetroForecast.Anchors) != 0 {
		t.Fatal("non-reaching anchor constrained downstream order")
	}
}

func TestMetroCorrectedModelPublicationWithdrawsOnlyItsOccurrence(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	r := s.Cache.metroRuntime
	r.observe(data, d, s.Patterns, now)
	at := now.Add(-time.Second)
	original := hubPosition{At: at.UnixMilli(), Lat: 38.73, Lon: -9.14}
	m := &metroOperationalMotion{Context: metroHubModelContext(original), Position: original, PublishedAt: at, Samples: []metroOperationalSample{{at, 108}}}
	m.rememberPublication(original)
	r.operational = map[string]*metroOperationalMotion{"metro:r|7": m}
	makeEvent := func(eventAt time.Time) *api.MetroEventEvidence {
		return &api.MetroEventEvidence{At: eventAt, WindowStart: eventAt.Add(-10 * time.Second), WindowEnd: eventAt, ConfirmedAt: &eventAt, Mode: "model_departure", ModelVersion: metroOperationalPolicy + ":synthetic", Persistence: "committed"}
	}
	historical := makeEvent(at.Add(-time.Minute))
	affected := makeEvent(at)
	track := &metroTrack{ModelMotion: m, Profile: "synthetic", ModelDepartureSupport: map[string]map[int64]metroModelPublication{"affected": {original.At: metroPublicationStamp(original)}}, Train: api.MetroTrain{RouteId: "metro:r", Reference: "7", Calls: []api.StopCall{{Id: "historical", Departure: api.CallTime{Inferred: historical}}, {Id: "affected", Departure: api.CallTime{Inferred: affected}}}}}
	r.tracks["synthetic"] = track
	corrected := original
	corrected.Lat += .0001
	r.correctHubModel("metro:r|7", corrected)
	m.observe(metroOperationalSample{at, 100}, corrected, now)
	if track.Train.Calls[0].Departure.Inferred != historical || track.Train.Calls[1].Departure.Inferred != nil {
		t.Fatal("correction did not scope occurrence withdrawal")
	}
	if len(m.Samples) != 0 || m.Position.Lat != corrected.Lat || len(r.hubCorrections) != 1 {
		t.Fatal("correction became movement or its payload was discarded")
	}
	before := len(r.capture.Queue)
	r.captureChanged(now)
	if len(r.capture.Queue) <= before {
		t.Fatal("correction was absent from retained replay")
	}
}

func TestMetroHistoricalOwnArrivalAnchorsOwnDepartureBeforeOfficial(t *testing.T) {
	now := time.Now().UTC()
	track := &metroTrack{Profile: "synthetic", Priors: []metroVisitPrior{{Dwell: 30, Run: 60, DwellSamples: []int{30}}}, Train: api.MetroTrain{Association: "supported", ValidUntil: now.Add(time.Minute), SourceUpdatedAt: now, Calls: []api.StopCall{{Arrival: api.CallTime{Prediction: &api.CallTimeEvidence{At: now.Add(20 * time.Second)}}, OwnPrediction: &api.CallTimeEvidence{At: now.Add(80 * time.Second), ModelVersion: ptr("historical-supported")}}}}}
	projectMetroScheduled(track, now)
	c := track.Train.Calls[0]
	if c.OwnDeparturePrediction == nil || !c.OwnDeparturePrediction.At.Equal(now.Add(110*time.Second)) || !c.Arrival.Prediction.At.Equal(now.Add(20*time.Second)) {
		t.Fatal("own chain mixed its anchor with official forecast", c)
	}
}

func TestMetroDelayedRepeatAndRetiredCorrectionUseFrozenPublication(t *testing.T) {
	now := time.Now().UTC()
	at := now.Add(-5 * time.Second)
	original := hubPosition{At: at.UnixMilli(), Lat: 38.73, Lon: -9.14}
	later := original
	later.At = now.UnixMilli()
	later.Lat += .001
	motion := &metroOperationalMotion{Position: later, PublishedAt: now}
	motion.rememberPublication(original)
	motion.rememberPublication(later)
	event := &api.MetroEventEvidence{At: at, Mode: "model_departure", ModelVersion: metroOperationalPolicy + ":synthetic"}
	retired := &metroTrack{Profile: "synthetic", ModelMotion: &metroOperationalMotion{}, ModelDepartureSupport: map[string]map[int64]metroModelPublication{"visit": {original.At: metroPublicationStamp(original)}}, Train: api.MetroTrain{RouteId: "metro:r", Reference: "7", Calls: []api.StopCall{{Id: "visit", Departure: api.CallTime{Inferred: event}}}}}
	r := newMetroRuntime()
	r.operational = map[string]*metroOperationalMotion{"metro:r|7": motion}
	r.tracks["retired"] = retired
	if r.correctHubModel("metro:r|7", original) || retired.Train.Calls[0].Departure.Inferred == nil {
		t.Fatal("delayed unchanged original treated as correction")
	}
	changed := original
	changed.Lat += .0001
	if !r.correctHubModel("metro:r|7", changed) || retired.Train.Calls[0].Departure.Inferred != nil {
		t.Fatal("replaced pointer lost frozen occurrence correction")
	}
}

func TestMetroForecastCacheExpiresWithContributingCandidate(t *testing.T) {
	now := time.Now().UTC()
	r := newMetroRuntime()
	expiry := now.Add(100 * time.Millisecond)
	r.tracks["candidate"] = &metroTrack{Train: api.MetroTrain{Association: "supported", SourceUpdatedAt: now, ValidUntil: expiry}}
	r.active["scope"] = "candidate"
	call := api.StopCall{Arrival: api.CallTime{Prediction: &api.CallTimeEvidence{At: now.Add(time.Minute), ValidUntil: ptr(now.Add(time.Minute))}}}
	if got := r.forecastSnapshotExpiry([]api.MetroForecastContext{{Calls: []api.StopCall{call}}}, now); !got.Equal(expiry) {
		t.Fatal("cache outlives candidate", got, expiry)
	}
	r.forecastCacheUntil = now.Add(time.Second)
	r.current(expiry.Add(time.Millisecond))
	if !r.forecastCacheUntil.IsZero() {
		t.Fatal("current expiry did not invalidate candidate cache")
	}
}

func TestMetroForecastContextsPreserveHistoricalOwnDepartureChain(t *testing.T) {
	s, d, data, now := metroLiveFixture(t)
	data.Waits = []MetroWait{metroTestRow(now, "RM", "7", "20")}
	publishMetroTest(s, d, data, now)
	r := s.Cache.metroRuntime
	track := r.tracks[data.Trains[0].JourneyId]
	historical := &api.CallTimeEvidence{At: now.Add(80 * time.Second), ValidUntil: ptr(now.Add(time.Minute)), ModelVersion: ptr("historical-supported"), SourceUpdatedAt: &now}
	track.Train.Calls[0].OwnPrediction = historical
	track.Train.Calls[0].OwnDeparturePrediction = &api.CallTimeEvidence{At: now.Add(110 * time.Second), ValidUntil: historical.ValidUntil, SourceUpdatedAt: &now, ModelVersion: ptr("metro-schedule-prior-v1:synthetic")}
	r.forecastCacheUntil = time.Time{}
	contexts := r.forecastContexts(now)
	if len(contexts) == 0 || contexts[0].Calls[0].OwnDeparturePrediction == nil || contexts[0].Calls[0].OwnDeparturePrediction.At.Before(contexts[0].Calls[0].OwnPrediction.At) {
		t.Fatal("forecast context mixed official arrival into own departure", contexts)
	}
}

func TestMetroCorrectionBarrierCannotQualifyThroughRepeatedClock(t *testing.T) {
	at := time.Now().UTC()
	original := hubPosition{At: at.UnixMilli(), Lat: 38.73}
	m := &metroOperationalMotion{}
	m.observe(metroOperationalSample{at, 100}, original, at)
	corrected := original
	corrected.Lat += .0001
	m.observe(metroOperationalSample{at, 110}, corrected, at.Add(time.Millisecond))
	m.observe(metroOperationalSample{at, 110}, corrected, at.Add(2*time.Millisecond))
	if len(m.Samples) != 0 {
		t.Fatal("correction repeat became sample")
	}
	m.BarrierAt = at.Add(10 * time.Second)
	m.PublishedAt = at.Add(-time.Second)
	for n := 1; n <= 2; n++ {
		p := corrected
		p.At = at.Add(-time.Duration(n) * time.Second).UnixMilli()
		m.observe(metroOperationalSample{time.UnixMilli(p.At), 110}, p, at)
	}
	if !m.BarrierAt.Equal(at.Add(10 * time.Second)) {
		t.Fatal("descending delayed clock lowered barrier")
	}
	at = at.Add(10 * time.Second)
	for n := 1; n <= 2; n++ {
		p := corrected
		p.At = at.Add(time.Duration(n) * time.Second).UnixMilli()
		m.observe(metroOperationalSample{time.UnixMilli(p.At), 110 + float64(n*10)}, p, time.UnixMilli(p.At))
	}
	if m.Sign != 0 || len(m.Samples) != 2 {
		t.Fatal("direction qualified without three post-barrier publications")
	}
}

func TestMetroDescendingHubClocksNeverLowerCorrectionBarrier(t *testing.T) {
	data, static := redDirectionInputs(t)
	var frames []metroCapturedFrame
	capturedMetroJSON(t, "metro-model-positions-20260929.json", &frames)
	r := newMetroRuntime()
	received := frames[0].ReceivedAt.Add(30 * time.Second)
	r.observe(&data, &static, nil, received)
	p := frames[0].Positions[0]
	original := p.At
	p.At = original + 10000
	r.observeHub([]hubPosition{p}, received)
	for _, clock := range []int64{original, original - 5000, original + 1000, original + 2000, original + 3000} {
		p.At = clock
		r.observeHub([]hubPosition{p}, received)
	}
	if len(r.operational) != 1 {
		t.Fatal("fixture model context not admitted", len(r.operational))
	}
	for _, m := range r.operational {
		if m.BarrierAt.UnixMilli() != original+10000 || len(m.Samples) != 0 || m.Sign != 0 {
			t.Fatal("delayed clocks lowered high-water or became movement", m.BarrierAt, m.Samples)
		}
	}
}
