package patterns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func addMetroDetailFixture(t *testing.T, config Config, kind string, start time.Time, rows []Receipt) block {
	t.Helper()
	var raw bytes.Buffer
	for _, row := range rows {
		if err := json.NewEncoder(&raw).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	segmentPrefix := metroDetailSegmentPrefix
	if kind == "detail" {
		segmentPrefix = "metro:detail:"
	}
	blob := encoder.EncodeAll(raw.Bytes(), nil)
	b := namedGeneration(block{Key: segmentPrefix + start.Format(time.RFC3339), Kind: kind, Operator: "metro", Age: start, AsOf: rows[len(rows)-1].ReceivedAt, Samples: int64(len(rows)), Closed: true}, blob)
	if err = os.WriteFile(filepath.Join(config.Directory, b.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	m := readFixtureManifest(t, config.Directory)
	m.Blocks = append(m.Blocks, b)
	writeSegmentManifest(t, config.Directory, m)
	return b
}

func TestMetroSegmentRecordValidation(t *testing.T) {
	start := time.Date(2026, 10, 25, 0, 10, 0, 0, time.UTC)
	first := durabilityReceipt(start.Add(time.Minute), "first")
	for _, mode := range []string{"duplicate clock", "outside bucket", "sample count", "watermark", "unknown field", "missing newline"} {
		t.Run(mode, func(t *testing.T) {
			rows := []Receipt{first}
			b := block{Kind: metroDetailSegmentKind, Operator: "metro", Key: metroDetailSegmentPrefix + start.Format(time.RFC3339), Age: start, AsOf: first.ReceivedAt, Samples: 1}
			switch mode {
			case "duplicate clock":
				rows = append(rows, first)
				b.Samples = 2
			case "outside bucket":
				rows = append(rows, durabilityReceipt(start.Add(5*time.Minute), "outside"))
				b.Samples = 2
			case "sample count":
				b.Samples = 2
			case "watermark":
				b.AsOf = b.AsOf.Add(time.Second)
			}
			var raw bytes.Buffer
			for _, row := range rows {
				if err := json.NewEncoder(&raw).Encode(row); err != nil {
					t.Fatal(err)
				}
			}
			data := raw.Bytes()
			if mode == "unknown field" {
				data = bytes.Replace(data, []byte(`"received_at":`), []byte(`"future_field":true,"received_at":`), 1)
			}
			if mode == "missing newline" {
				data = bytes.TrimSuffix(data, []byte("\n"))
			}
			if _, err := decodeDetailRecords(data, b); err == nil {
				t.Fatal("invalid segment records accepted")
			}
		})
	}
}

func TestMetroSegmentUTCKeysRemainDistinctAcrossLisbonClockChange(t *testing.T) {
	first := time.Date(2026, 10, 25, 0, 10, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	if first.In(lisbon).Format("2006-01-02 15:04") != second.In(lisbon).Format("2006-01-02 15:04") {
		t.Fatal("fixture does not cover repeated Lisbon time")
	}
	for _, start := range []time.Time{first, second} {
		r := durabilityReceipt(start.Add(time.Minute), "clock-change")
		b := block{Kind: metroDetailSegmentKind, Operator: "metro", Key: metroDetailSegmentPrefix + start.Format(time.RFC3339), Age: start, AsOf: r.ReceivedAt, Samples: 1}
		var raw bytes.Buffer
		if err := json.NewEncoder(&raw).Encode(r); err != nil {
			t.Fatal(err)
		}
		if got, err := decodeDetailRecords(raw.Bytes(), b); err != nil || len(got) != 1 {
			t.Fatalf("UTC segment %s rejected: %v", start, err)
		}
	}
}

func TestMetroSegmentReadFailureFencesAndPreserves(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			hour := time.Now().UTC().Truncate(time.Hour)
			config := segmentBaseFixture(t, hour, true)
			start := hour.Add(-time.Hour)
			b := addMetroDetailFixture(t, config, metroDetailSegmentKind, start, []Receipt{durabilityReceipt(start.Add(time.Minute), "retained")})
			path := filepath.Join(config.Directory, b.File)
			var err error
			if mode == "missing" {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, []byte("corrupt"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			job := newMetroRevision(s, context.Background(), nil, hour)
			if err := job.loadFrames(); err == nil || s.detailFence == nil || !s.protectedGenerations[retentionKey(b)] {
				t.Fatalf("invalid segment read did not fence: %v", err)
			}
			if err := s.Close(); err == nil || !fixtureHasBlock(readFixtureManifest(t, config.Directory), b) {
				t.Fatalf("segment was replaced or fence bypassed: %v", err)
			}
		})
	}
}

func TestMetroSegmentUncertainOrphanIsPreserved(t *testing.T) {
	for _, mode := range []string{"bad name", "bad hash"} {
		t.Run(mode, func(t *testing.T) {
			hour := time.Now().UTC().Truncate(time.Hour)
			config := segmentBaseFixture(t, hour, true)
			name := "metro-" + metroDetailSegmentKind + "-unidentifiable.json.zst"
			if mode == "bad hash" {
				name = namedGeneration(block{Kind: metroDetailSegmentKind, Key: "test"}, []byte("different")).File
			}
			path := filepath.Join(config.Directory, name)
			if err := os.WriteFile(path, []byte("unknown evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			if s.detailFence == nil {
				t.Fatal("uncertain orphan did not fence")
			}
			if err := s.Close(); err == nil {
				t.Fatal("orphan fence was bypassed")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != "unknown evidence" {
				t.Fatalf("orphan changed: %v", err)
			}
		})
	}
}

func TestMetroSegmentClosesAtExclusiveEnd(t *testing.T) {
	start := time.Date(2026, 10, 24, 12, 10, 0, 0, time.UTC)
	s := &Service{index: manifest{Blocks: []block{{Kind: metroDetailSegmentKind, Age: start}}}}
	s.closeRetainedBlocks(start.Add(metroDetailSegmentInterval - time.Nanosecond))
	if s.index.Blocks[0].Closed {
		t.Fatal("segment closed before its end")
	}
	s.closeRetainedBlocks(start.Add(metroDetailSegmentInterval))
	if !s.index.Blocks[0].Closed {
		t.Fatal("segment remained open at its end")
	}
}

func BenchmarkMetroSegmentIdentityValidation(b *testing.B) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	s := &Service{}
	for i := 0; i < 30*24*12; i++ {
		age := start.Add(time.Duration(i) * metroDetailSegmentInterval)
		s.index.Blocks = append(s.index.Blocks, block{Kind: metroDetailSegmentKind, Key: metroDetailSegmentPrefix + age.Format(time.RFC3339), Operator: "metro", Age: age, AsOf: age.Add(time.Second), Samples: 1})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.validateRecoveryDetailIdentities(); err != nil {
			b.Fatal(err)
		}
	}
}

func writeSegmentManifest(t *testing.T, directory string, m manifest) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func segmentBaseFixture(t *testing.T, hour time.Time, checkpoint bool) Config {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 30
	rows := []Receipt{durabilityReceipt(hour.Add(time.Minute), "current")}
	var cp []byte
	if checkpoint {
		cp = fixtureCheckpoint(t, hour, rows)
	}
	writeDurabilityFixture(t, config, rows, cp)
	m := readFixtureManifest(t, config.Directory)
	m.Blocks[0].AsOf = rows[0].ReceivedAt
	writeSegmentManifest(t, config.Directory, m)
	return config
}

func TestMetroSegmentMixedHistoricalRead(t *testing.T) {
	hour := time.Now().UTC().Truncate(time.Hour)
	config := segmentBaseFixture(t, hour, true)
	legacyStart := hour.Add(-3 * time.Hour)
	segmentStart := hour.Add(-2 * time.Hour)
	legacy := addMetroDetailFixture(t, config, "detail", legacyStart, []Receipt{durabilityReceipt(legacyStart.Add(time.Minute), "legacy")})
	firstSegment := addMetroDetailFixture(t, config, metroDetailSegmentKind, segmentStart, []Receipt{durabilityReceipt(segmentStart.Add(time.Minute), "first"), durabilityReceipt(segmentStart.Add(2*time.Minute), "second")})
	secondSegment := addMetroDetailFixture(t, config, metroDetailSegmentKind, segmentStart.Add(5*time.Minute), []Receipt{durabilityReceipt(segmentStart.Add(6*time.Minute), "third")})
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	if s.detailFence != nil {
		t.Fatal(s.detailFence)
	}
	sources := []struct {
		block      block
		identities []string
	}{{legacy, []string{"legacy"}}, {firstSegment, []string{"first", "second"}}, {secondSegment, []string{"third"}}}
	for _, source := range sources {
		rows, readErr := s.readDetailBlock(source.block)
		if readErr != nil {
			t.Fatal(readErr)
		}
		assertReceiptIdentities(t, rows, source.identities...)
	}
	job := newMetroRevision(s, context.Background(), nil, hour)
	if err = job.loadFrames(); err != nil {
		t.Fatal(err)
	}
	want := []string{"legacy", "first", "second", "third", "current"}
	if len(job.frames) != len(want) {
		t.Fatalf("frames = %d, want %d", len(job.frames), len(want))
	}
	for i, name := range want {
		if job.frames[i].Rows[0].Train != name || job.frames[i].Raw != nil {
			t.Fatalf("frame %d = %+v", i, job.frames[i])
		}
	}
	summary := s.operatorSummary("metro")
	if summary.Samples != int64(len(want)) || summary.AsOf == nil || !summary.AsOf.Equal(hour.Add(time.Minute+time.Second)) {
		t.Fatalf("operator summary = %+v", summary)
	}
	if _, err = s.Reprocess(context.Background(), []Correction{{}}, hour); err == nil || !strings.Contains(err.Error(), "Metro reprocessing unavailable") {
		t.Fatalf("Metro reprocessing was unexpectedly enabled: %v", err)
	}
	if err = s.Record(durabilityReceipt(hour.Add(2*time.Minute), "next"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if s.detailFence != nil {
		t.Fatal(s.detailFence)
	}
	for _, source := range sources {
		rows, readErr := s.readDetailBlock(source.block)
		if readErr != nil {
			t.Fatal(readErr)
		}
		assertReceiptIdentities(t, rows, source.identities...)
	}
	rows, _ := detailForHour(t, s, hour)
	assertReceiptIdentities(t, rows, "current", "next")
}

func TestMetroSegmentReaderPreservesTailRecoveryBoundary(t *testing.T) {
	f := makeTailFixture(t, nil)
	if _, err := RecoverMetroTail(f.directory, f.request); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(f.directory)
	start := f.request.DetailHour.Add(-time.Hour)
	segment := addMetroDetailFixture(t, config, metroDetailSegmentKind, start, []Receipt{durabilityReceipt(start.Add(time.Minute), "historical")})
	for attempt := 0; attempt < 2; attempt++ {
		s, err := Open(config)
		if err != nil {
			t.Fatal(err)
		}
		if s.detailFence != nil || s.tailRecovery == nil || s.tailRecovery.PriorState != f.state ||
			!strings.Contains(s.metroViewMetadata().Message, "Histórico parcial") {
			t.Fatalf("restart %d lost the recovery boundary: %v", attempt, s.detailFence)
		}
		original, err := os.ReadFile(filepath.Join(f.directory, f.state.File))
		if err != nil || digestBytes(original) != f.state.Hash {
			t.Fatalf("restart %d lost the pinned source: %v", attempt, err)
		}
		rows, err := s.readDetailBlock(segment)
		if err != nil {
			t.Fatal(err)
		}
		assertReceiptIdentities(t, rows, "historical")
		if attempt == 0 {
			if err = s.Record(durabilityReceipt(f.request.TailAt.Add(31*time.Second), "after"), testTopology()); err != nil {
				t.Fatal(err)
			}
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMetroTailRecoveryRejectsSegmentedArchivesWithoutWriting(t *testing.T) {
	cases := []struct {
		name   string
		offset time.Duration
		alter  string
	}{
		{"historical", -time.Hour, ""},
		{"same hour", 5 * time.Minute, ""},
		{"later hour", time.Hour, ""},
		{"malformed key", -time.Hour, "key"},
		{"wrong kind", -time.Hour, "kind"},
	}
	for _, applied := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("applied=%t/%s", applied, tc.name), func(t *testing.T) {
				f := makeTailFixture(t, nil)
				if applied {
					if _, err := RecoverMetroTail(f.directory, f.request); err != nil {
						t.Fatal(err)
					}
				}
				config := DefaultConfig(f.directory)
				start := f.request.DetailHour.Add(tc.offset)
				addMetroDetailFixture(t, config, metroDetailSegmentKind, start, []Receipt{durabilityReceipt(start.Add(time.Minute), "segment")})
				m := readFixtureManifest(t, f.directory)
				if tc.alter == "key" {
					m.Blocks[len(m.Blocks)-1].Key += ":invalid"
				} else if tc.alter == "kind" {
					m.Blocks[len(m.Blocks)-1].Kind = "correction"
				}
				writeSegmentManifest(t, f.directory, m)
				if !applied {
					manifest, err := os.ReadFile(filepath.Join(f.directory, "manifest.json"))
					if err != nil {
						t.Fatal(err)
					}
					f.request.ManifestSHA256 = digestBytes(manifest)
				}
				before := recoverySegmentInventory(t, f.directory)
				if _, err := RecoverMetroTail(f.directory, f.request); err == nil || !strings.Contains(err.Error(), "segmented Metro detail") {
					t.Fatalf("segmented archive was not rejected: %v", err)
				}
				if !reflect.DeepEqual(before, recoverySegmentInventory(t, f.directory)) {
					t.Fatal("rejected recovery modified the archive")
				}
			})
		}
	}
}

type recoverySegmentEvidence struct {
	hash string
	mode os.FileMode
}

func recoverySegmentInventory(t *testing.T, directory string) map[string]recoverySegmentEvidence {
	t.Helper()
	files := map[string]recoverySegmentEvidence{}
	for name, hash := range inventory(t, directory) {
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = recoverySegmentEvidence{hash, info.Mode()}
	}
	return files
}

func TestMetroSegmentUnprovenOrAmbiguousSourcesArePreserved(t *testing.T) {
	for _, mode := range []string{"no checkpoint", "missing checkpoint", "current hour", "after checkpoint", "mixed hour", "duplicate", "wrong kind", "bad key"} {
		t.Run(mode, func(t *testing.T) {
			config, b, m := ambiguousSegmentFixture(t, mode)
			before, err := os.ReadFile(filepath.Join(config.Directory, b.File))
			if err != nil {
				t.Fatal(err)
			}
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			assertAmbiguousSegmentFenced(t, s, m, b, mode == "mixed hour")
			s.mu.Lock()
			_, _ = s.expireSome(time.Now().UTC(), &cleanupWork{})
			_ = s.ensureSpaceFor(s.config.LimitBytes, 0, time.Now().UTC(), &cleanupWork{})
			s.mu.Unlock()
			if err = s.Close(); err == nil {
				t.Fatal("close bypassed detail fence")
			}
			s, err = Open(config)
			if err != nil {
				t.Fatal(err)
			}
			assertAmbiguousSegmentFenced(t, s, m, b, mode == "mixed hour")
			if err = s.Close(); err == nil {
				t.Fatal("reopened service bypassed detail fence")
			}
			after, err := os.ReadFile(filepath.Join(config.Directory, b.File))
			if err != nil || !bytes.Equal(before, after) || !fixtureHasBlock(readFixtureManifest(t, config.Directory), b) {
				t.Fatalf("protected evidence changed: %v", err)
			}
		})
	}
}

func ambiguousSegmentFixture(t *testing.T, mode string) (Config, block, manifest) {
	t.Helper()
	hour := time.Now().UTC().AddDate(0, 0, -8).Truncate(time.Hour)
	config := segmentBaseFixture(t, hour, mode != "no checkpoint")
	start := hour.Add(-time.Hour)
	if mode == "current hour" || mode == "mixed hour" {
		start = hour.Add(5 * time.Minute)
	}
	if mode == "after checkpoint" {
		start = hour.Add(time.Hour)
	}
	b := addMetroDetailFixture(t, config, metroDetailSegmentKind, start, []Receipt{durabilityReceipt(start.Add(time.Minute), "segment")})
	m := readFixtureManifest(t, config.Directory)
	switch mode {
	case "duplicate":
		m.Blocks = append(m.Blocks, b)
	case "wrong kind":
		m.Blocks[len(m.Blocks)-1].Kind = "correction"
	case "bad key":
		m.Blocks[len(m.Blocks)-1].Key += ":invalid"
	}
	writeSegmentManifest(t, config.Directory, m)
	if mode == "missing checkpoint" {
		for _, state := range m.Blocks {
			if state.Kind == "state" {
				if err := os.Remove(filepath.Join(config.Directory, state.File)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return config, b, m
}

func assertAmbiguousSegmentFenced(t *testing.T, s *Service, m manifest, segment block, mixed bool) {
	t.Helper()
	if s.detailFence == nil {
		t.Fatal("unproven source was accepted")
	}
	for _, candidate := range m.Blocks {
		if candidate.File == segment.File && !s.protectedGenerations[retentionKey(candidate)] {
			t.Fatal("segment is not protected")
		}
		if mixed && candidate.Kind == "detail" && !s.protectedGenerations[retentionKey(candidate)] {
			t.Fatal("overlapping legacy hour is not protected")
		}
	}
}

func TestMetroSegmentRetentionUsesBoundariesAndVerifiedContents(t *testing.T) {
	now := time.Now().UTC().Truncate(5 * time.Minute)
	config := segmentBaseFixture(t, now.Truncate(time.Hour), true)
	cutoff := now.AddDate(0, 0, -7)
	old := cutoff.Add(-5 * time.Minute)
	eligible := addMetroDetailFixture(t, config, metroDetailSegmentKind, old, []Receipt{durabilityReceipt(old.Add(time.Minute), "eligible")})
	protected := addMetroDetailFixture(t, config, metroDetailSegmentKind, cutoff, []Receipt{durabilityReceipt(cutoff.Add(time.Minute), "protected")})
	futureStart := old.Add(-5 * time.Minute)
	futureReceipt := durabilityReceipt(futureStart.Add(time.Minute), "future-clock")
	futureReceipt.Raw = json.RawMessage(fmt.Sprintf(`{"created_at":%q}`, now.Format(time.RFC3339)))
	future := addMetroDetailFixture(t, config, metroDetailSegmentKind, futureStart, []Receipt{futureReceipt})
	badStart := futureStart.Add(-5 * time.Minute)
	corrupt := addMetroDetailFixture(t, config, metroDetailSegmentKind, badStart, []Receipt{durabilityReceipt(badStart.Add(time.Minute), "corrupt")})
	if err := os.WriteFile(filepath.Join(config.Directory, corrupt.File), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil {
		t.Fatal(s.detailFence)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pressureEligible(eligible, now) || s.pressureEligible(protected, now) || s.pressureEligible(future, now) || s.pressureEligible(corrupt, now) {
		t.Fatal("seven-day cutoff or contents were not respected")
	}
	index := -1
	for i, b := range s.index.Blocks {
		if b.File == eligible.File {
			index = i
		}
	}
	if index < 0 || s.retireVerified(index, now, true, &cleanupWork{}) != nil {
		t.Fatal("eligible segment was not retired")
	}
	if _, err := os.Stat(filepath.Join(config.Directory, eligible.File)); !os.IsNotExist(err) {
		t.Fatalf("retired segment remains: %v", err)
	}
	for _, b := range []block{protected, future, corrupt} {
		if _, err := os.Stat(filepath.Join(config.Directory, b.File)); err != nil {
			t.Fatalf("protected segment removed: %v", err)
		}
	}
}

func TestMetroSegmentMaintenanceRemovesOnlyEligibleHistory(t *testing.T) {
	for _, mode := range []string{"expiry", "pressure"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Hour)
			config := segmentBaseFixture(t, now, true)
			old := now.AddDate(0, 0, -8)
			if mode == "expiry" {
				config.DetailDays = 1
				old = now.AddDate(0, 0, -2)
			}
			retired := addMetroDetailFixture(t, config, metroDetailSegmentKind, old, []Receipt{durabilityReceipt(old.Add(time.Minute), "retired")})
			newer := now.Add(-time.Hour)
			kept := addMetroDetailFixture(t, config, metroDetailSegmentKind, newer, []Receipt{durabilityReceipt(newer.Add(time.Minute), "kept")})
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.detailFence != nil {
				t.Fatal(s.detailFence)
			}
			if mode == "pressure" {
				s.mu.Lock()
				used, err := s.allocated()
				if err == nil {
					err = s.ensureSpaceFor(s.config.LimitBytes-used+1, 0, now, &cleanupWork{})
				}
				s.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(filepath.Join(config.Directory, retired.File)); !os.IsNotExist(err) {
				t.Fatalf("eligible history was not removed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(config.Directory, kept.File)); err != nil {
				t.Fatalf("recent history was removed: %v", err)
			}
		})
	}
}
