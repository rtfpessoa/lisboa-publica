package patterns

import (
	"sort"
	"time"
)

type calibrationContext struct {
	forecast Forecast
	horizon  int32
	floor    string
	now      time.Time
}

func (e *engine) radius(f Forecast, now time.Time, c Config) (*float64, int64) {
	if f.OwnAt == nil {
		return nil, 0
	}
	context := calibrationContext{f, horizon(f.OwnAt.Sub(f.IssuedAt).Seconds()), now.In(lisbon).AddDate(0, 0, -c.CalibrationDays).Format("2006-01-02"), now}
	bins, n := e.calibrationBins(context)
	return calibratedRadius(bins, n), n
}

func (e *engine) calibrationBins(q calibrationContext) (map[int32]int64, int64) {
	bins := map[int32]int64{}
	var count int64
	for _, a := range e.Aggregates {
		if !q.matches(a) {
			continue
		}
		count += a.Count
		bins[(a.Bucket+1)*a.Resolution] += a.Count
	}
	return bins, count
}

func (q calibrationContext) matches(a Aggregate) bool {
	if !q.matchesIdentity(a) || !q.matchesContext(a) {
		return false
	}
	if a.Date < q.floor || a.KnownAt >= q.now.UnixNano() || a.Resolution <= 0 {
		return false
	}
	return true
}

func (q calibrationContext) matchesIdentity(a Aggregate) bool {
	f := q.forecast
	if a.Kind != "calibration:"+f.Function || a.Mode != f.Mode {
		return false
	}
	return resolutionProfile(a.Profile) == resolutionProfile(f.Profile) && a.Reference == routeReference(f.Route)
}

func (q calibrationContext) matchesContext(a Aggregate) bool {
	f := q.forecast
	if a.Route != f.Route || a.Direction != f.Direction || a.Stop != f.Stop {
		return false
	}
	return a.Condition == f.Condition && a.Horizon == q.horizon
}

func calibratedRadius(bins map[int32]int64, n int64) *float64 {
	rank := (4*(n+1) + 4) / 5
	if n == 0 || rank > n {
		return nil
	}
	keys := []int{}
	for bucket := range bins {
		keys = append(keys, int(bucket))
	}
	sort.Ints(keys)
	var count int64
	for _, bucket := range keys {
		count += bins[int32(bucket)]
		if count >= rank {
			value := float64(bucket)
			return &value
		}
	}
	return nil
}

func (e *engine) calibrateForecast(f *Forecast, now time.Time, c Config) {
	f.LowerAt, f.UpperAt, f.CalibrationSamples = nil, nil, 0
	if f.OwnAt == nil {
		return
	}
	radius, n := e.radius(*f, now, c)
	f.CalibrationSamples = n
	if radius == nil {
		return
	}
	lower := f.OwnAt.Add(-time.Duration(*radius * float64(time.Second)))
	upper := f.OwnAt.Add(time.Duration(*radius * float64(time.Second)))
	f.LowerAt, f.UpperAt = &lower, &upper
}
