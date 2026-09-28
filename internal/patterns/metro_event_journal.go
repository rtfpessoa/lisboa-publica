package patterns

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// MetroEventRecord retains model/publication evidence independently of actual events.
// Payloads contain a summary and its contributing original samples, never credentials.
type MetroEventRecord struct {
	ID      string          `json:"id"`
	Journey string          `json:"journey"`
	At      time.Time       `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

// RecordMetroEvents shares archive ownership, manifest checksums, TTL and allocated-byte FIFO.
func (s *Service) RecordMetroEvents(records []MetroEventRecord, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return fmt.Errorf("archive closed")
	}
	groups, err := groupMetroEvents(records, now)
	if err != nil {
		return err
	}
	for key, values := range groups {
		if err = s.publishMetroEventHour(key, values, now); err != nil {
			break
		}
	}
	return err
}
func groupMetroEvents(records []MetroEventRecord, now time.Time) (map[string][]MetroEventRecord, error) {
	if len(records) > 1024 {
		return nil, fmt.Errorf("event batch limit")
	}
	groups := map[string][]MetroEventRecord{}
	size := 0
	for _, r := range records {
		size += len(r.Payload)
		if err := validateMetroEvent(r, size); err != nil {
			return nil, err
		}
		if !r.At.Before(now.AddDate(0, 0, -7)) {
			key := "metro-popup-events:" + r.At.UTC().Truncate(time.Hour).Format(time.RFC3339)
			groups[key] = append(groups[key], r)
		}
	}
	return groups, nil
}
func validateMetroEvent(r MetroEventRecord, size int) error {
	valid := len(r.Payload) <= 64<<10 && json.Valid(r.Payload)
	identity := r.ID != "" && r.Journey != "" && !r.At.IsZero()
	if !valid || !identity {
		return fmt.Errorf("invalid event proof")
	}
	if size > 8<<20 {
		return fmt.Errorf("event batch bytes")
	}
	return nil
}
func (s *Service) readMetroEventBlock(b block) ([]MetroEventRecord, error) {
	blob, err := s.readBlock(b)
	if err != nil {
		return nil, err
	}
	raw, err := s.decoder.DecodeAll(blob, nil)
	var values []MetroEventRecord
	if err == nil && json.Unmarshal(raw, &values) != nil {
		err = fmt.Errorf("invalid event journal")
	}
	return values, err
}
func (s *Service) retainedMetroEvents(key string) (map[string]MetroEventRecord, error) {
	retained := map[string]MetroEventRecord{}
	for _, b := range s.index.Blocks {
		if b.Key != key {
			continue
		}
		values, err := s.readMetroEventBlock(b)
		if err != nil {
			return nil, err
		}
		for _, v := range values {
			retained[v.ID] = v
		}
	}
	return retained, nil
}
func (s *Service) publishMetroEventHour(key string, values []MetroEventRecord, now time.Time) error {
	retained, err := s.retainedMetroEvents(key)
	if err != nil {
		return err
	}
	for _, v := range values {
		retained[v.ID] = v
	}
	blob, err := s.encodeMetroEvents(retained)
	if err != nil {
		return err
	}
	at := values[0].At.UTC().Truncate(time.Hour)
	return s.publish(block{Key: key, Kind: "popup-events", Operator: "metro", Age: at, Closed: at.Before(now.UTC().Truncate(time.Hour))}, blob, now)
}
func (s *Service) encodeMetroEvents(retained map[string]MetroEventRecord) ([]byte, error) {
	all := make([]MetroEventRecord, 0, len(retained))
	for _, v := range retained {
		all = append(all, v)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	raw, err := marshalMetroEventHour(all)
	if err != nil {
		return nil, err
	}
	blob := s.encoder.EncodeAll(raw, nil)
	decoded, err := s.decoder.DecodeAll(blob, nil)
	if err != nil || digestBytes(decoded) != digestBytes(raw) {
		return nil, fmt.Errorf("event codec verification")
	}
	return blob, nil
}
func marshalMetroEventHour(all []MetroEventRecord) ([]byte, error) {
	raw, err := json.Marshal(all)
	if err == nil && len(raw) > maxDetailHourBytes {
		err = fmt.Errorf("event hour limit")
	}
	return raw, err
}

// MetroJourneyEvents restores verified committed history only. A bounded read never restores live continuity.
func (s *Service) MetroJourneyEvents(ctx context.Context, journey string, now time.Time) ([]MetroEventRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []MetroEventRecord{}
	for _, b := range s.index.Blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if b.Kind != "popup-events" || b.Age.Before(now.AddDate(0, 0, -7)) {
			continue
		}
		var err error
		out, err = s.appendMetroJourneyEvents(out, b, journey)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Service) appendMetroJourneyEvents(out []MetroEventRecord, b block, journey string) ([]MetroEventRecord, error) {
	values, err := s.readMetroEventBlock(b)
	if err != nil {
		return nil, err
	}
	for _, v := range values {
		if v.Journey == journey {
			out = append(out, v)
			if len(out) > 1024 {
				return nil, fmt.Errorf("event read limit")
			}
		}
	}
	return out, nil
}
