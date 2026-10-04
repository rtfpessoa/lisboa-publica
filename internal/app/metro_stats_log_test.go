package app

import (
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

// The own-forecast counters must be logged on the queued history path, which is the
// production path, not only on the fallback publication path.
func TestMetroOwnStatsLoggedThroughHistoryPath(t *testing.T) {
	core, logs := observer.New(zap.DebugLevel)
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	client := &MetroClient{Cache: NewCache(), History: history, Log: zap.New(core)}
	now := time.Now().UTC()
	client.deliverPatternHistory(metroHistoryTask{Data: MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusOk}, Waits: []MetroWait{}}, At: now, Bytes: 1}, 0)
	for _, entry := range logs.All() {
		if entry.Message == "metro own forecasts" {
			return
		}
	}
	t.Fatal("own-forecast stats were not logged through the history path")
}

// An upstream failure that suppresses a sample warns once per distinct message and
// warns again after the source recovers and fails anew.
func TestMetroSampleErrorWarnsOnce(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	history, err := patterns.Open(patterns.DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	client := &MetroClient{Cache: NewCache(), History: history, Log: zap.New(core)}
	now := time.Now().UTC()
	bad := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusError, Message: "hub unavailable"}}
	if err := client.recordPatterns(bad, nil, now); err != nil {
		t.Fatal(err)
	}
	_ = client.recordPatterns(bad, nil, now.Add(time.Second))
	if got := logs.FilterMessage("metro sample skipped").Len(); got != 1 {
		t.Fatalf("warned %d times", got)
	}
	ok := &MetroData{Status: api.MetroStatus{Status: api.MetroStatusStatusOk}, Waits: []MetroWait{}}
	_ = client.recordPatterns(ok, nil, now.Add(2*time.Second))
	_ = client.recordPatterns(bad, nil, now.Add(3*time.Second))
	if got := logs.FilterMessage("metro sample skipped").Len(); got != 2 {
		t.Fatalf("warned %d times after recovery", got)
	}
}
