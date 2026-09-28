package patterns

import (
	"encoding/json"
	"fmt"
)

func (j *providerRevision) restoreAffectedDays() error {
	j.service.operatorEngine(j.operator)
	raw, err := json.Marshal(j.service.providers[j.operator])
	if err != nil {
		return err
	}
	j.replacement = &providerState{}
	if err := json.Unmarshal(raw, j.replacement); err != nil {
		return err
	}
	earliest := j.frames[0].ReceivedAt.UnixNano()
	if j.earliestInput > 0 {
		earliest = min(earliest, j.earliestInput)
	}
	for _, b := range j.service.index.Blocks {
		if b.Operator != j.operator || b.Kind != "aggregate" || !j.days[b.Date] {
			continue
		}
		if err := j.restoreDay(b, earliest); err != nil {
			return err
		}
	}
	return nil
}

func (j *providerRevision) restoreDay(b block, earliest int64) error {
	blob, err := j.service.readBlock(b)
	if err != nil {
		return err
	}
	rows, err := readAggregates(blob)
	if err != nil {
		return err
	}
	if err := j.mergeRetainedDay(rows, earliest); err != nil {
		return err
	}
	delete(j.replacement.Engine.ColdDays, b.Date)
	return nil
}

func (j *providerRevision) mergeRetainedDay(rows []Aggregate, earliest int64) error {
	for _, a := range rows {
		if j.affected[a.Route+"|"+a.Direction] && aggregatePredatesRevision(a, earliest) {
			return fmt.Errorf("provider aggregate predates reconstructable detail")
		}
		key := aggregateKey(a)
		if _, exists := j.replacement.Engine.Aggregates[key]; !exists && len(j.replacement.Engine.Aggregates) >= maxProviderAggregates {
			return errHistoryLimit
		}
		j.replacement.Engine.Aggregates[key] = a
	}
	return nil
}

func (j *providerRevision) replayFrames() error {
	j.replay = newProvider(j.operator)
	for _, r := range j.frames {
		if err := j.context.Err(); err != nil {
			return err
		}
		j.replay.step(r, j.service.config, true, true)
		if j.replay.Engine.Limited {
			return fmt.Errorf("provider revision hot state limit")
		}
	}
	return nil
}

func (j *providerRevision) replaceState() error {
	e := j.replacement.Engine
	for key, a := range e.Aggregates {
		if j.affected[a.Route+"|"+a.Direction] && j.days[a.Date] {
			delete(e.Aggregates, key)
		}
	}
	for _, a := range j.replay.Engine.Aggregates {
		if !j.affected[a.Route+"|"+a.Direction] || !j.days[a.Date] {
			continue
		}
		if len(e.Aggregates) >= maxProviderAggregates {
			return errHistoryLimit
		}
		a.KnownAt = j.now.UnixNano()
		e.add(a)
	}
	j.replacement.reset()
	return nil
}
