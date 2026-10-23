package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

const maintenanceInterval = time.Minute
const maintenancePassTimeout = time.Minute

// StartMaintenance starts the single store-owned maintenance loop. The first
// bounded pass runs before the caller starts ingestion or accepts requests.
// The returned function cancels and joins maintenance and its pressure vacuum.
func (s *Store) StartMaintenance(parent context.Context, log *zap.Logger) (func(), error) {
	ticker := time.NewTicker(maintenanceInterval)
	stop, err := s.startMaintenance(parent, log, ticker.C)
	if err != nil {
		ticker.Stop()
		return nil, err
	}
	return func() { ticker.Stop(); stop() }, nil
}

func (s *Store) startMaintenance(parent context.Context, log *zap.Logger, ticks <-chan time.Time) (func(), error) {
	if !s.maintenanceRunning.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("storage maintenance is already running")
	}
	if log == nil {
		log = zap.NewNop()
	}
	s.maintenanceLog.Store(log)
	ctx, cancel := context.WithCancel(parent)
	s.maintenancePass(ctx, log)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var lastCompaction time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case now, ok := <-ticks:
				if !ok {
					return
				}
				s.maintenancePass(ctx, log)
				if !s.pressureVacuum.Load() && (lastCompaction.IsZero() || now.Sub(lastCompaction) >= staticRefreshInterval) {
					if err := s.compactSnapshots(ctx); err != nil && ctx.Err() == nil {
						log.Warn("optional snapshot compaction skipped", zap.Error(err))
					}
					lastCompaction = now
				}
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			cancel()
			<-done
			s.pressureVacuumWG.Wait()
			s.maintenanceRunning.Store(false)
		})
	}, nil
}

func (s *Store) maintenancePass(parent context.Context, log *zap.Logger) {
	ctx, cancel := context.WithTimeout(parent, maintenancePassTimeout)
	defer cancel()
	started := time.Now()
	result, err := s.pruneWithVacuumContext(ctx, parent)
	fields := []zap.Field{zap.String("reason", result.reason), zap.Int64("deleted", result.deleted), zap.Duration("duration", time.Since(started))}
	if result.cutoff != nil {
		fields = append(fields, zap.Time("cutoff", *result.cutoff), zap.Int64("epoch", result.epoch))
	}
	if err != nil {
		fields = append(fields, zap.Error(err))
		log.Warn("storage maintenance failed", fields...)
	} else {
		log.Info("storage maintenance", fields...)
	}
}
