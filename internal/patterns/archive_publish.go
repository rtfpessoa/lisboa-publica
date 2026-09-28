package patterns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (s *Service) publish(b block, blob []byte, now time.Time) error {
	prepared, err := s.prepareGeneration(b, blob, now)
	if err != nil {
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

func (s *Service) prepareGeneration(b block, blob []byte, now time.Time) (block, error) {
	if len(blob) > maxBlockBytes {
		return b, fmt.Errorf("archive block limit")
	}
	b = namedGeneration(b, blob)
	// Account for replacement, manifest publication and allocation rounding.
	err := s.ensureSpace(generationReserve(blob), now)
	return b, err
}

func generationReserve(blob []byte) int64 {
	return int64((len(blob)+4095)/4096*4096) + manifestReserve
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
	if err := atomicFile(s.config.Directory, b.File, blob); err != nil {
		return err
	}
	// Callers verify a complete codec roundtrip before admission.
	if err := s.saveManifest(next); err != nil {
		return err
	}
	if err := s.removePreviousGeneration(previous); err != nil {
		return err
	}
	return syncDirectory(s.config.Directory)
}

func (s *Service) removePreviousGeneration(previous string) error {
	if previous == "" {
		return nil
	}
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
