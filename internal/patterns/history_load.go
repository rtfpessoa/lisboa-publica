package patterns

import (
	"bytes"
	"errors"
	"sort"

	"github.com/parquet-go/parquet-go"
)

var errHistoryLimit = errors.New("historical aggregate row limit")

// Restore retained summaries without counting them twice.
// The hot limit remains bounded; older blocks are still available for analysis.
type componentRecovery struct {
	service  *Service
	restored map[string]Aggregate
	limited  bool
}

func (s *Service) loadComponentHistory() error {
	recovery := componentRecovery{service: s, restored: map[string]Aggregate{}}
	blocks := append([]block{}, s.index.Blocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Date > blocks[j].Date })
	for _, b := range blocks {
		if b.Kind != "aggregate" || b.Operator != "metro" {
			continue
		}
		if err := recovery.restoreDay(b); err != nil {
			return err
		}
	}
	s.engine.Aggregates = recovery.restored
	return nil
}

func (r *componentRecovery) restoreDay(b block) error {
	if r.limited {
		r.service.engine.markColdDay(b.Date)
		return nil
	}
	rows, err := r.service.retainedComponentDay(b)
	if err != nil {
		return err
	}
	if len(r.restored)+len(rows) > maxEngineAggregates {
		r.service.engine.Limited, r.limited = true, true
		r.service.engine.markColdDay(b.Date)
	} else {
		delete(r.service.engine.ColdDays, b.Date)
		for key, a := range rows {
			r.restored[key] = a
		}
	}
	return nil
}

func (s *Service) retainedComponentDay(b block) (map[string]Aggregate, error) {
	blob, err := s.readBlock(b)
	if err != nil {
		return nil, err
	}
	rows, err := readAggregates(blob)
	if err != nil {
		return nil, err
	}
	if len(rows) > maxEngineAggregates {
		return nil, errHistoryLimit
	}
	dayRows := map[string]Aggregate{}
	// Pending withdrawals cannot be resurrected from an older generation.
	if !s.engine.DirtyDays[b.Date] {
		for _, a := range rows {
			dayRows[aggregateKey(a)] = a
		}
	}
	mergeHotComponentDay(dayRows, s.engine.Aggregates, b.Date)
	return dayRows, nil
}

func mergeHotComponentDay(dayRows, hot map[string]Aggregate, date string) {
	for key, a := range hot {
		if a.Date != date {
			continue
		}
		if old, exists := dayRows[key]; !exists || a.KnownAt > old.KnownAt {
			dayRows[key] = a
		}
	}
}

func aggregateKey(a Aggregate) string {
	a.Count, a.Sum, a.LowerSum, a.UpperSum, a.KnownAt, a.InputFrom = 0, 0, 0, 0, 0, 0
	return digest(a)
}

func readAggregates(blob []byte) ([]Aggregate, error) {
	file, err := parquet.OpenFile(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, err
	}
	if file.NumRows() > maxEngineAggregates {
		return nil, errHistoryLimit
	}
	return parquet.Read[Aggregate](bytes.NewReader(blob), int64(len(blob)))
}
