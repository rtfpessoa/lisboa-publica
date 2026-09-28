package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetroReplayUnavailableCancelsStreamsAndReleasesSlots(t *testing.T) {
	c := newMetroReplayControls()
	entered, done := make(chan struct{}), make(chan struct{})
	handler := c.wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/metro/live/stream", nil))
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("stream was not admitted")
	}
	w := httptest.NewRecorder()
	c.control(w, httptest.NewRequest("POST", "/test-replay/control", strings.NewReader(`{"available":false}`)))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream cancellation blocked")
	}
	c.mu.Lock()
	remaining := len(c.streams)
	c.mu.Unlock()
	if remaining != 0 {
		t.Fatal("leaked replay stream", remaining)
	}
	refused := httptest.NewRecorder()
	handler.ServeHTTP(refused, httptest.NewRequest("GET", "/api/v1/metro/live/stream", nil))
	if refused.Code != 503 || refused.Header().Get("Retry-After") != "5" {
		t.Fatal("missing unavailable retry contract")
	}
}
func TestMetroReplayDelayHonoursCancellationAndUnwraps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	underlying := httptest.NewRecorder()
	w := &metroReplayWriter{ResponseWriter: underlying, ctx: ctx, delay: 50 * time.Millisecond}
	if _, err := w.Write([]byte("late")); err == nil || underlying.Body.Len() != 0 {
		t.Fatal("cancelled replay write leaked data")
	}
	w.ctx = context.Background()
	w.delay = 0
	if err := http.NewResponseController(w).Flush(); err != nil || !underlying.Flushed {
		t.Fatal("relay hid response control", err)
	}
	c := newMetroReplayControls()
	for _, body := range []string{`{"mode":"invented"}`, `{"delay_ms":101}`, `{"delay_ms":-1}`} {
		rec := httptest.NewRecorder()
		c.control(rec, httptest.NewRequest("POST", "/test-replay/control", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatal("invalid replay input accepted", body)
		}
	}
}
