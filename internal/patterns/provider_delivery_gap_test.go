package patterns

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// Synthetic stopped/incoming transitions must not train a component across a
// captured loss, including when the first recovered receipt is partial.
func TestProviderDeliveryGapPersistsAndCutsReplayContinuity(t *testing.T) {
	c := DefaultConfig(t.TempDir())
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	path := syntheticProviderPath("cm")
	control := newProvider("cm")
	var recovery time.Time
	for i := 0; i < 3; i++ {
		for j := 0; j < 2; j++ {
			at := base.Add(time.Duration(i*60+j*30) * time.Second)
			if i == 2 && j == 0 {
				recovery = at.Add(-25 * time.Second)
				gap := ProviderReceipt{Operator: "cm", ReceivedAt: recovery, Partial: true, PredictionStop: "cm:stop", Gap: true}
				if err := s.RecordProvider(gap); err != nil {
					t.Fatal(err)
				}
			}
			r := syntheticProviderReceipt("cm", "v", path, at, i, j == 1)
			if err := s.RecordProvider(r); err != nil {
				t.Fatal(err)
			}
			control.step(r, c, true, false)
		}
	}
	if componentCount(control.Engine) != 2 || componentCount(s.providers["cm"].Engine) != 0 {
		t.Fatal("synthetic sequence did not distinguish continuous and interrupted learning")
	}
	replay := newProvider("cm")
	found := false
	for _, b := range s.index.Blocks {
		if b.Operator != "cm" || b.Kind != "observations" {
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
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
			var frame ProviderReceipt
			if err := json.Unmarshal(line, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.ReceivedAt.Equal(recovery) {
				found = frame.Gap && frame.Partial
			}
			replay.step(frame, c, true, true)
		}
	}
	if !found || replay.Engine.Gaps == 0 || componentCount(replay.Engine) != 0 {
		t.Fatalf("replay did not preserve interruption: found=%v gaps=%d components=%d", found, replay.Engine.Gaps, componentCount(replay.Engine))
	}
}
