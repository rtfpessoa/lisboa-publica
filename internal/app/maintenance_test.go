package app

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type maintenanceLogWriter chan []byte

func (w maintenanceLogWriter) Write(p []byte) (int, error) {
	w <- append([]byte(nil), p...)
	return len(p), nil
}
func (maintenanceLogWriter) Sync() error { return nil }

func TestMaintenanceRunsWithoutIngestion(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, err := s.DB.Exec(ctx, `INSERT INTO snapshots
		(operator_id,vehicle_id,observed_at,generation,position_kind,lat,lon,payload)
		VALUES ('fixture','old',now()-interval '20 days',1,'reported',0,0,'{}')`)
	if err != nil {
		t.Fatal(err)
	}
	s.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }}
	logs := make(maintenanceLogWriter, 8)
	log := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), logs, zap.InfoLevel))
	ticks := make(chan time.Time)
	stop, err := s.startMaintenance(ctx, log, ticks)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := s.startMaintenance(ctx, log, ticks); err == nil {
		t.Fatal("second maintenance owner accepted")
	}
	var event struct {
		Reason  string `json:"reason"`
		Deleted int64  `json:"deleted"`
	}
	if err := json.Unmarshal(<-logs, &event); err != nil || event.Reason != "vacuum_scheduled" || event.Deleted != 1 {
		t.Fatalf("initial pass: %+v %v", event, err)
	}
	waitPressureVacuum(t, s)
	s.pressureVacuumWG.Wait()
	_, err = s.DB.Exec(ctx, `UPDATE history_pressure SET insert_credit=0 WHERE id=1;
		INSERT INTO snapshots (operator_id,vehicle_id,observed_at,generation,position_kind,lat,lon,payload)
		VALUES ('fixture','next-old',now()-interval '19 days',1,'reported',0,0,'{}')`)
	if err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case entry := <-logs:
			if err := json.Unmarshal(entry, &event); err != nil {
				t.Fatal(err)
			}
			if event.Reason == "vacuum_scheduled" && event.Deleted == 1 {
				waitPressureVacuum(t, s)
				return
			}
		case <-deadline:
			t.Fatal("independent periodic pass did not remove the next eligible record")
		}
	}
}

func TestVacuumOutlivesPruneDeadline(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO snapshots
		(operator_id,vehicle_id,observed_at,generation,position_kind,lat,lon,payload)
		VALUES ('fixture','old',now()-interval '20 days',1,'reported',0,0,'{}')`); err != nil {
		t.Fatal(err)
	}
	s.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }}
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err = holder.Exec(ctx, `LOCK TABLE snapshots IN SHARE UPDATE EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	passCtx, cancel := context.WithCancel(ctx)
	result, err := s.pruneWithVacuumContext(passCtx, ctx)
	cancel()
	if err != nil || result.deleted != 1 {
		t.Fatalf("pressure pass: %+v %v", result, err)
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	waitPressureVacuum(t, s)
	s.pressureVacuumWG.Wait()
}

func TestMaintenanceShutdownCancelsWaitingPass(t *testing.T) {
	s := testStore(t)
	ticks := make(chan time.Time)
	stop, err := s.startMaintenance(context.Background(), zap.NewNop(), ticks)
	if err != nil {
		t.Fatal(err)
	}
	s.writeMu.Lock()
	ticks <- time.Now()
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.writeMu.Unlock()
		t.Fatal("shutdown did not cancel the blocked maintenance pass")
	}
	s.writeMu.Unlock()
	stop() // shutdown is idempotent
}

func TestMaintenanceShutdownLeavesInterruptedVacuumPending(t *testing.T) {
	s := testStore(t)
	s.directPostgres = true
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	s.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }, vacuumActive: &s.pressureVacuum}
	holder, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err = holder.Exec(ctx, `LOCK TABLE snapshots IN SHARE UPDATE EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	stop, err := s.startMaintenance(ctx, zap.NewNop(), make(chan time.Time))
	if err != nil {
		t.Fatal(err)
	}
	if !s.pressureVacuum.Load() {
		t.Fatal("pending vacuum was not scheduled")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE query='VACUUM (TRUNCATE FALSE) snapshots' AND wait_event_type='Lock')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("vacuum did not enter its blocked statement")
		}
		runtime.Gosched()
	}
	assertVacuumSavePreservesReservation(t, s)
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		holder.Rollback(ctx)
		t.Fatal("shutdown did not cancel and join the blocked vacuum")
	}
	var pending bool
	if err := s.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil || !pending {
		t.Fatalf("interrupted vacuum not pending: %v %v", pending, err)
	}
	interrupted := assertInterruptedVacuumReservation(t, s)
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		var active bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND state IS DISTINCT FROM 'idle')`, interrupted.pid).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("interrupted vacuum backend did not finish")
		}
		runtime.Gosched()
	}
	stop, err = s.startMaintenance(ctx, zap.NewNop(), make(chan time.Time))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	waitPressureVacuum(t, s)
}

func TestMaintenanceShutdownCancelsHistoryReaderWait(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO snapshots
		(operator_id,vehicle_id,observed_at,generation,position_kind,lat,lon,payload)
		VALUES ('fixture','old',now()-interval '20 days',1,'reported',0,0,'{}')`); err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	stop, err := s.startMaintenance(ctx, zap.NewNop(), ticks)
	if err != nil {
		t.Fatal(err)
	}
	s.writeMu.Lock()
	s.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }}
	s.writeMu.Unlock()
	s.historyReadMu.RLock()
	ticks <- time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for s.writeMu.TryLock() {
		s.writeMu.Unlock()
		if time.Now().After(deadline) {
			s.historyReadMu.RUnlock()
			stop()
			t.Fatal("maintenance did not start")
		}
		runtime.Gosched()
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		s.historyReadMu.RUnlock()
		t.Fatal("shutdown did not cancel the historical reader wait")
	}
	s.historyReadMu.RUnlock()
	var remaining int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM snapshots`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("blocked cleanup changed history: remaining=%d err=%v", remaining, err)
	}
}
