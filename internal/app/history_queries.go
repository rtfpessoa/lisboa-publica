package app

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"lisboapublica/internal/api"
)

// consumeReadRows bounds all scanned groups, including groups excluded by UI search.
// ForEachRow closes rows on scan, callback and server errors; the caller owns its transaction.
func consumeReadRows(ctx context.Context, rows pgx.Rows, targets []any, visit func()) error {
	scanned := 0
	_, err := pgx.ForEachRow(rows, targets, func() error {
		scanned++
		if scanned > maxReadResults {
			return readResultLimit()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visit()
		return nil
	})
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (s *Store) readMetrics(ctx context.Context, f Filter, generation int64) (api.Metrics, error) {
	metrics := api.Metrics{From: f.From, To: f.To, UnavailableFields: []string{"Velocidade comercial exata", "Viagens concluídas", "Frequência operacional", "Inventário completo da frota"}}
	tx, err := s.readTransaction(ctx)
	if err != nil {
		return metrics, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, `SELECT sum(speed_kmh*speed_sample_count::DOUBLE PRECISION)/NULLIF(sum(speed_sample_count::DOUBLE PRECISION) FILTER(WHERE speed_kmh IS NOT NULL),0),sum(distance_km),NULLIF(count(DISTINCT trip_id) FILTER (WHERE position_kind='reported' AND trip_id IS NOT NULL),0)`+snapshotWhere, args(f, generation)...).Scan(&metrics.SpeedKmh, &metrics.DistanceKm, &metrics.DetectedTrips); err != nil {
		return metrics, err
	}
	err = tx.QueryRow(ctx, `SELECT min(observed_at) FROM snapshots WHERE ($1::TEXT[] IS NULL OR operator_id=ANY($1)) AND generation <= $2`, f.Operators, generation).Scan(&metrics.FirstSnapshot)
	return metrics, err
}

func (s *Store) readHistory(ctx context.Context, f Filter, generation int64) ([]api.HistoryPoint, error) {
	tx, err := s.readTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT floor(extract(epoch from observed_at)/300)*300 AS bucket,count(DISTINCT vehicle_id) FILTER(WHERE position_kind='reported'),count(DISTINCT vehicle_id) FILTER(WHERE position_kind='estimated'),sum(speed_kmh*speed_sample_count::DOUBLE PRECISION)/NULLIF(sum(speed_sample_count::DOUBLE PRECISION) FILTER(WHERE speed_kmh IS NOT NULL),0),sum(distance_km)`+snapshotWhere+` GROUP BY bucket ORDER BY bucket`+fmt.Sprintf(" LIMIT %d", maxReadResults+1), args(f, generation)...)
	if err != nil {
		return nil, err
	}
	out := []api.HistoryPoint{}
	var v api.HistoryPoint
	var epoch float64
	err = consumeReadRows(ctx, rows, []any{&epoch, &v.ReportedVehicles, &v.EstimatedVehicles, &v.SpeedKmh, &v.DistanceKm}, func() {
		v.Bucket = time.Unix(int64(epoch), 0).UTC()
		out = append(out, v)
	})
	return out, err
}

func (s *Store) readFleet(ctx context.Context, f Filter, generation int64) ([]api.FleetVehicle, error) {
	tx, err := s.readTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT vehicle_id,operator_id,max(payload->>'source_id'),max(payload->>'model'),max(payload->>'license_plate'),max(payload->>'typology'),max(payload->>'propulsion'),max(position_kind),min(COALESCE(first_observed_at,observed_at)),max(observed_at),sum(distance_km),NULLIF(count(DISTINCT trip_id) FILTER (WHERE position_kind='reported' AND trip_id IS NOT NULL),0),array_agg(DISTINCT route_id) FILTER (WHERE route_id IS NOT NULL)`+snapshotWhere+` GROUP BY operator_id,vehicle_id ORDER BY vehicle_id`+fmt.Sprintf(" LIMIT %d", maxReadResults+1), args(f, generation)...)
	if err != nil {
		return nil, err
	}
	out := []api.FleetVehicle{}
	var v api.FleetVehicle
	err = consumeReadRows(ctx, rows, []any{&v.Id, &v.OperatorId, &v.SourceId, &v.Model, &v.LicensePlate, &v.Typology, &v.Propulsion, &v.PositionKind, &v.FirstSeen, &v.LastSeen, &v.DistanceKm, &v.DetectedTrips, &v.RouteIds}, func() {
		item := normalizeFleetVehicle(v)
		if fleetMatches(item, f.Q) {
			out = append(out, item)
		}
	})
	return out, err
}

func (s *Store) readTraffic(ctx context.Context, f Filter, generation int64) ([]api.TrafficPoint, error) {
	tx, err := s.readTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	parameters := append(args(f, generation), f.HourStart, f.HourEnd, f.Weekdays)
	rows, err := tx.Query(ctx, `SELECT operator_id,floor(lat*1000)/1000 AS y,floor(lon*1000)/1000 AS x,sum(speed_kmh*speed_sample_count::DOUBLE PRECISION)/NULLIF(sum(speed_sample_count::DOUBLE PRECISION) FILTER(WHERE speed_kmh IS NOT NULL),0),sum(speed_sample_count)`+snapshotWhere+` AND position_kind='reported' AND speed_kmh IS NOT NULL AND extract(hour from observed_at AT TIME ZONE 'Europe/Lisbon') >= $6 AND extract(hour from observed_at AT TIME ZONE 'Europe/Lisbon') < $7 AND (NOT $8 OR extract(isodow from observed_at AT TIME ZONE 'Europe/Lisbon')<=5) GROUP BY operator_id,y,x ORDER BY operator_id,y,x`+fmt.Sprintf(" LIMIT %d", maxReadResults+1), parameters...)
	if err != nil {
		return nil, err
	}
	out := []api.TrafficPoint{}
	var v api.TrafficPoint
	err = consumeReadRows(ctx, rows, []any{&v.OperatorId, &v.Lat, &v.Lon, &v.SpeedKmh, &v.Observations}, func() {
		v.Id = fmt.Sprintf("%s:%.3f:%.3f", v.OperatorId, v.Lat, v.Lon)
		out = append(out, v)
	})
	return out, err
}

func (s *Store) readRankings(ctx context.Context, f Filter, generation int64) ([]api.Ranking, error) {
	tx, err := s.readTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT operator_id,route_id,count(DISTINCT vehicle_id) FILTER (WHERE position_kind='reported'),sum(speed_kmh*speed_sample_count::DOUBLE PRECISION)/NULLIF(sum(speed_sample_count::DOUBLE PRECISION) FILTER(WHERE speed_kmh IS NOT NULL),0),sum(distance_km),NULLIF(count(DISTINCT trip_id) FILTER (WHERE position_kind='reported' AND trip_id IS NOT NULL),0)`+snapshotWhere+` AND route_id IS NOT NULL GROUP BY operator_id,route_id ORDER BY route_id`+fmt.Sprintf(" LIMIT %d", maxReadResults+1), args(f, generation)...)
	if err != nil {
		return nil, err
	}
	out := []api.Ranking{}
	var v api.Ranking
	err = consumeReadRows(ctx, rows, []any{&v.OperatorId, &v.RouteId, &v.ReportedVehicles, &v.SpeedKmh, &v.DistanceKm, &v.DetectedTrips}, func() {
		v.Id = v.RouteId
		out = append(out, v)
	})
	return out, err
}
