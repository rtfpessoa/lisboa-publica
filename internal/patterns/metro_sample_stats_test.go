package patterns

import (
	"bytes"
	"encoding/json"
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
	if first.Rows != 1 || first.Contexts != 1 || first.Admitted != 1 || first.FirstSlots != 1 ||
		first.WithPrior != 0 || first.ZeroETA != 0 || first.Signals != 0 || first.GroupsCreated != 0 ||
		first.GroupsDeleted != 0 || first.ActiveGroups != 0 || first.Gap {
		t.Fatalf("first sample counters %+v", first)
	}
	if !first.SampledAt.Equal(base.Add(time.Second)) {
		t.Fatalf("sampled_at %s", first.SampledAt)
	}

	at := base.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("A", "x", at, 0)), topology, config)
	second := e.LastMetroStats
	if second.Signals != 1 || second.SignalsApplied != 1 || second.GroupsCreated != 1 ||
		second.WithPrior != 1 || second.ZeroETA != 1 || second.ActiveGroups != 0 {
		t.Fatalf("transition counters %+v", second)
	}

	at = at.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("A", "x", at, 60), testRow("A", "x", at, 60)), topology, config)
	if e.LastMetroStats.RejectedDuplicate != 2 || e.LastMetroStats.GroupsDeleted != 1 {
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

// A context whose clock jumps beyond the continuity window is a continuity rejection,
// not a source-clock rejection.
func TestMetroSampleStatsContinuityRejection(t *testing.T) {
	base := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	config := DefaultConfig(t.TempDir())
	topology := testTopology()
	e := newEngine()
	e.step(testReceipt(base, testRow("A", "x", base, 600)), topology, config)
	at := base.Add(30 * time.Second)
	// Same source clock, different content: a continuity conflict, not a clock error.
	e.step(testReceipt(at, testRow("A", "x", base, 0)), topology, config)
	stats := e.LastMetroStats
	if stats.RejectedContinuity != 1 || stats.RejectedClock != 0 {
		t.Fatalf("continuity counters %+v", stats)
	}
}

// A group is deleted only when its train is absent from the sample, and only active
// groups are counted.
func TestMetroSampleStatsDeletionAndActive(t *testing.T) {
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
	if created == 0 || signals == 0 || e.LastMetroStats.ActiveGroups == 0 {
		t.Fatalf("triple counters created=%d signals=%d stats=%+v", created, signals, e.LastMetroStats)
	}
	active := e.LastMetroStats.ActiveGroups
	// A second, single-signal train creates an inactive group next to the active one.
	at := base.Add(6 * 30 * time.Second)
	e.step(testReceipt(at, testRow("F", "x", at, 600), testRow("A", "y", at, 600)), topology, config)
	at = at.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("F", "x", at, 600), testRow("A", "y", at, 0)), topology, config)
	stats := e.LastMetroStats
	if stats.GroupsCreated != 1 || stats.ActiveGroups != active {
		t.Fatalf("inactive group counted: %+v", stats)
	}
	// An absent-train sample deletes it again.
	e.step(testReceipt(at.Add(30*time.Second)), topology, config)
	if e.LastMetroStats.GroupsDeleted == 0 {
		t.Fatalf("deletion not counted: %+v", e.LastMetroStats)
	}
}

// The diagnostic counters must never be persisted with the checkpoint.
func TestMetroSampleStatsNotPersisted(t *testing.T) {
	base := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	config := DefaultConfig(t.TempDir())
	e := newEngine()
	e.step(testReceipt(base, testRow("A", "x", base, 600)), testTopology(), config)
	raw, err := json.Marshal(checkpoint{Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("LastMetroStats")) {
		t.Fatal("diagnostic counters persisted")
	}
	var restored checkpoint
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.Engine.LastMetroStats.SampledAt.IsZero() || restored.Engine.LastMetroStats.Rows != 0 {
		t.Fatalf("restored counters %+v", restored.Engine.LastMetroStats)
	}
}

// A failed upstream fetch must not wipe continuity or count as a gap: the interruption
// tells us nothing about the vehicles, and the next sample must still learn its signal.
func TestIntermediateErrorKeepsContinuity(t *testing.T) {
	base := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	config := DefaultConfig(t.TempDir())
	topology := testTopology()
	e := newEngine()
	e.step(testReceipt(base, testRow("A", "x", base, 600)), topology, config)
	gaps := e.Gaps
	failed := testReceipt(base.Add(10 * time.Second))
	failed.Error = "error"
	e.observeIntermediate(failed, topology, config)
	if len(e.Previous) == 0 || e.Gaps != gaps {
		t.Fatalf("failed fetch wiped continuity: previous=%d gaps=%d", len(e.Previous), e.Gaps-gaps)
	}
	at := base.Add(30 * time.Second)
	e.step(testReceipt(at, testRow("A", "x", at, 0)), topology, config)
	if e.LastMetroStats.Signals != 1 || e.LastMetroStats.GroupsCreated != 1 {
		t.Fatalf("signal lost after a failed fetch: %+v", e.LastMetroStats)
	}
	// The learned group survives a later failed observation as well.
	e.observeIntermediate(failed, topology, config)
	if len(e.Groups) != 1 {
		t.Fatalf("failed fetch cut the group: %d", len(e.Groups))
	}
}

// A sample recorded from a failed upstream fetch reports no data: it must not cut the
// learned groups nor count as a delivery gap.
func TestErrorSampleKeepsContinuity(t *testing.T) {
	base := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	config := DefaultConfig(t.TempDir())
	topology := testTopology()
	e := newEngine()
	healthy := testReceipt(base, testRow("A", "x", base, 600))
	healthy.RouteConditions = map[string]string{"line": "reported_normal"}
	e.step(healthy, topology, config)
	at := base.Add(10 * time.Second)
	failed := testReceipt(at)
	failed.Error, failed.RouteConditions = "error", map[string]string{}
	e.step(failed, topology, config)
	if len(e.Previous) == 0 || e.Gaps != 0 || e.LastMetroStats.Gap {
		t.Fatalf("error sample cut continuity: previous=%d gaps=%d stats=%+v", len(e.Previous), e.Gaps, e.LastMetroStats)
	}
	next := base.Add(30 * time.Second)
	e.step(testReceipt(next, testRow("A", "x", next, 0)), topology, config)
	if e.LastMetroStats.Signals != 1 {
		t.Fatalf("signal lost after an error sample: %+v", e.LastMetroStats)
	}
}
