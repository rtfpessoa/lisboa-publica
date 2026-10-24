package patterns

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func segmentCheckpointFixture(t *testing.T, start time.Time, mode string) (Config, block) {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 1
	rows := []Receipt{durabilityReceipt(start.Add(time.Minute), "first"), durabilityReceipt(start.Add(2*time.Minute), "second")}
	writeSegmentManifest(t, config.Directory, manifest{Version: 1})
	current := addMetroDetailFixture(t, config, metroDetailSegmentKind, start, rows)
	if mode == "missing" {
		if err := os.Remove(filepath.Join(config.Directory, current.File)); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "corrupt" {
		if err := os.WriteFile(filepath.Join(config.Directory, current.File), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "mismatch" {
		rows = rows[:1]
	}
	engine := newEngine()
	engine.LastReceipt = rows[len(rows)-1].ReceivedAt
	cp := checkpoint{Providers: map[string]*providerState{}, Engine: engine, Hour: start.Truncate(time.Hour), Detail: rows, SegmentStart: &start, BinSeconds: 30, SampleSeconds: 30}
	raw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	blob := encoder.EncodeAll(raw, nil)
	state := namedGeneration(block{Key: "metro:state", Kind: "state", Operator: "metro", Age: engine.LastReceipt}, blob)
	if err := os.WriteFile(filepath.Join(config.Directory, state.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	m := readFixtureManifest(t, config.Directory)
	markerBlob := encoder.EncodeAll([]byte(`{"version":1}`), nil)
	marker := namedGeneration(block{Key: metroSegmentFormatKey, Kind: metroSegmentFormatKind, Operator: "metro", Age: metroSegmentFormatAge}, markerBlob)
	if err := os.WriteFile(filepath.Join(config.Directory, marker.File), markerBlob, 0600); err != nil {
		t.Fatal(err)
	}
	m.Blocks = append(m.Blocks, marker)
	m.Blocks = append(m.Blocks, state)
	writeSegmentManifest(t, config.Directory, m)
	return config, current
}

func TestMetroSegmentCheckpointRestoresAndPinsActiveDetail(t *testing.T) {
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -8).Truncate(time.Hour).Add(10 * time.Minute)
	config, active := segmentCheckpointFixture(t, start, "")
	for attempt := 0; attempt < 2; attempt++ {
		s, err := Open(config)
		if err != nil {
			t.Fatal(err)
		}
		if s.detailFence != nil || !s.segmentStart.Equal(start) || len(s.detail) != 2 {
			t.Fatalf("restart %d could not restore active segment: %v", attempt, s.detailFence)
		}
		s.mu.Lock()
		for _, pressure := range []bool{false, true} {
			for _, index := range s.retirementCandidates(now, pressure) {
				if s.index.Blocks[index].File == active.File {
					t.Fatal("active checkpoint segment eligible for retirement")
				}
			}
		}
		index := -1
		for i, b := range s.index.Blocks {
			if b.File == active.File {
				index = i
			}
		}
		if index < 0 || s.retireVerified(index, now, true, &cleanupWork{}) != errRetentionChanged {
			t.Fatal("active segment was not pinned")
		}
		s.mu.Unlock()
		if _, err := os.Stat(filepath.Join(config.Directory, active.File)); err != nil {
			t.Fatal("active segment was removed", err)
		}
		// Avoid flushing until the segmented writer is installed.
		s.detailFence = errRetentionChanged
		_ = s.Close()
	}
}

func TestMetroSegmentCheckpointFencesMissingOrConflictingDetail(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	for _, mode := range []string{"missing", "corrupt", "mismatch"} {
		t.Run(mode, func(t *testing.T) {
			config, active := segmentCheckpointFixture(t, start, mode)
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.detailFence == nil {
				t.Fatal("accepted unproven active segment")
			}
			if mode != "missing" {
				if _, err := os.Stat(filepath.Join(config.Directory, active.File)); err != nil {
					t.Fatal("conflicting evidence removed", err)
				}
			}
		})
	}
}
