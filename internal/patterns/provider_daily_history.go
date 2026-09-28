package patterns

import (
	"fmt"
	"sort"
	"time"
)

func (s *Service) flushProviders(now time.Time) error {
	for operator, state := range s.providers {
		if err := s.flushProviderDays(operator, state, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) flushProviderDays(operator string, state *providerState, now time.Time) error {
	dates := []string{}
	for date := range state.Engine.DirtyDays {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	for _, date := range dates {
		if err := s.flushProviderDay(operator, state.Engine, date, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) flushProviderDay(operator string, e *engine, date string, now time.Time) error {
	blob, err := verifiedAggregateBlob(aggregateDayRows(e.Aggregates, date))
	if err != nil {
		return err
	}
	age, err := time.ParseInLocation("2006-01-02", date, lisbon)
	if err != nil {
		return err
	}
	b := block{Key: operator + ":aggregate:" + date, Kind: "aggregate", Operator: operator, Date: date, Age: age.UTC(), AsOf: now}
	return s.publish(b, blob, now)
}

func (s *Service) restoreProviderDay(b block) error {
	e := s.operatorEngine(b.Operator)
	if e.RecoveryThrough == nil {
		e.RecoveryThrough = map[string]int64{}
	}
	blob, err := s.readBlock(b)
	if err != nil {
		return err
	}
	rows, err := readAggregates(blob)
	if err != nil {
		return err
	}
	return admitRecoveredProviderDay(e, b, rows)
}

func admitRecoveredProviderDay(e *engine, b block, rows []Aggregate) error {
	if err := validateProviderDay(b, rows); err != nil {
		return err
	}
	// Durable coverage survives even when a complete day does not fit hot memory.
	e.RecoveryThrough[b.Date] = b.AsOf.UnixNano()
	if len(e.Aggregates)+len(rows) > maxProviderAggregates {
		e.Limited = true
		e.markColdDay(b.Date)
		return nil
	}
	delete(e.ColdDays, b.Date)
	for _, a := range rows {
		e.Aggregates[aggregateKey(a)] = a
	}
	return nil
}

func validateProviderDay(b block, rows []Aggregate) error {
	for _, a := range rows {
		if a.Operator != b.Operator || a.Date != b.Date || a.Reference != providerReference {
			return fmt.Errorf("provider aggregate identity")
		}
	}
	return nil
}
