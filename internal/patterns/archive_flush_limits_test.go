package patterns

import (
	"fmt"
	"strings"
	"testing"
	"time"
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

func bigAggregateDay(date string, rows int, size int) map[string]Aggregate {
	out := map[string]Aggregate{}
	for n := 0; n < rows; n++ {
		out[fmt.Sprintf("%s|%d", date, n)] = Aggregate{Date: date, Hour: int32(n % 24), Stop: fmt.Sprintf("stop-%d", n), Reference: strings.Repeat("x", size), Count: int64(n)}
	}
	return out
}

// The trim must never drop the current day; it trims older days first and keeps
// collecting today.
func TestCheckpointTrimNeverDropsCurrentDay(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().In(lisbon)
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	for key, a := range bigAggregateDay(yesterday, 30000, 2000) {
		s.engine.Aggregates[key] = a
	}
	for key, a := range bigAggregateDay(today, 10000, 2000) {
		s.engine.Aggregates[key] = a
	}
	if _, err := s.encodeCheckpoint(); err != nil {
		t.Fatalf("checkpoint failed with a protected current day: %v", err)
	}
	todayRows := 0
	for _, a := range s.engine.Aggregates {
		if a.Date == today {
			todayRows++
		}
		if a.Date == yesterday {
			t.Fatal("older day was not trimmed before the current day")
		}
	}
	if todayRows == 0 {
		t.Fatal("current day was dropped")
	}
}

// A current day that cannot fit must fail loudly and keep its aggregates instead of
// silently emptying the engine.
func TestCheckpointFailsLoudForOversizedCurrentDay(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	today := time.Now().In(lisbon).Format("2006-01-02")
	yesterday := time.Now().In(lisbon).AddDate(0, 0, -1).Format("2006-01-02")
	for key, a := range bigAggregateDay(today, 40000, 2000) {
		s.engine.Aggregates[key] = a
	}
	for key, a := range bigAggregateDay(yesterday, 5000, 2000) {
		s.engine.Aggregates[key] = a
	}
	if _, err := s.encodeCheckpoint(); err == nil {
		t.Fatal("oversized current day did not fail loudly")
	}
	if len(s.engine.Aggregates) == 0 {
		t.Fatal("loud failure emptied the engine")
	}
}

// An emptied dirty day must not republish an empty replacement over its published file.
func TestEmptyAggregateDayIsNotRepublished(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	date := "2026-09-20"
	rows := []Aggregate{{Date: date, Stop: "a", Hour: 1, Count: 2}, {Date: date, Stop: "b", Hour: 2, Count: 3}}
	if err := s.flushMetroDay(date, rows, now); err != nil {
		t.Fatal(err)
	}
	published := len(s.index.Blocks)
	s.dropAggregateDay(date)
	s.engine.DirtyDays[date] = true
	if err := s.flushMetroDay(date, nil, now); err != nil {
		t.Fatal(err)
	}
	if len(s.index.Blocks) != published {
		t.Fatal("empty dirty day published a replacement block")
	}
}

func TestSameAggregateMultiset(t *testing.T) {
	a := Aggregate{Date: "2026-09-29", Stop: "a", Hour: 1, Count: 2}
	b := Aggregate{Date: "2026-09-29", Stop: "b", Hour: 1, Count: 3}
	if !sameAggregateMultiset(nil, nil) || !sameAggregateMultiset(nil, []Aggregate{}) {
		t.Fatal("nil and empty slices must match")
	}
	if !sameAggregateMultiset([]Aggregate{a, a}, []Aggregate{a, a}) {
		t.Fatal("duplicates must match")
	}
	if sameAggregateMultiset([]Aggregate{a}, []Aggregate{a, b}) {
		t.Fatal("extra decoded row accepted")
	}
	if sameAggregateMultiset([]Aggregate{a, b}, []Aggregate{a}) {
		t.Fatal("missing decoded row accepted")
	}
	if sameAggregateMultiset([]Aggregate{a, a}, []Aggregate{a, b}) {
		t.Fatal("mismatched duplicate counts accepted")
	}
	if _, err := verifiedAggregateBlob(nil); err != nil {
		t.Fatalf("empty roundtrip failed: %v", err)
	}
}
