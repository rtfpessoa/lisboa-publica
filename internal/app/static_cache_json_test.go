package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"
	"time"

	"lisboapublica/internal/api"
)

func TestStaticCacheJSONCompatibilityAndBoundedWrites(t *testing.T) {
	now := time.Now().UTC()
	d := fixtureStatic("cp", now)
	d.PlanID, d.Source = "published", hubBase
	d.Models["train"] = Metadata{Model: "Published model", Plate: "Sample plate", Typology: "Published code", Propulsion: "Electric"}
	d.GeometryUpdated, d.GeometryError, d.GeometryPartial = &now, ptr("Partial coverage"), true
	d.Shapes = []api.RouteShape{{Id: "cp:shape", OperatorId: "cp", RouteId: "cp:1", ShapeId: "shape", PlanId: "published", SourceUrl: hubBase, Geometry: [][]float64{{-9.15, 38.72}, {-9.14, 38.73}}, UpdatedAt: now}}
	d.CMPaths = []CMPath{{ID: "cm:[plan][agency]pattern", Line: "cm:1001", Shape: "cm:shape", Visits: []CMPathVisit{{Stop: "s", Sequence: 1}}}}
	d.CMPathError = ptr("Partial path coverage")
	d.Routes[0].Geometry = &d.Shapes[0].Geometry
	trip := d.Schedule.Trips[0]
	d.Schedule.Trips = make([]ScheduledTrip, 2000)
	for i := range d.Schedule.Trips {
		d.Schedule.Trips[i] = trip
	}
	for _, data := range []*StaticData{nil, {}, d, {Routes: []api.RouteDetail{}, Stops: []api.Stop{}, Schedule: &Schedule{Trips: []ScheduledTrip{}}}} {
		original, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		writer := &measuredJSONWriter{}
		if err = writeStaticCacheJSON(writer, data); err != nil {
			t.Fatal(err)
		}
		var want, got any
		if err = json.Unmarshal(original, &want); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(writer.Bytes(), &got); err != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("cache JSON changed: %v", err)
		}
		if data == d && writer.maximum >= len(original)/10 {
			t.Fatal("buffered complete timetable")
		}
		blob, err := encodeCache(data)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(blob))
		if err != nil {
			t.Fatal(err)
		}
		var restored *StaticData
		var legacyRestored *StaticData
		if err = json.Unmarshal(original, &legacyRestored); err != nil {
			t.Fatal(err)
		}
		if data == nil {
			err = json.NewDecoder(reader).Decode(&restored)
		} else {
			restored = &StaticData{}
			err = decodeStaticCacheJSON(reader, restored)
		}
		reader.Close()
		if err != nil || !reflect.DeepEqual(legacyRestored, restored) {
			t.Fatalf("durable cache roundtrip changed data: %v", err)
		}
	}
}

type measuredJSONWriter struct {
	bytes.Buffer
	maximum int
}

func (w *measuredJSONWriter) Write(p []byte) (int, error) {
	if len(p) > w.maximum {
		w.maximum = len(p)
	}
	return w.Buffer.Write(p)
}

type failingJSONWriter struct{ remaining int }

func (w *failingJSONWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, io.ErrClosedPipe
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestStaticCacheJSONPropagatesFailures(t *testing.T) {
	d := fixtureStatic("cp", time.Now().UTC())
	for _, allowance := range []int{0, 10, 100, 300, 500} {
		if err := writeStaticCacheJSON(&failingJSONWriter{remaining: allowance}, d); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("writer failure swallowed at %d: %v", allowance, err)
		}
	}
	d.CMPaths = []CMPath{{ID: "cm:[plan][agency]pattern", Line: "cm:1001", Shape: "cm:shape", Visits: []CMPathVisit{{Stop: "s", Sequence: 1}}}}
	d.CMPathError = ptr("Partial path coverage")
	d.Routes[0].Geometry = ptr([][]float64{{math.NaN(), 38.72}})
	if _, err := encodeCache(d); err == nil {
		t.Fatal("invalid JSON value encoded")
	}
}
