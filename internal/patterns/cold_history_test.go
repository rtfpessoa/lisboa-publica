package patterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestProviderColdDaySurvivesCapacityLimitedRecovery(t *testing.T) {
	for _, covered := range []bool{true, false} {
		t.Run(fmt.Sprintf("covered=%t", covered), func(t *testing.T) { checkCapacityLimitedColdDay(t, covered) })
	}
}

func checkCapacityLimitedColdDay(t *testing.T, covered bool) {
	t.Helper()
	c := DefaultConfig(t.TempDir())
	seedCapacityLimitedProviderHistory(t, c, covered)
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oldDate := time.Now().In(lisbon).AddDate(0, 0, -1).Format("2006-01-02")
	assertRetainedColdDay(t, s, oldDate)
}

func assertRetainedColdDay(t *testing.T, s *Service, date string) {
	t.Helper()
	for _, b := range s.index.Blocks {
		if b.Operator != "cm" || b.Kind != "aggregate" || b.Date != date {
			continue
		}
		blob, err := s.readBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := readAggregates(blob)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Count != 1 {
			t.Fatalf("capacity-limited replay withdrew a complete retained day: %+v", rows)
		}
		return
	}
	t.Fatal("retained historical day disappeared")
}

func seedCapacityLimitedProviderHistory(t *testing.T, c Config, covered bool) {
	t.Helper()
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	local := time.Now().In(lisbon)
	recent := local.Truncate(time.Minute)
	old := time.Date(local.Year(), local.Month(), local.Day()-1, 12, 0, 0, 0, lisbon)
	priorAt := old.Add(-2 * time.Minute)
	path := syntheticProviderPath("cm")
	prior := baseAggregate(aggregateRequest{priorAt, path.Route, path.Direction, "cm:A", "visit:1", providerProfile(path, c), "unknown", "receipts"}, c)
	prior.Count, prior.KnownAt = 1, priorAt.UnixNano()
	oldAsOf := old.Add(time.Minute)
	if !covered {
		oldAsOf = old.Add(-time.Minute)
	}
	writeColdAggregateFixture(t, s, oldAsOf, []Aggregate{prior})
	rows := make([]Aggregate, maxProviderAggregates)
	for i := range rows {
		rows[i] = baseAggregate(aggregateRequest{recent, "cm:retained", "0", fmt.Sprintf("cm:retained-%d", i), "visit:1", "retained-fixture", "unknown", "receipts"}, c)
		rows[i].Count, rows[i].KnownAt = 1, recent.UnixNano()
	}
	writeColdAggregateFixture(t, s, recent, rows)
	var raw bytes.Buffer
	receipt := syntheticProviderReceipt("cm", "historical", path, old, 0, false)
	if err = json.NewEncoder(&raw).Encode(receipt); err != nil {
		t.Fatal(err)
	}
	b := block{Key: "cm:observations:" + old.Format(time.RFC3339), Kind: "observations", Operator: "cm", Age: old.Truncate(time.Hour), AsOf: old, Samples: 1}
	writeColdFixtureBlock(t, s, b, s.encoder.EncodeAll(raw.Bytes(), nil))
}

func writeColdAggregateFixture(t *testing.T, s *Service, at time.Time, rows []Aggregate) {
	t.Helper()
	blob, err := verifiedAggregateBlob(rows)
	if err != nil {
		t.Fatal(err)
	}
	date := at.In(lisbon).Format("2006-01-02")
	age, err := time.ParseInLocation("2006-01-02", date, lisbon)
	if err != nil {
		t.Fatal(err)
	}
	b := block{Key: "cm:aggregate:" + date, Kind: "aggregate", Operator: "cm", Date: date, Age: age.UTC(), AsOf: at}
	writeColdFixtureBlock(t, s, b, blob)
}

func writeColdFixtureBlock(t *testing.T, s *Service, b block, blob []byte) {
	t.Helper()
	// Synthetic verified generations represent retained days beyond the hot budget.
	b = namedGeneration(b, blob)
	if err := atomicFile(s.config.Directory, b.File, blob); err != nil {
		t.Fatal(err)
	}
	next := manifest{Version: 1, Blocks: append(append([]block{}, s.index.Blocks...), b)}
	if err := s.saveManifest(next); err != nil {
		t.Fatal(err)
	}
}
