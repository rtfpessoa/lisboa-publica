package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"lisboapublica/internal/api"
)

const schema = `
CREATE TABLE IF NOT EXISTS app_state (id INT PRIMARY KEY, generation BIGINT NOT NULL);
INSERT INTO app_state (id,generation) VALUES (1,0) ON CONFLICT (id) DO NOTHING;
CREATE TABLE IF NOT EXISTS cache_parts (operator_id TEXT NOT NULL, kind TEXT NOT NULL, part INT NOT NULL, data BYTEA NOT NULL, PRIMARY KEY(operator_id,kind,part));
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
CREATE INDEX IF NOT EXISTS snapshots_time ON snapshots(observed_at,operator_id,generation);
CREATE INDEX IF NOT EXISTS snapshots_route ON snapshots(route_id,observed_at);
CREATE TABLE IF NOT EXISTS sessions (token_hash TEXT PRIMARY KEY, email TEXT NOT NULL, name TEXT NOT NULL, auth_kind TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, owner_email TEXT NOT NULL, name TEXT NOT NULL, token_hash TEXT UNIQUE NOT NULL, scopes TEXT[] NOT NULL, created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL, revoked BOOLEAN NOT NULL DEFAULT FALSE);
CREATE INDEX IF NOT EXISTS keys_owner ON api_keys(owner_email,created_at);
`

// Store persists cached feeds, observations, hashed sessions and scoped API keys.
type Store struct {
	DB              *pgxpool.Pool
	RetentionDays   int
	HistoryInterval time.Duration
	historyMu       sync.Mutex
	collector       *historyCollector
	budget          *storageBudget
	PublishMu       sync.Mutex
}

// OpenStore opens a database pool and applies compatible Postgres/Cockroach migrations.
func OpenStore(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = databaseMaxConnections
	cfg.ConnConfig.RuntimeParams["application_name"] = "lisboapublica"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	value := &Store{DB: pool}
	for _, q := range splitStatements(schema) {
		if _, err = pool.Exec(ctx, q); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return value, nil
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
		tx, e := s.DB.BeginTx(ctx, pgx.TxOptions{})
		if e != nil {
			return e
		}
		e = fn(tx)
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if e == nil {
			return nil
		}
		_ = tx.Rollback(ctx)
		var pg *pgconn.PgError
		if !errors.As(e, &pg) || pg.Code != "40001" {
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
func encodeCache(v any) ([]byte, error) {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if e := json.NewEncoder(z).Encode(v); e != nil {
		return nil, e
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
	err = json.NewDecoder(io.LimitReader(archive, maxGTFSExpandedBytes)).Decode(dst)
	return err == nil, err
}
func writeCache(ctx context.Context, tx pgx.Tx, p, kind string, blob []byte) error {
	if _, e := tx.Exec(ctx, "DELETE FROM cache_parts WHERE operator_id=$1 AND kind=$2", p, kind); e != nil {
		return e
	}
	for part, offset := 0, 0; offset < len(blob); part, offset = part+1, offset+cachePartBytes {
		end := offset + cachePartBytes
		if end > len(blob) {
			end = len(blob)
		}
		if _, e := tx.Exec(ctx, "INSERT INTO cache_parts(operator_id,kind,part,data) VALUES($1,$2,$3,$4)", p, kind, part, blob[offset:end]); e != nil {
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

// Save commits a provider refresh and deduplicated observations atomically.
func (s *Store) Save(ctx context.Context, id string, static *StaticData, live *LiveData, op api.Operator, distances map[string]*float64) error {
	update, err := prepareCacheUpdate(static, live, op)
	if err != nil {
		return err
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	records, next := s.prepareHistory(id, live, distances)
	records, err = s.guardUpdate(ctx, update, records)
	if err != nil {
		return err
	}
	err = s.persistUpdate(ctx, id, update, records)
	if err == nil && next != nil {
		s.collector = next
	}
	return err
}

func (s *Store) prune(ctx context.Context) error {
	if err := s.reserveStorage(ctx, cleanupReservation, maximumDatabaseBytes); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx pgx.Tx) error {
		for _, q := range []string{"DELETE FROM snapshots WHERE (operator_id,vehicle_id,observed_at) IN (SELECT operator_id,vehicle_id,observed_at FROM snapshots WHERE observed_at < $1 ORDER BY observed_at LIMIT 10000)", "DELETE FROM sessions WHERE token_hash IN (SELECT token_hash FROM sessions WHERE expires_at < $1 LIMIT 10000)", "DELETE FROM api_keys WHERE id IN (SELECT id FROM api_keys WHERE expires_at < $1 LIMIT 10000)"} {
			cutoff := time.Now()
			if q == "DELETE FROM snapshots WHERE (operator_id,vehicle_id,observed_at) IN (SELECT operator_id,vehicle_id,observed_at FROM snapshots WHERE observed_at < $1 ORDER BY observed_at LIMIT 10000)" {
				cutoff = cutoff.AddDate(0, 0, -s.retentionDays()).Add(-time.Hour)
			}
			if _, e := tx.Exec(ctx, q, cutoff); e != nil {
				return e
			}
		}
		return nil
	})
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
		dl = &live
	}
	op := c.operator(p.ID)
	var data []byte
	e = s.DB.QueryRow(ctx, "SELECT payload FROM source_health WHERE operator_id=$1", p.ID).Scan(&data)
	if e == nil {
		if e = json.Unmarshal(data, &op); e != nil {
			return e
		}
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if ds != nil || dl != nil {
		c.update(p.ID, ds, dl, op)
	}
	return nil
}

func (s *Store) retentionDays() int {
	if s == nil || s.RetentionDays == 0 {
		return historyRetentionDays
	}
	return s.RetentionDays
}
