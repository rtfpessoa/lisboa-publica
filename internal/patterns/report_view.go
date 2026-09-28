package patterns

import (
	"math"
	"sort"
	"strings"
)

type metricBins struct {
	count             int64
	lower, upper      float64
	lowBins, highBins map[int]int64
}
type reportGroup struct {
	journeys      map[string]bool
	report        EvaluationReport
	days          map[string]bool
	own, official metricBins
	bad           bool
}
type reportBuilder struct {
	groups                          map[string]*reportGroup
	associations, histograms, dates int
}

func (b *reportBuilder) add(a Aggregate) {
	if !strings.HasPrefix(a.Kind, "report-") && !strings.HasPrefix(a.Kind, "evaluation-") {
		return
	}
	parts := strings.SplitN(a.Kind, ":", 2)
	if len(parts) != 2 {
		return
	}
	g := b.group(a, parts[1])
	b.addReportDay(g, a.Date)
	if counter := reportCounter(&g.report, parts[0]); counter != nil {
		*counter += a.Count
		return
	}
	switch parts[0] {
	case "report-journeys":
		b.addReportAssociation(g, a.EpisodeHash)
	case "report-unassociated", "report-distinct-incomplete":
		g.report.JourneyCountComplete = false
	case "evaluation-own", "evaluation-official":
		b.addReportErrors(g, a, parts[0])
	}
}

func (b *reportBuilder) group(a Aggregate, function string) *reportGroup {
	key := digest([]any{a.Route, a.Direction, a.Stop, resolutionProfile(a.Profile), a.Calendar, a.Reference, a.Condition, a.Mode, a.Horizon, a.Cohort, function})
	if b.groups == nil {
		b.groups = map[string]*reportGroup{}
	}
	g := b.groups[key]
	if g == nil {
		g = &reportGroup{report: EvaluationReport{Route: a.Route, Direction: a.Direction, Profile: resolutionProfile(a.Profile), Condition: a.Condition, Mode: a.Mode, Horizon: a.Horizon, Cohort: a.Cohort, Function: function, Support: "proxy_same_source", JourneyCountComplete: true}, days: map[string]bool{}, journeys: map[string]bool{}}
		b.groups[key] = g
	}
	if a.Reference == providerReference && g.report.Support == "proxy_same_source" {
		g.report.Support = "proxy_published_stop_status"
	}
	return g
}

func reportCounter(r *EvaluationReport, kind string) *int64 {
	var counter *int64
	switch kind {
	case "report-issued":
		counter = &r.Cases
	case "report-official":
		counter = &r.OfficialAvailable
	case "report-own":
		counter = &r.OwnAvailable
	case "report-paired":
		counter = &r.Paired
	case "report-band":
		counter = &r.BandCases
	case "report-band-certain":
		counter = &r.BandCertain
	case "report-band-possible":
		counter = &r.BandPossible
	}
	return counter
}

func (b *reportBuilder) addReportDay(g *reportGroup, date string) {
	if g.days[date] {
		return
	}
	if b.dates >= maxEngineAggregates {
		g.report.Support = "proxy_same_source_partial_summary"
		return
	}
	g.days[date] = true
	b.dates++
}

func (b *reportBuilder) addReportAssociation(g *reportGroup, identity string) {
	if identity == "" {
		g.report.JourneyCountComplete = false
		return
	}
	if b.associations >= maxEngineAggregates && !g.journeys[identity] {
		g.report.JourneyCountComplete = false
		return
	}
	if !g.journeys[identity] {
		b.associations++
	}
	g.journeys[identity] = true
}

func (b *reportBuilder) addReportErrors(g *reportGroup, a Aggregate, kind string) {
	m := &g.own
	if kind == "evaluation-official" {
		m = &g.official
	}
	m.count += a.Count
	m.lower += a.LowerSum
	m.upper += a.UpperSum
	if m.lowBins == nil {
		m.lowBins, m.highBins = map[int]int64{}, map[int]int64{}
	}
	if a.Resolution <= 0 || !a.ErrorHistogram {
		g.bad = true
		return
	}
	b.addReportHistogram(g, m.lowBins, int(a.LowerBucket*a.Resolution), a.Count)
	b.addReportHistogram(g, m.highBins, int((a.Bucket+1)*a.Resolution), a.Count)
}

func (b *reportBuilder) addReportHistogram(g *reportGroup, bins map[int]int64, key int, count int64) {
	if _, exists := bins[key]; !exists {
		if b.histograms >= maxEngineAggregates {
			g.bad = true
			g.report.Support = "proxy_same_source_partial_summary"
			return
		}
		b.histograms++
	}
	bins[key] += count
}

func quantile90(bins map[int]int64, n int64) *float64 {
	if n == 0 {
		return nil
	}
	rank := int64(math.Ceil(.9 * float64(n)))
	keys := []int{}
	for k := range bins {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var count int64
	for _, k := range keys {
		count += bins[k]
		if count >= rank {
			v := float64(k)
			return &v
		}
	}
	return nil
}
func metric(m metricBins) (*float64, *float64, *float64, *float64) {
	if m.count == 0 {
		return nil, nil, nil, nil
	}
	low, high := m.lower/float64(m.count), m.upper/float64(m.count)
	return &low, &high, quantile90(m.lowBins, m.count), quantile90(m.highBins, m.count)
}
func (b *reportBuilder) finish() []EvaluationReport {
	out := []EvaluationReport{}
	for _, g := range b.groups {
		r := g.report
		r.Days = len(g.days)
		r.Journeys = int64(len(g.journeys))
		r.Evaluated = max(g.own.count, g.official.count)
		r.MAEOwnLower, r.MAEOwnUpper, r.P90OwnLower, r.P90OwnUpper = metric(g.own)
		r.MAEOfficialLower, r.MAEOfficialUpper, r.P90OfficialLower, r.P90OfficialUpper = metric(g.official)
		if g.bad {
			r.P90OwnLower = nil
			r.P90OwnUpper = nil
			r.P90OfficialLower = nil
			r.P90OfficialUpper = nil
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return digest(out[i]) < digest(out[j]) })
	return out
}
