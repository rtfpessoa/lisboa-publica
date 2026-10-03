package app

import (
	"errors"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

// A visit already behind the train keeps the estimate that was published before it
// passed, including when it is only observed at the inference transition.
func TestMetroLastOfficialEstimateRetainedAndWithdrawn(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	clock := now.Add(-time.Minute)
	wait := 120
	zero := 0
	track := &metroTrack{
		Codes: []string{"RM"},
		// The positive wait exists only as the previous point, so the estimate must
		// come from the inference path rather than from a projected prediction.
		Points: map[string]metroPoint{"RM": {Stop: "RM", Platform: "1", Clock: clock, Seconds: &wait}},
		Train: api.MetroTrain{Association: "supported", ValidUntil: now.Add(time.Hour), Calls: []api.StopCall{{
			Id: "v0", StopId: "metro:gtfs-rm",
			Arrival:   missingCallTime("Sem previsão atual"),
			Departure: missingCallTime("Sem dados de partida"),
		}}},
	}
	runtime := newMetroRuntime()
	projection := metroPointProjection{runtime: runtime, track: track, now: now}
	if !projection.apply(0, metroPoint{Stop: "RM", Platform: "1", Clock: clock.Add(5 * time.Second), Seconds: &zero}) {
		t.Fatal("zero wait projection failed")
	}
	call := track.Train.Calls[0]
	if call.Arrival.Inferred == nil {
		t.Fatal("inferred arrival missing")
	}
	if call.LastOfficialEstimate == nil || !call.LastOfficialEstimate.At.Equal(clock.Add(2*time.Minute)) {
		t.Fatalf("inference did not retain the previous official estimate: %+v", call.LastOfficialEstimate)
	}

	// Prediction expiry clears the current prediction but never the retained estimate.
	expired := clock.Add(-time.Hour)
	call.Arrival = api.CallTime{Kind: "prediction", Prediction: &api.CallTimeEvidence{At: expired, ValidUntil: ptr(clock.Add(-30 * time.Minute))}}
	supported := api.MetroTrainAssociation("supported")
	expireMetroCall(&call, supported, now)
	if call.Arrival.Kind != "unavailable" {
		t.Fatal("expired prediction was kept as current")
	}
	if call.LastOfficialEstimate == nil {
		t.Fatal("expiry cleared the retained last official estimate")
	}

	// Suspension is historical evidence and keeps its own clock.
	suspendMetroTrack(track, "test suspension")
	if call.LastOfficialEstimate == nil {
		t.Fatal("suspension cleared the retained last official estimate")
	}

	// A retracted inferred arrival also retracts the retained estimate.
	track.Train.Calls[0].Arrival = api.CallTime{Kind: "inferred", Inferred: &api.MetroEventEvidence{At: clock, WindowStart: clock, WindowEnd: clock}}
	newer := 90
	runtime.retractArrivals(track, map[string]metroPoint{"RM": {Stop: "RM", Platform: "1", Clock: clock.Add(time.Minute), Seconds: &newer}})
	if track.Train.Calls[0].LastOfficialEstimate != nil {
		t.Fatal("retracted arrival kept a last official estimate")
	}
}

// Canonical forecast lists follow published stop order even when ids sort differently,
// and repeated canonicalization of one publication is stable.
func TestMetroForecastCallsFollowPublishedOrder(t *testing.T) {
	calls := []api.StopCall{
		{Id: "z", StopSequence: 3, StopName: "Terreiro do Paço", Arrival: api.CallTime{Kind: "prediction", At: ptr(time.Unix(300, 0).UTC())}},
		{Id: "a", StopSequence: 1, StopName: "Santa Apolónia", Arrival: api.CallTime{Kind: "prediction", At: ptr(time.Unix(100, 0).UTC())}},
		{Id: "m", StopSequence: 2, StopName: "Baixa/Chiado", Arrival: api.CallTime{Kind: "prediction", At: ptr(time.Unix(200, 0).UTC())}},
		{Id: "b", StopSequence: 0, StopName: "Reboleira", Arrival: api.CallTime{Kind: "unavailable"}},
	}
	first := canonicalMetroForecastCalls(append([]api.StopCall{}, calls...))
	order := []string{first[0].StopName, first[1].StopName, first[2].StopName, first[3].StopName}
	want := []string{"Reboleira", "Santa Apolónia", "Baixa/Chiado", "Terreiro do Paço"}
	for n := range want {
		if order[n] != want[n] {
			t.Fatalf("canonical order %v, want %v", order, want)
		}
	}
	second := canonicalMetroForecastCalls(append([]api.StopCall{}, calls...))
	for n := range first {
		if first[n].Id != second[n].Id || first[n].StopName != second[n].StopName {
			t.Fatal("canonicalization is not stable across repeated reads")
		}
	}
}

// Local-only calls carry no path sequence; the same total order must beat id order.
func TestMetroLocalForecastCallsOrderedDeterministically(t *testing.T) {
	calls := []api.StopCall{
		{Id: "a", StopName: "Roma", Arrival: api.CallTime{Kind: "unavailable"}},
		{Id: "b", StopName: "Alameda", Arrival: api.CallTime{Kind: "unavailable"}},
	}
	out := canonicalMetroForecastCalls(calls)
	if out[0].StopName != "Alameda" || out[1].StopName != "Roma" {
		t.Fatalf("local order %s, %s; id order would have been the reverse", out[0].StopName, out[1].StopName)
	}
}

// The retained real capture must produce contexts whose calls follow the published path,
// and encoding the same view twice must keep the frame revision identical.
func TestMetroRetainedCaptureContextsFollowPathOrder(t *testing.T) {
	var captures []struct {
		Data MetroData `json:"data"`
	}
	var static StaticData
	capturedMetroJSON(t, "metro-debug-direct-capture.json", &captures)
	capturedMetroJSON(t, "metro-debug-static.json", &static)
	data := captures[0].Data
	now := *data.Status.CheckedAt
	runtime := newMetroRuntime()
	runtime.observe(&data, &static, nil, now)
	contexts := runtime.forecastContexts(now)
	multi := 0
	for _, context := range contexts {
		for n := 1; n < len(context.Calls); n++ {
			if context.Calls[n].StopSequence < context.Calls[n-1].StopSequence {
				t.Fatalf("context %s not in published order: sequence %d after %d", context.Reference, context.Calls[n].StopSequence, context.Calls[n-1].StopSequence)
			}
		}
		if len(context.Calls) > 3 {
			multi++
		}
	}
	if multi == 0 {
		t.Fatal("retained capture produced no multi-stop context to check")
	}
	view, plan, viewContexts, _ := runtime.view(now)
	if view == nil || plan == nil {
		t.Fatal("runtime view missing for revision check")
	}
	frame := api.MetroLiveFrame{PublishedAt: now, PlanId: plan.PlanID, Trains: view.Trains, Vehicles: []api.Vehicle{}, Directions: []api.BoardDirection{}, UnassociatedForecasts: []api.StopCall{}, ForecastContexts: &viewContexts, HistoryStatus: "unavailable"}
	first, _, err := encodeMetroFrame(frame, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := encodeMetroFrame(frame, 1<<20)
	if err != nil || first.Revision != second.Revision {
		t.Fatalf("frame revision is not stable: %q vs %q (%v)", first.Revision, second.Revision, err)
	}
}

func metroFeedTestFetcher(t *testing.T, directAt time.Time) (*Fetcher, func(time.Time)) {
	t.Helper()
	cache := NewCache()
	direct := func(at time.Time) {
		op := cache.operator("metro")
		status := api.OperatorDirectStatusOk
		op.DirectStatus, op.DirectUpdatedAt = &status, &at
		cache.updateMetro(&MetroData{}, op)
	}
	direct(directAt)
	f := &Fetcher{}
	f.Cache = cache
	return f, direct
}

func metroFeedBatch(agency string, at, collected time.Time) hubObservationBatch {
	return hubObservationBatch{positions: []hubPosition{{Agency: agency, At: at.UnixMilli()}}, collected: collected}
}

// Each non-counting condition must pause the counter: without the guard, three counted
// batches would reach unavailable while the guard case stays unknown.
func TestMetroPositionFeedPausesWithoutEvidence(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	cases := []struct {
		name    string
		culprit func(*Fetcher, func(time.Time), time.Time)
	}{
		{"stale other agencies", func(f *Fetcher, _ func(time.Time), at time.Time) {
			f.updateMetroPositionFeed(metroFeedBatch("IA9T6", at.Add(-10*time.Minute), at))
		}},
		{"errored batch", func(f *Fetcher, _ func(time.Time), at time.Time) {
			// A hub envelope error can still carry rows; it must pause the counter anyway.
			batch := metroFeedBatch("IA9T6", at.Add(-time.Second), at)
			batch.err = errors.New("hub failure")
			f.updateMetroPositionFeed(batch)
		}},
		{"stale direct lane", func(f *Fetcher, direct func(time.Time), at time.Time) {
			direct(at.Add(-10 * time.Minute))
			f.updateMetroPositionFeed(metroFeedBatch("IA9T6", at.Add(-time.Second), at))
			direct(at.Add(time.Second))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, direct := metroFeedTestFetcher(t, base)
			f.updateMetroPositionFeed(metroFeedBatch("IA9T6", base.Add(-time.Second), base))
			tc.culprit(f, direct, base.Add(61*time.Second))
			f.updateMetroPositionFeed(metroFeedBatch("IA9T6", base.Add(62*time.Second), base.Add(62*time.Second)))
			if state, _ := f.metroPositionFeedState(); state == api.OperatorModelPositionStateUnavailable {
				t.Fatal("a non-counting batch did not pause the counter")
			}
			f.updateMetroPositionFeed(metroFeedBatch("IA9T6", base.Add(63*time.Second), base.Add(63*time.Second)))
			if state, _ := f.metroPositionFeedState(); state != api.OperatorModelPositionStateUnavailable {
				t.Fatal("three counted batches over 60 s did not mark unavailable")
			}
		})
	}
}

// The state is unknown after startup, becomes unavailable only after three counted
// batches and 60 seconds, and clears on the first Metro row.
func TestMetroPositionFeedAvailability(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	f, direct := metroFeedTestFetcher(t, base)
	if state, at := f.metroPositionFeedState(); state != api.OperatorModelPositionStateUnknown || at != nil {
		t.Fatalf("startup state %s at %v", state, at)
	}
	f.updateMetroPositionFeed(metroFeedBatch("IA9T6", base.Add(-time.Second), base))
	direct(base.Add(30 * time.Second))
	f.updateMetroPositionFeed(metroFeedBatch("IA9T6", base.Add(30*time.Second), base.Add(30*time.Second)))
	if state, _ := f.metroPositionFeedState(); state != api.OperatorModelPositionStateUnknown {
		t.Fatalf("premature unavailable state %s", state)
	}
	direct(base.Add(61 * time.Second))
	f.updateMetroPositionFeed(metroFeedBatch("IA9T6", base.Add(61*time.Second), base.Add(61*time.Second)))
	if state, _ := f.metroPositionFeedState(); state != api.OperatorModelPositionStateUnavailable {
		t.Fatalf("sustained zero-Metro batches did not mark unavailable: %s", state)
	}
	direct(base.Add(80 * time.Second))
	f.updateMetroPositionFeed(metroFeedBatch(metroAgencyID, base.Add(80*time.Second), base.Add(80*time.Second)))
	state, at := f.metroPositionFeedState()
	if state != api.OperatorModelPositionStatePublishing || at == nil || !at.Equal(base.Add(80*time.Second)) {
		t.Fatalf("Metro row did not clear the state: %s at %v", state, at)
	}
}