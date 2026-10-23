package patterns

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func replaceFixtureDetail(t *testing.T, directory string, blob []byte) block {
	t.Helper()
	m := readFixtureManifest(t, directory)
	previous := m.Blocks[0]
	replacement := namedGeneration(previous, blob)
	if err := os.WriteFile(filepath.Join(directory, replacement.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, previous.File)); err != nil {
		t.Fatal(err)
	}
	m.Blocks[0] = replacement
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return replacement
}

func malformedDetail(t *testing.T, mode string, base time.Time) []byte {
	t.Helper()
	if mode == "bad zstd" {
		return []byte("not compressed")
	}
	var raw bytes.Buffer
	switch mode {
	case "truncated json":
		raw.WriteString(`{"received_at":`)
	case "unknown field":
		row, err := json.Marshal(durabilityReceipt(base, "retained"))
		if err != nil {
			t.Fatal(err)
		}
		raw.Write(bytes.TrimSuffix(row, []byte("}")))
		raw.WriteString(",\"future_field\":true}\n")
	case "too many":
		encoder := json.NewEncoder(&raw)
		for i := 0; i <= maxDetailHourReceipts; i++ {
			row := Receipt{ReceivedAt: base.Add(time.Duration(i) * time.Second), Operator: "metro"}
			if err := encoder.Encode(row); err != nil {
				t.Fatal(err)
			}
		}
	case "too large":
		raw.Write(bytes.Repeat([]byte{' '}, maxDetailHourBytes+1))
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(raw.Bytes(), nil)
}

func TestUnreadableAndOversizedDetailIsNeverReplaced(t *testing.T) {
	for _, mode := range []string{"bad zstd", "truncated json", "unknown field", "too many", "too large"} {
		t.Run(mode, func(t *testing.T) {
			config := DefaultConfig(t.TempDir())
			base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
			rows := []Receipt{durabilityReceipt(base, "retained")}
			writeDurabilityFixture(t, config, rows, fixtureCheckpoint(t, base.Truncate(time.Hour), rows))
			suspect := replaceFixtureDetail(t, config.Directory, malformedDetail(t, mode, base))
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			if s.detailFence == nil {
				t.Fatal("unreadable detail accepted")
			}
			if err = s.Record(durabilityReceipt(base.Add(time.Hour), "later"), testTopology()); err == nil {
				t.Fatal("rollover bypassed fence")
			}
			if err = s.Close(); err == nil {
				t.Fatal("close bypassed fence")
			}
			if !fixtureHasBlock(readFixtureManifest(t, config.Directory), suspect) {
				t.Fatal("unreadable detail replaced")
			}
			if _, err = os.Stat(filepath.Join(config.Directory, suspect.File)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMissingReferencedCheckpointFencesUntilRestored(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	rows := []Receipt{durabilityReceipt(base, "retained")}
	writeDurabilityFixture(t, config, rows, fixtureCheckpoint(t, base.Truncate(time.Hour), rows))
	m := readFixtureManifest(t, config.Directory)
	statePath := filepath.Join(config.Directory, m.Blocks[1].File)
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if s.detailFence == nil {
		t.Fatal("missing referenced checkpoint accepted")
	}
	if err = s.Close(); err == nil {
		t.Fatal("missing checkpoint overwritten")
	}
	if err = os.WriteFile(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil {
		t.Fatal(s.detailFence)
	}
	if err = s.Record(durabilityReceipt(base.Add(31*time.Second), "later"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, got, "retained", "later")
}

func TestCheckpointWithLongerVerifiedPrefixIsRetained(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	rows := []Receipt{durabilityReceipt(base, "first"), durabilityReceipt(base.Add(31*time.Second), "checkpoint only")}
	writeDurabilityFixture(t, config, rows[:1], fixtureCheckpoint(t, base.Truncate(time.Hour), rows))
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil {
		t.Fatal(s.detailFence)
	}
	assertReceiptIdentities(t, s.detail, "first", "checkpoint only")
	if err = s.Record(durabilityReceipt(base.Add(62*time.Second), "later"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, got, "first", "checkpoint only", "later")
}

func TestMultiplePostCheckpointHoursFenceWithoutRetirement(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 1
	base := time.Now().UTC().AddDate(0, 0, -8).Truncate(time.Hour).Add(5 * time.Minute)
	first := durabilityReceipt(base, "checkpoint hour")
	writeDurabilityFixture(t, config, []Receipt{first}, fixtureCheckpoint(t, base.Truncate(time.Hour), []Receipt{first}))
	m := readFixtureManifest(t, config.Directory)
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	for i := 1; i <= 2; i++ {
		row := durabilityReceipt(base.Add(time.Duration(i)*time.Hour), "later hour")
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		blob := encoder.EncodeAll(append(raw, '\n'), nil)
		hour := row.ReceivedAt.UTC().Truncate(time.Hour)
		b := namedGeneration(block{Key: "metro:detail:" + hour.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: hour, Samples: 1}, blob)
		if err = os.WriteFile(filepath.Join(config.Directory, b.File), blob, 0600); err != nil {
			t.Fatal(err)
		}
		m.Blocks = append(m.Blocks, b)
	}
	manifestRaw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config.Directory, "manifest.json"), manifestRaw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if s.detailFence == nil {
		t.Fatal("multi-hour recovery did not fence")
	}
	s.mu.Lock()
	_, _ = s.expireSome(time.Now().UTC(), &cleanupWork{})
	_ = s.ensureSpaceFor(s.config.LimitBytes, 0, time.Now().UTC(), &cleanupWork{})
	s.mu.Unlock()
	if err = s.Record(durabilityReceipt(base.Add(3*time.Hour), "later"), testTopology()); err == nil {
		t.Fatal("record bypassed recovery fence")
	}
	if err = s.Close(); err == nil {
		t.Fatal("close overwrote unresolved checkpoint")
	}
	after := readFixtureManifest(t, config.Directory)
	for _, b := range m.Blocks {
		if !fixtureHasBlock(after, b) {
			t.Fatalf("unresolved %s removed", b.Key)
		}
		if _, err = os.Stat(filepath.Join(config.Directory, b.File)); err != nil {
			t.Fatal(err)
		}
	}
}
