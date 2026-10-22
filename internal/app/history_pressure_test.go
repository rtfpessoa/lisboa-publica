package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

// TestHistoryPressureCleanupReproduction captures the current retention/guard
// mismatch using a real database and an over-limit storage measurement.
func TestHistoryPressureCleanupReproduction(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	store.RetentionDays = 30
	_, err := store.DB.Exec(ctx, `INSERT INTO snapshots
		(operator_id, vehicle_id, observed_at, generation, position_kind, lat, lon, payload)
		SELECT 'fixture', 'vehicle-' || n, now() - interval '20 days', 1, 'reported', 0, 0, '{}'::jsonb
		FROM generate_series(1, 1000) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	store.budget = &storageBudget{
		now: time.Now,
		measure: func(context.Context) (int64, error) {
			return historyDatabaseBytes + 1, nil
		},
	}
	if err := store.prune(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.DB.QueryRow(ctx, `SELECT count(*) FROM snapshots`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	t.Logf("over-limit cleanup: remaining=%d status=%s", remaining, store.historyStatus())
	if remaining == 1000 {
		t.Fatal("over-limit cleanup left all older history in place")
	}
}

func waitPressureVacuum(t *testing.T, store *Store) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var pending bool
		if err := store.DB.QueryRow(context.Background(), `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if !pending {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("pressure vacuum did not complete")
}

type pressurePaginationFixture struct {
	store          *Store
	now            time.Time
	path, revision string
	request        func(string) *httptest.ResponseRecorder
	record         historicalRecord
}

func TestHistoryPressureCreditsAndPagination(t *testing.T) {
	f := preparePressurePagination(t)
	assertPressureInitialDeletion(t, f.store)
	assertPressurePagination(t, f)
	assertPressureCreditAccounting(t, f)
	assertPressureCreditRecycling(t, f)
}

func preparePressurePagination(t *testing.T) pressurePaginationFixture {
	t.Helper()
	store := testStore(t)
	ctx := context.Background()
	store.RetentionDays = 30
	_, err := store.DB.Exec(ctx, `INSERT INTO snapshots
		(operator_id, vehicle_id, observed_at, generation, position_kind, lat, lon, payload)
		SELECT 'fixture', 'old-' || n, now() - interval '20 days', 1, 'reported', 0, 0, '{}'::jsonb
		FROM generate_series(1, 12100) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB.Exec(ctx, `INSERT INTO snapshots
		(operator_id, vehicle_id, observed_at, generation, position_kind, lat, lon, payload)
		VALUES ('fixture','recent',now()-interval '1 day',1,'reported',0,0,'{}')`)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(store, NewCache(), Options{Origin: "https://example.com", PublicReads: true}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	path := "/api/v1/history?from=" + url.QueryEscape(now.Add(-10*24*time.Hour).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(now.Format(time.RFC3339Nano))
	request := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}
	first := request(path)
	if first.Code != http.StatusOK {
		t.Fatalf("initial history: %d %s", first.Code, first.Body.String())
	}
	page := decode[api.ListHistory200JSONResponse](t, first.Body.Bytes())
	if page.Page.Revision == nil {
		t.Fatal("missing history revision")
	}
	return pressurePaginationFixture{store: store, now: now, path: path, revision: *page.Page.Revision, request: request}
}

func assertPressureInitialDeletion(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	store.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }}
	if err := store.prune(ctx); err != nil {
		t.Fatal(err)
	}
	waitPressureVacuum(t, store)
	var remaining, recent, credit, allowance int64
	if err := store.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE observed_at >= now()-interval '7 days') FROM snapshots`).Scan(&remaining, &recent); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(ctx, `SELECT insert_credit,delete_credit FROM history_pressure WHERE id=1`).Scan(&credit, &allowance); err != nil {
		t.Fatal(err)
	}
	if remaining != 2101 || recent != 1 || credit != 10000 || allowance != 290000 {
		t.Fatalf("initial deletion: rows=%d recent=%d credit=%d allowance=%d", remaining, recent, credit, allowance)
	}
}

func assertPressurePagination(t *testing.T, f pressurePaginationFixture) {
	t.Helper()
	store, ctx := f.store, context.Background()
	var remaining int64
	if stale := f.request(f.path + "&revision=" + url.QueryEscape(f.revision)); stale.Code != http.StatusGone {
		t.Fatalf("stale revision: %d", stale.Code)
	}
	if fresh := f.request(f.path); fresh.Code != http.StatusOK {
		t.Fatalf("recent window: %d %s", fresh.Code, fresh.Body.String())
	}
	oldPath := "/api/v1/history?from=" + url.QueryEscape(f.now.Add(-25*24*time.Hour).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(f.now.Format(time.RFC3339Nano))
	if truncated := f.request(oldPath); truncated.Code != http.StatusGone {
		t.Fatalf("truncated window: %d", truncated.Code)
	}
	if err := store.prune(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(ctx, `SELECT count(*) FROM snapshots`).Scan(&remaining); err != nil || remaining != 2101 {
		t.Fatalf("repeated deletion without inserts: %d %v", remaining, err)
	}
}

func assertPressureCreditAccounting(t *testing.T, f pressurePaginationFixture) {
	t.Helper()
	store, ctx := f.store, context.Background()
	var credit, allowance int64
	vehicle := api.Vehicle{Id: "fixture:new", OperatorId: "fixture", SourceId: "new", PositionKind: api.VehiclePositionKindReported, ObservedAt: f.now, Lat: 1, Lon: 1}
	record := historicalRecord{Vehicle: vehicle}
	rollback := errors.New("rollback")
	err := store.transaction(ctx, func(tx pgx.Tx) error {
		if err := insertHistory(ctx, tx, historyWrite{operator: "fixture", generation: 2, records: []historicalRecord{record}}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback: %v", err)
	}
	if err := store.DB.QueryRow(ctx, `SELECT insert_credit FROM history_pressure WHERE id=1`).Scan(&credit); err != nil || credit != 10000 {
		t.Fatalf("rolled-back credit: %d %v", credit, err)
	}
	if err := store.transaction(ctx, func(tx pgx.Tx) error {
		return insertHistory(ctx, tx, historyWrite{operator: "fixture", generation: 2, records: []historicalRecord{record, record}})
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(ctx, `SELECT insert_credit,delete_credit FROM history_pressure WHERE id=1`).Scan(&credit, &allowance); err != nil || credit != 9999 || allowance != 290001 {
		t.Fatalf("duplicate credit: %d %d %v", credit, allowance, err)
	}
	oldReplay := record
	oldReplay.Vehicle.ObservedAt = f.now.Add(-8 * 24 * time.Hour)
	if eligible, err := store.replacementEligible(ctx, []historicalRecord{oldReplay}); err != nil || eligible {
		t.Fatalf("old replay earned replacement admission: %v %v", eligible, err)
	}
}

func assertPressureCreditRecycling(t *testing.T, f pressurePaginationFixture) {
	t.Helper()
	store, ctx := f.store, context.Background()
	var credit, allowance int64
	f.record = historicalRecord{Vehicle: api.Vehicle{Id: "fixture:new", OperatorId: "fixture", SourceId: "new", PositionKind: api.VehiclePositionKindReported, ObservedAt: f.now, Lat: 1, Lon: 1}}
	batch := make([]historicalRecord, 8999)
	for i := range batch {
		batch[i] = f.record
		batch[i].Vehicle.Id = fmt.Sprintf("fixture:replacement-%d", i)
	}
	if err := store.transaction(ctx, func(tx pgx.Tx) error {
		return insertHistory(ctx, tx, historyWrite{operator: "fixture", generation: 3, records: batch})
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.prune(ctx); err != nil {
		t.Fatal(err)
	}
	waitPressureVacuum(t, store)
	if err := store.DB.QueryRow(ctx, `SELECT insert_credit,delete_credit FROM history_pressure WHERE id=1`).Scan(&credit, &allowance); err != nil || credit != 3100 || allowance != 296900 {
		t.Fatalf("near-consumed batch did not recycle: %d %d %v", credit, allowance, err)
	}
}

func TestPressureAdmissionMeasuresFreshAndKeepsReservations(t *testing.T) {
	now := time.Now()
	checks := 0
	budget := &storageBudget{
		now:      func() time.Time { return now },
		checked:  now,
		bytes:    historyDatabaseBytes + 1,
		reserved: 10_000_000,
		measure: func(context.Context) (int64, error) {
			checks++
			return pressureDatabaseBytes - 15_000_000, nil
		},
	}
	keep, err := budget.reserveUpdatePressure(context.Background(), 3_000_000, 3_000_000, true)
	if err != nil || keep || checks != 1 || budget.reserved != 13_000_000 || budget.status() != "paused" {
		t.Fatalf("fresh admission: keep=%v checks=%d reserved=%d state=%s err=%v", keep, checks, budget.reserved, budget.status(), err)
	}
}

func TestPressureVacuumPendingBlocksAndRetries(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	store.RetentionDays = 30
	store.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }}
	_, err := store.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,delete_credit=299900,insert_credit=100,vacuum_pending=TRUE WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	vehicle := api.Vehicle{Id: "fixture:new", OperatorId: "fixture", SourceId: "new", PositionKind: api.VehiclePositionKindReported, ObservedAt: time.Now(), Lat: 1, Lon: 1}
	records := []historicalRecord{{Vehicle: vehicle}}
	if eligible, err := store.replacementEligible(ctx, records); err != nil || eligible {
		t.Fatalf("pending vacuum admitted replacement: %v %v", eligible, err)
	}
	// A fresh Store models a restart; the durable pending state still blocks.
	restarted := &Store{DB: store.DB, RetentionDays: 30, budget: store.budget}
	if eligible, err := restarted.replacementEligible(ctx, records); err != nil || eligible {
		t.Fatalf("restart admitted replacement before vacuum: %v %v", eligible, err)
	}
	if err := restarted.prune(ctx); err != nil {
		t.Fatal(err)
	}
	waitPressureVacuum(t, restarted)
	if eligible, err := restarted.replacementEligible(ctx, records); err != nil || !eligible {
		t.Fatalf("successful vacuum did not restore eligibility: %v %v", eligible, err)
	}
}

func TestPressureSaveResumesAndPhysicalCeilingPauses(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, staticRefreshInterval, true); err != nil {
		t.Fatal(err)
	}
	measured := historyDatabaseBytes + 1
	store.budget.measure = func(context.Context) (int64, error) { return measured, nil }
	if _, err := store.DB.Exec(context.Background(), `UPDATE history_pressure SET enabled=TRUE,delete_credit=299900,insert_credit=100 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-15 * time.Minute).Truncate(staticRefreshInterval)
	vehicle := historyVehicle("pressure-one", base.Add(10*time.Second), 20)
	saveHistory(t, store, base.Add(20*time.Second), []api.Vehicle{vehicle}, nil)
	saveHistory(t, store, base.Add(staticRefreshInterval+sourceFreshness), nil, nil)
	checkSnapshotCount(t, store, 1)
	var credit, allowance int64
	if err := store.DB.QueryRow(context.Background(), `SELECT insert_credit,delete_credit FROM history_pressure WHERE id=1`).Scan(&credit, &allowance); err != nil || credit != 99 || allowance != 299901 || store.historyStatus() != "collecting" {
		t.Fatalf("replacement save: credit=%d allowance=%d status=%s err=%v", credit, allowance, store.historyStatus(), err)
	}
	measured = pressureDatabaseBytes
	second := historyVehicle("pressure-two", base.Add(staticRefreshInterval+10*time.Second), 20)
	saveHistory(t, store, base.Add(staticRefreshInterval+20*time.Second), []api.Vehicle{second}, nil)
	saveHistory(t, store, base.Add(2*staticRefreshInterval+sourceFreshness), nil, nil)
	checkSnapshotCount(t, store, 1)
	if err := store.DB.QueryRow(context.Background(), `SELECT insert_credit,delete_credit FROM history_pressure WHERE id=1`).Scan(&credit, &allowance); err != nil || credit != 99 || allowance != 299901 || store.historyStatus() != "paused" {
		t.Fatalf("physical ceiling: credit=%d allowance=%d status=%s err=%v", credit, allowance, store.historyStatus(), err)
	}
}

func TestPressureSavePartialBatchAndNonRecentCredit(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	measured := historyDatabaseBytes + 1
	store.budget.measure = func(context.Context) (int64, error) { return measured, nil }
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,delete_credit=298999,insert_credit=1001 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	vehicles := make([]api.Vehicle, 12002)
	for i := range vehicles {
		vehicles[i] = historyVehicle(fmt.Sprintf("partial-%04d", i), now.Add(time.Duration(len(vehicles)-i)*time.Microsecond), 20)
	}
	if err := store.Save(ctx, "carris", nil, &LiveData{Collected: now, Vehicles: vehicles}, NewCache().operator("carris"), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshotCount(t, store, 1001)
	var credit, allowance int64
	if err := store.DB.QueryRow(ctx, `SELECT insert_credit,delete_credit FROM history_pressure WHERE id=1`).Scan(&credit, &allowance); err != nil || credit != 0 || allowance != 300000 || store.historyStatus() != "paused" {
		t.Fatalf("partial batch: credit=%d allowance=%d status=%s err=%v", credit, allowance, store.historyStatus(), err)
	}
	var first, last time.Time
	if err := store.DB.QueryRow(ctx, `SELECT min(observed_at),max(observed_at) FROM snapshots`).Scan(&first, &last); err != nil || !first.Equal(now.Add(time.Microsecond)) || !last.Equal(now.Add(1001*time.Microsecond)) {
		t.Fatalf("partial ordering: first=%v last=%v err=%v", first, last, err)
	}
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET insert_credit=1000 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	measured = historyDatabaseBytes - 30_000_000
	store.budget.mu.Lock()
	store.budget.checked = time.Time{}
	store.budget.mu.Unlock()
	old := historyVehicle("old-replay", now.Add(-8*24*time.Hour), 20)
	if err := store.Save(ctx, "carris", nil, &LiveData{Collected: now, Vehicles: []api.Vehicle{old}}, NewCache().operator("carris"), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(ctx, `SELECT insert_credit,delete_credit FROM history_pressure WHERE id=1`).Scan(&credit, &allowance); err != nil || credit != 1000 || allowance != 300000 {
		t.Fatalf("non-recent insert changed credit: %d %d %v", credit, allowance, err)
	}
}

func TestPressureVacuumPermissionFailsClosed(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	var version, schemaName string
	if err := store.DB.QueryRow(ctx, `SELECT version(),current_schema()`).Scan(&version, &schemaName); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version, "PostgreSQL") || strings.Contains(version, "CockroachDB") {
		t.Skip("PostgreSQL privilege test")
	}
	role := fmt.Sprintf("lp_pressure_%d", time.Now().UnixNano())
	roleSQL := pgx.Identifier{role}.Sanitize()
	if _, err := store.DB.Exec(ctx, "CREATE ROLE "+roleSQL); err != nil {
		t.Skipf("role creation unavailable: %v", err)
	}
	defer func() {
		_, _ = store.DB.Exec(ctx, "DROP OWNED BY "+roleSQL)
		_, _ = store.DB.Exec(ctx, "DROP ROLE "+roleSQL)
	}()
	if _, err := store.DB.Exec(ctx, "GRANT USAGE ON SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" TO "+roleSQL); err != nil {
		t.Fatal(err)
	}
	cfg := store.DB.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+roleSQL)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	limited := &Store{DB: pool}
	if allowed, err := limited.pressureVacuumAllowed(ctx); err != nil || allowed {
		t.Fatalf("unprivileged vacuum: allowed=%v err=%v", allowed, err)
	}
	assertUnprivilegedEmptyRecovery(t, store, limited, roleSQL)
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,insert_credit=1,vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	limited.schedulePressureVacuum(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for limited.pressureVacuum.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	var pending bool
	if err := store.DB.QueryRow(ctx, `SELECT vacuum_pending FROM history_pressure WHERE id=1`).Scan(&pending); err != nil || !pending || limited.pressureError.Load() == nil {
		t.Fatalf("failed vacuum must remain pending and observable: pending=%v err=%v", pending, err)
	}
	store.schedulePressureVacuum(ctx)
	waitPressureVacuum(t, store)
}

func assertUnprivilegedEmptyRecovery(t *testing.T, store, limited *Store, roleSQL string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, "GRANT SELECT ON history_pressure TO "+roleSQL); err != nil {
		t.Fatal(err)
	}
	limited.budget = &storageBudget{now: time.Now, bytes: historyDatabaseBytes + 1, measure: func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }}
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,insert_credit=1,vacuum_pending=FALSE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := limited.refreshEmptyHistoryStatus(ctx); err != nil || limited.historyStatus() != "paused" {
		t.Fatalf("unprivileged empty recovery: status=%s err=%v", limited.historyStatus(), err)
	}
}

func TestPressureEmptySaveRestoresStaging(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, staticRefreshInterval, true); err != nil {
		t.Fatal(err)
	}
	measured := historyDatabaseBytes + 1
	store.budget.measure = func(context.Context) (int64, error) { return measured, nil }
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,insert_credit=100,vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	assertEmptyPressureStatus(t, store, "paused")
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET vacuum_pending=FALSE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	assertEmptyPressureStatus(t, store, "collecting")
	assertPressureStagesAndPersists(t, store)
	store.budget.mu.Lock()
	store.budget.checked = time.Time{}
	store.budget.mu.Unlock()
	assertEmptyPressureStatus(t, store, "collecting")
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET insert_credit=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	assertEmptyPressureStatus(t, store, "paused")
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET insert_credit=100 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	measured = pressureDatabaseBytes
	assertEmptyPressureStatus(t, store, "paused")
}

func assertEmptyPressureStatus(t *testing.T, store *Store, want string) {
	t.Helper()
	op := NewCache().operator("carris")
	if err := store.Save(context.Background(), "carris", nil, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	if got := store.historyStatus(); got != want {
		t.Fatalf("empty save status=%s, want %s", got, want)
	}
}

func assertPressureStagesAndPersists(t *testing.T, store *Store) {
	t.Helper()
	now := time.Now().UTC()
	vehicle := historyVehicle("pressure-intermediate", now.Add(-10*time.Second), 20)
	store.stageLive(&LiveData{Collected: now, Vehicles: []api.Vehicle{vehicle}}, nil)
	if len(store.collector.Pending) != 1 {
		t.Fatal("recovered collection did not stage an intermediate sample")
	}
	closeAt := vehicle.ObservedAt.Truncate(staticRefreshInterval).Add(staticRefreshInterval + sourceFreshness)
	saveHistory(t, store, closeAt, nil, nil)
	checkSnapshotCount(t, store, 1)
}

func TestPressurePendingVacuumBlocksHistorySave(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	store.budget.measure = func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,insert_credit=100,vacuum_pending=TRUE WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	vehicle := historyVehicle("pending-vacuum", now, 20)
	if err := store.Save(ctx, "carris", nil, &LiveData{Collected: now, Vehicles: []api.Vehicle{vehicle}}, NewCache().operator("carris"), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshotCount(t, store, 0)
	if store.historyStatus() != "paused" {
		t.Fatalf("pending vacuum status=%s", store.historyStatus())
	}
}

func TestPressureEmptyReadinessFailsClosed(t *testing.T) {
	ctx := context.Background()
	budget := &storageBudget{now: time.Now, bytes: historyDatabaseBytes + 1, state: "collecting", measure: func(context.Context) (int64, error) { return 0, errors.New("fixture failure") }}
	store := &Store{budget: budget}
	if err := store.refreshEmptyHistoryStatus(ctx); err == nil || store.historyStatus() != "unavailable" {
		t.Fatalf("measurement failure: status=%s err=%v", store.historyStatus(), err)
	}
	budget.measure = func(context.Context) (int64, error) { return pressureDatabaseBytes - 1, nil }
	budget.reserved = 2
	if err := store.refreshEmptyHistoryStatus(ctx); err != nil || store.historyStatus() != "paused" {
		t.Fatalf("reserved headroom: status=%s err=%v", store.historyStatus(), err)
	}
	budget.bytes, budget.reserved = historyDatabaseBytes-2*historyRecordOverhead, 0
	if err := store.refreshEmptyHistoryStatus(ctx); err != nil || store.historyStatus() != "collecting" {
		t.Fatalf("normal headroom: status=%s err=%v", store.historyStatus(), err)
	}
}

func TestPressureLastCreditPausesAtCommit(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	store.budget.measure = func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,insert_credit=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	vehicle := historyVehicle("final-credit", now, 20)
	if err := store.Save(ctx, "carris", nil, &LiveData{Collected: now, Vehicles: []api.Vehicle{vehicle}}, NewCache().operator("carris"), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshotCount(t, store, 1)
	if store.historyStatus() != "paused" {
		t.Fatalf("status after final credit: %s", store.historyStatus())
	}
}

func TestPressureFailedCommitPausesHistory(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	store.budget.measure = func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }
	ctx := context.Background()
	_, err := store.DB.Exec(ctx, `UPDATE history_pressure SET enabled=TRUE,insert_credit=1 WHERE id=1;
		CREATE FUNCTION fail_history_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture failure'; END $$;
		CREATE TRIGGER fail_history BEFORE INSERT ON snapshots FOR EACH ROW EXECUTE FUNCTION fail_history_insert()`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	vehicle := historyVehicle("failed-commit", now, 20)
	if err := store.Save(ctx, "carris", nil, &LiveData{Collected: now, Vehicles: []api.Vehicle{vehicle}}, NewCache().operator("carris"), nil); err == nil {
		t.Fatal("fixture write unexpectedly succeeded")
	}
	if store.historyStatus() != "paused" {
		t.Fatalf("failed write status=%s", store.historyStatus())
	}
	checkSnapshotCount(t, store, 0)
}

func TestPressureFailedAdmissionPausesHistory(t *testing.T) {
	store := testStore(t)
	if err := store.ConfigureHistory(30, 0, true); err != nil {
		t.Fatal(err)
	}
	store.budget.bytes = historyDatabaseBytes + 1
	store.budget.checked = time.Now()
	store.budget.state = "collecting"
	store.budget.measure = func(context.Context) (int64, error) { return historyDatabaseBytes + 1, nil }
	ctx := context.Background()
	if _, err := store.DB.Exec(ctx, `DROP TABLE history_pressure`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	vehicle := historyVehicle("failed-admission", now, 20)
	if err := store.Save(ctx, "carris", nil, &LiveData{Collected: now, Vehicles: []api.Vehicle{vehicle}}, NewCache().operator("carris"), nil); err == nil {
		t.Fatal("admission unexpectedly succeeded")
	}
	if store.historyStatus() != "paused" {
		t.Fatalf("failed admission status=%s", store.historyStatus())
	}
}
