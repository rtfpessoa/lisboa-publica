package patterns

import "time"

type metroSampleRow struct {
	row                   Row
	route, key, signature string
	ids                   []string
	counts                map[string]int
	clock                 time.Time
	old                   priorRow
	hasOld, badClock      bool
}

func (e *engine) collectMetroRows(sample *metroSample) {
	keyed := map[string][]Row{}
	for _, row := range sample.receipt.Rows {
		if row.Stop != "" && row.Platform != "" && row.Destination != "" {
			keyed[contextKey(row)] = append(keyed[contextKey(row)], row)
		}
	}
	sample.stats.Contexts = len(keyed)
	for _, rows := range keyed {
		for _, row := range rows {
			e.collectMetroRow(sample, row, len(rows))
		}
	}
}

func (e *engine) collectMetroRow(sample *metroSample, row Row, count int) {
	route := sample.topology.route(row.Stop, row.Destination)
	if route == "" {
		sample.stats.RejectedRoute++
		return
	}
	ids := intermediateTrainIDs(row)
	if count != 1 {
		sample.stats.RejectedDuplicate++
		sample.presence.reject(ids, row.Destination, route)
		return
	}
	clock, valid := sourceClock(row.Clock)
	if !validMetroSampleClock(clock, valid, sample.now) {
		sample.stats.RejectedClock++
		return
	}
	context := e.metroSampleRow(row, route, ids, clock)
	if context.badClock {
		sample.stats.RejectedContinuity++
		sample.presence.reject(ids, row.Destination, route)
	} else {
		e.recordMetroCoverage(*sample, row, route)
		sample.presence.admit(row, ids, route)
		sample.stats.Admitted++
	}
	context.collectFirstSlot(sample)
}

func validMetroSampleClock(clock time.Time, valid bool, now time.Time) bool {
	return valid && !now.Before(clock) && now.Sub(clock) <= 90*time.Second
}

func (e *engine) metroSampleRow(row Row, route string, ids []string, clock time.Time) metroSampleRow {
	key := contextKey(row)
	old, exists := e.Previous[key]
	counts := map[string]int{}
	for _, id := range ids {
		counts[id]++
	}
	return metroSampleRow{row: row, route: route, key: key, signature: digest(row), ids: ids, counts: counts, clock: clock, old: old, hasOld: exists, badClock: e.intermediateRowConflict(key, row, 1, clock, true)}
}

func (r metroSampleRow) collectFirstSlot(sample *metroSample) {
	first, valid := eta(r.row.ETA)
	if !r.validFirstSlot(valid) {
		return
	}
	sample.current[r.key] = priorRow{r.clock, r.signature, r.ids[0], first}
	sample.stats.FirstSlots++
	if !sample.gap && r.hasOld && r.old.Train == r.ids[0] && r.clock.After(r.old.Clock) && r.clock.Sub(r.old.Clock) <= 60*time.Second {
		sample.stats.WithPrior++
		if first == 0 {
			sample.stats.ZeroETA++
		}
	}
	if r.transitionSignal(first, sample.gap) {
		key := groupKey(groupIdentity{Train: r.ids[0], Direction: r.row.Destination, Route: r.route})
		sample.signals[key] = append(sample.signals[key], signal{Stop: r.row.Stop, Platform: r.row.Platform, L: r.old.Clock, U: r.clock})
		sample.stats.Signals++
	}
}

func (r metroSampleRow) validFirstSlot(validETA bool) bool {
	if !validETA || r.badClock {
		return false
	}
	return validTrain(r.ids[0]) && r.counts[r.ids[0]] == 1
}

func (r metroSampleRow) transitionSignal(first float64, gap bool) bool {
	if gap || !r.hasOld {
		return false
	}
	if !r.clock.After(r.old.Clock) || r.clock.Sub(r.old.Clock) > 60*time.Second || r.old.Train != r.ids[0] {
		return false
	}
	return r.old.ETA > 0 && first == 0
}
