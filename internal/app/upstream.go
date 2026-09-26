package app

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type sourceBudget struct {
	requests []time.Time
	until    time.Time
	failures int
}

// BudgetTransport shares global attempt limits, source budgets and cooldowns across collectors.
type BudgetTransport struct {
	Base     http.RoundTripper
	mu       sync.Mutex
	limit    int
	requests []time.Time
	sources  map[string]*sourceBudget
	now      func() time.Time
}

// NewBudgetTransport caps actual attempts below the subscribed quota, including OAuth and redirects.
func NewBudgetTransport(limit int) *BudgetTransport {
	if limit < 1 || limit > upstreamRequestsPerMinute {
		limit = upstreamRequestsPerMinute
	}
	return &BudgetTransport{Base: http.DefaultTransport, limit: limit, sources: map[string]*sourceBudget{}, now: time.Now}
}

func recentRequests(requests []time.Time, cutoff time.Time) []time.Time {
	expired := 0
	for expired < len(requests) && !requests[expired].After(cutoff) {
		expired++
	}
	return append(requests[:0], requests[expired:]...)
}

func sourceLimit(host string) (int, time.Duration) {
	switch host {
	case "api.carrismetropolitana.pt":
		return 40, time.Second
	case "go.tmlmobilidade.pt":
		return 120, time.Minute
	default:
		return upstreamRequestsPerMinute, time.Minute
	}
}

func (t *BudgetTransport) claim(host string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	source := t.sources[host]
	if source == nil {
		if len(t.sources) >= maxRateEntries {
			return fmt.Errorf("upstream source budget capacity exhausted")
		}
		source = &sourceBudget{}
		t.sources[host] = source
	}
	if now.Before(source.until) {
		return fmt.Errorf("upstream source cooling down; retry on next scheduled refresh")
	}
	limit, window := sourceLimit(host)
	source.requests = recentRequests(source.requests, now.Add(-window))
	t.requests = recentRequests(t.requests, now.Add(-time.Minute))
	if len(t.requests) >= t.limit || len(source.requests) >= limit {
		return fmt.Errorf("upstream request budget exhausted; retry on next scheduled refresh")
	}
	t.requests = append(t.requests, now)
	source.requests = append(source.requests, now)
	return nil
}

func retryAfter(value string, now time.Time) (time.Time, bool) {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/time.Second) {
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	at, err := http.ParseTime(value)
	return at, err == nil && !at.Before(now)
}

func (t *BudgetTransport) response(host string, response *http.Response, failure error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	source := t.sources[host]
	if failure == nil && response.StatusCode < 400 {
		if !t.now().Before(source.until) {
			source.failures = 0
			source.until = time.Time{}
		}
		return
	}
	limited := failure != nil || response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable
	if !limited {
		return
	}
	source.failures = min(source.failures+1, 5)
	now := t.now()
	delay := min(30*time.Second*time.Duration(1<<(source.failures-1)), 5*time.Minute)
	until := now.Add(delay)
	if response != nil {
		if at, ok := retryAfter(response.Header.Get("Retry-After"), now); ok {
			until = at
		}
	}
	if until.After(source.until) {
		source.until = until
	}
}

// RoundTrip counts every actual attempt; callers never busy-retry during a cooldown.
func (t *BudgetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	if err := t.claim(host); err != nil {
		return nil, err
	}
	response, err := t.Base.RoundTrip(r)
	t.response(host, response, err)
	return response, err
}
