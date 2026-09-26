package app

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

func continuityFetcher() (*Fetcher, *Server) {
	c := NewCache()
	s := &Store{HistoryInterval: staticRefreshInterval, collector: newHistoryCollector()}
	f := NewFetcher(s, c, zap.NewNop())
	for _, p := range providers {
		f.lastPersist[p.ID] = time.Now()
	}
	return f, &Server{Store: s, Cache: c}
}

func vehiclePage(t *testing.T, s *Server, query string) api.VehiclePage {
	t.Helper()
	q, _ := url.ParseQuery(query)
	if q.Get("limit") == "" {
		q.Set("limit", "500")
	}
	r := httptest.NewRequest("GET", "http://localhost/api/v1/vehicles?"+q.Encode(), nil)
	result, err := s.ListVehicles(context.WithValue(context.Background(), requestKey, r), api.ListVehiclesRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	return api.VehiclePage(result.(api.ListVehicles200JSONResponse))
}

func TestMissingSnapshotKeepsOriginalPosition(t *testing.T) {
	for _, p := range providers {
		t.Run(p.ID, func(t *testing.T) {
			f, s := continuityFetcher()
			now := time.Now().UTC()
			v := api.Vehicle{Id: qualify(p.ID, "v"), OperatorId: p.ID, SourceId: "v", Lat: 38.72, Lon: -9.15, ObservedAt: now, CollectedAt: now, PositionKind: api.VehiclePositionKindReported}
			if p.Mode == "metro" {
				v.PositionKind = api.VehiclePositionKindEstimated
			}
			f.saveLive(context.Background(), p, []api.Vehicle{v}, now)
			first := vehiclePage(t, s, "operators="+p.ID)
			f.saveLive(context.Background(), p, []api.Vehicle{}, now.Add(5*time.Second))
			after := vehiclePage(t, s, "operators="+p.ID)
			if len(after.Data) != 1 || !after.Data[0].ObservedAt.Equal(first.Data[0].ObservedAt) {
				t.Fatalf("last position disappeared or rejuvenated: %+v", after.Data)
			}
		})
	}
}

func continuityVehicle(id string, at time.Time) api.Vehicle {
	return api.Vehicle{Id: "cp:" + id, OperatorId: "cp", SourceId: id, ObservedAt: at, CollectedAt: at, Lat: 38.72, Lon: -9.15, PositionKind: api.VehiclePositionKindReported, SourceUrl: hubBase}
}
func TestRegressedRecoveryKeepsLastKnown(t *testing.T) {
	now := time.Now().UTC()
	op := api.Operator{Status: api.OperatorStatusOk}
	original := continuityVehicle("train", now)
	d, _ := nextLive(nil, op, []api.Vehicle{original}, now)
	d, _ = nextLive(d, op, nil, now.Add(5*time.Second))
	regressed := continuityVehicle("train", now.Add(-time.Second))
	regressed.Lon = -9.3
	d, _ = nextLive(d, op, []api.Vehicle{regressed}, now.Add(10*time.Second))
	rows, reported, _, retained, _ := projectLive(d, op, nil, now.Add(10*time.Second))
	if len(d.Vehicles) != 0 || len(d.Samples) != 0 || len(rawHistory(d, nil)) != 0 || len(rows) != 1 || !rows[0].LastKnown || rows[0].Lon != original.Lon || !rows[0].ObservedAt.Equal(original.ObservedAt) || reported == nil || *reported != 0 || retained != 1 {
		t.Fatalf("regressed report restored current membership: %+v %+v", d, rows)
	}
}

func TestPaginationDoesNotRetainReplayLedgers(t *testing.T) {
	cache := NewCache()
	now := time.Now().UTC()
	op := cache.operator("cp")
	op.Status = api.OperatorStatusOk
	d, _ := nextLive(nil, op, []api.Vehicle{continuityVehicle("train", now)}, now)
	cache.update("cp", nil, d, op)
	frozen, _ := cache.state("")
	cache.update("metro", nil, &LiveData{Collected: now}, cache.operator("metro"))
	archived, err := cache.state(frozen.Revision)
	current, _ := cache.state("")
	if err != nil || len(archived.Live["cp"].Continuity) != 0 || len(frozen.Live["cp"].Continuity) != 1 || len(current.Live["cp"].Continuity) != 1 {
		t.Fatal("ledger archived, lost from current, or mutated in-flight")
	}
	rows, _, _, _, _ := projectLive(archived.Live["cp"], op, nil, now)
	if len(rows) != 1 || rows[0].LastKnown || rows[0].Id != d.Vehicles[0].Id {
		t.Fatal("archival pruning changed frozen display")
	}
}

func TestContinuityClocksRecoveryAndHistory(t *testing.T) {
	now := time.Now().UTC()
	op := api.Operator{Status: api.OperatorStatusOk}
	a := continuityVehicle("a", now)
	d, _ := nextLive(nil, op, []api.Vehicle{a}, now)
	if len(d.Samples) != 1 {
		t.Fatal("new source observation missing")
	}
	frozen := d
	d, _ = nextLive(d, op, nil, now.Add(5*time.Second))
	rows, r, _, n, _ := projectLive(d, op, nil, now.Add(6*time.Second))
	if len(rows) != 1 || !rows[0].LastKnown || !rows[0].Stale || *r != 0 || n != 1 || len(d.Samples) != 0 || len(rawHistory(d, nil)) != 0 {
		t.Fatal("omission is not display only")
	}
	if !rows[0].InactiveAt.Equal(now.Add(5*time.Minute)) || !rows[0].LastKnownExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatal("clocks moved")
	}
	d, _ = nextLive(d, op, []api.Vehicle{a}, now.Add(10*time.Second))
	if len(d.Samples) != 0 || !d.Continuity[a.Id].Discontinuous {
		t.Fatal("equal recovery reset history/baseline")
	}
	a.ObservedAt = now.Add(15 * time.Second)
	a.CollectedAt = a.ObservedAt
	a.Lon += .0001
	d, dist := nextLive(d, op, []api.Vehicle{a}, a.ObservedAt)
	if len(d.Samples) != 1 || d.Samples[0].SpeedKmh != nil || dist[a.Id] != nil {
		t.Fatal("speed bridged omission")
	}
	a.ObservedAt = now.Add(20 * time.Second)
	a.CollectedAt = a.ObservedAt
	a.Lon += .0001
	d, dist = nextLive(d, op, []api.Vehicle{a}, a.ObservedAt)
	if d.Samples[0].SpeedKmh == nil || dist[a.Id] == nil {
		t.Fatal("subsequent pair lost")
	}
	if len(frozen.Vehicles) != 1 || len(frozen.LastKnown) != 0 || frozen.Continuity[a.Id].Discontinuous {
		t.Fatal("old state mutated")
	}
}

func TestContinuityHealthyRepeatExpiry(t *testing.T) {
	now := time.Now().UTC()
	op := api.Operator{Status: api.OperatorStatusOk}
	// Repeated healthy CM-style inventory expires without collector changes.
	for _, age := range []time.Duration{181 * time.Second, 5 * time.Minute, time.Hour - time.Nanosecond, time.Hour} {
		repeated := &LiveData{Vehicles: []api.Vehicle{continuityVehicle("old", now)}, Collected: now.Add(age)}
		got, reported, _, _, _ := projectLive(repeated, op, nil, now.Add(age))
		if *reported != 0 {
			t.Fatal("stale row counted")
		}
		if age < time.Hour {
			if len(got) != 1 || !got[0].LastKnown {
				t.Fatal("eligible old inventory hidden")
			}
		} else if len(got) != 0 {
			t.Fatal("old inventory never expired")
		}
	}
}

func TestContinuityFailedSourceExpiry(t *testing.T) {
	now := time.Now().UTC()
	op := api.Operator{Status: api.OperatorStatusOk}
	frozen, _ := nextLive(nil, op, []api.Vehicle{continuityVehicle("a", now)}, now)

	for _, status := range []api.OperatorStatus{api.OperatorStatusError, api.OperatorStatusLoading} {
		failed := op
		failed.Status = status
		got, r, _, _, _ := projectLive(frozen, failed, nil, now.Add(time.Minute))
		if len(got) != 1 || !got[0].LastKnown || r != nil {
			t.Fatal("failure treated as zero/current")
		}
		got, _, _, _, _ = projectLive(frozen, failed, nil, now.Add(time.Hour))
		if len(got) != 0 {
			t.Fatal("failed source kept expired position")
		}
	}
}

func TestContinuityCapsRestartAndReplay(t *testing.T) {
	now := time.Now().UTC()
	op := api.Operator{Status: api.OperatorStatusOk}
	rows := make([]api.Vehicle, maxLastKnown+1)
	for i := range rows {
		rows[i] = continuityVehicle(stringID(uint64(i)), now.Add(-time.Duration(i)*time.Millisecond))
	}
	d, _ := nextLive(nil, op, rows, now)
	d, _ = nextLive(d, op, nil, now.Add(time.Second))
	got, _, _, count, truncated := projectLive(d, op, nil, now.Add(2*time.Second))
	if len(got) != maxLastKnown || count != maxLastKnown || !truncated || d.LastKnownTruncatedUntil.IsZero() {
		t.Fatal("cap not bounded/visible")
	}
	d, _ = nextLive(d, op, nil, now.Add(3*time.Second))
	if !d.LastKnownTruncatedUntil.After(now) {
		t.Fatal("truncation forgotten")
	}
	// Old candidate was evicted but its high-water remains, so replay cannot create evidence.
	d, _ = nextLive(d, op, []api.Vehicle{rows[len(rows)-1]}, now.Add(4*time.Second))
	if len(d.Samples) != 0 {
		t.Fatal("evicted replay recorded")
	}
	restart := restoreLive(d, now.Add(5*time.Second))
	replay := rows[0]
	replay.ObservedAt = now.Add(20 * time.Second)
	d, _ = nextLive(restart, op, []api.Vehicle{replay}, now.Add(6*time.Second))
	if len(d.Samples) != 0 || !d.ReplayFloor.Equal(now.Add(35*time.Second)) {
		t.Fatal("pre-restart skew replay")
	}
	replay.ObservedAt = now.Add(40 * time.Second)
	d, dist := nextLive(d, op, []api.Vehicle{replay}, replay.ObservedAt)
	if len(d.Samples) != 1 || dist[replay.Id] != nil || d.Samples[0].SpeedKmh != nil {
		t.Fatal("restart recovery bridged")
	}
	ledger := &LiveData{Continuity: map[string]vehicleContinuity{}}
	for i := 0; i < maxContinuityIDs+1; i++ {
		ledger.Continuity[stringID(uint64(i))] = vehicleContinuity{Observed: now.Add(-time.Duration(i) * time.Millisecond)}
	}
	boundContinuity(ledger, now)
	if len(ledger.Continuity) != maxContinuityIDs || ledger.ReplayFloor.IsZero() {
		t.Fatal("ledger eviction unbounded or lost floor")
	}
	d, _ = nextLive(ledger, op, []api.Vehicle{continuityVehicle("unknown", ledger.ReplayFloor)}, now)
	if len(d.Samples) != 0 {
		t.Fatal("unknown replay below floor")
	}
	// Original timestamps and old cache compatibility survive serialization.
	blob, err := encodeCache(restart)
	if err != nil || len(blob) == 0 {
		t.Fatal(err)
	}
	legacy := restoreLive(&LiveData{Vehicles: rows[:1], Collected: now}, now)
	if !legacy.Unverified || len(legacy.Samples) != 0 || !legacy.Vehicles[0].ObservedAt.Equal(rows[0].ObservedAt) {
		t.Fatal("restore rejuvenated legacy")
	}
}

func TestContinuityPaginationStoppedCollector(t *testing.T) {
	f, s := continuityFetcher()
	now := time.Now().UTC()
	p, _ := providerByID("cp")
	f.saveLive(context.Background(), p, []api.Vehicle{continuityVehicle("a", now.Add(-59*time.Minute)), continuityVehicle("b", now.Add(-58*time.Minute))}, now)
	s.Cache.mu.Lock()
	s.Cache.current.Created = now.Add(-10 * time.Minute)
	s.Cache.mu.Unlock()
	first := vehiclePage(t, s, "operators=cp&limit=1")
	if !first.Page.HasMore || !strings.HasPrefix(*first.Page.Revision, "v:") {
		t.Fatal("not paginated")
	}
	second := vehiclePage(t, s, "operators=cp&offset=1&limit=1&revision="+url.QueryEscape(*first.Page.Revision))
	if len(second.Data) != 1 || second.Data[0].Id == first.Data[0].Id {
		t.Fatal("stopped current state continuation failed")
	}
	state, asOf, _, err := s.vehicleState(Filter{Revision: *first.Page.Revision}, time.Now())
	if err != nil || state == nil || asOf.Before(state.Created) {
		t.Fatal(err)
	}
	_, _, _, err = s.vehicleState(Filter{Revision: *first.Page.Revision}, asOf.Add(5*time.Minute+time.Nanosecond))
	if err == nil {
		t.Fatal("expired token accepted")
	}
	for _, stamp := range []time.Time{now.Add(time.Minute), state.Created.Add(-time.Second)} {
		_, _, _, err = s.vehicleState(Filter{Revision: "v:" + state.Revision + ":" + strconv.FormatInt(stamp.UnixNano(), 10)}, now)
		if err == nil {
			t.Fatal("invalid timestamp accepted")
		}
	}
	if _, err = s.Cache.state(state.Revision); err == nil {
		t.Fatal("global state expiry weakened")
	}
}

func TestContinuityMixedCoverageAndPlanFilters(t *testing.T) {
	now := time.Now().UTC()
	c := NewCache()
	op := c.operator("cp")
	op.Status = api.OperatorStatusOk
	a := continuityVehicle("a", now)
	a.PlanId = ptr("old")
	a.RouteId = ptr("cp:1")
	c.update("cp", &StaticData{PlanID: "new"}, &LiveData{Collected: now, Vehicles: []api.Vehicle{a}}, op)
	state, _ := c.state("")
	r, e, coverage, missing := liveCounts(state, Filter{Operators: []string{"cp", "ttsl"}}, now)
	if r == nil || *r != 1 || *e != 0 || coverage != api.MetricsLiveCoveragePartial || len(missing) != 1 || missing[0] != "ttsl" {
		t.Fatal("mixed coverage fabricated")
	}
	rows, _, _, _, _ := projectLive(state.Live["cp"], op, state.Static["cp"], now)
	if rows[0].RouteId != nil {
		t.Fatal("old plan route leaked")
	}
	r, _, coverage, _ = liveCounts(state, Filter{Operators: []string{"ttsl"}}, now)
	if r != nil || coverage != api.MetricsLiveCoverageUnavailable {
		t.Fatal("unknown zero")
	}
}

func TestContinuityDurabilityAndRestore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	c := NewCache()
	f := NewFetcher(s, c, zap.NewNop())
	p, _ := providerByID("cp")
	now := time.Now().UTC()
	v := continuityVehicle("durable", now)
	f.saveLive(ctx, p, []api.Vehicle{v}, now)
	f.saveLive(ctx, p, nil, now.Add(time.Second))
	// Persist the current bounded live state without restaging a displayed position.
	state, _ := c.state("")
	d := state.Live[p.ID]
	if err := s.Save(ctx, p.ID, nil, d, c.operator(p.ID), nil); err != nil {
		t.Fatal(err)
	}
	checkSnapshotCount(t, s, 1)
	restored := NewCache()
	if err := s.Restore(ctx, restored); err != nil {
		t.Fatal(err)
	}
	state, _ = restored.state("")
	if len(state.Live[p.ID].LastKnown) != 1 || !state.Live[p.ID].Unverified {
		t.Fatal("last-known cache not restored")
	}
	_, r, _, _, _ := projectLive(state.Live[p.ID], state.Operators[p.ID], nil, time.Now())
	if r != nil {
		t.Fatal("restore claimed verified coverage")
	}
	rows, _, _, _, _ := projectLive(state.Live[p.ID], state.Operators[p.ID], nil, now.Add(time.Hour))
	if len(rows) != 0 {
		t.Fatal("restored position clock reset")
	}
	// Raw failed writes are partial loss; equal replay must not manufacture new observations.
	if _, err := s.DB.Exec(ctx, "DROP TABLE source_health"); err != nil {
		t.Fatal(err)
	}
	v.ObservedAt = now.Add(5 * time.Second)
	f.saveLive(ctx, p, []api.Vehicle{v}, now.Add(5*time.Second))
	if _, err := s.DB.Exec(ctx, "CREATE TABLE source_health(operator_id TEXT PRIMARY KEY,payload JSONB NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	f.saveLive(ctx, p, []api.Vehicle{v}, now.Add(6*time.Second))
	checkSnapshotCount(t, s, 1)
}

func TestContinuityPagingClockAfterPublication(t *testing.T) {
	_, s := continuityFetcher()
	before := time.Now().UTC()
	s.Cache.update("cp", nil, &LiveData{Collected: time.Now().UTC()}, s.Cache.operator("cp"))
	state, asOf, revision, err := s.vehicleState(Filter{}, before)
	if err != nil || asOf.Before(state.Created) {
		t.Fatal("first-page clock precedes publication", err)
	}
	if _, _, _, err = s.vehicleState(Filter{Revision: revision}, time.Now().UTC()); err != nil {
		t.Fatal("page2 rejected valid first page", err)
	}
}

func TestHistorySamplesDoNotAccumulateInRevisions(t *testing.T) {
	now := time.Now().UTC()
	v := continuityVehicle("sample", now)
	d := &LiveData{Vehicles: []api.Vehicle{v}, Samples: []api.Vehicle{v}, Collected: now}
	c := NewCache()
	c.update("cp", nil, d, c.operator("cp"))
	state, _ := c.state("")
	if len(state.Live["cp"].historyVehicles()) != 0 || len(d.Samples) != 1 {
		t.Fatal("publication samples retained or caller mutated")
	}
}
