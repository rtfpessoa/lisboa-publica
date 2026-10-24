package patterns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// flushSegmented publishes the checkpoint, active detail, and every changed
// day in a single manifest. Generations are encoded one at a time.
func (s *Service) flushSegmented(now time.Time) error {
	if err := s.prepareFlush(now); err != nil {
		return s.markPaused(err)
	}
	raw, err := s.checkpointPayload()
	if err != nil {
		return s.markPaused(err)
	}
	fingerprint := digestBytes(raw)
	cp := s.checkpointFor(s.providers, s.engine)
	items, err := s.segmentFlushItems(cp, now)
	if err != nil {
		return s.markPaused(err)
	}
	job := streamTransaction{service: s, context: context.Background(), items: items, now: now,
		validate: func() error {
			current, err := s.marshalCheckpoint()
			if err != nil {
				return err
			}
			if digestBytes(current) != fingerprint {
				return fmt.Errorf("archive state changed during transaction admission; retry")
			}
			return nil
		},
	}
	if err := job.publish(); err != nil {
		if s.detailFence != nil || s.manifestUncertain {
			s.protectCheckpointDetails()
		}
		return s.markPaused(err)
	}
	if cp.SegmentStart != nil {
		s.segmentFormat = true
	}
	s.acknowledgeFlush(now)
	return nil
}

func (s *Service) segmentFlushItems(cp checkpoint, now time.Time) ([]transactionItem, error) {
	items, err := s.segmentCheckpointItems(cp, now)
	if err != nil {
		return nil, err
	}
	metro, err := s.metroDayItems(now)
	if err != nil {
		return nil, err
	}
	items = append(items, metro...)
	providers, err := s.providerDayItems(now)
	if err != nil {
		return nil, err
	}
	items = append(items, providers...)
	return append(items, s.checkpointItem(now)), nil
}

func (s *Service) segmentCheckpointItems(cp checkpoint, now time.Time) ([]transactionItem, error) {
	items := []transactionItem{}
	if len(cp.Detail) > 0 {
		item, err := s.checkpointDetailItem(cp, now)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if cp.SegmentStart != nil {
		marker, err := s.segmentFormatItem()
		if err != nil {
			return nil, err
		}
		items = append(items, marker)
	}
	return items, nil
}

func (s *Service) checkpointItem(now time.Time) transactionItem {
	state := block{Key: "metro:state", Kind: "state", Operator: "metro", Age: now}
	return transactionItem{block: state, encode: func() ([]byte, error) {
		raw, err := s.marshalCheckpoint()
		if err != nil {
			return nil, err
		}
		if len(raw) > maxBlockBytes {
			return nil, fmt.Errorf("archive checkpoint limit")
		}
		return s.encoder.EncodeAll(raw, nil), nil
	}}
}

func (s *Service) checkpointDetailItem(cp checkpoint, now time.Time) (transactionItem, error) {
	start, kind, prefix, interval := cp.Hour, "detail", "metro:detail:", time.Hour
	if cp.SegmentStart != nil {
		start, kind, prefix, interval = *cp.SegmentStart, metroDetailSegmentKind, metroDetailSegmentPrefix, metroDetailSegmentInterval
	}
	b := block{Key: prefix + start.UTC().Format(time.RFC3339), Kind: kind, Operator: "metro", Age: start,
		AsOf: cp.Detail[len(cp.Detail)-1].ReceivedAt, Samples: int64(len(cp.Detail)), Closed: !start.Add(interval).After(now.UTC())}
	source, err := s.publicationSource(b.Key)
	if err != nil {
		return transactionItem{}, err
	}
	if source.block != nil {
		rows, err := s.readDetailBlock(*source.block)
		if err != nil {
			return transactionItem{}, err
		}
		if !receiptPrefix(rows, cp.Detail) {
			return transactionItem{}, s.fenceDetail(fmt.Errorf("detail would omit retained receipts"), source.block)
		}
	}
	return transactionItem{block: b, encode: func() ([]byte, error) {
		var raw bytes.Buffer
		encoder := json.NewEncoder(&raw)
		for _, receipt := range cp.Detail {
			if err := encoder.Encode(receipt); err != nil {
				return nil, err
			}
		}
		if raw.Len() > maxDetailHourBytes {
			return nil, fmt.Errorf("detail byte limit")
		}
		return s.verifiedDetailBlob(raw.Bytes())
	}}, nil
}

// Preserve admitted raw detail if an unencodable engine prevents a shared
// checkpoint. The unchanged checkpoint deliberately makes this tail unproven
// on restart, so collection remains fenced until it can be recovered.
func (s *Service) preserveUncheckpointedSegment(now time.Time) error {
	if err := s.prepareFlush(now); err != nil {
		return err
	}
	cp := s.checkpointFor(s.providers, s.engine)
	if cp.SegmentStart == nil || len(cp.Detail) == 0 {
		return fmt.Errorf("no admitted segment to preserve")
	}
	detail, err := s.checkpointDetailItem(cp, now)
	if err != nil {
		return err
	}
	marker, err := s.segmentFormatItem()
	if err != nil {
		return err
	}
	job := streamTransaction{service: s, context: context.Background(), items: []transactionItem{detail, marker}, now: now}
	err = job.publish()
	s.protectCheckpointDetails()
	return err
}

// Revision publishers share the Metro checkpoint. Include its active segment
// and format marker in the same transaction, even without a new Metro sample.
func (s *Service) segmentCheckpointBlocks(cp checkpoint, now time.Time) ([]preparedBlock, error) {
	if cp.SegmentStart == nil {
		return nil, nil
	}
	detail, err := s.checkpointDetailItem(cp, now)
	if err != nil {
		return nil, err
	}
	marker, err := s.segmentFormatItem()
	if err != nil {
		return nil, err
	}
	prepared := make([]preparedBlock, 0, 2)
	for _, item := range []transactionItem{detail, marker} {
		blob, err := item.encode()
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, preparedBlock{block: item.block, blob: blob})
	}
	return prepared, nil
}

func (s *Service) metroDayItems(now time.Time) ([]transactionItem, error) {
	byDay, dates := s.changedMetroDays()
	items := make([]transactionItem, 0, len(dates))
	for _, date := range dates {
		if len(byDay[date]) == 0 {
			continue
		}
		age, err := time.ParseInLocation("2006-01-02", date, lisbon)
		if err != nil {
			return nil, err
		}
		b := block{Key: "metro:aggregate:" + date, Kind: "aggregate", Operator: "metro", Date: date,
			Age: age.UTC(), Closed: date < now.In(lisbon).Format("2006-01-02")}
		items = append(items, transactionItem{block: b, encode: func() ([]byte, error) {
			return verifiedAggregateBlob(aggregateDayRows(s.engine.Aggregates, date))
		}})
	}
	return items, nil
}

func (s *Service) providerDayItems(now time.Time) ([]transactionItem, error) {
	operators := make([]string, 0, len(s.providers))
	for operator := range s.providers {
		operators = append(operators, operator)
	}
	sort.Strings(operators)
	items := []transactionItem{}
	for _, operator := range operators {
		state := s.providers[operator]
		dates := make([]string, 0, len(state.Engine.DirtyDays))
		for date := range state.Engine.DirtyDays {
			dates = append(dates, date)
		}
		sort.Strings(dates)
		for _, date := range dates {
			age, err := time.ParseInLocation("2006-01-02", date, lisbon)
			if err != nil {
				return nil, err
			}
			b := block{Key: operator + ":aggregate:" + date, Kind: "aggregate", Operator: operator, Date: date, Age: age.UTC(), AsOf: now}
			items = append(items, transactionItem{block: b, encode: func() ([]byte, error) {
				return verifiedAggregateBlob(aggregateDayRows(state.Engine.Aggregates, date))
			}})
		}
	}
	return items, nil
}
