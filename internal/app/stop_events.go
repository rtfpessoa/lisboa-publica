package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"lisboapublica/internal/api"
	"time"
)

// An adapter may construct this only for an explicit, timestamped occurrence
// with a verified journey/visit association. Positions and ETA are not adapters.
// No current upstream source has certified actual-event semantics.
type reportedStopEvent struct {
	Journey    string               `json:"journey"`
	Sequence   int                  `json:"sequence"`
	Kind       string               `json:"kind"`
	Occurred   bool                 `json:"occurred"`
	Revision   int64                `json:"revision"`
	Correction bool                 `json:"correction"`
	Evidence   api.CallTimeEvidence `json:"evidence"`
}

func (e reportedStopEvent) valid(now time.Time) bool {
	identity := e.Occurred && e.Journey != "" && len(e.Journey) <= 1024 && e.Sequence >= 0
	return identity && e.validKind() && e.validEvidence(now)
}
func sameStopEvent(a, b reportedStopEvent) bool {
	return a.Evidence.At.Equal(b.Evidence.At) && a.Evidence.SourceUrl == b.Evidence.SourceUrl
}

// Persist before serving. Versions are append-only, so a pinned generation can
// still read the original occurrence after a correction or conflict is received.
func (s *Store) saveReportedStopEvents(ctx context.Context, events []reportedStopEvent) error {
	if len(events) == 0 {
		return nil
	}
	bytes, err := validateStopEventBatch(events, time.Now().UTC())
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err = s.reserveStorage(ctx, bytes, historyDatabaseBytes)
	if err == nil {
		err = s.transaction(ctx, func(tx pgx.Tx) error { return persistStopEventBatch(ctx, tx, events) })
	}
	return err
}
func validateStopEventBatch(events []reportedStopEvent, now time.Time) (int64, error) {
	if len(events) > maxReadResults {
		return 0, fmt.Errorf("stop event batch capacity")
	}
	seen := map[string]reportedStopEvent{}
	var bytes int64
	var err error
	for _, event := range events {
		bytesForEvent, e := validateStopEvent(event, now, seen)
		if e != nil {
			err = e
			break
		}
		bytes += bytesForEvent
	}
	return bytes, err
}
func validateStopEvent(event reportedStopEvent, now time.Time, seen map[string]reportedStopEvent) (int64, error) {
	key := fmt.Sprintf("%s|%d|%s", event.Journey, event.Sequence, event.Kind)
	old, ok := seen[key]
	var err error
	if ok && !sameStopEvent(old, event) {
		err = fmt.Errorf("conflicting event batch")
	}
	if !event.valid(now) {
		err = fmt.Errorf("unverified stop event")
	}
	seen[key] = event
	blob, e := json.Marshal(event)
	if e != nil {
		err = e
	}
	return int64(len(blob))*storageWriteOverhead + historyRecordOverhead, err
}
func persistStopEventBatch(ctx context.Context, tx pgx.Tx, events []reportedStopEvent) error {
	var generation int64
	err := tx.QueryRow(ctx, "UPDATE app_state SET generation=generation+1 WHERE id=1 RETURNING generation").Scan(&generation)
	if err == nil {
		for _, event := range events {
			err = persistStopEvent(ctx, tx, generation, event)
			if err != nil {
				break
			}
		}
	}
	return err
}
func persistStopEvent(ctx context.Context, tx pgx.Tx, generation int64, event reportedStopEvent) error {
	old, err := previousStopEvent(ctx, tx, event)
	if err != nil {
		return err
	}
	if old != nil && (sameStopEvent(*old, event) || event.Revision < old.Revision) {
		return nil
	}
	blob, err := json.Marshal(event)
	if err == nil {
		_, err = tx.Exec(ctx, "INSERT INTO stop_events(journey_id,stop_sequence,event_kind,generation,event_at,payload) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING", event.Journey, event.Sequence, event.Kind, generation, event.Evidence.At, blob)
	}
	return err
}
func previousStopEvent(ctx context.Context, tx pgx.Tx, event reportedStopEvent) (*reportedStopEvent, error) {
	var blob []byte
	err := tx.QueryRow(ctx, "SELECT payload FROM stop_events WHERE journey_id=$1 AND stop_sequence=$2 AND event_kind=$3 ORDER BY generation DESC LIMIT 1", event.Journey, event.Sequence, event.Kind).Scan(&blob)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	var old reportedStopEvent
	if err == nil {
		err = json.Unmarshal(blob, &old)
	}
	return &old, err
}

func (e reportedStopEvent) validKind() bool {
	return (e.Kind == "arrival" || e.Kind == "departure") && e.Revision >= 0
}
func (e reportedStopEvent) validEvidence(now time.Time) bool {
	return e.Evidence.SourceUrl != "" && !e.Evidence.At.IsZero() && !e.Evidence.At.After(now.Add(providerClockSkew)) && e.Evidence.SourceUpdatedAt != nil && e.Evidence.CollectedAt != nil
}
