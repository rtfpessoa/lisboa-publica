package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"lisboapublica/internal/api"
)

const snapshotWhere = ` FROM snapshots WHERE observed_at >= $1 AND observed_at < $2 AND ($3::TEXT[] IS NULL OR operator_id=ANY($3)) AND ($4='' OR route_id=$4) AND generation <= $5`

func (s *Server) snapshotRevision(ctx context.Context, f *Filter) (int64, string, error) {
	latest, err := s.Store.generation(ctx)
	if err != nil {
		return 0, "", err
	}
	generation := latest
	if f.Revision != "" {
		parts := strings.Split(f.Revision, ":")
		if len(parts) != 4 || parts[0] != "s" {
			return 0, "", fail(http.StatusBadRequest, "revision", "Revisão histórica inválida.")
		}
		generation, err = strconv.ParseInt(parts[1], 10, numericBitSize)
		from, ef := strconv.ParseInt(parts[2], 10, numericBitSize)
		to, et := strconv.ParseInt(parts[3], 10, numericBitSize)
		if err != nil || ef != nil || et != nil || generation < 0 || generation > latest {
			return 0, "", fail(http.StatusBadRequest, "revision", "Revisão histórica inválida.")
		}
		start, end := time.Unix(0, from).UTC(), time.Unix(0, to).UTC()
		query := request(ctx).URL.Query()
		if (query.Get("from") != "" && !f.From.Equal(start)) || (query.Get("to") != "" && !f.To.Equal(end)) {
			return 0, "", fail(http.StatusBadRequest, "revision", "O intervalo mudou entre páginas.")
		}
		f.From, f.To = start, end
		if !end.After(start) || start.Before(time.Now().AddDate(0, 0, -s.Store.retentionDays())) || end.After(time.Now().Add(time.Minute)) {
			return 0, "", fail(http.StatusGone, "history_expired", "Intervalo histórico expirado.")
		}
	}
	return generation, fmt.Sprintf("s:%d:%d:%d", generation, f.From.UnixNano(), f.To.UnixNano()), nil
}
func args(f Filter, n int64) []any { return []any{f.From, f.To, f.Operators, f.Route, n} }

// GetMetrics aggregates retained reported observations with honest nullable metrics.
func (s *Server) GetMetrics(ctx context.Context, _ api.GetMetricsRequestObject) (api.GetMetricsResponseObject, error) {
	filter, err := s.filter(ctx, true)
	if err != nil {
		return nil, err
	}
	generation, revision, err := s.snapshotRevision(ctx, &filter)
	if err != nil {
		return nil, err
	}
	metrics := api.Metrics{Revision: revision, From: filter.From, To: filter.To, UnavailableFields: []string{"Velocidade comercial exata", "Viagens concluídas", "Frequência operacional", "Inventário completo da frota"}}
	var trips *int
	err = s.Store.DB.QueryRow(ctx, `SELECT sum(speed_kmh*speed_sample_count::DOUBLE PRECISION)/NULLIF(sum(speed_sample_count::DOUBLE PRECISION) FILTER(WHERE speed_kmh IS NOT NULL),0),sum(distance_km),NULLIF(count(DISTINCT trip_id) FILTER (WHERE position_kind='reported' AND trip_id IS NOT NULL),0)`+snapshotWhere, args(filter, generation)...).Scan(&metrics.SpeedKmh, &metrics.DistanceKm, &trips)
	if err != nil {
		return nil, err
	}
	metrics.DetectedTrips = trips
	err = s.Store.DB.QueryRow(ctx, `SELECT min(observed_at) FROM snapshots WHERE ($1::TEXT[] IS NULL OR operator_id=ANY($1)) AND generation <= $2`, filter.Operators, generation).Scan(&metrics.FirstSnapshot)
	if err != nil {
		return nil, err
	}
	state, _ := s.Cache.state("")
	reported, estimated := 0, 0
	available := false
	for p, data := range state.Live {
		if !filter.selected(p) {
			continue
		}
		if time.Since(data.Collected) <= 90*time.Second {
			available = true
		}
		for _, v := range data.Vehicles {
			if filter.Route != "" && (v.RouteId == nil || *v.RouteId != filter.Route) {
				continue
			}
			if time.Since(v.ObservedAt) > 180*time.Second {
				continue
			}
			if v.PositionKind == "estimated" {
				estimated++
			} else {
				reported++
			}
		}
	}
	if available {
		metrics.ReportedVehicles = ptr(reported)
		metrics.EstimatedVehicles = ptr(estimated)
	}
	return api.GetMetrics200JSONResponse(metrics), nil
}

// ListHistory returns retained five-minute speed and volume samples.
func (s *Server) ListHistory(ctx context.Context, _ api.ListHistoryRequestObject) (api.ListHistoryResponseObject, error) {
	filter, err := s.filter(ctx, true)
	if err != nil {
		return nil, err
	}
	generation, revision, err := s.snapshotRevision(ctx, &filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.DB.Query(ctx, `SELECT floor(extract(epoch from observed_at)/300)*300 AS bucket,count(DISTINCT vehicle_id) FILTER(WHERE position_kind='reported'),count(DISTINCT vehicle_id) FILTER(WHERE position_kind='estimated'),sum(speed_kmh*speed_sample_count::DOUBLE PRECISION)/NULLIF(sum(speed_sample_count::DOUBLE PRECISION) FILTER(WHERE speed_kmh IS NOT NULL),0),sum(distance_km)`+snapshotWhere+` GROUP BY bucket ORDER BY bucket`, args(filter, generation)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.HistoryPoint{}
	for rows.Next() {
		var v api.HistoryPoint
		var epoch float64
		if err = rows.Scan(&epoch, &v.ReportedVehicles, &v.EstimatedVehicles, &v.SpeedKmh, &v.DistanceKm); err != nil {
			return nil, err
		}
		v.Bucket = time.Unix(int64(epoch), 0).UTC()
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListHistory200JSONResponse{Data: data, Page: page}, nil
}

// ListFleet lists vehicles detected in the selected observation window.
func (s *Server) ListFleet(ctx context.Context, _ api.ListFleetRequestObject) (api.ListFleetResponseObject, error) {
	filter, err := s.filter(ctx, true)
	if err != nil {
		return nil, err
	}
	generation, revision, err := s.snapshotRevision(ctx, &filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.DB.Query(ctx, `SELECT vehicle_id,operator_id,max(payload->>'source_id'),max(payload->>'model'),max(payload->>'license_plate'),max(payload->>'typology'),max(payload->>'propulsion'),max(position_kind),min(COALESCE(first_observed_at,observed_at)),max(observed_at),sum(distance_km),NULLIF(count(DISTINCT trip_id) FILTER (WHERE position_kind='reported' AND trip_id IS NOT NULL),0),array_agg(DISTINCT route_id) FILTER (WHERE route_id IS NOT NULL)`+snapshotWhere+` GROUP BY operator_id,vehicle_id ORDER BY vehicle_id`, args(filter, generation)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.FleetVehicle{}
	for rows.Next() {
		var v api.FleetVehicle
		if err = rows.Scan(&v.Id, &v.OperatorId, &v.SourceId, &v.Model, &v.LicensePlate, &v.Typology, &v.Propulsion, &v.PositionKind, &v.FirstSeen, &v.LastSeen, &v.DistanceKm, &v.DetectedTrips, &v.RouteIds); err != nil {
			return nil, err
		}
		v = normalizeFleetVehicle(v)
		if !fleetMatches(v, filter.Q) {
			continue
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return fleetLess(out[i], out[j], filter.Sort) })
	page, data := paginate(out, filter, revision)
	return api.ListFleet200JSONResponse{Data: data, Page: page}, nil
}

// ListTraffic aggregates observed transit speeds by spatial cell.
func (s *Server) ListTraffic(ctx context.Context, _ api.ListTrafficRequestObject) (api.ListTrafficResponseObject, error) {
	filter, err := s.filter(ctx, true)
	if err != nil {
		return nil, err
	}
	generation, revision, err := s.snapshotRevision(ctx, &filter)
	if err != nil {
		return nil, err
	}
	left := args(filter, generation)
	left = append(left, filter.HourStart, filter.HourEnd, filter.Weekdays)
	rows, err := s.Store.DB.Query(ctx, `SELECT operator_id,floor(lat*1000)/1000 AS y,floor(lon*1000)/1000 AS x,sum(speed_kmh*speed_sample_count::DOUBLE PRECISION)/NULLIF(sum(speed_sample_count::DOUBLE PRECISION) FILTER(WHERE speed_kmh IS NOT NULL),0),sum(speed_sample_count)`+snapshotWhere+` AND position_kind='reported' AND speed_kmh IS NOT NULL AND extract(hour from observed_at AT TIME ZONE 'Europe/Lisbon') >= $6 AND extract(hour from observed_at AT TIME ZONE 'Europe/Lisbon') < $7 AND (NOT $8 OR extract(isodow from observed_at AT TIME ZONE 'Europe/Lisbon')<=5) GROUP BY operator_id,y,x ORDER BY operator_id,y,x`, left...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.TrafficPoint{}
	for rows.Next() {
		var v api.TrafficPoint
		if err = rows.Scan(&v.OperatorId, &v.Lat, &v.Lon, &v.SpeedKmh, &v.Observations); err != nil {
			return nil, err
		}
		v.Id = fmt.Sprintf("%s:%.3f:%.3f", v.OperatorId, v.Lat, v.Lon)
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListTraffic200JSONResponse{Data: data, Page: page}, nil
}

// ListRankings ranks route observations without inferring completed trips.
func (s *Server) ListRankings(ctx context.Context, _ api.ListRankingsRequestObject) (api.ListRankingsResponseObject, error) {
	filter, err := s.filter(ctx, true)
	if err != nil {
		return nil, err
	}
	generation, revision, err := s.snapshotRevision(ctx, &filter)
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.DB.Query(ctx, `SELECT operator_id,route_id,count(DISTINCT vehicle_id) FILTER (WHERE position_kind='reported'),sum(speed_kmh*speed_sample_count::DOUBLE PRECISION)/NULLIF(sum(speed_sample_count::DOUBLE PRECISION) FILTER(WHERE speed_kmh IS NOT NULL),0),sum(distance_km),NULLIF(count(DISTINCT trip_id) FILTER (WHERE position_kind='reported' AND trip_id IS NOT NULL),0)`+snapshotWhere+` AND route_id IS NOT NULL GROUP BY operator_id,route_id ORDER BY route_id`, args(filter, generation)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Ranking{}
	state, _ := s.Cache.state("")
	names := map[string]string{}
	for _, d := range state.Static {
		for _, r := range d.Routes {
			names[r.Id] = r.ShortName + " · " + r.LongName
		}
	}
	for rows.Next() {
		var v api.Ranking
		if err = rows.Scan(&v.OperatorId, &v.RouteId, &v.ReportedVehicles, &v.SpeedKmh, &v.DistanceKm, &v.DetectedTrips); err != nil {
			return nil, err
		}
		v.Id = v.RouteId
		v.RouteName = names[v.RouteId]
		if v.RouteName == "" {
			v.RouteName = v.RouteId
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return rankingLess(out[i], out[j], filter.Sort) })
	page, data := paginate(out, filter, revision)
	return api.ListRankings200JSONResponse{Data: data, Page: page}, nil
}

func rankingLess(left, right api.Ranking, order string) bool {
	switch order {
	case "distance":
		if (left.DistanceKm == nil) != (right.DistanceKm == nil) {
			return left.DistanceKm != nil
		}
		if left.DistanceKm != nil && right.DistanceKm != nil && *left.DistanceKm != *right.DistanceKm {
			return *left.DistanceKm > *right.DistanceKm
		}
	case "trips":
		if (left.DetectedTrips == nil) != (right.DetectedTrips == nil) {
			return left.DetectedTrips != nil
		}
		if left.DetectedTrips != nil && right.DetectedTrips != nil && *left.DetectedTrips != *right.DetectedTrips {
			return *left.DetectedTrips > *right.DetectedTrips
		}
	default:
		if (left.SpeedKmh == nil) != (right.SpeedKmh == nil) {
			return left.SpeedKmh != nil
		}
		if left.SpeedKmh != nil && right.SpeedKmh != nil && *left.SpeedKmh != *right.SpeedKmh {
			return *left.SpeedKmh > *right.SpeedKmh
		}
	}
	return left.Id < right.Id
}

func fleetLess(left, right api.FleetVehicle, order string) bool {
	comparison := 0
	switch order {
	case "distance":
		comparison = compareNullable(left.DistanceKm, right.DistanceKm)
	case "trips":
		comparison = compareNullable(left.DetectedTrips, right.DetectedTrips)
	case "first_seen":
		comparison = left.FirstSeen.Compare(right.FirstSeen)
	case "model":
		comparison = strings.Compare(stringValue(left.Model), stringValue(right.Model))
	case "vehicle":
		comparison = strings.Compare(left.Id, right.Id)
	default:
		comparison = right.LastSeen.Compare(left.LastSeen)
	}
	if comparison == 0 {
		return left.Id < right.Id
	}
	return comparison < 0
}

// compareNullable orders present measurements before missing ones, descending.
func compareNullable[T ~int | ~float64](left, right *T) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return 1
	}
	if right == nil {
		return -1
	}
	if *left > *right {
		return -1
	}
	if *left < *right {
		return 1
	}
	return 0
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func normalizeFleetVehicle(v api.FleetVehicle) api.FleetVehicle {
	if v.RouteIds == nil {
		v.RouteIds = []string{}
	}
	sort.Strings(v.RouteIds)
	if v.Model != nil {
		v.Model = optional(publishedModel("", *v.Model))
	}
	return v
}

func fleetMatches(v api.FleetVehicle, query string) bool {
	text := v.SourceId
	if v.Model != nil {
		text += " " + *v.Model
	}
	if v.LicensePlate != nil {
		text += " " + *v.LicensePlate
	}
	return query == "" || strings.Contains(strings.ToLower(text), query)
}
