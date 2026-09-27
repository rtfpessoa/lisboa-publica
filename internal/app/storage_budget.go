package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maximumDatabaseBytes     int64 = 5_000_000_000
	historyDatabaseBytes     int64 = 4_000_000_000
	operationalDatabaseBytes int64 = 4_500_000_000
	maximumWriteBytes        int64 = 128 << 20
	storageCheckInterval           = time.Minute
	storageCheckTimeout            = 10 * time.Second
	storageWriteOverhead     int64 = 4
	historyRecordOverhead    int64 = 1024
	cleanupReservation       int64 = 8 << 20
)

type storageBudget struct {
	mu              sync.Mutex
	measure         func(context.Context) (int64, error)
	now             func() time.Time
	checked         time.Time
	bytes, reserved int64
	state           string
}

func (b *storageBudget) reserve(ctx context.Context, bytes, ceiling int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.refresh(ctx); err != nil {
		return err
	}
	return b.claim(bytes, ceiling)
}

func (b *storageBudget) claim(bytes, ceiling int64) error {
	if bytes > maximumWriteBytes || bytes < 0 || b.bytes+b.reserved+bytes > ceiling {
		b.state = "paused"
		return fmt.Errorf("storage budget exhausted; collection paused")
	}
	b.reserved += bytes
	if ceiling == historyDatabaseBytes {
		b.state = "collecting"
	}
	return nil
}

func (b *storageBudget) refresh(ctx context.Context) error {
	if !b.checked.IsZero() && b.now().Sub(b.checked) < storageCheckInterval {
		if b.state == "unavailable" {
			return fmt.Errorf("storage measurement unavailable")
		}
		return nil
	}
	check, cancel := context.WithTimeout(ctx, storageCheckTimeout)
	defer cancel()
	bytes, err := b.measure(check)
	b.checked = b.now()
	if err != nil || bytes < 0 {
		b.state = "unavailable"
		return fmt.Errorf("storage measurement unavailable")
	}
	b.bytes, b.reserved = bytes, 0
	b.state = "collecting"
	if bytes >= historyDatabaseBytes {
		b.state = "paused"
	}
	return nil
}

func (b *storageBudget) status() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (s *Store) databaseBytes(ctx context.Context) (int64, error) {
	var version string
	if err := s.DB.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		return 0, err
	}
	query := "SELECT pg_database_size(current_database())"
	if strings.Contains(version, "CockroachDB") {
		query = "SELECT COALESCE(sum(range_size),0)::INT8 FROM [SHOW RANGES FROM CURRENT_CATALOG WITH TABLES, DETAILS]"
	}
	var bytes int64
	err := s.DB.QueryRow(ctx, query).Scan(&bytes)
	return bytes, err
}

// ConfigureHistory sets retention, historical resolution and the optional five-GB guard before collection starts.
func (s *Store) ConfigureHistory(days int, interval time.Duration, guard bool) error {
	if days < 1 || days > historyRetentionDays {
		return fmt.Errorf("history retention must be between 1 and 30 days")
	}
	if interval != 0 && interval != staticRefreshInterval {
		return fmt.Errorf("historical interval must be 0 or 300 seconds")
	}
	s.RetentionDays, s.HistoryInterval = days, interval
	s.collector = newHistoryCollector()
	if !guard {
		s.budget = nil
	}
	if guard && s.budget == nil {
		s.budget = &storageBudget{measure: s.databaseBytes, now: time.Now, state: "unavailable"}
	}
	return nil
}

func (s *Store) reserveStorage(ctx context.Context, bytes, ceiling int64) error {
	if s.budget == nil {
		return nil
	}
	return s.budget.reserve(ctx, bytes, ceiling)
}

func (s *Store) historyStatus() string {
	if s.budget == nil {
		return "collecting"
	}
	return s.budget.status()
}

func (s *Store) storageLimit() *int64 {
	if s.budget == nil {
		return nil
	}
	return ptr(maximumDatabaseBytes)
}

// exec bounds credential issuance within the operational budget.
func (s *Store) exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.execLocked(ctx, operationalDatabaseBytes, query, args...)
}

// execCleanup reserves existing cleanup headroom for credential invalidation only.
func (s *Store) execCleanup(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.execLocked(ctx, maximumDatabaseBytes, query, args...)
}

// execLocked requires writeMu, allowing quota checks and issuance to share the lock.
func (s *Store) execLocked(ctx context.Context, ceiling int64, query string, args ...any) (pgconn.CommandTag, error) {
	bytes := int64(len(query))
	for _, arg := range args {
		bytes += int64(len(fmt.Sprint(arg)))
	}
	if err := s.reserveStorage(ctx, bytes*storageWriteOverhead+historyRecordOverhead, ceiling); err != nil {
		return pgconn.CommandTag{}, err
	}
	return s.DB.Exec(ctx, query, args...)
}

// reserveMigrations rejects unbounded existing-data backfills before any DDL runs.
func (s *Store) reserveMigrations(ctx context.Context) error {
	var complete bool
	err := s.DB.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='snapshots' AND column_name IN ('first_observed_at','speed_sample_count'))=2
 AND (SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('snapshots_time','snapshots_route','keys_owner'))=3`).Scan(&complete)
	if err != nil {
		return err
	}
	bytes := cleanupReservation
	if !complete {
		s.budget.mu.Lock()
		err := s.budget.refresh(ctx)
		existing := s.budget.bytes
		s.budget.mu.Unlock()
		if err != nil {
			return err
		}
		bytes += existing * storageWriteOverhead
	}
	return s.reserveStorage(ctx, bytes, operationalDatabaseBytes)
}

// reserveUpdate shares one measurement across the cache and history portions of a transaction.
func (b *storageBudget) reserveUpdate(ctx context.Context, operations, history int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.refresh(ctx); err != nil {
		return false, err
	}
	if err := b.claim(operations, operationalDatabaseBytes); err != nil {
		return false, err
	}
	if operations+history > maximumWriteBytes {
		b.state = "paused"
		return false, nil
	}
	return b.claim(history, historyDatabaseBytes) == nil, nil
}

func (s *Store) guardUpdate(ctx context.Context, update cacheUpdate, records []historicalRecord) ([]historicalRecord, error) {
	operations := int64(len(update.Static)+len(update.Live)+len(update.Health))*storageWriteOverhead + historyRecordOverhead
	for _, fact := range update.Facts {
		operations += int64(len(fact.SourceID)+len(fact.Payload))*storageWriteOverhead + historyRecordOverhead
	}
	historyBytes := recordBytes(records)
	if s.budget == nil {
		if operations+historyBytes > maximumWriteBytes {
			records = nil
		}
		return records, nil
	}
	keep, err := s.budget.reserveUpdate(ctx, operations, historyBytes)
	if err != nil {
		return nil, err
	}
	if !keep {
		records = nil
	}
	return records, nil
}
