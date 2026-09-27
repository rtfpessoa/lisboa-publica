package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func cpTestData() (*StaticData, time.Time) {
	day := time.Date(2026, 9, 26, 12, 0, 0, 0, lisbon)
	now := serviceStart(day).Add(10 * time.Hour)
	t := ScheduledTrip{ID: "A_20251214", Route: "1", Service: "daily", Headsign: "Lisboa Santa Apolonia", Label: "123", Times: []StopTime{{Stop: "S", Sequence: 1, Arrival: 36600, Departure: 36600}, {Stop: "T", Sequence: 2, Arrival: 37800, Departure: 37800}}, CPTiming: &cpTripTiming{FirstSequence: 0, LastSequence: 3, FirstArrival: 28800, FirstDeparture: 28800, LastArrival: 39600}}
	d := &StaticData{PlanID: "plan", ValidFrom: "20260101", ValidUntil: "20261231", CPPredictionMetadata: true, Schedule: &Schedule{Trips: []ScheduledTrip{t}, Calendars: map[string]Calendar{"daily": {Start: "20260101", End: "20261231", Days: [7]bool{true, true, true, true, true, true, true}}}, Exceptions: map[string]map[string]int{}, Parents: map[string]string{}}, Stops: []api.Stop{{Id: "cp:S", SourceId: "S", Name: "Lisboa Oriente", OperatorId: "cp"}, {Id: "cp:T", SourceId: "T", Name: "Lisboa Santa Apolonia", OperatorId: "cp"}}, Routes: []api.RouteDetail{{Id: "cp:1", ShortName: "R", LongName: "Regional"}}}
	return d, now
}

func cpTestUpdate(now time.Time, seconds int, sequence bool) cpUpdate {
	u := cpUpdate{}
	u.Trip.ID = "[plan][N18KL]A_20251214"
	u.Timestamp = now.Add(-time.Duration(seconds) * time.Second).Unix()
	s := cpStopUpdate{ID: "hub-S"}
	s.Arrival.Delay = ptr(60)
	if sequence {
		s.Sequence = ptr(1)
		s.Arrival.Time = ptr(now.Add(11 * time.Minute).Unix())
	}
	u.Stops = []cpStopUpdate{s}
	return u
}

func cpTestFeed(now time.Time, updates ...cpUpdate) *cpFeed {
	f := &cpFeed{Updates: updates}
	f.Header.Version = "2.0"
	f.Header.Incrementality = "FULL_DATASET"
	f.Header.Timestamp = now.Unix()
	return f
}

func TestCPDelayOnlyUsesExactMappingCalendarAndOriginalClock(t *testing.T) {
	d, now := cpTestData()
	old := cpTestUpdate(now, 200, true)
	fresh := cpTestUpdate(now, 2, false)
	result, err := normalizeCP(context.Background(), cpTestFeed(now, old, fresh), d, now)
	if err != nil || len(result.Rows) != 1 {
		t.Fatalf("safe prediction missing: %v %+v", err, result)
	}
	r := result.Rows[0]
	if r.ServiceDate == nil || r.ServiceDate.Format("2006-01-02") != "2026-09-26" || r.DateBasis == nil || *r.DateBasis != api.CPPredictionDateBasisMatchedSchedule || r.StopId != "cp:S" || r.StopSequence != 1 || r.SourceTripId != "cp:A_20251214" {
		t.Fatalf("incorrect join: %+v", r)
	}
	if !r.ValidUntil.Equal(now.Add(88*time.Second)) || !r.ExpectedAt.Equal(now.Add(11*time.Minute)) || r.DelaySeconds == nil || *r.DelaySeconds != 60 {
		t.Fatal("clock/deviation lost")
	}
	if result.Availability.Status != api.CPPredictionAvailabilityStatusPartial {
		t.Fatal("excluded old rows hidden")
	}
}

func TestCPUnprovableAndConflictingAssociationsFailClosed(t *testing.T) {
	for _, name := range []string{"wrong_plan", "wrong_agency", "missing_mapping", "repeated_stop", "conflicting_crosswalk", "inactive_day", "frequency", "unknown_metadata", "explicit_frequency", "invalid_date", "start_time", "contradictory_events", "overlap", "future_clock", "expired_clock"} {
		t.Run(name, func(t *testing.T) {
			d, now := cpTestData()
			old := cpTestUpdate(now, 200, true)
			fresh := cpTestUpdate(now, 2, false)
			feed := cpTestFeed(now, old, fresh)
			switch name {
			case "wrong_plan":
				fresh.Trip.ID = "[other][N18KL]A_20251214"
			case "wrong_agency":
				fresh.Trip.ID = "[plan][other]A_20251214"
			case "missing_mapping":
				fresh.Stops[0].ID = "unknown"
			case "repeated_stop":
				d.Schedule.Trips[0].Times = append(d.Schedule.Trips[0].Times, StopTime{Stop: "S", Sequence: 3, Arrival: 38000})
			case "conflicting_crosswalk":
				conflict := old
				conflict.Stops = []cpStopUpdate{{ID: "hub-S", Sequence: ptr(2)}}
				feed.Updates = append(feed.Updates, conflict)
			case "inactive_day":
				d.Schedule.Exceptions["daily"] = map[string]int{"20260926": 2}
			case "frequency":
				d.CPHasFrequencies = true
			case "unknown_metadata":
				d.CPPredictionMetadata = false
			case "explicit_frequency":
				d.CPHasFrequencies = true
				fresh.Trip.Date = "20260926"
			case "invalid_date":
				fresh.Trip.Date = "20260230"
			case "start_time":
				fresh.Trip.StartTime = "09:00:00"
			case "contradictory_events":
				s := fresh.Stops[0]
				s.Sequence = ptr(2)
				s.ID = "hub-T"
				s.Arrival.Time = ptr(now.Add(25 * time.Minute).Unix())
				fresh.Stops = append(fresh.Stops, s)
			case "overlap":
				d.Schedule.Trips[0].CPTiming.LastArrival = 72000
			case "future_clock":
				fresh.Timestamp = now.Add(31 * time.Second).Unix()
			case "expired_clock":
				fresh.Timestamp = now.Add(-90 * time.Second).Unix()
			}
			feed.Updates[1] = fresh
			result, err := normalizeCP(context.Background(), feed, d, now)
			if err != nil || len(result.Rows) != 0 {
				t.Fatalf("unproven prediction admitted: %v %+v", err, result)
			}
		})
	}
}

func TestCPDuplicateOrderingCancellationAndSignedDeviation(t *testing.T) {
	d, now := cpTestData()
	base := cpTestUpdate(now, 10, true)
	newer := cpTestUpdate(now, 2, true)
	newer.Stops[0].Arrival.Delay = ptr(-30)
	newer.Stops[0].Arrival.Time = ptr(now.Add(570 * time.Second).Unix())
	for _, rows := range [][]cpUpdate{{base, newer}, {newer, base}, {base, newer, newer}} {
		result, err := normalizeCP(context.Background(), cpTestFeed(now, rows...), d, now)
		if err != nil || len(result.Rows) != 1 || *result.Rows[0].DelaySeconds != -30 {
			t.Fatal("ordering/negative deviation", err, result)
		}
	}
	conflict := newer
	conflict.Stops = append([]cpStopUpdate(nil), newer.Stops...)
	conflict.Stops[0].Arrival.Delay = ptr(0)
	conflict.Stops[0].Arrival.Time = ptr(now.Add(600 * time.Second).Unix())
	result, err := normalizeCP(context.Background(), cpTestFeed(now, newer, conflict), d, now)
	if err != nil || len(result.Rows) != 0 {
		t.Fatal("equal-time conflict admitted")
	}
	for _, relationship := range []string{"CANCELED", "SKIPPED", "NO_DATA"} {
		block := newer
		block.Stops = append([]cpStopUpdate(nil), newer.Stops...)
		if relationship == "CANCELED" {
			block.Trip.Relationship = relationship
		} else {
			block.Stops[0].Relationship = relationship
		}
		result, err = normalizeCP(context.Background(), cpTestFeed(now, newer, block), d, now)
		if err != nil || len(result.Rows) != 0 {
			t.Fatal("cancelled/skipped call resurrected", relationship)
		}
	}
}

func TestCPOfficialSanitizedMixedFeedAdmitsOrienteWithoutCredentials(t *testing.T) {
	blob, err := os.ReadFile("testdata/cp/public-trip-updates.json")
	if err != nil {
		t.Fatal(err)
	}
	feed, err := decodeCPFeed(blob)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile("testdata/cp/schedule.zip")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := providerByID("cp")
	now := time.Unix(feed.Header.Timestamp, 0).UTC()
	data, err := readGTFS(archive, p, "76XA2", "20260616", "20261231", hubBase+"/plans", now)
	if err != nil {
		t.Fatal(err)
	}
	result, err := normalizeCP(context.Background(), feed, data, now)
	if err != nil {
		t.Fatal(err)
	}
	oriente := 0
	for _, row := range result.Rows {
		if row.StopId == "cp:94_31039" && row.ServiceDate != nil {
			oriente++
		}
	}
	if oriente < 1 {
		t.Fatalf("no safely admitted Oriente arrival: %+v", result.Availability)
	}
	t.Logf("source_entities=%d admitted_calls=%d oriente_calls=%d", len(feed.Updates), len(result.Rows), oriente)
}

func TestCPFeedSemanticsAndResourceBounds(t *testing.T) {
	d, now := cpTestData()
	feed := cpTestFeed(now, cpTestUpdate(now, 2, true))
	valid := map[string]any{"data": map[string]any{"header": map[string]any{"gtfs_realtime_version": "2.0", "incrementality": "FULL_DATASET", "timestamp": now.Unix()}, "entity": []any{map[string]any{"trip_update": feed.Updates[0]}}}, "error": nil}
	blob, _ := json.Marshal(valid)
	if _, err := decodeCPFeed(blob); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`{"data":{"header":{"gtfs_realtime_version":"2.0","incrementality":"DIFFERENTIAL","timestamp":1},"entity":[]}}`), []byte(`{"data":{"header":{"gtfs_realtime_version":"2.0","incrementality":"FULL_DATASET","timestamp":1},"entity":[{"is_deleted":true}]}}`), append(blob, []byte(`{}`)...)} {
		if _, err := decodeCPFeed(bad); err == nil {
			t.Fatal("unsupported/malformed feed admitted")
		}
	}
	rows := make([]api.CPPrediction, cpMaxRows+1)
	if err := finishCP(&CPData{Rows: rows}); err == nil {
		t.Fatal("row overflow")
	}
	for n := 0; n < cpMaxServices+1; n++ {
		trip := d.Schedule.Trips[0]
		trip.ID = stringID(uint64(n))
		d.Schedule.Trips = append(d.Schedule.Trips, trip)
		u := cpTestUpdate(now, 2, true)
		u.Trip.ID = "[plan][N18KL]" + trip.ID
		feed.Updates = append(feed.Updates, u)
	}
	if _, err := normalizeCP(context.Background(), feed, d, now); err == nil {
		t.Fatal("service overflow")
	}
}

func TestCPContradictoryChronologyAndDescriptorClock(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		d, now := cpTestData()
		u := cpTestUpdate(now, 2, true)
		u.Stops[0].Arrival.Time = nil
		u.Stops[0].Arrival.Delay = ptr(600)
		last := cpStopUpdate{ID: "hub-T", Sequence: ptr(2)}
		last.Arrival.Delay = ptr(-1200)
		u.Stops = append(u.Stops, last)
		if reverse {
			u.Stops[0], u.Stops[1] = u.Stops[1], u.Stops[0]
		}
		got, err := normalizeCP(context.Background(), cpTestFeed(now, u), d, now)
		if err != nil || len(got.Rows) != 0 {
			t.Fatalf("contradictory calls admitted reverse=%v: %v %+v", reverse, err, got)
		}
	}
	for _, frequencies := range []bool{false, true} {
		d, now := cpTestData()
		d.CPPredictionMetadata = false
		d.CPHasFrequencies = frequencies
		u := cpTestUpdate(now, 2, true)
		u.Trip.StartTime = "99:99:99"
		got, err := normalizeCP(context.Background(), cpTestFeed(now, u), d, now)
		if err != nil || len(got.Rows) != 0 {
			t.Fatalf("malformed start time admitted: %v %+v", err, got)
		}
	}
}

func TestCPDecoderRejectsAmplificationAndCancellation(t *testing.T) {
	for _, bad := range []string{
		`{"data":{"x":` + strings.Repeat("[", 20) + `0` + strings.Repeat("]", 20) + `}}`,
		`{"data":{"x":"` + strings.Repeat("x", 4097) + `"}}`,
		`{"data":{"entity":[{"x":[` + strings.Repeat("0,", 40000) + `0]}]}}`,
	} {
		if _, err := decodeCPFeed([]byte(bad)); err == nil {
			t.Fatal("amplification admitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	blob, err := os.ReadFile("testdata/cp/public-trip-updates.json")
	if err != nil {
		t.Fatal(err)
	}
	trailing := append(append([]byte{}, blob...), []byte("["+strings.Repeat("0,", 100000)+"0]")...)
	if _, err = decodeCPFeed(trailing); err == nil {
		t.Fatal("large trailing array admitted")
	}
	if _, err = decodeCPFeedContext(ctx, blob); err == nil {
		t.Fatal("cancelled decode admitted")
	}
}

// Cancel a real context while normalization is running, without timing-sensitive sleeps.
type cpCancelChecks struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *cpCancelChecks) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestCPNormalizationCancellation(t *testing.T) {
	for _, checks := range []int{1, 12, 1200, 2500} {
		t.Run(fmt.Sprint(checks), func(t *testing.T) {
			data, now := cpTestData()
			update := cpTestUpdate(now, 2, true)
			updates := make([]cpUpdate, 512)
			for n := range updates {
				updates[n] = update
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			controlled := &cpCancelChecks{Context: ctx, cancel: cancel, remaining: checks}
			started := time.Now()
			_, err := normalizeCP(controlled, cpTestFeed(now, updates...), data, now)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("normalization ignored cancellation: %v", err)
			}
			if time.Since(started) > time.Second {
				t.Fatal("cancellation did not return promptly")
			}
		})
	}
}

func TestCPCanceledNormalizationKeepsFallbackClocks(t *testing.T) {
	data, now := cpTestData()
	previous, err := normalizeCP(context.Background(), cpTestFeed(now, cpTestUpdate(now, 2, true)), data, now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = normalizeCP(ctx, cpTestFeed(now, cpTestUpdate(now, 1, true)), data, now.Add(time.Second))
	if err == nil {
		t.Fatal("cancellation ignored")
	}
	fallback := failedCP(previous, data.PlanID, now.Add(time.Second))
	if !reflect.DeepEqual(previous.Rows, fallback.Rows) {
		t.Fatal("failed normalization renewed source clocks")
	}
}
