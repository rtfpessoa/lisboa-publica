package patterns

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestTransactionRejectsChangedReplacementSources(t *testing.T) {
	for _, corrupt := range []string{"metro", "provider"} {
		t.Run(corrupt, func(t *testing.T) {
			s, err := Open(DefaultConfig(t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			now := time.Now().UTC().Truncate(time.Second)
			metro := block{Key: "metro:correction:test", Kind: "correction", Operator: "metro", Age: now}
			provider := block{Key: "cm:correction:test", Kind: "correction", Operator: "cm", Age: now}
			if err := s.publish(metro, []byte("old metro"), now); err != nil {
				t.Fatal(err)
			}
			if err := s.publish(provider, []byte("old provider"), now); err != nil {
				t.Fatal(err)
			}
			before := append([]block(nil), s.index.Blocks...)
			prepared := []preparedBlock{{metro, []byte("new metro")}, {provider, []byte("new provider")}}
			transaction, err := s.prepareTransaction(prepared, nil, now)
			if err != nil {
				t.Fatal(err)
			}
			defer transaction.rollback()
			if err := transaction.write(context.Background(), prepared); err != nil {
				t.Fatal(err)
			}
			victim := before[0]
			if corrupt == "provider" {
				victim = before[1]
			}
			if err := os.WriteFile(filepath.Join(s.config.Directory, victim.File), []byte("changed source"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := transaction.commit(); err == nil {
				t.Fatal("accepted changed replacement source")
			}
			for i, b := range before {
				if s.index.Blocks[i].File != b.File {
					t.Fatal("replaced an unverified source")
				}
				if _, err := os.Stat(filepath.Join(s.config.Directory, b.File)); err != nil {
					t.Fatal("lost source evidence", err)
				}
			}
		})
	}
}

func TestTransactionPreservesAllSourcesOnUncertainManifest(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Second)
	metro := block{Key: "metro:correction:test", Kind: "correction", Operator: "metro", Age: now}
	provider := block{Key: "cm:correction:test", Kind: "correction", Operator: "cm", Age: now}
	if err := s.publish(metro, []byte("old metro"), now); err != nil {
		t.Fatal(err)
	}
	if err := s.publish(provider, []byte("old provider"), now); err != nil {
		t.Fatal(err)
	}
	before := append([]block(nil), s.index.Blocks...)
	s.manifestSync = func(string) error { return errors.New("uncertain manifest sync") }
	prepared := []preparedBlock{{metro, []byte("new metro")}, {provider, []byte("new provider")}}
	if err := s.publishTransaction(context.Background(), prepared, nil, now); err == nil {
		t.Fatal("uncertain publication acknowledged")
	}
	if !s.manifestUncertain {
		t.Fatal("uncertainty did not fence archive")
	}
	for _, old := range before {
		if _, err := os.Stat(filepath.Join(s.config.Directory, old.File)); err != nil {
			t.Fatal("old source removed", err)
		}
	}
	for _, next := range prepared {
		if _, err := os.Stat(filepath.Join(s.config.Directory, next.block.File)); err != nil {
			t.Fatal("new source removed", err)
		}
	}
}

func TestTransactionRebuildsAfterAdmissionRetiresAggregate(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	old := now.AddDate(0, 0, -8)
	a := baseAggregate(aggregateRequest{old, "line", "d", "A", "1", "p", "reported_normal", "signals"}, s.config)
	a.Count, a.KnownAt = 1, old.UnixNano()
	s.engine.add(a)
	if err := s.flush(old); err != nil {
		t.Fatal(err)
	}
	var oldDay block
	for _, b := range s.index.Blocks {
		if b.Kind == "aggregate" && b.Operator == "metro" {
			oldDay = b
		}
	}
	if oldDay.File == "" {
		t.Fatal("missing old aggregate fixture")
	}
	prior, err := s.publicationSource("metro:state")
	if err != nil {
		t.Fatal(err)
	}
	prepare := func() []preparedBlock {
		blob, err := s.encodeCheckpoint()
		if err != nil {
			t.Fatal(err)
		}
		return []preparedBlock{{block{Key: "metro:state", Kind: "state", Operator: "metro", Age: now}, blob}}
	}
	stale := prepare()
	s.config.MinFreeBytes = 2 << 20
	s.spaceProbe = func(string) (archiveSpace, error) {
		if _, err := os.Stat(filepath.Join(config.Directory, oldDay.File)); err == nil {
			return archiveSpace{bytes: 1 << 20, inodes: 100}, nil
		}
		return archiveSpace{bytes: 1 << 40, inodes: 1 << 30}, nil
	}
	if err := s.publishTransaction(context.Background(), stale, nil, now); err == nil {
		t.Fatal("published checkpoint prepared before FIFO retirement")
	}
	if _, err := os.Stat(filepath.Join(config.Directory, oldDay.File)); !os.IsNotExist(err) {
		t.Fatal("fixture day was not retired", err)
	}
	if len(s.engine.Aggregates) != 0 || s.detailFence != nil {
		t.Fatal("unexpected state or fence after retirement")
	}
	current, err := s.publicationSource("metro:state")
	if err != nil || current.block == nil || prior.block == nil || current.block.File != prior.block.File {
		t.Fatal("stale checkpoint was published", err)
	}
	if err := s.publishTransaction(context.Background(), prepare(), nil, now); err != nil {
		t.Fatal("rebuilt transaction could not progress", err)
	}
}

func TestTransactionRetriesAfterAdmissionRetiresReplacement(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	old := now.AddDate(0, 0, -8)
	value := MetroJourneyCheckpoint{Journey: "metro:run:old", Revision: 1, SourceAt: old, Payload: json.RawMessage(`{"calls":[]}`)}
	prior, err := s.prepareMetroCheckpoint(value, "old", old)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.publish(prior.block, prior.blob, old); err != nil {
		t.Fatal(err)
	}
	oldFile := s.index.Blocks[0].File
	value.SourceAt, value.Revision = now, 2
	prepare := func() []preparedBlock {
		current, err := s.prepareMetroCheckpoint(value, "new", now)
		if err != nil {
			t.Fatal(err)
		}
		return []preparedBlock{current}
	}
	s.config.MinFreeBytes = 2 << 20
	s.spaceProbe = func(string) (archiveSpace, error) {
		if _, err := os.Stat(filepath.Join(config.Directory, oldFile)); err == nil {
			return archiveSpace{bytes: 1 << 20, inodes: 100}, nil
		}
		return archiveSpace{bytes: 1 << 40, inodes: 1 << 30}, nil
	}
	if err := s.publishTransaction(context.Background(), prepare(), nil, now); err == nil {
		t.Fatal("accepted a candidate after its predecessor was retired")
	}
	if _, err := os.Stat(filepath.Join(config.Directory, oldFile)); !os.IsNotExist(err) {
		t.Fatal("old checkpoint was not retired", err)
	}
	if s.detailFence != nil {
		t.Fatal("owner retirement was mistaken for corruption", s.detailFence)
	}
	if err := s.publishTransaction(context.Background(), prepare(), nil, now); err != nil {
		t.Fatal("rebuilt transaction could not progress", err)
	}
}

func TestTransactionRejectsUnexpectedlyPresentSource(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	candidate := block{Key: "cm:correction:new", Kind: "correction", Operator: "cm", Age: now}
	prepared := []preparedBlock{{candidate, []byte("new")}}
	transaction, err := s.prepareTransaction(prepared, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	if err := transaction.write(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	injected := namedGeneration(candidate, []byte("unexpected"))
	if err := os.WriteFile(filepath.Join(s.config.Directory, injected.File), []byte("unexpected"), 0600); err != nil {
		t.Fatal(err)
	}
	s.index.Blocks = append(s.index.Blocks, injected)
	if err := transaction.commit(); err == nil {
		t.Fatal("accepted a source that was absent during staging")
	}
	if _, err := os.Stat(filepath.Join(s.config.Directory, injected.File)); err != nil {
		t.Fatal("unexpected source evidence lost", err)
	}
}

func TestTransactionPostCommitCorruptionPreservesAllOldSources(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	metro := block{Key: "metro:correction:test", Kind: "correction", Operator: "metro", Age: now}
	provider := block{Key: "cm:correction:test", Kind: "correction", Operator: "cm", Age: now}
	if err := s.publish(metro, []byte("old metro"), now); err != nil {
		t.Fatal(err)
	}
	if err := s.publish(provider, []byte("old provider"), now); err != nil {
		t.Fatal(err)
	}
	old := append([]block(nil), s.index.Blocks...)
	s.manifestSync = func(directory string) error {
		if err := os.WriteFile(filepath.Join(directory, old[1].File), []byte("changed after rename"), 0600); err != nil {
			return err
		}
		return syncDirectory(directory)
	}
	prepared := []preparedBlock{{metro, []byte("new metro")}, {provider, []byte("new provider")}}
	if err := s.publishTransaction(context.Background(), prepared, nil, now); err == nil {
		t.Fatal("missed changed old generation")
	}
	for _, source := range old {
		if _, err := os.Stat(filepath.Join(s.config.Directory, source.File)); err != nil {
			t.Fatal("old source removed", err)
		}
	}
	if err := s.Close(); err == nil {
		t.Fatal("post-commit corruption did not fence close")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence == nil {
		t.Fatal("corrupt old source did not fence reopen")
	}
	if _, err := os.Stat(filepath.Join(config.Directory, old[1].File)); err != nil {
		t.Fatal("corrupt old source was discarded on reopen", err)
	}
}

func TestTransactionDoesNotOverwriteCorruptOrphan(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	candidate := block{Key: "cm:correction:orphan", Kind: "correction", Operator: "cm", Age: now}
	prepared := []preparedBlock{{candidate, []byte("expected")}}
	transaction, err := s.prepareTransaction(prepared, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.Directory, prepared[0].block.File)
	if err := os.WriteFile(path, []byte("corrupt orphan evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.write(context.Background(), prepared); err == nil {
		t.Fatal("overwrote corrupt orphan")
	}
	transaction.rollback()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "corrupt orphan evidence" {
		t.Fatal("orphan evidence changed", err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("corrupt orphan did not fence close")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence == nil {
		t.Fatal("corrupt orphan did not fence reopen")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("corrupt orphan removed on reopen", err)
	}
}

func TestTransactionReusesVerifiedExistingGeneration(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	candidate := block{Key: "cm:correction:orphan", Kind: "correction", Operator: "cm", Age: now}
	prepared := []preparedBlock{{candidate, []byte("expected")}}
	transaction, err := s.prepareTransaction(prepared, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	path := filepath.Join(s.config.Directory, prepared[0].block.File)
	if err := os.WriteFile(path, []byte("expected"), 0600); err != nil {
		t.Fatal(err)
	}
	synced := false
	s.generationSync = func(f *os.File) error { synced = true; return f.Sync() }
	if err := transaction.write(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	if !synced {
		t.Fatal("existing generation was not synced")
	}
	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.readBlock(prepared[0].block); err != nil {
		t.Fatal("verified generation not admitted", err)
	}
}

func TestTransactionRollbackPreservesCorruptedStagedGeneration(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	candidate := block{Key: "cm:correction:staged", Kind: "correction", Operator: "cm", Age: now}
	prepared := []preparedBlock{{candidate, []byte("expected")}}
	transaction, err := s.prepareTransaction(prepared, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.write(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.Directory, prepared[0].block.File)
	if err := os.WriteFile(path, []byte("changed staged evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	transaction.rollback()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "changed staged evidence" {
		t.Fatal("rollback removed changed staged generation", err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("corrupt staged generation did not fence close")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence == nil {
		t.Fatal("corrupt staged generation did not fence reopen")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("corrupt staged generation removed on reopen", err)
	}
}

func TestTransactionChecksStagedGenerationBeforeCommit(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	prepared := []preparedBlock{{block{Key: "cm:correction:staged", Kind: "correction", Operator: "cm", Age: now}, []byte("expected")}}
	transaction, err := s.prepareTransaction(prepared, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	if err := transaction.write(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.config.Directory, prepared[0].block.File)
	if err := os.WriteFile(path, []byte("changed after staging"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.commit(); err == nil || len(s.index.Blocks) != 0 {
		t.Fatal("committed changed staged generation", err)
	}
}

func TestTransactionRechecksSourceAfterCandidateSync(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	value := block{Key: "cm:correction:target", Kind: "correction", Operator: "cm", Age: now}
	if err := s.publish(value, []byte("old"), now); err != nil {
		t.Fatal(err)
	}
	prior := s.index.Blocks[0]
	prepared := []preparedBlock{{value, []byte("new")}}
	transaction, err := s.prepareTransaction(prepared, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	if err := transaction.write(context.Background(), prepared); err != nil {
		t.Fatal(err)
	}
	s.generationSync = func(f *os.File) error {
		if err := os.WriteFile(filepath.Join(s.config.Directory, prior.File), []byte("changed source"), 0600); err != nil {
			return err
		}
		return f.Sync()
	}
	if err := transaction.commit(); err == nil {
		t.Fatal("accepted a source changed during candidate verification")
	}
	if s.index.Blocks[0].File != prior.File {
		t.Fatal("manifest changed after source corruption")
	}
}

func TestTransactionDoesNotAdmitUnsyncedExistingGeneration(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	prepared := []preparedBlock{{block{Key: "cm:correction:reuse", Kind: "correction", Operator: "cm", Age: now}, []byte("expected")}}
	transaction, err := s.prepareTransaction(prepared, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	path := filepath.Join(s.config.Directory, prepared[0].block.File)
	if err := os.WriteFile(path, []byte("expected"), 0600); err != nil {
		t.Fatal(err)
	}
	s.generationSync = func(*os.File) error { return errors.New("injected sync failure") }
	if err := transaction.write(context.Background(), prepared); err == nil || len(s.index.Blocks) != 0 {
		t.Fatal("admitted a generation whose file sync failed", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("existing generation was removed", err)
	}
}

func TestTransactionRejectsFIFOWithoutBlocking(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	prepared := []preparedBlock{{block{Key: "cm:correction:fifo", Kind: "correction", Operator: "cm", Age: now}, []byte("expected")}}
	transaction, err := s.prepareTransaction(prepared, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.rollback()
	path := filepath.Join(s.config.Directory, prepared[0].block.File)
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- transaction.write(context.Background(), prepared) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted FIFO as a generation")
		}
	case <-time.After(time.Second):
		// Unblock an unsafe reader before failing, so the regression cannot hang.
		fd, openErr := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
		if openErr == nil {
			_ = syscall.Close(fd)
		}
		<-done
		t.Fatal("blocked while opening FIFO generation")
	}
	if _, err := s.readBlock(prepared[0].block); err == nil {
		t.Fatal("read accepted FIFO as a generation")
	}
	if err := s.Close(); err == nil {
		t.Fatal("FIFO did not fence close")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence == nil {
		t.Fatal("FIFO did not fence restart")
	}
}
