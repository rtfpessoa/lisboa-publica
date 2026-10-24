package patterns

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"
)

type archiveTransaction struct {
	service      *Service
	next         manifest
	admitted     map[string]bool
	created, old []string
	committed    bool
	sources      []publicationSource
	candidates   []block
}

// Admit corrected days, ledger and checkpoint through one durable manifest.
func (s *Service) publishTransaction(ctx context.Context, prepared []preparedBlock, sources map[string]bool, now time.Time) error {
	transaction, err := s.prepareTransaction(prepared, sources, now)
	if err != nil {
		return err
	}
	defer transaction.rollback()
	if err = transaction.write(ctx, prepared); err != nil {
		return err
	}
	return transaction.commit()
}

func (s *Service) prepareTransaction(prepared []preparedBlock, sources map[string]bool, now time.Time) (*archiveTransaction, error) {
	guarded, err := s.transactionSources(prepared)
	if err != nil {
		return nil, err
	}
	reserve, err := prepareTransactionGenerations(prepared)
	if err != nil {
		return nil, err
	}
	if err = s.admitTransaction(prepared, guarded, reserve, now); err != nil {
		return nil, err
	}
	transaction := &archiveTransaction{service: s, sources: guarded, next: manifest{Version: 1, Blocks: append([]block{}, s.index.Blocks...)}, admitted: map[string]bool{}}
	for _, b := range s.index.Blocks {
		transaction.admitted[b.File] = true
	}
	if err = transaction.checkSources(sources); err != nil {
		return nil, err
	}
	for _, p := range prepared {
		transaction.candidates = append(transaction.candidates, p.block)
		transaction.mergeBlock(p.block)
	}
	return transaction, nil
}

func (s *Service) admitTransaction(prepared []preparedBlock, guarded []publicationSource, reserve int64, now time.Time) error {
	aggregates := s.transactionAggregateSources()
	retirementSerial := s.retirementSerial
	build := func() manifest {
		next := manifest{Version: 1, Blocks: append([]block{}, s.index.Blocks...)}
		for _, p := range prepared {
			next = mergeTransactionManifest(next, p.block)
		}
		return next
	}
	if err := s.admitManifestFiles(build, reserve, uint64(len(prepared)), now); err != nil {
		return err
	}
	// Retention may delete in-memory aggregate state during admission. The
	// caller must rebuild every payload against the newly admitted state.
	if !maps.Equal(aggregates, s.transactionAggregateSources()) {
		return fmt.Errorf("archive changed during transaction admission; retry")
	}
	// Admission has a one-retirement work budget. A source retired by this
	// owner is not corruption; the caller must rebuild against its absence.
	if s.transactionSourceRetired(guarded, retirementSerial) {
		return fmt.Errorf("transaction source retired during admission; retry")
	}
	for _, source := range guarded {
		if err := s.verifyPublicationSource(source); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) transactionSourceRetired(guarded []publicationSource, serial uint64) bool {
	if s.retirementSerial == serial {
		return false
	}
	for _, source := range guarded {
		if source.block != nil && retentionKey(*source.block) == s.lastRetired {
			return true
		}
	}
	return false
}

func (s *Service) transactionSources(prepared []preparedBlock) ([]publicationSource, error) {
	guarded := make([]publicationSource, 0, len(prepared))
	keys := make(map[string]bool, len(prepared))
	for _, item := range prepared {
		if keys[item.block.Key] {
			return nil, fmt.Errorf("duplicate transaction publication key")
		}
		keys[item.block.Key] = true
		if item.block.Key == "metro:state" && s.detailFence != nil {
			return nil, s.detailFence
		}
		source, err := s.publicationSource(item.block.Key)
		if err != nil {
			return nil, err
		}
		if err = s.verifyPublicationSource(source); err != nil {
			return nil, err
		}
		guarded = append(guarded, source)
	}
	return guarded, nil
}

func (s *Service) transactionAggregateSources() map[retentionClockKey]bool {
	result := make(map[retentionClockKey]bool)
	for _, b := range s.index.Blocks {
		if b.Kind == "aggregate" {
			result[retentionKey(b)] = true
		}
	}
	return result
}

func mergeTransactionManifest(next manifest, value block) manifest {
	for i := range next.Blocks {
		if next.Blocks[i].Key == value.Key {
			next.Blocks[i] = value
			return next
		}
	}
	next.Blocks = append(next.Blocks, value)
	return next
}

func prepareTransactionGenerations(prepared []preparedBlock) (int64, error) {
	reserve, working := int64(0), 0
	for i := range prepared {
		p := &prepared[i]
		working += len(p.blob)
		if working > maxBlockBytes {
			return 0, fmt.Errorf("reprocessing publication working set limit")
		}
		if len(p.blob) > maxBlockBytes {
			return 0, fmt.Errorf("reprocessing block limit")
		}
		p.block = namedGeneration(p.block, p.blob)
		reserve += allocatedBytes(len(p.blob))
	}
	return reserve, nil
}

func (t *archiveTransaction) checkSources(sources map[string]bool) error {
	for name := range sources {
		if !t.admitted[name] {
			return fmt.Errorf("source retired during reservation; reprocessing cancelled")
		}
	}
	return nil
}

func (t *archiveTransaction) write(ctx context.Context, prepared []preparedBlock) error {
	for _, p := range prepared {
		if err := ctx.Err(); err != nil {
			return err
		}
		created, err := t.service.writeTransactionGeneration(p)
		if created {
			t.created = append(t.created, p.block.File)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) writeTransactionGeneration(p preparedBlock) (bool, error) {
	path := filepath.Join(s.config.Directory, p.block.File)
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, s.fenceDetail(fmt.Errorf("transaction generation is not a regular file"), &p.block)
		}
		if err := s.verifyReusedTransactionGeneration(p); err != nil {
			return false, s.fenceDetail(fmt.Errorf("existing transaction generation unavailable: %w", err), &p.block)
		}
		return false, syncDirectory(s.config.Directory)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	temp, err := writeRecoveryTemporary(s.config.Directory, p.blob)
	if err != nil {
		return false, err
	}
	defer os.Remove(temp)
	if err = renameRecoveryNoReplace(temp, path); err != nil {
		return false, fmt.Errorf("create transaction generation: %w", err)
	}
	return true, syncDirectory(s.config.Directory)
}

func (s *Service) verifyReusedTransactionGeneration(p preparedBlock) error {
	blob, err := s.readSyncedTransactionGeneration(p.block)
	if err != nil {
		return err
	}
	if !bytes.Equal(blob, p.blob) {
		return fmt.Errorf("transaction generation contents changed")
	}
	return nil
}

func (s *Service) readSyncedTransactionGeneration(b block) ([]byte, error) {
	f, err := openArchiveGeneration(filepath.Join(s.config.Directory, b.File))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sync := s.generationSync
	if sync == nil {
		sync = (*os.File).Sync
	}
	if err := sync(f); err != nil {
		return nil, err
	}
	return readGeneration(f, b.Hash)
}

func (t *archiveTransaction) mergeBlock(value block) {
	for i, prior := range t.next.Blocks {
		if prior.Key != value.Key {
			continue
		}
		if prior.File != value.File {
			t.old = append(t.old, prior.File)
		}
		t.next.Blocks[i] = value
		return
	}
	t.next.Blocks = append(t.next.Blocks, value)
}

func (t *archiveTransaction) commit() error {
	if err := t.verifyBeforeCommit(); err != nil {
		return err
	}
	if err := t.service.saveManifest(t.next); err != nil {
		if t.service.manifestUncertain {
			// Both generations must survive until a reopen resolves the manifest.
			t.committed = true
		}
		return err
	}
	t.committed = true
	// Cleanup after admission is recoverable by startup reconciliation.
	if err := t.verifyOldSources(); err != nil {
		return err
	}
	t.removeFiles(t.old)
	return nil
}

func (t *archiveTransaction) verifyBeforeCommit() error {
	for _, candidate := range t.candidates {
		if _, err := t.service.readSyncedTransactionGeneration(candidate); err != nil {
			return t.service.fenceDetail(fmt.Errorf("transaction generation changed before commit: %w", err), &candidate)
		}
	}
	if len(t.candidates) > 0 {
		if err := syncDirectory(t.service.config.Directory); err != nil {
			return err
		}
	}
	for _, source := range t.sources {
		if err := t.service.verifyPublicationSource(source); err != nil {
			return err
		}
	}
	return nil
}

func (t *archiveTransaction) verifyOldSources() error {
	for _, source := range t.sources {
		if source.block == nil || t.service.generationReferenced(source.block.File) {
			continue
		}
		if err := t.service.verifyOldGeneration(source); err != nil {
			return err
		}
	}
	return nil
}

func (t *archiveTransaction) rollback() {
	if !t.committed {
		t.removeFiles(t.created)
	}
}

func (t *archiveTransaction) removeFiles(names []string) {
	for _, name := range names {
		if t.service.generationReferenced(name) || t.service.preserveUncertainOrphan(name) {
			continue
		}
		t.service.forgetRetentionFile(name)
		_ = os.Remove(filepath.Join(t.service.config.Directory, name))
	}
	_ = syncDirectory(t.service.config.Directory)
}
