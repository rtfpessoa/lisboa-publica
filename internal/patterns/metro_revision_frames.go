package patterns

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type metroRevision struct {
	service                       *Service
	context                       context.Context
	changes, existing             []Correction
	now                           time.Time
	frames                        []Receipt
	sourceFiles, affected, days   map[string]bool
	earliestInput                 int64
	replay, replacement, retained *engine
	prepared                      []preparedBlock
	used                          int
}

func newMetroRevision(s *Service, ctx context.Context, changes []Correction, now time.Time) *metroRevision {
	files := map[string]bool{}
	for _, b := range s.index.Blocks {
		files[b.File] = true
	}
	return &metroRevision{service: s, context: ctx, changes: changes, now: now, sourceFiles: files, affected: map[string]bool{}, days: map[string]bool{}}
}

func (j *metroRevision) loadFrames() error {
	for _, b := range j.service.index.Blocks {
		if err := j.context.Err(); err != nil {
			return err
		}
		if b.Operator != "metro" || b.Kind != "detail" && b.Kind != "correction" {
			continue
		}
		if err := j.loadFrameBlock(b); err != nil {
			return err
		}
	}
	sort.Slice(j.frames, func(a, b int) bool { return j.frames[a].ReceivedAt.Before(j.frames[b].ReceivedAt) })
	sort.SliceStable(j.existing, func(a, b int) bool { return j.existing[a].ReceivedAt.Before(j.existing[b].ReceivedAt) })
	j.earliestInput = earliestMetroRevisionInput(j.frames)
	return nil
}

func (j *metroRevision) loadFrameBlock(b block) error {
	raw, err := j.service.revisionBlock(b, &j.used, "reprocessing working set limit")
	if err != nil {
		return err
	}
	if b.Kind == "correction" {
		return j.loadCorrectionLedger(raw)
	}
	values, err := decodeRevisionFrames(raw, 25000-len(j.frames), "reprocessing receipt limit", func(r *Receipt) { r.Raw = nil })
	j.frames = append(j.frames, values...)
	return err
}

func (j *metroRevision) loadCorrectionLedger(raw []byte) error {
	var changes []Correction
	if json.Unmarshal(raw, &changes) != nil {
		return fmt.Errorf("invalid correction ledger")
	}
	j.existing = append(j.existing, changes...)
	return nil
}

func earliestMetroRevisionInput(frames []Receipt) int64 {
	earliest := int64(0)
	for _, frame := range frames {
		for _, row := range frame.Rows {
			at, valid := sourceClock(row.Clock)
			if validMetroSampleClock(at, valid, frame.ReceivedAt) {
				earliest = earliestRevisionInput(earliest, at)
			}
		}
	}
	return earliest
}

func earliestRevisionInput(current int64, at time.Time) int64 {
	if current == 0 || at.UnixNano() < current {
		return at.UnixNano()
	}
	return current
}
