package patterns

import (
	"fmt"
	"strings"
	"testing"
)

func TestAggregateBlobIgnoresRowOrder(t *testing.T) {
	rows := []Aggregate{
		{Date: "2026-09-29", Stop: "a", Hour: 1, Count: 2},
		{Date: "2026-09-29", Stop: "b", Hour: 1, Count: 3},
		{Date: "2026-09-29", Stop: "c", Hour: 2, Count: 4},
	}
	blob, err := verifiedAggregateBlob(rows)
	if err != nil {
		t.Fatalf("ordered rows failed: %v", err)
	}
	reversed := []Aggregate{rows[2], rows[1], rows[0]}
	if _, err := verifiedAggregateBlob(reversed); err != nil {
		t.Fatalf("reversed rows failed: %v", err)
	}
	decoded, err := readAggregates(blob)
	if err != nil || !sameAggregateMultiset(rows, decoded) {
		t.Fatalf("roundtrip mismatch: %v", err)
	}
}

// A checkpoint larger than one block must trim the oldest aggregate days instead of
// failing the flush and pausing the archive.
func TestCheckpointTrimKeepsFlushPublishable(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	days := []string{"2026-09-27", "2026-09-28", "2026-09-29"}
	for _, day := range days {
		for n := 0; n < 12000; n++ {
			s.engine.Aggregates[fmt.Sprintf("%s|%d", day, n)] = Aggregate{
				Date: day, Hour: int32(n % 24), Stop: fmt.Sprintf("stop-%d", n),
				Reference: strings.Repeat("x", 2000), Count: int64(n),
			}
		}
	}
	blob, err := s.encodeCheckpoint()
	if err != nil {
		t.Fatalf("checkpoint failed with oversized engine: %v", err)
	}
	if len(blob) > maxBlockBytes {
		t.Fatalf("checkpoint blob %d exceeds block limit", len(blob))
	}
	if !s.engine.Limited {
		t.Fatal("trim did not disclose the limited window")
	}
	for key, a := range s.engine.Aggregates {
		if a.Date == days[0] {
			t.Fatalf("oldest day not trimmed: %s", key)
		}
	}
	if len(s.engine.Aggregates) == 0 {
		t.Fatal("trim removed every aggregate day")
	}
	if !s.engine.ColdDays[days[0]] {
		t.Fatal("trimmed day not marked cold")
	}
}
