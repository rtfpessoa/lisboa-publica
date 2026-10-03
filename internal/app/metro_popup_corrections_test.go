package app

import (
	"errors"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

// A passed visit keeps the last official estimate after inference, and the estimate
// is withdrawn together with a retracted inferred arrival.
func TestMetroLastOfficialEstimateRetainedAndWithdrawn(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	clock := now.Add(-time.Minute)
	track := &metroTrack{
		Codes:  []string{"RM"},
		Points: map[string]metroPoint{},
		Train:  api.MetroTrain{Association: "supported", ValidUntil: now.Add(time.Hour), Calls: []api.StopCall{{Id: "v0", StopId: "metro:gtfs-rm", Arrival: missingCallTime("Sem previsão atual"), Departure: missingCallTime("Sem dados de partida")}}},
	}
	runtime := newMetroRuntime()
	projection := metroPointProjection{runtime: runtime, track: track, now: now}

	wait := 120
	if !projection.apply(0, metroPoint{Stop: "RM", Platform: "1", Clock: clock, Seconds: &wait}) {
		t.Fatal("positive wait projection failed")
	}
	estimate := track.Train.Calls[0].LastOfficialEstimate
	if estimate == nil || !estimate.At.Equal(clock.Add(2*time.Minute)) {
		t.Fatalf("last official estimate not retained: %+v", estimate)
	}

	zero := 0
	track.Points["RM"] = metroPoint{Stop: "RM", Platform: "1", Clock: clock, Seconds: &wait}
	if !projection.apply(0, metroPoint{Stop: "RM", Platform: "1", Clock: clock.Add(5 * time.Second), Seconds: &zero}) {
		t.Fatal("zero wait projection failed")
	}
	call := track.Train.Calls[0]
	if call.Arrival.Inferred == nil {
		t.Fatal("inferred arrival missing")
	}
	if call.LastOfficialEstimate == nil || !call.LastOfficialEstimate.At.Equal(clock.Add(2*time.Minute)) {
		t.Fatalf("last official estimate lost at inference: %+v", call.LastOfficialEstimate)
	}

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

// Local-only calls carry no path sequence; the same total order keeps them deterministic.
func TestMetroLocalForecastCallsOrderedDeterministically(t *testing.T) {
	calls := []api.StopCall{
		{Id: "b", StopName: "Roma", Arrival: api.CallTime{Kind: "unavailable"}},
		{Id: "a", StopName: "Alameda", Arrival: api.CallTime{Kind: "unavailable"}},
	}
	out := canonicalMetroForecastCalls(calls)
	if out[0].StopName != "Alameda" || out[1].StopName != "Roma" {
		t.Fatalf("local order %s, %s", out[0].StopName, out[1].StopName)
	}
}

// The Metro position feed is unknown after startup, unavailable after sustained healthy
// batches without Metro rows, paused by errored batches and cleared by a Metro row.
func TestMetroPositionFeedAvailability(t *testing.T) {
	fetcher := &Fetcher{}
	now := time.Now().UTC().Truncate(time.Second)
	if state, at := fetcher.metroPositionFeedState(); state != api.OperatorModelPositionStateUnknown || at != nil {
		t.Fatalf("startup state %s at %v", state, at)
	}
	batch := func(agency string) hubObservationBatch {
		return hubObservationBatch{positions: []hubPosition{{Agency: agency, At: now.UnixMilli()}}, collected: now}
	}
	fetcher.updateMetroPositionFeed(batch("IA9T6"))
	now = now.Add(30 * time.Second)
	fetcher.updateMetroPositionFeed(batch("IA9T6"))
	if state, _ := fetcher.metroPositionFeedState(); state != api.OperatorModelPositionStateUnknown {
		t.Fatalf("premature unavailable state %s", state)
	}
	now = now.Add(30 * time.Second)
	fetcher.updateMetroPositionFeed(batch("IA9T6"))
	if state, _ := fetcher.metroPositionFeedState(); state != api.OperatorModelPositionStateUnavailable {
		t.Fatalf("sustained zero-Metro batches did not mark unavailable: %s", state)
	}
	fetcher.updateMetroPositionFeed(hubObservationBatch{collected: now, err: errors.New("hub failure")})
	if state, _ := fetcher.metroPositionFeedState(); state != api.OperatorModelPositionStateUnavailable {
		t.Fatal("errored batch changed the state")
	}
	fetcher.updateMetroPositionFeed(batch(metroAgencyID))
	state, at := fetcher.metroPositionFeedState()
	if state != api.OperatorModelPositionStatePublishing || at == nil || !at.Equal(now) {
		t.Fatalf("Metro row did not clear the state: %s at %v", state, at)
	}
}
