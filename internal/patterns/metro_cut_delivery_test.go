package patterns

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMetroCutRejectsOldClaimsAndFailsClosed(t *testing.T) {
	c := newMetroCutWith(func(p []byte) (int, error) { return len(p), nil })
	ready := c.Snapshot()
	if !ready.Valid || ready.Pending || !c.Claim(ready) {
		t.Fatal("fresh cut was not ready")
	}
	first, second := c.MarkPending(), c.MarkPending()
	if c.Acknowledge(first) || c.Claim(ready) || !c.Acknowledge(second) || c.Snapshot().Pending {
		t.Fatal("old or current cut acknowledgement was incorrect")
	}
	invalid := newMetroCutWith(func([]byte) (int, error) { return 0, errors.New("no entropy") })
	if invalid.Snapshot().Valid || invalid.MarkPending().Valid || invalid.Claim(ready) {
		t.Fatal("entropy failure authorized history")
	}
	c.state.Store((maxMetroCutRevision - 1) << 1)
	if c.MarkPending().Valid || c.Snapshot().Valid {
		t.Fatal("revision overflow did not remain invalid")
	}
}

func TestMetroFIFORespectsCadenceAndExclusiveOwnership(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	topology := testTopology()
	if _, err = s.RecordMetro(testReceipt(base), topology); err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireMetroFIFO()
	if err != nil {
		t.Fatal(err)
	}
	before := s.MetroCut().Snapshot()
	if _, err = s.RecordMetro(testReceipt(base.Add(time.Second)), topology); err == nil {
		t.Fatal("direct recording bypassed FIFO owner")
	}
	s.InterruptMetroContinuity()
	if s.MetroCut().Snapshot() != before || s.pendingMetroDeliveryGap || len(s.detail) != 1 {
		t.Fatal("rejected direct operation mutated delivery")
	}
	for i := 1; i <= 600; i++ {
		at := base.Add(time.Duration(i) * 500 * time.Millisecond)
		if i%10 == 0 {
			s.MetroCut().MarkPending()
			if err := lease.InterruptMetroContinuity(); err != nil {
				t.Fatal(err)
			}
		}
		result, err := lease.RecordMetro(testReceipt(at), topology)
		if err != nil {
			t.Fatal(err)
		}
		if i%60 != 0 {
			if result.Kind != MetroUnsampled || result.Appended {
				t.Fatalf("early full sample at %v: %+v", at.Sub(base), result)
			}
			continue
		}
		if result.Kind != MetroGapSampled || !result.Appended || !s.MetroCut().Acknowledge(result.Cut) {
			t.Fatalf("due gap sample was not acknowledged: %+v", result)
		}
	}
	if len(s.detail) != 11 {
		t.Fatalf("got %d full samples in five minutes", len(s.detail))
	}
	for _, r := range s.detail[1:] {
		if !r.DeliveryGap {
			t.Fatal("admitted sample lost pending cut")
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.RecordMetro(testReceipt(base.Add(6*time.Minute)), topology); err == nil {
		t.Fatal("stale FIFO lease recorded a sample")
	}
}

func TestMetroGapRejectsRegressingClockBeforeHourMutation(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Now().UTC().Truncate(time.Second)
	if err = s.Record(testReceipt(base), testTopology()); err != nil {
		t.Fatal(err)
	}
	s.InterruptMetroContinuity()
	beforeHour, beforeSample, beforeIndex := s.hour, s.lastSample, digest(s.index)
	for _, at := range []time.Time{base, base.Add(-2 * time.Hour)} {
		result, err := s.RecordMetro(testReceipt(at), testTopology())
		if err != nil || result.Kind != MetroUnsampled || result.Appended {
			t.Fatalf("bad regressing receipt outcome: %+v %v", result, err)
		}
		if !s.pendingMetroDeliveryGap || !s.hour.Equal(beforeHour) || !s.lastSample.Equal(beforeSample) || digest(s.index) != beforeIndex {
			t.Fatal("regressing input changed the pending barrier or archive")
		}
	}
	result, err := s.RecordMetro(testReceipt(base.Add(30*time.Second)), testTopology())
	if err != nil || result.Kind != MetroGapSampled || !result.Appended || !result.Acknowledged {
		t.Fatalf("strictly later barrier did not recover: %+v %v", result, err)
	}
}

func TestMetroRecordReportsPartialAppendAndOneFailureCut(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(testReceipt(base), testTopology()); err != nil {
		t.Fatal(err)
	}
	s.InterruptMetroContinuity()
	before := s.MetroCut().Snapshot()
	s.mu.Lock()
	s.manifestSync = func(string) error { return errors.New("injected sync failure") }
	s.mu.Unlock()
	result, err := s.RecordMetro(testReceipt(base.Add(61*time.Second)), testTopology())
	after := s.MetroCut().Snapshot()
	if err == nil || !result.Appended || result.Kind != MetroGapSampled || !result.FailureMarked || result.Acknowledged {
		t.Fatalf("partial append reported incorrectly: %+v %v", result, err)
	}
	if !after.Pending || after.Revision != before.Revision+1 || s.MetroCut().Acknowledge(result.Cut) {
		t.Fatalf("partial failure did not create one new pending cut: before=%+v after=%+v", before, after)
	}
	if len(s.detail) != 2 || !s.manifestUncertain {
		t.Fatal("partial publication discarded evidence or uncertainty")
	}
}

func TestMetroViewFencesPendingCutBeforeWorker(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	query := seedSupportedMetroForecast(t, s, now)
	official, lower, upper := now.Add(90*time.Second), now.Add(30*time.Second), now.Add(2*time.Minute)
	errorSeconds := 12.0
	s.mu.Lock()
	f := &s.engine.Live[0]
	f.SourceAt, f.OfficialAt = &now, &official
	f.LowerAt, f.UpperAt = &lower, &upper
	f.Components = []Component{{Origin: "A", Target: "B", OriginAt: now, Seconds: 60}}
	f.CalibrationSamples, f.Selected, f.Evaluated = 20, true, true
	f.ReferenceLower, f.ReferenceUpper, f.ErrorLower, f.ErrorUpper = &lower, &upper, &errorSeconds, &errorSeconds
	s.mu.Unlock()
	if len(s.MetroPopupForecasts(query)) != 1 {
		t.Fatal("supported fixture unavailable")
	}
	s.MetroCut().MarkPending()
	if len(s.MetroPopupForecasts(query)) != 0 {
		t.Fatal("popup escaped pending producer cut")
	}
	view, err := s.View(context.Background(), "A")
	if err != nil || view.Status != "paused" || len(view.Forecasts) != 1 || view.Forecasts[0].OwnAt != nil {
		t.Fatalf("view escaped pending producer cut: %+v %v", view, err)
	}
	fenced := view.Forecasts[0]
	if fenced.OfficialAt == nil || !fenced.OfficialAt.Equal(official) || fenced.SourceAt == nil || !fenced.SourceAt.Equal(now) {
		t.Fatal("view lost a valid independent official forecast")
	}
	assertInterruptedMetroForecast(t, fenced)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.engine.Live[0].Components) != 1 || s.engine.Live[0].CalibrationSamples != 20 || s.engine.Live[0].OwnAt == nil {
		t.Fatal("response fencing modified the retained engine forecast")
	}
}

func assertInterruptedMetroForecast(t *testing.T, f Forecast) {
	t.Helper()
	retained := []struct {
		field   string
		present bool
	}{
		{"lower", f.LowerAt != nil}, {"upper", f.UpperAt != nil},
		{"validity", f.OwnValidUntil != nil}, {"components", len(f.Components) != 0},
		{"calibration", f.CalibrationSamples != 0}, {"reference lower", f.ReferenceLower != nil},
		{"reference upper", f.ReferenceUpper != nil}, {"error lower", f.ErrorLower != nil},
		{"error upper", f.ErrorUpper != nil}, {"selected", f.Selected}, {"evaluated", f.Evaluated},
	}
	for _, field := range retained {
		if field.present {
			t.Errorf("pending cut retained %s", field.field)
		}
	}
	if f.Result != "lost_support" || f.Unavailable != "history_interrupted" {
		t.Fatalf("incorrect interrupted forecast status: %+v", f)
	}
}

func TestMetroErrorSampleCannotAcknowledgeItsCut(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(testReceipt(base), testTopology()); err != nil {
		t.Fatal(err)
	}
	receipt := testReceipt(base.Add(30 * time.Second))
	receipt.Error = "upstream unavailable"
	result, err := s.RecordMetro(receipt, testTopology())
	if err != nil || !result.Appended || result.Kind != MetroGapSampled || !result.FailureMarked || result.Acknowledged || !s.pendingMetroDeliveryGap || !s.MetroCut().Snapshot().Pending {
		t.Fatalf("error sample cleared delivery cut: %+v %v", result, err)
	}
	result, err = s.RecordMetro(testReceipt(base.Add(31*time.Second)), testTopology())
	if err != nil || result.Appended || !s.pendingMetroDeliveryGap {
		t.Fatal("early healthy input consumed the pending cut")
	}
	result, err = s.RecordMetro(testReceipt(base.Add(60*time.Second)), testTopology())
	if err != nil || !result.Appended || result.Kind != MetroGapSampled || !result.Acknowledged || s.pendingMetroDeliveryGap {
		t.Fatalf("healthy gap sample did not recover: %+v %v", result, err)
	}
}

func TestMetroCutConcurrentLossesHaveDistinctRevisions(t *testing.T) {
	const count = 128
	c := newMetroCut()
	results := make(chan MetroCutSnapshot, count)
	for i := 0; i < count; i++ {
		go func() { results <- c.MarkPending() }()
	}
	seen := make(map[uint64]bool, count)
	for i := 0; i < count; i++ {
		cut := <-results
		if !cut.Valid || !cut.Pending || seen[cut.Revision] {
			t.Fatalf("duplicate or invalid concurrent revision: %+v", cut)
		}
		seen[cut.Revision] = true
	}
	latest := c.Snapshot()
	if latest.Revision != count || !c.Acknowledge(latest) || c.Snapshot().Pending {
		t.Fatalf("concurrent losses did not converge: %+v", latest)
	}
}
