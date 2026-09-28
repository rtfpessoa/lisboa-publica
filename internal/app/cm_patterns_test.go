package app

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

const cmTestTrips = "route_id,service_id,trip_id,trip_headsign,shape_id,direction_id,pattern_id\n1001_0,daily,A,Centro,one,0,p\n1001_0,daily,B,Terminal,two,1,q\n1001_0,daily,C,Centro,one,0,p\n"
const cmTestVisits = "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nC,12:00:00,12:00:00,S,3\nA,12:00:00,12:00:00,T,2\nB,12:00:00,12:00:00,S,2\nC,12:00:00,12:00:00,S,1\nA,12:00:00,12:00:00,S,3\nB,12:00:00,12:00:00,T,1\nC,12:00:00,12:00:00,T,2\nA,12:00:00,12:00:00,S,1\n"

func cmPatternArchive(t *testing.T, changes map[string]string) []byte {
	t.Helper()
	tables := map[string]string{"trips.txt": cmTestTrips, "stop_times.txt": cmTestVisits, "stops.txt": "stop_id,stop_name,stop_lat,stop_lon\nS,Centro,38.72,-9.15\nT,Terminal,38.73,-9.14\n"}
	for k, v := range changes {
		tables[k] = v
	}
	return replaceGTFS(t, shapeArchive(t, true), tables)
}
func parseCMTest(t *testing.T, changes map[string]string) *StaticData {
	t.Helper()
	p, _ := providerByID("cm")
	d, err := readCMNetwork(cmPatternArchive(t, changes), &hubPlan{ID: "plan", Agency: "agency"}, p, hubBase, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestCMPatternsVerifyInterleavedTripsAndLoops(t *testing.T) {
	d := parseCMTest(t, nil)
	if len(d.CMPaths) != 2 || d.CMPathError != nil {
		t.Fatal("valid patterns discarded", d.CMPathError)
	}
	p := d.CMPaths[0]
	if p.ID != "cm:[plan][agency]p" || p.Line != "cm:1001" || len(p.Visits) != 3 || p.Visits[0].Stop != "S" || p.Visits[1].Stop != "T" || p.Visits[2].Stop != "S" || p.Visits[2].Sequence != 3 || p.Shape != "cm:plan/agency/1001_0/one/0" {
		t.Fatalf("wrong published path %+v", p)
	}
}
func TestCMPatternsRejectAmbiguityAndInvalidRows(t *testing.T) {
	for name, changes := range map[string]map[string]string{
		"duplicate":           {"stop_times.txt": cmTestVisits + "C,12:00:00,12:00:00,S,1\n"},
		"missing":             {"stop_times.txt": strings.Replace(cmTestVisits, "C,12:00:00,12:00:00,T,2\n", "", 1)},
		"extra":               {"stop_times.txt": cmTestVisits + "C,12:00:00,12:00:00,S,4\n"},
		"different_stop":      {"stop_times.txt": strings.Replace(cmTestVisits, "C,12:00:00,12:00:00,T,2", "C,12:00:00,12:00:00,S,2", 1)},
		"different_direction": {"trips.txt": strings.Replace(cmTestTrips, "C,Centro,one,0,p", "C,Centro,one,1,p", 1)},
		"different_shape":     {"trips.txt": strings.Replace(cmTestTrips, "C,Centro,one,0,p", "C,Centro,two,0,p", 1)},
	} {
		t.Run(name, func(t *testing.T) {
			d := parseCMTest(t, changes)
			if len(d.CMPaths) != 1 || !strings.HasSuffix(d.CMPaths[0].ID, "]q") {
				t.Fatal("ambiguous pattern published")
			}
		})
	}
	for name, changes := range map[string]map[string]string{
		"unknown_stop":     {"stop_times.txt": strings.Replace(cmTestVisits, ",T,2", ",unknown,2", 1)},
		"unknown_trip":     {"stop_times.txt": cmTestVisits + "unknown,12:00:00,12:00:00,S,1\n"},
		"invalid_sequence": {"stop_times.txt": strings.Replace(cmTestVisits, ",S,3", ",S,-1", 1)},
		"no_pattern":       {"trips.txt": strings.ReplaceAll(cmTestTrips, ",pattern_id", ",missing_field")},
	} {
		t.Run(name, func(t *testing.T) {
			d := parseCMTest(t, changes)
			if len(d.CMPaths) != 0 || d.CMPathError == nil || len(d.Shapes) == 0 {
				t.Fatal("invalid enrichment did not degrade safely")
			}
		})
	}
	a, err := openGTFS(cmPatternArchive(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	a["stop_times.txt"].CRC32 ^= 1
	d := parseCMTest(t, nil)
	if _, err = readCMPatterns(a, &hubPlan{ID: "plan", Agency: "agency"}, d.Shapes); err == nil {
		t.Fatal("corrupt stop times accepted")
	}
}
func cmFullCallsFixture(t *testing.T) (*Cache, *StaticData, api.Vehicle, http.Handler) {
	t.Helper()
	cache, _, v, _ := callsFixture(t, "cm")
	d := parseCMTest(t, nil)
	d.PlanID = ""
	d.Stops = []api.Stop{{Id: "cm:S", SourceId: "S", OperatorId: "cm", Name: "Centro", Lat: 38.72, Lon: -9.15, RouteIds: []string{"cm:1001"}}, {Id: "cm:T", SourceId: "T", OperatorId: "cm", Name: "Terminal", Lat: 38.73, Lon: -9.14, RouteIds: []string{"cm:1001"}}}
	v.PlanId = nil
	v.OperationalDate = nil
	v.PatternId = ptr("cm:[plan][agency]p")
	v.RouteId = ptr("cm:1001")
	v.StopId = ptr("cm:S")
	cache.update("cm", d, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cm"))
	_, h := securityServer(t, &Store{}, cache)
	return cache, d, v, h
}
func TestCMCompleteCallsGeometryAndPinnedIdentity(t *testing.T) {
	cache, d, v, h := cmFullCallsFixture(t)
	path := "/api/v1/vehicles/cm:v/calls?limit=2"
	page := readCalls(t, h, path)
	if page.Coverage != "complete_published_route" || page.Progress != "unknown" || page.Page.Total != 3 || page.Geometry != nil || page.Vehicle.OperationalDate != nil {
		t.Fatalf("wrong semantics %+v", page)
	}
	for _, row := range page.Data {
		if row.Kind != "published_route" || row.ScheduledAt != nil || row.ExpectedAt != nil || row.Stop == nil || row.StopStaticUpdatedAt == nil {
			t.Fatal("invented schedule or missing stop provenance")
		}
	}
	full := readCalls(t, h, path+"&include_geometry=true")
	if full.Geometry == nil || full.Geometry.Id != d.CMPaths[0].Shape {
		t.Fatal("wrong exact variant")
	}
	newer := *d
	newer.CMPaths = []CMPath{}
	newer.Shapes = nil
	cache.update("cm", &newer, nil, cache.operator("cm"))
	next := readCalls(t, h, path+"&include_geometry=true&offset=2&revision="+url.QueryEscape(*full.Page.Revision))
	if next.Geometry != nil || next.Coverage != "complete_published_route" || next.Page.Total != 3 || next.Data[0].StopId != "cm:S" || next.Data[0].Id == page.Data[0].Id {
		t.Fatal("loop visits or frozen path lost")
	}
	state, _ := cache.state("")
	n := navigationFor(state, state.Created, v)
	n.Pattern = "cm:[other][agency]p"
	if got := securityRequest(h, "GET", path+"&reference="+url.QueryEscape(encodeNavigation("n1.", n)), "", nil); got.Code != 410 {
		t.Fatal("forged pattern accepted")
	}
	if got := securityRequest(h, "GET", path+"&include_geometry=garbage", "", nil); got.Code != 400 {
		t.Fatal("geometry flag not validated")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildVehicleCalls(ctx, &State{Static: map[string]*StaticData{"cm": d}}, v, time.Now(), false); err != context.Canceled {
		t.Fatal("CM calls ignored cancellation")
	}
}
func TestCMCallsNeverGuessPatternsOrPartialCompleteRoutes(t *testing.T) {
	for name, change := range map[string]func(*api.Vehicle, *StaticData){
		"missing":              func(v *api.Vehicle, d *StaticData) { v.PatternId = nil },
		"plan":                 func(v *api.Vehicle, d *StaticData) { v.PatternId = ptr("cm:[other][agency]p") },
		"agency":               func(v *api.Vehicle, d *StaticData) { v.PatternId = ptr("cm:[plan][other]p") },
		"line":                 func(v *api.Vehicle, d *StaticData) { v.RouteId = ptr("cm:wrong") },
		"shape":                func(v *api.Vehicle, d *StaticData) { d.Shapes = nil },
		"unknown_catalog_stop": func(v *api.Vehicle, d *StaticData) { d.Stops = d.Stops[:1] },
	} {
		t.Run(name, func(t *testing.T) {
			cache, d, v, h := cmFullCallsFixture(t)
			change(&v, d)
			cache.update("cm", d, &LiveData{Vehicles: []api.Vehicle{v}, Collected: v.CollectedAt}, cache.operator("cm"))
			page := readCalls(t, h, "/api/v1/vehicles/cm:v/calls?include_geometry=true")
			if page.Coverage == "complete_published_route" || page.Geometry != nil || page.Availability != "next_stop_only" {
				t.Fatalf("unsafe fallback %+v", page)
			}
		})
	}
}
func TestCMPublishedPatternAndAtomicGeometryRetention(t *testing.T) {
	for _, raw := range []string{"", "p", "[plan][agency]", "[plan][agency]p\n", "[plan][agency]p[bad]", strings.Repeat("p", 256)} {
		if publishedCMPattern(raw) != nil {
			t.Fatal("invalid source pattern accepted")
		}
	}
	if textValue(publishedCMPattern("[plan][agency]p")) != "cm:[plan][agency]p" {
		t.Fatal("explicit identity lost")
	}
	cache, d, _, _ := cmFullCallsFixture(t)
	f := &Fetcher{publicationState: publicationState{Cache: cache}}
	newer := &StaticData{CMPaths: []CMPath{{ID: "wrong", Shape: "wrong"}}}
	f.retainGeometry(newer, "temporary failure")
	if len(newer.CMPaths) != len(d.CMPaths) || newer.CMPaths[0].Shape != d.Shapes[0].Id || newer.Shapes[0].Id != d.Shapes[0].Id {
		t.Fatal("retained geometry mixed with new patterns")
	}
}

func TestCMNativePatternPreservesOriginalObservation(t *testing.T) {
	now := time.Now().UTC()
	p, _ := providerByID("cm")
	f := &Fetcher{CM: "https://api.carrismetropolitana.pt/v2", publicationState: publicationState{Cache: NewCache()}}
	raw := cmPosition{ID: "unit", Line: "1001", Trip: "[plan][agency]trip", Pattern: "[plan][agency]p", Lat: 38.73, Lon: -9.14}
	original := now.Add(-time.Minute)
	v := f.cmVehicle(p, raw, original, now)
	if textValue(v.PatternId) != "cm:[plan][agency]p" || textValue(v.TripId) != "cm:[plan][agency]trip" || v.PlanId != nil || v.OperationalDate != nil || !v.ObservedAt.Equal(original) {
		t.Fatal("published pattern altered service or observation semantics")
	}
}
