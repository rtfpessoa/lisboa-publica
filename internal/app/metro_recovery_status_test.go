package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lisboapublica/internal/patterns"
)

func TestMetroEmptyStoppedFIFORejectsLateRefreshAndDirectRecord(t *testing.T) {
	s, _, data, now := metroLiveFixture(t)
	fifo, err := s.Patterns.AcquireMetroFIFO()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &MetroClient{Cache: s.Cache, History: s.Patterns, Store: &Store{}, ClientID: "test", Secret: "test"}
	m.data, m.lastPersist = data, time.Now()
	m.historyQueue, m.historyFIFO = make(chan metroHistoryTask, 1), fifo
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/estadoLinha/todos":
			_, _ = w.Write([]byte(`{"codigo":200,"resposta":{"amarela":"ok","amarela_curta":"normal","azul":"ok","azul_curta":"normal","verde":"ok","verde_curta":"normal","vermelha":"ok","vermelha_curta":"normal"}}`))
		case "/tempoEspera/Estacao/todos":
			_, _ = fmt.Fprintf(w, `{"codigo":200,"resposta":[{"stop_id":"RM","cais":"1","hora":"%s","comboio":"7","tempoChegada1":120,"destino":"54"}]}`, now.In(lisbon).Format("20060102150405"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	m.Client, m.Base, m.tokenValue, m.expires = upstream.Client(), upstream.URL, "test-token", time.Now().Add(time.Hour)
	done := make(chan struct{})
	go func() { m.historyLoop(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("empty worker did not stop")
	}
	before := s.Patterns.MetroSampleStats()
	if got := m.Refresh(context.Background()); got == nil {
		t.Fatal("late refresh returned no data")
	}
	cut := s.Patterns.MetroCut().Snapshot()
	if !cut.Pending || m.historyBytes != 0 || len(m.historyQueue) != 0 || !m.historyStopped {
		t.Fatal("late refresh was not rejected with a gap")
	}
	_, err = s.Patterns.RecordMetro(patterns.Receipt{ReceivedAt: now.Add(time.Minute)}, patterns.Topology{})
	if err == nil || s.Patterns.MetroCut().Snapshot() != cut || s.Patterns.MetroSampleStats() != before {
		t.Fatal("direct record crossed the stopped FIFO owner")
	}
}

func TestMetroCaptureAcknowledgementRefreshesHistoryStatus(t *testing.T) {
	for _, variant := range []string{"recovered", "new capture gap", "new history cut", "checkpoint fenced"} {
		t.Run(variant, func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
				t.Fatal(err)
			}
			fifo, err := s.Patterns.AcquireMetroFIFO()
			if err != nil {
				t.Fatal(err)
			}
			defer fifo.Close()
			m := &MetroClient{Cache: s.Cache, History: s.Patterns}
			m.historyFIFO, m.historyQueue = fifo, make(chan metroHistoryTask, 1)
			m.enqueuePatterns(data, d, now)
			m.enqueuePatterns(data, d, now.Add(time.Second))
			generation := m.deliverPatternHistory(<-m.historyQueue, 0)
			m.enqueuePatterns(data, d, now.Add(31*time.Second))
			m.deliverPatternHistory(<-m.historyQueue, generation)
			r := s.Cache.metroRuntime
			r.mu.Lock()
			if r.historyStatus != "paused" || r.historyDeliveryGap || !r.capture.Gap || s.Patterns.MetroCut().Snapshot().Pending {
				r.mu.Unlock()
				t.Fatal("fixture did not recover FIFO before capture")
			}
			input := metroCapturedInput{GapBefore: true, GapVersion: r.capture.GapVersion, ReceivedAt: now.Add(32 * time.Second)}
			raw, err := json.Marshal(input)
			if err != nil {
				r.mu.Unlock()
				t.Fatal(err)
			}
			r.admitCapturedInput(input, raw, "gap", now.Add(32*time.Second))
			r.mu.Unlock()
			r.flushInputsWithWrite(context.Background(), func(ctx context.Context, batch []patterns.MetroEventRecord, at time.Time) error {
				err := s.Patterns.RecordMetroInputs(ctx, batch, at)
				if err == nil {
					r.mu.Lock()
					switch variant {
					case "new capture gap":
						r.markCaptureGap()
					case "new history cut":
						s.Patterns.MetroCut().MarkPending()
					case "checkpoint fenced":
						r.checkpointFenced = true
					}
					r.mu.Unlock()
				}
				return err
			}, now.Add(32*time.Second))
			r.mu.Lock()
			defer r.mu.Unlock()
			if len(r.capture.Queue) != 0 {
				t.Fatal("capture batch was not retired")
			}
			if variant == "recovered" {
				if r.capture.Gap || r.historyStatus != "collecting" {
					t.Fatal("matching capture did not resume history status")
				}
			} else if r.historyStatus != "paused" || (variant == "new capture gap" && !r.capture.Gap) {
				t.Fatal("capture acknowledgement concealed a newer gap or fence")
			}
		})
	}
}

func TestMetroStandaloneAcknowledgementRefreshesHistoryStatus(t *testing.T) {
	for _, newer := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovered", true: "newer cut"}[newer], func(t *testing.T) {
			s, d, data, now := metroLiveFixture(t)
			if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
				t.Fatal(err)
			}
			r := s.Cache.metroRuntime
			r.mu.Lock()
			r.historyStatus, r.historyDeliveryGap = "paused", true
			r.mu.Unlock()
			s.Patterns.InterruptMetroContinuity()
			m := &MetroClient{Cache: s.Cache, History: s.Patterns}
			err := m.recordPatternsWith(data, d, now, func(receipt patterns.Receipt, topology patterns.Topology) (patterns.MetroRecordResult, error) {
				result, err := s.Patterns.RecordMetro(receipt, topology)
				if err == nil && !result.Acknowledged {
					t.Fatal("fixture failed to acknowledge standalone gap")
				}
				if newer {
					s.Patterns.MetroCut().MarkPending()
				}
				return result, err
			})
			if err != nil {
				t.Fatal(err)
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			if newer {
				if !r.historyDeliveryGap || r.historyStatus != "paused" || !s.Patterns.MetroCut().Snapshot().Pending {
					t.Fatal("standalone acknowledgement concealed a newer cut")
				}
			} else if r.historyDeliveryGap || r.historyStatus != "collecting" {
				t.Fatal("standalone acknowledgement did not recover status")
			}
		})
	}
}

func TestMetroUnrelatedCaptureDoesNotClearHistoryPause(t *testing.T) {
	s, _, _, now := metroLiveFixture(t)
	if err := s.Cache.BindMetroHistory(s.Patterns); err != nil {
		t.Fatal(err)
	}
	r := s.Cache.metroRuntime
	r.mu.Lock()
	r.historyStatus = "paused"
	input := metroCapturedInput{ReceivedAt: now}
	raw, err := json.Marshal(input)
	if err != nil {
		r.mu.Unlock()
		t.Fatal(err)
	}
	r.admitCapturedInput(input, raw, "normal", now)
	r.mu.Unlock()
	r.flushInputs(context.Background(), s.Patterns, now)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.capture.Queue) != 0 || r.historyStatus != "paused" {
		t.Fatal("unrelated capture cleared a history pause")
	}
}
