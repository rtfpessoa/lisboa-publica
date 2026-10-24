package app

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"lisboapublica/internal/patterns"
)

// Fixture decoding and archive setup are outside each timed interval.
func BenchmarkMetroDiagnosticOverhead(b *testing.B) {
	data, static := diagnosticBenchmarkInputs(b)
	for _, enabled := range []bool{false, true} {
		name := "baseline"
		if enabled {
			name = "instrumented"
		}
		b.Run("enqueue/"+name, func(b *testing.B) {
			m := &MetroClient{Cache: NewCache()}
			m.historyQueue = make(chan metroHistoryTask, 1)
			if enabled {
				m.diagnostics = newMetroDeliveryDiagnostics()
			}
			now := time.Now().UTC()
			var lockPath time.Duration
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				m.enqueuePatterns(data, static, now)
				lockPath += time.Since(start)
				task := <-m.historyQueue
				m.historyMu.Lock()
				m.historyBytes -= task.Bytes
				m.historyMu.Unlock()
			}
			b.ReportMetric(float64(lockPath.Nanoseconds())/float64(b.N), "lockpath-ns/op")
		})
		b.Run("worker/"+name, func(b *testing.B) {
			history, err := patterns.Open(patterns.DefaultConfig(b.TempDir()))
			if err != nil {
				b.Fatal(err)
			}
			defer history.Close()
			fifo, err := history.AcquireMetroFIFO()
			if err != nil {
				b.Fatal(err)
			}
			defer fifo.Close()
			m := &MetroClient{Cache: NewCache(), History: history}
			m.historyFIFO, m.historyQueue = fifo, make(chan metroHistoryTask, 1)
			if enabled {
				m.diagnostics = newMetroDeliveryDiagnostics()
			}
			now := time.Now().UTC()
			task := metroHistoryTask{Data: *data, Static: static, At: now, Bytes: 1, queuedAt: time.Now()}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				task.At = now.Add(time.Duration(i) * time.Second)
				m.historyBytes++
				m.deliverPatternHistory(task, 0)
			}
		})
	}
}

func BenchmarkMetroDiagnosticLogEncoding(b *testing.B) {
	for _, enhanced := range []bool{false, true} {
		name := "baseline"
		var fields []zap.Field
		if enhanced {
			name = "enhanced"
			fields = []zap.Field{zap.Object("delivery", metroDiagnosticSnapshot{fifo: true, queueAvailable: true})}
		}
		b.Run(name, func(b *testing.B) {
			enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
			entry := zapcore.Entry{Level: zap.DebugLevel, Message: "metro sample"}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				encoded, err := enc.EncodeEntry(entry, fields)
				if err != nil {
					b.Fatal(err)
				}
				encoded.Free()
			}
		})
	}
	b.Run("snapshot-and-encoding", func(b *testing.B) {
		history, err := patterns.Open(patterns.DefaultConfig(b.TempDir()))
		if err != nil {
			b.Fatal(err)
		}
		defer history.Close()
		enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
		m := &MetroClient{History: history, Log: zap.New(zapcore.NewCore(enc, zapcore.AddSync(io.Discard), zap.DebugLevel))}
		m.historyQueue, m.diagnostics = make(chan metroHistoryTask, 1), newMetroDeliveryDiagnostics()
		clock := time.Now()
		m.diagnostics.now = func() time.Time { clock = clock.Add(time.Minute); return clock }
		entry := zapcore.Entry{Level: zap.DebugLevel, Message: "metro sample"}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			field, ok := m.metroDeliveryDiagnosticField(true)
			if !ok {
				b.Fatal("diagnostic snapshot unexpectedly unavailable")
			}
			encoded, err := enc.EncodeEntry(entry, []zap.Field{field})
			if err != nil {
				b.Fatal(err)
			}
			encoded.Free()
		}
	})
}

func diagnosticBenchmarkInputs(b *testing.B) (*MetroData, *StaticData) {
	b.Helper()
	var frames []struct {
		Data MetroData `json:"data"`
	}
	var static StaticData
	readDiagnosticBenchmarkFixture(b, "metro-debug-direct-capture", &frames)
	readDiagnosticBenchmarkFixture(b, "metro-debug-static", &static)
	if len(frames) != 8 || static.Schedule == nil || len(static.Schedule.Trips) != 2527 || len(frames[0].Data.Stations) != 50 || len(frames[0].Data.Waits) != 111 {
		b.Fatal("unexpected captured Metro fixture dimensions")
	}
	return &frames[0].Data, &static
}

func readDiagnosticBenchmarkFixture(b *testing.B, name string, target any) {
	b.Helper()
	f, err := os.Open("testdata/metro-20260928/" + name + ".json.gz")
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		b.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		b.Fatal(err)
	}
}
