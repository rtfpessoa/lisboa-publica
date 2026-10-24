package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"lisboapublica/internal/patterns"
)

func TestArchiveHeartbeatStates(t *testing.T) {
	now := time.Now()
	base := patterns.MaintenanceProgress{WorkerStartedAt: now, Interval: time.Minute, LastResult: "never_started"}
	for _, tc := range []struct {
		name   string
		change func(*patterns.MaintenanceProgress)
		now    time.Time
		level  zapcore.Level
		stale  string
	}{
		{"waiting for first pass", func(*patterns.MaintenanceProgress) {}, now.Add(3*time.Minute - time.Nanosecond), zapcore.InfoLevel, ""},
		{"first pass overdue", func(*patterns.MaintenanceProgress) {}, now.Add(3 * time.Minute), zapcore.WarnLevel, "never_started"},
		{"completed", func(p *patterns.MaintenanceProgress) { p.LastResult, p.LastCompletedAt = "completed", now }, now.Add(time.Minute), zapcore.InfoLevel, ""},
		{"completed but overdue", func(p *patterns.MaintenanceProgress) { p.LastResult, p.LastCompletedAt = "completed", now }, now.Add(3 * time.Minute), zapcore.WarnLevel, "overdue"},
		{"pending", func(p *patterns.MaintenanceProgress) { p.LastResult, p.LastCompletedAt = "pending", now }, now.Add(time.Minute), zapcore.InfoLevel, ""},
		{"hard failure", func(p *patterns.MaintenanceProgress) { p.LastResult, p.LastCompletedAt = "error", now }, now.Add(time.Minute), zapcore.WarnLevel, ""},
		{"recent pass now blocked", func(p *patterns.MaintenanceProgress) {
			p.LastResult, p.LastCompletedAt, p.LastStartedAt, p.InProgress = "completed", now, now.Add(time.Second), true
		}, now.Add(2*time.Minute + time.Second), zapcore.WarnLevel, "in_progress"},
		{"stopped", func(p *patterns.MaintenanceProgress) { p.Stopped = true }, now, zapcore.WarnLevel, "stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			progress := base
			tc.change(&progress)
			core, logs := observer.New(zapcore.InfoLevel)
			logArchiveProgress(zap.New(core), progress, tc.now)
			entries := logs.All()
			if len(entries) != 1 || entries[0].Level != tc.level {
				t.Fatalf("unexpected heartbeat: %+v", entries)
			}
			fields := entries[0].ContextMap()
			if fields["last_result"] != progress.LastResult || fields["in_progress"] != progress.InProgress || fields["stale_reason"] != tc.stale || fields["stopped"] != progress.Stopped || fields["maintenance_interval_seconds"] != float64(60) {
				t.Fatalf("incorrect heartbeat fields: %+v", fields)
			}
			if _, exists := fields["last_completed_at"]; exists != !progress.LastCompletedAt.IsZero() {
				t.Fatalf("incorrect completion clock field: %+v", fields)
			}
		})
	}
}

func TestArchiveHeartbeatRunsIndependentlyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks, called, done := make(chan time.Time, 1), make(chan struct{}, 2), make(chan struct{})
	core, logs := observer.New(zapcore.InfoLevel)
	go func() {
		defer close(done)
		runArchiveHeartbeat(ctx, ticks, func() patterns.MaintenanceProgress {
			called <- struct{}{}
			return patterns.MaintenanceProgress{LastResult: "pending"}
		}, zap.New(core))
	}()
	awaitCall := func() {
		t.Helper()
		select {
		case <-called:
		case <-time.After(5 * time.Second):
			t.Fatal("heartbeat did not emit")
		}
	}
	awaitCall()
	ticks <- time.Now()
	awaitCall()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat did not stop")
	}
	if logs.Len() != 2 {
		t.Fatalf("wanted startup and periodic heartbeat, got %d", logs.Len())
	}
}

type blockedArchiveLogSink struct {
	writeStarted, syncStarted, release chan struct{}
	writeOnce, syncOnce                sync.Once
}

func (sink *blockedArchiveLogSink) Write(data []byte) (int, error) {
	sink.writeOnce.Do(func() { close(sink.writeStarted) })
	<-sink.release
	return len(data), nil
}

func (sink *blockedArchiveLogSink) Sync() error {
	sink.syncOnce.Do(func() { close(sink.syncStarted) })
	<-sink.release
	return nil
}

func TestArchiveLifecycleWithoutArchive(t *testing.T) {
	manageArchiveLifecycle(context.Background(), nil, nil)()
}

func TestArchiveHeartbeatBlockedLogDoesNotPreventArchiveClose(t *testing.T) {
	config := patterns.DefaultConfig(t.TempDir())
	service, err := patterns.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	sink := &blockedArchiveLogSink{writeStarted: make(chan struct{}), syncStarted: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(sink.release) }) }
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), sink, zapcore.InfoLevel)
	log := zap.New(core)
	closeArchive := manageArchiveLifecycle(context.Background(), service, log)
	t.Cleanup(func() { release(); closeArchive() })
	select {
	case <-sink.writeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat did not enter blocked log write")
	}

	closed, synced := make(chan struct{}), make(chan error, 1)
	go func() {
		closeArchive()
		close(closed)
		synced <- log.Sync()
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked log write prevented archive closure")
	}
	select {
	case <-sink.syncStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("logger sync was not reached after archive closure")
	}
	select {
	case err := <-synced:
		t.Fatalf("blocked log sink unexpectedly synced: %v", err)
	default:
	}
	// Reopening proves that Close released archive ownership while the log sink
	// and the subsequent logger sync are still blocked.
	reopened, err := patterns.Open(config)
	if err != nil {
		t.Fatalf("archive ownership was not released: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-synced:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logger did not sync after the sink was released")
	}
}
