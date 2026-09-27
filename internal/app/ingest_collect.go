package app

import (
	"context"
	"go.uber.org/zap"
	"lisboapublica/internal/api"
	"strings"
	"sync"
	"time"
)

// Run refreshes provider data until its context is cancelled.
func (f *Fetcher) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(providerCollectorCount)
	go func() { defer wg.Done(); f.staticLoop(ctx) }()
	go func() { defer wg.Done(); f.cpLoop(ctx) }()
	go func() { defer wg.Done(); f.cmArrivalLoop(ctx) }()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(providerRefreshInterval)
		defer ticker.Stop()
		prune := time.NewTicker(staticRefreshInterval)
		defer prune.Stop()
		f.refreshLive(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f.refreshLive(ctx)
			case <-prune.C:
				if e := f.Store.prune(ctx); e != nil {
					f.Log.Warn("retention cleanup failed", zap.Error(e))
				}
				if e := f.Store.compactSnapshots(ctx); e != nil {
					f.Log.Warn("optional snapshot compaction skipped")
				}
			}
		}
	}()
	wg.Wait()
}
func (f *Fetcher) staticLoop(ctx context.Context) {
	f.refreshStatic(ctx)
	timer := time.NewTimer(staticRefreshInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			f.refreshStatic(ctx)
			timer.Reset(staticRefreshInterval)
		}
	}
}
func (f *Fetcher) markError(ctx context.Context, p provider, static bool, err error) {
	f.Store.PublishMu.Lock()
	defer f.Store.PublishMu.Unlock()
	op := f.Cache.operator(p.ID)
	message := err.Error()
	if strings.Contains(message, "objectstorage.") {
		message = "Falha ao obter GTFS no armazenamento oficial; os dados anteriores mantêm-se."
	}
	if static {
		op.StaticStatus = api.OperatorStaticStatusError
		op.StaticError = ptr(message)
	} else {
		op.Status = api.OperatorStatusError
		op.Error = ptr(message)
	}
	f.publishProvider(ctx, p.ID, nil, op, nil)
	f.Log.Warn("provider refresh failed", zap.String("operator", p.ID), zap.Bool("static", static), zap.String("reason", message))
}
