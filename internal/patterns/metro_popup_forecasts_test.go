package patterns

import (
	"strconv"
	"testing"
	"time"
)

func TestMetroPopupForecastRequiresIndependentEpisodeSupport(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	s.status = "collecting"
	s.engine.LastReceipt = now
	s.topology.Profile = "synthetic-s" + strconv.Itoa(int(s.config.SampleInterval/time.Second)) + "-b" + strconv.Itoa(s.config.BinSeconds)
	g := &group{ID: "episode", Train: "7", Route: "line", Direction: "direction", Active: true}
	candidates := []MetroPopupSignal{}
	for i, stop := range []string{"A", "B", "C"} {
		start := now.Add(time.Duration(-120+i*30) * time.Second)
		end := start.Add(10 * time.Second)
		g.Signals = append(g.Signals, signal{Stop: stop, L: start, U: end, Supported: true})
		candidates = append(candidates, MetroPopupSignal{Stop: stop, Start: start, End: end})
	}
	s.engine.Groups[groupKey(groupIdentity{Train: "7", Route: "line", Direction: "direction"})] = g
	expected, expiry := now.Add(time.Minute), now.Add(90*time.Second)
	s.engine.Live = []Forecast{{Episode: "episode", Train: "7", Route: "line", Direction: "direction", OwnAt: &expected, OwnValidUntil: &expiry}}
	get := func(signals []MetroPopupSignal, profile string) []Forecast {
		return s.MetroPopupForecasts(MetroPopupForecastQuery{Train: "7", Route: "line", Direction: "direction", Profile: profile, Signals: signals, Now: now})
	}
	if len(get(nil, "synthetic")) != 0 || len(get(candidates[:2], "synthetic")) != 0 {
		t.Fatal("reference-only or insufficient support admitted")
	}
	if len(get(candidates, "wrong")) != 0 {
		t.Fatal("incompatible topology admitted")
	}
	if len(get(candidates, "synthetic")) != 1 {
		t.Fatal("supported existing engine forecast missing")
	}
	// Duplicate engine signals cannot turn one independent visit into three proofs.
	g.Signals = []signal{g.Signals[0], g.Signals[0], g.Signals[0]}
	if len(get(candidates, "synthetic")) != 0 {
		t.Fatal("duplicate visit support counted repeatedly")
	}
	g.Active = false
	if len(get(candidates, "synthetic")) != 0 {
		t.Fatal("inactive episode admitted")
	}
}
