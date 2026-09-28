package patterns

import (
	"encoding/json"
	"strings"
	"time"
)

// Intermediate observations revoke contradictory support without adding training.
func (e *engine) observeIntermediate(r Receipt, t Topology, c Config) {
	if len(e.Groups) == 0 {
		return
	}
	if r.Error != "" || e.Profile != t.Profile {
		e.resetContinuity()
		e.Gaps++
		return
	}
	e.updateConditions(r, t)
	support := e.intermediateSupport(r, t)
	if !e.cutUnsupportedGroups(support) {
		return
	}
	// A returning identifier cannot turn a known intermediate loss into a signal.
	e.Previous = map[string]priorRow{}
	e.Gaps++
	e.Live = e.forecasts(r, t, c)
}

type intermediatePresence struct{ present, conflict map[string]bool }

func (e *engine) intermediateSupport(r Receipt, t Topology) intermediatePresence {
	keyed := map[string][]Row{}
	for _, row := range r.Rows {
		keyed[contextKey(row)] = append(keyed[contextKey(row)], row)
	}
	support := intermediatePresence{map[string]bool{}, map[string]bool{}}
	for _, rows := range keyed {
		for _, row := range rows {
			route := t.route(row.Stop, row.Destination)
			if route != "" {
				e.collectIntermediateRow(&support, row, route, len(rows), r.ReceivedAt)
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
	return clock.Equal(previous.Clock) && digest(row) != previous.Signature
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
		if validTrain(id) {
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
		if !validTrain(id) {
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

func (e *engine) cutUnsupportedGroups(s intermediatePresence) bool {
	cut := false
	for key := range e.Groups {
		if !s.present[key] || s.conflict[key] {
			delete(e.Groups, key)
			cut = true
		}
	}
	return cut
}
