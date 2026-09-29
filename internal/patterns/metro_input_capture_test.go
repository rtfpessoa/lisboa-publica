package patterns

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMetroInputCaptureRecoveryBoundsAndRetention(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(5 * time.Minute).Add(time.Second)
	record := MetroEventRecord{ID: "capture-1", At: now, Payload: json.RawMessage(`{"synthetic":true,"gap":true}`)}
	if err := s.RecordMetroInputs(context.Background(), []MetroEventRecord{record}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMetroInputs(context.Background(), []MetroEventRecord{record}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := "metro-inputs:" + now.Truncate(5*time.Minute).Format(time.RFC3339)
	rows, err := s.retainedMetroEvents(key)
	if err != nil || len(rows) != 1 {
		t.Fatal("input did not recover or duplicate was retained", rows, err)
	}
	if len(s.engine.Previous) != 0 {
		t.Fatal("capture recovery restored live motion")
	}
	tooMany := make([]MetroEventRecord, 65)
	if err := s.RecordMetroInputs(context.Background(), tooMany, now); err == nil {
		t.Fatal("batch bound missing")
	}
	record.Payload = make([]byte, (256<<10)+1)
	if err := s.RecordMetroInputs(context.Background(), []MetroEventRecord{record}, now); err == nil {
		t.Fatal("record bound missing")
	}
	next := now.Add(8 * 24 * time.Hour)
	fresh := MetroEventRecord{ID: "capture-2", At: next, Payload: json.RawMessage(`{}`)}
	if err := s.RecordMetroInputs(context.Background(), []MetroEventRecord{fresh}, next); err != nil {
		t.Fatal(err)
	}
	for _, b := range s.index.Blocks {
		if b.Key == key {
			t.Fatal("original-age TTL not enforced")
		}
	}
}

func TestMetroDeliveryLossResetsLearningWithoutErasingIssuedEvidence(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.engine.Previous["synthetic"] = priorRow{}
	s.engine.Groups["synthetic"] = &group{}
	s.engine.Cases = []Forecast{{Train: "synthetic"}}
	s.InterruptMetroContinuity()
	if len(s.engine.Previous) != 0 || len(s.engine.Groups) != 0 || !s.pendingMetroDeliveryGap || len(s.engine.Cases) != 1 {
		t.Fatal("delivery gap did not cut learning or erased frozen evidence")
	}
}

func TestMetroDeliveryBarrierPersistsDespiteSamplingAndCutsReplay(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	topology := Topology{Profile: "synthetic", Stations: []Station{}, Patterns: []Pattern{}}
	if err := s.Record(Receipt{ReceivedAt: now, Rows: []Row{}}, topology); err != nil {
		t.Fatal(err)
	}
	s.InterruptMetroContinuity()
	if err := s.Record(Receipt{ReceivedAt: now.Add(time.Second), Rows: []Row{}}, topology); err != nil {
		t.Fatal(err)
	}
	if len(s.detail) != 2 || !s.detail[1].DeliveryGap || s.pendingMetroDeliveryGap {
		t.Fatal("unsampled receipt lost ordered barrier", s.detail)
	}
	raw, err := json.Marshal(s.detail[1])
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	replay := newEngine()
	replay.Previous["synthetic"] = priorRow{}
	replay.Groups["synthetic"] = &group{}
	replay.Profile = s.topology.Profile
	replay.step(receipt, s.topology, config)
	if len(replay.Previous) != 0 || len(replay.Groups) != 0 || replay.Gaps != 1 {
		t.Fatal("retained barrier did not cut replay")
	}
}
