package patterns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func (s *Service) allocated() (int64, error) {
	var total int64
	err := filepath.WalkDir(s.config.Directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("allocated storage unavailable")
		}
		total += stat.Blocks * 512
		return nil
	})
	return total, err
}

func (s *Service) reconcile() error {
	if s.manifestUncertain {
		return fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	keep := map[string]bool{"manifest.json": true, ".lock": true}
	for _, b := range s.index.Blocks {
		keep[b.File] = true
	}
	entries, err := os.ReadDir(s.config.Directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if keep[entry.Name()] {
			continue
		}
		if entry.IsDir() || !(strings.HasPrefix(entry.Name(), "metro-") || entry.Name() == ".manifest.tmp") {
			return fmt.Errorf("unmanaged file in transport archive")
		}
		if err = os.Remove(filepath.Join(s.config.Directory, entry.Name())); err != nil {
			return err
		}
	}
	return syncDirectory(s.config.Directory)
}

func (s *Service) ensureSpace(reserve int64, now time.Time) error {
	if s.manifestUncertain {
		return fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	s.closeRetainedBlocks(now)
	for {
		used, err := s.allocated()
		if err != nil {
			return err
		}
		oldest, expired := s.retirementCandidate(now, false)
		if !expired && used+reserve <= s.config.LimitBytes {
			return nil
		}
		if !expired {
			oldest, _ = s.retirementCandidate(now, true)
		}
		if oldest < 0 {
			return fmt.Errorf("transport archive budget exhausted")
		}
		if err = s.retireBlock(oldest); err != nil {
			return err
		}
	}
}

func (s *Service) closeRetainedBlocks(now time.Time) {
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		switch b.Kind {
		case "detail", "correction", "observations", "popup-events", "popup-checkpoint", "metro-inputs":
			b.Closed = b.Age.Before(now.UTC().Truncate(time.Hour))
		case "aggregate":
			b.Closed = b.Date < now.In(lisbon).Format("2006-01-02")
		}
	}
}

func (s *Service) retirementCandidate(now time.Time, pressure bool) (int, bool) {
	oldest, expired := -1, false
	for i, b := range s.index.Blocks {
		if !b.Closed || b.Kind == "state" {
			continue
		}
		candidateExpired := s.blockExpired(b, now)
		if !candidateExpired && (!pressure || !s.pressureEligible(b, now)) {
			continue
		}
		if oldest < 0 || preferRetirement(b, candidateExpired, s.index.Blocks[oldest], expired) {
			oldest, expired = i, candidateExpired
		}
	}
	return oldest, expired
}

func preferRetirement(candidate block, expired bool, current block, currentExpired bool) bool {
	if expired != currentExpired {
		return expired
	}
	return candidate.Age.Before(current.Age)
}

func (s *Service) retireBlock(index int) error {
	removed := s.index.Blocks[index]
	next := manifest{Version: 1, Blocks: append([]block{}, s.index.Blocks...)}
	next.Blocks = append(next.Blocks[:index], next.Blocks[index+1:]...)
	// Retirement also needs a temporary manifest alongside the old one.
	blob, err := encodeManifest(next)
	if err != nil {
		return err
	}
	used, err := s.allocated()
	if err != nil {
		return err
	}
	if used+allocatedBytes(len(blob)) > s.config.LimitBytes {
		return fmt.Errorf("transport archive budget exhausted")
	}
	// Publish retirement before unlinking; serialized readers have already closed.
	if err := s.saveManifest(next); err != nil {
		return err
	}
	s.forgetRetiredAggregate(removed)
	delete(s.aggregateClocks, removed.File)
	err = os.Remove(filepath.Join(s.config.Directory, removed.File))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = syncDirectory(s.config.Directory); err != nil {
		return err
	}
	return nil
}

func (s *Service) forgetRetiredAggregate(removed block) {
	if removed.Kind != "aggregate" {
		return
	}
	target := s.operatorEngine(removed.Operator)
	delete(target.DirtyDays, removed.Date)
	delete(target.ColdDays, removed.Date)
	for key, row := range target.Aggregates {
		if row.Operator == removed.Operator && row.Date == removed.Date {
			delete(target.Aggregates, key)
		}
	}
}
