package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSnapshotArchiveLosslessAndBounds(t *testing.T) {
	raw := []byte(`{"source_id":"7","model":null,"license_plate":"","typology":["legacy"],"other":{"keep":1}}`)
	projection, archive, err := archiveSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSnapshotArchive(archive)
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatalf("lossless decode failed: %v", err)
	}
	var projected map[string]json.RawMessage
	_ = json.Unmarshal(projection, &projected)
	if string(projected["model"]) != "null" || string(projected["license_plate"]) != `""` || string(projected["typology"]) != `["legacy"]` || projected["propulsion"] != nil || projected["other"] != nil {
		t.Fatal("original query values changed")
	}
	for _, value := range [][]byte{nil, []byte("bad"), append(append([]byte{}, archive...), 1), archive[:len(archive)-1]} {
		if _, err = decodeSnapshotArchive(value); err == nil {
			t.Fatal("invalid archive admitted")
		}
	}
	var oversized bytes.Buffer
	oversized.WriteString(snapshotArchiveVersion)
	writer := gzip.NewWriter(&oversized)
	_, _ = writer.Write([]byte(`"` + strings.Repeat("x", snapshotArchiveLimit) + `"`))
	_ = writer.Close()
	if _, err = decodeSnapshotArchive(oversized.Bytes()); err == nil {
		t.Fatal("expanded archive limit ignored")
	}
}

func archiveLegacyJSON(t *testing.T) []byte {
	t.Helper()
	values := map[string]any{"source_id": "published", "model": "Model", "license_plate": nil, "typology": "", "propulsion": "raw", "seated_capacity": 0, "wheelchair_accessible": false}
	for n := 0; n < 1000; n++ {
		values[fmt.Sprintf("unconsumed_published_field_%04d", n)] = fmt.Sprintf("value_%08d", n*7919)
	}
	raw, err := json.Marshal(values)
	if err != nil || len(raw) > snapshotArchiveLimit {
		t.Fatalf("invalid legacy fixture %d %v", len(raw), err)
	}
	return raw
}
func seedArchiveRow(t *testing.T, s *Store, id string, at time.Time, payload []byte) {
	t.Helper()
	_, err := s.DB.Exec(context.Background(), `INSERT INTO snapshots(operator_id,vehicle_id,observed_at,generation,route_id,trip_id,position_kind,lat,lon,speed_kmh,distance_km,payload,first_observed_at,speed_sample_count) VALUES('carris',$1,$2,1,'carris:1','carris:T','reported',38.72,-9.15,12,1.5,$3,$2,3)`, id, at, payload)
	if err != nil {
		t.Fatal(err)
	}
}
func archiveQueryResults(t *testing.T, s *Store, f Filter) []any {
	t.Helper()
	ctx := context.Background()
	metrics, e := s.readMetrics(ctx, f, 1)
	if e != nil {
		t.Fatal(e)
	}
	history, e := s.readHistory(ctx, f, 1)
	if e != nil {
		t.Fatal(e)
	}
	fleet, e := s.readFleet(ctx, f, 1)
	if e != nil {
		t.Fatal(e)
	}
	traffic, e := s.readTraffic(ctx, f, 1)
	if e != nil {
		t.Fatal(e)
	}
	ranking, e := s.readRankings(ctx, f, 1)
	if e != nil {
		t.Fatal(e)
	}
	coverage, e := s.operatorCoverage(ctx, f, 1)
	if e != nil {
		t.Fatal(e)
	}
	return []any{metrics, history, fleet, traffic, ranking, coverage}
}
func TestSnapshotCompactionHistoricalEquivalenceAndNativeSavings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedArchiveRow(t, s, "large", now.Add(-48*time.Hour), archiveLegacyJSON(t))
	seedArchiveRow(t, s, "native", now.Add(-47*time.Hour), []byte(`{"source_id":"native","other":"`+strings.Repeat("a", 30000)+`"}`))
	seedArchiveRow(t, s, "small", now.Add(-46*time.Hour), []byte(`{"source_id":"small","model":null}`))
	seedArchiveRow(t, s, "hot", now.Add(-time.Hour), archiveLegacyJSON(t))
	f := Filter{From: now.Add(-3 * 24 * time.Hour), To: now, HourEnd: 24}
	before := archiveQueryResults(t, s, f)
	var original string
	var oldStored int64
	if err := s.DB.QueryRow(ctx, "SELECT payload::TEXT,pg_column_size(payload) FROM snapshots WHERE vehicle_id='large'").Scan(&original, &oldStored); err != nil {
		t.Fatal(err)
	}
	if err := s.compactSnapshots(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, archiveQueryResults(t, s, f)) {
		t.Fatal("analytical/coverage/baseline fleet queries changed")
	}
	supported, err := s.supportsSnapshotCompaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var archive []byte
	var newStored int64
	if err = s.DB.QueryRow(ctx, "SELECT payload_archive,pg_column_size(payload)+COALESCE(pg_column_size(payload_archive),0) FROM snapshots WHERE vehicle_id='large'").Scan(&archive, &newStored); err != nil {
		t.Fatal(err)
	}
	if !supported {
		if archive != nil || oldStored != newStored {
			t.Fatal("unproven native storage rewritten")
		}
		return
	}
	if archive == nil {
		t.Fatal("beneficial legacy payload not compressed")
	}
	restored, err := decodeSnapshotArchive(archive)
	if err != nil || string(restored) != original {
		t.Fatal("complete legacy payload not retained")
	}
	if newStored+snapshotArchiveRowOverhead+snapshotArchiveMinimumSaving > oldStored {
		t.Fatal("native savings criterion violated")
	}
	t.Logf("legacy JSON text=%d stored_before=%d stored_after=%d savings=%d", len(original), oldStored, newStored, oldStored-newStored)
	var unwanted int
	if err = s.DB.QueryRow(ctx, "SELECT count(*) FROM snapshots WHERE vehicle_id!='large' AND payload_archive IS NOT NULL").Scan(&unwanted); err != nil || unwanted != 0 {
		t.Fatal("tiny/hot/already native-compressed payload rewritten")
	}
	if err = s.compactSnapshots(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, archiveQueryResults(t, s, f)) {
		t.Fatal("repeat compaction changed results")
	}
}

func TestSnapshotCompactionScanProgressAndConditionalWrite(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	supported, err := s.supportsSnapshotCompaction(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Skip("native encoded size cannot prove physical Cockroach savings")
	}
	base := time.Now().UTC().Add(-48 * time.Hour)
	for n := 0; n < snapshotArchiveBatch; n++ {
		seedArchiveRow(t, s, fmt.Sprint(n), base.Add(time.Duration(n)*time.Second), []byte(`{"source_id":"small"}`))
	}
	seedArchiveRow(t, s, "oversized", base.Add(snapshotArchiveBatch*time.Second), []byte(`{"other":"`+strings.Repeat("x", snapshotArchiveLimit+1)+`"}`))
	seedArchiveRow(t, s, "later", base.Add((snapshotArchiveBatch+1)*time.Second), archiveLegacyJSON(t))
	if err = s.compactSnapshots(ctx); err != nil {
		t.Fatal(err)
	}
	if s.archiveCursor == nil {
		t.Fatal("skipped first batch did not advance")
	}
	if err = s.compactSnapshots(ctx); err != nil {
		t.Fatal(err)
	}
	var archive []byte
	if err = s.DB.QueryRow(ctx, "SELECT payload_archive FROM snapshots WHERE vehicle_id='later'").Scan(&archive); err != nil || archive == nil {
		t.Fatal("skipped/oversized rows starved later legacy payload")
	}
	if s.archiveCursor != nil {
		t.Fatal("end of sweep did not wrap")
	}
	candidate := snapshotArchiveCandidate{Key: snapshotArchiveKey{Operator: "carris", Vehicle: "0", At: base}, Stored: 10000, Raw: ptr(`{"source_id":"wrong-version"}`)}
	projected, compressed, _ := archiveSnapshot([]byte(*candidate.Raw))
	if err = s.writeArchives(ctx, []snapshotArchiveUpdate{{Candidate: candidate, Projection: projected, Archive: compressed}}); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow(ctx, "SELECT payload_archive FROM snapshots WHERE vehicle_id='0'").Scan(&archive); err != nil || archive != nil {
		t.Fatal("conditional write archived a different payload version")
	}
}

func TestSnapshotCompactionGuardAndCancellation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	seedArchiveRow(t, s, "large", now.Add(-48*time.Hour), archiveLegacyJSON(t))
	s.budget = &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return operationalDatabaseBytes - 1, nil }, state: "unavailable"}
	if err := s.compactSnapshots(ctx); err != nil {
		t.Fatal(err)
	}
	var archived int
	if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM snapshots WHERE payload_archive IS NOT NULL").Scan(&archived); err != nil || archived != 0 {
		t.Fatal("exhausted maintenance budget wrote history")
	}
	budget := &storageBudget{now: time.Now, measure: func(context.Context) (int64, error) { return 1, nil }, state: "unavailable"}
	admitted, err := budget.reserveOptional(ctx, maximumWriteBytes+1)
	if err != nil || admitted || budget.status() != "collecting" {
		t.Fatal("optional rejection falsely paused ingestion")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = s.compactSnapshots(canceled); err == nil {
		t.Fatal("canceled maintenance continued")
	}
}
