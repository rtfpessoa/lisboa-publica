package app

import (
	"context"
	"lisboapublica/internal/api"
	"sort"
	"time"
)

type plannedBoardRead struct {
	ctx      context.Context
	state    *State
	data     *StaticData
	operator string
	filter   Filter
	index    *journeyIndex
	calls    []api.StopCall
}

func stopBoardCalls(ctx context.Context, state *State, op string, f Filter) ([]api.BoardDirection, []api.StopCall, error) {
	d := state.Static[op]
	r := plannedBoardRead{ctx: ctx, state: state, data: d, operator: op, filter: f, calls: []api.StopCall{}}
	err := r.collect()
	if err != nil {
		return nil, nil, err
	}
	calls := appendUnjoinedPredictions(state, op, f, r.calls)
	if op == "metro" {
		calls = append(calls, metroBoardCalls(state, f)...)
	}
	if len(calls) > maxReadResults {
		return nil, nil, readResultLimit()
	}
	sort.Slice(calls, func(i, j int) bool {
		a, b := nextCallTime(calls[i]), nextCallTime(calls[j])
		if a.Equal(b) {
			return calls[i].Id < calls[j].Id
		}
		return a.Before(b)
	})
	dirs := addPopupDirections(boardCatalogue(d, op, f.Stop), calls, d, op)
	return dirs, calls, nil
}
func (r *plannedBoardRead) collect() error {
	if r.data == nil || r.data.Schedule == nil || r.data.Schedule.HasFrequencies {
		return nil
	}
	r.index = r.data.journeys(r.operator)
	day := r.filter.From.In(lisbon)
	day = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, lisbon).AddDate(0, 0, -3)
	var err error
	for ; !day.After(r.filter.To.In(lisbon).Add(12 * time.Hour)); day = day.AddDate(0, 0, 1) {
		err = r.collectDay(day)
		if err != nil {
			break
		}
	}
	return err
}
func (r *plannedBoardRead) collectDay(day time.Time) error {
	var err error
	for _, t := range r.index.stops[r.filter.Stop] {
		err = r.ctx.Err()
		if err == nil && popupServiceActive(r.data, t, day) {
			err = r.collectTrip(t, day)
		}
		if err != nil {
			break
		}
	}
	return err
}
func (r *plannedBoardRead) collectTrip(t *ScheduledTrip, day time.Time) error {
	for _, v := range t.Times {
		if !r.matchesStop(v) {
			continue
		}
		call := (plannedPopupJourney{r.data, r.operator, t, day, r.index, r.filter.From}).call(v)
		applyCPPredictions(&call, r.state, t, day, r.filter.From)
		if callInWindow(call, r.filter.From, r.filter.To) {
			r.calls = append(r.calls, call)
		}
		if len(r.calls) > maxReadResults {
			return readResultLimit()
		}
	}
	return nil
}
func (r *plannedBoardRead) matchesStop(v StopTime) bool {
	return qualify(r.operator, v.Stop) == r.filter.Stop || qualify(r.operator, r.data.Schedule.Parents[v.Stop]) == r.filter.Stop
}
