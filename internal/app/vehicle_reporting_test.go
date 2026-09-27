package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func reportingFixture(now time.Time) api.Vehicle {
	return api.Vehicle{Id: "carris:reporting-test", SourceId: "reporting-test", OperatorId: "carris", ObservedAt: now, CollectedAt: now, PositionKind: "reported", SourceUrl: "https://example.invalid/positions", Lat: 38.72, Lon: -9.15}
}
func assertReporting(t *testing.T, s *Store, id, state, reason string) reportingRecord {
	t.Helper()
	s.reporting.mu.Lock()
	defer s.reporting.mu.Unlock()
	e := s.reporting.Entries[factKey("carris", id)]
	if e == nil {
		t.Fatal("missing reporting entry")
	}
	if string(e.Record.Value.State) != state || string(e.Record.Value.Reason) != reason {
		t.Fatalf("got %s/%s, want %s/%s", e.Record.Value.State, e.Record.Value.Reason, state, reason)
	}
	return e.Record
}
func TestVehicleReportingMembershipAndClocks(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := &Store{}
	op := api.Operator{Status: "ok"}
	v := reportingFixture(now)
	live := &LiveData{Vehicles: []api.Vehicle{v}, Collected: now}
	changed, err := s.reporting.stage(reportingLookup{ctx, s.readReporting}, "carris", live, op, now)
	if err != nil || !changed {
		t.Fatalf("first state: %v %v", changed, err)
	}
	first := assertReporting(t, s, v.SourceId, "reporting", "current")
	repeat := *live
	repeat.Collected = now.Add(5 * time.Second)
	if _, err = s.reporting.stage(reportingLookup{ctx, s.readReporting}, "carris", &repeat, op, repeat.Collected); err != nil {
		t.Fatal(err)
	}
	rec := assertReporting(t, s, v.SourceId, "reporting", "current")
	if !rec.Value.LastObservedAt.Equal(now) || !rec.Value.LastSeenAt.Equal(repeat.Collected) || !rec.Value.StateChangedAt.Equal(first.Value.StateChangedAt) {
		t.Fatalf("repeated observation changed original clocks: %#v", rec)
	}
	missing := &LiveData{Vehicles: []api.Vehicle{}, Collected: now.Add(10 * time.Second)}
	if _, err = s.reporting.stage(reportingLookup{ctx, s.readReporting}, "carris", missing, op, missing.Collected); err != nil {
		t.Fatal(err)
	}
	rec = assertReporting(t, s, v.SourceId, "not_reporting", "missing_from_snapshot")
	if !rec.Value.LastObservedAt.Equal(now) || !rec.Value.LastSeenAt.Equal(repeat.Collected) {
		t.Fatal("omission invented observation or membership")
	}
	recovery := *live
	recovery.Collected = now.Add(15 * time.Second)
	if _, err = s.reporting.stage(reportingLookup{ctx, s.readReporting}, "carris", &recovery, op, recovery.Collected); err != nil {
		t.Fatal(err)
	}
	assertReporting(t, s, v.SourceId, "reporting", "current")
	old := *live
	old.Collected = now.Add(181 * time.Second)
	if _, err = s.reporting.stage(reportingLookup{ctx, s.readReporting}, "carris", &old, op, old.Collected); err != nil {
		t.Fatal(err)
	}
	assertReporting(t, s, v.SourceId, "not_reporting", "observation_old")
}
func TestVehicleReportingUnknownReasonsAndPriority(t *testing.T) {
	now := time.Now().UTC()
	v := reportingFixture(now)
	record := reportingRecord{CollectionAt: now, Present: true, Value: api.ReportingState{LastObservedAt: ptr(now)}}
	cases := []struct {
		name       string
		op         api.Operator
		unverified bool
		at         time.Time
		reason     string
	}{
		{"current", api.Operator{Status: "ok"}, false, now, "current"},
		{"collection expired", api.Operator{Status: "ok"}, false, now.Add(sourceFreshness + time.Nanosecond), "collection_old"},
		{"outage", api.Operator{Status: "error", Error: ptr("failed")}, false, now, "source_error"},
		{"restart", api.Operator{Status: "error", Error: ptr("old failed")}, true, now, "source_unverified"},
	}
	_ = v
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, reason := reportingClassification(record, tc.op, tc.unverified, tc.at)
			if string(reason) != tc.reason {
				t.Fatalf("got %s", reason)
			}
		})
	}
}
func TestVehicleReportingDurabilityAcknowledgesExactVersion(t *testing.T) {
	s := &Store{}
	now := time.Now().UTC()
	v := reportingFixture(now)
	op := api.Operator{Status: "ok"}
	live := &LiveData{Vehicles: []api.Vehicle{v}, Collected: now}
	_, _ = s.reporting.stage(reportingLookup{context.Background(), s.readReporting}, "carris", live, op, now)
	old, _ := s.reporting.pending("carris")
	missing := &LiveData{Vehicles: []api.Vehicle{}, Collected: now.Add(time.Second)}
	_, _ = s.reporting.stage(reportingLookup{context.Background(), s.readReporting}, "carris", missing, op, missing.Collected)
	s.reporting.acknowledge("carris", old)
	if !s.reporting.immediate("carris") {
		t.Fatal("old acknowledgement dropped new transition")
	}
	latest, _ := s.reporting.pending("carris")
	s.reporting.acknowledge("carris", latest)
	if s.reporting.immediate("carris") {
		t.Fatal("committed transition still pending")
	}
	projection := s.reporting.projection("carris", live)
	if !projection.Vehicles[0].Reporting.Persisted {
		t.Fatal("committed reporting not exposed as durable")
	}
	if live.Vehicles[0].Reporting != nil {
		t.Fatal("projection mutated old revision")
	}
}
func TestVehicleReportingClockWorkerWithoutIngestion(t *testing.T) {
	s := &Store{}
	c := NewCache()
	now := time.Now().UTC().Add(-181 * time.Second)
	v := reportingFixture(now)
	live := &LiveData{Vehicles: []api.Vehicle{v}, Collected: now.Add(180 * time.Second)}
	op := c.operator("carris")
	op.Status = "ok"
	_, _ = s.reporting.stage(reportingLookup{context.Background(), s.readReporting}, "carris", live, op, now)
	c.update("carris", nil, live, op)
	if err := s.advanceReporting(context.Background(), c, time.Now().UTC()); err == nil {
		t.Fatal("missing DB should fail persistence")
	}
	assertReporting(t, s, v.SourceId, "not_reporting", "observation_old")
	state, _ := c.state("")
	if state.Live["carris"].Vehicles[0].Reporting.Persisted {
		t.Fatal("failed worker write marked durable")
	}
}
func TestVehicleReportingImmediateOnProviderFailure(t *testing.T) {
	s := &Store{HistoryInterval: staticRefreshInterval}
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	now := time.Now().UTC()
	v := reportingFixture(now)
	p, _ := providerByID("carris")
	f.lastPersist[p.ID] = now
	f.saveLive(context.Background(), p, []api.Vehicle{v}, now)
	f.markError(context.Background(), p, false, errors.New("outage"))
	assertReporting(t, s, v.SourceId, "unknown", "source_error")
	state, _ := c.state("")
	if state.Live[p.ID].Vehicles[0].Reporting.Persisted {
		t.Fatal("outage state marked durable without DB")
	}
}

func TestVehicleReportingRegistryCapacityProtectsPendingWrites(t *testing.T) {
	r := &reportingRegistry{Entries: map[string]*reportingEntry{}}
	for i := 0; i < maxReportingIdentities; i++ {
		r.Entries[stringID(uint64(i))] = &reportingEntry{Loaded: true, Dirty: true}
	}
	if _, err := r.entry("carris", "overflow"); err == nil {
		t.Fatal("unbounded pending inventory accepted")
	}
	if len(r.Entries) != maxReportingIdentities {
		t.Fatal("capacity refusal changed pending inventory")
	}
	r.Entries["0"].Dirty = false
	if _, err := r.entry("carris", "replacement"); err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != maxReportingIdentities || r.Entries["0"] != nil {
		t.Fatal("clean identity eviction did not preserve bound")
	}
}

func TestVehicleReportingLegacyCachePreservesOriginalObservation(t *testing.T) {
	s := &Store{}
	now := time.Now().UTC()
	v := reportingFixture(now.Add(-time.Minute))
	live := &LiveData{Vehicles: []api.Vehicle{v}, Collected: now.Add(-30 * time.Second), Unverified: true}
	op := api.Operator{Status: "ok"}
	if _, err := s.reporting.stage(reportingLookup{context.Background(), s.readReporting}, "carris", live, op, now); err != nil {
		t.Fatal(err)
	}
	rec := assertReporting(t, s, v.SourceId, "unknown", "source_unverified")
	if rec.Value.LastObservedAt == nil || !rec.Value.LastObservedAt.Equal(v.ObservedAt) || rec.Value.LastSeenAt != nil {
		t.Fatal("legacy restore invented membership or lost original observation")
	}
}

func TestVehicleReportingUnloadedIdentitiesDoNotPinCapacity(t *testing.T) {
	r := &reportingRegistry{Entries: map[string]*reportingEntry{}}
	for i := 0; i < maxReportingIdentities; i++ {
		r.Entries[stringID(uint64(i))] = &reportingEntry{Used: uint64(i)}
	}
	if _, err := r.entry("carris", "after-outage"); err != nil {
		t.Fatalf("failed lazy reads pinned cache capacity: %v", err)
	}
	if len(r.Entries) != maxReportingIdentities || r.Entries["0"] != nil {
		t.Fatal("unloaded entry eviction lost registry bound")
	}
}
