package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

const schema = `
CREATE TABLE IF NOT EXISTS app_state (id INT PRIMARY KEY, generation BIGINT NOT NULL);
INSERT INTO app_state (id,generation) VALUES (1,0) ON CONFLICT (id) DO NOTHING;
CREATE TABLE IF NOT EXISTS history_pressure (id INT PRIMARY KEY, enabled BOOLEAN NOT NULL, delete_credit BIGINT NOT NULL, insert_credit BIGINT NOT NULL, epoch BIGINT NOT NULL, cutoff TIMESTAMPTZ, vacuum_pending BOOLEAN NOT NULL DEFAULT FALSE);
INSERT INTO history_pressure (id,enabled,delete_credit,insert_credit,epoch) VALUES (1,FALSE,300000,0,0) ON CONFLICT (id) DO NOTHING;
CREATE TABLE IF NOT EXISTS cache_parts (operator_id TEXT NOT NULL, kind TEXT NOT NULL, part INT NOT NULL, data BYTEA NOT NULL, PRIMARY KEY(operator_id,kind,part));
CREATE TABLE IF NOT EXISTS vehicle_facts (operator_id TEXT NOT NULL, source_id TEXT NOT NULL, payload JSONB NOT NULL, PRIMARY KEY(operator_id,source_id));
CREATE TABLE IF NOT EXISTS vehicle_reporting (operator_id TEXT NOT NULL, source_id TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL, payload JSONB NOT NULL, PRIMARY KEY(operator_id,source_id));
CREATE TABLE IF NOT EXISTS source_health (operator_id TEXT PRIMARY KEY, payload JSONB NOT NULL);
CREATE TABLE IF NOT EXISTS snapshots (
 operator_id TEXT NOT NULL, vehicle_id TEXT NOT NULL, observed_at TIMESTAMPTZ NOT NULL,
 generation BIGINT NOT NULL, route_id TEXT, trip_id TEXT, position_kind TEXT NOT NULL,
 lat DOUBLE PRECISION NOT NULL, lon DOUBLE PRECISION NOT NULL, speed_kmh DOUBLE PRECISION,
 distance_km DOUBLE PRECISION, payload JSONB NOT NULL,
 first_observed_at TIMESTAMPTZ, speed_sample_count INT NOT NULL DEFAULT 1,
 PRIMARY KEY(operator_id,vehicle_id,observed_at));
ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS first_observed_at TIMESTAMPTZ;
ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS speed_sample_count INT NOT NULL DEFAULT 1;
ALTER TABLE snapshots ADD COLUMN IF NOT EXISTS payload_archive BYTEA;
CREATE INDEX IF NOT EXISTS snapshots_time ON snapshots(observed_at,operator_id,generation);
CREATE INDEX IF NOT EXISTS snapshots_route ON snapshots(route_id,observed_at);
CREATE TABLE IF NOT EXISTS stop_events (
 journey_id TEXT NOT NULL, stop_sequence INT NOT NULL, event_kind TEXT NOT NULL,
 generation BIGINT NOT NULL, event_at TIMESTAMPTZ NOT NULL, payload JSONB NOT NULL,
 PRIMARY KEY(journey_id,stop_sequence,event_kind,generation));
CREATE INDEX IF NOT EXISTS stop_events_time ON stop_events(event_at);
CREATE TABLE IF NOT EXISTS sessions (token_hash TEXT PRIMARY KEY, email TEXT NOT NULL, name TEXT NOT NULL, auth_kind TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, owner_email TEXT NOT NULL, name TEXT NOT NULL, token_hash TEXT UNIQUE NOT NULL, scopes TEXT[] NOT NULL, created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL, revoked BOOLEAN NOT NULL DEFAULT FALSE);
CREATE INDEX IF NOT EXISTS keys_owner ON api_keys(owner_email,created_at);
`

// Store persists cached feeds, observations, hashed sessions and scoped API keys.
type Store struct {
	DB                 *pgxpool.Pool
	RetentionDays      int
	HistoryInterval    time.Duration
	writeMu            writeLock
	historyReadMu      historyLock
	pressureVacuum     atomic.Bool
	pressureVacuumWG   sync.WaitGroup
	maintenanceRunning atomic.Bool
	maintenanceLog     atomic.Pointer[zap.Logger]
	postgres           bool
	directPostgres     bool
	pressureError      atomic.Pointer[string]
	historyMu          sync.Mutex
	collector          *historyCollector
	archiveCursor      *snapshotArchiveKey
	budget             *storageBudget
	PublishMu          sync.Mutex
	facts              factRegistry
	reporting          reportingRegistry
}

// OpenStore opens a database pool and applies compatible Postgres/Cockroach migrations.
func OpenStore(ctx context.Context, url string) (*Store, error) {
	return OpenStoreWithStorageGuard(ctx, url, false)
}

// OpenStoreWithStorageGuard budgets migrations before applying any schema writes.
func OpenStoreWithStorageGuard(ctx context.Context, url string, guard bool) (*Store, error) {
	return OpenStoreWithStorageFilesystem(ctx, url, guard, nil)
}

// OpenStoreWithStorageFilesystem enables an optional fail-closed local PGDATA
// filesystem probe in addition to the existing database-size storage guard.
func OpenStoreWithStorageFilesystem(ctx context.Context, url string, guard bool, filesystem *StorageFilesystemConfig) (*Store, error) {
	return OpenStoreWithStorageOptions(ctx, url, guard, filesystem, false)
}

// OpenStoreWithStorageOptions checks for work left by a previous process before
// migrations when direct PostgreSQL identity has been verified by an operator.
func OpenStoreWithStorageOptions(ctx context.Context, url string, guard bool, filesystem *StorageFilesystemConfig, direct bool) (*Store, error) {
	if direct && !guard {
		return nil, fmt.Errorf("direct PostgreSQL recovery requires a storage guard")
	}
	var probe *filesystemProbe
	if filesystem != nil {
		if !guard {
			return nil, fmt.Errorf("database filesystem probe requires STORAGE_GUARD")
		}
		var err error
		probe, err = newFilesystemProbe(*filesystem)
		if err != nil {
			return nil, err
		}
	}
	pool, err := openDatabasePool(ctx, url)
	if err != nil {
		return nil, err
	}
	value := &Store{DB: pool, directPostgres: direct}
	if guard {
		value.budget = &storageBudget{measure: value.databaseBytes, now: time.Now, state: "unavailable", filesystem: probe, vacuumActive: &value.pressureVacuum}
	}
	if err = value.initialize(ctx, guard); err != nil {
		pool.Close()
		return nil, err
	}
	return value, nil
}

func openDatabasePool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = databaseMaxConnections
	cfg.ConnConfig.RuntimeParams["application_name"] = "lisboapublica"
	return pgxpool.NewWithConfig(ctx, cfg)
}

func (s *Store) initialize(ctx context.Context, guard bool) error {
	if err := s.DB.Ping(ctx); err != nil {
		return err
	}
	var product string
	if err := s.DB.QueryRow(ctx, "SELECT version()").Scan(&product); err != nil {
		return err
	}
	s.postgres = strings.Contains(product, "PostgreSQL") && !strings.Contains(product, "CockroachDB")
	if err := s.initializeDirectPostgres(ctx); err != nil {
		return err
	}
	if err := s.initializeStorageGuard(ctx, guard); err != nil {
		return err
	}
	return s.applySchema(ctx)
}

func (s *Store) initializeDirectPostgres(ctx context.Context) error {
	if !s.directPostgres {
		return nil
	}
	if !s.postgres {
		return fmt.Errorf("direct PostgreSQL recovery requires PostgreSQL")
	}
	if err := s.ensurePriorBackendsSettled(ctx); err != nil {
		return fmt.Errorf("guarded database initialization refused before migrations: %w", err)
	}
	return nil
}

func (s *Store) initializeStorageGuard(ctx context.Context, guard bool) error {
	if !guard {
		return nil
	}
	if s.budget == nil {
		s.budget = &storageBudget{measure: s.databaseBytes, now: time.Now, state: "unavailable", vacuumActive: &s.pressureVacuum}
	}
	if err := s.reserveMigrations(ctx); err != nil {
		return fmt.Errorf("guarded database initialization refused before migrations; inspect storage and use the read-only recovery procedure: %w", err)
	}
	return nil
}

func (s *Store) applySchema(ctx context.Context) error {
	for _, q := range splitStatements(schema) {
		if _, err := s.DB.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func splitStatements(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == ';' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
func (s *Store) transaction(ctx context.Context, fn func(pgx.Tx) error) error {
	for attempt := 0; attempt < serializationAttempts; attempt++ {
		e, uncertain := s.transactionAttempt(ctx, fn)
		if e == nil {
			return nil
		}
		var pg *pgconn.PgError
		if uncertain || !errors.As(e, &pg) || pg.Code != "40001" {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(serializationBackoffMillis*(1<<attempt)) * time.Millisecond):
		}
	}
	return fmt.Errorf("serialization retry limit exceeded")
}

func (s *Store) transactionAttempt(ctx context.Context, fn func(pgx.Tx) error) (error, bool) {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return err, false
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err, false
	}
	var backend databaseBackend
	defer func() {
		if recovered := recover(); recovered != nil {
			if rollbackTransaction(tx) != nil {
				s.discardUncertainConnection(conn, backend)
			}
			panic(recovered)
		}
	}()
	backend, err = s.backendForWrite(ctx, tx)
	if err != nil {
		_ = rollbackTransaction(tx)
		return err, false
	}
	return s.executeTransaction(ctx, conn, tx, backend, fn)
}

type backendConnection interface {
	activityQuery
	Conn() *pgx.Conn
}

func (s *Store) backendForWrite(ctx context.Context, conn backendConnection) (databaseBackend, error) {
	var backend databaseBackend
	if s.budget == nil || !s.postgres || !s.directPostgres {
		return backend, nil
	}
	err := conn.QueryRow(ctx, `SELECT pg_backend_pid(),COALESCE(inet_server_addr()::TEXT,''),COALESCE(inet_server_port(),0)`).Scan(&backend.pid, &backend.host, &backend.port)
	if err != nil {
		return backend, err
	}
	if backend.pid <= 0 || uint32(backend.pid) != conn.Conn().PgConn().PID() || backend.host == "" || backend.port <= 0 {
		return backend, fmt.Errorf("direct PostgreSQL backend identity is unavailable or mismatched")
	}
	return backend, nil
}

func rollbackTransaction(tx pgx.Tx) error {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), storageCheckTimeout)
	defer cancel()
	return tx.Rollback(rollbackCtx)
}

// An uncertain connection must not return to the pool before reconciliation.
func (s *Store) discardUncertainConnection(conn *pgxpool.Conn, backend databaseBackend) {
	s.markUncertainWrite(backend)
	closeCtx, cancel := context.WithTimeout(context.Background(), storageCheckTimeout)
	_ = conn.Conn().Close(closeCtx)
	cancel()
}

func (s *Store) executeTransaction(ctx context.Context, conn *pgxpool.Conn, tx pgx.Tx, backend databaseBackend, fn func(pgx.Tx) error) (error, bool) {
	err := fn(tx)
	if err != nil {
		if rollbackTransaction(tx) != nil {
			s.discardUncertainConnection(conn, backend)
			return err, true
		}
		return err, false
	}
	err = tx.Commit(ctx)
	if err != nil {
		var pg *pgconn.PgError
		// A server error followed by ReadyForQuery in the idle state proves
		// the transaction ended; transport errors have no such guarantee.
		if !errors.As(err, &pg) || conn.Conn().PgConn().TxStatus() != 'I' {
			s.discardUncertainConnection(conn, backend)
			return err, true
		}
	}
	return err, false
}
func encodeCache(v any) ([]byte, error) {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	var err error
	switch data := v.(type) {
	case *StaticData:
		err = writeStaticCacheJSON(z, data)
	case StaticData:
		err = writeStaticCacheJSON(z, &data)
	default:
		err = json.NewEncoder(z).Encode(v)
	}
	if err != nil {
		return nil, err
	}
	if e := z.Close(); e != nil {
		return nil, e
	}
	return b.Bytes(), nil
}
func (s *Store) loadCache(ctx context.Context, p, kind string, dst any) (bool, error) {
	rows, err := s.DB.Query(ctx, "SELECT data FROM cache_parts WHERE operator_id=$1 AND kind=$2 ORDER BY part", p, kind)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var buffer bytes.Buffer
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return false, err
		}
		if buffer.Len()+len(data) > maxGTFSCompressedBytes {
			return false, fmt.Errorf("stored cache size limit")
		}
		buffer.Write(data)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if buffer.Len() == 0 {
		return false, nil
	}
	archive, err := gzip.NewReader(&buffer)
	if err != nil {
		return false, err
	}
	defer archive.Close()
	err = decodeStoredCacheJSON(io.LimitReader(archive, maxGTFSExpandedBytes), dst)
	return err == nil, err
}
func writeCache(ctx context.Context, tx pgx.Tx, p, kind string, blob []byte) error {
	parts := (len(blob) + cachePartBytes - 1) / cachePartBytes
	if _, e := tx.Exec(ctx, "DELETE FROM cache_parts WHERE operator_id=$1 AND kind=$2 AND part >= $3", p, kind, parts); e != nil {
		return e
	}
	for part, offset := 0, 0; offset < len(blob); part, offset = part+1, offset+cachePartBytes {
		end := offset + cachePartBytes
		if end > len(blob) {
			end = len(blob)
		}
		if _, e := tx.Exec(ctx, "INSERT INTO cache_parts(operator_id,kind,part,data) VALUES($1,$2,$3,$4) ON CONFLICT(operator_id,kind,part) DO UPDATE SET data=excluded.data WHERE cache_parts.data IS DISTINCT FROM excluded.data", p, kind, part, blob[offset:end]); e != nil {
			return e
		}
	}
	return nil
}

// Restore restores the last good provider data and health into the cache.
func (s *Store) Restore(ctx context.Context, c *Cache) error {
	for _, p := range providers {
		if err := s.restoreProvider(ctx, c, p); err != nil {
			return err
		}
	}
	var direct MetroData
	ok, err := s.loadCache(ctx, "metro", "direct", &direct)
	if err != nil {
		return err
	}
	if ok {
		c.updateMetro(&direct, c.operator("metro"))
	}
	return nil
}

// preparedPublication groups the position/history input with the registry whose
// exact pending versions participate in the same transaction.
type preparedPublication struct {
	Update    cacheUpdate
	Live      *LiveData
	Distances map[string]*float64
	Reporting *reportingRegistry
}

// Save commits a provider refresh and deduplicated observations atomically.
func (s *Store) Save(ctx context.Context, id string, static *StaticData, live *LiveData, op api.Operator, distances map[string]*float64) error {
	if s.DB == nil {
		return fmt.Errorf("durable storage unavailable")
	}
	update, err := prepareCacheUpdate(static, live, op)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	publication := preparedPublication{update, live, distances, &s.reporting}
	return s.savePreparedUpdate(ctx, id, publication)
}
func (s *Store) preparePendingUpdate(ctx context.Context, id string, update cacheUpdate, reporting *reportingRegistry) (cacheUpdate, error) {
	var err error
	update.Reporting, err = reporting.pending(id)
	if err != nil {
		return update, err
	}
	update.Facts, err = s.pendingFacts(ctx, id)
	return update, err
}

// savePreparedUpdate requires writeMu and historyMu, retaining one atomic acknowledgement boundary.
func (s *Store) savePreparedUpdate(ctx context.Context, id string, publication preparedPublication) error {
	update, err := s.preparePendingUpdate(ctx, id, publication.Update, publication.Reporting)
	if err != nil {
		return err
	}
	records, next := s.prepareHistory(id, publication.Live, publication.Distances)
	records, replacement, partial, err := s.guardUpdate(ctx, update, records)
	if err != nil {
		s.pauseHistoryCollection()
		return err
	}
	err = s.persistUpdate(ctx, id, update, records, replacement)
	if err != nil {
		s.pauseHistoryCollection()
		return err
	}
	s.acknowledgeUpdate(id, update, next)
	if replacement && !partial {
		// The transaction may have consumed the final credit. A failed check
		// changes status, but cannot undo or falsely reject the committed write.
		_ = s.refreshEmptyHistoryStatus(ctx)
	}
	return nil
}
func (s *Store) acknowledgeUpdate(id string, update cacheUpdate, next *historyCollector) {
	s.acknowledgeFacts(id, update.Facts)
	s.reporting.acknowledge(id, update.Reporting)
	if next != nil {
		s.collector = next
	}
}

func (s *Store) prune(ctx context.Context) error {
	_, err := s.pruneWithVacuumContext(ctx, ctx)
	return err
}

func (s *Store) pruneWithVacuumContext(ctx, vacuumParent context.Context) (pressureMaintenanceResult, error) {
	if err := s.writeMu.LockContext(ctx); err != nil {
		return pressureMaintenanceResult{reason: "write_lock_canceled"}, err
	}
	defer s.writeMu.Unlock()
	if s.pressureVacuum.Load() {
		return pressureMaintenanceResult{reason: "vacuum_in_progress"}, nil
	}
	if err := s.reconcileCompletedReservations(ctx); err != nil {
		return pressureMaintenanceResult{reason: "measurement_failed"}, err
	}
	if err := s.reserveStorage(ctx, cleanupReservation, maximumDatabaseBytes); err != nil {
		return pressureMaintenanceResult{reason: "cleanup_reservation_failed"}, err
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{"DELETE FROM stop_events WHERE (journey_id,stop_sequence,event_kind,generation) IN (SELECT journey_id,stop_sequence,event_kind,generation FROM stop_events WHERE event_at < $1 ORDER BY event_at LIMIT 10000)", "DELETE FROM snapshots WHERE (operator_id,vehicle_id,observed_at) IN (SELECT operator_id,vehicle_id,observed_at FROM snapshots WHERE observed_at < $1 ORDER BY observed_at LIMIT 10000)", "DELETE FROM sessions WHERE token_hash IN (SELECT token_hash FROM sessions WHERE expires_at < $1 LIMIT 10000)", "DELETE FROM api_keys WHERE id IN (SELECT id FROM api_keys WHERE expires_at < $1 LIMIT 10000)"} {
			cutoff := time.Now()
			if strings.HasPrefix(q, "DELETE FROM stop_events") {
				cutoff = cutoff.AddDate(0, 0, -s.retentionDays())
			}
			if q == "DELETE FROM snapshots WHERE (operator_id,vehicle_id,observed_at) IN (SELECT operator_id,vehicle_id,observed_at FROM snapshots WHERE observed_at < $1 ORDER BY observed_at LIMIT 10000)" {
				cutoff = cutoff.AddDate(0, 0, -s.retentionDays()).Add(-time.Hour)
			}
			if _, e := tx.Exec(ctx, q, cutoff); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return pressureMaintenanceResult{reason: "ordinary_cleanup_failed"}, err
	}
	return s.finishPressureMaintenance(ctx, vacuumParent)
}

func (s *Store) finishPressureMaintenance(ctx, vacuumParent context.Context) (pressureMaintenanceResult, error) {
	result, err := s.pressurePrune(ctx, vacuumParent)
	if err != nil || result.deleted > 0 {
		return result, err
	}
	if err := s.reconcileCompletedReservations(ctx); err != nil {
		return result, fmt.Errorf("pressure maintenance completed; measurement failed: %w", err)
	}
	return result, nil
}
func (s *Store) generation(ctx context.Context) (int64, error) {
	var n int64
	e := s.DB.QueryRow(ctx, "SELECT generation FROM app_state WHERE id=1").Scan(&n)
	return n, e
}

func (s *Store) restoreProvider(ctx context.Context, c *Cache, p provider) error {
	var static StaticData
	var live LiveData
	var ds *StaticData
	var dl *LiveData
	ok, e := s.loadCache(ctx, p.ID, "static", &static)
	if e != nil {
		return e
	}
	if ok {
		ds = &static
	}
	ok, e = s.loadCache(ctx, p.ID, "live", &live)
	if e != nil {
		return e
	}
	if ok {
		dl = restoreLive(&live, time.Now().UTC())
	}
	return s.restoreProviderFacts(ctx, c, p, ds, dl)
}

func (s *Store) restoreOperator(ctx context.Context, id string, op *api.Operator) error {
	var data []byte
	err := s.DB.QueryRow(ctx, "SELECT payload FROM source_health WHERE operator_id=$1", id).Scan(&data)
	if err == nil {
		return json.Unmarshal(data, op)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func (s *Store) retentionDays() int {
	if s == nil || s.RetentionDays == 0 {
		return historyRetentionDays
	}
	return s.RetentionDays
}
