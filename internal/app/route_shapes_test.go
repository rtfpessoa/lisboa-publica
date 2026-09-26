package app

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"go.uber.org/zap"
	"io"
	"lisboapublica/internal/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func shapeArchive(t *testing.T, cm bool) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	route := "1"
	header := "route_id,route_short_name,route_long_name"
	row := "1,1,Centro"
	if cm {
		route = "1001_0"
		header += " ,line_id"
		header = strings.ReplaceAll(header, " ", "")
		row = "1001_0,1001,Centro,1001"
	}
	files := map[string]string{
		"routes.txt":     header + "\n" + row + "\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nS,Centro,38.72,-9.15\n",
		"trips.txt":      "route_id,service_id,trip_id,trip_headsign,shape_id,direction_id\n" + route + ",daily,A,Centro,one,0\n" + route + ",daily,B,Terminal,two,1\n" + route + ",daily,C,Centro,one,0\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nA,12:00:00,12:00:00,S,1\nB,12:00:00,12:00:00,S,1\nC,12:00:00,12:00:00,S,1\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\ndaily,1,1,1,1,1,1,1,20260101,20261231\n",
		"shapes.txt":     "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\none,38.73,-9.15,3\none,38.72,-9.15,1\none,38.725,-9.15,2\ntwo,38.73,-9.15,1\ntwo,38.725,-9.14,2\ntwo,38.72,-9.15,3\n",
	}
	for name, data := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(w, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestGTFSShapeDirectionsAndCMLineJoin(t *testing.T) {
	p, _ := providerByID("carris")
	now := time.Now().UTC()
	data, err := readGTFS(shapeArchive(t, false), p, "plan", "20260101", "20261231", hubBase, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Shapes) != 2 || *data.Shapes[0].DirectionId != 0 || *data.Shapes[1].DirectionId != 1 {
		t.Fatalf("variants=%+v", data.Shapes)
	}
	first := data.Shapes[0].Geometry
	if len(first) != 2 || first[0][0] != -9.15 || first[0][1] != 38.72 || first[1][1] != 38.73 {
		t.Fatalf("ordered endpoints not retained: %v", first)
	}
	if len(data.Shapes[1].Geometry) != 3 {
		t.Fatal("turn was simplified away")
	}
	p, _ = providerByID("cm")
	plan := &hubPlan{ID: "plan", Agency: "agency"}
	shapes, err := readCMShapes(shapeArchive(t, true), plan, p, hubBase, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(shapes) != 2 || shapes[0].RouteId != "cm:1001" || !strings.Contains(shapes[0].Id, "plan/agency/") {
		t.Fatalf("CM identity/join=%+v", shapes)
	}
	catalog := &StaticData{Routes: []api.RouteDetail{{Id: "cm:1001"}}, Shapes: shapes}
	attachCMRouteGeometry(catalog)
	if catalog.Routes[0].Geometry == nil || len(*catalog.Routes[0].Geometry) != 2 {
		t.Fatal("CM selected-route representative shape absent")
	}
}

func TestRouteShapesHTTPPaginationAndRevision(t *testing.T) {
	store := testStore(t)
	cache := NewCache()
	now := time.Now().UTC()
	p, _ := providerByID("carris")
	data, err := readGTFS(shapeArchive(t, false), p, "plan", "20260101", "20261231", hubBase, now)
	if err != nil {
		t.Fatal(err)
	}
	cache.update("carris", data, nil, cache.operator("carris"))
	server, err := NewServer(store, cache, Options{Origin: "http://localhost", Environment: "development", PublicReads: true, RateLimit: 100}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	status, b := req(t, ts.Client(), "GET", ts.URL+"/api/v1/route-shapes?operators=carris&limit=1", nil, nil)
	expectStatus(t, status, 200, b)
	first := decode[api.RouteShapePage](t, b)
	if first.Page.Total != 2 || len(first.Data) != 1 || first.Coverage[0].Status != "available" {
		t.Fatalf("page=%s", b)
	}
	changed := *data
	changed.Shapes = nil
	cache.update("carris", &changed, nil, cache.operator("carris"))
	status, b = req(t, ts.Client(), "GET", ts.URL+fmt.Sprintf("/api/v1/route-shapes?operators=carris&limit=1&offset=1&revision=%s", *first.Page.Revision), nil, nil)
	expectStatus(t, status, 200, b)
	second := decode[api.RouteShapePage](t, b)
	if second.Page.Total != 2 || second.Data[0].Id == first.Data[0].Id {
		t.Fatalf("pagination drifted: %s", b)
	}
	status, b = req(t, ts.Client(), "GET", ts.URL+"/api/v1/route-shapes?operators=cp", nil, nil)
	expectStatus(t, status, 200, b)
	missing := decode[api.RouteShapePage](t, b)
	if len(missing.Data) != 0 || missing.Coverage[0].Status != "unavailable" {
		t.Fatalf("missing coverage=%s", b)
	}
	fetcher := NewFetcher(store, cache, zap.NewNop())
	changed.Shapes = data.Shapes
	changed.GeometryUpdated = &now
	cache.update("cm", &changed, nil, cache.operator("cm"))
	catalog := &StaticData{Routes: []api.RouteDetail{{Id: "cm:new"}}}
	fetcher.updateCMShapes(context.Background(), catalog, nil, fmt.Errorf("test outage"), 20260926)
	if len(catalog.Shapes) != 2 || catalog.GeometryError == nil || catalog.Routes[0].Id != "cm:new" {
		t.Fatal("geometry failure discarded catalog or previous geometry")
	}
}

func TestGeometryFailureCoverageAndConditionalBodyBound(t *testing.T) {
	now := time.Now().UTC()
	data := &StaticData{GeometryUpdated: &now, GeometryError: ptr("upstream unavailable"), Shapes: []api.RouteShape{{Id: "cm:old"}}}
	coverage := geometryCoverage("cm", data)
	if coverage.Status != "stale" || coverage.UpdatedAt == nil {
		t.Fatalf("old geometry coverage=%+v", coverage)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Error("large body retained in conditional cache")
		}
		w.Header().Set("ETag", "large")
		_, _ = io.WriteString(w, strings.Repeat("x", maxConditionalBodyBytes+1))
	}))
	defer server.Close()
	fetcher := NewFetcher(nil, NewCache(), zap.NewNop())
	for i := 0; i < 2; i++ {
		body, err := fetcher.fetch(context.Background(), server.URL, maxConditionalBodyBytes+1)
		if err != nil || len(body) != maxConditionalBodyBytes+1 {
			t.Fatalf("fetch bytes=%d err=%v", len(body), err)
		}
	}
	if len(fetcher.blobs) != 0 {
		t.Fatal("large response retained")
	}
}

func TestCMPlanWithoutShapesIsRejected(t *testing.T) {
	blob := shapeArchive(t, true)
	original, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range original.File {
		if file.Name == "shapes.txt" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		target, err := writer.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.Copy(target, reader); err != nil {
			t.Fatal(err)
		}
		_ = reader.Close()
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	p, _ := providerByID("cm")
	shapes, err := readCMShapes(output.Bytes(), &hubPlan{ID: "plan", Agency: "agency"}, p, hubBase, time.Now())
	if err == nil || len(shapes) > 0 {
		t.Fatal("missing agency geometry silently accepted")
	}
}
