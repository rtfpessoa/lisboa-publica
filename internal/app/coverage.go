package app

import (
	"context"
	"github.com/jackc/pgx/v5"
	"lisboapublica/internal/api"
	"sort"
)

// ListOperatorCoverage reports committed observations by operator for the requested window.
func (s *Server) ListOperatorCoverage(ctx context.Context, _ api.ListOperatorCoverageRequestObject) (api.ListOperatorCoverageResponseObject, error) {
	filter, generation, revision, err := s.coverageWindow(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.Store.operatorCoverage(ctx, filter, generation)
	if err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListOperatorCoverage200JSONResponse{Data: data, Page: page}, nil
}

func (s *Server) coverageWindow(ctx context.Context) (Filter, int64, string, error) {
	filter, err := s.filter(ctx, true)
	if err != nil {
		return filter, 0, "", err
	}
	generation, revision, err := s.snapshotRevision(ctx, &filter)
	return filter, generation, revision, err
}

func (s *Store) operatorCoverage(ctx context.Context, filter Filter, generation int64) ([]api.OperatorCoverage, error) {
	tx, err := s.readTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	left := append(args(filter, generation), filter.HourStart, filter.HourEnd, filter.Weekdays)
	rows, err := tx.Query(ctx, `SELECT operator_id,count(DISTINCT vehicle_id),count(DISTINCT vehicle_id) FILTER(WHERE position_kind='reported'),count(DISTINCT vehicle_id) FILTER(WHERE position_kind='estimated'),COALESCE(sum(speed_sample_count) FILTER(WHERE position_kind='reported' AND speed_kmh IS NOT NULL),0),count(DISTINCT vehicle_id) FILTER(WHERE NULLIF(payload->>'model','') IS NOT NULL),count(DISTINCT vehicle_id) FILTER(WHERE NULLIF(payload->>'license_plate','') IS NOT NULL),count(DISTINCT vehicle_id) FILTER(WHERE NULLIF(payload->>'typology','') IS NOT NULL)`+snapshotWhere+` AND extract(hour from observed_at AT TIME ZONE 'Europe/Lisbon') >= $6 AND extract(hour from observed_at AT TIME ZONE 'Europe/Lisbon') < $7 AND (NOT $8 OR extract(isodow from observed_at AT TIME ZONE 'Europe/Lisbon')<=5) GROUP BY operator_id`, left...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found, err := scanOperatorCoverage(rows)
	if err != nil {
		return nil, err
	}
	out := []api.OperatorCoverage{}
	for _, p := range providers {
		if filter.selected(p.ID) {
			v := found[p.ID]
			v.OperatorId = p.ID
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OperatorId < out[j].OperatorId })
	return out, nil
}

func scanOperatorCoverage(rows pgx.Rows) (map[string]api.OperatorCoverage, error) {
	found := map[string]api.OperatorCoverage{}
	for rows.Next() {
		var v api.OperatorCoverage
		if err := rows.Scan(&v.OperatorId, &v.Vehicles, &v.ReportedVehicles, &v.EstimatedVehicles, &v.SpeedSamples, &v.ModelVehicles, &v.PlateVehicles, &v.TypologyVehicles); err != nil {
			return nil, err
		}
		found[v.OperatorId] = v
	}
	return found, rows.Err()
}
