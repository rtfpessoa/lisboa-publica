package patterns

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestCeilingAndProtectedAdmission(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Private archive helpers require the same owner lock as production callers.
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Hour)
	b := block{Key: "protected", Kind: "detail", Operator: "metro", Age: now.Add(-time.Hour)}
	if err = s.publish(b, []byte("protected generation"), now); err != nil {
		t.Fatal(err)
	}
	before := s.index.Blocks[0]
	base := manifest{Version: 1, Blocks: []block{{Key: ""}}}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Blocks[0].Key = strings.Repeat("x", maxManifestBytes-len(raw))
	if data, err := encodeManifest(base); err != nil || len(data) != maxManifestBytes {
		t.Fatalf("exact limit %d: %v", len(data), err)
	}
	base.Blocks[0].Key += "x"
	if err = s.admitManifest(func() manifest { return base }, 0, now); !errors.Is(err, errManifestLimit) {
		t.Fatalf("limit: %v", err)
	}
	if len(s.index.Blocks) != 1 || s.index.Blocks[0].File != before.File {
		t.Fatal("oversized candidate changed manifest")
	}
	if data, err := s.readBlock(before); err != nil || string(data) != "protected generation" {
		t.Fatal("protected generation lost", err)
	}
}

func TestManifestLargerThanLegacyCeilingReopens(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	// Synthetic cardinality and block mix approximate the sampled live manifest.
	// Identical fixture blocks intentionally share one valid backing file.
	shared := []byte("synthetic block")
	name := "metro-fixture-" + strings.Repeat("a", 80) + ".json.zst"
	if err = os.WriteFile(filepath.Join(c.Directory, name), shared, 0600); err != nil {
		t.Fatal(err)
	}
	m := manifest{Version: 1}
	for i := 0; i < 2641; i++ {
		kind := "popup-checkpoint"
		if i >= 1468 && i < 2469 {
			kind = "metro-inputs"
		}
		if i >= 2469 {
			kind = "detail"
		}
		m.Blocks = append(m.Blocks, block{Key: fmt.Sprintf("fixture:%08d:%s", i, strings.Repeat("k", 55)), File: name, Hash: digestBytes(shared), Kind: kind, Operator: "metro", Age: now})
	}
	if err = s.saveManifest(m); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(c.Directory, "manifest.json"))
	if err != nil || info.Size() <= 1<<20 || info.Size() >= maxManifestBytes {
		t.Fatalf("fixture manifest size %v, %v", info, err)
	}
	value := MetroJourneyCheckpoint{Journey: "metro:run:synthetic", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"calls":[]}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{value}, now); err != nil {
		t.Fatal(err)
	}
	value.Revision++
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{value}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, state, err := s.MetroJourneyCheckpoint(context.Background(), value.Journey, now.Add(time.Minute))
	if err != nil || state != "restored" || got.Revision != 2 {
		t.Fatalf("restore %s %+v %v", state, got, err)
	}
}

func TestSmallArchiveRejectsWithoutDeletingRecent(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	c.LimitBytes = 1 << 20
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Private archive helpers require the same owner lock as production callers.
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Hour)
	b := block{Kind: "detail", Operator: "metro", Age: now.Add(-time.Hour)}
	b.Key = "metro:detail:" + b.Age.Format(time.RFC3339)
	if err = s.publish(b, make([]byte, 600<<10), now); err != nil {
		t.Fatal(err)
	}
	before := s.index.Blocks[0]
	if err = s.publish(block{Key: "other", Kind: "detail", Operator: "metro", Age: now}, make([]byte, 600<<10), now); err == nil {
		t.Fatal("capacity admitted")
	}
	if len(s.index.Blocks) != 1 || s.index.Blocks[0].File != before.File {
		t.Fatal("recent data evicted")
	}
	if _, err = s.readBlock(before); err != nil {
		t.Fatal(err)
	}
}

func TestManifestRenameUncertaintyPreservesBothGenerations(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	v := MetroJourneyCheckpoint{Journey: "j", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"v":1}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{v}, now); err != nil {
		t.Fatal(err)
	}
	old := s.index.Blocks[0]
	s.manifestSync = func(string) error { return errors.New("synthetic directory sync failure") }
	v.Revision = 2
	v.Payload = json.RawMessage(`{"v":2}`)
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{v}, now); err == nil {
		t.Fatal("uncertain commit acknowledged")
	}
	if !s.manifestUncertain {
		t.Fatal("archive not fenced")
	}
	if _, err = os.Stat(filepath.Join(c.Directory, old.File)); err != nil {
		t.Fatal("old generation lost", err)
	}
	if _, state, err := s.MetroJourneyCheckpoint(context.Background(), v.Journey, now); err != nil || state != "restored" {
		t.Fatal("new manifest references missing generation", state, err)
	}
	if err = s.ensureSpace(0, now); err == nil {
		t.Fatal("uncertain archive accepted further writes")
	}
	if err = s.Close(); err == nil {
		t.Fatal("close omitted uncertainty")
	}
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, state, err := s.MetroJourneyCheckpoint(context.Background(), v.Journey, now)
	if err != nil || state != "restored" || got.Revision != 2 {
		t.Fatal("reopen did not resolve manifest", state, err)
	}
}

func TestRetentionBucketEndsAndClockSafety(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 30, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -7)
	for _, tc := range []struct {
		name, kind string
		age        time.Time
		want       bool
	}{
		{"hour straddles", "popup-events", cutoff.Truncate(time.Hour), false},
		{"hour ends at cutoff", "popup-events", cutoff.Truncate(time.Hour).Add(-time.Hour), true},
		{"input ends at cutoff", "metro-inputs", cutoff.Add(-5 * time.Minute), true},
		{"input straddles", "metro-inputs", cutoff, false},
		{"checkpoint equality", "popup-checkpoint", cutoff, false},
		{"checkpoint older", "popup-checkpoint", cutoff.Add(-time.Nanosecond), true},
		{"future", "popup-events", now.Add(time.Hour), false},
		{"missing", "popup-events", time.Time{}, false},
		{"invalid hour", "popup-events", cutoff.Add(-time.Minute), false},
		{"unknown correction", "correction", cutoff.Add(-time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := block{Kind: tc.kind, Age: tc.age, Operator: "metro"}
			switch tc.kind {
			case "popup-checkpoint":
				b.Key = "metro-popup-checkpoint:test"
			case "popup-events":
				b.Key = "metro-popup-events:" + tc.age.Format(time.RFC3339)
			case "metro-inputs":
				b.Key = "metro-inputs:" + tc.age.Format(time.RFC3339)
			}
			end, point, ok := metadataBoundary(b)
			got := ok && !end.After(now) && ((point && end.Before(cutoff)) || (!point && !end.After(cutoff)))
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	// A shorter explicitly configured detail retention remains effective.
	detail := block{Kind: "detail", Age: now.AddDate(0, 0, -2).Truncate(time.Hour).Add(-time.Hour)}
	detail.Key = "metro:detail:" + detail.Age.Format(time.RFC3339)
	if end, _, ok := metadataBoundary(detail); !ok || end.After(now.AddDate(0, 0, -2)) {
		t.Fatal("configured short normal retention boundary ignored")
	}
}

func TestOversizedCandidateRetriesNormalExpiryOnly(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Private archive helpers require the same owner lock as production callers.
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Hour)
	oldAge := now.AddDate(0, 0, -8)
	payload, err := s.encodeMetroCheckpoint(MetroJourneyCheckpoint{Journey: "old", Revision: 1, SourceAt: oldAge, Generation: "test", CommittedAt: oldAge, Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	filename := "metro-expired-fixture.json.zst"
	if err = os.WriteFile(filepath.Join(c.Directory, filename), payload, 0600); err != nil {
		t.Fatal(err)
	}
	old := block{Key: "metro-popup-checkpoint:" + strings.Repeat("a", maxManifestBytes-1024), Kind: "popup-checkpoint", Operator: "metro", Age: oldAge, File: filename, Hash: digestBytes(payload)}
	if err = s.saveManifest(manifest{Version: 1, Blocks: []block{old}}); err != nil {
		t.Fatal(err)
	}
	fresh := block{Key: "fresh:" + strings.Repeat("b", 2000), Kind: "detail", Operator: "metro", Age: now}
	if err = s.publish(fresh, []byte("fresh"), now); err != nil {
		t.Fatal(err)
	}
	if len(s.index.Blocks) != 1 || s.index.Blocks[0].Key != fresh.Key {
		t.Fatal("expiry did not make room")
	}
	if _, err = os.Stat(filepath.Join(c.Directory, filename)); !os.IsNotExist(err) {
		t.Fatal("expired generation not retired", err)
	}
}

func TestNestedClocksProtectArchiveGenerations(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 23, 12, 30, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -8).Truncate(time.Hour)
	recent := now.AddDate(0, 0, -1)
	checkpoint := MetroJourneyCheckpoint{Journey: "j", Revision: 1, SourceAt: old, Generation: "g", CommittedAt: old, Payload: json.RawMessage(fmt.Sprintf(`{"train":{"calls":[{"departure":{"inferred":{"confirmed_at":%q}}}]},"format":"json","numeric_id":9999999999999}`, recent.Format(time.RFC3339)))}
	blob, err := s.encodeMetroCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	b := block{Key: metroCheckpointKey("j"), Kind: "popup-checkpoint", Operator: "metro", Age: old}
	if err = s.publish(b, blob, old); err != nil {
		t.Fatal(err)
	}
	b = s.index.Blocks[0]
	if s.blockExpired(b, now) || s.pressureEligible(b, now) {
		t.Fatal("recent checkpoint evidence was retired")
	}
	if !s.pressureEligible(b, recent.AddDate(0, 0, 8)) {
		t.Fatal("old verified checkpoint did not age out")
	}

	// A captured Hub provider clock at the exclusive bucket end is still a
	// represented point and cannot be retired exactly at the seven-day cutoff.
	cutoff := now.AddDate(0, 0, -7)
	age := cutoff.Add(-5 * time.Minute)
	record := MetroEventRecord{ID: "fixture", At: age.Add(time.Second), Payload: json.RawMessage(fmt.Sprintf(`{"Models":[{"Position":{"created_at":%d},"PublishedAt":%q}],"format":"json","numeric_id":9999999999999}`, cutoff.UnixMilli(), cutoff.Format(time.RFC3339)))}
	if err = s.RecordMetroInputs(context.Background(), []MetroEventRecord{record}, age); err != nil {
		t.Fatal(err)
	}
	var input block
	for _, candidate := range s.index.Blocks {
		if candidate.Kind == "metro-inputs" {
			input = candidate
		}
	}
	if s.pressureEligible(input, now) {
		t.Fatal("nested clock equal to cutoff was retired")
	}
	if !s.pressureEligible(input, now.Add(time.Second)) {
		t.Fatal("verified provider input did not age out")
	}

	bad := record
	bad.ID = "malformed"
	bad.At = old
	bad.Payload = json.RawMessage(`{"Models":[{"Position":{"created_at":"unknown"}}]}`)
	if err = s.RecordMetroInputs(context.Background(), []MetroEventRecord{bad}, old); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range s.index.Blocks {
		if candidate.Kind == "metro-inputs" && candidate.Age.Equal(old) && s.pressureEligible(candidate, now) {
			t.Fatal("unverifiable clock was retired")
		}
	}
}

func TestRetentionCivilClockUsesLaterFallbackInstant(t *testing.T) {
	latest := time.Time{}
	if !scanClockText("20261025013000", true, &latest) || !latest.Equal(time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)) {
		t.Fatalf("fallback maximum = %s", latest)
	}
	if scanClockText("20260329013000", true, &latest) {
		t.Fatal("nonexistent local clock was treated as verified")
	}
}

func TestRetainedDirectFixtureCanExpire(t *testing.T) {
	f, err := os.Open("../app/testdata/metro-20260928/red-direction-direct.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	age := time.Date(2026, 9, 28, 23, 0, 0, 0, time.UTC)
	receipt, err := json.Marshal(Receipt{ReceivedAt: age.Add(20 * time.Minute), Operator: "metro", Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	b := block{Key: "metro:detail:" + age.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: age}
	if err = s.publish(b, s.encoder.EncodeAll(append(receipt, '\n'), nil), age); err != nil {
		t.Fatal(err)
	}
	s.config.DetailDays = 2
	if !s.blockExpired(s.index.Blocks[0], age.AddDate(0, 0, 3)) || s.pressureEligible(s.index.Blocks[0], age.AddDate(0, 0, 3)) {
		t.Fatal("short configured retention and pressure floor were not distinguished")
	}
	if !s.pressureEligible(s.index.Blocks[0], age.AddDate(0, 0, 30)) {
		t.Fatal("captured direct receipt did not age out")
	}
}

func TestAggregateVerifiedNewestAndLisbonDayBoundary(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		day   string
		hours time.Duration
	}{{"2026-03-29", 23 * time.Hour}, {"2026-10-25", 25 * time.Hour}} {
		t.Run(tc.day, func(t *testing.T) {
			start, err := time.ParseInLocation("2006-01-02", tc.day, lisbon)
			if err != nil {
				t.Fatal(err)
			}
			rows := []Aggregate{{Operator: "metro", Date: tc.day, KnownAt: start.UnixNano(), InputFrom: start.UnixNano()}}
			blob, err := verifiedAggregateBlob(rows)
			if err != nil {
				t.Fatal(err)
			}
			b := namedGeneration(block{Key: "aggregate:" + tc.day, Kind: "aggregate", Operator: "metro", Date: tc.day, Age: start.UTC()}, blob)
			if err = atomicFile(s.config.Directory, b.File, blob); err != nil {
				t.Fatal(err)
			}
			end, point, ok := s.blockBoundary(b)
			if !ok || point || end.Sub(start) != tc.hours {
				t.Fatalf("boundary=%s point=%v ok=%v", end, point, ok)
			}
		})
	}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	start, _ := time.ParseInLocation("2006-01-02", "2026-09-01", lisbon)
	rows := []Aggregate{{Operator: "metro", Date: "2026-09-01", KnownAt: now.Add(-time.Hour).UnixNano()}}
	blob, err := verifiedAggregateBlob(rows)
	if err != nil {
		t.Fatal(err)
	}
	b := namedGeneration(block{Key: "corrected", Kind: "aggregate", Operator: "metro", Date: "2026-09-01", Age: start.UTC()}, blob)
	if err = atomicFile(s.config.Directory, b.File, blob); err != nil {
		t.Fatal(err)
	}
	if s.pressureEligible(b, now) || s.blockExpired(b, now.AddDate(0, 0, 0)) {
		t.Fatal("recent correction not protected")
	}
	if err = os.Remove(filepath.Join(s.config.Directory, b.File)); err != nil {
		t.Fatal(err)
	}
	if s.pressureEligible(b, now.AddDate(0, 0, 8)) {
		t.Fatal("missing aggregate treated as verified")
	}
}

func TestOversizedManifestRejectedWithoutMutation(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	if err := os.WriteFile(filepath.Join(c.Directory, "manifest.json"), []byte(strings.Repeat("x", maxManifestBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(c.Directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s, err := Open(c); err == nil {
		s.Close()
		t.Fatal("oversized manifest opened")
	}
	after, err := os.ReadFile(filepath.Join(c.Directory, "manifest.json"))
	if err != nil || digestBytes(before) != digestBytes(after) {
		t.Fatal("failed open modified manifest", err)
	}
}

func TestArchiveBackupRestorePreservesCommittedCheckpoint(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	v := MetroJourneyCheckpoint{Journey: "backup", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"arrival":"inferred"}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{v}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restore := DefaultConfig(t.TempDir())
	entries, err := os.ReadDir(c.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		original, err := os.ReadFile(filepath.Join(c.Directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(restore.Directory, entry.Name())
		if err = os.WriteFile(dest, original, 0600); err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(dest)
		if err != nil || digestBytes(copied) != digestBytes(original) {
			t.Fatal("backup hash mismatch", entry.Name(), err)
		}
	}
	recovered, err := Open(restore)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	got, state, err := recovered.MetroJourneyCheckpoint(context.Background(), v.Journey, now)
	if err != nil || state != "restored" || got.Revision != v.Revision || string(got.Payload) != string(v.Payload) {
		t.Fatal("backup restore", state, err)
	}
}

func TestSinglePublicationUncertainRenamePreservesPrevious(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	b := block{Key: "metro:detail:" + now.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: now}
	if err = s.publish(b, []byte("old"), now); err != nil {
		t.Fatal(err)
	}
	old := s.index.Blocks[0]
	s.manifestSync = func(string) error { return errors.New("synthetic directory sync failure") }
	if err = s.publish(b, []byte("new"), now); err == nil {
		t.Fatal("uncertain publication accepted")
	}
	if err = s.publish(b, []byte("new"), now); err == nil {
		t.Fatal("identical retry acknowledged uncertain manifest")
	}
	if _, err = s.readBlock(old); err != nil {
		t.Fatal("old generation removed", err)
	}
	if _, err = s.readBlock(s.index.Blocks[0]); err != nil {
		t.Fatal("new generation removed", err)
	}
	_ = s.Close()
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.index.Blocks) != 1 || s.index.Blocks[0].Hash != digestBytes([]byte("new")) {
		t.Fatal("renamed manifest not restored")
	}
}

func TestCheckpointRejectsRegressingSourceClock(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Second)
	v := MetroJourneyCheckpoint{Journey: "clock", Revision: 1, SourceAt: now, Payload: json.RawMessage(`{"v":1}`)}
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{v}, now); err != nil {
		t.Fatal(err)
	}
	v.Revision++
	v.SourceAt = now.Add(-time.Second)
	if _, err = s.CommitMetroJourneys(context.Background(), []MetroJourneyCheckpoint{v}, now); err == nil {
		t.Fatal("regressed source clock committed")
	}
	got, state, err := s.MetroJourneyCheckpoint(context.Background(), v.Journey, now)
	if err != nil || state != "restored" || got.Revision != 1 {
		t.Fatal("prior checkpoint lost", state, err)
	}
}
