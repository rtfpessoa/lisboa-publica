package patterns

import (
	"context"
	"fmt"
	"time"
)

// Correction changes retained inputs only; it cannot alter issued forecasts.
// ExpectedHash is the digest of the currently corrected Row, guarding stale edits.
type Correction struct {
	ReceivedAt   time.Time `json:"received_at"`
	ExpectedHash string    `json:"expected_hash"`
	Row          Row       `json:"row"`
	Evidence     string    `json:"evidence"`
}
type ReprocessResult struct {
	Receipts, Corrections, Days int
	PartialCoverage             bool
}
type preparedBlock struct {
	block block
	blob  []byte
}

// Reprocess revises retained inputs exclusively, preserving original emissions.
func (s *Service) Reprocess(ctx context.Context, changes []Correction, now time.Time) (ReprocessResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ReprocessResult{PartialCoverage: true}
	if err := s.beginMetroRevision(ctx, len(changes), now); err != nil {
		return result, err
	}
	job := newMetroRevision(s, ctx, changes, now)
	steps := []func() error{job.loadFrames, job.applyChanges, job.replayFrames, job.restoreAffectedDays, job.replaceEngine, job.prepareBlocks, job.publish}
	for _, step := range steps {
		if err := step(); err != nil {
			return result, err
		}
	}
	return ReprocessResult{Receipts: len(job.frames), Corrections: len(changes), Days: len(job.days), PartialCoverage: true}, nil
}

func (s *Service) beginMetroRevision(ctx context.Context, count int, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.lock == nil || count == 0 || count > 1000 {
		return fmt.Errorf("invalid reprocessing request")
	}
	return s.flush(now)
}
