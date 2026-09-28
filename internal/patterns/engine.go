package patterns

import (
	"time"
)

const referenceProfile = "metro-positive-zero-triple-v1"
const forecastMode = "official+sequential-proxy-components/v3"
const maxEngineAggregates = 100_000
const maxPendingForecasts = 20_000

type signal struct {
	Stop, Platform string
	L, U           time.Time
	Supported      bool
}
type group struct {
	ID, Train, Direction, Route string
	Signals                     []signal
	Active                      bool
	Pairs                       map[string]bool
}
type priorRow struct {
	Clock     time.Time
	Signature string
	Train     string
	ETA       float64
}
type selection struct {
	Episode string
	At      int64
}
type engine struct {
	ColdDays                    map[string]bool
	RecoveryThrough             map[string]int64 `json:"-"`
	Operator                    string
	Capacity                    int
	Replaying                   bool `json:"-"`
	Profile                     string
	Conditions                  map[string]string
	Topology                    Topology
	ReportSeen                  map[string]int64
	Condition                   string
	LastReceipt, LastEvaluation time.Time
	Gaps                        int64
	Aggregates                  map[string]Aggregate
	Cases                       []Forecast
	Groups                      map[string]*group
	Previous                    map[string]priorRow
	Live                        []Forecast
	Selections                  map[string]selection
	Outcomes                    []Forecast
	DirtyDays                   map[string]bool
	Limited                     bool
}

func newEngine() *engine {
	return &engine{Aggregates: map[string]Aggregate{}, Groups: map[string]*group{}, Previous: map[string]priorRow{}, Cases: []Forecast{}, Live: []Forecast{}, Selections: map[string]selection{}, DirtyDays: map[string]bool{}}
}

func groupKey(g groupIdentity) string { return g.Train + "|" + g.Direction + "|" + g.Route }
func contextKey(r Row) string         { return r.Stop + "|" + r.Platform + "|" + r.Destination }

func (e *engine) resetContinuity() {
	e.Groups = map[string]*group{}
	e.Previous = map[string]priorRow{}
	e.Live = []Forecast{}
}

func hourKey(at time.Time) string { local := at.In(lisbon); return local.Format("2006-01-02T15Z07:00") }
func baseAggregate(q aggregateRequest, c Config) Aggregate {
	at, route, direction, stop, platform, profile, condition, kind := q.at, q.route, q.direction, q.stop, q.platform, q.profile, q.condition, q.kind
	local := at.In(lisbon)
	_, offset := local.Zone()
	return Aggregate{Operator: routeOperator(route), Date: local.Format("2006-01-02"), Hour: int32(local.Hour()), Offset: int32(offset), DayType: dayType(at), Calendar: calendarVersion, Route: route, Direction: direction, Stop: stop, Platform: platform, Profile: profile, Reference: routeReference(route), Condition: condition, Kind: kind, Resolution: int32(c.BinSeconds), InputFrom: at.UnixNano()}
}
func (e *engine) add(a Aggregate) {
	if cutoff, exists := e.RecoveryThrough[a.Date]; exists && a.KnownAt <= cutoff {
		return
	}
	if e.ColdDays[a.Date] {
		e.Limited = true
		return
	}
	if e.DirtyDays == nil {
		e.DirtyDays = map[string]bool{}
	}
	key := aggregateKey(a)
	prior := e.Aggregates[key]
	if prior.Count == 0 {
		if !e.admitNewAggregate(a.Date) {
			return
		}
		prior = a
		prior.Count, prior.Sum, prior.LowerSum, prior.UpperSum = 0, 0, 0, 0
	}
	// Capacity-rejected inputs cannot mark an untouched retained day for replacement.
	e.DirtyDays[a.Date] = true
	e.Aggregates[key] = mergeAggregateContribution(prior, a)
}

func (e *engine) admitNewAggregate(date string) bool {
	if len(e.Aggregates) < aggregateCapacity(e) {
		return true
	}
	oldest := e.oldestAggregateDate()
	e.Limited = true
	if oldest >= date || e.DirtyDays[oldest] || e.dayHasLiveAssociation(oldest) {
		return false
	}
	// Drop a complete hot day without changing its published retained generation.
	e.dropHotAggregateDate(oldest)
	return true
}

func (e *engine) oldestAggregateDate() string {
	oldest := ""
	for _, value := range e.Aggregates {
		if oldest == "" || value.Date < oldest {
			oldest = value.Date
		}
	}
	return oldest
}

func (e *engine) dropHotAggregateDate(date string) {
	e.markColdDay(date)
	for key, value := range e.Aggregates {
		if value.Date == date {
			delete(e.Aggregates, key)
		}
	}
}

func mergeAggregateContribution(prior, a Aggregate) Aggregate {
	prior.Count += a.Count
	prior.Sum += a.Sum
	prior.LowerSum += a.LowerSum
	prior.UpperSum += a.UpperSum
	if prior.Count == a.Count {
		prior.InputFrom = a.InputFrom
	} else if prior.InputFrom == 0 || a.InputFrom == 0 {
		prior.InputFrom = 0
	} else {
		prior.InputFrom = min(prior.InputFrom, a.InputFrom)
	}
	prior.KnownAt = max(prior.KnownAt, a.KnownAt)
	return prior
}

// A later association contradiction invalidates its inputs for future reuse.
// Conservative withdrawal removes the affected route/direction/day strata;
// historical forecast values stay unchanged in detail and pending outcomes.
func (e *engine) withdraw(g *group) {
	dates := map[string]bool{}
	for _, s := range g.Signals {
		dates[s.L.In(lisbon).Format("2006-01-02")] = true
		dates[s.U.In(lisbon).Format("2006-01-02")] = true
	}
	for _, f := range e.Cases {
		if f.Episode == g.ID {
			dates[f.IssuedAt.In(lisbon).Format("2006-01-02")] = true
		}
	}
	for k, a := range e.Aggregates {
		if a.Profile == e.Profile && a.Route == g.Route && a.Direction == g.Direction && dates[a.Date] && a.Kind != "receipts" {
			delete(e.Aggregates, k)
			e.DirtyDays[a.Date] = true
		}
	}
	e.Gaps++
}

func (e *engine) trim(now time.Time, c Config) {
	e.trimAggregateDates(now.In(lisbon).AddDate(0, -c.AggregateMonths, 0).Format("2006-01-02"))
	e.trimCasesAndReports(now.AddDate(0, 0, -c.DetailDays))
	e.trimSelections(now.AddDate(0, 0, -max(c.DetailDays, c.CalibrationDays)))
}

func (e *engine) trimAggregateDates(floor string) {
	for date := range e.ColdDays {
		if date < floor {
			delete(e.ColdDays, date)
		}
	}
	for key, a := range e.Aggregates {
		if a.Date < floor {
			delete(e.Aggregates, key)
		}
	}
}

func (e *engine) trimCasesAndReports(earliest time.Time) {
	cases := e.Cases[:0]
	for _, f := range e.Cases {
		if f.IssuedAt.After(earliest) {
			cases = append(cases, f)
		}
	}
	e.Cases = cases
	for key, at := range e.ReportSeen {
		if at < earliest.UnixNano() {
			delete(e.ReportSeen, key)
		}
	}
}

func (e *engine) trimSelections(earliest time.Time) {
	for key, selection := range e.Selections {
		if selection.At < earliest.UnixNano() {
			delete(e.Selections, key)
		}
	}
}

type official struct {
	At, Source time.Time
	Platform   string
	First      bool
}

func horizon(seconds float64) int32 {
	if seconds < 300 {
		return 0
	}
	if seconds < 900 {
		return 1
	}
	if seconds < 1800 {
		return 2
	}
	return 3
}
func selectionKey(f Forecast) string {
	if f.OwnAt == nil {
		return ""
	}
	return digest([]any{f.Episode, f.Stop, f.Platform, horizon(f.OwnAt.Sub(f.IssuedAt).Seconds()), f.Function, f.Mode, f.Profile, routeReference(f.Route)})
}

func (e *engine) markColdDay(date string) {
	if e.ColdDays == nil {
		e.ColdDays = map[string]bool{}
	}
	e.ColdDays[date] = true
}

func (e *engine) dayHasLiveAssociation(date string) bool {
	for _, g := range e.Groups {
		for _, signal := range g.Signals {
			if signal.L.In(lisbon).Format("2006-01-02") == date || signal.U.In(lisbon).Format("2006-01-02") == date {
				return true
			}
		}
	}
	return false
}
