package app

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *Server) journeyEvents(ctx context.Context, journey string, generation int64, asOf time.Time, sequenceRange ...int) ([]reportedStopEvent, error) {
	out := []reportedStopEvent{}
	if s.Store == nil || s.Store.DB == nil {
		return out, nil
	}
	query := "SELECT payload FROM stop_events WHERE journey_id=$1 AND generation<=$2 AND event_at>=$3"
	args := []any{journey, generation, asOf.AddDate(0, 0, -s.Store.retentionDays())}
	if len(sequenceRange) == 2 {
		query += " AND stop_sequence >= $4 AND stop_sequence <= $5"
		args = append(args, sequenceRange[0], sequenceRange[1])
	}
	query += " ORDER BY generation,stop_sequence,event_kind LIMIT 100001"
	rows, err := s.Store.DB.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanStopEvents(rows)
}
func scanStopEvents(rows pgx.Rows) ([]reportedStopEvent, error) {
	out := []reportedStopEvent{}
	var err error
	for rows.Next() {
		var blob []byte
		err = rows.Scan(&blob)
		var event reportedStopEvent
		if err == nil {
			err = json.Unmarshal(blob, &event)
		}
		if err != nil {
			break
		}
		out = append(out, event)
		if len(out) > maxReadResults {
			err = readResultLimit()
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	return out, err
}
