package patterns

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const maxMetroCheckpointBytes = 256 << 10
const maxMetroCheckpointBatchBytes = 8 << 20

// MetroJourneyCheckpoint stores a complete latest state independently of event
// proofs. SourceAt governs retention; reading or committing cannot renew it.
type MetroJourneyCheckpoint struct {
	Journey     string          `json:"journey"`
	Revision    uint64          `json:"revision"`
	SourceAt    time.Time       `json:"source_at"`
	Generation  string          `json:"generation"`
	CommittedAt time.Time       `json:"committed_at"`
	Payload     json.RawMessage `json:"payload"`
}

// CommitMetroJourneys commits all records through one manifest generation. A
// successor and its predecessor must be submitted together. Ordinary progress
// may be coalesced by the caller, but a stale revision never overwrites history.
func (s *Service) CommitMetroJourneys(ctx context.Context, records []MetroJourneyCheckpoint, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return "", fmt.Errorf("archive closed")
	}
	if err := validateMetroCheckpoints(records, now); err != nil {
		return "", err
	}
	return s.publishMetroCheckpoints(ctx, records, now)
}
func (s *Service) publishMetroCheckpoints(ctx context.Context, records []MetroJourneyCheckpoint, now time.Time) (string, error) {
	values := append([]MetroJourneyCheckpoint{}, records...)
	sort.Slice(values, func(i, j int) bool { return values[i].Journey < values[j].Journey })
	identity, err := json.Marshal(struct {
		At      time.Time
		Records []MetroJourneyCheckpoint
	}{now, values})
	if err != nil {
		return "", err
	}
	generation := digestBytes(identity)
	prepared, err := s.prepareMetroCheckpoints(ctx, values, generation, now)
	if err != nil {
		return "", err
	}

	err = s.publishTransaction(ctx, prepared, nil, now)
	if err != nil {
		generation = ""
	}
	return generation, err
}

func (s *Service) prepareMetroCheckpoints(ctx context.Context, values []MetroJourneyCheckpoint, generation string, now time.Time) ([]preparedBlock, error) {
	prepared := make([]preparedBlock, 0, len(values))
	for _, value := range values {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		block, err := s.prepareMetroCheckpoint(value, generation, now)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, block)
	}
	return prepared, nil
}
func (s *Service) prepareMetroCheckpoint(value MetroJourneyCheckpoint, generation string, now time.Time) (preparedBlock, error) {
	if err := s.validateMetroCheckpointPredecessor(value, now); err != nil {
		return preparedBlock{}, err
	}
	value.Generation, value.CommittedAt = generation, now.UTC()
	blob, err := s.encodeMetroCheckpoint(value)
	if err != nil {
		return preparedBlock{}, err
	}
	return preparedBlock{block: block{Key: metroCheckpointKey(value.Journey), Kind: "popup-checkpoint", Operator: "metro", Age: value.SourceAt.UTC(), Closed: value.SourceAt.Before(now.UTC().Truncate(time.Hour))}, blob: blob}, nil
}
func (s *Service) validateMetroCheckpointPredecessor(value MetroJourneyCheckpoint, now time.Time) error {
	prior, state, err := s.metroJourneyCheckpoint(value.Journey, now)
	if err != nil || state == "corrupt" {
		return fmt.Errorf("checkpoint predecessor unavailable: %w", err)
	}
	if state == "restored" && value.Revision <= prior.Revision {
		return fmt.Errorf("checkpoint revision did not advance")
	}
	return nil
}
func (s *Service) encodeMetroCheckpoint(value MetroJourneyCheckpoint) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxMetroCheckpointBytes {
		return nil, fmt.Errorf("checkpoint byte limit")
	}
	blob := s.encoder.EncodeAll(raw, nil)
	decoded, err := s.decoder.DecodeAll(blob, nil)
	if err != nil || digestBytes(decoded) != digestBytes(raw) {
		return nil, fmt.Errorf("checkpoint codec verification")
	}
	return blob, nil
}

func validateMetroCheckpoints(records []MetroJourneyCheckpoint, now time.Time) error {
	if len(records) == 0 || len(records) > 1024 {
		return fmt.Errorf("checkpoint batch count limit")
	}
	return validateMetroCheckpointRecords(records, now)
}
func validateMetroCheckpointRecords(records []MetroJourneyCheckpoint, now time.Time) error {
	seen, size := map[string]bool{}, 0
	for _, v := range records {
		if !validMetroCheckpointRecord(v, now) || seen[v.Journey] {
			return fmt.Errorf("invalid checkpoint identity, clock or payload")
		}
		seen[v.Journey] = true
		raw, err := json.Marshal(v)
		size += len(raw)
		if err != nil || len(raw) > maxMetroCheckpointBytes || size > maxMetroCheckpointBatchBytes {
			return fmt.Errorf("checkpoint byte limit")
		}
	}
	return nil
}
func validMetroCheckpointRecord(v MetroJourneyCheckpoint, now time.Time) bool {
	identity := v.Journey != "" && len(v.Journey) <= 128 && v.Revision > 0
	clock := validMetroCheckpointClock(v.SourceAt, now)
	return identity && clock && json.Valid(v.Payload)
}

func validMetroCheckpointClock(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now.Add(5*time.Minute)) && !at.Before(now.AddDate(0, 0, -7))
}

func metroCheckpointKey(journey string) string { return "metro-popup-checkpoint:" + digest(journey) }

// MetroJourneyCheckpoint looks up one latest verified record by its manifest
// key. It never scans event payloads, extends retention or restores continuity.
// Missing records are unavailable: absence alone cannot distinguish eviction
// from a journey that was never committed.
func (s *Service) MetroJourneyCheckpoint(ctx context.Context, journey string, now time.Time) (MetroJourneyCheckpoint, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return MetroJourneyCheckpoint{}, "unavailable", err
	}
	if s.lock == nil {
		return MetroJourneyCheckpoint{}, "unavailable", fmt.Errorf("archive closed")
	}
	return s.metroJourneyCheckpoint(journey, now)
}

func (s *Service) metroJourneyCheckpoint(journey string, now time.Time) (MetroJourneyCheckpoint, string, error) {
	for _, b := range s.index.Blocks {
		if b.Kind != "popup-checkpoint" || b.Key != metroCheckpointKey(journey) {
			continue
		}
		if b.Age.Before(now.AddDate(0, 0, -7)) {
			return MetroJourneyCheckpoint{}, "expired", nil
		}
		return s.decodeMetroCheckpoint(b, journey)
	}

	return MetroJourneyCheckpoint{}, "unavailable", nil
}

func (s *Service) decodeMetroCheckpoint(b block, journey string) (MetroJourneyCheckpoint, string, error) {
	blob, err := s.readBlock(b)
	if err != nil {
		return MetroJourneyCheckpoint{}, "corrupt", err
	}
	raw, err := s.decoder.DecodeAll(blob, nil)
	var value MetroJourneyCheckpoint
	decoded := err == nil && len(raw) <= maxMetroCheckpointBytes && json.Unmarshal(raw, &value) == nil
	if !decoded || !verifiedMetroCheckpoint(value, b, journey) {
		return MetroJourneyCheckpoint{}, "corrupt", fmt.Errorf("invalid checkpoint")
	}
	return value, "restored", nil
}
func verifiedMetroCheckpoint(value MetroJourneyCheckpoint, b block, journey string) bool {
	identity := value.Journey == journey && value.Revision > 0 && value.Generation != ""
	clocks := !value.CommittedAt.IsZero() && value.SourceAt.Equal(b.Age)
	return identity && clocks && json.Valid(value.Payload)
}
