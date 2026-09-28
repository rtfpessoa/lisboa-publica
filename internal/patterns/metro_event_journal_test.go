package patterns

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMetroEventJournalDedupRecoveryAndCorruption(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := MetroEventRecord{ID: "visit:arrival", Journey: "run", At: now, Payload: json.RawMessage(`{"before":{"wait":10},"after":{"wait":0}}`)}
	if err = s.RecordMetroEvents([]MetroEventRecord{record}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordMetroEvents([]MetroEventRecord{record}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, err := s.MetroJourneyEvents(context.Background(), "run", now)
	if err != nil || len(out) != 1 || out[0].ID != record.ID {
		t.Fatal("committed evidence did not survive", out, err)
	}
	if len(s.engine.Previous) != 0 || len(s.engine.Groups) != 0 {
		t.Fatal("event recovery restored live continuity")
	}
	var eventBlock block
	for _, b := range s.index.Blocks {
		if b.Kind == "popup-events" {
			eventBlock = b
		}
	}
	if err = os.WriteFile(filepath.Join(config.Directory, eventBlock.File), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MetroJourneyEvents(context.Background(), "run", now); err == nil {
		t.Fatal("corrupt proof trusted")
	}
}
func TestMetroEventJournalSevenDayTTLAndProofLimit(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	record := MetroEventRecord{ID: "old", Journey: "run", At: now.Add(-6 * 24 * time.Hour), Payload: json.RawMessage(`{"synthetic":true}`)}
	if err = s.RecordMetroEvents([]MetroEventRecord{record}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordMetroEvents([]MetroEventRecord{{ID: "new", Journey: "run", At: now.Add(2 * 24 * time.Hour), Payload: json.RawMessage(`{}`)}}, now.Add(2*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.MetroJourneyEvents(context.Background(), "run", now.Add(2*24*time.Hour))
	if err != nil || len(rows) != 1 || rows[0].ID != "new" {
		t.Fatal("TTL not enforced", rows, err)
	}
	record.Payload = make([]byte, 64<<10+1)
	if err = s.RecordMetroEvents([]MetroEventRecord{record}, now); err == nil {
		t.Fatal("oversized proof admitted")
	}
}
