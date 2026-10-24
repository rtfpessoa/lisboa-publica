package patterns

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Work is bounded by both the archive manifest ceiling and these per-pass limits.
// A single filesystem call or a single compressed generation can still be slow.
const maxArchiveEntries = 32768
const maxRetentionChecks = 2
const maxRetirements = 1

var errMaintenancePending = errors.New("transport archive maintenance pending")
var errRetentionChanged = errors.New("archive retention evidence changed")

type cleanupWork struct {
	checks, retirements int
}

type archiveSpace struct {
	bytes, inodes uint64
}

// archiveReservation describes a pending publication and whether its payload
// remains owned by the archive for retry.
type archiveReservation struct {
	bytes  int64
	files  uint64
	pinned bool
}

func filesystemArchiveSpace(path string) (archiveSpace, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return archiveSpace{}, err
	}
	if stat.Bsize <= 0 || stat.Bavail > stat.Blocks {
		return archiveSpace{}, fmt.Errorf("archive filesystem space invalid")
	}
	blocks, size := uint64(stat.Bavail), uint64(stat.Bsize)
	if blocks > math.MaxUint64/size {
		return archiveSpace{}, fmt.Errorf("archive filesystem space overflow")
	}
	return archiveSpace{bytes: blocks * size, inodes: uint64(stat.Ffree)}, nil
}

func (s *Service) availableSpace() (archiveSpace, error) {
	if s.spaceProbe != nil {
		return s.spaceProbe(s.config.Directory)
	}
	return filesystemArchiveSpace(s.config.Directory)
}

func (s *Service) allocated() (int64, error) {
	dir, err := os.Open(s.config.Directory)
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	root, err := dir.Stat()
	if err != nil {
		return 0, err
	}
	total, err := allocatedFile(root)
	if err != nil {
		return 0, err
	}
	return allocatedEntries(dir, total)
}

func allocatedFile(info os.FileInfo) (int64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("allocated storage unavailable")
	}
	return stat.Blocks * 512, nil
}

func allocatedEntries(dir *os.File, total int64) (int64, error) {
	count := 0
	for {
		entries, readErr := dir.ReadDir(256)
		for _, entry := range entries {
			count++
			if count > maxArchiveEntries || entry.IsDir() {
				return 0, fmt.Errorf("transport archive directory exceeds bounded layout")
			}
			info, err := entry.Info()
			if err != nil {
				return 0, err
			}
			size, err := allocatedFile(info)
			if err != nil {
				return 0, err
			}
			total += size
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return 0, readErr
		}
	}
}

func (s *Service) reconcile() error {
	if s.manifestUncertain {
		return fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	sync := s.reconcileSync
	if sync == nil {
		sync = syncDirectory
	}
	if !s.reconciledHead {
		// Establish the manifest selected on reopen before unlinking any orphan.
		if err := sync(s.config.Directory); err != nil {
			return err
		}
		s.reconciledHead = true
	}
	if err := s.removeOrphanedGenerations(); err != nil {
		return err
	}
	return sync(s.config.Directory)
}

func (s *Service) removeOrphanedGenerations() error {
	keep := map[string]bool{"manifest.json": true, ".lock": true}
	for _, b := range s.index.Blocks {
		keep[b.File] = true
	}
	s.retainRecoverySource(keep)
	dir, err := os.Open(s.config.Directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	count := 0
	for {
		entries, readErr := dir.ReadDir(256)
		for _, entry := range entries {
			count++
			if count > maxArchiveEntries {
				return fmt.Errorf("transport archive directory exceeds bounded layout")
			}
			if err := s.removeOrphanedEntry(entry, keep); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (s *Service) retainRecoverySource(keep map[string]bool) {
	if s.tailRecovery == nil {
		return
	}
	prior := s.tailRecovery.PriorState
	keep[prior.File] = true
	raw, err := readRecoveryFile(filepath.Join(s.config.Directory, prior.File), maxBlockBytes)
	if err != nil || digestBytes(raw) != prior.Hash {
		s.protectCheckpointDetails()
		s.fenceDetail(fmt.Errorf("Metro recovery source unavailable or corrupt"), &prior)
	}
}

func (s *Service) removeOrphanedEntry(entry os.DirEntry, keep map[string]bool) error {
	if keep[entry.Name()] {
		return nil
	}
	if entry.IsDir() || !(strings.HasPrefix(entry.Name(), "metro-") || entry.Name() == ".manifest.tmp") {
		return fmt.Errorf("unmanaged file in transport archive")
	}
	// A fenced checkpoint may contain an unreadable reference to a prior state.
	if s.detailFence != nil && strings.HasPrefix(entry.Name(), "metro-state-") {
		return nil
	}
	if s.preserveUncertainOrphan(entry.Name()) {
		return nil
	}
	if err := os.Remove(filepath.Join(s.config.Directory, entry.Name())); err != nil {
		return err
	}
	s.forgetRetentionFile(entry.Name())
	return nil
}

// Generated detail/state names embed a hash. Do not delete an orphan whose
// contents cannot be verified, including across a restart after a partial commit.
func (s *Service) preserveUncertainOrphan(name string) bool {
	if !strings.HasPrefix(name, "metro-detail-") && !strings.HasPrefix(name, "metro-"+metroDetailSegmentKind+"-") && !strings.HasPrefix(name, "metro-state-") {
		return false
	}
	const suffix = ".json.zst"
	stem := strings.TrimSuffix(name, suffix)
	cut := strings.LastIndexByte(stem, '-')
	if !strings.HasSuffix(name, suffix) || cut < 0 || len(stem)-cut-1 != 64 {
		s.fenceDetail(fmt.Errorf("unidentifiable archive orphan"), nil)
		return true
	}
	hash := stem[cut+1:]
	if _, err := hex.DecodeString(hash); err != nil {
		s.fenceDetail(fmt.Errorf("unidentifiable archive orphan"), nil)
		return true
	}
	if _, err := s.readBlock(block{File: name, Hash: hash}); err != nil {
		s.fenceDetail(err, nil)
		return true
	}
	return false
}

func (s *Service) ensureSpace(reserve int64, now time.Time) error {
	work := s.cleanupBudget()
	return s.ensureSpaceFor(reserve, 0, now, work)
}

func (s *Service) ensureSpaceFor(reserve int64, files uint64, now time.Time, work *cleanupWork) (result error) {
	reservation := archiveReservation{bytes: reserve, files: files}
	defer func() { s.recordSpaceFailure(reservation, result) }()
	if s.manifestUncertain {
		return fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	if reserve < 0 {
		return fmt.Errorf("invalid archive reservation")
	}
	if reserve > s.config.LimitBytes {
		return fmt.Errorf("archive reservation exceeds budget")
	}
	s.closeRetainedBlocks(now)
	return s.relieveSpacePressure(reservation, now, work)
}

func (s *Service) recordSpaceFailure(reservation archiveReservation, result error) {
	if result == nil {
		return
	}
	s.maintenanceError = result
	// Keep a failed publication's reservation until maintenance has made
	// room for it. Other admissions wait instead of consuming that room.
	if reservation.bytes <= s.config.LimitBytes && (reservation.bytes > 0 || reservation.files > 0 || errors.Is(result, errMaintenancePending)) {
		reservation.pinned = !s.pressurePending && reservation.bytes == 0 && reservation.files == 0
		s.reserveMaintenance(reservation)
	}
}

func (s *Service) relieveSpacePressure(reservation archiveReservation, now time.Time, work *cleanupWork) error {
	for {
		used, err := s.allocated()
		if err != nil {
			return err
		}
		pressure, err := s.archivePressure(used, reservation)
		if err != nil || !pressure {
			return err
		}
		if err = s.retireForPressure(now, work); errors.Is(err, errRetentionChanged) {
			continue
		} else if err != nil {
			return err
		}
	}
}

func (s *Service) retireForPressure(now time.Time, work *cleanupWork) error {
	if work.retirements >= maxRetirements {
		return errMaintenancePending
	}
	// Space pressure uses one global age order, irrespective of each
	// generation's separate normal-expiry policy.
	oldest, _, pending := s.retirementCandidate(now, true, work)
	if pending {
		return errMaintenancePending
	}
	if oldest < 0 {
		return fmt.Errorf("transport archive budget exhausted")
	}
	return s.retireVerified(oldest, now, true, work)
}

func (s *Service) archivePressure(used int64, reservation archiveReservation) (bool, error) {
	if used > s.config.LimitBytes || reservation.bytes > s.config.LimitBytes-used {
		return true, nil
	}
	if s.config.MinFreeBytes == 0 && s.config.MinFreeInodes == 0 {
		return false, nil
	}
	space, err := s.availableSpace()
	if err != nil {
		return false, fmt.Errorf("archive filesystem space unavailable: %w", err)
	}
	needBytes := uint64(s.config.MinFreeBytes) + uint64(reservation.bytes)
	needInodes := uint64(s.config.MinFreeInodes) + reservation.files
	return space.bytes < needBytes || space.inodes < needInodes, nil
}

func (s *Service) closeRetainedBlocks(now time.Time) {
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		switch b.Kind {
		case metroDetailSegmentKind:
			b.Closed = !b.Age.Add(metroDetailSegmentInterval).After(now.UTC())
		case "detail", "correction", "observations", "popup-events", "popup-checkpoint", "metro-inputs":
			b.Closed = b.Age.Before(now.UTC().Truncate(time.Hour))
		case "aggregate":
			b.Closed = b.Date < now.In(lisbon).Format("2006-01-02")
		}
	}
}

func (s *Service) retirementCandidate(now time.Time, pressure bool, work *cleanupWork) (int, bool, bool) {
	// Metadata is bounded by the manifest size and does not read generation files.
	// Verify in age order so the first verified candidate is globally oldest.
	for _, i := range s.retirementCandidates(now, pressure) {
		b := s.index.Blocks[i]
		if !s.retentionCached(b) {
			if work.checks >= maxRetentionChecks {
				return -1, false, true
			}
			work.checks++
		}
		if (!pressure && s.blockExpired(b, now)) || (pressure && s.pressureEligible(b, now)) {
			return i, true, false
		}
	}
	return -1, false, false
}

func (s *Service) retirementCandidates(now time.Time, pressure bool) []int {
	indices := make([]int, 0)
	for i, b := range s.index.Blocks {
		if !b.Closed || b.Kind == "state" || s.protectedGenerations[retentionKey(b)] {
			continue
		}
		cutoff, ok := s.expiryCutoff(b, now)
		if pressure {
			cutoff, ok = now.AddDate(0, 0, -7), true
		}
		if ok && pastMetadataBoundary(b, cutoff, now) {
			indices = append(indices, i)
		}
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return s.index.Blocks[indices[i]].Age.Before(s.index.Blocks[indices[j]].Age)
	})
	return indices
}

func (s *Service) retentionCached(b block) bool {
	info, err := os.Stat(filepath.Join(s.config.Directory, b.File))
	if err != nil {
		// Missing generations are retained; their check is cheap and repeatable.
		return true
	}
	cached, ok := s.aggregateClocks[retentionKey(b)]
	return ok && cachedRetentionClock(cached, b, info)
}

func (s *Service) retireVerified(index int, now time.Time, pressure bool, work *cleanupWork) error {
	if work.checks >= maxRetentionChecks || work.retirements >= maxRetirements {
		return errMaintenancePending
	}
	work.checks++
	b := s.index.Blocks[index]
	// Rehash the complete generation immediately before retirement. If it is
	// unchanged, a validated cached clock for this exact block remains valid.
	if _, err := s.readBlock(b); err != nil {
		delete(s.aggregateClocks, retentionKey(b))
		return errRetentionChanged
	}
	if (!pressure && !s.blockExpired(b, now)) || (pressure && !s.pressureEligible(b, now)) {
		return errRetentionChanged
	}
	if err := s.retireBlock(index); err != nil {
		return err
	}
	work.retirements++
	return nil
}

func (s *Service) retireBlock(index int) error {
	removed := s.index.Blocks[index]
	next := manifest{Version: 1, Blocks: append([]block{}, s.index.Blocks...)}
	next.Blocks = append(next.Blocks[:index], next.Blocks[index+1:]...)
	if err := s.ensureRetirementHeadroom(next); err != nil {
		return err
	}
	// Publish retirement before unlinking; serialized readers have already closed.
	if err := s.saveManifest(next); err != nil {
		return err
	}
	s.forgetRetiredAggregate(removed)
	delete(s.aggregateClocks, retentionKey(removed))
	return s.removeRetiredGeneration(removed)
}

func (s *Service) ensureRetirementHeadroom(next manifest) error {
	// Retirement also needs a temporary manifest alongside the old one.
	blob, err := encodeManifest(next)
	if err != nil {
		return err
	}
	space, err := s.availableSpace()
	if err != nil {
		return fmt.Errorf("archive filesystem space unavailable: %w", err)
	}
	// A lowered archive limit may already be exceeded. Allow the one temporary
	// manifest needed for safe retirement only when the filesystem can hold it.
	if space.bytes < uint64(allocatedBytes(len(blob))) || space.inodes < 1 {
		return fmt.Errorf("insufficient filesystem space to retire archive data")
	}
	return nil
}

func (s *Service) removeRetiredGeneration(removed block) error {
	if s.generationReferenced(removed.File) {
		return nil
	}
	s.forgetRetentionFile(removed.File)
	err := os.Remove(filepath.Join(s.config.Directory, removed.File))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDirectory(s.config.Directory)
}

func (s *Service) generationReferenced(name string) bool {
	for _, b := range s.index.Blocks {
		if b.File == name {
			return true
		}
	}
	return false
}

func (s *Service) forgetRetentionFile(name string) {
	for key := range s.aggregateClocks {
		if key.file == name {
			delete(s.aggregateClocks, key)
		}
	}
}

func (s *Service) trimRetentionClocks() {
	if len(s.aggregateClocks) <= len(s.index.Blocks)+128 {
		return
	}
	live := make(map[retentionClockKey]bool, len(s.index.Blocks))
	for _, b := range s.index.Blocks {
		live[retentionKey(b)] = true
	}
	for key := range s.aggregateClocks {
		if !live[key] {
			delete(s.aggregateClocks, key)
		}
	}
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
