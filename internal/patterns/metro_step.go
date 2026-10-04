package patterns

import "time"

type metroSample struct {
	receipt  Receipt
	topology Topology
	config   Config
	now      time.Time
	gap      bool
	presence intermediatePresence
	current  map[string]priorRow
	signals  map[string][]signal
	stats    MetroSampleStats
}

// MetroSampleStats is a read-only summary of one processed Metro sample. It is
// diagnostic only and never feeds inference or persistence.
type MetroSampleStats struct {
	SampledAt          time.Time
	Gap                bool
	Empty              bool
	Rows               int
	Contexts           int
	Admitted           int
	FirstSlots         int
	WithPrior          int
	ZeroETA            int
	RejectedRoute      int
	RejectedDuplicate  int
	RejectedClock      int
	RejectedContinuity int
	Signals            int
	SignalsApplied     int
	GroupsCreated      int
	GroupsDeleted      int
	ActiveGroups       int
	IntermediateCuts   int64
	Gaps               int64
}

func (e *engine) step(receipt Receipt, topology Topology, config Config) {
	if !e.LastReceipt.IsZero() && !receipt.ReceivedAt.After(e.LastReceipt) {
		return
	}
	sample := e.prepareMetroStep(receipt, topology, config)
	sample.stats.Gap = sample.gap
	sample.stats.Empty = len(receipt.Rows) == 0 && receipt.Error == ""
	sample.stats.IntermediateCuts, sample.stats.Gaps = e.IntermediateCuts, e.Gaps
	if receipt.Error != "" {
		sample.stats.SampledAt, sample.stats.ActiveGroups = sample.now, activeMetroGroups(e.Groups)
		e.LastMetroStats = sample.stats
		return
	}
	e.collectMetroRows(&sample)
	e.applyMetroPresence(&sample)
	e.supportMetroGroups(sample)
	e.Previous = sample.current
	e.pruneMetroCases(sample)
	e.Live = e.forecasts(receipt, topology, config)
	e.issueMetroCases(sample)
	sample.stats.ActiveGroups, sample.stats.SampledAt = activeMetroGroups(e.Groups), sample.now
	e.LastMetroStats = sample.stats
}

func activeMetroGroups(groups map[string]*group) int {
	active := 0
	for _, g := range groups {
		if g.Active {
			active++
		}
	}
	return active
}

func (e *engine) prepareMetroStep(receipt Receipt, topology Topology, config Config) metroSample {
	now := receipt.ReceivedAt
	e.Outcomes = []Forecast{}
	if e.Selections == nil {
		e.Selections = map[string]selection{}
	}
	if e.DirtyDays == nil {
		e.DirtyDays = map[string]bool{}
	}
	if e.Profile != topology.Profile {
		e.resetContinuity()
		e.Profile = topology.Profile
	}
	e.updateConditions(receipt, topology)
	e.Topology, e.Condition = topology, receipt.ServiceCondition
	gap := receipt.DeliveryGap || !e.LastReceipt.IsZero() && now.Sub(e.LastReceipt) > 60*time.Second
	if gap {
		e.Gaps++
		e.resetContinuity()
	}
	e.LastReceipt = now
	e.trim(now, config)
	return metroSample{receipt: receipt, topology: topology, config: config, now: now, gap: gap, presence: intermediatePresence{map[string]bool{}, map[string]bool{}}, current: map[string]priorRow{}, signals: map[string][]signal{}, stats: MetroSampleStats{Rows: len(receipt.Rows)}}
}

func (e *engine) recordMetroCoverage(sample metroSample, row Row, route string) {
	coverage := baseAggregate(aggregateRequest{sample.now, route, row.Destination, row.Stop, row.Platform, e.Profile, sample.receipt.condition(route), "receipts"}, sample.config)
	coverage.Count, coverage.KnownAt = 1, sample.now.UnixNano()
	e.add(coverage)
}
