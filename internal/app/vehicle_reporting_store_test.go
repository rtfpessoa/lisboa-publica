package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func persistedReporting(t *testing.T, s *Store, source string) reportingRecord {
	t.Helper()
	rows, err := s.readReporting(context.Background(), "carris", []string{source})
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := rows[source]
	if !ok {
		t.Fatal("missing durable reporting row")
	}
	return rec
}
func TestVehicleReportingPersistedTransitionsAndRestore(t *testing.T) {
	s := testStore(t)
	s.HistoryInterval = staticRefreshInterval
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	v := reportingFixture(now)
	p, _ := providerByID("carris")
	f.lastPersist[p.ID] = now
	f.saveLive(ctx, p, []api.Vehicle{v}, now)
	rec := persistedReporting(t, s, v.SourceId)
	if rec.Value.State != "reporting" || !rec.Value.Persisted {
		t.Fatalf("first reporting state not immediately committed: %#v", rec)
	}
	repeatedAt := now.Add(5 * time.Second)
	f.saveLive(ctx, p, []api.Vehicle{v}, repeatedAt)
	rec = persistedReporting(t, s, v.SourceId)
	if !rec.Value.LastSeenAt.Equal(now) {
		t.Fatal("unchanged state bypassed batch cadence")
	}
	state, _ := c.state("")
	if state.Live[p.ID].Vehicles[0].Reporting.Persisted {
		t.Fatal("uncommitted membership clock marked durable")
	}
	missingAt := now.Add(10 * time.Second)
	f.saveLive(ctx, p, []api.Vehicle{}, missingAt)
	rec = persistedReporting(t, s, v.SourceId)
	if rec.Value.Reason != "missing_from_snapshot" || !rec.Value.LastObservedAt.Equal(now) || !rec.Value.LastSeenAt.Equal(repeatedAt) {
		t.Fatalf("omission state/clocks: %#v", rec)
	}
	if _, err := s.DB.Exec(ctx, "DELETE FROM cache_parts WHERE operator_id='carris' AND kind='live'"); err != nil {
		t.Fatal(err)
	}
	recovered := &Store{DB: s.DB, HistoryInterval: staticRefreshInterval}
	restored := NewCache()
	if err := recovered.Restore(ctx, restored); err != nil {
		t.Fatal(err)
	}
	if err := recovered.advanceReporting(ctx, restored, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rec = persistedReporting(t, recovered, v.SourceId)
	if rec.Value.State != "unknown" || rec.Value.Reason != "source_unverified" {
		t.Fatalf("position-free restart claimed verified state: %#v", rec)
	}
	if !rec.Value.LastObservedAt.Equal(now) || !rec.Value.LastSeenAt.Equal(repeatedAt) {
		t.Fatal("restart altered original clocks")
	}
	newer := v
	newer.ObservedAt = now.Add(15 * time.Second)
	rf := NewFetcher(recovered, restored, zap.NewNop())
	rf.saveLive(ctx, p, []api.Vehicle{newer}, now.Add(15*time.Second))
	rec = persistedReporting(t, recovered, v.SourceId)
	if rec.Value.State != "reporting" || !rec.Value.LastObservedAt.Equal(newer.ObservedAt) {
		t.Fatal("fresh feed did not recover durable identity")
	}
}
func TestVehicleReportingRejectsDurableRegressionsAfterPositionExpiry(t *testing.T) {
	s := testStore(t)
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	now := time.Now().UTC().Truncate(time.Microsecond)
	v := reportingFixture(now)
	p, _ := providerByID("carris")
	f.saveLive(context.Background(), p, []api.Vehicle{v}, now)
	restarted := &Store{DB: s.DB}
	fresh := NewCache()
	rf := NewFetcher(restarted, fresh, zap.NewNop())
	old := v
	old.ObservedAt = now.Add(-time.Minute)
	rf.saveLive(context.Background(), p, []api.Vehicle{old}, now.Add(time.Second))
	state, _ := fresh.state("")
	if len(state.Live[p.ID].Vehicles) != 0 || len(state.Live[p.ID].Samples) != 0 {
		t.Fatal("durable regression entered publication/history")
	}
	rec := persistedReporting(t, restarted, v.SourceId)
	if !rec.Value.LastObservedAt.Equal(now) || rec.Value.Reason != "missing_from_snapshot" {
		t.Fatalf("regression replaced latest state: %#v", rec)
	}
}
func TestVehicleReportingFailedTransactionAndRetry(t *testing.T) {
	s := testStore(t)
	s.HistoryInterval = staticRefreshInterval
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	now := time.Now().UTC().Truncate(time.Microsecond)
	v := reportingFixture(now)
	p, _ := providerByID("carris")
	ctx := context.Background()
	f.saveLive(ctx, p, []api.Vehicle{v}, now)
	beforeGeneration, _ := s.generation(ctx)
	if _, err := s.DB.Exec(ctx, "ALTER TABLE vehicle_reporting RENAME TO unavailable_reporting"); err != nil {
		t.Fatal(err)
	}
	f.saveLive(ctx, p, []api.Vehicle{}, now.Add(time.Second))
	afterGeneration, _ := s.generation(ctx)
	if afterGeneration != beforeGeneration {
		t.Fatal("failed reporting write partially committed cache/history transaction")
	}
	state, _ := c.state("")
	row := state.Live[p.ID].LastKnown[0]
	if row.Reporting.Persisted || row.Reporting.State != "not_reporting" {
		t.Fatal("failed write did not retain truthful in-memory transition")
	}
	if _, err := s.DB.Exec(ctx, "ALTER TABLE unavailable_reporting RENAME TO vehicle_reporting"); err != nil {
		t.Fatal(err)
	}
	if err := s.advanceReporting(ctx, c, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	rec := persistedReporting(t, s, v.SourceId)
	if rec.Value.Reason != "missing_from_snapshot" {
		t.Fatal("dirty transition not retried")
	}
	state, _ = c.state("")
	if !state.Live[p.ID].LastKnown[0].Reporting.Persisted {
		t.Fatal("retry acknowledgement not published")
	}
}
func TestVehicleReportingStorageGuardAtomicity(t *testing.T) {
	s := testStore(t)
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	now := time.Now().UTC().Truncate(time.Microsecond)
	v := reportingFixture(now)
	p, _ := providerByID("carris")
	ctx := context.Background()
	f.saveLive(ctx, p, []api.Vehicle{v}, now)
	before, _ := s.generation(ctx)
	s.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return operationalDatabaseBytes, nil }}
	f.saveLive(ctx, p, []api.Vehicle{}, now.Add(time.Second))
	after, _ := s.generation(ctx)
	if before != after {
		t.Fatal("budget rejection changed generation")
	}
	if persistedReporting(t, s, v.SourceId).Value.State != "reporting" {
		t.Fatal("budget rejection mutated row")
	}
	state, _ := c.state("")
	if state.Live[p.ID].LastKnown[0].Reporting.Persisted {
		t.Fatal("rejected state exposed as committed")
	}
	s.budget = nil
	if err := s.advanceReporting(ctx, c, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if persistedReporting(t, s, v.SourceId).Value.State != "not_reporting" {
		t.Fatal("budget recovery failed to commit pending state")
	}
}
func TestVehicleReportingSupersededWriteRollsBack(t *testing.T) {
	s := testStore(t)
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	now := time.Now().UTC().Truncate(time.Microsecond)
	v := reportingFixture(now)
	p, _ := providerByID("carris")
	ctx := context.Background()
	f.saveLive(ctx, p, []api.Vehicle{v}, now)
	record := persistedReporting(t, s, v.SourceId)
	record.UpdatedAt = record.UpdatedAt.Add(-time.Second)
	record.Value.State = "unknown"
	payload, _ := json.Marshal(record)
	update := cacheUpdate{Health: []byte(`{"id":"carris","status":"error"}`), Reporting: []reportingWrite{{SourceID: v.SourceId, Record: record, Payload: payload}}}
	before, _ := s.generation(ctx)
	err := s.persistUpdate(ctx, p.ID, update, nil)
	if err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("stale write accepted: %v", err)
	}
	after, _ := s.generation(ctx)
	if after != before {
		t.Fatal("superseded write partially committed")
	}
	if persistedReporting(t, s, v.SourceId).Value.State != "reporting" {
		t.Fatal("stale state replaced newer durable state")
	}
}
func TestVehicleReportingSweepBoundedAndClockDurable(t *testing.T) {
	s := testStore(t)
	c := NewCache()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	op := c.operator("carris")
	op.Status = api.OperatorStatusOk
	v := reportingFixture(now.Add(-181 * time.Second))
	live := &LiveData{Vehicles: []api.Vehicle{v}, Collected: now}
	_, _ = s.reporting.stage(reportingLookup{ctx, s.readReporting}, "carris", live, op, now.Add(-181*time.Second))
	if err := s.Save(ctx, "carris", nil, live, op, nil); err != nil {
		t.Fatal(err)
	}
	c.update("carris", nil, s.reporting.projection("carris", live), op)
	if err := s.advanceReporting(ctx, c, now); err != nil {
		t.Fatal(err)
	}
	rec := persistedReporting(t, s, v.SourceId)
	if rec.Value.Reason != "observation_old" {
		t.Fatalf("clock transition not durable: %#v", rec)
	}
	if err := s.advanceReporting(ctx, c, now.Add(sourceFreshness+time.Second)); err != nil {
		t.Fatal(err)
	}
	rec = persistedReporting(t, s, v.SourceId)
	if rec.Value.Reason != "collection_old" {
		t.Fatalf("collection clock transition: %#v", rec)
	}
	rows, err := s.readReportingBatch(ctx)
	if err != nil || len(rows) > reportingSweepBatch {
		t.Fatalf("unbounded sweep %d %v", len(rows), err)
	}
}

func TestVehicleReportingVisibleClockAdvancesWhenSweepReadFails(t *testing.T) {
	s := testStore(t)
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	v := reportingFixture(now)
	p, _ := providerByID("carris")
	f.saveLive(ctx, p, []api.Vehicle{v}, now)
	if _, err := s.DB.Exec(ctx, "ALTER TABLE vehicle_reporting RENAME TO unavailable_reporting"); err != nil {
		t.Fatal(err)
	}
	if err := s.advanceReporting(ctx, c, now.Add(sourceFreshness+time.Second)); err == nil {
		t.Fatal("missing reporting table unexpectedly succeeded")
	}
	rec := assertReporting(t, s, v.SourceId, "unknown", "collection_old")
	if !rec.Value.LastObservedAt.Equal(now) {
		t.Fatal("failed sweep advanced observation clock")
	}
	state, _ := c.state("")
	if state.Live[p.ID].Vehicles[0].Reporting.Persisted {
		t.Fatal("failed sweep transition marked committed")
	}
	if _, err := s.DB.Exec(ctx, "ALTER TABLE unavailable_reporting RENAME TO vehicle_reporting"); err != nil {
		t.Fatal(err)
	}
	if err := s.advanceReporting(ctx, c, now.Add(sourceFreshness+2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if persistedReporting(t, s, v.SourceId).Value.Reason != "collection_old" {
		t.Fatal("clock state not retried after read/write recovery")
	}
}
