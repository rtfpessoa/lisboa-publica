package patterns

import (
	"encoding/json"
	"strings"
	"time"
)

// Intermediate observations revoke contradictory support without adding training.
func (e *engine) observeIntermediate(r Receipt, t Topology, c Config) {
	if r.Error != "" {
		// A failed upstream fetch observes nothing about the network. Keeping the
		// previous rows lets a single interrupted publication continue the learned
		// sequence instead of wiping every pending arrival.
		return
	}
	if len(e.Groups) == 0 && len(e.Previous) == 0 {
		return
	}
	if e.Profile != t.Profile {
		e.resetContinuity()
		e.Gaps++
		return
	}
	beforeConditions := e.Gaps
	e.updateConditions(r, t)
	keyed := intermediateRows(r)
	support := e.intermediateSupport(keyed, r.ReceivedAt, t)
	lost, changed, _ := e.cutIntermediateSupport(keyed, r.Rows, support, r.ReceivedAt, t)
	e.noteIntermediateRows(keyed, r.ReceivedAt)
	if !changed && e.Gaps == beforeConditions {
		return
	}
	if lost {
		e.Gaps++
		e.IntermediateCuts++
	}
	e.Live = e.forecasts(r, t, c)
}

type intermediatePresence struct{ present, conflict map[string]bool }

func intermediateRows(r Receipt) map[string][]Row {
	keyed := map[string][]Row{}
	for _, row := range r.Rows {
		if validMetroContext(row) {
			keyed[contextKey(row)] = append(keyed[contextKey(row)], row)
		}
	}
	return keyed
}

// The internal context and group keys use a reserved separator. Reject
// ambiguous components before using them as inference identities.
func validMetroContext(row Row) bool {
	return row.Stop != "" && row.Platform != "" && row.Destination != "" &&
		!strings.Contains(row.Stop, "|") && !strings.Contains(row.Platform, "|") && !strings.Contains(row.Destination, "|")
}

func validMetroGroupTrain(id string) bool {
	return validTrain(id) && !strings.Contains(id, "|")
}

func (e *engine) intermediateSupport(keyed map[string][]Row, received time.Time, t Topology) intermediatePresence {
	support := intermediatePresence{map[string]bool{}, map[string]bool{}}
	for _, rows := range keyed {
		for _, row := range rows {
			route := t.route(row.Stop, row.Destination)
			if route != "" && !strings.Contains(route, "|") {
				e.collectIntermediateRow(&support, row, route, len(rows), received)
			}
		}
	}
	return support
}

func intermediateTrainIDs(row Row) []string {
	return []string{strings.TrimSpace(row.Train), strings.TrimSpace(row.Train2), strings.TrimSpace(row.Train3)}
}

func (e *engine) intermediateRowConflict(key string, row Row, count int, clock time.Time, validClock bool) bool {
	if count != 1 {
		return true
	}
	previous, exists := e.Previous[key]
	if !exists || !validClock {
		return false
	}
	if clock.Before(previous.Clock) || clock.Sub(previous.Clock) > 60*time.Second {
		return true
	}
	if clock.Equal(previous.Clock) && digest(row) != previous.Signature {
		return true
	}
	if !previous.IntermediateClock.IsZero() {
		return clock.Before(previous.IntermediateClock) || clock.Equal(previous.IntermediateClock) && digest(row) != previous.IntermediateSignature
	}
	return false
}

func (e *engine) collectIntermediateRow(support *intermediatePresence, row Row, route string, count int, received time.Time) {
	clock, valid := sourceClock(row.Clock)
	ids := intermediateTrainIDs(row)
	if e.intermediateRowConflict(contextKey(row), row, count, clock, valid) {
		support.reject(ids, row.Destination, route)
		return
	}
	if !valid || received.Before(clock) || received.Sub(clock) > 90*time.Second {
		return
	}
	support.admit(row, ids, route)
}

func (s *intermediatePresence) reject(ids []string, direction, route string) {
	for _, id := range ids {
		if validMetroGroupTrain(id) {
			s.conflict[groupKey(groupIdentity{Train: id, Direction: direction, Route: route})] = true
		}
	}
}

func (s *intermediatePresence) admit(row Row, ids []string, route string) {
	counts := map[string]int{}
	for _, id := range ids {
		counts[id]++
	}
	values := []json.RawMessage{row.ETA, row.ETA2, row.ETA3}
	for i, id := range ids {
		if !validMetroGroupTrain(id) {
			continue
		}
		key := groupKey(groupIdentity{Train: id, Direction: row.Destination, Route: route})
		if counts[id] != 1 {
			s.conflict[key] = true
			continue
		}
		if _, valid := eta(values[i]); valid {
			s.present[key] = true
		}
	}
}

// An unsampled publication can revoke support but never create a sample. A
// station baseline is usable only while its exact first-slot context survives.
func (e *engine) cutIntermediateSupport(rows map[string][]Row, rawRows []Row, s intermediatePresence, received time.Time, t Topology) (bool, bool, int) {
	cutGroups := map[string]bool{}
	for key := range e.Groups {
		if !s.present[key] || s.conflict[key] {
			cutGroups[key] = true
		}
	}
	invalid, lost, valid := e.scanIntermediatePriors(rows, cutGroups, received, t)
	if !valid {
		// A malformed saved context cannot safely be attributed to a group.
		removed := len(e.Groups)
		e.resetContinuity()
		return true, true, removed
	}
	e.cutReturnedMetroContexts(rawRows, cutGroups, received, t)
	lost = lost || len(cutGroups) > 0
	e.removeIntermediateCuts(invalid, cutGroups, t)
	return lost, len(cutGroups) > 0 || len(invalid) > 0, len(cutGroups)
}

func (e *engine) scanIntermediatePriors(rows map[string][]Row, cutGroups map[string]bool, received time.Time, t Topology) (map[string]bool, bool, bool) {
	invalid := map[string]bool{}
	lost := false
	for key, previous := range e.Previous {
		group, ok := intermediatePriorGroup(key, previous, t)
		if !ok {
			return invalid, lost, false
		}
		if e.intermediatePriorSupported(key, previous, rows[key], received) {
			continue
		}
		invalid[key] = true
		if e.intermediateZeroDeparture(key, previous, rows[key], received, e.Groups[group]) {
			continue
		}
		lost = true
		if e.Groups[group] != nil {
			cutGroups[group] = true
		}
	}
	return invalid, lost, true
}

func (e *engine) intermediateZeroDeparture(key string, previous priorRow, rows []Row, received time.Time, g *group) bool {
	if g != nil {
		return e.safeMetroZeroDeparture(key, previous, rows, received, g)
	}
	return previous.ETA == 0 && e.cleanZeroDeparture(key, previous, rows, received)
}

func (e *engine) removeIntermediateCuts(invalid, cutGroups map[string]bool, t Topology) {
	for key := range cutGroups {
		delete(e.Groups, key)
	}
	for key, previous := range e.Previous {
		group, _ := intermediatePriorGroup(key, previous, t)
		if invalid[key] || cutGroups[group] {
			delete(e.Previous, key)
		}
	}
}

func intermediatePriorGroup(key string, previous priorRow, t Topology) (string, bool) {
	parts := strings.Split(key, "|")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" || !validMetroGroupTrain(previous.Train) {
		return "", false
	}
	route := t.route(parts[0], parts[2])
	if route == "" || strings.Contains(route, "|") {
		return "", false
	}
	return groupKey(groupIdentity{Train: previous.Train, Direction: parts[2], Route: route}), true
}

func (e *engine) intermediatePriorSupported(key string, previous priorRow, rows []Row, received time.Time) bool {
	if len(rows) != 1 {
		return false
	}
	row := rows[0]
	clock, valid := sourceClock(row.Clock)
	if !validMetroSampleClock(clock, valid, received) || e.intermediateRowConflict(key, row, 1, clock, valid) {
		return false
	}
	ids := intermediateTrainIDs(row)
	if ids[0] != previous.Train || ids[0] == ids[1] || ids[0] == ids[2] {
		return false
	}
	value, valid := eta(row.ETA)
	if !valid {
		return false
	}
	return previous.IntermediateZero.IsZero() || value == 0 || !clock.After(previous.IntermediateZero)
}

func (e *engine) noteIntermediateRows(rows map[string][]Row, received time.Time) {
	for key, previous := range e.Previous {
		values := rows[key]
		if !e.intermediatePriorSupported(key, previous, values, received) {
			continue
		}
		clock, _ := sourceClock(values[0].Clock)
		previous.IntermediateClock, previous.IntermediateSignature = clock, digest(values[0])
		value, _ := eta(values[0].ETA)
		if value == 0 && clock.After(previous.Clock) {
			previous.IntermediateZero = clock
		}
		e.Previous[key] = previous
	}
}

// A zero baseline can disappear normally after its own unique sampled signal.
// A returned reference at that context will revoke the old group below.
func (e *engine) safeMetroZeroDeparture(key string, previous priorRow, rows []Row, received time.Time, g *group) bool {
	if previous.ETA != 0 {
		return false
	}
	parts := strings.Split(key, "|") // Already validated by intermediatePriorGroup.
	matches := 0
	for _, signal := range g.Signals {
		if signal.Stop == parts[0] && signal.Platform == parts[1] && !signal.L.IsZero() && signal.L.Before(signal.U) && !signal.U.After(previous.Clock) {
			matches++
		}
	}
	if matches != 1 {
		return false
	}
	return e.cleanZeroDeparture(key, previous, rows, received)
}

func (e *engine) cleanZeroDeparture(key string, previous priorRow, rows []Row, received time.Time) bool {
	if len(rows) == 0 {
		return true
	}
	if len(rows) != 1 {
		return false
	}
	clock, valid := sourceClock(rows[0].Clock)
	if !validMetroSampleClock(clock, valid, received) || e.intermediateRowConflict(key, rows[0], 1, clock, valid) {
		return false
	}
	for _, id := range intermediateTrainIDs(rows[0]) {
		if id == previous.Train {
			return false
		}
	}
	return true
}

// A later occurrence at a signalled station cannot join the old group after
// its baseline departed, or after its zero wait changes back to positive.
type metroReturnEvidence struct {
	row   *Row
	clock time.Time
	route string
}

func metroReturnRows(rows []Row, received time.Time, topology Topology) map[string][]metroReturnEvidence {
	byStop := make(map[string][]metroReturnEvidence)
	for n := range rows {
		row := &rows[n]
		clock, valid := sourceClock(row.Clock)
		if !validMetroSampleClock(clock, valid, received) {
			continue
		}
		route := topology.route(row.Stop, row.Destination)
		if row.Stop != "" {
			byStop[row.Stop] = append(byStop[row.Stop], metroReturnEvidence{row, clock, route})
		}
	}
	return byStop
}

func (e *engine) cutReturnedMetroContexts(rows []Row, cut map[string]bool, received time.Time, topology Topology) {
	if len(e.Groups) == 0 {
		return
	}
	byStop := metroReturnRows(rows, received, topology)
	for groupKey, g := range e.Groups {
		if cut[groupKey] {
			continue
		}
		for _, signal := range g.Signals {
			if e.returnedMetroSignal(g, signal, byStop[signal.Stop]) {
				cut[groupKey] = true
				break
			}
		}
	}
}

func (e *engine) returnedMetroSignal(g *group, signal signal, rows []metroReturnEvidence) bool {
	key := signal.Stop + "|" + signal.Platform + "|" + g.Direction
	previous, exists := e.Previous[key]
	departed := !exists || previous.Train != g.Train
	for _, item := range rows {
		if item.route != "" && (item.row.Destination != g.Direction || item.route != g.Route) {
			continue
		}
		if returnedMetroEvidence(g.Train, signal.Platform, previous, departed, item) {
			return true
		}
	}
	return false
}

func returnedMetroEvidence(train, platform string, previous priorRow, departed bool, item metroReturnEvidence) bool {
	row := item.row
	ids := intermediateTrainIDs(*row)
	values := []json.RawMessage{row.ETA, row.ETA2, row.ETA3}
	for n, id := range ids {
		if id != train {
			continue
		}
		if departed || row.Platform != platform {
			return true
		}
		value, valid := eta(values[n])
		if valid && previous.ETA == 0 && value > 0 && item.clock.After(previous.Clock) {
			return true
		}
	}
	return false
}

// A sampled conflicting clock must not become a new baseline after its old
// group is cleared. Capture these rejections before removing any prior rows.
func (e *engine) conflictingSampleContexts(rows map[string][]Row) map[string]bool {
	rejected := map[string]bool{}
	for key, values := range rows {
		if len(values) != 1 {
			continue // The normal sampled-row path rejects duplicates.
		}
		clock, valid := sourceClock(values[0].Clock)
		if e.intermediateRowConflict(key, values[0], 1, clock, valid) {
			rejected[key] = true
		}
	}
	return rejected
}
