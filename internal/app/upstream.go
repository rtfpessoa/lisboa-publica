package app

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// A single shared transport bounds all provider and token calls for this process.
// Rejected requests wait for the next scheduled refresh, never busy-retry.
type BudgetTransport struct {
	Base     http.RoundTripper
	mu       sync.Mutex
	limit    int
	requests []time.Time
	now      func() time.Time
}

// NewBudgetTransport creates a transport capped below the provider subscription quota.
func NewBudgetTransport(limit int) *BudgetTransport {
	if limit < 1 || limit > upstreamRequestsPerMinute {
		limit = upstreamRequestsPerMinute
	}
	return &BudgetTransport{Base: http.DefaultTransport, limit: limit, now: time.Now}
}

// RoundTrip counts every outbound attempt against the rolling minute budget.
func (t *BudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	now := t.now()
	cutoff := now.Add(-time.Minute)
	expiredCount := 0
	for expiredCount < len(t.requests) && !t.requests[expiredCount].After(cutoff) {
		expiredCount++
	}
	if expiredCount > 0 {
		t.requests = append(t.requests[:0], t.requests[expiredCount:]...)
	}
	allowed := len(t.requests) < t.limit
	if allowed {
		t.requests = append(t.requests, now)
	}
	t.mu.Unlock()
	if !allowed {
		return nil, fmt.Errorf("upstream request budget exhausted; retry on next refresh")
	}
	return t.Base.RoundTrip(r)
}
