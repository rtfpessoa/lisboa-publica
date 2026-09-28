package app

import (
	"context"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHubPositionsPacerPressureAndRollingRecovery(t *testing.T) {
	now := time.Date(2026, 9, 28, 20, 0, 0, 0, time.UTC)
	p := positionsPacer{}
	if p.update(now, true) != time.Second {
		t.Fatal("one-second target missing")
	}
	if p.update(now.Add(time.Second), false) != 2*time.Second || p.update(now.Add(3*time.Second), false) != 5*time.Second {
		t.Fatal("pressure did not reduce cadence")
	}
	if p.update(now.Add(8*time.Second), true) != 5*time.Second || p.update(now.Add(67*time.Second), true) != 5*time.Second {
		t.Fatal("recovered before a full rolling minute")
	}
	if p.update(now.Add(68*time.Second), true) != 2*time.Second || p.update(now.Add(127*time.Second), true) != 2*time.Second || p.update(now.Add(128*time.Second), true) != time.Second {
		t.Fatal("healthy recovery failed")
	}
}

func TestHubPositionsCollectorStartsIsolationAndSharedResponse(t *testing.T) {
	store := testStore(t)
	cache := NewCache()
	now := time.Now().UTC()
	cache.update("metro", fixtureStatic("metro", now), nil, cache.operator("metro"))
	cache.update("cp", fixtureStatic("cp", now), nil, cache.operator("cp"))
	before, _ := cache.state("")
	starts := make(chan time.Time, 4)
	var calls, active, maximum atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/vehicles/positions" {
			count := active.Add(1)
			defer active.Add(-1)
			for old := maximum.Load(); count > old && !maximum.CompareAndSwap(old, count); old = maximum.Load() {
			}
			starts <- time.Now()
			if calls.Add(1) == 1 {
				time.Sleep(1250 * time.Millisecond)
			}
			io.WriteString(w, `{"data":[],"error":null}`)
		} else {
			io.WriteString(w, `[]`)
		}
	}))
	defer upstream.Close()
	f := NewFetcher(store, cache, zap.NewNop())
	f.Hub, f.CM = upstream.URL, upstream.URL
	f.Client = upstream.Client()
	f.positionsOwned = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); f.positionsLoop(ctx, make(chan struct{})) }()
	at := []time.Time{}
	for len(at) < 3 {
		select {
		case start := <-starts:
			at = append(at, start)
		case <-ctx.Done():
			t.Fatal("collector stalled")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("collector failed cancellation")
	}
	if maximum.Load() != 1 || at[1].Sub(at[0]) < 1250*time.Millisecond || at[2].Sub(at[1]) < 990*time.Millisecond {
		t.Fatal("overlap or catch-up after slow response", at, maximum.Load())
	}
	after, _ := cache.state("")
	if after.Live["cp"] != before.Live["cp"] || after.Operators["cp"].LiveUpdatedAt != before.Operators["cp"].LiveUpdatedAt {
		t.Fatal("fast acquisition changed other operator publication")
	}
	count := calls.Load()
	f.refreshLive(context.Background())
	if calls.Load() != count {
		t.Fatal("five-second consumer fetched a second Hub response")
	}
}

func TestHubPositionsProtectedWorkAndActualChainBudgets(t *testing.T) {
	transport := NewBudgetTransport(900)
	now := time.Now()
	transport.now = func() time.Time { return now }
	attempts := 0
	transport.Base = roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	client := &http.Client{Transport: transport}
	host := "go.tmlmobilidade.pt"
	// Synthetic retained attempts leave three chain slots, twenty safety slots
	// and six slots of due protected work.
	for i := 0; i < 91; i++ {
		if err := transport.claim(host); err != nil {
			t.Fatal(err)
		}
	}
	release := ProtectUpstreamWork(client, host, 6)
	ctx := positionRequestContext(context.Background())
	for i := 0; i < 3; i++ {
		r, _ := http.NewRequestWithContext(ctx, "GET", "https://"+host+"/positions", nil)
		res, err := transport.RoundTrip(r)
		if err != nil {
			t.Fatal("bounded chain was not reserved", i, err)
		}
		res.Body.Close()
	}
	r, _ := http.NewRequestWithContext(positionRequestContext(context.Background()), "GET", "https://"+host+"/positions", nil)
	if _, err := transport.RoundTrip(r); err == nil {
		t.Fatal("positions consumed protected/safety headroom")
	}
	if attempts != 3 {
		t.Fatal("denied positions became actual attempts", attempts)
	}
	for i := 0; i < 6; i++ {
		if err := transport.claim(host); err != nil {
			t.Fatal("due protected work was starved", err)
		}
	}
	release()
	release()
	if transport.protectedTotal != 0 {
		t.Fatal("reservation leaked")
	}
	for i := 0; i < 20; i++ {
		if err := transport.claim(host); err != nil {
			t.Fatal(err)
		}
	}
	if err := transport.claim(host); err == nil {
		t.Fatal("hard 120/min cap ignored")
	}
	now = now.Add(time.Minute)
	healthy, _ := transport.positionsHeadroom(host)
	if !healthy {
		t.Fatal("rolling window did not recover")
	}
}

func TestHubPositionsGlobalProtectedHeadroom(t *testing.T) {
	transport := NewBudgetTransport(900)
	now := time.Now()
	transport.now = func() time.Time { return now }
	for i := 0; i < 798; i++ {
		if err := transport.claim("direct.example"); err != nil {
			t.Fatal(err)
		}
	}
	if err := transport.claimPriority("go.tmlmobilidade.pt", true); err == nil {
		t.Fatal("global safety headroom not protected")
	}
	for i := 0; i < 102; i++ {
		if err := transport.claim("direct.example"); err != nil {
			t.Fatal("protected request denied", err)
		}
	}
	if err := transport.claim("go.tmlmobilidade.pt"); err == nil {
		t.Fatal("hard 900/min cap ignored")
	}
}
