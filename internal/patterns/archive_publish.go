package patterns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (s *Service) publish(b block, blob []byte, now time.Time) (err error) {
	defer func() {
		if err != nil {
			err = s.markPaused(err)
		}
	}()
	if s.manifestUncertain {
		return fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	if len(blob) > maxBlockBytes {
		return fmt.Errorf("archive block limit")
	}
	prepared := namedGeneration(b, blob)
	source, err := s.guardPublication(b)
	if err != nil {
		return err
	}
	if _, _, changed := replacementManifest(s.index, prepared); !changed {
		return nil
	}
	build := func() manifest { next, _, _ := replacementManifest(s.index, prepared); return next }
	if err := s.admitManifest(build, allocatedBytes(len(blob)), now); err != nil {
		return err
	}
	if err = s.verifyGuardedSource(source); err != nil {
		return err
	}
	if !s.generationRetained(prepared) {
		return nil
	}
	next, existing, changed := replacementManifest(s.index, prepared)
	if !changed {
		return nil
	}
	return s.commitGeneration(next, prepared, existing, blob)
}

func (s *Service) generationRetained(b block) bool {
	if b.Kind != "aggregate" {
		return true
	}
	target := s.operatorEngine(b.Operator)
	for _, a := range target.Aggregates {
		if a.Date == b.Date {
			return true
		}
	}
	// A generation prepared before FIFO cannot resurrect a retired day.
	return target.DirtyDays[b.Date]
}

func replacementManifest(current manifest, b block) (manifest, string, bool) {
	next := manifest{Version: 1, Blocks: append([]block{}, current.Blocks...)}
	for i, old := range next.Blocks {
		if old.Key != b.Key {
			continue
		}
		if old.Hash == b.Hash {
			return next, "", false
		}
		next.Blocks[i] = b
		return next, old.File, true
	}
	next.Blocks = append(next.Blocks, b)
	return next, "", true
}

func (s *Service) commitGeneration(next manifest, b block, previous string, blob []byte) error {
	source, err := s.guardPublication(b)
	if err != nil {
		return err
	}
	if err = s.writeGuardedGeneration(b, blob, source); err != nil {
		return err
	}
	// Callers verify a complete codec roundtrip before admission.
	if err := s.saveManifest(next); err != nil {
		if !s.manifestUncertain && !s.generationReferenced(b.File) {
			_ = os.Remove(filepath.Join(s.config.Directory, b.File))
		}
		return err
	}
	if source != nil && previous != "" {
		if err := s.verifyOldGeneration(*source); err != nil {
			return err
		}
	}
	if err := s.removePreviousGeneration(previous); err != nil {
		return err
	}
	sync := s.publicationSync
	if sync == nil {
		sync = syncDirectory
	}
	return sync(s.config.Directory)
}

func (s *Service) writeGuardedGeneration(b block, blob []byte, source *publicationSource) error {
	if err := atomicFile(s.config.Directory, b.File, blob); err != nil {
		return err
	}
	if err := s.verifyGuardedSource(source); err != nil {
		if !s.generationReferenced(b.File) {
			_ = os.Remove(filepath.Join(s.config.Directory, b.File))
		}
		return err
	}
	return nil
}

func (s *Service) removePreviousGeneration(previous string) error {
	if previous == "" || s.generationReferenced(previous) {
		return nil
	}
	s.forgetRetentionFile(previous)
	err := os.Remove(filepath.Join(s.config.Directory, previous))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func namedGeneration(b block, blob []byte) block {
	b.Hash = digestBytes(blob)
	suffix := "json.zst"
	if b.Kind == "aggregate" {
		suffix = "parquet"
	}
	b.File = "metro-" + b.Kind + "-" + digest(b.Key)[:16] + "-" + b.Hash + "." + suffix
	return b
}
