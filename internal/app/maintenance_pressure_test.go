package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Reservations from already-committed writes cannot permanently prevent the
// maintenance pass from recovering history when fresh measured size permits it.
func TestPruneReconcilesCommittedReservations(t *testing.T) {
	s := testStore(t)
	s.directPostgres = true
	ctx := context.Background()
	_, err := s.DB.Exec(ctx, `INSERT INTO snapshots
		(operator_id, vehicle_id, observed_at, generation, position_kind, lat, lon, payload)
		VALUES ('fixture','old',now()-interval '20 days',1,'reported',0,0,'{}'),
		       ('fixture','recent',now()-interval '1 day',1,'reported',0,0,'{}')`)
	if err != nil {
		t.Fatal(err)
	}
	s.RetentionDays = 30
	s.budget = &storageBudget{
		now: time.Now, checked: time.Now(), state: "paused",
		bytes: 4_122_506_931, reserved: 65_000_000,
		measure: func(context.Context) (int64, error) { return 4_122_506_931, nil },
	}
	if err := s.prune(ctx); err != nil {
		t.Fatal(err)
	}
	waitPressureVacuum(t, s)
	var old, recent, credit int
	err = s.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE observed_at<now()-interval '7 days'),
		count(*) FILTER (WHERE observed_at>=now()-interval '7 days') FROM snapshots`).Scan(&old, &recent)
	if err == nil {
		err = s.DB.QueryRow(ctx, `SELECT insert_credit FROM history_pressure WHERE id=1`).Scan(&credit)
	}
	if err != nil || old != 0 || recent != 1 || credit != 1 {
		t.Fatalf("cleanup: old=%d recent=%d insert credit=%d err=%v", old, recent, credit, err)
	}
}

func startupTestDSN(t *testing.T) string {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	var canCreate bool
	if err := s.DB.QueryRow(ctx, `SELECT rolcreatedb OR rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&canCreate); err != nil || !canCreate {
		t.Skip("isolated startup fixture requires CREATEDB")
	}
	database := fmt.Sprintf("lp_startup_%d", time.Now().UnixNano())
	if _, err := s.DB.Exec(ctx, "CREATE DATABASE "+database); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.DB.Exec(ctx, "DROP DATABASE "+database) })
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + database
	return u.String()
}

func requireStartupBlocked(t *testing.T, dsn string) {
	t.Helper()
	store, err := OpenStoreWithStorageOptions(context.Background(), dsn, true, nil, true)
	if err == nil {
		store.DB.Close()
		t.Fatal("startup accepted a prior backend")
	}
	if !strings.Contains(err.Error(), "before migrations") {
		t.Fatalf("unexpected startup error: %v", err)
	}
}

func TestDirectStartupWaitsForPriorTransactionBeforeMigrations(t *testing.T) {
	dsn := startupTestDSN(t)
	ctx := context.Background()
	holder, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	conn, err := holder.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	requireStartupBlocked(t, dsn)
	var table *string
	if err := tx.QueryRow(ctx, `SELECT to_regclass('public.history_pressure')::text`).Scan(&table); err != nil || table != nil {
		t.Fatalf("migrations ran before the startup barrier: table=%v err=%v", table, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// An idle session may still have a command queued in transit.
	requireStartupBlocked(t, dsn)
	conn.Release()
	holder.Close()
	store := openStoreAfterSessionsExit(t, dsn)
	store.DB.Close()
}

func startBlockingWrite(t *testing.T, s *Store, ctx context.Context) (int32, <-chan error) {
	t.Helper()
	started := make(chan int32, 1)
	done := make(chan error, 1)
	go func() {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		done <- s.transaction(ctx, func(tx pgx.Tx) error {
			var pid int32
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO snapshots (operator_id,vehicle_id,observed_at,generation,position_kind,lat,lon,payload) VALUES ('old','write',now(),1,'reported',0,0,'{}')`); err != nil {
				return err
			}
			started <- pid
			_, err := tx.Exec(ctx, `SELECT pg_sleep(30)`)
			return err
		})
	}()
	select {
	case pid := <-started:
		return pid, done
	case err := <-done:
		t.Fatalf("write did not start: %v", err)
		return 0, nil
	}
}

func waitForBackendActivity(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var active bool
		if err := s.DB.QueryRow(context.Background(), query, args...).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("backend did not reach its expected activity")
		}
		runtime.Gosched()
	}
}

func TestDirectStartupAfterCanceledWrite(t *testing.T) {
	dsn := startupTestDSN(t)
	store := openStoreAfterSessionsExit(t, dsn)
	defer func() { store.DB.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pid, done := startBlockingWrite(t, store, ctx)
	waitForBackendActivity(t, store, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event='PgSleep')`, pid)
	requireStartupBlocked(t, dsn)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled previous-owner write succeeded")
	}
	store.DB.Close()
	store = openStoreAfterSessionsExit(t, dsn)
	var rows int
	if err := store.DB.QueryRow(context.Background(), `SELECT count(*) FROM snapshots WHERE operator_id='old'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("old canceled transaction survived restart: rows=%d err=%v", rows, err)
	}
}

func beginBlockedVacuum(t *testing.T, s *Store, ctx context.Context) (pgx.Tx, <-chan error) {
	t.Helper()
	if _, err := s.DB.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	blocker, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `LOCK TABLE snapshots IN SHARE UPDATE EXCLUSIVE MODE`); err != nil {
		_ = blocker.Rollback(context.Background())
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.runPressureVacuum(ctx) }()
	return blocker, done
}

func TestDirectStartupAfterCanceledVacuum(t *testing.T) {
	dsn := startupTestDSN(t)
	store := openStoreAfterSessionsExit(t, dsn)
	defer func() { store.DB.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	blocker, done := beginBlockedVacuum(t, store, ctx)
	defer blocker.Rollback(context.Background())
	waitForBackendActivity(t, store, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND query='VACUUM (TRUNCATE FALSE) snapshots' AND wait_event_type='Lock')`)
	requireStartupBlocked(t, dsn)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled previous-owner vacuum succeeded")
	}
	if err := blocker.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.DB.Close()
	store = openStoreAfterSessionsExit(t, dsn)
	assertPendingVacuumRecovers(t, store)
}

func assertPendingVacuumRecovers(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	var pending bool
	if err := store.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil || !pending {
		t.Fatalf("pending vacuum was lost across restart: pending=%v err=%v", pending, err)
	}
	stop, err := store.startMaintenance(ctx, zap.NewNop(), make(chan time.Time))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	waitPressureVacuum(t, store)
	stop()
	if err := store.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil || pending {
		t.Fatalf("restarted vacuum did not finish: pending=%v err=%v", pending, err)
	}
}

func openStoreAfterSessionsExit(t *testing.T, dsn string) *Store {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		store, err := OpenStoreWithStorageOptions(context.Background(), dsn, true, nil, true)
		if err == nil {
			return store
		}
		if time.Now().After(deadline) {
			t.Fatalf("previous sessions did not leave: %v", err)
		}
		runtime.Gosched()
	}
}

func TestBackendRecoverySelfReuseAndIdentity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	cfg := s.DB.Config().Copy()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	backend, _, _, err := readBackendActivity(ctx, conn, conn.Conn().PgConn().PID(), 0)
	conn.Release()
	if err != nil {
		t.Fatal(err)
	}
	r := &Store{DB: pool, postgres: true, directPostgres: true, budget: &storageBudget{
		now: time.Now, bytes: 4_100_000_000, reserved: 40_000_000,
		measure: func(context.Context) (int64, error) { return 4_100_000_000, nil },
	}}
	// The one-connection pool must reuse the checking backend. A direct
	// connection cannot be reused until its prior transaction has ended.
	r.markUncertainWrite(backend)
	r.writeMu.Lock()
	err = r.reconcileCompletedReservations(ctx)
	r.writeMu.Unlock()
	if err != nil || r.budget.reserved != 0 {
		t.Fatalf("safe self reuse did not recover: reserved=%d err=%v", r.budget.reserved, err)
	}
	backend.host = "203.0.113.1"
	r.budget.reserved = 40_000_000
	r.markUncertainWrite(backend)
	r.writeMu.Lock()
	err = r.reconcileCompletedReservations(ctx)
	r.writeMu.Unlock()
	if err == nil || r.budget.reserved != 40_000_000 {
		t.Fatalf("different server identity was accepted: reserved=%d err=%v", r.budget.reserved, err)
	}
}

func TestBackendRecoveryWaitsForReusedPIDInTransaction(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.directPostgres = true
	s.budget = &storageBudget{now: time.Now, reserved: 40_000_000,
		measure: func(context.Context) (int64, error) { return 4_100_000_000, nil }}
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if conn != nil {
			conn.Release()
		}
	}()
	backend, _, _, err := readBackendActivity(ctx, conn, conn.Conn().PgConn().PID(), 0)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	s.markUncertainWrite(backend)
	s.writeMu.Lock()
	err = s.reconcileCompletedReservations(ctx)
	s.writeMu.Unlock()
	if err == nil || s.budget.reserved != 40_000_000 {
		t.Fatalf("active reused PID was accepted: reserved=%d err=%v", s.budget.reserved, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s.writeMu.Lock()
	err = s.reconcileCompletedReservations(ctx)
	s.writeMu.Unlock()
	if err == nil || s.budget.reserved != 40_000_000 {
		t.Fatalf("idle backend might have work in transit: reserved=%d err=%v", s.budget.reserved, err)
	}
	underlying := conn.Hijack()
	conn = nil
	if err := underlying.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s.writeMu.Lock()
	err = s.reconcileCompletedReservations(ctx)
	s.writeMu.Unlock()
	if err != nil || s.budget.reserved != 0 {
		t.Fatalf("terminated backend was not accepted: reserved=%d err=%v", s.budget.reserved, err)
	}
}

func TestActivityViewUnavailableFailsClosed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var superuser bool
	if err := s.DB.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&superuser); err != nil || !superuser {
		t.Skip("permission fixture requires a PostgreSQL superuser on the isolated test database")
	}
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `REVOKE SELECT ON pg_catalog.pg_stat_activity FROM PUBLIC`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE pg_read_all_stats`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readBackendActivity(ctx, tx, conn.Conn().PgConn().PID(), 0); err == nil {
		t.Fatal("unavailable activity view must fail closed")
	}
}

func TestCanceledWriteKeepsReservationUntilBackendExits(t *testing.T) {
	s := testStore(t)
	s.directPostgres = true
	s.budget = &storageBudget{now: time.Now, bytes: 4_100_000_000, reserved: 50_000_000,
		measure: func(context.Context) (int64, error) { return 4_100_000_000, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidCh := make(chan int32, 1)
	done := make(chan error, 1)
	go func() {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		done <- s.transaction(ctx, func(tx pgx.Tx) error {
			var pid int32
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO snapshots
				(operator_id,vehicle_id,observed_at,generation,position_kind,lat,lon,payload)
				VALUES ('fixture','pending',now(),1,'reported',0,0,'{}')`); err != nil {
				return err
			}
			pidCh <- pid
			_, err := tx.Exec(ctx, `SELECT pg_sleep(30)`)
			return err
		})
	}()
	pid := <-pidCh
	deadline := time.Now().Add(10 * time.Second)
	for {
		var sleeping bool
		err := s.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event='PgSleep')`, pid).Scan(&sleeping)
		if err != nil {
			t.Fatal(err)
		}
		if sleeping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture write did not enter its cancellable query")
		}
		runtime.Gosched()
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled write unexpectedly succeeded")
	}
	s.budget.mu.Lock()
	tracked := false
	for backend := range s.budget.uncertainBackends {
		tracked = tracked || backend.pid == pid
	}
	reserved := s.budget.reserved
	if err := s.budget.refresh(context.Background()); err == nil || reserved != 50_000_000 || s.budget.reserved != reserved {
		s.budget.mu.Unlock()
		t.Fatalf("canceled write lost its reservation: tracked=%v reserved=%d err=%v", tracked, reserved, err)
	}
	s.budget.mu.Unlock()
	if !tracked {
		t.Fatal("canceled write backend not tracked")
	}
	for {
		var present bool
		if err := s.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)`, pid).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled backend did not terminate")
		}
		runtime.Gosched()
	}
	s.writeMu.Lock()
	err := s.reconcileCompletedReservations(context.Background())
	s.writeMu.Unlock()
	if err != nil || s.budget.reserved != 0 {
		t.Fatalf("completed backend did not release reservation: %d %v", s.budget.reserved, err)
	}
}

func TestReconcileReservationsPreservesUncertainWrites(t *testing.T) {
	s := &Store{postgres: true, directPostgres: true, budget: &storageBudget{
		now: time.Now, bytes: 4_100_000_000, reserved: 40_000_000,
		measure: func(context.Context) (int64, error) { return 0, errors.New("measurement failed") },
	}}
	s.writeMu.Lock()
	if err := s.reconcileCompletedReservations(context.Background()); err == nil || s.budget.reserved != 40_000_000 {
		t.Fatalf("failed measurement must retain reservation: %d, %v", s.budget.reserved, err)
	}
	s.pressureVacuum.Store(true)
	if err := s.reconcileCompletedReservations(context.Background()); err != nil || s.budget.reserved != 40_000_000 {
		t.Fatalf("in-flight vacuum must retain reservation: %d, %v", s.budget.reserved, err)
	}
	s.pressureVacuum.Store(false)
	s.writeMu.Unlock()
}

func TestUnverifiedBackendDoesNotUseExtraReconciliation(t *testing.T) {
	checks := 0
	s := &Store{postgres: true, budget: &storageBudget{reserved: 40_000_000, now: time.Now,
		measure: func(context.Context) (int64, error) { checks++; return 1, nil }}}
	s.writeMu.Lock()
	err := s.reconcileCompletedReservations(context.Background())
	s.writeMu.Unlock()
	if err != nil || checks != 0 || s.budget.reserved != 40_000_000 {
		t.Fatalf("unverified backend used aggressive reconciliation: checks=%d reserved=%d err=%v", checks, s.budget.reserved, err)
	}
	s.markUncertainWrite(databaseBackend{})
	if err := s.reserveStorage(context.Background(), 1, operationalDatabaseBytes); !errors.Is(err, errUncertainStorageWrite) {
		t.Fatalf("unidentified backend did not fail closed: %v", err)
	}
}

func TestTransactionPanicRollsBack(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = s.transaction(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO snapshots (operator_id,vehicle_id,observed_at,generation,position_kind,lat,lon,payload) VALUES ('panic','write',now(),1,'reported',0,0,'{}')`); err != nil {
				t.Fatal(err)
			}
			panic("fixture")
		})
	}()
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM snapshots WHERE operator_id='panic'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("panic left a transaction open or committed: count=%d err=%v", count, err)
	}
}
