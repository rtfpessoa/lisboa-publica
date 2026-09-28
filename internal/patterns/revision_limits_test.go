package patterns

import (
	"context"
	"testing"
	"time"
)

func TestMetroRevisionRejectsCapacityLimitedReplay(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().UTC()
	topology := testTopology()
	f := Forecast{Episode: "synthetic", IssuedAt: at, Stop: "A", Function: "waiting"}
	values := make([]Forecast, maxPendingForecasts+1)
	for i := range values {
		values[i] = f
	}
	job := newMetroRevision(s, context.Background(), nil, at)
	job.frames = []Receipt{{ReceivedAt: at, Topology: &topology, Forecasts: values}}
	if err := job.replayFrames(); err == nil {
		t.Fatal("oversized pending replay was accepted for publication")
	}
}
