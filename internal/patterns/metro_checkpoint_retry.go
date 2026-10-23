package patterns

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// MetroCheckpointAttempt is an owner-issued retry ticket; callers cannot alter it.
type MetroCheckpointAttempt struct {
	records      []checkpointRecordIdentity
	predecessors []checkpointPredecessor
}

type checkpointRecordIdentity struct {
	journey, payloadHash, generation string
	revision                         uint64
	sourceAt, committedAt            time.Time
}

var errCheckpointPredecessor = errors.New("checkpoint predecessor unverifiable or changed")

type checkpointPredecessor struct {
	journey, generation, payloadHash string
	revision                         uint64
	sourceAt                         time.Time
	present                          bool
}

// MetroCheckpointCommit distinguishes success from a proven pre-rename retry.
type MetroCheckpointCommit struct {
	Generation string
	Retry      *MetroCheckpointAttempt
	Retryable  bool
}

func (s *Service) commitMetroJourneysGuarded(ctx context.Context, records []MetroJourneyCheckpoint, now time.Time, retry *MetroCheckpointAttempt) (MetroCheckpointCommit, error) {
	var result MetroCheckpointCommit
	if s.lock == nil || s.manifestUncertain || s.checkpointUncertain {
		return result, fmt.Errorf("checkpoint archive unavailable; reopen required")
	}
	if err := validateMetroCheckpoints(records, now); err != nil {
		return s.invalidCheckpointBatch(records, retry), err
	}
	attempt, err := s.checkpointAttempt(records, now, retry)
	if err != nil {
		return result, err
	}
	generation, preRename, err := s.publishGuardedCheckpoints(ctx, records, now, attempt)
	if err == nil {
		s.checkpointRetry = nil
		result.Generation = generation
		return result, nil
	}
	return s.checkpointPublicationFailure(attempt, preRename, err)
}

func (s *Service) invalidCheckpointBatch(records []MetroJourneyCheckpoint, retry *MetroCheckpointAttempt) MetroCheckpointCommit {
	var result MetroCheckpointCommit
	if retry != nil && retry == s.checkpointRetry && sameCheckpointIdentities(checkpointIdentities(records), retry.records) {
		result.Retry, result.Retryable = retry, true
	}
	return result
}

func (s *Service) checkpointPublicationFailure(attempt *MetroCheckpointAttempt, preRename bool, err error) (MetroCheckpointCommit, error) {
	var result MetroCheckpointCommit
	if errors.Is(err, errCheckpointPredecessor) {
		s.checkpointRetry = attempt
	} else if preRename {
		s.checkpointRetry = attempt
		result.Retry, result.Retryable = attempt, true
	} else {
		s.checkpointUncertain = true
	}
	return result, s.markPaused(err)
}

func checkpointIdentities(records []MetroJourneyCheckpoint) []checkpointRecordIdentity {
	result := make([]checkpointRecordIdentity, 0, len(records))
	for _, v := range records {
		result = append(result, checkpointRecordIdentity{journey: v.Journey, payloadHash: digestBytes(v.Payload), generation: v.Generation, revision: v.Revision, sourceAt: v.SourceAt, committedAt: v.CommittedAt})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].journey < result[j].journey })
	return result
}

func sameCheckpointIdentities(a, b []checkpointRecordIdentity) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].journey != b[i].journey || a[i].payloadHash != b[i].payloadHash || a[i].generation != b[i].generation || a[i].revision != b[i].revision || !a[i].sourceAt.Equal(b[i].sourceAt) || !a[i].committedAt.Equal(b[i].committedAt) {
			return false
		}
	}
	return true
}

func (s *Service) checkpointAttempt(records []MetroJourneyCheckpoint, now time.Time, retry *MetroCheckpointAttempt) (*MetroCheckpointAttempt, error) {
	identities := checkpointIdentities(records)
	if s.checkpointRetry != nil {
		if retry != s.checkpointRetry || !sameCheckpointIdentities(identities, retry.records) {
			return nil, fmt.Errorf("checkpoint retry ownership or payload changed")
		}
		return retry, nil
	}
	if retry != nil {
		return nil, fmt.Errorf("stale checkpoint retry ticket")
	}
	attempt := &MetroCheckpointAttempt{records: identities}
	journeys := make([]string, len(identities))
	for i, identity := range identities {
		journeys[i] = identity.journey
	}
	predecessors, err := s.checkpointPredecessors(journeys, now)
	if err != nil {
		return nil, err
	}
	for i, identity := range identities {
		prior := predecessors[i]
		if prior.present && (identity.revision <= prior.revision || identity.sourceAt.Before(prior.sourceAt)) {
			return nil, fmt.Errorf("checkpoint revision or source did not advance")
		}
	}
	attempt.predecessors = predecessors
	return attempt, nil
}

// Collect matching identities in one manifest pass, then freshly verify each file.
func (s *Service) checkpointPredecessors(journeys []string, now time.Time) ([]checkpointPredecessor, error) {
	found, err := s.checkpointPredecessorBlocks(journeys)
	if err != nil {
		return nil, err
	}
	result := make([]checkpointPredecessor, 0, len(journeys))
	for _, journey := range journeys {
		prior := checkpointPredecessor{journey: journey}
		if b, exists := found[journey]; exists {
			prior, err = s.readCheckpointPredecessor(b, journey, now)
			if err != nil {
				return nil, err
			}
		}
		result = append(result, prior)
	}
	return result, nil
}

func (s *Service) checkpointPredecessorBlocks(journeys []string) (map[string]block, error) {
	wanted := make(map[string]string, len(journeys))
	for _, journey := range journeys {
		wanted[metroCheckpointKey(journey)] = journey
	}
	found := make(map[string]block, len(journeys))
	for _, b := range s.index.Blocks {
		journey, wantedKey := wanted[b.Key]
		if !wantedKey {
			continue
		}
		if _, duplicate := found[journey]; duplicate || b.Kind != "popup-checkpoint" || b.Operator != "metro" {
			return nil, fmt.Errorf("ambiguous checkpoint predecessor")
		}
		found[journey] = b
	}
	return found, nil
}

func (s *Service) readCheckpointPredecessor(b block, journey string, now time.Time) (checkpointPredecessor, error) {
	prior := checkpointPredecessor{journey: journey}
	if b.Age.Before(now.AddDate(0, 0, -7)) {
		return prior, fmt.Errorf("checkpoint predecessor expired")
	}
	value, state, err := s.decodeMetroCheckpoint(b, journey)
	if err != nil || state != "restored" {
		return prior, fmt.Errorf("checkpoint predecessor unverifiable")
	}
	prior.present, prior.revision, prior.sourceAt = true, value.Revision, value.SourceAt
	prior.generation, prior.payloadHash = value.Generation, digestBytes(value.Payload)
	return prior, nil
}

func (s *Service) verifyCheckpointPredecessors(attempt *MetroCheckpointAttempt, now time.Time) error {
	journeys := make([]string, len(attempt.predecessors))
	for i, expected := range attempt.predecessors {
		journeys[i] = expected.journey
	}
	actual, err := s.checkpointPredecessors(journeys, now)
	if err != nil {
		return fmt.Errorf("%w: %v", errCheckpointPredecessor, err)
	}
	for i, expected := range attempt.predecessors {
		if actual[i].present != expected.present || actual[i].revision != expected.revision || !actual[i].sourceAt.Equal(expected.sourceAt) || actual[i].generation != expected.generation || actual[i].payloadHash != expected.payloadHash {
			return errCheckpointPredecessor
		}
	}
	return nil
}

// AbandonMetroCheckpointAttempt releases a proven pre-rename attempt after its
// caller has explicitly expired or abandoned the never-committed pending work.
func (s *Service) AbandonMetroCheckpointAttempt(attempt *MetroCheckpointAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if attempt == nil || attempt != s.checkpointRetry || s.manifestUncertain || s.checkpointUncertain {
		return fmt.Errorf("checkpoint retry cannot be abandoned")
	}
	s.checkpointRetry = nil
	return nil
}
