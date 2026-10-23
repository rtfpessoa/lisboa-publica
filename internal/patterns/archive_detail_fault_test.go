package patterns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCorruptionAfterManifestCommitSurvivesRestart(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(durabilityReceipt(base, "first"), testTopology()); err != nil {
		t.Fatal(err)
	}
	_, prior := detailForHour(t, s, base.Truncate(time.Hour))
	priorPath := filepath.Join(config.Directory, prior.File)
	injected := false
	s.mu.Lock()
	s.manifestSync = func(directory string) error {
		if !injected {
			injected = true
			if err := os.WriteFile(priorPath, []byte("corrupted after commit"), 0600); err != nil {
				return err
			}
		}
		return syncDirectory(directory)
	}
	s.mu.Unlock()
	if err = s.Record(durabilityReceipt(base.Add(61*time.Second), "second"), testTopology()); err == nil || !injected {
		t.Fatalf("missing post-commit corruption failure: %v", err)
	}
	if err = s.Close(); err == nil {
		t.Fatal("corrupt orphan did not fence close")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence == nil {
		t.Fatal("reconciliation ignored corrupt orphan")
	}
	got, err := os.ReadFile(priorPath)
	if err != nil || !bytes.Equal(got, []byte("corrupted after commit")) {
		t.Fatalf("orphan not preserved: %v", err)
	}
	rows, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "first", "second")
}

func TestCorruptionDuringAdmissionFencesReplacement(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MinFreeBytes = 1
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(durabilityReceipt(base, "first"), testTopology()); err != nil {
		t.Fatal(err)
	}
	_, prior := detailForHour(t, s, base.Truncate(time.Hour))
	injected := false
	s.mu.Lock()
	s.spaceProbe = func(string) (archiveSpace, error) {
		if !injected {
			injected = true
			if err := os.WriteFile(filepath.Join(config.Directory, prior.File), []byte("changed while admitting"), 0600); err != nil {
				return archiveSpace{}, err
			}
		}
		return archiveSpace{bytes: 1 << 40, inodes: 1 << 30}, nil
	}
	s.mu.Unlock()
	if err = s.Record(durabilityReceipt(base.Add(61*time.Second), "second"), testTopology()); err == nil || !injected {
		t.Fatalf("changed source accepted: %v", err)
	}
	s.mu.Lock()
	current, findErr := s.detailBlock(base.Truncate(time.Hour))
	fence := s.detailFence
	s.mu.Unlock()
	if findErr != nil || current == nil || current.File != prior.File || fence == nil {
		t.Fatal("source replacement or missing fence")
	}
}

func TestSharedCheckpointFencePreservesProviderPartialCommit(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(durabilityReceipt(base, "metro"), testTopology()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	state, _ := s.publicationSource("metro:state")
	s.lastCheckpoint = time.Time{}
	s.detailFence = errors.New("injected unreadable checkpoint")
	s.mu.Unlock()
	r := ProviderReceipt{Operator: "cm", ReceivedAt: base.Add(time.Minute), Rows: []Observation{}}
	if err = s.RecordProvider(r); err == nil {
		t.Fatal("provider checkpoint bypassed fence")
	}
	s.mu.Lock()
	retained := false
	for _, b := range s.index.Blocks {
		if b.Operator == "cm" && b.Kind == "observations" {
			retained = true
		}
	}
	after, _ := s.publicationSource("metro:state")
	s.mu.Unlock()
	if !retained || state.block == nil || after.block == nil || state.block.File != after.block.File {
		t.Fatal("partial provider detail or shared checkpoint lost")
	}
	_, err = s.ReprocessProvider(context.Background(), "cm", []ProviderCorrection{{}}, base.Add(2*time.Minute))
	if err == nil {
		t.Fatal("provider reprocess bypassed fence")
	}
	_, err = s.Reprocess(context.Background(), []Correction{{}}, base.Add(2*time.Minute))
	if err == nil {
		t.Fatal("Metro reprocess bypassed fence")
	}
}

func TestDetailRetiredDuringAdmissionIsNotResurrected(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 30
	base := time.Now().UTC().AddDate(0, 0, -8).Truncate(time.Hour).Add(5 * time.Minute)
	writeDurabilityFixture(t, config, []Receipt{durabilityReceipt(base, "old")}, nil)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, prior := detailForHour(t, s, base.Truncate(time.Hour))
	priorPath := filepath.Join(config.Directory, prior.File)
	s.mu.Lock()
	s.detail = append(s.detail, durabilityReceipt(base.Add(61*time.Second), "new"))
	var candidate bytes.Buffer
	for _, row := range s.detail {
		if err = json.NewEncoder(&candidate).Encode(row); err != nil {
			s.mu.Unlock()
			t.Fatal(err)
		}
	}
	blob, err := s.verifiedDetailBlob(candidate.Bytes())
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.config.MinFreeBytes = 2 << 20
	s.spaceProbe = func(string) (archiveSpace, error) {
		if _, err := os.Stat(priorPath); err == nil {
			return archiveSpace{bytes: 1 << 20, inodes: 100}, nil
		}
		return archiveSpace{bytes: 1 << 40, inodes: 1 << 30}, nil
	}
	err = s.publish(prior, blob, time.Now().UTC())
	s.mu.Unlock()
	if err == nil {
		t.Fatal("retired source was resurrected")
	}
	publishErr := err
	if _, err = os.Stat(priorPath); !os.IsNotExist(err) {
		t.Fatalf("source was not pressure retired (%v): %v", publishErr, err)
	}
	s.mu.Lock()
	source, sourceErr := s.detailBlock(base.Truncate(time.Hour))
	fenced, retained := s.detailFence != nil, len(s.detail)
	s.mu.Unlock()
	if sourceErr != nil || source != nil || !fenced || retained != 2 {
		t.Fatal("source replacement was not safely aborted")
	}
}

func TestMetroManifestSyncUncertaintyPreservesBothGenerations(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(durabilityReceipt(base, "first"), testTopology()); err != nil {
		t.Fatal(err)
	}
	_, prior := detailForHour(t, s, base.Truncate(time.Hour))
	s.mu.Lock()
	s.manifestSync = func(string) error { return errors.New("injected uncertain manifest sync") }
	s.mu.Unlock()
	if err = s.Record(durabilityReceipt(base.Add(61*time.Second), "second"), testTopology()); err == nil {
		t.Fatal("uncertain sync succeeded")
	}
	s.mu.Lock()
	uncertain := s.manifestUncertain
	s.mu.Unlock()
	if !uncertain {
		t.Fatal("uncertainty was not fenced")
	}
	if _, err = os.Stat(filepath.Join(config.Directory, prior.File)); err != nil {
		t.Fatal("old generation was not retained")
	}
	if err = s.Close(); err == nil {
		t.Fatal("uncertain close succeeded")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil || s.manifestUncertain {
		t.Fatalf("verified reopen failed: %v", s.detailFence)
	}
	rows, _ := detailForHour(t, s, base.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "first", "second")
}
