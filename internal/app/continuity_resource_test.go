package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/api"
)

// Explicitly downloaded fixtures only. Run without race instrumentation to measure the app envelope.
func TestProviderContinuityResourceEnvelope(t *testing.T) {
	feeds := officialGeometryFeeds(t)
	manifest := os.Getenv("GTFS_GEOMETRY_FIXTURES")
	cache := NewCache()
	f, closeFixture := officialCMFetcher(t, cache, manifest)
	defer closeFixture()
	loadResourceNetworks(t, cache, f, feeds, false)
	var candidate *cmPathCandidate
	if os.Getenv("MEASURE_CM_PATH_CANDIDATE") == "1" {
		candidate = measureCMPaths(t, feeds)
	}
	store := &Store{HistoryInterval: staticRefreshInterval, collector: newHistoryCollector()}
	base := time.Now().Add(-time.Hour)
	// Reproduce one-hour extreme churn, saturating both caps and all retained versions.
	runContinuityChurn(t, cache, store, base)
	cpVersions := resourceCPPredictions(t, cache)
	if len(cache.versions) != maxCachedVersions {
		t.Fatal("churn did not saturate pagination retention")
	}
	t.Logf("churn_retained_versions=%d history_pending=%d", len(cache.versions), len(store.collector.Pending))
	arrivalWorkspace := resourceArrivalRetention(t, cache)
	// Static refresh overlap while a full current network and prior revisions remain retained.
	loadResourceNetworks(t, cache, f, feeds, true)
	if candidate != nil {
		next := measureCMPaths(t, feeds)
		runtime.KeepAlive(next)
	}
	concurrentNavigationReads(t, cache)
	concurrentCPDecode(t)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	state, _ := cache.state("")
	if len(state.Static) != 8 {
		t.Fatalf("expected all8 static providers, got%d", len(state.Static))
	}
	t.Logf("static_providers=%d retained_versions=%d history_pending=%d heap_bytes=%d sys_bytes=%d", len(state.Static), len(cache.versions), len(store.collector.Pending), m.HeapAlloc, m.Sys)
	for _, p := range providers {
		d := state.Live[p.ID]
		t.Logf("%s source=%d retained=%d ledger=%d", p.ID, len(d.Vehicles), len(d.LastKnown), len(d.Continuity))
	}
	if path := os.Getenv("RESOURCE_HEAP_PROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.WriteHeapProfile(f); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	runtime.KeepAlive(cache)
	runtime.KeepAlive(store)
	runtime.KeepAlive(cpVersions)
	runtime.KeepAlive(candidate)
	runtime.KeepAlive(arrivalWorkspace)
}

func runContinuityChurn(t *testing.T, cache *Cache, store *Store, base time.Time) {
	for tick := 0; tick <= 720; tick++ {
		now := base.Add(time.Duration(tick) * 5 * time.Second)
		for _, p := range providers {
			publishContinuityChurn(t, cache, store, p, tick, now)
		}
	}
}

func publishContinuityChurn(t *testing.T, cache *Cache, store *Store, p provider, tick int, now time.Time) {
	state, _ := cache.state("")
	op := state.Operators[p.ID]
	op.Status = api.OperatorStatusOk
	op.Error = nil
	rows := churnVehicles(p, tick, now)
	if p.ID == "cm" {
		resourceCMPatterns(rows, state.Static["cm"].CMPaths[0])
	}

	d, dist := nextLive(state.Live[p.ID], op, rows, now)
	if len(d.LastKnown) > maxLastKnown || len(d.Continuity) > maxContinuityIDs {
		t.Fatal("unbounded continuity")
	}
	store.stageLive(d, dist)
	cache.update(p.ID, nil, d, op)
	if tick%120 == 0 {
		update, err := prepareCacheUpdate(nil, d, op)
		if err != nil || int64(len(update.Live)+len(update.Health))*storageWriteOverhead > maximumWriteBytes {
			t.Fatal("live cache admission", err)
		}
	}
}

// Every CM source row carries its own pattern string, matching the native feed.
func resourceCMPatterns(rows []api.Vehicle, path CMPath) {
	for i := range rows {
		rows[i].PatternId = ptr(strings.Clone(path.ID))
		rows[i].RouteId = ptr(strings.Clone(path.Line))
	}
}

func churnVehicles(p provider, tick int, now time.Time) []api.Vehicle {
	// Source inventory is fixed independently of the display cap: reducing
	// retention must not weaken this extreme-churn input workload.
	const sourceRows = 1000
	rows := make([]api.Vehicle, sourceRows)
	for i := range rows {
		id := stringID(uint64(tick*sourceRows + i))
		rows[i] = api.Vehicle{Id: qualify(p.ID, id), SourceId: id, OperatorId: p.ID, ObservedAt: now, CollectedAt: now, PositionKind: api.VehiclePositionKindReported, Lat: 38.72, Lon: -9.15, CurrentStatus: ptr(api.STOPPEDAT), SourceStopId: ptr("published-stop"), StopId: ptr(qualify(p.ID, "published-stop")), StopName: ptr("Published terminal"), OperationalDate: ptr("2026-09-26"), Model: ptr("Published model"), LicensePlate: ptr("Sample plate"), SeatedCapacity: ptr(42), TotalCapacity: ptr(80), WheelchairAccessible: ptr(true), Contactless: ptr(false), SourceUrl: hubBase}
		if p.ID == "cp" {
			rows[i].ScheduledService = &api.ScheduledEndpoints{OriginSourceStopId: "outside-origin", OriginName: "Published origin outside Lisbon", DestinationSourceStopId: "outside-destination", DestinationName: "Published destination outside Lisbon", SourceUrl: hubBase, ServiceDate: "2026-09-26"}
		}
	}
	return rows
}

func officialCMFetcher(t *testing.T, cache *Cache, manifest string) (*Fetcher, func()) {
	t.Helper()
	fixtureDir := filepath.Dir(manifest)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/lines" || r.URL.Path == "/stops" {
			http.ServeFile(w, r, filepath.Join(fixtureDir, "cm-"+r.URL.Path[1:]+".json"))
		} else {
			http.NotFound(w, r)
		}
	}))

	f := NewFetcher(nil, cache, zap.NewNop())
	f.CM = ts.URL

	return f, ts.Close
}

func loadResourceNetworks(t *testing.T, cache *Cache, f *Fetcher, feeds []officialGeometryFeed, refresh bool) {
	network := &StaticData{Models: map[string]Metadata{}}
	for _, feed := range feeds {
		d := loadOfficialGeometry(t, feed)
		if feed.providerID() == "cm" {
			mergeCMFixture(network, d)
			continue
		}
		if refresh {
			verifyResourceStaticUpdate(t, cache, feed.Operator, d)
		}
		cache.update(feed.Operator, d, nil, staticHealth(cache.operator(feed.Operator), d))
	}
	publishResourceCM(t, cache, f, network)
}

func verifyResourceStaticUpdate(t *testing.T, cache *Cache, id string, d *StaticData) {
	state, _ := cache.state("")
	op := staticHealth(state.Operators[id], d)
	prepareGTFSGeometry(d, state.Static[id], op)
	update, err := prepareCacheUpdate(d, nil, op)
	if err != nil || int64(len(update.Static)+len(update.Health))*storageWriteOverhead > maximumWriteBytes {
		t.Fatal("static cache admission", err)
	}
}

func publishResourceCM(t *testing.T, cache *Cache, f *Fetcher, network *StaticData) {
	p, _ := providerByID("cm")
	cm, err := f.cmStatic(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	cm.Shapes, cm.Models = network.Shapes, network.Models
	cm.CMPaths = network.CMPaths
	sort.Slice(cm.CMPaths, func(i, j int) bool { return cm.CMPaths[i].ID < cm.CMPaths[j].ID })
	verifyResourceCMPaths(t, cm)
	cm.GeometryUpdated = ptr(time.Now())
	attachCMRouteGeometry(cm)
	op := staticHealth(cache.operator("cm"), cm)
	if !geometryCacheFits(cm, op) {
		t.Fatal("CM geometry admission")
	}
	cache.update("cm", cm, nil, op)
}

func verifyResourceCMPaths(t *testing.T, d *StaticData) {
	t.Helper()
	visits := 0
	for _, path := range d.CMPaths {
		visits += len(path.Visits)
	}
	if len(d.CMPaths) != 1621 || visits != 57286 {
		t.Fatalf("production CM index incomplete: %d patterns / %d visits", len(d.CMPaths), visits)
	}
	encoded, err := encodeCache(d)
	if err != nil {
		t.Fatal(err)
	}
	var restored StaticData
	reader, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewDecoder(reader).Decode(&restored); err != nil {
		t.Fatal(err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	restoredVisits := 0
	for _, path := range restored.CMPaths {
		restoredVisits += len(path.Visits)
	}
	if len(restored.CMPaths) != 1621 || restoredVisits != 57286 {
		t.Fatal("production CM index lost in serialization")
	}
	t.Logf("production_CM_patterns=%d visits=%d gzip_bytes=%d", len(restored.CMPaths), restoredVisits, len(encoded))
}
