package patterns

import (
	"context"
	"fmt"
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
	reserve, err := prepareTransactionGenerations(prepared)
	if err != nil {
		return nil, err
	}
	if err = s.ensureSpace(reserve, now); err != nil {
		return nil, err
	}
	transaction := &archiveTransaction{service: s, next: manifest{Version: 1, Blocks: append([]block{}, s.index.Blocks...)}, admitted: map[string]bool{}}
	for _, b := range s.index.Blocks {
		transaction.admitted[b.File] = true
	}
	return transaction, transaction.checkSources(sources)
}

func prepareTransactionGenerations(prepared []preparedBlock) (int64, error) {
	reserve, working := int64(manifestReserve), 0
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
		reserve += int64((len(p.blob) + 4095) / 4096 * 4096)
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
		if !t.admitted[p.block.File] {
			if err := atomicFile(t.service.config.Directory, p.block.File, p.blob); err != nil {
				return err
			}
			t.created = append(t.created, p.block.File)
		}
		t.mergeBlock(p.block)
	}
	return nil
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
	if err := t.service.saveManifest(t.next); err != nil {
		return err
	}
	t.committed = true
	// Cleanup after admission is recoverable by startup reconciliation.
	t.removeFiles(t.old)
	return nil
}

func (t *archiveTransaction) rollback() {
	if !t.committed {
		t.removeFiles(t.created)
	}
}

func (t *archiveTransaction) removeFiles(names []string) {
	for _, name := range names {
		_ = os.Remove(filepath.Join(t.service.config.Directory, name))
	}
	_ = syncDirectory(t.service.config.Directory)
}
