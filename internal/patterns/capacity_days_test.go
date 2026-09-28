package patterns

import (
	"testing"
	"time"
)

func capacityAggregate(at time.Time, stop string) Aggregate {
	a := baseAggregate(aggregateRequest{at, "cm:route", "0", stop, "visit:1", "fixture", "unknown", "receipts"}, DefaultConfig("synthetic"))
	a.Count, a.KnownAt = 1, at.UnixNano()
	return a
}

func TestCapacityKeepsPendingDayComplete(t *testing.T) {
	e := newEngine()
	e.Capacity = 1
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, lisbon)
	original := capacityAggregate(at, "cm:A")
	e.add(original)
	e.add(capacityAggregate(at.AddDate(0, 0, 1), "cm:B"))
	if len(e.Aggregates) != 1 || e.Aggregates[aggregateKey(original)].Count != 1 {
		t.Fatal("capacity discarded an unpublished complete day")
	}
}

func TestCapacityDoesNotReopenPartOfEvictedDay(t *testing.T) {
	e := newEngine()
	e.Capacity = 2
	at := time.Date(2026, 9, 27, 12, 0, 0, 0, lisbon)
	e.add(capacityAggregate(at, "cm:A"))
	e.add(capacityAggregate(at, "cm:B"))
	e.DirtyDays = map[string]bool{}
	e.add(capacityAggregate(at.AddDate(0, 0, 1), "cm:C"))
	e.add(capacityAggregate(at, "cm:D"))
	if e.DirtyDays[at.Format("2006-01-02")] {
		t.Fatal("capacity reopened an incomplete retained day for replacement")
	}
}

func TestCapacityKeepsPublishedDayWhileAssociationCanWithdrawIt(t *testing.T) {
	e := newEngine()
	e.Capacity = 1
	at := time.Date(2026, 9, 27, 23, 59, 30, 0, lisbon)
	original := capacityAggregate(at, "cm:A")
	e.add(original)
	e.DirtyDays = map[string]bool{}
	e.Groups["synthetic"] = &group{ID: "synthetic", Active: true, Signals: []signal{{L: at, U: at.Add(20 * time.Second)}}}
	e.add(capacityAggregate(at.Add(time.Minute), "cm:B"))
	if e.Aggregates[aggregateKey(original)].Count != 1 {
		t.Fatal("live association lost the complete day required for possible withdrawal")
	}
}
