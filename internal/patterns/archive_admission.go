package patterns

import (
	"errors"
	"fmt"
	"time"
)

func allocatedBytes(size int) int64 { return int64((size + 4095) / 4096 * 4096) }

// Admission validates the complete candidate before writing a generation or
// retiring data for pressure. Expiry alone may make room for an oversized
// candidate; it never uses pressure eviction to satisfy the manifest ceiling.
func (s *Service) admitManifest(build func() manifest, generationBytes int64, now time.Time) error {
	if s.manifestUncertain {
		return fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	s.closeRetainedBlocks(now)
	for {
		blob, err := encodeManifest(build())
		if err != nil {
			if !errors.Is(err, errManifestLimit) {
				return err
			}
			index, expired := s.retirementCandidate(now, false)
			if !expired {
				return err
			}
			if err = s.retireBlock(index); err != nil {
				return err
			}
			continue
		}
		count := len(s.index.Blocks)
		if err = s.ensureSpace(generationBytes+allocatedBytes(len(blob)), now); err != nil {
			return err
		}
		if len(s.index.Blocks) == count {
			return nil
		}
	}
}
