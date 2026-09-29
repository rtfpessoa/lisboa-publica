package patterns

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Changed input batches share the existing archive owner and allocated-byte
// FIFO. Five-minute chunks bound manifest growth and retain original receipts.
func (s *Service) RecordMetroInputs(ctx context.Context, records []MetroEventRecord, now time.Time) error {
	groups, err := metroInputGroups(records)
	if err != nil || len(records) == 0 {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return fmt.Errorf("archive closed")
	}
	prepared, err := s.prepareMetroInputs(groups, now)
	if err == nil {
		err = s.publishTransaction(ctx, prepared, nil, now)
	}
	return err
}
func (s *Service) prepareMetroInputs(groups map[string][]MetroEventRecord, now time.Time) ([]preparedBlock, error) {
	keys := []string{}
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	prepared := []preparedBlock{}
	for _, key := range keys {
		item, err := s.prepareMetroInputChunk(key, groups[key], now)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, item)
	}
	return prepared, nil
}
func (s *Service) prepareMetroInputChunk(key string, records []MetroEventRecord, now time.Time) (preparedBlock, error) {
	retained, err := s.retainedMetroEvents(key)
	if err != nil {
		return preparedBlock{}, err
	}
	for _, record := range records {
		retained[record.ID] = record
	}
	raw, err := marshalMetroInputChunk(retained)
	if err != nil {
		return preparedBlock{}, err
	}
	age := records[0].At.UTC().Truncate(5 * time.Minute)
	b := block{Key: key, Kind: "metro-inputs", Operator: "metro", Age: age, Closed: age.Add(5 * time.Minute).Before(now)}
	return preparedBlock{block: b, blob: s.encoder.EncodeAll(raw, nil)}, nil
}
func marshalMetroInputChunk(records map[string]MetroEventRecord) ([]byte, error) {
	values := []MetroEventRecord{}
	for _, record := range records {
		values = append(values, record)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	raw, err := json.Marshal(values)
	if err == nil && len(raw) > 32<<20 {
		err = fmt.Errorf("input capture chunk limit")
	}
	return raw, err
}

func metroInputGroups(records []MetroEventRecord) (map[string][]MetroEventRecord, error) {
	if len(records) > 64 {
		return nil, fmt.Errorf("input capture batch limit")
	}
	size := 0
	groups := map[string][]MetroEventRecord{}
	for _, r := range records {
		size += len(r.Payload)
		if len(r.Payload) > 256<<10 || !json.Valid(r.Payload) || r.ID == "" || r.At.IsZero() {
			return nil, fmt.Errorf("invalid input capture")
		}
		key := "metro-inputs:" + r.At.UTC().Truncate(5*time.Minute).Format(time.RFC3339)
		groups[key] = append(groups[key], r)
	}
	if size > 8<<20 {
		return nil, fmt.Errorf("input capture byte limit")
	}
	return groups, nil
}
