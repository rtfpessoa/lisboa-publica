package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func TestVehicleFactsPartialFieldsAndIdentity(t *testing.T) {
	now := time.Now().UTC()
	s := &Store{}
	v := continuityVehicle("facts", now)
	v.Model = ptr("Original model")
	v.LicensePlate = ptr("AA-00-AA")
	v.TotalCapacity = ptr(0)
	v.Contactless = ptr(false)
	if err := s.stageVehicleFacts(context.Background(), "cp", []api.Vehicle{v}, nil, now); err != nil {
		t.Fatal(err)
	}
	partial := v
	partial.Model = nil
	partial.TotalCapacity = nil
	partial.Contactless = nil
	partial.ObservedAt = now.Add(time.Minute)
	if err := s.stageVehicleFacts(context.Background(), "cp", []api.Vehicle{partial}, nil, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rows := []api.Vehicle{partial}
	s.enrichFacts("cp", rows)
	if stringValue(rows[0].Model) != "Original model" || rows[0].TotalCapacity == nil || *rows[0].TotalCapacity != 0 || rows[0].Contactless == nil || *rows[0].Contactless {
		t.Fatal("omission lost verified zero/false metadata")
	}
	facts := s.facts.Entries[factKey("cp", v.SourceId)].Facts
	model := facts.Contexts[facts.Active]["model"]
	if !model.ConfirmedAt.Equal(now) || model.SourceAt == nil || !model.SourceAt.Equal(now) {
		t.Fatal("X-only response renewed model clocks")
	}
	// Catalogue values cannot replace direct values or graft another registration.
	if err := s.stageMetadataFacts(context.Background(), "cp", map[string]Metadata{v.SourceId: {Model: "Fallback", Plate: "BB-00-BB", TotalCapacity: ptr(99)}}, factInput{Source: "catalogue", ConfirmedAt: now.Add(2 * time.Minute), Priority: 1}); err != nil {
		t.Fatal(err)
	}
	rows = []api.Vehicle{partial}
	s.enrichFacts("cp", rows)
	if stringValue(rows[0].Model) != "Original model" || *rows[0].TotalCapacity != 0 {
		t.Fatal("incompatible fallback replaced direct facts")
	}
	replacement := partial
	replacement.LicensePlate = ptr("BB-00-BB")
	replacement.ObservedAt = now.Add(3 * time.Minute)
	_ = s.stageVehicleFacts(context.Background(), "cp", []api.Vehicle{replacement}, nil, replacement.ObservedAt)
	rows = []api.Vehicle{replacement}
	s.enrichFacts("cp", rows)
	if rows[0].Model != nil || rows[0].Contactless != nil {
		t.Fatal("reused source ID borrowed old vehicle fields")
	}
	facts = s.facts.Entries[factKey("cp", v.SourceId)].Facts
	if len(facts.Contexts) != 2 || facts.Contexts["AA-00-AA"]["model"].Value == nil {
		t.Fatal("previous identity facts were deleted")
	}
	// Another operator's identical raw ID cannot recover CP metadata.
	rows = []api.Vehicle{{SourceId: v.SourceId, ObservedAt: replacement.ObservedAt}}
	s.enrichFacts("ttsl", rows)
	if rows[0].Model != nil {
		t.Fatal("facts crossed operator namespace")
	}
	_ = s.stageVehicleFacts(context.Background(), "cp", []api.Vehicle{v}, nil, now.Add(time.Hour))
	if s.facts.Entries[factKey("cp", v.SourceId)].Facts.Active != "BB-00-BB" {
		t.Fatal("regression changed active identity")
	}
}
func TestSameClockMetadataKeepsAtomicObservationAndJourney(t *testing.T) {
	s := &Store{}
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	p, _ := providerByID("cp")
	// Avoid fixture DB writes; the ordinary persistence path has integration coverage below.
	s.HistoryInterval = staticRefreshInterval
	f.lastPersist["cp"] = time.Now().UTC()
	now := time.Now().UTC()
	v := continuityVehicle("partial", now)
	v.Model = ptr("A")
	v.TripId = ptr("cp:trip-a")
	f.saveLive(context.Background(), p, []api.Vehicle{v}, now)
	revised := v
	revised.Model = ptr("B")
	revised.Lon += .01
	revised.TripId = ptr("cp:trip-b")
	revised.CollectedAt = now.Add(time.Second)
	f.saveLive(context.Background(), p, []api.Vehicle{revised}, now.Add(time.Second))
	state, _ := c.state("")
	live := state.Live["cp"]
	got := live.Vehicles[0]
	if stringValue(got.Model) != "B" || got.Lon != v.Lon || !got.CollectedAt.Equal(v.CollectedAt) || stringValue(got.TripId) != "cp:trip-a" || len(live.Samples) != 0 {
		t.Fatal("metadata update modified original observation", got)
	}
	revised.ObservedAt = now.Add(2 * time.Second)
	revised.TripId = ptr("cp:trip-b")
	revised.PlanId = nil
	revised.OperationalDate = nil
	revised.PatternId = nil
	f.saveLive(context.Background(), p, []api.Vehicle{revised}, revised.ObservedAt)
	state, _ = c.state("")
	got = state.Live["cp"].Vehicles[0]
	if stringValue(got.TripId) != "cp:trip-b" || got.PlanId != nil || got.OperationalDate != nil || got.PatternId != nil {
		t.Fatal("journey fields leaked between trips")
	}
}
func TestVehicleFactsDurabilityIndependentOfPositionsAndHistory(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	v := continuityVehicle("permanent", now)
	v.Model = ptr("Confirmed")
	v.Contactless = ptr(false)
	if err := s.stageVehicleFacts(ctx, "cp", []api.Vehicle{v}, nil, now); err != nil {
		t.Fatal(err)
	}
	op := api.Operator{Id: "cp", Status: api.OperatorStatusOk}
	if err := s.Save(ctx, "cp", nil, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, "DELETE FROM cache_parts"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, "DELETE FROM snapshots"); err != nil {
		t.Fatal(err)
	}
	restarted := &Store{DB: s.DB}
	partial := v
	partial.Model = nil
	partial.Contactless = nil
	partial.ObservedAt = now.Add(48 * time.Hour)
	if err := restarted.stageVehicleFacts(ctx, "cp", []api.Vehicle{partial}, nil, partial.ObservedAt); err != nil {
		t.Fatal(err)
	}
	rows := []api.Vehicle{partial}
	restarted.enrichFacts("cp", rows)
	if stringValue(rows[0].Model) != "Confirmed" || rows[0].Contactless == nil || *rows[0].Contactless {
		t.Fatal("restart/position expiry/history cleanup lost facts")
	}
	// Failed publication cannot acknowledge facts; retry writes the original confirmation clock.
	partial.Model = ptr("Updated")
	_ = restarted.stageVehicleFacts(ctx, "cp", []api.Vehicle{partial}, nil, partial.ObservedAt)
	if _, err := s.DB.Exec(ctx, "DROP TABLE source_health"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Save(ctx, "cp", nil, nil, op, nil); err == nil {
		t.Fatal("expected failed transaction")
	}
	if !restarted.facts.Entries[factKey("cp", v.SourceId)].Dirty {
		t.Fatal("failed transaction acknowledged facts")
	}
	if _, err := s.DB.Exec(ctx, "CREATE TABLE source_health(operator_id TEXT PRIMARY KEY,payload JSONB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Save(ctx, "cp", nil, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := s.DB.QueryRow(ctx, "SELECT payload FROM vehicle_facts WHERE operator_id='cp' AND source_id=$1", v.SourceId).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var facts vehicleFacts
	if err := json.Unmarshal(payload, &facts); err != nil {
		t.Fatal(err)
	}
	if !facts.Contexts[facts.Active]["model"].ConfirmedAt.Equal(partial.ObservedAt) {
		t.Fatal("retry renewed field confirmation")
	}
}

func TestCatalogueConfirmsOnlyExplicitFields(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	now := time.Now().UTC()
	initial := map[string]Metadata{"id": {Model: "Model", TotalCapacity: ptr(0), Contactless: ptr(false)}}
	if err := s.stageMetadataFacts(ctx, "cm", initial, factInput{Source: "catalogue", ConfirmedAt: now, Priority: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.stageMetadataFacts(ctx, "cm", map[string]Metadata{"id": {Model: "Model"}}, factInput{Source: "catalogue", ConfirmedAt: now.Add(time.Hour), Priority: 1}); err != nil {
		t.Fatal(err)
	}
	fields := s.facts.Entries[factKey("cm", "id")].Facts.Contexts[""]
	if !fields["model"].ConfirmedAt.Equal(now.Add(time.Hour)) || !fields["capacity"].ConfirmedAt.Equal(now) || !fields["contactless"].ConfirmedAt.Equal(now) || fields["model"].SourceAt != nil {
		t.Fatal("catalogue invented source time or reconfirmed omitted fields")
	}
	if err := s.stageMetadataFacts(ctx, "cm", map[string]Metadata{"id": {Model: " ", TotalCapacity: ptr(-1)}}, factInput{Source: "catalogue", ConfirmedAt: now.Add(2 * time.Hour), Priority: 1}); err != nil {
		t.Fatal(err)
	}
	fields = s.facts.Entries[factKey("cm", "id")].Facts.Contexts[""]
	if string(fields["capacity"].Value) != "0" || string(fields["model"].Value) != "\"Model\"" {
		t.Fatal("invalid fields erased confirmed facts")
	}
}

func TestRegistrationChangeCannotCreateMovementPair(t *testing.T) {
	now := time.Now().UTC()
	a := continuityVehicle("reused", now)
	a.LicensePlate = ptr("AA-00-AA")
	b := a
	b.ObservedAt = now.Add(5 * time.Second)
	b.Lon += .0001
	b.LicensePlate = ptr("BB-00-BB")
	if distance, speed := sampledDistance(a, b); distance != nil || speed != nil {
		t.Fatal("movement bridged physical identity conflict")
	}
}

func TestMetadataProjectionKeepsPositionAndHistoryEvidence(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	now := time.Now().UTC()
	v := continuityVehicle("projection", now)
	v.Model = ptr("Previous catalogue")
	live, _ := nextLive(nil, api.Operator{Status: api.OperatorStatusOk}, []api.Vehicle{v}, now)
	_ = s.stageMetadataFacts(ctx, "cp", map[string]Metadata{v.SourceId: {Model: "New catalogue"}}, factInput{Source: "catalogue", ConfirmedAt: now.Add(time.Minute), Priority: 1})
	projected, err := s.factProjection(ctx, "cp", live)
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(projected.Vehicles[0].Model) != "New catalogue" || stringValue(live.Vehicles[0].Model) != "Previous catalogue" || !projected.Vehicles[0].ObservedAt.Equal(v.ObservedAt) || !projected.Collected.Equal(live.Collected) || len(projected.Samples) != 0 || len(live.Samples) != 1 || projected.Continuity[v.Id] != live.Continuity[v.Id] {
		t.Fatal("metadata projection mutated position/history/prior revision")
	}
}

func TestRestoreReprojectsDurableFactsOntoOlderCache(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	v := continuityVehicle("restored-fields", now)
	v.Model = ptr("Previous catalogue")
	live, _ := nextLive(nil, api.Operator{Status: api.OperatorStatusOk}, []api.Vehicle{v}, now)
	op := api.Operator{Id: "cp", Status: api.OperatorStatusOk}
	if err := s.Save(ctx, "cp", nil, live, op, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.stageMetadataFacts(ctx, "cp", map[string]Metadata{v.SourceId: {Model: "Confirmed update"}}, factInput{Source: "catalogue", ConfirmedAt: now.Add(time.Minute), Priority: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "cp", nil, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	restarted := &Store{DB: s.DB}
	cache := NewCache()
	if err := restarted.Restore(ctx, cache); err != nil {
		t.Fatal(err)
	}
	state, _ := cache.state("")
	got := state.Live["cp"].Vehicles[0]
	if stringValue(got.Model) != "Confirmed update" || !got.ObservedAt.Equal(now) || !got.CollectedAt.Equal(now) || !state.Live["cp"].Unverified || len(state.Live["cp"].Samples) != 0 {
		t.Fatal("restored facts reset clocks or missed independent update")
	}
}

func TestCatalogueRegistrationChangeDoesNotBridgeMovement(t *testing.T) {
	f, server := continuityFetcher()
	ctx := context.Background()
	now := time.Now().UTC()
	p, _ := providerByID("cp")
	v := continuityVehicle("catalogue-identity", now)
	_ = f.Store.stageMetadataFacts(ctx, "cp", map[string]Metadata{v.SourceId: {Plate: "AA-00-AA", Model: "First vehicle"}}, factInput{Source: "catalogue", ConfirmedAt: now, Priority: 1})
	f.saveLive(ctx, p, []api.Vehicle{v}, now)
	original, _ := server.Cache.state("")
	_ = f.Store.stageMetadataFacts(ctx, "cp", map[string]Metadata{v.SourceId: {Plate: "BB-00-BB"}}, factInput{Source: "catalogue", ConfirmedAt: now.Add(time.Second), Priority: 1})
	v.ObservedAt = now.Add(5 * time.Second)
	v.Lon += .0001
	f.saveLive(ctx, p, []api.Vehicle{v}, v.ObservedAt)
	state, _ := server.Cache.state("")
	latest := state.Live["cp"].Vehicles[0]
	if stringValue(latest.LicensePlate) != "BB-00-BB" || latest.SpeedKmh != nil || latest.Model != nil {
		t.Fatal("catalogue identity change borrowed metadata/movement", latest)
	}
	if stringValue(original.Live["cp"].Vehicles[0].LicensePlate) != "AA-00-AA" || stringValue(original.Live["cp"].Vehicles[0].Model) != "First vehicle" {
		t.Fatal("prior revision mutated")
	}
}

func TestFirstRegistrationRefinesCompatibleSourceIdentity(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	now := time.Now().UTC()
	v := continuityVehicle("first-registration", now)
	v.Model = ptr("Confirmed model")
	_ = s.stageVehicleFacts(ctx, "cp", []api.Vehicle{v}, nil, now)
	v.Model = nil
	v.LicensePlate = ptr("AA-00-AA")
	v.ObservedAt = now.Add(time.Minute)
	_ = s.stageVehicleFacts(ctx, "cp", []api.Vehicle{v}, nil, v.ObservedAt)
	rows := []api.Vehicle{v}
	s.enrichFacts("cp", rows)
	if stringValue(rows[0].Model) != "Confirmed model" {
		t.Fatal("first registration erased compatible stable attribute")
	}
	facts := s.facts.Entries[factKey("cp", v.SourceId)].Facts
	if !facts.Contexts[facts.Active]["model"].ConfirmedAt.Equal(now) {
		t.Fatal("identity refinement reconfirmed missing field")
	}
}

func TestFailedInitialFactReadPreservesPartialReceiptsUntilRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	op := api.Operator{Id: "cp", Status: api.OperatorStatusOk}
	warm := continuityVehicle("warm", now)
	warm.Model = ptr("Old model")
	if err := s.stageVehicleFacts(ctx, "cp", []api.Vehicle{warm}, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "cp", nil, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, "ALTER TABLE vehicle_facts RENAME TO temporarily_unavailable_facts"); err != nil {
		t.Fatal(err)
	}
	cold := continuityVehicle("cold", now.Add(time.Second))
	cold.Model = ptr("Confirmed during outage")
	cold.Contactless = ptr(false)
	warm.ObservedAt = cold.ObservedAt
	warm.Model = ptr("Updated during outage")
	if err := s.stageVehicleFacts(ctx, "cp", []api.Vehicle{cold, warm}, nil, cold.ObservedAt); err == nil {
		t.Fatal("expected read failure")
	}
	if err := s.Save(ctx, "cp", nil, nil, op, nil); err == nil {
		t.Fatal("unresolved durable base was overwritten")
	}
	if _, err := s.DB.Exec(ctx, "ALTER TABLE temporarily_unavailable_facts RENAME TO vehicle_facts"); err != nil {
		t.Fatal(err)
	}
	partial := cold
	partial.Model = nil
	partial.Contactless = nil
	partial.ObservedAt = now.Add(2 * time.Second)
	if err := s.stageVehicleFacts(ctx, "cp", []api.Vehicle{partial}, nil, partial.ObservedAt); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, "cp", nil, nil, op, nil); err != nil {
		t.Fatal(err)
	}
	restarted := &Store{DB: s.DB}
	if err := restarted.stageVehicleFacts(ctx, "cp", []api.Vehicle{partial}, nil, partial.ObservedAt); err != nil {
		t.Fatal(err)
	}
	rows := []api.Vehicle{partial}
	restarted.enrichFacts("cp", rows)
	if stringValue(rows[0].Model) != "Confirmed during outage" || rows[0].Contactless == nil || *rows[0].Contactless {
		t.Fatal("read failure plus X-only response lost supplied Y/Z")
	}
	facts := restarted.facts.Entries[factKey("cp", cold.SourceId)].Facts
	if !facts.Contexts[facts.Active]["model"].ConfirmedAt.Equal(cold.ObservedAt) || !facts.Contexts[facts.Active]["contactless"].ConfirmedAt.Equal(cold.ObservedAt) {
		t.Fatal("retry/X-only response renewed omitted field clocks")
	}
	partial = warm
	partial.Model = nil
	if err := restarted.stageVehicleFacts(ctx, "cp", []api.Vehicle{partial}, nil, partial.ObservedAt); err != nil {
		t.Fatal(err)
	}
	rows = []api.Vehicle{partial}
	restarted.enrichFacts("cp", rows)
	if stringValue(rows[0].Model) != "Updated during outage" {
		t.Fatal("one missing identity stopped updates for warm identities")
	}
}

func TestLegacyCacheFieldsSurviveExpiredPositionAndStaticReplacement(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	v := continuityVehicle("legacy", now.Add(-25*time.Hour))
	v.Model = ptr("Previously confirmed")
	v.Contactless = ptr(false)
	v.LicensePlate = ptr("AA-00-AA")
	op := api.Operator{Id: "cp", Status: api.OperatorStatusOk}
	if err := s.Save(ctx, "cp", nil, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, op, nil); err != nil {
		t.Fatal(err)
	}
	restarted := &Store{DB: s.DB}
	cache := NewCache()
	if err := restarted.Restore(ctx, cache); err != nil {
		t.Fatal(err)
	}
	state, _ := cache.state("")
	priorConfirmation := restarted.facts.Entries[factKey("cp", v.SourceId)].Facts.Contexts["AA-00-AA"]["model"].ConfirmedAt
	if err := restarted.seedLegacyFacts(ctx, "cp", nil, state.Live["cp"]); err != nil {
		t.Fatal(err)
	}
	if !restarted.facts.Entries[factKey("cp", v.SourceId)].Facts.Contexts["AA-00-AA"]["model"].ConfirmedAt.Equal(priorConfirmation) {
		t.Fatal("legacy replay renewed field confirmation")
	}
	rows, _, _, _, _ := projectLive(state.Live["cp"], op, nil, now)
	if len(rows) != 0 {
		t.Fatal("legacy migration restored an expired marker")
	}
	if err := restarted.Save(ctx, "cp", nil, &LiveData{Vehicles: []api.Vehicle{}, Collected: now}, op, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, "DELETE FROM cache_parts"); err != nil {
		t.Fatal(err)
	}
	again := &Store{DB: s.DB}
	partial := v
	partial.Model = nil
	partial.Contactless = nil
	partial.ObservedAt = now
	if err := again.stageVehicleFacts(ctx, "cp", []api.Vehicle{partial}, nil, now); err != nil {
		t.Fatal(err)
	}
	rows = []api.Vehicle{partial}
	again.enrichFacts("cp", rows)
	if stringValue(rows[0].Model) != "Previously confirmed" || rows[0].Contactless == nil || *rows[0].Contactless {
		t.Fatal("legacy values disappeared with expired position/cache replacement")
	}
	facts := again.facts.Entries[factKey("cp", v.SourceId)].Facts
	field := facts.Contexts[facts.Active]["model"]
	if field.Source != "legacy-cache" || field.SourceAt != nil || field.Priority != -1 {
		t.Fatal("legacy cache invented original field provenance")
	}
}
