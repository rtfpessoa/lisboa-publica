package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var errStorageBudgetExhausted = errors.New("storage budget exhausted; collection paused")
var errUncertainStorageWrite = errors.New("database write outcome uncertain; maintenance awaits backend termination")

const (
	maximumDatabaseBytes     int64 = 5_000_000_000
	historyDatabaseBytes     int64 = 4_000_000_000
	pressureDatabaseBytes    int64 = 4_200_000_000
	operationalDatabaseBytes int64 = 4_500_000_000
	maximumWriteBytes        int64 = 128 << 20
	storageCheckInterval           = time.Minute
	storageCheckTimeout            = 10 * time.Second
	storageWriteOverhead     int64 = 4
	historyRecordOverhead    int64 = 1024
	cleanupReservation       int64 = 8 << 20
)

type storageBudget struct {
	mu                sync.Mutex
	measure           func(context.Context) (int64, error)
	now               func() time.Time
	checked           time.Time
	bytes, reserved   int64
	state             string
	pressureReady     bool
	filesystem        *filesystemProbe
	vacuumActive      *atomic.Bool
	uncertainBackends map[databaseBackend]struct{}
	uncertainUnknown  bool
	rejectedBatches   int64
	partialBatches    int64
	lastRejection     historyAdmissionRejection
}

type historyAdmissionRejection struct {
	reason                   string
	bytes, reserved, history int64
	replacement              bool
}

// setHistoryState requires budget.mu. Any non-collecting state invalidates
// the previously verified replacement-history readiness.
func (b *storageBudget) setHistoryState(state string) {
	b.state = state
	if state != "collecting" {
		b.pressureReady = false
	}
}

func (b *storageBudget) setOrdinaryCollecting() {
	b.state = "collecting"
	b.pressureReady = false
}

type databaseBackend struct {
	pid  int32
	host string
	port int32
}

type activityQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// An operator-verified direct connection is required: proxy-assigned PIDs or
// activity rows from a different server cannot prove backend termination.
func readBackendActivity(ctx context.Context, conn activityQuery, physicalPID uint32, target int32) (databaseBackend, bool, bool, error) {
	var current databaseBackend
	var ownVisible, targetPresent, otherPresent bool
	err := conn.QueryRow(ctx, `SELECT pg_backend_pid(),COALESCE(inet_server_addr()::TEXT,''),COALESCE(inet_server_port(),0),
		EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=pg_backend_pid() AND usename=current_user AND datname=current_database() AND state='active'),
		EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND pid<>pg_backend_pid()),
		EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE (datname=current_database() OR datname IS NULL) AND (usename=current_user OR usename IS NULL) AND (backend_type='client backend' OR backend_type IS NULL) AND pid<>pg_backend_pid())`, target).
		Scan(&current.pid, &current.host, &current.port, &ownVisible, &targetPresent, &otherPresent)
	if err != nil || current.pid <= 0 || uint32(current.pid) != physicalPID || current.host == "" || current.port <= 0 || !ownVisible {
		return databaseBackend{}, false, false, errUncertainStorageWrite
	}
	return current, targetPresent, otherPresent, nil
}

// A newly started process has no in-memory reservations for work still
// executing on connections from its predecessor. Refuse even migrations until
// same-role client sessions, including idle sessions, have disappeared. This
// relies on the documented single-owner and dedicated-database requirements;
// an activity snapshot is not a distributed fencing or write-ahead protocol.
func (s *Store) ensurePriorBackendsSettled(ctx context.Context) error {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return errUncertainStorageWrite
	}
	defer conn.Release()
	_, _, present, err := readBackendActivity(ctx, conn, conn.Conn().PgConn().PID(), 0)
	if err != nil || present {
		return fmt.Errorf("prior same-role PostgreSQL sessions remain or cannot be verified: %w", errUncertainStorageWrite)
	}
	return nil
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
	if err := b.checkFilesystem(bytes); err != nil {
		return err
	}
	if bytes > maximumWriteBytes || bytes < 0 || b.bytes+b.reserved+bytes > ceiling {
		b.setHistoryState("paused")
		return errStorageBudgetExhausted
	}
	b.reserved += bytes
	if ceiling == historyDatabaseBytes {
		b.setOrdinaryCollecting()
	} else if err := b.retainPressureReadiness(bytes); err != nil {
		return err
	}
	return nil
}

// retainPressureReadiness accounts for the current uncommitted operation as
// well as database reservations. Known history-only shortages may still allow
// the operation itself; unknown filesystem state fails closed.
func (b *storageBudget) retainPressureReadiness(operationBytes int64) error {
	projected := b.bytes + b.reserved + historyRecordOverhead
	if projected <= historyDatabaseBytes {
		b.pressureReady = false
		return nil
	}
	if !b.pressureReady {
		if b.state == "collecting" {
			b.setHistoryState("paused")
		}
		return nil
	}
	if b.vacuumActive != nil && b.vacuumActive.Load() {
		b.setHistoryState("paused")
		return nil
	}
	if projected > pressureDatabaseBytes {
		b.setHistoryState("paused")
		return nil
	}
	_, err := b.historyReadinessFilesystem(operationBytes)
	return err
}

func (b *storageBudget) refresh(ctx context.Context) error {
	if b.uncertainUnknown || len(b.uncertainBackends) != 0 {
		b.setHistoryState("unavailable")
		return errUncertainStorageWrite
	}
	if err := b.checkFilesystem(0); err != nil {
		return err
	}
	if !b.checked.IsZero() && b.now().Sub(b.checked) < storageCheckInterval {
		if b.state == "unavailable" {
			return fmt.Errorf("storage measurement unavailable")
		}
		return nil
	}
	return b.refreshMeasurement(ctx)
}

func (b *storageBudget) refreshMeasurement(ctx context.Context) error {
	check, cancel := context.WithTimeout(ctx, storageCheckTimeout)
	defer cancel()
	bytes, err := b.measure(check)
	b.checked = b.now()
	if err != nil || bytes < 0 {
		b.setHistoryState("unavailable")
		return fmt.Errorf("storage measurement unavailable")
	}
	b.bytes = bytes
	if b.vacuumActive == nil || !b.vacuumActive.Load() {
		b.reserved = 0
	}
	return b.updateMeasuredHistoryState()
}

// updateMeasuredHistoryState does not query application tables: measurements
// also run before migrations. Only a verified pressure state can survive a
// new pressure-range measurement.
func (b *storageBudget) updateMeasuredHistoryState() error {
	if b.bytes+b.reserved+historyRecordOverhead <= historyDatabaseBytes {
		b.setOrdinaryCollecting()
		return nil
	}
	if b.pressureReady && b.state == "collecting" && (b.vacuumActive == nil || !b.vacuumActive.Load()) && b.bytes+b.reserved+historyRecordOverhead <= pressureDatabaseBytes {
		ready, err := b.historyReadinessFilesystem(0)
		if err != nil || !ready {
			return err
		}
		return nil
	}
	b.setHistoryState("paused")
	return nil
}

// measureFresh keeps outstanding reservations while checking the current
// physical database size for a replacement-history admission.
func (b *storageBudget) measureFresh(ctx context.Context) error {
	if err := b.measureCurrent(ctx); err != nil {
		return err
	}
	return b.retainPressureReadiness(0)
}

func (b *storageBudget) measureCurrent(ctx context.Context) error {
	if b.uncertainUnknown || len(b.uncertainBackends) != 0 {
		b.setHistoryState("unavailable")
		return errUncertainStorageWrite
	}
	if err := b.checkFilesystem(0); err != nil {
		return err
	}
	check, cancel := context.WithTimeout(ctx, storageCheckTimeout)
	defer cancel()
	bytes, err := b.measure(check)
	if err != nil || bytes < 0 {
		b.setHistoryState("unavailable")
		return fmt.Errorf("storage measurement unavailable")
	}
	b.bytes = bytes
	return nil
}

func (b *storageBudget) checkFilesystem(required int64) error {
	if b.vacuumActive != nil && b.vacuumActive.Load() && required > 0 {
		// The ordinary writer and an asynchronous pressure vacuum can both
		// grow database files. Keep their estimated headroom separate.
		if required > int64(^uint64(0)>>1)-cleanupReservation {
			b.setHistoryState("unavailable")
			return errFilesystemHeadroom
		}
		required += cleanupReservation
	}
	if err := b.filesystem.check(required); err != nil {
		b.setHistoryState("unavailable")
		return err
	}
	return nil
}

// reconcileCompletedReservations requires Store.writeMu. All application
// transactions holding reservations have completed when that lock is acquired.
// An asynchronous pressure vacuum is the only exception, so do not reconcile
// until it has finished and its pending-state update has released writeMu.
func (s *Store) reconcileCompletedReservations(ctx context.Context) error {
	if s.budget == nil || s.pressureVacuum.Load() {
		return nil
	}
	// A proxy/unknown backend cannot prove that an earlier operation has
	// finished. Preserve the pre-existing periodic guard behavior without
	// adding extra reservation resets for those deployments.
	if !s.postgres || !s.directPostgres {
		return nil
	}
	if err := s.resolveUncertainBackends(ctx); err != nil {
		return err
	}
	b := s.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.measureCurrent(ctx); err != nil {
		return err
	}
	b.reserved = 0
	b.checked = b.now()
	return b.updateMeasuredHistoryState()
}

// markUncertainWrite retains all current reservations when an interrupted
// transaction may still be executing or committing on the database server.
func (s *Store) markUncertainWrite(backend databaseBackend) {
	if s.budget == nil {
		return
	}
	b := s.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if backend.pid <= 0 || backend.host == "" || backend.port <= 0 {
		b.uncertainUnknown = true
	} else {
		if b.uncertainBackends == nil {
			b.uncertainBackends = make(map[databaseBackend]struct{})
		}
		b.uncertainBackends[backend] = struct{}{}
	}
	b.setHistoryState("unavailable")
}

// resolveUncertainBackends requires writeMu and never releases reservations
// while a possibly interrupted PostgreSQL backend remains present, unless the
// check itself reuses the same serialized physical connection.
func (s *Store) resolveUncertainBackends(ctx context.Context) error {
	b := s.budget
	ids, err := b.uncertainBackendList()
	if err != nil {
		return err
	}
	if len(ids) > 0 && (!s.postgres || !s.directPostgres) {
		return errUncertainStorageWrite
	}
	for _, backend := range ids {
		if !s.backendEnded(ctx, backend) {
			return errUncertainStorageWrite
		}
		b.mu.Lock()
		delete(b.uncertainBackends, backend)
		b.mu.Unlock()
	}
	return nil
}

func (b *storageBudget) uncertainBackendList() ([]databaseBackend, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.uncertainUnknown {
		return nil, errUncertainStorageWrite
	}
	ids := make([]databaseBackend, 0, len(b.uncertainBackends))
	for backend := range b.uncertainBackends {
		ids = append(ids, backend)
	}
	return ids, nil
}

func (s *Store) backendEnded(ctx context.Context, backend databaseBackend) bool {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return false
	}
	defer conn.Release()
	current, present, _, err := readBackendActivity(ctx, conn, conn.Conn().PgConn().PID(), backend.pid)
	return err == nil && !present && current.host == backend.host && current.port == backend.port
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
		s.budget = &storageBudget{measure: s.databaseBytes, now: time.Now, state: "unavailable", vacuumActive: &s.pressureVacuum}
	} else if guard {
		s.budget.mu.Lock()
		s.budget.pressureReady = false
		if s.budget.bytes+s.budget.reserved+historyRecordOverhead > historyDatabaseBytes && s.budget.state == "collecting" {
			s.budget.setHistoryState("paused")
		}
		s.budget.mu.Unlock()
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
	var tag pgconn.CommandTag
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		tag, err = tx.Exec(ctx, query, args...)
		return err
	})
	return tag, err
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
	return b.reserveUpdatePressure(ctx, operations, history, false)
}

type updateReservation struct {
	operations, history int64
	replacement         bool
}

type historyAdmissionOutcome struct {
	updateReservation
	partial, rejected bool
}

func (b *storageBudget) reserveUpdatePressure(ctx context.Context, operations, history int64, replacement bool) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.refresh(ctx); err != nil {
		return false, err
	}
	if replacement {
		if err := b.measureFresh(ctx); err != nil {
			return false, err
		}
	}
	reservation := updateReservation{operations: operations, history: history, replacement: replacement}
	admitHistory, err := b.preflightHistory(reservation)
	if err != nil {
		return false, err
	}
	if err := b.claim(operations, operationalDatabaseBytes); err != nil {
		return false, err
	}
	if !admitHistory {
		b.setHistoryState("paused")
		return false, nil
	}
	return b.reserveAdmittedHistory(reservation)
}

func (b *storageBudget) historyFitsBudget(reservation updateReservation) bool {
	// Decide logical history admission first: when history must be dropped,
	// operations alone should not be charged for filesystem space it will not use.
	operations, history := reservation.operations, reservation.history
	admitHistory := operations >= 0 && history >= 0 && operations <= maximumWriteBytes && history <= maximumWriteBytes-operations
	if admitHistory && history > 0 {
		ceiling := historyDatabaseBytes
		projected := b.bytes + b.reserved + operations + history
		if reservation.replacement && projected > historyDatabaseBytes {
			ceiling = pressureDatabaseBytes
		}
		admitHistory = projected <= ceiling
	}
	return admitHistory
}

func (b *storageBudget) preflightHistory(reservation updateReservation) (bool, error) {
	if !b.historyFitsBudget(reservation) {
		return false, nil
	}
	// Cache and admitted history share one transaction; testing their two
	// allocations separately would consume the filesystem reserve.
	if err := b.checkFilesystem(reservation.operations + reservation.history); err != nil {
		if reservation.history > 0 && errors.Is(err, errFilesystemHeadroom) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (b *storageBudget) reserveAdmittedHistory(reservation updateReservation) (bool, error) {
	keep, err := b.reserveHistoryUpdate(reservation.history, reservation.replacement)
	if err != nil {
		return b.fallbackToOperations(reservation, err)
	}
	if !keep {
		return false, nil
	}
	// Each claim remeasures the filesystem. Check the combined transaction
	// once more after those measurements so a later low reading cannot admit
	// both writes just because each would fit separately.
	if err := b.checkFilesystem(reservation.operations + reservation.history); err != nil {
		b.reserved -= reservation.history
		return b.fallbackToOperations(reservation, err)
	}
	if err := b.retainPressureReadiness(reservation.operations + reservation.history); err != nil {
		return false, err
	}
	return true, nil
}

func (b *storageBudget) fallbackToOperations(reservation updateReservation, cause error) (bool, error) {
	if reservation.history == 0 || !errors.Is(cause, errFilesystemHeadroom) {
		return false, cause
	}
	if err := b.checkFilesystem(reservation.operations); err != nil {
		return false, err
	}
	b.setHistoryState("paused")
	return false, nil
}

// reserveHistoryUpdate decides history admission after operational bytes are reserved.
func (b *storageBudget) reserveHistoryUpdate(history int64, replacement bool) (bool, error) {
	if history == 0 {
		return true, nil
	}
	ceiling := historyDatabaseBytes
	if replacement && b.bytes+b.reserved+history > historyDatabaseBytes {
		ceiling = pressureDatabaseBytes
	}
	err := b.claim(history, ceiling)
	if err != nil && !errors.Is(err, errStorageBudgetExhausted) {
		return false, err
	}
	return err == nil, nil
}

func (s *Store) guardUpdate(ctx context.Context, update cacheUpdate, records []historicalRecord) ([]historicalRecord, bool, bool, error) {
	operations := cacheUpdateBytes(update)
	historyBytes := recordBytes(records)
	if s.budget == nil {
		if operations+historyBytes > maximumWriteBytes {
			records = nil
		}
		return records, false, false, nil
	}
	if err := s.reconcileUpdatePressure(ctx, updateReservation{operations: operations, history: historyBytes}); err != nil {
		return nil, false, false, err
	}
	if len(records) == 0 {
		return s.guardEmptyUpdate(ctx, operations)
	}
	records, replacement, partial, err := s.selectUpdateHistory(ctx, records, operations, historyBytes)
	if err != nil {
		return nil, false, false, err
	}
	if replacement {
		historyBytes = recordBytes(records)
	}
	keep, err := s.budget.reserveUpdatePressure(ctx, operations, historyBytes, replacement)
	if err != nil {
		return nil, false, false, err
	}
	if !keep || partial {
		s.budget.noteHistoryAdmission(historyAdmissionOutcome{updateReservation: updateReservation{operations: operations, history: historyBytes, replacement: replacement}, partial: partial, rejected: !keep})
	}
	if !keep {
		records = nil
		replacement = false
	}
	if partial {
		s.pauseHistoryCollection()
	}
	return records, replacement, partial, nil
}

// The current update has not claimed its operational or history reservation.
func (s *Store) reconcileUpdatePressure(ctx context.Context, pending updateReservation) error {
	b := s.budget
	b.mu.Lock()
	nearLimit := b.reserved > 0 && b.bytes+b.reserved+pending.operations+max(pending.history, historyRecordOverhead) >= historyDatabaseBytes
	b.mu.Unlock()
	if nearLimit {
		return s.reconcileCompletedReservations(ctx)
	}
	return nil
}

// noteHistoryAdmission records only bounded aggregate diagnostic state. The
// operation reservation has already been claimed when history is rejected.
func (b *storageBudget) noteHistoryAdmission(outcome historyAdmissionOutcome) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if outcome.partial {
		b.partialBatches++
	}
	if !outcome.rejected {
		return
	}
	b.rejectedBatches++
	ceiling := historyDatabaseBytes
	if outcome.replacement {
		ceiling = pressureDatabaseBytes
	}
	operations, history := outcome.operations, outcome.history
	reason := "filesystem_headroom"
	if operations < 0 || history < 0 || operations > maximumWriteBytes || history > maximumWriteBytes-operations {
		reason = "write_size"
	} else if b.bytes+b.reserved+history > ceiling {
		reason = "database_headroom"
	}
	b.lastRejection = historyAdmissionRejection{reason, b.bytes, b.reserved, history, outcome.replacement}
}

func cacheUpdateBytes(update cacheUpdate) int64 {
	operations := int64(len(update.Static)+len(update.Live)+len(update.Health))*storageWriteOverhead + historyRecordOverhead
	for _, fact := range update.Facts {
		operations += int64(len(fact.SourceID)+len(fact.Payload))*storageWriteOverhead + historyRecordOverhead
	}
	for _, row := range update.Reporting {
		operations += int64(len(row.SourceID)+len(row.Payload))*storageWriteOverhead + historyRecordOverhead
	}
	return operations
}

func (s *Store) selectUpdateHistory(ctx context.Context, records []historicalRecord, operations, historyBytes int64) ([]historicalRecord, bool, bool, error) {
	s.budget.mu.Lock()
	err := s.budget.refresh(ctx)
	needReplacement := err == nil && s.budget.bytes+s.budget.reserved+operations+historyBytes > historyDatabaseBytes
	s.budget.mu.Unlock()
	if err != nil {
		return nil, false, false, err
	}
	if !needReplacement || len(records) == 0 {
		return records, false, false, nil
	}
	selected, err := s.replacementRecords(ctx, records)
	if err != nil {
		return nil, false, false, err
	}
	if len(selected) == 0 {
		return records, false, false, nil
	}
	return selected, true, len(selected) < len(records), nil
}

func (s *Store) guardEmptyUpdate(ctx context.Context, operations int64) ([]historicalRecord, bool, bool, error) {
	keep, err := s.budget.reserveUpdatePressure(ctx, operations, 0, false)
	if err != nil || !keep {
		return nil, false, false, err
	}
	err = s.refreshEmptyHistoryStatusWithPending(ctx, operations)
	return nil, false, false, err
}
