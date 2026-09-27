package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"lisboapublica/internal/api"
)

// Facts are independent of positions, history retention and pagination revisions.
// Only verified operator-scoped source IDs enter this store. Registration changes
// create a new context; previous contexts remain available in durable storage.
type vehicleFact struct {
	Value       json.RawMessage `json:"value"`
	Source      string          `json:"source"`
	SourceAt    *time.Time      `json:"source_at,omitempty"`
	ConfirmedAt time.Time       `json:"confirmed_at"`
	Priority    int             `json:"priority"`
}
type vehicleFacts struct {
	Active           string                            `json:"active"`
	IdentityAt       time.Time                         `json:"identity_at"`
	IdentitySourceAt *time.Time                        `json:"identity_source_at,omitempty"`
	Observed         time.Time                         `json:"observed"`
	Contexts         map[string]map[string]vehicleFact `json:"contexts"`
}
type factInput struct {
	Metadata    Metadata
	Source      string
	SourceAt    *time.Time
	ConfirmedAt time.Time
	Priority    int
}
type factEntry struct {
	Loaded   bool
	Deferred []factInput
	Facts    vehicleFacts
	Version  uint64
	Dirty    bool
	Used     uint64
}
type factRegistry struct {
	mu      sync.Mutex
	Entries map[string]*factEntry
	Clock   uint64
}
type factWrite struct {
	SourceID string
	Payload  []byte
	Version  uint64
}

const maxCachedFactIdentities = 8192

func factKey(operator, source string) string { return operator + "\x00" + source }
func registration(value *string) string {
	if value == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(*value))
}
func stableVehicleMetadata(v api.Vehicle) Metadata {
	text := func(v *string, max int) string {
		if v == nil {
			return ""
		}
		s := strings.TrimSpace(*v)
		if len(s) > max {
			return ""
		}
		return s
	}
	return Metadata{Model: text(v.Model, 256), Plate: text(v.LicensePlate, 64), Typology: text(v.Typology, 128), Propulsion: text(v.Propulsion, 128), SeatedCapacity: publishedCapacity(v.SeatedCapacity), TotalCapacity: publishedCapacity(v.TotalCapacity), WheelchairAccessible: v.WheelchairAccessible, Contactless: v.Contactless}
}
func validatedMetadata(m Metadata) Metadata {
	return stableVehicleMetadata(api.Vehicle{Model: optional(m.Model), LicensePlate: optional(m.Plate), Typology: optional(m.Typology), Propulsion: optional(m.Propulsion), SeatedCapacity: m.SeatedCapacity, TotalCapacity: m.TotalCapacity, WheelchairAccessible: m.WheelchairAccessible, Contactless: m.Contactless})
}
func metadataFields(m Metadata) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	add := func(key string, value any) { b, _ := json.Marshal(value); out[key] = b }
	if m.Model != "" {
		add("model", m.Model)
	}
	if m.Plate != "" {
		add("plate", m.Plate)
	}
	if m.Typology != "" {
		add("typology", m.Typology)
	}
	if m.Propulsion != "" {
		add("propulsion", m.Propulsion)
	}
	if m.SeatedCapacity != nil {
		add("seats", *m.SeatedCapacity)
	}
	if m.TotalCapacity != nil {
		add("capacity", *m.TotalCapacity)
	}
	if m.WheelchairAccessible != nil {
		add("wheelchair", *m.WheelchairAccessible)
	}
	if m.Contactless != nil {
		add("contactless", *m.Contactless)
	}
	return out
}
func (facts *vehicleFacts) merge(input factInput) bool {
	fields := metadataFields(input.Metadata)
	if len(fields) == 0 || !facts.selectContext(input) {
		return false
	}
	changed := false
	for name, value := range fields {
		old, exists := facts.Contexts[facts.Active][name]
		if exists && !acceptFact(old, value, input) {
			continue
		}
		facts.Contexts[facts.Active][name] = vehicleFact{value, input.Source, input.SourceAt, input.ConfirmedAt, input.Priority}
		changed = true
	}
	return changed
}
func acceptFact(old vehicleFact, value json.RawMessage, input factInput) bool {
	if input.Priority < old.Priority {
		return false
	}
	if input.SourceAt != nil && old.SourceAt != nil && input.SourceAt.Before(*old.SourceAt) {
		return false
	}
	return !repeatedFact(old, value, input)
}
func repeatedFact(old vehicleFact, value json.RawMessage, input factInput) bool {
	if !bytes.Equal(value, old.Value) || input.Source != old.Source || input.Priority != old.Priority {
		return false
	}
	if input.Priority < 0 {
		return true
	}
	return input.SourceAt != nil && old.SourceAt != nil && input.SourceAt.Equal(*old.SourceAt)
}
func (facts *vehicleFacts) selectContext(input factInput) bool {
	if facts.Contexts == nil {
		facts.Contexts = map[string]map[string]vehicleFact{}
	}
	plate := registration(optional(input.Metadata.Plate))
	if plate != "" && plate != facts.Active {
		if !facts.acceptRegistration(input) {
			return false
		}
		facts.refineFirstRegistration(plate)
		facts.Active = plate
		facts.IdentityAt = input.ConfirmedAt
		if input.SourceAt != nil {
			facts.IdentityAt = *input.SourceAt
		}
		facts.IdentitySourceAt = input.SourceAt
	}
	if facts.Contexts[facts.Active] == nil {
		facts.Contexts[facts.Active] = map[string]vehicleFact{}
	}
	return true
}
func (facts *vehicleFacts) acceptRegistration(input factInput) bool {
	active, exists := facts.Contexts[facts.Active]["plate"]
	if exists && input.Priority < active.Priority {
		return false
	}
	if input.SourceAt != nil && facts.IdentitySourceAt != nil {
		return input.SourceAt.After(*facts.IdentitySourceAt)
	}
	if input.SourceAt == nil && facts.IdentitySourceAt == nil && !facts.IdentityAt.IsZero() {
		return input.ConfirmedAt.After(facts.IdentityAt)
	}
	return true
}
func (facts *vehicleFacts) refineFirstRegistration(plate string) {
	// First registration refines the anonymous context; later conflicts inherit nothing.
	if facts.Active != "" || len(facts.Contexts) > 1 || facts.Contexts[plate] != nil {
		return
	}
	facts.Contexts[plate] = map[string]vehicleFact{}
	for name, field := range facts.Contexts[""] {
		facts.Contexts[plate][name] = field
	}
}

// Decode into fresh storage: writing through a copied API pointer would mutate
// prior revisions and pending historical samples.
func decodeFact[T any](raw json.RawMessage) *T {
	var value *T
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}
func (facts *vehicleFacts) enrich(v *api.Vehicle) {
	plate := registration(v.LicensePlate)
	if plate != "" && plate != facts.Active {
		return
	}
	if facts.IdentitySourceAt != nil && v.ObservedAt.Before(*facts.IdentitySourceAt) {
		return
	}
	for name, field := range facts.Contexts[facts.Active] {
		applyVehicleFact(v, name, field.Value)
	}
}
func assignFact[T any](target **T, raw json.RawMessage) {
	if value := decodeFact[T](raw); value != nil {
		*target = value
	}
}
func applyVehicleFact(v *api.Vehicle, name string, raw json.RawMessage) {
	switch name {
	case "model":
		assignFact(&v.Model, raw)
	case "plate":
		assignFact(&v.LicensePlate, raw)
	case "typology":
		assignFact(&v.Typology, raw)
	case "propulsion":
		assignFact(&v.Propulsion, raw)
	case "seats":
		assignFact(&v.SeatedCapacity, raw)
	case "capacity":
		assignFact(&v.TotalCapacity, raw)
	case "wheelchair":
		assignFact(&v.WheelchairAccessible, raw)
	case "contactless":
		assignFact(&v.Contactless, raw)
	}
}

// A failed initial lookup must not lose newly supplied attributes. Retry against
// the durable base before writing, preserving unrelated fields and original clocks.
func (entry *factEntry) deferInput(input factInput) {
	if len(metadataFields(input.Metadata)) == 0 {
		return
	}
	if n := len(entry.Deferred); n > 0 {
		previous := &entry.Deferred[n-1]
		a, _ := json.Marshal(previous.Metadata)
		b, _ := json.Marshal(input.Metadata)
		if previous.Source == input.Source && previous.Priority == input.Priority && bytes.Equal(a, b) {
			if input.SourceAt == nil || previous.SourceAt == nil || input.SourceAt.After(*previous.SourceAt) {
				*previous = input
			}
			return
		}
	}
	entry.Deferred = append(entry.Deferred, input)
	entry.Dirty = true
}

// loadFacts lazily restores only identities needed by the current publication.
// Clean entries may leave memory; dirty entries remain pending until a successful transaction.
func (s *Store) loadFacts(ctx context.Context, operator string, ids []string) error {
	missing := s.facts.missing(operator, ids)
	loaded, err := s.readFacts(ctx, operator, missing)
	if err == nil {
		for _, id := range missing {
			s.facts.entry(operator, id).restore(loaded[id])
		}
	}
	return err
}
func (r *factRegistry) entry(operator, id string) *factEntry {
	if r.Entries == nil {
		r.Entries = map[string]*factEntry{}
	}
	key := factKey(operator, id)
	if r.Entries[key] == nil {
		r.Entries[key] = &factEntry{}
	}
	return r.Entries[key]
}
func (r *factRegistry) missing(operator string, ids []string) []string {
	missing := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !r.entry(operator, id).Loaded && !seen[id] {
			missing = append(missing, id)
			seen[id] = true
		}
	}
	return missing
}
func (s *Store) readFacts(ctx context.Context, operator string, ids []string) (map[string]vehicleFacts, error) {
	if len(ids) == 0 || s.DB == nil {
		return map[string]vehicleFacts{}, nil
	}
	rows, err := s.DB.Query(ctx, "SELECT source_id,payload FROM vehicle_facts WHERE operator_id=$1 AND source_id=ANY($2)", operator, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFacts(rows)
}
func scanFacts(rows pgx.Rows) (map[string]vehicleFacts, error) {
	loaded := map[string]vehicleFacts{}
	var err error
	for rows.Next() {
		var id string
		var data []byte
		if err = rows.Scan(&id, &data); err != nil {
			break
		}
		var facts vehicleFacts
		if err = json.Unmarshal(data, &facts); err != nil {
			err = fmt.Errorf("decode vehicle facts: %w", err)
			break
		}
		loaded[id] = facts
	}
	if err == nil {
		err = rows.Err()
	}
	return loaded, err
}
func (entry *factEntry) restore(base vehicleFacts) {
	changed := false
	for _, input := range entry.Deferred {
		if input.SourceAt != nil && input.SourceAt.Before(base.Observed) {
			continue
		}
		if base.merge(input) {
			changed = true
		}
	}
	if entry.Facts.Observed.After(base.Observed) {
		base.Observed = entry.Facts.Observed
		changed = true
	}
	entry.Facts, entry.Loaded, entry.Deferred = base, true, nil
	entry.Dirty = changed && len(base.Contexts) > 0
	if entry.Dirty {
		entry.Version++
	}
}

func (s *Store) stageVehicleFacts(ctx context.Context, operator string, vehicles []api.Vehicle, previous *LiveData, now time.Time) error {
	if operator == "metro" {
		return nil
	} // Synthetic IDs do not identify physical trains.
	r := &s.facts
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := []string{}
	for _, v := range vehicles {
		ids = append(ids, v.SourceId)
	}
	loadErr := s.loadFacts(ctx, operator, ids)
	old := latestFactVehicles(previous)
	for _, v := range vehicles {
		entry := r.touch(operator, v.SourceId)
		if factObservationRegressed(v, old, entry) {
			continue
		}
		entry.observe(factInput{stableVehicleMetadata(v), v.SourceUrl, ptr(v.ObservedAt), now, 2})
	}
	return loadErr
}
func latestFactVehicles(previous *LiveData) map[string]api.Vehicle {
	old := map[string]api.Vehicle{}
	if previous != nil {
		for _, v := range previous.LastKnown {
			old[v.Id] = v
		}
		for _, v := range previous.Vehicles {
			mergeLatest(old, v)
		}
	}
	return old
}
func factObservationRegressed(v api.Vehicle, old map[string]api.Vehicle, entry *factEntry) bool {
	if previous, exists := old[v.Id]; exists && v.ObservedAt.Before(previous.ObservedAt) {
		return true
	}
	return v.ObservedAt.Before(entry.Facts.Observed)
}
func (r *factRegistry) touch(operator, id string) *factEntry {
	entry := r.entry(operator, id)
	r.Clock++
	entry.Used = r.Clock
	return entry
}
func (entry *factEntry) observe(input factInput) {
	if !entry.Loaded {
		entry.deferInput(input)
	}
	changed := entry.Facts.merge(input)
	if input.SourceAt != nil && input.SourceAt.After(entry.Facts.Observed) && (len(entry.Facts.Contexts) > 0 || !entry.Loaded) {
		entry.Facts.Observed = *input.SourceAt
		changed = true
	}
	if changed {
		entry.Version++
		entry.Dirty = true
	}
}

func (s *Store) stageMetadataFacts(ctx context.Context, operator string, models map[string]Metadata, origin factInput) error {
	if operator == "metro" {
		return nil
	}
	r := &s.facts
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := []string{}
	for id := range models {
		ids = append(ids, id)
	}
	loadErr := s.loadFacts(ctx, operator, ids)
	for id, metadata := range models {
		input := origin
		input.Metadata = validatedMetadata(metadata)
		r.touch(operator, id).observe(input)
	}
	return loadErr
}

func (s *Store) enrichFacts(operator string, vehicles []api.Vehicle) {
	r := &s.facts
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range vehicles {
		v := &vehicles[i]
		if entry := r.Entries[factKey(operator, v.SourceId)]; entry != nil {
			entry.Facts.enrich(v)
		}
	}
}
func (s *Store) pendingFacts(ctx context.Context, operator string) ([]factWrite, error) {
	r := &s.facts
	r.mu.Lock()
	defer r.mu.Unlock()
	writes := []factWrite{}
	prefix := operator + "\x00"
	unloaded := []string{}
	for key, entry := range r.Entries {
		if !entry.Loaded && strings.HasPrefix(key, prefix) {
			unloaded = append(unloaded, strings.TrimPrefix(key, prefix))
		}
	}
	if err := s.loadFacts(ctx, operator, unloaded); err != nil {
		return nil, err
	}
	for key, entry := range r.Entries {
		if !entry.Dirty || !strings.HasPrefix(key, prefix) {
			continue
		}
		b, err := json.Marshal(entry.Facts)
		if err != nil {
			return nil, err
		}
		writes = append(writes, factWrite{strings.TrimPrefix(key, prefix), b, entry.Version})
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].SourceID < writes[j].SourceID })
	return writes, nil
}
func (s *Store) acknowledgeFacts(operator string, writes []factWrite) {
	r := &s.facts
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, w := range writes {
		if e := r.Entries[factKey(operator, w.SourceID)]; e != nil && e.Version == w.Version {
			e.Dirty = false
		}
	}
	s.evictFacts()
}
func (s *Store) evictFacts() {
	r := &s.facts
	if s.DB == nil || len(r.Entries) <= maxCachedFactIdentities {
		return
	}
	type candidate struct {
		Key  string
		Used uint64
	}
	clean := []candidate{}
	for key, e := range r.Entries {
		if e.Loaded && !e.Dirty {
			clean = append(clean, candidate{key, e.Used})
		}
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i].Used < clean[j].Used })
	for _, c := range clean {
		if len(r.Entries) <= maxCachedFactIdentities {
			break
		}
		delete(r.Entries, c.Key)
	}
}
func insertVehicleFacts(ctx context.Context, tx pgx.Tx, operator string, writes []factWrite) error {
	if len(writes) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, w := range writes {
		batch.Queue("INSERT INTO vehicle_facts(operator_id,source_id,payload) VALUES($1,$2,$3) ON CONFLICT(operator_id,source_id) DO UPDATE SET payload=excluded.payload", operator, w.SourceID, w.Payload)
	}
	return tx.SendBatch(ctx, batch).Close()
}

// Metadata publication may change attributes without creating a position publication.
func (s *Store) factProjection(ctx context.Context, operator string, live *LiveData) (*LiveData, error) {
	if live == nil || operator == "metro" {
		return live, nil
	}
	ids := []string{}
	for _, v := range live.Vehicles {
		ids = append(ids, v.SourceId)
	}
	for _, v := range live.LastKnown {
		ids = append(ids, v.SourceId)
	}
	r := &s.facts
	r.mu.Lock()
	err := s.loadFacts(ctx, operator, ids)
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	copyLive := *live
	copyLive.Vehicles = append([]api.Vehicle{}, live.Vehicles...)
	copyLive.LastKnown = append([]api.Vehicle{}, live.LastKnown...)
	copyLive.Samples = []api.Vehicle{}
	s.enrichFacts(operator, copyLive.Vehicles)
	s.enrichFacts(operator, copyLive.LastKnown)
	return &copyLive, nil
}

// Older caches lack per-field provenance. Preserve validated values at the
// lowest precedence, labelled as legacy rather than inventing a source clock.
func (s *Store) seedLegacyFacts(ctx context.Context, operator string, static *StaticData, live *LiveData) error {
	if operator == "metro" {
		return nil
	}
	models := map[string]Metadata{}
	if static != nil {
		for id, m := range static.Models {
			models[id] = validatedMetadata(m)
		}
	}
	latest := map[string]api.Vehicle{}
	if live != nil {
		for _, v := range live.LastKnown {
			mergeLatest(latest, v)
		}
		for _, v := range live.Vehicles {
			mergeLatest(latest, v)
		}
	}
	mergeLegacyVehicleFacts(models, latest)
	return s.stageMetadataFacts(ctx, operator, models, factInput{Source: "legacy-cache", ConfirmedAt: time.Now().UTC(), Priority: -1})
}

func (s *Store) restoreProviderFacts(ctx context.Context, c *Cache, p provider, ds *StaticData, dl *LiveData) error {
	op := c.operator(p.ID)
	if e := s.restoreOperator(ctx, p.ID, &op); e != nil {
		return e
	}
	var err error
	dl, err = s.recoverProviderFacts(ctx, p.ID, ds, dl)
	if err != nil {
		return err
	}
	if _, err = s.reporting.stage(reportingLookup{ctx, s.readReporting}, p.ID, dl, op, time.Now().UTC()); err != nil {
		return err
	}
	dl = s.reporting.projection(p.ID, dl)
	if ds != nil || dl != nil {
		c.update(p.ID, ds, dl, op)
	}
	return nil
}

func (s *Store) recoverProviderFacts(ctx context.Context, id string, ds *StaticData, dl *LiveData) (*LiveData, error) {
	if err := s.seedLegacyFacts(ctx, id, ds, dl); err != nil {
		return nil, err
	}
	return s.factProjection(ctx, id, dl)
}

func mergeLegacyVehicleFacts(models map[string]Metadata, latest map[string]api.Vehicle) {
	for _, v := range latest {
		if v.SourceId == "" {
			continue
		}
		m := stableVehicleMetadata(v)
		previous := models[v.SourceId]
		if m.Plate != "" && previous.Plate != "" && registration(optional(m.Plate)) != registration(optional(previous.Plate)) {
			previous = Metadata{}
		}
		models[v.SourceId] = mergeMetadata(previous, m)
	}
}
