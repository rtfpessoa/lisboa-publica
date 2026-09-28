package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	Base             http.RoundTripper
	mu               sync.Mutex
	limit            int
	requests         []time.Time
	sources          map[string]*sourceBudget
	now              func() time.Time
	protected        map[string]int
	protectedTotal   int
	positionDeferred uint64
}

type positionRequestKey struct{}
type positionRequestWork struct{ remaining int }

var errPositionDeferred = errors.New("positions budget deferred")

// ProtectUpstreamWork reserves bounded redirect-chain headroom while due work
// runs. Every actual attempt still crosses the hard rolling-window admission.
func ProtectUpstreamWork(client *http.Client, host string, attempts int) func() {
	t, ok := client.Transport.(*BudgetTransport)
	if !ok {
		return func() {}
	}
	attempts = min(max(attempts, 1), 32)
	t.mu.Lock()
	if t.protected == nil {
		t.protected = map[string]int{}
	}
	t.protected[host] += attempts
	t.protectedTotal += attempts
	t.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.protected[host] -= attempts
			if t.protected[host] == 0 {
				delete(t.protected, host)
			}
			t.protectedTotal -= attempts
		})
	}
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
	return t.claimPriority(host, false)
}
func (t *BudgetTransport) claimPriority(host string, positions bool) error {
	return t.claimWork(upstreamWork{host: host, positions: positions, chain: 3})
}

type upstreamWork struct {
	host      string
	positions bool
	chain     int
}

func (t *BudgetTransport) claimWork(work upstreamWork) error {
	host := work.host
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	source, err := t.sourceForWork(host)
	if err != nil {
		return err
	}
	return t.admitSourceWork(source, work, now)
}
func (t *BudgetTransport) sourceForWork(host string) (*sourceBudget, error) {
	if source := t.sources[host]; source != nil {
		return source, nil
	}
	if len(t.sources) >= maxRateEntries {
		return nil, fmt.Errorf("upstream source budget capacity exhausted")
	}
	source := &sourceBudget{}
	t.sources[host] = source
	return source, nil
}
func (t *BudgetTransport) admitSourceWork(source *sourceBudget, work upstreamWork, now time.Time) error {
	host := work.host
	if now.Before(source.until) {
		return fmt.Errorf("upstream source cooling down; retry on next scheduled refresh")
	}

	limit, window := sourceLimit(host)
	source.requests = recentRequests(source.requests, now.Add(-window))
	t.requests = recentRequests(t.requests, now.Add(-time.Minute))
	if len(t.requests) >= t.limit || len(source.requests) >= limit {
		return fmt.Errorf("upstream request budget exhausted; retry on next scheduled refresh")
	}
	if err := t.positionWorkHeadroom(source, work, limit); err != nil {
		return err
	}

	t.requests = append(t.requests, now)
	source.requests = append(source.requests, now)
	return nil
}

func (t *BudgetTransport) positionWorkHeadroom(source *sourceBudget, work upstreamWork, limit int) error {
	if !work.positions {
		return nil
	}
	global := len(t.requests) + 100 + t.protectedTotal + work.chain
	host := len(source.requests) + 20 + t.protected[work.host] + work.chain
	if global > t.limit || host > limit {
		t.positionDeferred++
		return fmt.Errorf("positions deferred to protect shared upstream headroom")
	}
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
	work, _ := r.Context().Value(positionRequestKey{}).(*positionRequestWork)
	chain := 3
	if work != nil {
		chain = work.remaining
		if chain < 1 {
			return nil, fmt.Errorf("positions redirect chain exhausted")
		}
	}
	if err := t.claimWork(upstreamWork{host: host, positions: work != nil, chain: chain}); err != nil {
		if work != nil {
			return nil, fmt.Errorf("%w: %s", errPositionDeferred, err)
		}
		return nil, err
	}
	if work != nil {
		work.remaining--
	}
	response, err := t.Base.RoundTrip(r)
	// A caller's shorter deadline does not establish an origin-wide outage.
	// Actual provider throttling still takes priority over caller cancellation.
	providerLimited := response != nil && (response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable)
	if err == nil || r.Context().Err() == nil || providerLimited {
		t.response(host, response, err)
	}
	return response, err
}

func positionRequestContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, positionRequestKey{}, &positionRequestWork{remaining: 3})
}

func (t *BudgetTransport) positionsHeadroom(host string) (bool, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.requests = recentRequests(t.requests, now.Add(-time.Minute))
	source := t.sources[host]
	count := 0
	var until time.Time
	if source != nil {
		source.requests = recentRequests(source.requests, now.Add(-time.Minute))
		count = len(source.requests)
		until = source.until
	}
	limit, _ := sourceLimit(host)
	return !now.Before(until) && len(t.requests)+100+t.protectedTotal+3 <= t.limit && count+20+t.protected[host]+3 <= limit, until
}

// CheckUpstreamRedirect retains the initial TLS origin, including Metro's fixed port 8243.
func CheckUpstreamRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 || len(via) >= 3 {
		return http.ErrUseLastResponse
	}
	initial := via[0].URL
	effectivePort := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		return "443"
	}
	if initial.Scheme != "https" || req.URL.Scheme != "https" || initial.User != nil || req.URL.User != nil || !strings.EqualFold(req.URL.Hostname(), initial.Hostname()) || effectivePort(req.URL) != effectivePort(initial) {
		return http.ErrUseLastResponse
	}
	return nil
}
