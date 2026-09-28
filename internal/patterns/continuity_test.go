package patterns

import (
	"testing"
	"time"
)

func TestKnownIntermediateLossNeverSurvivesSampling(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	topology := testTopology()
	e := newEngine()
	e.Profile = topology.Profile
	e.Condition = "reported_normal"
	base := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	g := &group{ID: "episode", Train: "x", Route: "line", Direction: "d", Active: true}
	key := groupKey(groupIdentity{Train: "x", Direction: "d", Route: "line"})
	e.Groups[key] = g
	e.observeIntermediate(testReceipt(base.Add(5*time.Second)), topology, c)
	if len(e.Groups) != 0 {
		t.Fatal("known intermediate absence retained support")
	}
	e.observeIntermediate(testReceipt(base.Add(10*time.Second), testRow("A", "x", base.Add(10*time.Second), 0)), topology, c)
	if len(e.Groups) != 0 {
		t.Fatal("return before next archive sample restored support")
	}
	if len(e.Aggregates) != 0 {
		t.Fatal("intermediate check created training samples")
	}
}
