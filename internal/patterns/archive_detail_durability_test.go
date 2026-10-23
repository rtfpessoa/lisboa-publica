package patterns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func durabilityReceipt(at time.Time, identity string) Receipt {
	r := testReceipt(at, testRow("A", identity, at, 100))
	r.Raw = json.RawMessage(fmt.Sprintf(`[{"identity":%q,"extra":{"value":7}}]`, identity))
	return r
}

func detailForHour(t *testing.T, s *Service, hour time.Time) ([]Receipt, block) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.detailBlock(hour)
	if err != nil || b == nil {
		t.Fatalf("detail source: %v", err)
	}
	rows, err := s.readDetailBlock(*b)
	if err != nil {
		t.Fatal(err)
	}
	return rows, *b
}

func assertReceiptIdentities(t *testing.T, rows []Receipt, identities ...string) {
	t.Helper()
	if len(rows) != len(identities) {
		t.Fatalf("got %d rows, want %d", len(rows), len(identities))
	}
	for i, identity := range identities {
		if !bytes.Contains(rows[i].Raw, []byte(fmt.Sprintf(`"identity":%q`, identity))) {
			t.Fatalf("row %d does not retain %q", i, identity)
		}
	}
}

func TestDetailSurvivesFailureAfterItsPublication(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	for i, id := range []string{"first", "second"} {
		if err = s.Record(durabilityReceipt(base.Add(time.Duration(i)*31*time.Second), id), testTopology()); err != nil {
			t.Fatal(err)
		}
	}
	blocker := filepath.Join(config.Directory, ".manifest.tmp")
	injected := false
	s.mu.Lock()
	s.manifestSync = func(directory string) error {
		if !injected {
			injected = true
			if err := os.WriteFile(blocker, []byte("later publication failure"), 0600); err != nil {
				return err
			}
		}
		return syncDirectory(directory)
	}
	s.mu.Unlock()
	if err = s.Record(durabilityReceipt(base.Add(62*time.Second), "third"), testTopology()); err == nil || !injected {
		t.Fatalf("expected later flush failure: %v", err)
	}
	rows, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "first", "second", "third")
	s.mu.Lock()
	if len(s.detail) != 3 {
		s.mu.Unlock()
		t.Fatal("failed flush discarded retained buffer")
	}
	s.manifestSync = nil
	s.mu.Unlock()
	if err = os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err = s.Record(durabilityReceipt(base.Add(93*time.Second), "fourth"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, _ = detailForHour(t, reopened, base.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "first", "second", "third", "fourth")
}

func TestDetailSurvivesPostCommitDirectorySyncFailure(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(durabilityReceipt(base, "first"), testTopology()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.publicationSync = func(string) error { return errors.New("injected final directory sync failure") }
	s.mu.Unlock()
	if err = s.Record(durabilityReceipt(base.Add(61*time.Second), "second"), testTopology()); err == nil {
		t.Fatal("expected post-commit sync failure")
	}
	rows, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "first", "second")
	s.mu.Lock()
	if len(s.detail) != 2 || s.manifestUncertain {
		s.mu.Unlock()
		t.Fatal("post-commit failure discarded detail or incorrectly fenced rename")
	}
	s.publicationSync = nil
	s.mu.Unlock()
	if err = s.Record(durabilityReceipt(base.Add(92*time.Second), "third"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, _ = detailForHour(t, reopened, base.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "first", "second", "third")
}

func writeDurabilityFixture(t *testing.T, config Config, rows []Receipt, checkpointRaw []byte) {
	t.Helper()
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	var raw bytes.Buffer
	for _, r := range rows {
		if err = json.NewEncoder(&raw).Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	hour := rows[0].ReceivedAt.UTC().Truncate(time.Hour)
	blob := encoder.EncodeAll(raw.Bytes(), nil)
	b := namedGeneration(block{Key: "metro:detail:" + hour.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: hour, Samples: int64(len(rows))}, blob)
	blocks := []block{b}
	if err = os.WriteFile(filepath.Join(config.Directory, b.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	if checkpointRaw != nil {
		blob = encoder.EncodeAll(checkpointRaw, nil)
		state := namedGeneration(block{Key: "metro:state", Kind: "state", Operator: "metro", Age: hour}, blob)
		if err = os.WriteFile(filepath.Join(config.Directory, state.File), blob, 0600); err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, state)
	}
	data, err := json.Marshal(manifest{Version: 1, Blocks: blocks})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config.Directory, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func fixtureCheckpoint(t *testing.T, hour time.Time, rows []Receipt) []byte {
	t.Helper()
	cp := checkpoint{Providers: map[string]*providerState{}, Engine: newEngine(), Hour: hour, Detail: rows, BinSeconds: 30, SampleSeconds: 30}
	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRecoveryUsesCompleteDetailWithOrWithoutCheckpoint(t *testing.T) {
	for _, checkpointPresent := range []bool{false, true} {
		t.Run(fmt.Sprintf("checkpoint_%t", checkpointPresent), func(t *testing.T) {
			config := DefaultConfig(t.TempDir())
			base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
			rows := []Receipt{durabilityReceipt(base, "first"), durabilityReceipt(base.Add(31*time.Second), "second")}
			var cp []byte
			if checkpointPresent {
				cp = fixtureCheckpoint(t, base.Truncate(time.Hour), rows[:1])
			}
			writeDurabilityFixture(t, config, rows, cp)
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.detailFence != nil {
				t.Fatal(s.detailFence)
			}
			assertReceiptIdentities(t, s.detail, "first", "second")
			if err = s.Record(durabilityReceipt(base.Add(62*time.Second), "third"), testTopology()); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			got, _ := detailForHour(t, reopened, base.Truncate(time.Hour))
			assertReceiptIdentities(t, got, "first", "second", "third")
		})
	}
}
