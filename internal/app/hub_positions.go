package app

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"
)

// One acquisition owner and one immutable latest batch serve all consumers.
type hubPositionState struct {
	positionsMu    sync.RWMutex
	positionsOwned bool
	positions      hubObservationBatch
}

// positionsPacer measures minimum refresh starts. Slow work never overlaps or
// catches up; one healthy rolling minute is required for each recovery step.
type positionsPacer struct {
	interval     time.Duration
	healthySince time.Time
}

func (p *positionsPacer) update(now time.Time, healthy bool) time.Duration {
	if p.interval == 0 {
		p.interval = time.Second
	}
	if !healthy {
		p.healthySince = time.Time{}
		if p.interval < 2*time.Second {
			p.interval = 2 * time.Second
		} else {
			p.interval = 5 * time.Second
		}
		return p.interval
	}
	if p.healthySince.IsZero() {
		p.healthySince = now
	}
	if p.interval > time.Second && now.Sub(p.healthySince) >= time.Minute {
		if p.interval > 2*time.Second {
			p.interval = 2 * time.Second
		} else {
			p.interval = time.Second
		}
		p.healthySince = now
	}
	return p.interval
}

func (f *Fetcher) positionsLoop(ctx context.Context, ready chan<- struct{}) {
	defer func() {
		if ready != nil {
			close(ready)
		}
	}()
	u, _ := url.Parse(f.Hub)
	pacer := positionsPacer{}
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		healthy, cooldown := f.positionsCycle(ctx, u.Hostname())
		if ctx.Err() != nil {
			return
		}

		if ready != nil {
			close(ready)
			ready = nil
		}
		next := f.positionsNext(&pacer, positionCycleTiming{started: started, healthy: healthy, cooldown: cooldown, host: u.Hostname()})
		if !waitPositionStart(ctx, next) {
			return
		}
	}
}

type positionCycleTiming struct {
	started, cooldown time.Time
	healthy           bool
	host              string
}

func (f *Fetcher) positionsNext(pacer *positionsPacer, v positionCycleTiming) time.Time {
	next := v.started.Add(pacer.update(v.started, v.healthy))
	if budget, ok := f.Client.Transport.(*BudgetTransport); ok {
		_, v.cooldown = budget.positionsHeadroom(v.host)
	}
	if v.cooldown.After(next) {
		next = v.cooldown
	}
	return next
}
func waitPositionStart(ctx context.Context, next time.Time) bool {
	timer := time.NewTimer(max(time.Until(next), time.Millisecond))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (f *Fetcher) positionsCycle(ctx context.Context, host string) (bool, time.Time) {
	healthy, cooldown := true, time.Time{}
	if budget, ok := f.Client.Transport.(*BudgetTransport); ok {
		healthy, cooldown = budget.positionsHeadroom(host)
	}
	if !healthy {
		return false, cooldown
	}
	batch := f.fetchHubPositions(positionRequestContext(ctx))
	if ctx.Err() != nil {
		return false, cooldown
	}
	if !errors.Is(batch.err, errPositionDeferred) {
		f.positionsMu.Lock()
		f.positions = batch
		f.positionsMu.Unlock()
		f.publishMetroPositions(ctx, batch)
	}
	return batch.err == nil, cooldown
}

func (f *Fetcher) publishMetroPositions(ctx context.Context, batch hubObservationBatch) {
	for _, p := range providers {
		if p.ID != "metro" {
			continue
		}
		if batch.err != nil {
			r := f.Cache.metroRuntime
			r.mu.Lock()
			r.hubError = batch.err.Error()
			r.markCaptureGap()
			r.captureChanged(batch.collected)
			r.mu.Unlock()
			f.markError(ctx, p, false, batch.err)
			return
		}
		vehicles, err := f.hubVehicles(p, batch.positions, batch.collected)
		if err != nil {
			f.markError(ctx, p, false, err)
			return
		}
		f.saveLive(ctx, p, vehicles, batch.collected)
		f.Cache.metroRuntime.observeHub(batch.positions, batch.collected)
		return
	}
}
