package patterns

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveRoundtripRestartAndOwnership(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Open(config); err == nil {
		other.Close()
		t.Fatal("two archive writers admitted")
	}
	base := time.Now().UTC().Truncate(time.Minute)
	raw := json.RawMessage(`[{"unknown_field":{"nested":[1,"value"]}}]`)
	r := testReceipt(base, testRow("A", "x", base, 100))
	r.Raw = raw
	if err = s.Record(r, testTopology()); err != nil {
		t.Fatal(err)
	}
	count := len(s.detail)
	if err = s.Record(r, testTopology()); err != nil {
		t.Fatal(err)
	}
	if len(s.detail) != count {
		t.Fatal("duplicate receipt archived")
	}
	var detailBlock block
	for _, b := range s.index.Blocks {
		if b.Kind == "detail" {
			detailBlock = b
		}
	}
	blob, err := s.readBlock(detailBlock)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := s.decoder.DecodeAll(blob, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(decoded), `"unknown_field"`) {
		t.Fatal("unknown upstream field lost")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.detail) != 1 || len(s.engine.Groups) != 0 || len(s.engine.Previous) != 0 {
		t.Fatal("restart lost detail or invented continuity")
	}
	if s.lastSample != r.ReceivedAt {
		t.Fatal("lost receipt dedup clock")
	}
}

func TestSparseParquetAndCorruptionUnavailable(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	s.engine.Profile = "p"
	a := baseAggregate(aggregateRequest{now, "line", "d", "A", "1", "p", "reported_normal", "signals"}, s.config)
	a.Count = 1
	a.KnownAt = now.UnixNano()
	s.engine.add(a)
	if err = s.flush(now); err != nil {
		t.Fatal(err)
	}
	view, err := s.View(context.Background(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Patterns) != 1 || view.Patterns[0].Signals != 1 || view.Patterns[0].Probability != nil || view.DwellSeconds != nil || view.SpeedKmh != nil {
		t.Fatalf("wrong sparse/null semantics: %+v", view)
	}
	for _, b := range s.index.Blocks {
		if b.Kind == "aggregate" {
			if err = os.WriteFile(filepath.Join(s.config.Directory, b.File), []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	view, err = s.View(context.Background(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "degraded" || len(view.Patterns) != 0 {
		t.Fatal("corrupt file became a zero pattern")
	}
}

func TestGlobalFIFOAccountsAllocatedSpaceAndOrphans(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.LimitBytes = 8 << 20
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	old := block{Key: "old", Kind: "aggregate", Operator: "metro", Date: now.In(lisbon).AddDate(0, 0, -2).Format("2006-01-02"), Age: now.Add(-48 * time.Hour), Closed: true}
	newer := block{Key: "newer", Kind: "detail", Operator: "metro", Age: now.Add(-time.Hour), Closed: true}
	payload := make([]byte, 2<<20)
	for i := range payload {
		payload[i] = byte(i)
	}
	s.engine.DirtyDays[old.Date] = true
	if err = s.publish(old, payload, now); err != nil {
		t.Fatal(err)
	}
	payload[0] = 77
	if err = s.publish(newer, payload, now); err != nil {
		t.Fatal(err)
	}
	if err = s.ensureSpace(4<<20, now); err != nil {
		t.Fatal(err)
	}
	for _, b := range s.index.Blocks {
		if b.Key == "old" {
			t.Fatal("FIFO kept older aggregate ahead of newer detail")
		}
	}
	if _, err = os.Stat(filepath.Join(config.Directory, "metro-aggregate-"+digest(old.Key)[:16]+"-"+digestBytes(append([]byte{0}, payload[1:]...))+".parquet")); !os.IsNotExist(err) {
		t.Fatal("retired bytes still on disk")
	}
	orphan := filepath.Join(config.Directory, "metro-unpublished.tmp")
	if err = os.WriteFile(orphan, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcile(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("orphan not reconciled")
	}
	allocated, err := s.allocated()
	if err != nil || allocated+4<<20 > config.LimitBytes {
		t.Fatalf("unsafe allocation %d %v", allocated, err)
	}
}

func TestPublicationFailureKeepsPreviousManifest(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	b := block{Key: "fixture", Kind: "detail", Operator: "metro", Age: now}
	if err = s.publish(b, []byte("old"), now); err != nil {
		t.Fatal(err)
	}
	old := s.index.Blocks[0]
	// A nonempty temporary directory makes atomic manifest publication fail after
	// the replacement generation is complete. The previous index remains active.
	path := filepath.Join(s.config.Directory, ".manifest.tmp")
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err = s.publish(b, []byte("new"), now); err == nil {
		t.Fatal("expected publication failure")
	}
	if s.index.Blocks[0].Hash != old.Hash {
		t.Fatal("published incomplete transaction")
	}
	if blob, err := s.readBlock(old); err != nil || string(blob) != "old" {
		t.Fatal("lost previous admitted generation")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcile(); err != nil {
		t.Fatal(err)
	}
}
