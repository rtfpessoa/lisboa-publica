package patterns

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestRestartGapWaitsForNextEligibleReceipt(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Record(durabilityReceipt(base, "before"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if !s.pendingMetroDeliveryGap || !s.MetroCut().Snapshot().Pending || s.detailFence != nil {
		t.Fatal("verified restart did not establish its cut")
	}
	for _, at := range []time.Time{base, base.Add(time.Second)} {
		result, recordErr := s.RecordMetro(durabilityReceipt(at, "unsampled"), testTopology())
		if recordErr != nil || result.Appended || result.Kind != MetroUnsampled || !s.pendingMetroDeliveryGap {
			t.Fatalf("restart barrier was consumed too early: %+v %v", result, recordErr)
		}
	}
	result, err := s.RecordMetro(durabilityReceipt(base.Add(30*time.Second), "after"), testTopology())
	if err != nil || !result.Appended || result.Kind != MetroGapSampled || !result.Acknowledged {
		t.Fatalf("eligible restart barrier failed: %+v %v", result, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "before", "after")
	if !rows[1].DeliveryGap || !s.pendingMetroDeliveryGap {
		t.Fatal("persisted barrier or next restart cut was missing")
	}
}

func TestCheckpointOnlyDetailPreservesRowsAndStartsWithCut(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	rows := []Receipt{durabilityReceipt(base, "checkpoint-only")}
	writeDurabilityFixture(t, config, rows, fixtureCheckpoint(t, base.Truncate(time.Hour), rows))
	m := readFixtureManifest(t, config.Directory)
	if err := os.Remove(filepath.Join(config.Directory, m.Blocks[0].File)); err != nil {
		t.Fatal(err)
	}
	m.Blocks = m.Blocks[1:]
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config.Directory, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.detailFence != nil || !s.pendingMetroDeliveryGap || !s.lastSample.Equal(rows[0].ReceivedAt) {
		t.Fatalf("checkpoint-only recovery: fence=%v pending=%v sample=%v want=%v", s.detailFence, s.pendingMetroDeliveryGap, s.lastSample, rows[0].ReceivedAt)
	}
	result, err := s.RecordMetro(durabilityReceipt(base.Add(30*time.Second), "new"), testTopology())
	if err != nil || !result.Appended || result.Kind != MetroGapSampled {
		t.Fatalf("checkpoint-only barrier failed: %+v %v", result, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	retained, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, retained, "checkpoint-only", "new")
}

func TestUnprovenTailProtectsItsSourcesButAllowsOlderPressureExpiry(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 30
	now := time.Now().UTC().Truncate(time.Hour)
	base := now.Add(-time.Hour).Add(time.Minute)
	rows := []Receipt{durabilityReceipt(base, "checkpoint"), durabilityReceipt(base.Add(31*time.Second), "tail")}
	writeDurabilityFixture(t, config, rows, fixtureCheckpoint(t, base.Truncate(time.Hour), rows[:1]))
	m := readFixtureManifest(t, config.Directory)
	oldHour := now.AddDate(0, 0, -8)
	oldReceipt := durabilityReceipt(oldHour.Add(time.Minute), "old")
	raw, err := json.Marshal(oldReceipt)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	blob := encoder.EncodeAll(append(raw, '\n'), nil)
	encoder.Close()
	old := namedGeneration(block{Kind: "detail", Operator: "metro", Age: oldHour, Key: "metro:detail:" + oldHour.Format(time.RFC3339), Samples: 1}, blob)
	if err = os.WriteFile(filepath.Join(config.Directory, old.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	m.Blocks = append(m.Blocks, old)
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config.Directory, "manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.detailFence == nil || s.protectedGenerations[retentionKey(old)] {
		t.Fatal("unrelated older generation was unnecessarily fenced")
	}
	for _, b := range m.Blocks[:2] {
		if !s.protectedGenerations[retentionKey(b)] {
			t.Fatalf("tail source not protected: %s", b.Key)
		}
	}
	s.mu.Lock()
	_ = s.ensureSpaceFor(s.config.LimitBytes, 0, now, &cleanupWork{})
	s.mu.Unlock()
	if _, err = os.Stat(filepath.Join(config.Directory, old.File)); !os.IsNotExist(err) {
		t.Fatalf("eligible old detail was not reclaimed: %v", err)
	}
	for _, b := range m.Blocks[:2] {
		if _, err = os.Stat(filepath.Join(config.Directory, b.File)); err != nil {
			t.Fatalf("protected tail source removed: %v", err)
		}
	}
}
