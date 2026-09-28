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
}

func (e *engine) step(receipt Receipt, topology Topology, config Config) {
	if !e.LastReceipt.IsZero() && !receipt.ReceivedAt.After(e.LastReceipt) {
		return
	}
	sample := e.prepareMetroStep(receipt, topology, config)
	if receipt.Error != "" {
		return
	}
	e.collectMetroRows(&sample)
	e.applyMetroPresence(sample)
	e.supportMetroGroups(sample)
	e.Previous = sample.current
	e.pruneMetroCases(sample)
	e.Live = e.forecasts(receipt, topology, config)
	e.issueMetroCases(sample)
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
	gap := !e.LastReceipt.IsZero() && now.Sub(e.LastReceipt) > 60*time.Second
	if gap || receipt.Error != "" {
		e.Gaps++
		e.resetContinuity()
	}
	e.LastReceipt = now
	e.trim(now, config)
	return metroSample{receipt: receipt, topology: topology, config: config, now: now, gap: gap, presence: intermediatePresence{map[string]bool{}, map[string]bool{}}, current: map[string]priorRow{}, signals: map[string][]signal{}}
}

func (e *engine) recordMetroCoverage(sample metroSample, row Row, route string) {
	coverage := baseAggregate(aggregateRequest{sample.now, route, row.Destination, row.Stop, row.Platform, e.Profile, sample.receipt.condition(route), "receipts"}, sample.config)
	coverage.Count, coverage.KnownAt = 1, sample.now.UnixNano()
	e.add(coverage)
}
