package patterns

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func allocatedBytes(size int) int64 { return int64((size + 4095) / 4096 * 4096) }

// Admission validates the complete candidate before writing a generation or
// retiring data for pressure. Expiry alone may make room for an oversized
// candidate; it never uses pressure eviction to satisfy the manifest ceiling.
func (s *Service) admitManifest(build func() manifest, generationBytes int64, now time.Time) error {
	return s.admitManifestFiles(build, generationBytes, 1, now)
}

type manifestAdmission struct {
	build           func() manifest
	generationBytes int64
	generationFiles uint64
	now             time.Time
	work            *cleanupWork
	expiryTried     bool
}

func (s *Service) admitManifestFiles(build func() manifest, generationBytes int64, generationFiles uint64, now time.Time) error {
	s.expireUnownedReservation()
	if s.pressurePending {
		return s.maintenanceError
	}
	if s.manifestUncertain {
		return fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	s.closeRetainedBlocks(now)
	admission := manifestAdmission{build: build, generationBytes: generationBytes, generationFiles: generationFiles,
		now: now, work: s.cleanupBudget(), expiryTried: s.maintenanceWork != nil}
	for {
		retry, err := s.prepareManifestCandidate(&admission)
		if err != nil || !retry {
			return err
		}
	}
}

func (s *Service) prepareManifestCandidate(admission *manifestAdmission) (bool, error) {
	candidate := admission.build()
	blob, err := encodeManifest(candidate)
	if err != nil {
		return true, s.handleManifestEncodingError(candidate, err, admission)
	}
	count := len(s.index.Blocks)
	reserve := admission.generationBytes + allocatedBytes(len(blob))
	if err = s.ensureSpaceFor(reserve, admission.generationFiles+1, admission.now, admission.work); err != nil {
		return false, err
	}
	if len(s.index.Blocks) != count {
		return true, nil
	}
	return s.expireDuringAdmission(admission, count)
}

func (s *Service) handleManifestEncodingError(candidate manifest, err error, admission *manifestAdmission) error {
	if !errors.Is(err, errManifestLimit) {
		return err
	}
	return s.admitOversizedManifest(candidate, admission.now, admission.work)
}

func (s *Service) expireDuringAdmission(admission *manifestAdmission, count int) (bool, error) {
	if admission.expiryTried {
		return false, nil
	}
	admission.expiryTried = true
	if _, err := s.expireSome(admission.now, admission.work); err != nil {
		s.maintenanceError = err
		return false, err
	}
	return len(s.index.Blocks) != count, nil
}

// admitOversizedManifest tries one bounded expiry step, then the caller rebuilds
// the manifest to account for any retired entries and replacements.
func (s *Service) admitOversizedManifest(candidate manifest, now time.Time, work *cleanupWork) error {
	count := len(s.index.Blocks)
	if err := s.ensureSpaceFor(0, 0, now, work); err != nil {
		return s.waitForManifest(candidate, err)
	}
	if len(s.index.Blocks) != count {
		return nil
	}
	if work.retirements >= maxRetirements {
		return s.waitForManifest(candidate, errMaintenancePending)
	}
	index, expired, pending := s.retirementCandidate(now, false, work)
	if pending {
		return s.waitForManifest(candidate, errMaintenancePending)
	}
	if !expired {
		return s.waitForManifest(candidate, errManifestLimit)
	}
	if err := s.retireVerified(index, now, false, work); err != nil && !errors.Is(err, errRetentionChanged) {
		return s.waitForManifest(candidate, err)
	}
	return nil
}

// Reject a candidate whose new entries alone cannot fit. Do not retain the
// candidate: a buffered flush will rebuild and check its actual manifest.
func (s *Service) waitForManifest(candidate manifest, cause error) error {
	current := make(map[block]int, len(s.index.Blocks))
	for _, b := range s.index.Blocks {
		current[b]++
	}
	addition := 16 // conservative allowance for manifest punctuation
	for _, b := range candidate.Blocks {
		if current[b] > 0 {
			current[b]--
			continue
		}
		raw, err := json.Marshal(b)
		if err != nil {
			return err
		}
		addition += len(raw) + 1
		if addition >= maxManifestBytes-32 {
			return errManifestLimit
		}
	}
	s.reserveMaintenance(archiveReservation{})
	s.pressureManifest = true
	s.maintenanceError = cause
	return cause
}
