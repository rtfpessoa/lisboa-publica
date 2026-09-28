package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"lisboapublica/internal/api"
)

// RefreshArrivals performs one collection cycle. Readers only register interest;
// cancellation of a popup never cancels another reader's shared upstream request.
func (f *Fetcher) RefreshArrivals(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); f.refreshCMArrivals(ctx) }()
	f.refreshCP(ctx)
	wg.Wait()
}
func (f *Fetcher) refreshCMArrivals(ctx context.Context) {
	var wg sync.WaitGroup
	for _, stop := range f.Cache.arrivals.claimCM(time.Now()) {
		wg.Add(1)
		go func() { defer wg.Done(); defer f.Cache.arrivals.releaseCM(stop); f.refreshCMStop(ctx, stop) }()
	}
	wg.Wait()
}

type cmArrival struct {
	Line      string `json:"line_id"`
	Trip      string `json:"trip_id"`
	Headsign  string `json:"headsign"`
	Scheduled int64  `json:"scheduled_arrival_unix"`
	Estimated int64  `json:"estimated_arrival_unix"`
	Observed  int64  `json:"observed_arrival_unix"`
}

func (f *Fetcher) refreshCMStop(parent context.Context, stop string) {
	ctx, cancel := context.WithTimeout(parent, providerRefreshInterval)
	defer cancel()
	state, _ := f.Cache.state("")
	if static := state.Static["cm"]; static != nil {
		now := time.Now().UTC()
		v := newCMArrivalSnapshot(static, stop, now)
		blob, err := f.fetchArrivalBody(ctx, f.CM+"/arrivals/by_stop/"+url.PathEscape(strings.TrimPrefix(stop, "cm:")))
		if err == nil {
			err = decodeCMArrivals(ctx, blob, stop, now, &v)
		}
		if err != nil {
			f.restoreCMPlanned(stop, now, &v)
		}
		f.publishArrivals(stop, v)
	}
}

func newCMArrivalSnapshot(static *StaticData, stop string, now time.Time) arrivalSnapshot {
	v := arrivalSnapshot{static: static, rows: []api.Arrival{}, expires: now.Add(arrivalInterest)}
	local := now.In(lisbon)
	end := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, lisbon).UTC()
	v.availability = api.ArrivalAvailability{Status: "ok", PlannedStatus: "ok", SourceUrl: arrivalSource(stop), CollectedAt: &now, ValidUntil: ptr(v.expires), CoverageUntil: &end}
	return v
}

func (f *Fetcher) restoreCMPlanned(stop string, now time.Time, v *arrivalSnapshot) {
	v.rows = []api.Arrival{}
	v.availability.Status = "error"
	v.availability.PlannedStatus = "unavailable"
	if previous, ok := f.Cache.arrivals.previous(stop, v.static, now); ok {
		v.expires = previous.expires
		v.availability.ValidUntil = ptr(v.expires)
		v.availability.CoverageUntil = previous.availability.CoverageUntil
		v.rows = usableCMPlanned(previous.rows, now)
		if len(v.rows) > 0 {
			v.availability.PlannedStatus = "ok"
		}
	}
}

func usableCMPlanned(rows []api.Arrival, now time.Time) []api.Arrival {
	out := []api.Arrival{}
	for _, r := range rows {
		if r.ScheduledAt == nil || r.ScheduledAt.Before(now) {
			continue
		}
		r.Kind = "scheduled"
		r.ExpectedAt = nil
		r.VehicleId = nil
		r.SourceUpdatedAt = nil
		r.ObservedAt = nil
		out = append(out, r)
	}
	return out
}
func validArrivalUnix(v int64) bool { return v >= arrivalEarliestTimestamp && v < cpLatestTimestamp }
func (f *Fetcher) fetchArrivalBody(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "LisboaPublica/1.0 (independent transit dashboard)")
	res, err := f.Client.Do(req)
	if err == nil {
		defer res.Body.Close()
		return readArrivalBody(res)
	}
	return nil, err
}

func readArrivalBody(res *http.Response) ([]byte, error) {
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("arrivals upstream status")
	}
	blob, err := io.ReadAll(io.LimitReader(res.Body, arrivalBodyLimit+1))
	if len(blob) > arrivalBodyLimit {
		return nil, fmt.Errorf("arrivals response capacity")
	}
	return blob, err
}
func (f *Fetcher) publishArrivals(stop string, v arrivalSnapshot) {
	// Publication is conditional on the exact immutable static generation used.
	// Hold Cache before the store everywhere to avoid lock inversion.
	f.Cache.mu.RLock()
	defer f.Cache.mu.RUnlock()
	operator, _, _ := strings.Cut(stop, ":")
	if f.Cache.current.Static[operator] == v.static {
		f.Cache.arrivals.publish(stop, v)
		f.recordArrivalHistory(operator, stop, v)
	}
}

func (f *Fetcher) cmArrivalLoop(ctx context.Context) {
	for {
		f.refreshCMArrivals(ctx)
		timer := time.NewTimer(providerRefreshInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
