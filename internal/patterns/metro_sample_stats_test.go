package patterns

import (
	"testing"
	"time"
)

// The diagnostic counters must reflect what one sample admitted, rejected and learned.
func TestMetroSampleStatsCountTransitionsAndRejections(t *testing.T) {
	base := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	config := DefaultConfig(t.TempDir())
	topology := testTopology()
	e := newEngine()

	e.step(testReceipt(base, testRow("A", "x", base, 600)), topology, config)
	first := e.LastMetroStats
	if first.Rows != 1 || first.Contexts != 1 || first.Admitted != 1 || first.Signals != 0 {
		t.Fatalf("first sample counters %+v", first)
	}

	at := base.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("A", "x", at, 0)), topology, config)
	second := e.LastMetroStats
	if second.Signals != 1 || second.GroupsCreated != 1 {
		t.Fatalf("transition counters %+v", second)
	}

	at = at.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("A", "x", at, 60), testRow("A", "x", at, 60)), topology, config)
	if e.LastMetroStats.RejectedDuplicate != 2 {
		t.Fatalf("duplicate counters %+v", e.LastMetroStats)
	}

	at = at.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("ZZ", "x", at, 60)), topology, config)
	if e.LastMetroStats.RejectedRoute != 1 {
		t.Fatalf("route counters %+v", e.LastMetroStats)
	}

	e.step(testReceipt(at.Add(30*time.Second), testRow("B", "y", base.Add(-10*time.Minute), 60)), topology, config)
	if e.LastMetroStats.RejectedClock != 1 {
		t.Fatalf("clock counters %+v", e.LastMetroStats)
	}
}

// A supported triple must be visible through the diagnostic active-group counter.
func TestMetroSampleStatsActiveGroups(t *testing.T) {
	base := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	config := DefaultConfig(t.TempDir())
	topology := testTopology()
	e := newEngine()
	created, signals := 0, 0
	for i := 0; i < 6; i++ {
		at := base.Add(time.Duration(i) * 30 * time.Second)
		rows := []Row{}
		for j, stop := range []string{"A", "B", "C", "D", "E", "F"} {
			n := 600
			if i >= 2*j+1 {
				n = 0
			}
			rows = append(rows, testRow(stop, "x", at, n))
		}
		e.step(testReceipt(at, rows...), topology, config)
		created += e.LastMetroStats.GroupsCreated
		signals += e.LastMetroStats.Signals
	}
	if created == 0 || signals == 0 {
		t.Fatalf("triple counters created=%d signals=%d", created, signals)
	}
	if e.LastMetroStats.ActiveGroups == 0 {
		t.Fatalf("no active group reported: %+v", e.LastMetroStats)
	}
}
