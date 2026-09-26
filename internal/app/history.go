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

// historicalFilter freezes the selected observation window and ingestion generation.
func (s *Server) historicalFilter(ctx context.Context) (Filter, int64, string, error) {
	filter, err := s.filter(ctx, true)
	if err != nil {
		return filter, 0, "", err
	}
	generation, revision, err := s.snapshotRevision(ctx, &filter)
	return filter, generation, revision, err
}

// GetMetrics aggregates retained reported observations with honest nullable metrics.
func (s *Server) GetMetrics(ctx context.Context, _ api.GetMetricsRequestObject) (api.GetMetricsResponseObject, error) {
	filter, generation, revision, err := s.historicalFilter(ctx)
	if err != nil {
		return nil, err
	}
	metrics, err := s.Store.readMetrics(ctx, filter, generation)
	if err != nil {
		return nil, err
	}
	metrics.Revision = revision
	state, _ := s.Cache.state("")
	metrics.ReportedVehicles, metrics.EstimatedVehicles = liveVehicleCounts(state, filter)
	return api.GetMetrics200JSONResponse(metrics), nil
}

func liveVehicleCounts(state *State, filter Filter) (*int, *int) {
	reported, estimated := 0, 0
	available := false
	for p, data := range state.Live {
		if !filter.selected(p) {
			continue
		}
		if time.Since(data.Collected) <= sourceFreshness {
			available = true
		}
		r, e := countLiveVehicles(data.Vehicles, filter.Route)
		reported += r
		estimated += e
	}
	if !available {
		return nil, nil
	}
	return ptr(reported), ptr(estimated)
}

func countLiveVehicles(vehicles []api.Vehicle, route string) (int, int) {
	reported, estimated := 0, 0
	for _, v := range vehicles {
		if route != "" && (v.RouteId == nil || *v.RouteId != route) {
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
	return reported, estimated
}

// ListHistory returns retained five-minute speed and volume samples.
func (s *Server) ListHistory(ctx context.Context, _ api.ListHistoryRequestObject) (api.ListHistoryResponseObject, error) {
	filter, generation, revision, err := s.historicalFilter(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.Store.readHistory(ctx, filter, generation)
	if err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListHistory200JSONResponse{Data: data, Page: page}, nil
}

// ListFleet lists vehicles detected in the selected observation window.
func (s *Server) ListFleet(ctx context.Context, _ api.ListFleetRequestObject) (api.ListFleetResponseObject, error) {
	filter, generation, revision, err := s.historicalFilter(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.Store.readFleet(ctx, filter, generation)
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return fleetLess(out[i], out[j], filter.Sort) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListFleet200JSONResponse{Data: data, Page: page}, nil
}

// ListTraffic aggregates observed transit speeds by spatial cell.
func (s *Server) ListTraffic(ctx context.Context, _ api.ListTrafficRequestObject) (api.ListTrafficResponseObject, error) {
	filter, generation, revision, err := s.historicalFilter(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.Store.readTraffic(ctx, filter, generation)
	if err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListTraffic200JSONResponse{Data: data, Page: page}, nil
}

// ListRankings ranks route observations without inferring completed trips.
func (s *Server) ListRankings(ctx context.Context, _ api.ListRankingsRequestObject) (api.ListRankingsResponseObject, error) {
	filter, generation, revision, err := s.historicalFilter(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.Store.readRankings(ctx, filter, generation)
	if err != nil {
		return nil, err
	}
	state, _ := s.Cache.state("")
	nameRankings(out, state)
	sort.Slice(out, func(i, j int) bool { return rankingLess(out[i], out[j], filter.Sort) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	page, data := paginate(out, filter, revision)
	return api.ListRankings200JSONResponse{Data: data, Page: page}, nil
}

func nameRankings(out []api.Ranking, state *State) {
	names := map[string]string{}
	for _, d := range state.Static {
		for _, r := range d.Routes {
			names[r.Id] = r.ShortName + " · " + r.LongName
		}
	}
	for i := range out {
		out[i].RouteName = names[out[i].RouteId]
		if out[i].RouteName == "" {
			out[i].RouteName = out[i].RouteId
		}
	}
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
