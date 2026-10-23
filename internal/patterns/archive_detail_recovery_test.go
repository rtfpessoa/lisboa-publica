package patterns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyCheckpointAllowsAddedZeroFields(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	rows := []Receipt{durabilityReceipt(base, "legacy")}
	checkpoint := fixtureCheckpoint(t, base.Truncate(time.Hour), rows)
	// The previous engine layout, before a72fa9e, did not contain this field.
	legacy := bytes.Replace(checkpoint, []byte(`,"IntermediateCuts":0`), nil, 1)
	if bytes.Equal(legacy, checkpoint) {
		t.Fatal("legacy checkpoint fixture not modified")
	}
	writeDurabilityFixture(t, config, rows, legacy)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil {
		t.Fatal(s.detailFence)
	}
	if err := s.Record(durabilityReceipt(base.Add(31*time.Second), "new"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, got, "legacy", "new")
}

func TestJSONPreservationRejectsUnknownDuplicateAndChangedValues(t *testing.T) {
	for _, raw := range []string{
		`{"received_at":"2026-10-23T12:00:00Z","operator":"metro","profile":"","service_condition":"","rows":null,"unknown":true}`,
		`{"received_at":"2026-10-23T12:00:00Z","operator":"metro","operator":"metro","profile":"","service_condition":"","rows":null}`,
		`{"received_at":"2026-10-23T12:00:00Z","operator":"metro","profile":"","service_condition":"","rows":null,"raw":{"x":1,"x":1}}`,
	} {
		var receipt Receipt
		if err := knownJSON([]byte(raw), &receipt); err == nil {
			t.Fatalf("accepted ambiguous JSON %s", raw)
		}
	}
}

func TestInvalidDetailRecoveryFencesAndPreservesEvidence(t *testing.T) {
	for _, mode := range []string{"missing", "corrupt", "conflicting checkpoint", "unknown checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			config := DefaultConfig(t.TempDir())
			base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
			rows := []Receipt{durabilityReceipt(base, "retained")}
			cpRows := rows
			if mode == "conflicting checkpoint" {
				cpRows = []Receipt{durabilityReceipt(base, "different")}
			}
			checkpoint := fixtureCheckpoint(t, base.Truncate(time.Hour), cpRows)
			if mode == "unknown checkpoint" {
				checkpoint = bytes.Replace(checkpoint, []byte(`"BinSeconds":30`), []byte(`"future_field":1,"BinSeconds":30`), 1)
			}
			writeDurabilityFixture(t, config, rows, checkpoint)
			m := readFixtureManifest(t, config.Directory)
			detail, state := m.Blocks[0], m.Blocks[1]
			detailPath := filepath.Join(config.Directory, detail.File)
			if mode == "missing" {
				if err := os.Remove(detailPath); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "corrupt" {
				if err := os.WriteFile(detailPath, []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(config.Directory, state.File))
			if err != nil {
				t.Fatal(err)
			}
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			if s.detailFence == nil {
				t.Fatal("expected recovery fence")
			}
			if err = s.Record(durabilityReceipt(base.Add(31*time.Second), "later"), testTopology()); err == nil {
				t.Fatal("record bypassed fence")
			}
			s.mu.Lock()
			_, _ = s.expireSome(time.Now().UTC(), &cleanupWork{})
			s.mu.Unlock()
			view, err := s.View(context.Background(), "")
			if err != nil || view.Status != "paused" {
				t.Fatalf("status after maintenance = %s: %v", view.Status, err)
			}
			if err = s.Close(); err == nil {
				t.Fatal("close bypassed fence")
			}
			after, err := os.ReadFile(filepath.Join(config.Directory, state.File))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("checkpoint changed: %v", err)
			}
			m2 := readFixtureManifest(t, config.Directory)
			if !fixtureHasBlock(m2, detail) || !fixtureHasBlock(m2, state) {
				t.Fatal("fenced manifest entry removed")
			}
		})
	}
}

func readFixtureManifest(t *testing.T, directory string) manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func fixtureHasBlock(m manifest, expected block) bool {
	for _, b := range m.Blocks {
		if b.File == expected.File {
			return true
		}
	}
	return false
}

func TestCollidingDetailKeysProtectAllGenerations(t *testing.T) {
	for _, invalidFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(invalidFirst), func(t *testing.T) {
			config := DefaultConfig(t.TempDir())
			config.DetailDays = 1
			base := time.Now().UTC().AddDate(0, 0, -8).Truncate(time.Hour).Add(time.Minute)
			rows := []Receipt{durabilityReceipt(base, "old")}
			writeDurabilityFixture(t, config, rows, nil)
			m := readFixtureManifest(t, config.Directory)
			first := m.Blocks[0]
			invalid := first
			invalid.Kind = "correction"
			second := first
			second.File = "metro-detail-second.json.zst"
			third := first
			third.File = "metro-detail-third.json.zst"
			blob, err := os.ReadFile(filepath.Join(config.Directory, first.File))
			if err != nil {
				t.Fatal(err)
			}
			for _, b := range []block{second, third} {
				if err = os.WriteFile(filepath.Join(config.Directory, b.File), blob, 0600); err != nil {
					t.Fatal(err)
				}
			}
			m.Blocks = []block{first, second, invalid, third}
			if invalidFirst {
				m.Blocks = []block{invalid, first, second, third}
			}
			data, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(config.Directory, "manifest.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if s.detailFence == nil {
				t.Fatal("collision not fenced")
			}
			s.mu.Lock()
			for _, b := range []block{first, second, third} {
				if !s.protectedGenerations[retentionKey(b)] {
					s.mu.Unlock()
					t.Fatalf("detail %s is unprotected", b.File)
				}
			}
			_, _ = s.expireSome(time.Now().UTC(), &cleanupWork{})
			_ = s.ensureSpaceFor(s.config.LimitBytes, 0, time.Now().UTC(), &cleanupWork{})
			s.mu.Unlock()
			for _, b := range []block{first, second, third} {
				if _, err := os.Stat(filepath.Join(config.Directory, b.File)); err != nil {
					t.Fatalf("collision retired: %v", err)
				}
			}
		})
	}
}
