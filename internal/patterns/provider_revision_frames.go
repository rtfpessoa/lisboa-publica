package patterns

import (
	"fmt"
	"sort"
	"time"
)

func (j *providerRevision) loadFrames() error {
	existing, err := j.service.providerCorrections(j.operator)
	if err != nil {
		return err
	}
	used := 0
	for _, b := range j.service.index.Blocks {
		// Reservation protects every original input and checkpoint.
		j.sourceFiles[b.File] = true
		if b.Operator != j.operator || b.Kind != "observations" {
			continue
		}
		raw, err := j.service.revisionBlock(b, &used, "provider revision replay limit")
		if err != nil {
			return err
		}
		frames, err := decodeRevisionFrames(raw, 25000-len(j.frames), "provider revision receipt limit", func(r *ProviderReceipt) { applyProviderCorrections(r, existing) })
		if err != nil {
			return err
		}
		j.frames = append(j.frames, frames...)
	}
	sort.SliceStable(j.frames, func(i, jj int) bool { return j.frames[i].ReceivedAt.Before(j.frames[jj].ReceivedAt) })
	j.findEarliestInput()
	return nil
}

func (j *providerRevision) findEarliestInput() {
	for _, frame := range j.frames {
		for _, row := range frame.Rows {
			at := row.ObservedAt
			if at.IsZero() || at.After(frame.ReceivedAt) || frame.ReceivedAt.Sub(at) > 90*time.Second {
				continue
			}
			if j.earliestInput == 0 || at.UnixNano() < j.earliestInput {
				j.earliestInput = at.UnixNano()
			}
		}
	}
}

func (j *providerRevision) loadPaths() error {
	for _, r := range j.frames {
		for id, path := range r.Journeys {
			if !validProviderPath(j.operator, id, path) {
				return fmt.Errorf("provider revision topology invalid")
			}
			j.paths[id] = path
		}
	}
	return nil
}
