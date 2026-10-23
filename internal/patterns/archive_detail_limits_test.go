package patterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// sizedDetail constructs valid, ordered JSONL records of an exact total size.
func sizedDetail(t *testing.T, base time.Time, size int) []Receipt {
	t.Helper()
	rows := make([]Receipt, 9)
	remaining := size
	for i := range rows {
		r := Receipt{ReceivedAt: base.Add(time.Duration(i) * time.Second), Operator: "metro", Profile: "fixture", Raw: json.RawMessage(`""`)}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		target := 1_900_000
		if i == len(rows)-1 {
			target = remaining
		}
		if target < len(raw)+1 || target > 2<<20 {
			t.Fatalf("invalid synthetic target %d", target)
		}
		r.Raw = append(append([]byte{'"'}, bytes.Repeat([]byte{'x'}, target-len(raw)-1)...), '"')
		rows[i] = r
		remaining -= target
	}
	if remaining != 0 {
		t.Fatal("fixture size mismatch")
	}
	actual, err := detailSize(rows)
	if err != nil || actual != size {
		t.Fatalf("fixture bytes %d: %v", actual, err)
	}
	return rows
}

func installDetailFixture(t *testing.T, s *Service, base time.Time, rows []Receipt) {
	t.Helper()
	size, err := detailSize(rows)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hour, s.detail, s.detailBytes = base.UTC().Truncate(time.Hour), rows, size
	s.lastSample, s.lastCheckpoint = rows[len(rows)-1].ReceivedAt, rows[len(rows)-1].ReceivedAt
	s.engine.LastReceipt = s.lastSample
}

func TestDetailExactByteLimitAndHourRollover(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	topology := testTopology()
	formatted := topology
	formatted.Profile = fmt.Sprintf("%s-s%d-b%d", topology.Profile, int(config.SampleInterval/time.Second), config.BinSeconds)
	r := Receipt{ReceivedAt: base.Add(time.Minute), Operator: "metro", Profile: formatted.Profile, Topology: &formatted, DeliveryGap: true, Error: "synthetic gap"}
	raw, err := boundedMetroReceipt(r)
	if err != nil {
		t.Fatal(err)
	}
	rows := sizedDetail(t, base, maxDetailHourBytes-len(raw)-1)
	installDetailFixture(t, s, base, rows)
	r.Operator, r.Profile, r.Topology = "", "", nil
	if err = s.Record(r, topology); err != nil {
		t.Fatal(err)
	}
	if s.detailBytes != maxDetailHourBytes {
		t.Fatalf("got %d bytes", s.detailBytes)
	}
	for i := 0; i < 2; i++ {
		r.ReceivedAt = base.Add(time.Duration(2+i) * time.Minute)
		if err = s.Record(r, topology); err == nil {
			t.Fatal("accepted an over-limit record")
		}
		if s.detailBytes != maxDetailHourBytes || len(s.detail) != 10 {
			t.Fatal("byte refusal changed retained detail")
		}
	}
	r.ReceivedAt = base.Truncate(time.Hour).Add(time.Hour + time.Minute)
	if err = s.Record(r, topology); err != nil {
		t.Fatal(err)
	}
	prior, _ := detailForHour(t, s, base.Truncate(time.Hour))
	if len(prior) != 10 {
		t.Fatalf("rollover retained %d rows", len(prior))
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	prior, _ = detailForHour(t, s, base.Truncate(time.Hour))
	if len(prior) != 10 || len(s.detail) != 1 {
		t.Fatal("reopen lost detail at exact byte limit")
	}
}

func TestDetailReceiptLimitAndHourRollover(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Now().UTC().Truncate(time.Hour).Add(time.Minute)
	rows := make([]Receipt, maxDetailHourReceipts)
	for i := range rows {
		rows[i] = Receipt{ReceivedAt: base.Add(time.Duration(i) * 2 * time.Second), Operator: "metro"}
	}
	installDetailFixture(t, s, base, rows)
	r := Receipt{ReceivedAt: base.Add(41 * time.Minute), DeliveryGap: true, Error: "synthetic gap"}
	for i := 0; i < 2; i++ {
		if err = s.Record(r, testTopology()); err == nil {
			t.Fatal("accepted 1,201st record")
		}
		if len(s.detail) != maxDetailHourReceipts {
			t.Fatal("count refusal changed detail")
		}
	}
	r.ReceivedAt = base.Truncate(time.Hour).Add(time.Hour + time.Minute)
	if err = s.Record(r, testTopology()); err != nil {
		t.Fatal(err)
	}
	prior, _ := detailForHour(t, s, base.Truncate(time.Hour))
	if len(prior) != maxDetailHourReceipts {
		t.Fatal("rollover lost rows")
	}
}

func TestPostInferenceOverflowFencesCheckpointUntilReopen(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	rows := sizedDetail(t, base, maxDetailHourBytes-(64<<10))
	installDetailFixture(t, s, base, rows)
	s.mu.Lock()
	for i := 0; i < 100; i++ {
		s.engine.Cases = append(s.engine.Cases, Forecast{Route: string(bytes.Repeat([]byte{'x'}, 2000)), IssuedAt: base, Episode: fmt.Sprint(i)})
	}
	s.mu.Unlock()
	r := Receipt{ReceivedAt: base.Add(time.Minute), DeliveryGap: true, Rows: []Row{}}
	if err = s.Record(r, testTopology()); err == nil {
		t.Fatal("expected final receipt overflow")
	}
	s.mu.Lock()
	if s.detailFence == nil || len(s.detail) != len(rows) || !s.engine.LastReceipt.Equal(rows[len(rows)-1].ReceivedAt) {
		s.mu.Unlock()
		t.Fatal("overflow leaked rejected receipt")
	}
	for _, b := range s.index.Blocks {
		if b.Key == "metro:state" {
			s.mu.Unlock()
			t.Fatal("checkpoint published rejected inference")
		}
	}
	s.mu.Unlock()
	if err = s.Close(); err == nil {
		t.Fatal("fenced close succeeded")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil || len(s.detail) != len(rows) {
		t.Fatalf("recovery lost earlier detail: %v", s.detailFence)
	}
}
