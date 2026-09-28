package patterns

import (
	"encoding/json"
	"fmt"
)

func (j *metroRevision) replayFrames() error {
	j.replay = newEngine()
	j.replay.Replaying = true
	for _, frame := range j.frames {
		if err := j.context.Err(); err != nil {
			return err
		}
		if frame.Topology == nil {
			continue
		}
		j.replay.step(frame, *frame.Topology, j.service.config)
		if err := j.replayRecordedForecasts(frame.Forecasts); err != nil {
			return err
		}
		if j.replay.Limited {
			return fmt.Errorf("metro revision hot state limit")
		}

	}
	return nil
}

func (j *metroRevision) restoreAffectedDays() error {
	if len(j.frames) == 0 {
		return fmt.Errorf("retained inputs unavailable")
	}
	if err := j.cloneRetainedEngine(); err != nil {
		return err
	}
	earliest := j.frames[0].ReceivedAt.UnixNano()
	if j.earliestInput > 0 {
		earliest = min(earliest, j.earliestInput)
	}
	for _, b := range j.service.index.Blocks {
		if b.Operator != "metro" || b.Kind != "aggregate" || !j.days[b.Date] {
			continue
		}
		if err := j.restoreDay(b, earliest); err != nil {
			return err
		}
	}
	return nil
}

func (j *metroRevision) restoreDay(b block, earliest int64) error {
	blob, err := j.service.readBlock(b)
	if err != nil {
		return err
	}
	rows, err := readAggregates(blob)
	if err != nil || len(rows) > maxEngineAggregates {
		return fmt.Errorf("reprocessing aggregate unavailable")
	}
	if err := j.mergeRetainedDay(rows, earliest); err != nil {
		return err
	}
	delete(j.retained.ColdDays, b.Date)
	return nil
}

func (j *metroRevision) mergeRetainedDay(rows []Aggregate, earliest int64) error {
	for _, a := range rows {
		if j.affected[a.Route+"|"+a.Direction] && aggregatePredatesRevision(a, earliest) {
			return fmt.Errorf("affected aggregate predates retained inputs; cannot reconstruct")
		}
		key := aggregateKey(a)
		if _, exists := j.retained.Aggregates[key]; !exists {
			if len(j.retained.Aggregates) >= maxEngineAggregates {
				return errHistoryLimit
			}
			j.retained.Aggregates[key] = a
		}
	}
	return nil
}

func aggregatePredatesRevision(a Aggregate, earliest int64) bool {
	return a.KnownAt < earliest || a.InputFrom == 0 || a.InputFrom < earliest
}

func (j *metroRevision) replaceEngine() error {
	replacement := newEngine()
	for key, a := range j.retained.Aggregates {
		if !j.affected[a.Route+"|"+a.Direction] || !j.days[a.Date] {
			replacement.Aggregates[key] = a
		}
	}
	for _, a := range j.replay.Aggregates {
		if j.affected[a.Route+"|"+a.Direction] && j.days[a.Date] {
			a.KnownAt = j.now.UnixNano()
			if len(replacement.Aggregates) >= maxEngineAggregates {
				return errHistoryLimit
			}
			replacement.add(a)
		}
	}
	replacement.ColdDays = j.retained.ColdDays
	replacement.Cases = append([]Forecast{}, j.service.engine.Cases...)
	replacement.ReportSeen, replacement.Profile = j.service.engine.ReportSeen, j.service.engine.Profile
	replacement.Topology, replacement.LastReceipt = j.service.topology, j.service.engine.LastReceipt
	replacement.Gaps = j.service.engine.Gaps + 1
	replacement.resetContinuity()
	for date := range j.days {
		replacement.DirtyDays[date] = true
	}
	j.replacement = replacement
	return nil
}

func (j *metroRevision) replayRecordedForecasts(values []Forecast) error {
	for _, forecast := range values {
		// Recorded issuance and selection are reused without recomputing values.
		j.replay.reportIssued(forecast, j.service.config)
		if forecast.Episode == "" {
			continue
		}
		if len(j.replay.Cases) >= maxPendingForecasts {
			return fmt.Errorf("metro revision pending state limit")
		}
		j.replay.Cases = append(j.replay.Cases, forecast)
	}
	return nil
}

func (j *metroRevision) cloneRetainedEngine() error {
	raw, err := json.Marshal(j.service.engine)
	if err != nil {
		return err
	}
	j.retained = newEngine()
	return json.Unmarshal(raw, j.retained)
}
