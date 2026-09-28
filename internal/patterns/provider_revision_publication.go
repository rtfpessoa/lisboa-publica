package patterns

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

func (j *providerRevision) prepareBlocks() error {
	dates := []string{}
	for date := range j.days {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	for _, date := range dates {
		if err := j.prepareDay(date); err != nil {
			return err
		}
	}
	ledger, err := j.service.prepareRevisionLedger(j.operator, j.changes, j.now, true)
	if err != nil {
		return err
	}
	j.prepared = append(j.prepared, ledger)
	return j.prepareCheckpoint()
}

func (j *providerRevision) prepareDay(date string) error {
	blob, err := revisionAggregateBlob(aggregateDayRows(j.replacement.Engine.Aggregates, date))
	if err != nil {
		return err
	}
	age, err := time.ParseInLocation("2006-01-02", date, lisbon)
	if err != nil {
		return err
	}
	b := block{Key: j.operator + ":aggregate:" + date, Kind: "aggregate", Operator: j.operator, Date: date, Age: age.UTC(), AsOf: j.now}
	j.prepared = append(j.prepared, preparedBlock{b, blob})
	return nil
}

func (j *providerRevision) prepareCheckpoint() error {
	states := map[string]*providerState{}
	for op, p := range j.service.providers {
		states[op] = p
	}
	states[j.operator] = j.replacement
	s := j.service
	cp := checkpoint{states, s.engine, s.hour, s.detail, s.config.BinSeconds, int(s.config.SampleInterval / time.Second), s.topology}
	raw, err := json.Marshal(cp)
	if err != nil || len(raw) > maxBlockBytes {
		return fmt.Errorf("provider revised checkpoint limit")
	}
	j.prepared = append(j.prepared, preparedBlock{block{Key: "metro:state", Kind: "state", Operator: "metro", Age: j.now}, s.encoder.EncodeAll(raw, nil)})
	return nil
}

func (j *providerRevision) publish() error {
	if err := j.service.publishTransaction(j.context, j.prepared, j.sourceFiles, j.now); err != nil {
		return err
	}
	j.service.providers[j.operator] = j.replacement
	j.service.lastCheckpoint = j.now
	j.replacement.Engine.DirtyDays = map[string]bool{}
	return nil
}
