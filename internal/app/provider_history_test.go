package app

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/patterns"
)

// A failed receipt must break continuity before a later already-queued receipt.
func TestProviderHistoryWorkerMarksQueuedReceiptAfterWriteFailure(t *testing.T) {
	service, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	before := now.Add(-40 * time.Second)
	after := now.Add(-5 * time.Second)
	if err := service.RecordProvider(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: before}); err != nil {
		t.Fatal(err)
	}
	baseline, err := service.ProviderView(context.Background(), "cm", "", "")
	if err != nil {
		t.Fatal(err)
	}
	f := &Fetcher{Patterns: service, history: make(chan patterns.ProviderReceipt, 2), historyGaps: map[string]bool{}, publicationState: publicationState{Log: zap.NewNop()}}
	// Both receipts enter the queue before the worker starts. The first exceeds
	// the provider input bound and fails before the service can reset continuity.
	f.history <- patterns.ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(-20 * time.Second), Rows: make([]patterns.Observation, 20001)}
	f.history <- patterns.ProviderReceipt{Operator: "cm", ReceivedAt: after}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.historyLoop(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.After(10 * time.Second)
	for {
		view, err := service.ProviderView(context.Background(), "cm", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if view.AsOf != nil && view.AsOf.Equal(after) {
			if view.Gaps <= baseline.Gaps {
				t.Fatalf("queued receipt after failed write did not break continuity: gaps before=%d after=%d", baseline.Gaps, view.Gaps)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("worker did not process the later receipt")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// A loss belongs to its operator and remains pending through failed retries,
// even when the next accepted receipt is a partial prediction publication.
func TestProviderHistoryWorkerRetriesGapAcrossOperatorsAndPartialReceipt(t *testing.T) {
	service, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.ConfigureOperators([]string{"metro", "cm", "carris"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	before := now.Add(-40 * time.Second)
	for _, operator := range []string{"cm", "carris"} {
		if err := service.RecordProvider(patterns.ProviderReceipt{Operator: operator, ReceivedAt: before}); err != nil {
			t.Fatal(err)
		}
	}
	cmBefore := providerHistoryView(t, service, "cm").Gaps
	carrisBefore := providerHistoryView(t, service, "carris").Gaps
	carrisAfter, cmAfter := now.Add(-10*time.Second), now.Add(-5*time.Second)
	f := &Fetcher{Patterns: service, history: make(chan patterns.ProviderReceipt, 4), historyGaps: map[string]bool{}, publicationState: publicationState{Log: zap.NewNop()}}
	f.history <- patterns.ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(-25 * time.Second), Rows: make([]patterns.Observation, 20001)}
	f.history <- patterns.ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(-20 * time.Second), Rows: make([]patterns.Observation, 20001)}
	f.history <- patterns.ProviderReceipt{Operator: "carris", ReceivedAt: carrisAfter}
	f.history <- patterns.ProviderReceipt{Operator: "cm", ReceivedAt: cmAfter, Partial: true, PredictionStop: "cm:stop"}
	stop := startProviderHistoryWorker(f)
	defer stop()
	cm := awaitProviderHistory(t, service, "cm", func(v patterns.View) bool { return v.AsOf != nil && v.AsOf.Equal(cmAfter) })
	carris := providerHistoryView(t, service, "carris")
	if cm.Gaps <= cmBefore {
		t.Fatalf("failed retry or other operator consumed cm barrier: %d -> %d", cmBefore, cm.Gaps)
	}
	if carris.AsOf == nil || !carris.AsOf.Equal(carrisAfter) || carris.Gaps != carrisBefore {
		t.Fatalf("cm failure changed carris continuity: before=%d view=%+v", carrisBefore, carris)
	}
}

// A full queue drops a later receipt; queued earlier work must precede its gap.
func TestProviderHistoryWorkerKeepsOverflowGapAfterOlderReceipts(t *testing.T) {
	service, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordProvider(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: time.Now().UTC().Add(-40 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	baseline := providerHistoryView(t, service, "cm").Gaps
	f := &Fetcher{Patterns: service, history: make(chan patterns.ProviderReceipt, 1), historyGaps: map[string]bool{}, publicationState: publicationState{Log: zap.NewNop()}}
	f.enqueueProviderHistory(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: time.Now().UTC()})
	older := <-f.history
	f.history <- older
	f.enqueueProviderHistory(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: time.Now().UTC()}) // dropped
	stop := startProviderHistoryWorker(f)
	defer stop()
	view := awaitProviderHistory(t, service, "cm", func(v patterns.View) bool { return v.AsOf != nil && v.AsOf.Equal(older.ReceivedAt) })
	if view.Gaps != baseline {
		t.Fatalf("overflow gap applied before older queued receipt: %d -> %d", baseline, view.Gaps)
	}
	f.enqueueProviderHistory(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: time.Now().UTC()})
	awaitProviderHistory(t, service, "cm", func(v patterns.View) bool { return v.Gaps > baseline })
}

func TestProviderHistorySynchronousHelperRetriesGap(t *testing.T) {
	service, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	f := &Fetcher{Patterns: service, publicationState: publicationState{Log: zap.NewNop()}}
	f.enqueueProviderHistory(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(-40 * time.Second)})
	baseline := providerHistoryView(t, service, "cm").Gaps
	f.enqueueProviderHistory(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(-20 * time.Second), Rows: make([]patterns.Observation, 20001)})
	f.enqueueProviderHistory(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(-10 * time.Second), Rows: make([]patterns.Observation, 20001)})
	f.enqueueProviderHistory(patterns.ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(-5 * time.Second), Partial: true, PredictionStop: "cm:stop"})
	if got := providerHistoryView(t, service, "cm").Gaps; got <= baseline {
		t.Fatalf("synchronous retry lost failure gap: %d -> %d", baseline, got)
	}
}

func startProviderHistoryWorker(f *Fetcher) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.historyLoop(ctx) }()
	return func() { cancel(); <-done }
}

func providerHistoryView(t *testing.T, service *patterns.Service, operator string) patterns.View {
	t.Helper()
	view, err := service.ProviderView(context.Background(), operator, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func awaitProviderHistory(t *testing.T, service *patterns.Service, operator string, ready func(patterns.View) bool) patterns.View {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		view := providerHistoryView(t, service, operator)
		if ready(view) {
			return view
		}
		select {
		case <-deadline:
			t.Fatalf("worker did not process %s receipt", operator)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
