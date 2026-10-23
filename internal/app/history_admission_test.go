package app

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"lisboapublica/internal/api"
)

func admissionStore(t *testing.T, measured, reserved, credit int64) *Store {
	t.Helper()
	s := testStore(t)
	if err := s.ConfigureHistory(30, staticRefreshInterval, true); err != nil {
		t.Fatal(err)
	}
	s.directPostgres = true
	s.budget = &storageBudget{now: time.Now, checked: time.Now(), state: "collecting", bytes: measured, reserved: reserved,
		measure: func(context.Context) (int64, error) { return measured, nil }, vacuumActive: &s.pressureVacuum}
	if _, err := s.DB.Exec(context.Background(), `UPDATE history_pressure SET enabled=TRUE,insert_credit=$1,vacuum_pending=FALSE WHERE id=1`, credit); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPressureNoopReconcilesCompletedReservations(t *testing.T) {
	s := admissionStore(t, 4_154_496_691, 30_000_000, 5656)
	for i := 0; i < 2; i++ {
		result, err := s.pruneWithVacuumContext(context.Background(), context.Background())
		if err != nil || result.reason != "awaiting_insert_credit_consumption" || result.deleted != 0 {
			t.Fatalf("pass %d: result=%+v err=%v", i, result, err)
		}
		if s.budget.reserved != 0 {
			t.Fatalf("pass %d retained completed cleanup reservations: %d", i, s.budget.reserved)
		}
	}
}

func TestAdmissionReconcilesBeforeSaveAndStagesNextBucket(t *testing.T) {
	s := admissionStore(t, 4_199_000_000, 10_000_000, 100)
	base := time.Now().UTC().Add(-20 * time.Minute).Truncate(staticRefreshInterval)
	first := historyVehicle("admission-first", base.Add(10*time.Second), 20)
	s.stageLive(&LiveData{Collected: base.Add(20 * time.Second), Vehicles: []api.Vehicle{first}}, nil)
	saveHistory(t, s, base.Add(staticRefreshInterval+sourceFreshness), nil, nil)
	checkSnapshotCount(t, s, 1)
	var credit int64
	if err := s.DB.QueryRow(context.Background(), `SELECT insert_credit FROM history_pressure WHERE id=1`).Scan(&credit); err != nil || credit != 99 || s.historyStatus() != "collecting" {
		t.Fatalf("first bucket: credit=%d status=%s err=%v", credit, s.historyStatus(), err)
	}
	second := historyVehicle("admission-second", base.Add(staticRefreshInterval+10*time.Second), 20)
	s.stageLive(&LiveData{Collected: base.Add(staticRefreshInterval + 20*time.Second), Vehicles: []api.Vehicle{second}}, nil)
	saveHistory(t, s, base.Add(2*staticRefreshInterval+sourceFreshness), nil, nil)
	checkSnapshotCount(t, s, 2)
}

func TestEmptySaveReconcilesBeforeHistoryReadiness(t *testing.T) {
	s := admissionStore(t, 4_199_500_000, 2_000_000, 5)
	if err := s.Save(context.Background(), "carris", nil, nil, NewCache().operator("carris"), nil); err != nil {
		t.Fatal(err)
	}
	if s.historyStatus() != "collecting" || s.budget.reserved >= 2_000_000 {
		t.Fatalf("readiness: status=%s reserved=%d", s.historyStatus(), s.budget.reserved)
	}
}

func TestEmptySaveTriggerIncludesReadinessOverhead(t *testing.T) {
	op := NewCache().operator("carris")
	update, err := prepareCacheUpdate(nil, nil, op)
	if err != nil {
		t.Fatal(err)
	}
	operations := cacheUpdateBytes(update)
	measured := historyDatabaseBytes - operations - historyRecordOverhead - 50_000 + 1
	s := admissionStore(t, measured, 50_000, 5)
	if err := s.Save(context.Background(), "carris", nil, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	if s.budget.reserved != operations || s.historyStatus() != "collecting" {
		t.Fatalf("readiness overhead was omitted: reserved=%d operations=%d status=%s", s.budget.reserved, operations, s.historyStatus())
	}
}

func TestUnchangedStaticSaveCanCloseHistoryBucket(t *testing.T) {
	s := admissionStore(t, 4_190_000_000, 0, 5)
	static := fixtureStatic("carris", time.Now().UTC())
	op := NewCache().operator("carris")
	if err := s.Save(context.Background(), "carris", static, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-10 * time.Minute).Truncate(staticRefreshInterval)
	vehicle := historyVehicle("static-save", base.Add(10*time.Second), 20)
	s.stageLive(&LiveData{Collected: base.Add(20 * time.Second), Vehicles: []api.Vehicle{vehicle}}, nil)
	s.budget.mu.Lock()
	s.budget.reserved = 20_000_000
	s.budget.checked = time.Now()
	s.budget.mu.Unlock()
	if err := s.Save(context.Background(), "carris", static, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshotCount(t, s, 1)
}

func TestSaveFallsBackToOperationsOnFilesystemShortage(t *testing.T) {
	s := admissionStore(t, historyDatabaseBytes+1, 10_000_000, 5)
	if err := s.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	v := historyVehicle("filesystem", now, 20)
	live := &LiveData{Collected: now, Vehicles: []api.Vehicle{v}}
	op := NewCache().operator("carris")
	update, err := prepareCacheUpdate(nil, live, op)
	if err != nil {
		t.Fatal(err)
	}
	operations := cacheUpdateBytes(update)
	history := recordBytes(rawHistory(live, nil))
	s.budget.filesystem = &filesystemProbe{minimumBytes: 1000, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) {
			return filesystemCapacity{bytes: uint64(1000 + operations + history - 1), inodes: 2}, nil
		}}
	if err := s.Save(context.Background(), "carris", nil, live, op, nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshotCount(t, s, 0)
	if s.budget.lastRejection.reason != "filesystem_headroom" {
		t.Fatalf("wrong rejection diagnostic: %+v", s.budget.lastRejection)
	}
	var persisted bool
	if err := s.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM source_health WHERE operator_id='carris')`).Scan(&persisted); err != nil || !persisted {
		t.Fatalf("operational save: %v %v", persisted, err)
	}
}

func TestSaveStillRejectsRealCapacityAndUnavailableCredit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		measured int64
		credit   int64
		pending  bool
	}{
		{"current_write_exceeds_limit", pressureDatabaseBytes - 500, 5, false},
		{"no_credit", historyDatabaseBytes + 1, 0, false},
		{"pending_vacuum", historyDatabaseBytes + 1, 5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := admissionStore(t, tc.measured, 10_000_000, tc.credit)
			if err := s.ConfigureHistory(30, 0, true); err != nil {
				t.Fatal(err)
			}
			if tc.pending {
				if _, err := s.DB.Exec(context.Background(), `UPDATE history_pressure SET vacuum_pending=TRUE WHERE id=1`); err != nil {
					t.Fatal(err)
				}
			}
			v := historyVehicle("not-admitted", time.Now().UTC(), 20)
			if err := s.Save(context.Background(), "carris", nil, &LiveData{Collected: time.Now().UTC(), Vehicles: []api.Vehicle{v}}, NewCache().operator("carris"), nil); err != nil {
				t.Fatal(err)
			}
			checkSnapshotCount(t, s, 0)
			var persisted bool
			if err := s.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM source_health WHERE operator_id='carris')`).Scan(&persisted); err != nil || !persisted {
				t.Fatalf("operational save: %v %v", persisted, err)
			}
		})
	}
}

func TestSaveRetainsReservationsWhenReconciliationUnsafe(t *testing.T) {
	for _, tc := range []string{"active_vacuum", "active_backend", "unknown_backend", "measurement_failure", "unverified_backend"} {
		t.Run(tc, func(t *testing.T) {
			s := admissionStore(t, 4_199_000_000, 10_000_000, 5)
			if err := s.ConfigureHistory(30, 0, true); err != nil {
				t.Fatal(err)
			}
			switch tc {
			case "active_vacuum":
				s.pressureVacuum.Store(true)
			case "active_backend":
				conn, err := s.DB.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Release()
				backend, _, _, err := readBackendActivity(context.Background(), conn, conn.Conn().PgConn().PID(), 0)
				if err != nil {
					t.Fatal(err)
				}
				s.markUncertainWrite(backend)
			case "unknown_backend":
				s.markUncertainWrite(databaseBackend{})
			case "measurement_failure":
				s.budget.measure = func(context.Context) (int64, error) { return 0, errors.New("unavailable") }
			case "unverified_backend":
				s.directPostgres = false
			}
			v := historyVehicle("unsafe", time.Now().UTC(), 20)
			err := s.Save(context.Background(), "carris", nil, &LiveData{Collected: time.Now().UTC(), Vehicles: []api.Vehicle{v}}, NewCache().operator("carris"), nil)
			if (tc == "active_backend" || tc == "unknown_backend" || tc == "measurement_failure") != (err != nil) {
				t.Fatalf("unexpected save result: %v", err)
			}
			if s.budget.reserved < 10_000_000 {
				t.Fatalf("unsafe reservation reset: %d", s.budget.reserved)
			}
			checkSnapshotCount(t, s, 0)
		})
	}
}

func TestVacuumFilesystemReserveIncludesConcurrentWriter(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	available := uint64(1000 + 2*cleanupReservation - 1)
	b := &storageBudget{vacuumActive: &active, filesystem: &filesystemProbe{minimumBytes: 1000, minimumInodes: 1,
		measure: func() (filesystemCapacity, error) { return filesystemCapacity{bytes: available, inodes: 2}, nil }}}
	if err := b.checkFilesystem(cleanupReservation); !errors.Is(err, errFilesystemHeadroom) {
		t.Fatalf("vacuum was admitted without separate concurrent headroom: %v", err)
	}
	available++
	if err := b.checkFilesystem(cleanupReservation); err != nil {
		t.Fatalf("vacuum rejected with full headroom: %v", err)
	}
}

func TestOrdinaryCleanupCommittedBeforeMeasurementFailure(t *testing.T) {
	s := admissionStore(t, historyDatabaseBytes+1, 0, 5)
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO snapshots(operator_id,vehicle_id,observed_at,generation,position_kind,lat,lon,payload) VALUES ('fixture','expired',now()-interval '40 days',1,'reported',0,0,'{}')`); err != nil {
		t.Fatal(err)
	}
	checks := 0
	s.budget.measure = func(context.Context) (int64, error) {
		checks++
		if checks > 1 {
			return 0, errors.New("measurement unavailable")
		}
		return historyDatabaseBytes + 1, nil
	}
	result, err := s.pruneWithVacuumContext(context.Background(), context.Background())
	if err == nil || result.reason != "ordinary_cleanup_committed_measurement_failed" || s.budget.reserved != cleanupReservation {
		t.Fatalf("result=%+v reserved=%d err=%v", result, s.budget.reserved, err)
	}
	checkSnapshotCount(t, s, 0)
}

func TestResumedVacuumKeepsReservationUntilPendingCleared(t *testing.T) {
	s := admissionStore(t, historyDatabaseBytes+1, 0, 5)
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
	if err != nil || result.reason != "vacuum_pending" || !s.pressureVacuum.Load() || s.budget.reserved != cleanupReservation {
		t.Fatalf("resume: result=%+v reserved=%d err=%v", result, s.budget.reserved, err)
	}
	waitForBackendActivity(t, s, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query='VACUUM (TRUNCATE FALSE) snapshots' AND wait_event_type='Lock')`)
	s.writeMu.Lock()
	if err := holder.Rollback(ctx); err != nil {
		s.writeMu.Unlock()
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var active bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query='VACUUM (TRUNCATE FALSE) snapshots' AND state='active')`).Scan(&active); err != nil {
			s.writeMu.Unlock()
			t.Fatal(err)
		}
		if !active {
			break
		}
		if time.Now().After(deadline) {
			s.writeMu.Unlock()
			t.Fatal("vacuum did not finish before its pending-state write")
		}
		runtime.Gosched()
	}
	var pending bool
	err = s.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending)
	if err != nil || !pending || !s.pressureVacuum.Load() || s.budget.reserved != cleanupReservation {
		s.writeMu.Unlock()
		t.Fatalf("pending-state write: pending=%v reserved=%d err=%v", pending, s.budget.reserved, err)
	}
	s.writeMu.Unlock()
	waitPressureVacuum(t, s)
	s.pressureVacuumWG.Wait()
}

func assertVacuumSavePreservesReservation(t *testing.T, s *Store) {
	t.Helper()
	vehicle := historyVehicle("pending-vacuum", time.Now().UTC(), 20)
	if err := s.Save(context.Background(), "carris", nil, &LiveData{Collected: time.Now().UTC(), Vehicles: []api.Vehicle{vehicle}}, NewCache().operator("carris"), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshotCount(t, s, 0)
	s.budget.mu.Lock()
	reserved := s.budget.reserved
	s.budget.mu.Unlock()
	if reserved < cleanupReservation {
		t.Fatalf("save cleared the active vacuum reservation: %d", reserved)
	}
}

func assertInterruptedVacuumReservation(t *testing.T, s *Store) databaseBackend {
	t.Helper()
	s.budget.mu.Lock()
	var interrupted databaseBackend
	for backend := range s.budget.uncertainBackends {
		interrupted = backend
	}
	reserved := s.budget.reserved
	s.budget.mu.Unlock()
	if interrupted.pid == 0 || reserved < cleanupReservation {
		t.Fatalf("interrupted vacuum lost its backend or reservation: pid=%d reserved=%d", interrupted.pid, reserved)
	}
	return interrupted
}

func TestMaintenanceLogsAdmissionDiagnosticsOnlyWhenNeeded(t *testing.T) {
	s := admissionStore(t, pressureDatabaseBytes-500, 10_000_000, 5)
	if err := s.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	v := historyVehicle("diagnostic", time.Now().UTC(), 20)
	if err := s.Save(context.Background(), "carris", nil, &LiveData{Collected: time.Now().UTC(), Vehicles: []api.Vehicle{v}}, NewCache().operator("carris"), nil); err != nil {
		t.Fatal(err)
	}
	logs := make(maintenanceLogWriter, 2)
	log := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), logs, zap.InfoLevel))
	s.maintenancePass(context.Background(), log)
	var event map[string]any
	if err := json.Unmarshal(<-logs, &event); err != nil {
		t.Fatal(err)
	}
	if event["history_rejected_batches"] != float64(1) || event["history_rejection_reason"] != "database_headroom" || event["history_insert_credit"] != float64(5) || event["history_vacuum_pending"] != false {
		t.Fatalf("unexpected diagnostic: %+v", event)
	}
	s.maintenancePass(context.Background(), log)
	event = nil
	if err := json.Unmarshal(<-logs, &event); err != nil {
		t.Fatal(err)
	}
	if _, ok := event["history_rejected_batches"]; ok {
		t.Fatal("empty interval repeated diagnostics")
	}
}

func TestAdmissionDiagnosticsRemainVisibleAtWarnLevel(t *testing.T) {
	s := admissionStore(t, pressureDatabaseBytes-500, 10_000_000, 5)
	if err := s.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	v := historyVehicle("warning", time.Now().UTC(), 20)
	if err := s.Save(context.Background(), "carris", nil, &LiveData{Collected: time.Now().UTC(), Vehicles: []api.Vehicle{v}}, NewCache().operator("carris"), nil); err != nil {
		t.Fatal(err)
	}
	logs := make(maintenanceLogWriter, 2)
	log := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), logs, zap.WarnLevel))
	s.maintenancePass(context.Background(), log)
	var event map[string]any
	if err := json.Unmarshal(<-logs, &event); err != nil || event["history_rejected_batches"] != float64(1) || event["level"] != "warn" {
		t.Fatalf("warning diagnostic: %+v %v", event, err)
	}
	s.maintenancePass(context.Background(), log)
	select {
	case entry := <-logs:
		t.Fatalf("unexpected repeated warning: %s", entry)
	default:
	}
}
