package app

import (
	"context"
	"encoding/json"
	"fmt"
	"lisboapublica/internal/api"
	"os"
	"strings"
	"testing"
	"time"
)

// TestOfficialShapeCacheFootprint validates explicitly downloaded official fixtures without network reads.
func TestOfficialShapeCacheFootprint(t *testing.T) {
	manifest := os.Getenv("GTFS_GEOMETRY_FIXTURES")
	if manifest == "" {
		t.Skip("set GTFS_GEOMETRY_FIXTURES to a downloaded official manifest")
	}
	blob, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var feeds []struct {
		Operator, Agency, Plan, File string
		From, Until                  int
	}
	if err = json.Unmarshal(blob, &feeds); err != nil {
		t.Fatal(err)
	}
	var store *Store
	cache := NewCache()
	if dsn := os.Getenv("GEOMETRY_LOCAL_DATABASE_URL"); dsn != "" {
		store, err = OpenStore(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer store.DB.Close()
		if err = store.Restore(context.Background(), cache); err != nil {
			t.Fatal(err)
		}
	}
	combined := &StaticData{Updated: time.Now().UTC(), Models: map[string]Metadata{}}
	for _, feed := range feeds {
		archive, err := os.ReadFile(feed.File)
		if err != nil {
			t.Fatal(err)
		}
		id := feed.Operator
		if strings.HasPrefix(id, "cm") {
			id = "cm"
		}
		p, _ := providerByID(id)
		var data *StaticData
		if id == "cm" {
			network, err := readCMNetwork(archive, &hubPlan{ID: feed.Plan, Agency: feed.Agency}, p, hubBase, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			data = network
			combined.Shapes = append(combined.Shapes, network.Shapes...)
			for id, metadata := range network.Models {
				combined.Models[id] = metadata
			}
		} else {
			data, err = readGTFS(archive, p, feed.Plan, fmt.Sprint(feed.From), fmt.Sprint(feed.Until), hubBase, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
		}
		encoded, err := encodeCache(data)
		if err != nil {
			t.Fatal(err)
		}
		points := 0
		for _, shape := range data.Shapes {
			points += len(shape.Geometry)
		}
		t.Logf("%s variants=%d retained_points=%d metadata=%d gzip_bytes=%d reservation_bytes=%d", feed.Operator, len(data.Shapes), points, len(data.Models), len(encoded), int64(len(encoded))*storageWriteOverhead)
		if len(data.Shapes) == 0 || !geometryCacheFits(data, api.Operator{}) {
			t.Fatal("geometry cache exceeds admission limits")
		}
		if store != nil && id != "cm" {
			op := cache.operator(id)
			op.StaticStatus = "ok"
			op.StaticUpdatedAt = ptr(data.Updated)
			if err = store.Save(context.Background(), id, data, nil, op, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !geometryCacheFits(combined, api.Operator{}) {
		t.Fatal("combined CM exceeds admission limit")
	}
	encoded, _ := encodeCache(combined)
	t.Logf("combined_CM variants=%d gzip_bytes=%d reservation_bytes=%d", len(combined.Shapes), len(encoded), int64(len(encoded))*storageWriteOverhead)
	if store != nil {
		state, _ := cache.state("")
		old := state.Static["cm"]
		if old == nil {
			t.Fatal("local CM catalog missing")
		}
		copyData := *old
		copyData.Shapes = combined.Shapes
		copyData.GeometryUpdated = ptr(combined.Updated)
		copyData.GeometryError = nil
		op := cache.operator("cm")
		op.StaticUpdatedAt = ptr(combined.Updated)
		if err = store.Save(context.Background(), "cm", &copyData, nil, op, nil); err != nil {
			t.Fatal(err)
		}
	}
}
