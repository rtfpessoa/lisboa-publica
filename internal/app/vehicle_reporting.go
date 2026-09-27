package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"lisboapublica/internal/api"
)

const maxReportingIdentities = 8192
const reportingObservationAge = 180 * time.Second

// Reporting describes publication membership, never physical movement or service activity.
// reportingLookup supplies durable identity reads while the registry owns mutable state.
type reportingLookup struct {
	Context context.Context
	Read    func(context.Context, string, []string) (map[string]reportingRecord, error)
}

type reportingRecord struct {
	Value        api.ReportingState `json:"value"`
	CollectionAt time.Time          `json:"collection_at"`
	Present      bool               `json:"present"`
	UpdatedAt    time.Time          `json:"updated_at"`
}
type reportingEntry struct {
	Record    reportingRecord
	Loaded    bool
	Dirty     bool
	Immediate bool
	Version   uint64
	Used      uint64
}
type reportingRegistry struct {
	mu        sync.Mutex
	Entries   map[string]*reportingEntry
	Clock     uint64
	Cursor    string
	NextSweep time.Time
}
type reportingWrite struct {
	SourceID string
	Record   reportingRecord
	Payload  []byte
	Version  uint64
}

func reportingClassification(record reportingRecord, op api.Operator, unverified bool, now time.Time) (api.ReportingStateState, api.ReportingStateReason) {
	state, reason := api.ReportingStateState("unknown"), api.ReportingStateReason("source_unverified")
	if !unverified && !record.CollectionAt.IsZero() {
		state, reason = verifiedReportingClassification(record, op, now)
	}
	return state, reason
}
func verifiedReportingClassification(record reportingRecord, op api.Operator, now time.Time) (api.ReportingStateState, api.ReportingStateReason) {
	state, reason := membershipReportingClassification(record, now)
	if now.Sub(record.CollectionAt) > sourceFreshness {
		state, reason = "unknown", "collection_old"
	}
	if op.Error != nil || op.Status == api.OperatorStatusError {
		state, reason = "unknown", "source_error"
	}
	return state, reason
}
func membershipReportingClassification(record reportingRecord, now time.Time) (api.ReportingStateState, api.ReportingStateReason) {
	state, reason := api.ReportingStateState("reporting"), api.ReportingStateReason("current")
	if record.Value.LastObservedAt == nil || now.Sub(*record.Value.LastObservedAt) > reportingObservationAge {
		state, reason = "not_reporting", "observation_old"
	}
	if !record.Present {
		state, reason = "not_reporting", "missing_from_snapshot"
	}
	return state, reason
}

func (e *reportingEntry) classify(op api.Operator, unverified bool, now time.Time) bool {
	state, reason := reportingClassification(e.Record, op, unverified, now)
	if e.Record.Value.State == state && e.Record.Value.Reason == reason {
		return false
	}
	e.Record.Value.State, e.Record.Value.Reason = state, reason
	e.Record.Value.StateChangedAt = now
	e.changed(now)
	e.Immediate = true
	return true
}
func (e *reportingEntry) changed(now time.Time) {
	now = now.UTC().Truncate(time.Microsecond)
	if !now.After(e.Record.UpdatedAt) {
		now = e.Record.UpdatedAt.Add(time.Microsecond)
	}
	e.Record.UpdatedAt = now
	e.Record.Value.Persisted = false
	e.Version++
	e.Dirty = true
}
func (r *reportingRegistry) entry(operator, source string) (*reportingEntry, error) {
	if r.Entries == nil {
		r.Entries = map[string]*reportingEntry{}
	}
	key := factKey(operator, source)
	if e := r.Entries[key]; e != nil {
		r.Clock++
		e.Used = r.Clock
		return e, nil
	}
	r.evict(maxReportingIdentities - 1)
	if len(r.Entries) >= maxReportingIdentities {
		return nil, fmt.Errorf("reporting identity capacity reached")
	}
	r.Clock++
	e := &reportingEntry{Used: r.Clock}
	r.Entries[key] = e
	return e, nil
}
func (r *reportingRegistry) evict(limit int) {
	if len(r.Entries) <= limit {
		return
	}
	type candidate struct {
		key  string
		used uint64
	}
	rows := []candidate{}
	for k, e := range r.Entries {
		if !e.Dirty {
			rows = append(rows, candidate{k, e.Used})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].used < rows[j].used })
	for _, row := range rows {
		if len(r.Entries) <= limit {
			break
		}
		delete(r.Entries, row.key)
	}
}

// Reporting rows are loaded by exact source identity, including identities returning after map expiry.
func (r *reportingRegistry) load(lookup reportingLookup, operator string, ids []string) error {
	missing := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		e, err := r.entry(operator, id)
		if err != nil {
			return err
		}
		if !e.Loaded {
			missing = append(missing, id)
		}
	}
	loaded, err := lookup.Read(lookup.Context, operator, missing)
	if err != nil {
		return err
	}
	for _, id := range missing {
		e, _ := r.entry(operator, id)
		e.Record = loaded[id]
		e.Loaded = true
		e.Record.Value.Persisted = true
	}
	return nil
}

func reportingIDs(live *LiveData) []string {
	ids := []string{}
	if live != nil {
		for _, v := range live.Vehicles {
			ids = append(ids, v.SourceId)
		}
		for _, v := range live.LastKnown {
			ids = append(ids, v.SourceId)
		}
	}
	return ids
}

// filterReportingRegressions extends replay protection beyond the expiring position ledger.
func (r *reportingRegistry) filterRegressions(lookup reportingLookup, operator string, rows []api.Vehicle) ([]api.Vehicle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := []string{}
	for _, v := range rows {
		ids = append(ids, v.SourceId)
	}
	loadErr := r.load(lookup, operator, ids)
	kept := make([]api.Vehicle, 0, len(rows))
	for _, v := range rows {
		e := r.Entries[factKey(operator, v.SourceId)]
		if e != nil && reportingObservationRegressed(e.Record, v) {
			continue
		}
		kept = append(kept, v)
	}
	return kept, loadErr
}

// stageReporting requires PublishMu. Every successful normalized snapshot supplies membership;
// identical observations advance last_seen_at without advancing last_observed_at.
func (r *reportingRegistry) stage(lookup reportingLookup, operator string, live *LiveData, op api.Operator, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	loadErr := r.load(lookup, operator, reportingIDs(live))
	r.seedObservations(operator, live, now)
	members := reportingMembers(live)
	transition := false
	prefix := operator + "\x00"
	for key, e := range r.Entries {
		if !strings.HasPrefix(key, prefix) || !e.Loaded {
			continue
		}
		source := strings.TrimPrefix(key, prefix)
		v, present := members[source]
		e.acceptMembership(live, v, present, now)
		unverified := live == nil || live.Unverified
		transition = e.classify(op, unverified, now) || transition
	}
	return transition, loadErr
}

// Legacy position caches still retain real original observation clocks after a schema upgrade.
// They do not verify current source membership, and never create historical samples.
func (r *reportingRegistry) seedObservations(operator string, live *LiveData, now time.Time) {
	if live == nil {
		return
	}
	seed := func(rows []api.Vehicle) {
		for _, v := range rows {
			e := r.Entries[factKey(operator, v.SourceId)]
			if e != nil && e.Loaded && e.Record.Value.LastObservedAt == nil {
				e.Record.Value.LastObservedAt = ptr(v.ObservedAt)
				e.changed(now)
			}
		}
	}
	seed(live.LastKnown)
	seed(live.Vehicles)
}

func reportingMembers(live *LiveData) map[string]api.Vehicle {
	members := map[string]api.Vehicle{}
	if live != nil {
		for _, v := range live.Vehicles {
			members[v.SourceId] = v
		}
	}
	return members
}
func (e *reportingEntry) acceptMembership(live *LiveData, v api.Vehicle, present bool, now time.Time) {
	if live == nil || live.Unverified {
		return
	}
	if live.Collected.Before(e.Record.CollectionAt) {
		return
	}
	if present && reportingObservationRegressed(e.Record, v) {
		present = false
	}
	changed := !live.Collected.Equal(e.Record.CollectionAt) || present != e.Record.Present
	e.Record.CollectionAt, e.Record.Present = live.Collected, present
	if present {
		changed = e.acceptClocks(v.ObservedAt, live.Collected) || changed
	}
	if changed {
		e.changed(now)
	}
}
func reportingObservationRegressed(record reportingRecord, v api.Vehicle) bool {
	return record.Value.LastObservedAt != nil && v.ObservedAt.Before(*record.Value.LastObservedAt)
}
func (e *reportingEntry) acceptClocks(observed, seen time.Time) bool {
	changed := false
	if e.Record.Value.LastObservedAt == nil || observed.After(*e.Record.Value.LastObservedAt) {
		e.Record.Value.LastObservedAt = ptr(observed)
		changed = true
	}
	if e.Record.Value.LastSeenAt == nil || seen.After(*e.Record.Value.LastSeenAt) {
		e.Record.Value.LastSeenAt = ptr(seen)
		changed = true
	}
	return changed
}

func (r *reportingRegistry) projection(operator string, live *LiveData) *LiveData {
	if live == nil {
		return nil
	}
	copyLive := *live
	copyLive.Vehicles = append([]api.Vehicle{}, live.Vehicles...)
	copyLive.LastKnown = append([]api.Vehicle{}, live.LastKnown...)
	copyLive.Samples = append([]api.Vehicle{}, live.Samples...)
	r.mu.Lock()
	defer r.mu.Unlock()
	attach := func(rows []api.Vehicle) {
		for i := range rows {
			if e := r.Entries[factKey(operator, rows[i].SourceId)]; e != nil && e.Loaded {
				value := e.Record.Value
				value.Persisted = !e.Dirty
				rows[i].Reporting = &value
			}
		}
	}
	attach(copyLive.Vehicles)
	attach(copyLive.LastKnown)
	return &copyLive
}

func (r *reportingRegistry) immediate(operator string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix := operator + "\x00"
	for k, e := range r.Entries {
		if strings.HasPrefix(k, prefix) && e.Dirty && e.Immediate {
			return true
		}
	}
	return false
}
