package patterns

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestProviderStagesSamplingRecoveryAndSharedArchive(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Hour).Add(time.Minute)
	source := at.Add(-5 * time.Minute)
	receipt := ProviderReceipt{Operator: "cm", ReceivedAt: at, Rows: []Observation{{ID: "cm:1", ObservedAt: source, Route: "cm:r", Trip: "cm:t", Stop: "cm:s", PositionKind: "reported", Lat: 38.7, Lon: -9.1}}}
	if err = s.RecordProvider(receipt); err != nil {
		t.Fatal(err)
	}
	if len(s.index.Blocks) != 0 {
		t.Fatal("disabled operator collected")
	}
	if err = s.ConfigureOperators([]string{"metro", "carris"}); err == nil {
		t.Fatal("CM stage skipped")
	}
	if err = s.ConfigureOperators(operatorOrder); err != nil {
		t.Fatal(err)
	}
	seedOperatorSamples(t, s, receipt, at)
	assertOperatorSamples(t, s, source)
	view, err := s.View(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Operators) != 8 || !view.Operators[1].Forecasts || view.Operators[1].PhysicalValidation || view.StorageBytes > c.LimitBytes {
		t.Fatal("capability or budget overclaim")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.ConfigureOperators(operatorOrder); err != nil {
		t.Fatal(err)
	}
	assertRecoveredOperatorCadence(t, s, receipt, at)

}

func TestGlobalFIFOIncludesOtherOperatorDetail(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	if err = s.RecordProvider(ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(-2 * time.Hour), Rows: []Observation{}}); err != nil {
		t.Fatal(err)
	}
	at := now.Add(-time.Hour)
	if err = s.Record(testReceipt(at, testRow("A", "x", at, 30)), testTopology()); err != nil {
		t.Fatal(err)
	}
	used, err := s.allocated()
	if err != nil {
		t.Fatal(err)
	}
	// Reserve one byte beyond remaining headroom: the oldest closed data block
	// must retire globally, independent of operator/type.
	if err = s.ensureSpace(c.LimitBytes-used+1, now); err != nil {
		t.Fatal(err)
	}
	metroDetail := false
	for _, b := range s.index.Blocks {
		if b.Operator == "cm" {
			t.Fatal("older provider detail escaped global FIFO")
		}
		if b.Kind == "detail" && b.Operator == "metro" {
			metroDetail = true
		}
	}
	if !metroDetail {
		t.Fatal("newer Metro detail removed before older provider detail")
	}
}

func seedOperatorSamples(t *testing.T, s *Service, receipt ProviderReceipt, at time.Time) {
	t.Helper()
	var err error
	for _, id := range operatorOrder[1:] {
		receipt.Operator = id
		receipt.Rows[0].ID = id + ":1"
		if err = s.RecordProvider(receipt); err != nil {
			t.Fatal(err)
		}
		receipt.ReceivedAt = at.Add(5 * time.Second)
		if err = s.RecordProvider(receipt); err != nil {
			t.Fatal(err)
		}
		receipt.ReceivedAt = at
	}
}

func assertOperatorSamples(t *testing.T, s *Service, source time.Time) {
	t.Helper()
	blocks := 0
	for _, b := range s.index.Blocks {
		if b.Kind == "observations" {
			blocks++
		}
	}
	if blocks != 7 {
		t.Fatal("sampling did not bound provider collection")
	}
	for _, b := range s.index.Blocks {
		if b.Kind != "observations" {
			continue
		}
		blob, err := s.readBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := s.decoder.DecodeAll(blob, nil)
		if err != nil {
			t.Fatal(err)
		}
		var actual ProviderReceipt
		if json.Unmarshal(raw, &actual) != nil || actual.Rows[0].ObservedAt != source {
			t.Fatal("receipt renewed source clock")
		}
	}
}

func assertRecoveredOperatorCadence(t *testing.T, s *Service, receipt ProviderReceipt, at time.Time) {
	t.Helper()
	var err error
	receipt.Operator = "cm"
	receipt.ReceivedAt = at.Add(10 * time.Second)
	if err = s.RecordProvider(receipt); err != nil {
		t.Fatal(err)
	}
	if s.operatorHistory()[1].Samples != 1 {
		t.Fatal("restart lost sampling clock")
	}
	receipt.ReceivedAt = at.Add(30 * time.Second)
	receipt.Error = "upstream_refresh_failed"
	receipt.Rows = nil
	if err = s.RecordProvider(receipt); err != nil {
		t.Fatal(err)
	}
	if s.operatorHistory()[1].Samples != 2 {
		t.Fatal("source failure not retained")
	}
	receipt.ReceivedAt = at.Add(31 * time.Second)
	receipt.Gap = true
	if err = s.RecordProvider(receipt); err != nil {
		t.Fatal(err)
	}
	if s.operatorHistory()[1].Samples != 3 {
		t.Fatal("between-sample collection gap discarded")
	}
	receipt.ReceivedAt = at.Add(30 * time.Second)
	if err = s.RecordProvider(receipt); err == nil {
		t.Fatal("gap allowed receipt clock regression")
	}
}
