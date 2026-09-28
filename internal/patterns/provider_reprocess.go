package patterns

import (
	"context"
	"fmt"
	"time"
)

type providerRevision struct {
	service             *Service
	context             context.Context
	operator            string
	changes             []ProviderCorrection
	now                 time.Time
	frames              []ProviderReceipt
	sourceFiles         map[string]bool
	paths               map[string]ProviderJourney
	affected, days      map[string]bool
	earliestInput       int64
	replacement, replay *providerState
	prepared            []preparedBlock
}

// ReprocessProvider revises verified retained inputs while preserving issued forecasts.
func (s *Service) ReprocessProvider(ctx context.Context, operator string, changes []ProviderCorrection, now time.Time) (ReprocessResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := ReprocessResult{PartialCoverage: true}
	job := &providerRevision{service: s, context: ctx, operator: operator, changes: changes, now: now, sourceFiles: map[string]bool{}, paths: map[string]ProviderJourney{}, affected: map[string]bool{}, days: map[string]bool{}}
	steps := []func() error{job.begin, job.loadFrames, job.loadPaths, job.applyChanges, job.expandDays, job.restoreAffectedDays, job.replayFrames, job.replaceState, job.prepareBlocks, job.publish}
	for _, step := range steps {
		if err := step(); err != nil {
			return result, err
		}
	}
	return ReprocessResult{Receipts: len(job.frames), Corrections: len(changes), Days: len(job.days), PartialCoverage: true}, nil
}

func (j *providerRevision) begin() error {
	if err := j.context.Err(); err != nil {
		return err
	}
	if j.service.lock == nil || !knownOperator(j.operator) || j.operator == "metro" || len(j.changes) == 0 || len(j.changes) > 1000 {
		return fmt.Errorf("invalid provider revision")
	}
	return j.service.flush(j.now)
}
