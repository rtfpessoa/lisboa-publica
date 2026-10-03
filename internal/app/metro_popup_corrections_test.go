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
	call := &track.Train.Calls[0]
	if call.Arrival.Inferred == nil {
		t.Fatal("inferred arrival missing")
	}
	if call.LastOfficialEstimate == nil || !call.LastOfficialEstimate.At.Equal(clock.Add(2*time.Minute)) {
		t.Fatalf("inference did not retain the previous official estimate: %+v", call.LastOfficialEstimate)
	}

	// Prediction expiry clears the current prediction but never the retained estimate.
	expired := clock.Add(-time.Hour)
	call.Arrival = api.CallTime{Kind: "prediction", Prediction: &api.CallTimeEvidence{At: expired, ValidUntil: ptr(clock.Add(-30 * time.Minute))}}
	expireMetroCall(call, "supported", now)
	if call.Arrival.Kind != "unavailable" {
		t.Fatal("expired prediction was kept as current")
	}
	if call.LastOfficialEstimate == nil {
		t.Fatal("expiry cleared the retained last official estimate")
	}

	// Suspension is historical evidence and keeps its own clock.
	suspendMetroTrack(track, "test suspension")
	if track.Train.Calls[0].LastOfficialEstimate == nil {
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

// The local-context call site must keep the same order; the removed post-canonical id
// sort would restore id order for these calls.
func TestMetroLocalContextsSortedOrder(t *testing.T) {
	contexts := metroLocalContexts{contexts: map[string]*api.MetroForecastContext{
		"metro:1_0|33|001A": {Reference: "001A", RouteId: "metro:1_0", Status: "admissible", Calls: []api.StopCall{
			{Id: "a", StopName: "Roma", Arrival: api.CallTime{Kind: "unavailable"}},
			{Id: "b", StopName: "Alameda", Arrival: api.CallTime{Kind: "unavailable"}},
		}},
	}}
	out := contexts.sorted()
	if len(out) != 1 || len(out[0].Calls) != 2 {
		t.Fatalf("unexpected local context shape: %+v", out)
	}
	if out[0].Calls[0].StopName != "Alameda" || out[0].Calls[1].StopName != "Roma" {
		t.Fatalf("local context order %s, %s; the id sort would have restored Roma first", out[0].Calls[0].StopName, out[0].Calls[1].StopName)
	}
}

// The retained real capture must produce contexts whose calls follow the published path,
// and two independent reads of one publication must encode to the same frame revision.
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

	// Two independent reads of the same publication must encode identically, bypassing
	// the short-lived forecast cache so the ordering is re-derived.
	first, plan, firstContexts, _ := runtime.view(now)
	if first == nil || plan == nil {
		t.Fatal("runtime view missing for revision check")
	}
	runtime.forecastCache, runtime.forecastCacheUntil = nil, time.Time{}
	second, _, secondContexts, _ := runtime.view(now)
	if second == nil {
		t.Fatal("second runtime view missing for revision check")
	}
	firstFrame := api.MetroLiveFrame{PublishedAt: now, PlanId: plan.PlanID, Trains: first.Trains, Vehicles: []api.Vehicle{}, Directions: []api.BoardDirection{}, UnassociatedForecasts: []api.StopCall{}, ForecastContexts: &firstContexts, HistoryStatus: "unavailable"}
	secondFrame := api.MetroLiveFrame{PublishedAt: now, PlanId: plan.PlanID, Trains: second.Trains, Vehicles: []api.Vehicle{}, Directions: []api.BoardDirection{}, UnassociatedForecasts: []api.StopCall{}, ForecastContexts: &secondContexts, HistoryStatus: "unavailable"}
	encodedFirst, _, err := encodeMetroFrame(firstFrame, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	encodedSecond, _, err := encodeMetroFrame(secondFrame, 1<<20)
	if err != nil || encodedFirst.Revision != encodedSecond.Revision {
		t.Fatalf("frame revision is not stable: %q vs %q (%v)", encodedFirst.Revision, encodedSecond.Revision, err)
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

// Three counted batches inside 60 seconds must not enter unavailable: the window is
// measured from the first counted batch.
func TestMetroPositionFeedWaitsForFullWindow(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	f, direct := metroFeedTestFetcher(t, base)
	for n := 0; n < 3; n++ {
		at := base.Add(time.Duration(n) * time.Second)
		direct(at)
		f.updateMetroPositionFeed(metroFeedBatch("IA9T6", at.Add(-time.Second), at))
	}
	if state, _ := f.metroPositionFeedState(); state == api.OperatorModelPositionStateUnavailable {
		t.Fatal("three fast batches entered unavailable before the 60 s window")
	}
	at := base.Add(61 * time.Second)
	direct(at)
	f.updateMetroPositionFeed(metroFeedBatch("IA9T6", at.Add(-time.Second), at))
	if state, _ := f.metroPositionFeedState(); state != api.OperatorModelPositionStateUnavailable {
		t.Fatal("a counted batch after the window did not mark unavailable")
	}
}

// A Metro row clears the state and restarts the absence window.
func TestMetroPositionFeedWindowResetsOnMetroRow(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second)
	f, direct := metroFeedTestFetcher(t, base)
	direct(base)
	f.updateMetroPositionFeed(metroFeedBatch("IA9T6", base.Add(-time.Second), base))
	rowAt := base.Add(61 * time.Second)
	direct(rowAt)
	f.updateMetroPositionFeed(metroFeedBatch(metroAgencyID, rowAt, rowAt))
	if state, _ := f.metroPositionFeedState(); state != api.OperatorModelPositionStatePublishing {
		t.Fatalf("Metro row did not set publishing: %s", state)
	}
	for n := 0; n < 3; n++ {
		at := rowAt.Add(time.Duration(n+1) * time.Second)
		direct(at)
		f.updateMetroPositionFeed(metroFeedBatch("IA9T6", at.Add(-time.Second), at))
	}
	if state, _ := f.metroPositionFeedState(); state == api.OperatorModelPositionStateUnavailable {
		t.Fatal("the absence window did not restart after a Metro row")
	}
	at := rowAt.Add(61 * time.Second)
	direct(at)
	f.updateMetroPositionFeed(metroFeedBatch("IA9T6", at.Add(-time.Second), at))
	if state, _ := f.metroPositionFeedState(); state != api.OperatorModelPositionStateUnavailable {
		t.Fatal("the restarted window did not mark unavailable")
	}
}

// Station forecast rows must be ordered by their expected time within a direction.
func TestMetroStationForecastsOrderedByTime(t *testing.T) {
	clock := func(sec int64) *time.Time { return ptr(time.Unix(sec, 0).UTC()) }
	call := func(id, direction, reference string, sec int64) api.StopCall {
		return api.StopCall{Id: id, LineKey: "metro:1_0", DirectionKey: ptr(direction), ServiceLabel: ptr(reference),
			Arrival: api.CallTime{Kind: "prediction", At: clock(sec), Prediction: &api.CallTimeEvidence{At: *clock(sec)}}}
	}
	calls := []api.StopCall{
		call("c", "33", "003A", 300),
		call("b", "33", "002A", 200),
		call("a", "33", "001A", 100),
		call("d", "42", "004A", 50),
		{Id: "e", LineKey: "metro:1_0", DirectionKey: ptr("33"), ServiceLabel: ptr("000A"), Arrival: missingCallTime("Sem previsão atual")},
	}
	sortMetroStationForecasts(calls)
	want := []string{"a", "b", "c", "e", "d"}
	got := []string{calls[0].Id, calls[1].Id, calls[2].Id, calls[3].Id, calls[4].Id}
	for n := range want {
		if got[n] != want[n] {
			t.Fatalf("station forecast order %v, want %v", got, want)
		}
	}
}
