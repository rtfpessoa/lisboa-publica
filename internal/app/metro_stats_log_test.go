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
