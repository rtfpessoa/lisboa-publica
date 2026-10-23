package app

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func verifiedPressureStore(t *testing.T, credit int64) *Store {
	t.Helper()
	s := admissionStore(t, 4_150_000_000, 0, credit)
	assertEmptyPressureStatus(t, s, "collecting")
	return s
}

func TestReadinessMaintainsStagingDuringCleanup(t *testing.T) {
	s := verifiedPressureStore(t, 5656)
	ctx := context.Background()
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `LOCK TABLE sessions IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.pruneWithVacuumContext(ctx, ctx); done <- err }()
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE 'DELETE FROM sessions%' AND wait_event_type='Lock')`)
	base := time.Now().UTC().Add(-20 * time.Minute).Truncate(staticRefreshInterval)
	first := historyVehicle("during-maintenance", base.Add(10*time.Second), 20)
	staged := make(chan struct{})
	go func() {
		s.stageLive(&LiveData{Collected: base.Add(15 * time.Second), Vehicles: []api.Vehicle{first}}, map[string]*float64{first.Id: ptr(.1)})
		close(staged)
	}()
	select {
	case <-staged:
	case <-time.After(5 * time.Second):
		t.Fatal("staging waited for unrelated cleanup transaction")
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := s.historyStatus(); got != "collecting" {
		t.Fatalf("status after maintenance: %s", got)
	}
	first.ObservedAt, first.SpeedKmh = base.Add(40*time.Second), ptr(40.0)
	s.stageLive(&LiveData{Collected: base.Add(45 * time.Second), Vehicles: []api.Vehicle{first}}, map[string]*float64{first.Id: ptr(.2)})
	saveHistory(t, s, base.Add(staticRefreshInterval+sourceFreshness), nil, nil)
	var samples int
	var distance, speed float64
	var firstObserved time.Time
	if err := s.DB.QueryRow(ctx, `SELECT speed_sample_count,distance_km,speed_kmh,first_observed_at FROM snapshots`).Scan(&samples, &distance, &speed, &firstObserved); err != nil {
		t.Fatal(err)
	}
	if samples != 2 || math.Abs(distance-.3) > 1e-9 || speed != 30 || !firstObserved.Equal(base.Add(10*time.Second)) {
		t.Fatalf("bucket lost staged samples: count=%d distance=%f speed=%f first=%v", samples, distance, speed, firstObserved)
	}
}

func TestReadinessNoopMaintenanceAndMetroRefresh(t *testing.T) {
	s := admissionStore(t, 4_150_000_000, 0, 5656)
	s.pauseHistoryCollection()
	result, err := s.pruneWithVacuumContext(context.Background(), context.Background())
	if err != nil || result.reason != "awaiting_insert_credit_consumption" || s.historyStatus() != "collecting" {
		t.Fatalf("no-op recovery: result=%+v status=%s err=%v", result, s.historyStatus(), err)
	}
	s.budget.mu.Lock()
	s.budget.checked = time.Time{}
	s.budget.mu.Unlock()
	if err := s.SaveMetro(context.Background(), &MetroData{}, NewCache().operator("metro")); err != nil {
		t.Fatal(err)
	}
	if s.historyStatus() != "collecting" {
		t.Fatalf("metro refresh paused history: %s", s.historyStatus())
	}
}

func TestReadinessRetainedDuringBlockedCheckAndInvalidated(t *testing.T) {
	s := verifiedPressureStore(t, 5)
	ctx := context.Background()
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `LOCK TABLE history_pressure IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, `UPDATE history_pressure SET insert_credit=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		done <- s.refreshEmptyHistoryStatus(ctx)
	}()
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE 'SELECT enabled,insert_credit,vacuum_pending%' AND wait_event_type='Lock')`)
	base := time.Now().UTC().Add(-10 * time.Minute).Truncate(staticRefreshInterval)
	v := historyVehicle("during-readiness", base.Add(10*time.Second), 20)
	s.stageLive(&LiveData{Collected: base.Add(20 * time.Second), Vehicles: []api.Vehicle{v}}, nil)
	if s.historyStatus() != "collecting" || len(s.collector.Pending) != 1 {
		t.Fatal("verified readiness was lost before query completed")
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.historyStatus() != "paused" || s.budget.pressureReady {
		t.Fatal("zero credit did not invalidate readiness")
	}
}

func TestStageWaitsForFinalCreditCommit(t *testing.T) {
	s := verifiedPressureStore(t, 1)
	ctx := context.Background()
	base := time.Now().UTC().Add(-10 * time.Minute).Truncate(staticRefreshInterval)
	v := historyVehicle("credit-committed", base.Add(10*time.Second), 20)
	s.stageLive(&LiveData{Collected: base.Add(20 * time.Second), Vehicles: []api.Vehicle{v}}, nil)
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `SELECT id FROM history_pressure WHERE id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	saved := make(chan error, 1)
	go func() {
		saved <- s.Save(ctx, "carris", nil, &LiveData{Collected: base.Add(staticRefreshInterval + sourceFreshness)}, NewCache().operator("carris"), nil)
	}()
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE 'UPDATE history_pressure SET delete_credit=%' AND wait_event_type='Lock')`)
	second := historyVehicle("after-last-credit", base.Add(staticRefreshInterval+10*time.Second), 20)
	stageStarted, stageDone := make(chan struct{}), make(chan struct{})
	go func() {
		close(stageStarted)
		s.stageLive(&LiveData{Collected: base.Add(staticRefreshInterval + 20*time.Second), Vehicles: []api.Vehicle{second}}, nil)
		close(stageDone)
	}()
	<-stageStarted
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	<-stageDone
	checkSnapshotCount(t, s, 1)
	if s.historyStatus() != "paused" || len(s.collector.Pending) != 0 {
		t.Fatalf("staged after final credit: status=%s pending=%d", s.historyStatus(), len(s.collector.Pending))
	}
}

func TestReadinessVacuumResumesWithoutSave(t *testing.T) {
	s := verifiedPressureStore(t, 5)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `LOCK TABLE snapshots IN SHARE UPDATE EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	result, err := s.pruneWithVacuumContext(ctx, ctx)
	if err != nil || result.reason != "vacuum_pending" {
		t.Fatalf("pending vacuum: %+v %v", result, err)
	}
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query='VACUUM (TRUNCATE FALSE) snapshots' AND wait_event_type='Lock')`)
	if s.historyStatus() != "paused" || s.budget.reserved < cleanupReservation {
		t.Fatal("active vacuum did not fence history and retain its reservation")
	}
	v := historyVehicle("vacuum", time.Now().UTC(), 20)
	s.stageLive(&LiveData{Collected: time.Now().UTC(), Vehicles: []api.Vehicle{v}}, nil)
	if len(s.collector.Pending) != 0 {
		t.Fatal("staged during pressure vacuum")
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s.pressureVacuumWG.Wait()
	var pending bool
	if err := s.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil || pending {
		t.Fatalf("vacuum pending=%v err=%v", pending, err)
	}
	if s.historyStatus() != "collecting" || !s.budget.pressureReady {
		t.Fatalf("vacuum did not restore readiness: %s", s.historyStatus())
	}
	s.stageLive(&LiveData{Collected: time.Now().UTC(), Vehicles: []api.Vehicle{v}}, nil)
	if len(s.collector.Pending) != 1 {
		t.Fatal("staging did not resume after vacuum")
	}
}

func TestReadinessInvalidatesForOperationHeadroom(t *testing.T) {
	for _, optional := range []bool{false, true} {
		b := &storageBudget{now: time.Now, checked: time.Now(), state: "collecting", pressureReady: true, bytes: pressureDatabaseBytes - 1500}
		if optional {
			ok, err := b.reserveOptional(context.Background(), 500)
			if err != nil || !ok {
				t.Fatalf("optional operation rejected: %v %v", ok, err)
			}
		} else if err := b.reserve(context.Background(), 500, operationalDatabaseBytes); err != nil {
			t.Fatal(err)
		}
		if b.status() != "paused" || b.pressureReady {
			t.Fatalf("operation did not invalidate readiness, optional=%v", optional)
		}
	}
	b := &storageBudget{now: time.Now, checked: time.Now(), state: "collecting", pressureReady: true, bytes: 4_150_000_000,
		filesystem: &filesystemProbe{minimumBytes: 1000, minimumInodes: 1, measure: func() (filesystemCapacity, error) {
			return filesystemCapacity{bytes: uint64(1000 + 500 + historyRecordOverhead - 1), inodes: 2}, nil
		}}}
	if err := b.reserve(context.Background(), 500, operationalDatabaseBytes); err != nil {
		t.Fatal(err)
	}
	if b.status() != "paused" || b.pressureReady {
		t.Fatal("filesystem shortage did not invalidate readiness")
	}
}

func TestReadinessCannotSurviveUnknownWriteOrProbeFailure(t *testing.T) {
	for _, fail := range []string{"uncertain", "filesystem", "measurement"} {
		s := verifiedPressureStore(t, 5)
		s.budget.mu.Lock()
		switch fail {
		case "uncertain":
			s.budget.uncertainUnknown = true
		case "filesystem":
			s.budget.filesystem = &filesystemProbe{measure: func() (filesystemCapacity, error) { return filesystemCapacity{}, errors.New("probe failed") }}
		case "measurement":
			s.budget.measure = func(context.Context) (int64, error) { return 0, errors.New("measure failed") }
		}
		s.budget.mu.Unlock()
		if err := s.refreshEmptyHistoryStatus(context.Background()); err == nil {
			t.Fatalf("%s check unexpectedly succeeded", fail)
		}
		if s.historyStatus() != "unavailable" || s.budget.pressureReady {
			t.Fatalf("%s retained unsafe readiness", fail)
		}
	}
}

func TestReadinessOrdinaryVacuumBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		projected int64
		want      string
	}{
		{"below", historyDatabaseBytes - 1, "collecting"},
		{"exact", historyDatabaseBytes, "collecting"},
		{"above", historyDatabaseBytes + 1, "paused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var active atomic.Bool
			active.Store(true)
			measured := tc.projected - cleanupReservation - historyRecordOverhead
			b := &storageBudget{now: time.Now, state: "collecting", pressureReady: true, reserved: cleanupReservation,
				measure: func(context.Context) (int64, error) { return measured, nil }, vacuumActive: &active}
			b.mu.Lock()
			err := b.refreshMeasurement(context.Background())
			b.mu.Unlock()
			if err != nil || b.status() != tc.want || b.pressureReady || b.reserved != cleanupReservation {
				t.Fatalf("status=%s proof=%v reserved=%d err=%v", b.status(), b.pressureReady, b.reserved, err)
			}
		})
	}
}

func TestOrdinaryReadinessClaimsCrossPressureBoundary(t *testing.T) {
	for _, optional := range []bool{false, true} {
		for _, delta := range []int64{0, 1} {
			operation := int64(500)
			b := &storageBudget{now: time.Now, checked: time.Now(), state: "collecting", bytes: historyDatabaseBytes - historyRecordOverhead - operation + delta}
			if optional {
				ok, err := b.reserveOptional(context.Background(), operation)
				if err != nil || !ok {
					t.Fatalf("optional operation rejected: %v %v", ok, err)
				}
			} else if err := b.reserve(context.Background(), operation, operationalDatabaseBytes); err != nil {
				t.Fatal(err)
			}
			want := "collecting"
			if delta > 0 {
				want = "paused"
			}
			if b.status() != want || b.pressureReady {
				t.Fatalf("optional=%v delta=%d status=%s proof=%v", optional, delta, b.status(), b.pressureReady)
			}
		}
	}
}

func TestConfigureHistoryUsesProjectedOrdinaryHeadroom(t *testing.T) {
	s := &Store{budget: &storageBudget{state: "collecting", bytes: historyDatabaseBytes - cleanupReservation, reserved: cleanupReservation}}
	if err := s.ConfigureHistory(30, staticRefreshInterval, true); err != nil {
		t.Fatal(err)
	}
	if s.historyStatus() != "paused" || s.budget.pressureReady {
		t.Fatal("configuration ignored reserved bytes and minimal history")
	}
}

func TestOrdinarySaveChecksCreditAfterCrossingPressure(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	vehicle := historyVehicle("ordinary-final-credit", now, 20)
	live := &LiveData{Collected: now, Vehicles: []api.Vehicle{vehicle}}
	op := NewCache().operator("carris")
	update, err := prepareCacheUpdate(nil, live, op)
	if err != nil {
		t.Fatal(err)
	}
	operations := cacheUpdateBytes(update)
	history := recordBytes(rawHistory(live, nil))
	measured := historyDatabaseBytes - operations - history - historyRecordOverhead + 1
	s := admissionStore(t, measured, 0, 1)
	if err := s.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	s.budget.measure = func(context.Context) (int64, error) { return 4_150_000_000, nil }
	if err := s.Save(ctx, "carris", nil, live, op, nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshotCount(t, s, 1)
	var credit int64
	if err := s.DB.QueryRow(ctx, `SELECT insert_credit FROM history_pressure WHERE id=1`).Scan(&credit); err != nil || credit != 0 {
		t.Fatalf("credit=%d err=%v", credit, err)
	}
	if s.historyStatus() != "paused" || s.budget.pressureReady {
		t.Fatal("ordinary insert consumed final credit without invalidating pressure readiness")
	}
}

func TestVacuumReadinessFailureDoesNotUndoCommit(t *testing.T) {
	s := verifiedPressureStore(t, 5)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `LOCK TABLE snapshots IN SHARE UPDATE EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pruneWithVacuumContext(ctx, ctx); err != nil {
		t.Fatal(err)
	}
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query='VACUUM (TRUNCATE FALSE) snapshots' AND wait_event_type='Lock')`)
	s.budget.mu.Lock()
	s.budget.measure = func(context.Context) (int64, error) { return 0, errors.New("fixture measurement failure") }
	s.budget.mu.Unlock()
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s.pressureVacuumWG.Wait()
	var pending bool
	if err := s.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil || pending {
		t.Fatalf("committed pending clear: pending=%v err=%v", pending, err)
	}
	if s.pressureError.Load() != nil || s.historyStatus() != "unavailable" || s.budget.pressureReady {
		t.Fatal("post-commit readiness failure was not isolated")
	}
}

func TestReadinessRechecksUncertaintyAfterQuery(t *testing.T) {
	s := verifiedPressureStore(t, 5)
	ctx := context.Background()
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `LOCK TABLE history_pressure IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.refreshEmptyHistoryStatus(ctx) }()
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE 'SELECT enabled,insert_credit,vacuum_pending%' AND wait_event_type='Lock')`)
	s.markUncertainWrite(databaseBackend{})
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, errUncertainStorageWrite) {
		t.Fatalf("uncertainty not rechecked: %v", err)
	}
	if s.historyStatus() != "unavailable" || s.budget.pressureReady {
		t.Fatal("query result overwrote uncertainty")
	}
}

func TestCommittedSaveAcknowledgedWhenReadinessFails(t *testing.T) {
	s := verifiedPressureStore(t, 5)
	if err := s.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s.budget.measure = func(check context.Context) (int64, error) {
		var committed bool
		if err := s.DB.QueryRow(check, `SELECT EXISTS(SELECT 1 FROM snapshots WHERE vehicle_id='carris:readiness-failure')`).Scan(&committed); err != nil {
			return 0, err
		}
		if committed {
			return 0, errors.New("fixture post-commit failure")
		}
		return 4_150_000_000, nil
	}
	now := time.Now().UTC()
	v := historyVehicle("readiness-failure", now, 20)
	if err := s.Save(ctx, "carris", nil, &LiveData{Collected: now, Vehicles: []api.Vehicle{v}}, NewCache().operator("carris"), nil); err != nil {
		t.Fatalf("committed save returned failure: %v", err)
	}
	checkSnapshotCount(t, s, 1)
	if s.historyStatus() != "unavailable" || s.budget.pressureReady {
		t.Fatal("post-commit failure did not fail readiness closed")
	}
}

func TestOrdinarySavePreservesUncertaintyFromInterruptedVacuum(t *testing.T) {
	s := admissionStore(t, 3_900_000_000, 0, 5)
	if err := s.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `LOCK TABLE snapshots IN SHARE UPDATE EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, `SELECT id FROM history_pressure WHERE id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	vacuumCtx, cancelVacuum := context.WithCancel(ctx)
	defer cancelVacuum()
	result, err := s.pruneWithVacuumContext(ctx, vacuumCtx)
	if err != nil || result.reason != "vacuum_pending" {
		t.Fatalf("vacuum schedule: %+v %v", result, err)
	}
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query='VACUUM (TRUNCATE FALSE) snapshots' AND wait_event_type='Lock')`)
	now := time.Now().UTC()
	v := historyVehicle("uncertain-vacuum", now, 20)
	done := make(chan error, 1)
	go func() {
		done <- s.Save(ctx, "carris", nil, &LiveData{Collected: now, Vehicles: []api.Vehicle{v}}, NewCache().operator("carris"), nil)
	}()
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query LIKE 'UPDATE history_pressure SET delete_credit=%' AND wait_event_type='Lock')`)
	cancelVacuum()
	s.pressureVacuumWG.Wait()
	s.budget.mu.Lock()
	uncertain := s.budget.uncertainUnknown || len(s.budget.uncertainBackends) > 0
	s.budget.mu.Unlock()
	if !uncertain || s.historyStatus() != "unavailable" {
		t.Fatalf("vacuum interruption did not fence writes: uncertain=%v status=%s", uncertain, s.historyStatus())
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("committed save was not acknowledged: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("save did not finish")
	}
	checkSnapshotCount(t, s, 1)
	if s.historyStatus() != "unavailable" || s.budget.pressureReady {
		t.Fatalf("ordinary readiness overwrote uncertainty: status=%s", s.historyStatus())
	}
}

func TestOrdinaryReadinessRejectsAllUncertainty(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		b := &storageBudget{bytes: 3_900_000_000, reserved: 123, state: "collecting", pressureReady: true}
		if unknown {
			b.uncertainUnknown = true
		} else {
			b.uncertainBackends = map[databaseBackend]struct{}{{pid: 1}: {}}
		}
		ready, err := b.emptyHistoryHeadroom(context.Background(), 0)
		if ready || !errors.Is(err, errUncertainStorageWrite) || b.status() != "unavailable" || b.pressureReady || b.reserved != 123 {
			t.Fatalf("unknown=%v ready=%v status=%s reserved=%d err=%v", unknown, ready, b.status(), b.reserved, err)
		}
	}
}
