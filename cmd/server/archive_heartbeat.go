package main

import (
	"context"
	"time"

	"go.uber.org/zap"
	"lisboapublica/internal/patterns"
)

const archiveHeartbeatInterval = time.Minute
const archiveProgressGrace = 2 * time.Minute
const archiveHeartbeatStopGrace = 250 * time.Millisecond

// manageArchiveLifecycle starts reporting only for an enabled archive and
// stops it before releasing the archive owner.
func manageArchiveLifecycle(ctx context.Context, service *patterns.Service, log *zap.Logger) func() {
	if service == nil {
		return func() {}
	}
	stop := startArchiveHeartbeat(ctx, service, log)
	return func() { stop(); _ = service.Close() }
}

// startArchiveHeartbeat owns no archive resources. It reads the worker's atomic
// snapshot so reporting continues when a pass cannot acquire the archive mutex.
func startArchiveHeartbeat(parent context.Context, service *patterns.Service, log *zap.Logger) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(archiveHeartbeatInterval)
		defer ticker.Stop()
		runArchiveHeartbeat(ctx, ticker.C, service.MaintenanceProgress, log)
	}()
	return func() {
		cancel()
		timer := time.NewTimer(archiveHeartbeatStopGrace)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			// An arbitrary log sink cannot be canceled. Do not hold up archive
			// closure if its single heartbeat goroutine is blocked in Write.
		}
	}
}

func runArchiveHeartbeat(ctx context.Context, ticks <-chan time.Time, snapshot func() patterns.MaintenanceProgress, log *zap.Logger) {
	if ctx.Err() != nil {
		return
	}
	logArchiveProgress(log, snapshot(), time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ticks:
			if !ok {
				return
			}
			logArchiveProgress(log, snapshot(), time.Now())
		}
	}
}

func logArchiveProgress(log *zap.Logger, progress patterns.MaintenanceProgress, now time.Time) {
	stale := archiveProgressStale(progress, now)
	fields := []zap.Field{
		zap.String("last_result", progress.LastResult),
		zap.Bool("in_progress", progress.InProgress),
		zap.Bool("stopped", progress.Stopped),
		zap.Float64("maintenance_interval_seconds", progress.Interval.Seconds()),
		zap.String("stale_reason", stale),
	}
	if !progress.WorkerStartedAt.IsZero() {
		fields = append(fields, zap.Time("worker_started_at", progress.WorkerStartedAt))
	}
	if !progress.LastStartedAt.IsZero() {
		fields = append(fields, zap.Time("last_started_at", progress.LastStartedAt))
	}
	if !progress.LastCompletedAt.IsZero() {
		fields = append(fields, zap.Time("last_completed_at", progress.LastCompletedAt))
	}
	if stale != "" || progress.LastResult == "error" {
		log.Warn("transport archive maintenance heartbeat", fields...)
	} else {
		log.Info("transport archive maintenance heartbeat", fields...)
	}
}

func archiveProgressStale(progress patterns.MaintenanceProgress, now time.Time) string {
	if progress.Stopped {
		return "stopped"
	}
	if progress.InProgress {
		if !progress.LastStartedAt.IsZero() && now.Sub(progress.LastStartedAt) >= archiveProgressGrace {
			return "in_progress"
		}
		return ""
	}
	last := progress.LastCompletedAt
	if last.IsZero() {
		last = progress.WorkerStartedAt
	}
	if !last.IsZero() && now.Sub(last) >= progress.Interval+archiveProgressGrace {
		if progress.LastCompletedAt.IsZero() {
			return "never_started"
		}
		return "overdue"
	}
	return ""
}
