package patterns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func syntheticProviderPath(op string) ProviderJourney {
	p := ProviderJourney{Route: op + ":r", Direction: "0", Plan: "p", Pattern: "published", Headsign: "Terminus"}
	for i, s := range []string{"A", "B", "C", "D", "E", "F"} {
		p.Visits = append(p.Visits, ProviderVisit{Stop: op + ":" + s, Name: s, Sequence: i + 1, Lat: 38.7 + float64(i)*.001, Lon: -9.1})
	}
	return p
}
func syntheticProviderReceipt(op, vehicle string, path ProviderJourney, at time.Time, index int, stopped bool) ProviderReceipt {
	status := "INCOMING_AT"
	if stopped {
		status = "STOPPED_AT"
	}
	visit := path.Visits[index]
	return ProviderReceipt{Operator: op, ReceivedAt: at, Journeys: map[string]ProviderJourney{ProviderJourneyID(path): path}, Rows: []Observation{{ID: op + ":" + vehicle, SourceID: vehicle, SourceURL: "https://synthetic.invalid/positions", Journey: ProviderJourneyID(path), ObservedAt: at, Route: path.Route, Trip: op + ":trip-" + vehicle, Plan: path.Plan, Pattern: path.Pattern, OperationalDate: at.In(lisbon).Format("2006-01-02"), Stop: visit.Stop, Status: status, PositionKind: "reported", Lat: visit.Lat, Lon: visit.Lon}}}
}
func componentCount(e *engine) int64 {
	var n int64
	for _, a := range e.Aggregates {
		if a.Kind == "component" {
			n += a.Count
		}
	}
	return n
}
func trainProvider(p *providerState, op string, path ProviderJourney, base time.Time, c Config) {
	for i := range path.Visits {
		p.step(syntheticProviderReceipt(op, "training", path, base.Add(time.Duration(i*60)*time.Second), i, false), c, true, false)
		p.step(syntheticProviderReceipt(op, "training", path, base.Add(time.Duration(i*60+30)*time.Second), i, true), c, true, false)
	}
}
func TestEveryProviderUsesRemainingComponentsAndPreservesOfficial(t *testing.T) {
	for _, op := range operatorOrder[1:] {
		t.Run(op, func(t *testing.T) {
			c := DefaultConfig(t.TempDir())
			p := newProvider(op)
			path := syntheticProviderPath(op)
			base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
			trainProvider(p, op, path, base, c)
			if componentCount(p.Engine) != 5 {
				t.Fatalf("expected five bounded adjacent components, got %d", componentCount(p.Engine))
			}
			for i := 0; i < 3; i++ {
				p.step(syntheticProviderReceipt(op, "live", path, base.Add(time.Duration(360+i*60)*time.Second), i, false), c, true, false)
				p.step(syntheticProviderReceipt(op, "live", path, base.Add(time.Duration(390+i*60)*time.Second), i, true), c, true, false)
			}
			if componentCount(p.Engine) != 7 {
				t.Fatal("normal source turnover revoked completed component evidence")
			}
			now := base.Add(511 * time.Second)
			sequence := 4
			anchor := ProviderPrediction{ID: "feed:anchor", Stop: op + ":D", Route: path.Route, Trip: op + ":trip-live", Plan: "p", ServiceDate: base.In(lisbon).Format("2006-01-02"), Sequence: &sequence, ReceivedAt: now, ExpectedAt: now.Add(120 * time.Second), ValidUntil: now.Add(90 * time.Second)}
			p.step(ProviderReceipt{Operator: op, Partial: true, PredictionStop: "*", ReceivedAt: now, Predictions: []ProviderPrediction{anchor}}, c, true, false)
			waiting, onward := false, false
			for _, f := range p.Engine.Live {
				if f.Stop == op+":D" && (f.OwnAt != nil || f.OfficialAt == nil || !f.OfficialAt.Equal(anchor.ExpectedAt)) {
					t.Fatalf("anchor replaced or counted twice: %+v", f)
				}
				if f.Stop == op+":E" {
					if f.OwnAt == nil || !f.OwnAt.Equal(anchor.ExpectedAt.Add(time.Minute)) || len(f.Components) != 1 || f.Components[0].Origin != op+":D" {
						t.Fatalf("not using remaining adjacent component: %+v", f)
					}
					waiting = waiting || f.Function == "waiting"
					onward = onward || f.Function == "onward"
				}
			}
			if !waiting || !onward {
				t.Fatal("both forecast functions must be emitted")
			}
			// An intermediate missing vehicle immediately revokes displayed own support.
			p.step(ProviderReceipt{Operator: op, ReceivedAt: now.Add(time.Second), Rows: []Observation{}}, c, false, false)
			if len(p.Tracks) != 0 {
				t.Fatal("missing intermediate receipt preserved association")
			}
		})
	}
}
func TestProviderRejectsUnsupportedPhysicalAndIdentityInputs(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	path := syntheticProviderPath("cm")
	for _, mutate := range []func(*ProviderReceipt){
		func(r *ProviderReceipt) { r.Rows[0].PositionKind = "estimated" },
		func(r *ProviderReceipt) { r.Rows[0].ObservedAt = r.ReceivedAt.Add(-91 * time.Second) },
		func(r *ProviderReceipt) { r.Rows = append(r.Rows, r.Rows[0]) },
		func(r *ProviderReceipt) { r.Rows[0].Journey = "unverified" },
		func(r *ProviderReceipt) { r.Rows[0].Lat = 39.5 },
	} {
		p := newProvider("cm")
		for i := 0; i < 3; i++ {
			for j := 0; j < 2; j++ {
				r := syntheticProviderReceipt("cm", "v", path, base.Add(time.Duration(i*60+j*30)*time.Second), i, j == 1)
				mutate(&r)
				p.step(r, c, true, false)
			}
		}
		if componentCount(p.Engine) != 0 {
			t.Fatal("unsupported source created training")
		}
	}
	// Repeated stops without a unique preceding visit cannot seed an association.
	path.Visits[1].Stop = path.Visits[0].Stop
	p := newProvider("cm")
	p.step(syntheticProviderReceipt("cm", "v", path, base, 0, false), c, true, false)
	if len(p.Tracks) != 0 {
		t.Fatal("ambiguous repeated visit was guessed")
	}
}
func TestProviderArchiveRecoveryDedupAndFailedPublication(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	c.MaintenanceInterval = 50 * time.Millisecond
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	initial := s
	t.Cleanup(func() { _ = initial.Close() })
	if err = s.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	path := syntheticProviderPath("cm")
	seedProviderArchiveSequence(t, s, path, base)
	if providerRetainedState(s).components != 3 {
		t.Fatal("archive did not train")
	}
	assertProviderPublicationRollback(t, s, c, path, base)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	components, tracks := componentCount(s.providers["cm"].Engine), len(s.providers["cm"].Tracks)
	s.mu.Unlock()
	if components != 3 || tracks != 0 {
		t.Fatal("recovery changed evidence or invented continuity")
	}
	assertProviderDictionaryDeduplicated(t, s)
	v, err := s.ProviderView(context.Background(), "cm", "cm:A", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Operator != "cm" || v.PhysicalValidation || v.DwellSeconds != nil {
		t.Fatal("provider view claims physical evidence")
	}
}

func TestProviderRevisionsAreAtomicScopedAndPreserveOriginalEvidence(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	c.MaintenanceInterval = 50 * time.Millisecond
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	initial := s
	t.Cleanup(func() { _ = initial.Close() })
	if err = s.ConfigureOperators([]string{"metro", "cm", "carris"}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	path := syntheticProviderPath("cm")
	var original ProviderReceipt
	original = seedProviderArchiveSequence(t, s, path, base)
	before := providerRetainedState(s).components
	change := ProviderCorrection{ReceivedAt: original.ReceivedAt, ExpectedHash: digest(original.Rows[0]), Row: original.Rows[0], Evidence: "Synthetic source revision: reported position was estimated."}
	change.Row.PositionKind = "estimated"
	now := base.Add(5 * time.Minute)
	assertProviderRevisionRollback(t, s, c, change, now, before)
	result, err := s.ReprocessProvider(context.Background(), "cm", []ProviderCorrection{change}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Corrections != 1 || providerRetainedState(s).components != 0 {
		t.Fatal("revision did not revoke unsupported sequence")
	}
	s.mu.Lock()
	ledger, err := s.providerCorrections("cm")
	s.mu.Unlock()
	if err != nil || len(ledger) != 1 {
		t.Fatal("revision ledger missing")
	}
	if _, err = s.ReprocessProvider(context.Background(), "cm", []ProviderCorrection{change}, now.Add(time.Second)); err == nil {
		t.Fatal("stale row hash accepted")
	}
	assertProviderOriginalObservation(t, s, original, change)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if providerRetainedState(s).components != 0 {
		t.Fatal("restart resurrected revised training")
	}
}

func TestProviderManifestPreRenameFailureKeepsPriorEvidence(t *testing.T) {
	s, c, next := providerManifestFixture(t)
	prior := providerRetainedState(s)
	manifestPath := filepath.Join(c.Directory, "manifest.json")
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(c.Directory, ".manifest.tmp")
	if err = os.WriteFile(blocker, []byte("synthetic pre-rename blocker"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(blocker) })
	err = s.RecordProvider(next)
	var pathErr *os.PathError
	if !errors.Is(err, os.ErrExist) || !errors.As(err, &pathErr) || pathErr.Path != blocker {
		t.Fatalf("expected manifest temporary-file collision, got %v", err)
	}
	assertProviderRetained(t, s, prior)
	manifestAfter, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(manifestBefore, manifestAfter) {
		t.Fatalf("pre-rename failure changed the committed manifest: %v", err)
	}
	if err = os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordProvider(next); err != nil {
		t.Fatalf("provider did not retry after pre-rename failure: %v", err)
	}
	if state := providerRetainedState(s); !state.lastSample.Equal(next.ReceivedAt) {
		t.Fatal("successful retry did not advance the provider sample")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}

func providerManifestFixture(t *testing.T) (*Service, Config, ProviderReceipt) {
	t.Helper()
	c := DefaultConfig(t.TempDir())
	c.MaintenanceInterval = time.Hour
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	path := syntheticProviderPath("cm")
	seedProviderArchiveSequence(t, s, path, base)
	next := syntheticProviderReceipt("cm", "v", path, base.Add(240*time.Second), 4, false)
	return s, c, next
}

func TestProviderIntermediateCutsReplayOnlyTheAffectedAssociation(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	path := syntheticProviderPath("cm")
	for i := 0; i < 3; i++ {
		for j := 0; j < 2; j++ {
			at := base.Add(time.Duration(i*60+j*30) * time.Second)
			r := syntheticProviderReceipt("cm", "one", path, at, i, j == 1)
			other := syntheticProviderReceipt("cm", "two", path, at, i, j == 1)
			r.Rows = append(r.Rows, other.Rows...)
			if err = s.RecordProvider(r); err != nil {
				t.Fatal(err)
			}
		}
	}
	r := syntheticProviderReceipt("cm", "two", path, base.Add(155*time.Second), 2, true)
	if err = s.RecordProvider(r); err != nil {
		t.Fatal(err)
	}
	r = syntheticProviderReceipt("cm", "two", path, base.Add(180*time.Second), 3, false)
	if err = s.RecordProvider(r); err != nil {
		t.Fatal(err)
	}
	replay := newProvider("cm")
	found := false
	for _, b := range s.index.Blocks {
		if b.Operator != "cm" || b.Kind != "observations" {
			continue
		}
		blob, err := s.readBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := s.decoder.DecodeAll(blob, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
			var frame ProviderReceipt
			if err = json.Unmarshal(line, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.ReceivedAt.Equal(r.ReceivedAt) {
				_, found = frame.Cuts["cm:one"]
				if frame.Gap || len(frame.Cuts) != 1 {
					t.Fatal("individual loss became an operator-wide gap")
				}
			}
			replay.step(frame, c, true, true)
		}
	}
	if !found || replay.Tracks["cm:two"] == nil || !replay.Tracks["cm:two"].Group.Active || componentCount(replay.Engine) != componentCount(s.providers["cm"].Engine) {
		t.Fatal("replay lost another supported association")
	}
}
func TestProviderLongJourneysBoundComponentPayloadForBothFunctions(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	p := newProvider("cm")
	path := syntheticProviderPath("cm")
	for len(path.Visits) < 512 {
		i := len(path.Visits)
		path.Visits = append(path.Visits, ProviderVisit{Stop: fmt.Sprintf("cm:S%d", i), Name: fmt.Sprint(i), Sequence: i + 1, Lat: 38.7 + float64(i)*.001, Lon: -9.1})
	}
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	id := ProviderJourneyID(path)
	profile := providerProfile(path, c)
	p.Paths[id] = path
	p.Engine.Profile = profile
	r := syntheticProviderReceipt("cm", "live", path, now.Add(-30*time.Second), 2, true)
	g := &group{ID: "synthetic-supported", Route: path.Route, Direction: path.Direction, Active: true, Signals: []signal{{Stop: path.Visits[2].Stop, Platform: visitPlatform(path.Visits[2]), L: now.Add(-60 * time.Second), U: now.Add(-30 * time.Second), Supported: true}}}
	p.Tracks["cm:live"] = &providerTrack{Path: id, Previous: r.Rows[0], Group: g}
	p.Engine.Groups["cm:live"] = g
	for i := 3; i+1 < len(path.Visits); i++ {
		a := baseAggregate(aggregateRequest{now.AddDate(0, 0, -7), path.Route, path.Direction, path.Visits[i].Stop, visitPlatform(path.Visits[i]), profile, "unknown", "component"}, c)
		a.Target = path.Visits[i+1].Stop
		a.TargetPlatform = visitPlatform(path.Visits[i+1])
		a.Count = 1
		a.Sum = 1
		a.KnownAt = now.AddDate(0, 0, -7).Add(time.Minute).UnixNano()
		p.Engine.add(a)
	}
	pred := ProviderPrediction{ID: "feed:synthetic", Route: path.Route, Trip: r.Rows[0].Trip, Stop: path.Visits[3].Stop, ReceivedAt: now, ExpectedAt: now.Add(time.Minute), ValidUntil: now.Add(90 * time.Second)}
	p.Predictions[pred.ID] = pred
	refs := 0
	waiting, onward, limited := false, false, false
	for _, f := range p.forecasts(now, c) {
		refs += len(f.Components)
		waiting = waiting || f.Function == "waiting" && f.OwnAt != nil
		onward = onward || f.Function == "onward" && f.OwnAt != nil
		limited = limited || f.Unavailable == "component_summary_limit"
	}
	if refs > maxProviderComponentRefs || !waiting || !onward || !limited || !p.Engine.Limited {
		t.Fatalf("unbounded or hidden payload limit: %d", refs)
	}
}

func assertProviderDictionaryDeduplicated(t *testing.T, s *Service) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	definitions := 0
	for _, b := range s.index.Blocks {
		if b.Operator != "cm" || b.Kind != "observations" {
			continue
		}
		blob, err := s.readBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := s.decoder.DecodeAll(blob, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
			var r ProviderReceipt
			if err = json.Unmarshal(line, &r); err != nil {
				t.Fatal(err)
			}
			definitions += len(r.Journeys)
		}
	}
	if definitions != 1 {
		t.Fatalf("dictionary repeated %d times", definitions)
	}
}

func assertProviderPublicationRollback(t *testing.T, s *Service, c Config, path ProviderJourney, base time.Time) {
	t.Helper()
	// The directory invalidates allocation before manifest publication.
	blocker := providerLayoutBlocker(t, c.Directory)
	next := syntheticProviderReceipt("cm", "v", path, base.Add(240*time.Second), 4, false)
	prior := providerRetainedState(s)
	if err := s.RecordProvider(next); err == nil || !strings.Contains(err.Error(), "transport archive directory exceeds bounded layout") {
		t.Fatalf("publication did not reject the invalid layout: %v", err)
	}
	assertProviderRetained(t, s, prior)
	waitProviderLayoutFailure(t, s, false)
	releaseProviderLayout(t, s, blocker)
	assertProviderRetained(t, s, prior)
}

func assertProviderRevisionRollback(t *testing.T, s *Service, c Config, change ProviderCorrection, now time.Time, before int64) {
	t.Helper()
	// Reconciliation rejects this layout before a revision can be published.
	blocker := providerLayoutBlocker(t, c.Directory)
	prior := providerRetainedState(s)
	if _, err := s.ReprocessProvider(context.Background(), "cm", []ProviderCorrection{change}, now); err == nil || !strings.Contains(err.Error(), "unmanaged file in transport archive") {
		t.Fatalf("revision did not reject the invalid layout during reconciliation: %v", err)
	}
	if prior.components != before {
		t.Fatal("failed revision changed hot state")
	}
	assertProviderRetained(t, s, prior)
	waitProviderLayoutFailure(t, s, true)
	releaseProviderLayout(t, s, blocker)
	assertProviderRetained(t, s, prior)
}

type retainedProviderEvidence struct {
	components     int64
	lastSample     time.Time
	lastInput      time.Time
	aggregatesHash string
}

func providerRetainedState(s *Service) retainedProviderEvidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.providers["cm"]
	return retainedProviderEvidence{componentCount(state.Engine), state.LastSample, state.LastInputAt, digest(state.Engine.Aggregates)}
}

func assertProviderRetained(t *testing.T, s *Service, prior retainedProviderEvidence) {
	t.Helper()
	if got := providerRetainedState(s); got != prior {
		t.Fatalf("failure or maintenance changed retained provider evidence: got %+v, want %+v", got, prior)
	}
}

func providerLayoutBlocker(t *testing.T, directory string) string {
	t.Helper()
	blocker := filepath.Join(directory, ".manifest.tmp")
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(blocker) })
	return blocker
}

func waitProviderLayoutFailure(t *testing.T, s *Service, requireUnreserved bool) {
	t.Helper()
	started := time.Now()
	wakeMaintenance(s)
	waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool {
		return !p.InProgress && p.LastResult == "error" && p.LastStartedAt.After(started)
	})
	s.mu.Lock()
	pending, err := s.pressurePending, s.maintenanceError
	s.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "transport archive directory exceeds bounded layout") {
		t.Fatalf("worker did not observe the invalid layout: %v", err)
	}
	if requireUnreserved && pending {
		t.Fatal("revision unexpectedly reserved maintenance before the failing pass")
	}
}

func releaseProviderLayout(t *testing.T, s *Service, blocker string) {
	t.Helper()
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	wakeMaintenance(s)
	waitMaintenanceProgress(t, s, func(p MaintenanceProgress) bool {
		if p.InProgress || p.LastResult != "completed" || !p.LastStartedAt.After(started) {
			return false
		}
		s.mu.Lock()
		ready := !s.pressurePending && s.maintenanceError == nil
		s.mu.Unlock()
		return ready
	})
}

func assertProviderOriginalObservation(t *testing.T, s *Service, original ProviderReceipt, change ProviderCorrection) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.index.Blocks {
		if b.Operator != "cm" || b.Kind != "observations" {
			continue
		}
		blob, err := s.readBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := s.decoder.DecodeAll(blob, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
			var r ProviderReceipt
			_ = json.Unmarshal(line, &r)
			if r.ReceivedAt.Equal(original.ReceivedAt) && digest(r.Rows[0]) != change.ExpectedHash {
				t.Fatal("original observation rewritten")
			}
		}
	}
}

func seedProviderArchiveSequence(t *testing.T, s *Service, path ProviderJourney, base time.Time) ProviderReceipt {
	t.Helper()
	var original ProviderReceipt
	for i := 0; i < 4; i++ {
		for j := 0; j < 2; j++ {
			r := syntheticProviderReceipt("cm", "v", path, base.Add(time.Duration(i*60+j*30)*time.Second), i, j == 1)
			if i == 1 && j == 1 {
				original = r
			}
			if err := s.RecordProvider(r); err != nil {
				t.Fatal(err)
			}
		}
	}
	return original
}
