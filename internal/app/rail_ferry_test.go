package app

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func replaceGTFS(t *testing.T, blob []byte, changes map[string]string) []byte {
	t.Helper()
	input, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, f := range input.File {
		text, ok := changes[f.Name]
		if ok && text == "" {
			continue
		}
		w, err := z.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			_, err = io.WriteString(w, text)
		} else {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			_, err = io.Copy(w, r)
			_ = r.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRailFerryGeometryFailureBoundaries(t *testing.T) {
	now := time.Now().UTC()
	p, _ := providerByID("cp")
	original := shapeArchive(t, false)
	valid, err := readGTFS(original, p, "plan", "20260101", "20261231", hubBase, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, shapes := range []string{"", "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\none,\"broken\n"} {
		d, err := readGTFS(replaceGTFS(t, original, map[string]string{"shapes.txt": shapes}), p, "plan", "20260101", "20261231", hubBase, now.Add(time.Minute))
		if err != nil {
			t.Fatal("optional shapes poisoned schedule", err)
		}
		if len(d.Routes) != 1 || len(d.Schedule.Trips) != 3 || len(d.Shapes) != 0 {
			t.Fatal("usable services lost")
		}
		prepareGTFSGeometry(d, valid, api.Operator{Id: "cp"})
		if len(d.Shapes) != 2 || geometryCoverage("cp", d).Status != api.GeometryCoverageStatusStale || !d.GeometryUpdated.Equal(*valid.GeometryUpdated) {
			t.Fatal("compatible fallback not stale")
		}
		d.PlanID = "other"
		stripGeometry(d)
		prepareGTFSGeometry(d, valid, api.Operator{Id: "cp"})
		if len(d.Shapes) != 0 || d.Routes[0].Geometry != nil || geometryCoverage("cp", d).Status != api.GeometryCoverageStatusUnavailable {
			t.Fatal("new plan reused old geometry")
		}
	}
	// Duplicate/conflicting point order makes that entire variant unavailable; never bridge it.
	partial := "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\none,38.72,-9.15,1\none,38.73,-9.15,1\ntwo,38.72,-9.15,1\ntwo,38.73,-9.14,2\n"
	d, err := readGTFS(replaceGTFS(t, original, map[string]string{"shapes.txt": partial}), p, "plan", "20260101", "20261231", hubBase, now)
	if err != nil || len(d.Shapes) != 1 || d.Routes[0].Geometry == nil || d.Shapes[0].ShapeId != "two" || geometryCoverage("cp", d).Status != api.GeometryCoverageStatusPartial {
		t.Fatal("ambiguous shape fabricated/full coverage", err)
	}
	// An exact duplicate point can be normalized without changing the published path.
	exact := strings.Replace(partial, "one,38.73,-9.15,1", "one,38.72,-9.15,1\none,38.73,-9.15,2", 1)
	d, err = readGTFS(replaceGTFS(t, original, map[string]string{"shapes.txt": exact}), p, "plan", "20260101", "20261231", hubBase, now)
	if err != nil || len(d.Shapes) != 2 || geometryCoverage("cp", d).Status != api.GeometryCoverageStatusAvailable {
		t.Fatal("identical duplicate not safely normalized", err)
	}
	// Corrupt shape data remains fatal even if the CSV fails before checksum consumption.
	corrupt := replaceGTFS(t, original, map[string]string{"shapes.txt": "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\none,\"broken\n"})
	index := bytes.Index(corrupt, []byte("broken"))
	if index < 0 {
		t.Fatal("corruption fixture")
	}
	corrupt[index] = 'X'
	if _, err = readGTFS(corrupt, p, "plan", "20260101", "20261231", hubBase, now); err == nil {
		t.Fatal("corrupt archive softened into geometry error")
	}
	if _, err = readGTFS(replaceGTFS(t, original, map[string]string{"stops.txt": "bad\n"}), p, "plan", "20260101", "20261231", hubBase, now); err == nil {
		t.Fatal("invalid base GTFS published")
	}
}

func TestRailFerryLocalVariantsAndPointGuard(t *testing.T) {
	now := time.Now().UTC()
	p, _ := providerByID("fertagus")
	trips := "route_id,service_id,trip_id,trip_headsign,shape_id,direction_id\n1,daily,A,Local,one,0\n1,daily,B,Outside,two,1\n"
	times := "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nA,12:00:00,12:00:00,S,1\n"
	d, err := readGTFS(replaceGTFS(t, shapeArchive(t, false), map[string]string{"trips.txt": trips, "stop_times.txt": times}), p, "plan", "20260101", "20261231", hubBase, now)
	if err != nil || len(d.Shapes) != 1 || d.Shapes[0].ShapeId != "one" {
		t.Fatal("nonlocal-only variant admitted", err)
	}
	g := &gtfsReader{provider: p, data: &StaticData{}, pointCount: maxGeometryPoints, shapes: map[string][]shapePoint{}}
	if err := g.shape(map[string]string{"shape_id": "one", "shape_pt_lat": "38.72", "shape_pt_lon": "-9.15", "shape_pt_sequence": "1"}); err != nil || !g.geometryFailed || len(g.shapes) != 0 {
		t.Fatal("optional point cap not bounded")
	}
	// Preserve the complete service continuation outside Lisbon; no vertex clipping.
	shapes := "shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence\none,38.72,-9.15,1\none,40.0,-9.0,2\none,41.0,-8.5,3\n"
	d, err = readGTFS(replaceGTFS(t, shapeArchive(t, false), map[string]string{"trips.txt": trips, "stop_times.txt": times, "shapes.txt": shapes}), p, "plan", "20260101", "20261231", hubBase, now)
	if err != nil || len(d.Shapes) != 1 || d.Shapes[0].Geometry[len(d.Shapes[0].Geometry)-1][1] != 41 {
		t.Fatal("service continuation clipped", err)
	}
}

func TestOptionalGeometryNeverSoftensArchiveStreamErrors(t *testing.T) {
	p, _ := providerByID("cp")
	now := time.Now()
	// Reserved DEFLATE block type is a decompressor error, not necessarily zip.ErrChecksum.
	deflated := shapeArchive(t, false)
	z, err := zip.NewReader(bytes.NewReader(deflated), int64(len(deflated)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range z.File {
		if f.Name == "shapes.txt" {
			offset, err := f.DataOffset()
			if err != nil {
				t.Fatal(err)
			}
			deflated[offset] = (deflated[offset] &^ 6) | 6
		}
	}
	if _, err = readGTFS(deflated, p, "plan", "20260101", "20261231", hubBase, now); err == nil {
		t.Fatal("corrupt deflate accepted as optional geometry")
	}
	unsupported := replaceGTFS(t, shapeArchive(t, false), nil)
	for start := 0; start < len(unsupported); {
		next := bytes.Index(unsupported[start:], []byte{'P', 'K', 1, 2})
		if next < 0 {
			break
		}
		index := start + next
		length := int(binary.LittleEndian.Uint16(unsupported[index+28 : index+30]))
		if string(unsupported[index+46:index+46+length]) == "shapes.txt" {
			binary.LittleEndian.PutUint16(unsupported[index+10:index+12], 65535)
			break
		}
		start = index + 46 + length
	}
	if _, err = readGTFS(unsupported, p, "plan", "20260101", "20261231", hubBase, now); err == nil {
		t.Fatal("unsupported ZIP algorithm accepted as optional geometry")
	}
}
