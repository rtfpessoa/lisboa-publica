package app

import (
	"context"
	"encoding/json"
	"lisboapublica/internal/api"
	"os"
	"testing"
	"time"
)

// TestOfficialShapeCacheFootprint validates explicitly downloaded official fixtures without network reads.
func TestOfficialShapeCacheFootprint(t *testing.T) {
	feeds := officialGeometryFeeds(t)
	var store *Store
	cache := NewCache()
	if dsn := os.Getenv("GEOMETRY_LOCAL_DATABASE_URL"); dsn != "" {
		var err error
		store, err = OpenStore(context.Background(), dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer store.DB.Close()
		if err = store.Restore(context.Background(), cache); err != nil {
			t.Fatal(err)
		}
	}
	browserShapes := []api.RouteShape{}
	combined := &StaticData{Updated: time.Now().UTC(), Models: map[string]Metadata{}}
	for _, feed := range feeds {
		id := feed.providerID()
		data := loadOfficialGeometry(t, feed)
		if id == "cm" {
			mergeCMFixture(combined, data)
		}

		browserShapes = append(browserShapes, data.Shapes...)
		verifyOfficialGeometryAdmission(t, feed, data)
		if store != nil && id != "cm" {
			saveOfficialGeometry(t, store, cache, id, data)
		}

	}
	writeBrowserGeometryFixture(t, browserShapes)
	if !geometryCacheFits(combined, api.Operator{}) {
		t.Fatal("combined CM exceeds admission limit")
	}
	encoded, _ := encodeCache(combined)
	t.Logf("combined_CM variants=%d gzip_bytes=%d reservation_bytes=%d", len(combined.Shapes), len(encoded), int64(len(encoded))*storageWriteOverhead)
	if store != nil {
		saveCombinedCMGeometry(t, store, cache, combined)
	}
}

func saveCombinedCMGeometry(t *testing.T, store *Store, cache *Cache, combined *StaticData) {

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
	if err := store.Save(context.Background(), "cm", &copyData, nil, op, nil); err != nil {
		t.Fatal(err)
	}

}

func verifyOfficialGeometryAdmission(t *testing.T, feed officialGeometryFeed, data *StaticData) {
	t.Helper()
	encoded, err := encodeCache(data)
	if err != nil {
		t.Fatal(err)
	}
	points := 0
	covered := map[string]bool{}
	for _, shape := range data.Shapes {
		points += len(shape.Geometry)
		covered[shape.RouteId] = true
	}
	t.Logf("%s variants=%d retained_points=%d metadata=%d gzip_bytes=%d reservation_bytes=%d covered_routes=%d/%d status=%s", feed.Operator, len(data.Shapes), points, len(data.Models), len(encoded), int64(len(encoded))*storageWriteOverhead, len(covered), len(data.Routes), geometryCoverage(feed.providerID(), data).Status)
	if len(data.Shapes) == 0 || !geometryCacheFits(data, api.Operator{}) {
		t.Fatal("geometry cache exceeds admission limits")
	}
}

func saveOfficialGeometry(t *testing.T, store *Store, cache *Cache, id string, data *StaticData) {
	t.Helper()
	op := cache.operator(id)
	op.StaticStatus = "ok"
	op.StaticUpdatedAt = ptr(data.Updated)
	if err := store.Save(context.Background(), id, data, nil, op, nil); err != nil {
		t.Fatal(err)
	}
}

func writeBrowserGeometryFixture(t *testing.T, shapes []api.RouteShape) {
	t.Helper()
	target := os.Getenv("GEOMETRY_BROWSER_FIXTURE")
	if target == "" {
		return
	}
	blob, err := json.Marshal(shapes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, blob, 0600); err != nil {
		t.Fatal(err)
	}
}
