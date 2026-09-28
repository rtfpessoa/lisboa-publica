package app

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Controls exist only in the opt-in test server, never in the application binary.
type metroReplayControls struct {
	mu                sync.Mutex
	mode, pendingMode string
	applyAt           time.Time
	available         bool
	forget            bool
	forgotten         int
	sequence          int
	streams           map[int]context.CancelFunc
	delay             time.Duration
}

func newMetroReplayControls() *metroReplayControls {
	return &metroReplayControls{mode: "positive", available: true, streams: map[int]context.CancelFunc{}}
}

func (c *metroReplayControls) settings(now time.Time) (string, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pendingMode != "" && !now.Before(c.applyAt) {
		c.mode = c.pendingMode
		c.pendingMode = ""
	}
	forget := c.forget
	c.forget = false
	return c.mode, forget, c.delay > 0
}

func (c *metroReplayControls) control(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var input metroReplayCommand
	if json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024)).Decode(&input) != nil {
		w.WriteHeader(400)
		return
	}
	if !validMetroReplayCommand(input) {
		w.WriteHeader(400)
		return
	}
	c.applyCommand(input)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
}

type metroReplayCommand struct {
	Mode      string `json:"mode"`
	Available *bool  `json:"available"`
	Forget    bool   `json:"forget"`
	DelayMS   *int   `json:"delay_ms"`
}

func validMetroReplayCommand(input metroReplayCommand) bool {
	mode := input.Mode == "" || input.Mode == "positive" || input.Mode == "arrival" || input.Mode == "correction" || input.Mode == "freeze"
	delay := input.DelayMS == nil || *input.DelayMS >= 0 && *input.DelayMS <= 100
	return mode && delay
}
func (c *metroReplayControls) applyCommand(input metroReplayCommand) {
	c.mu.Lock()
	if input.Mode != "" {
		c.pendingMode = input.Mode
		c.applyAt = time.Now().UTC().Truncate(time.Second).Add(time.Second)
	}
	if input.DelayMS != nil {
		c.delay = time.Duration(*input.DelayMS) * time.Millisecond
	}
	c.forget = c.forget || input.Forget
	var cancel []context.CancelFunc
	if input.Available != nil {
		c.available = *input.Available
		if !c.available {
			for _, f := range c.streams {
				cancel = append(cancel, f)
			}
		}
	}
	c.mu.Unlock()
	for _, f := range cancel {
		f()
	}

}

func (c *metroReplayControls) state(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"mode": c.mode, "pending_mode": c.pendingMode, "available": c.available, "forgotten": c.forgotten, "streams": len(c.streams), "request_delay_ms": c.delay.Milliseconds(), "write_delay_ms": c.delay.Milliseconds()})
}

// Each incoming request and response write receives a declared one-way synthetic delay.
// This is a controlled application relay, not a claim about packet RTT or radio hardware.
func (c *metroReplayControls) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c.mu.Lock()
		delay := c.delay
		available := c.available
		c.mu.Unlock()
		if delay > 0 {
			select {
			case <-req.Context().Done():
				return
			case <-time.After(delay):
			}
		}
		if req.URL.Path == "/api/v1/metro/live/stream" {
			if !available {
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(503)
				return
			}
			ctx, cancel := context.WithCancel(req.Context())
			defer cancel()
			c.mu.Lock()
			c.sequence++
			id := c.sequence
			c.streams[id] = cancel
			available = c.available
			c.mu.Unlock()
			defer func() { c.mu.Lock(); delete(c.streams, id); c.mu.Unlock() }()
			if !available {
				w.Header().Set("Retry-After", "5")
				w.WriteHeader(503)
				return
			}
			req = req.WithContext(ctx)
		}
		next.ServeHTTP(&metroReplayWriter{ResponseWriter: w, ctx: req.Context(), delay: delay}, req)
	})
}

type metroReplayWriter struct {
	http.ResponseWriter
	ctx   context.Context
	delay time.Duration
}

func (w *metroReplayWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *metroReplayWriter) Write(raw []byte) (int, error) {
	if w.delay > 0 {
		select {
		case <-w.ctx.Done():
			return 0, w.ctx.Err()
		case <-time.After(w.delay):
		}
	}
	return w.ResponseWriter.Write(raw)
}
