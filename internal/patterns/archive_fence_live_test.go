package patterns

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func seedSupportedMetroForecast(t *testing.T, s *Service, now time.Time) MetroPopupForecastQuery {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine.LastReceipt = now
	s.topology.Profile = fmt.Sprintf("synthetic-s%d-b%d", int(s.config.SampleInterval/time.Second), s.config.BinSeconds)
	g := &group{ID: "episode", Train: "7", Route: "line", Direction: "direction", Active: true}
	query := MetroPopupForecastQuery{Train: "7", Route: "line", Direction: "direction", Profile: "synthetic", Now: now}
	for i, stop := range []string{"A", "B", "C"} {
		start := now.Add(time.Duration(-120+i*30) * time.Second)
		end := start.Add(10 * time.Second)
		g.Signals = append(g.Signals, signal{Stop: stop, L: start, U: end, Supported: true})
		query.Signals = append(query.Signals, MetroPopupSignal{Stop: stop, Start: start, End: end})
	}
	s.engine.Groups[groupKey(groupIdentity{Train: "7", Route: "line", Direction: "direction"})] = g
	expected, expiry := now.Add(time.Minute), now.Add(90*time.Second)
	s.engine.Live = []Forecast{{Stop: "A", Episode: "episode", Train: "7", Route: "line", Direction: "direction", OwnAt: &expected, OwnValidUntil: &expiry}}
	s.lastCheckpoint = time.Time{}
	return query
}

func TestProviderFlushCorruptionImmediatelyWithdrawsMetroSupport(t *testing.T) {
	for _, key := range []string{"detail", "state"} {
		t.Run(key, func(t *testing.T) {
			config := DefaultConfig(t.TempDir())
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.ConfigureOperators([]string{"metro", "cm"}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			if err = s.Record(durabilityReceipt(now, "metro"), testTopology()); err != nil {
				t.Fatal(err)
			}
			query := seedSupportedMetroForecast(t, s, now)
			if len(s.MetroPopupForecasts(query)) != 1 {
				t.Fatal("fixture did not expose supported popup")
			}
			priorView, err := s.View(context.Background(), "A")
			if err != nil || len(priorView.Forecasts) != 1 || priorView.Forecasts[0].OwnAt == nil {
				t.Fatalf("fixture missing live forecast: %v", err)
			}
			s.mu.Lock()
			sourceKey := "metro:state"
			if key == "detail" {
				sourceKey = "metro:detail:" + now.Truncate(time.Hour).Format(time.RFC3339)
			}
			source, findErr := s.publicationSource(sourceKey)
			s.mu.Unlock()
			if findErr != nil || source.block == nil {
				t.Fatal("missing source generation")
			}
			if err = os.WriteFile(filepath.Join(config.Directory, source.block.File), []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
			r := ProviderReceipt{Operator: "cm", ReceivedAt: now.Add(time.Second), Rows: []Observation{}}
			if err = s.RecordProvider(r); err == nil {
				t.Fatal("provider flush accepted corrupt Metro generation")
			}
			if len(s.MetroPopupForecasts(query)) != 0 {
				t.Fatal("popup retained support after the fence")
			}
			view, err := s.View(context.Background(), "A")
			if err != nil || view.Status != "paused" || len(view.Forecasts) != 0 {
				t.Fatalf("view retained live support: status=%s forecasts=%d err=%v", view.Status, len(view.Forecasts), err)
			}
			s.mu.Lock()
			reset := s.pendingMetroDeliveryGap && len(s.engine.Groups) == 0 && len(s.engine.Live) == 0
			s.mu.Unlock()
			if !reset {
				t.Fatal("fence did not immediately reset continuity")
			}
		})
	}
}
