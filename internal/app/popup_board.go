package app

import (
	"context"
	"lisboapublica/internal/api"
	"strings"
)

type stationPopupRead struct {
	filter             Filter
	state              *State
	operator, revision string
	directions         []api.BoardDirection
	calls              []api.StopCall
	coverage           api.PopupCoverage
	arrivals           arrivalSnapshot
}

// GetStopBoard reads all available line/direction groups in a cached collection.
func (s *Server) GetStopBoard(ctx context.Context, r api.GetStopBoardRequestObject) (api.GetStopBoardResponseObject, error) {
	read, err := s.readStationPopup(ctx, r.StopId)
	if err != nil {
		return nil, err
	}
	read.countDirections()
	return api.GetStopBoard200JSONResponse{Directions: read.directions, Coverage: read.coverage, Revision: read.revision}, nil
}

// ListStopCalls reads independent times for a selected line and direction.
func (s *Server) ListStopCalls(ctx context.Context, r api.ListStopCallsRequestObject) (api.ListStopCallsResponseObject, error) {
	read, err := s.readStationPopup(ctx, r.StopId)
	if err != nil {
		return nil, err
	}
	q := request(ctx).URL.Query()
	selected := selectBoardCalls(read.calls, q.Get("line_key"), q.Get("direction_key"))
	page, rows := paginate(selected, read.filter, read.revision)
	coverage := s.stationCoverage(read.state, read.operator, rows, read.arrivals)
	return api.ListStopCalls200JSONResponse{Data: rows, Page: page, Coverage: coverage}, nil
}
func (s *Server) readStationPopup(ctx context.Context, stop string) (*stationPopupRead, error) {
	f, state, rev, err := s.popupFilter(ctx)
	if err != nil {
		return nil, err
	}
	f.Stop = stop
	operator, err := arrivalOperator(state, f)
	if err == nil {
		err = validateBoardPredictionRevision(state, operator, f)
	}
	read := &stationPopupRead{filter: f, state: state, operator: operator, revision: rev}
	if err == nil {
		err = read.collect(ctx, s)
	}
	return read, err
}
func (r *stationPopupRead) collect(ctx context.Context, s *Server) error {
	dirs, calls, err := stopBoardCalls(ctx, r.state, r.operator, r.filter)
	if err != nil {
		return err
	}
	view, rev, err := s.popupArrivalSnapshot(ctx, r.state, r.operator, r.filter, r.revision)
	if err == nil {
		calls = appendPopupArrivals(r.state, r.operator, r.filter, view, calls)
		if len(calls) > maxReadResults {
			err = readResultLimit()
		}
		r.directions = addPopupDirections(dirs, calls, r.state.Static[r.operator], r.operator)
		r.calls = calls
		r.arrivals = view
		r.revision = rev
		r.coverage = s.stationCoverage(r.state, r.operator, calls, view)
	}
	return err
}
func (r *stationPopupRead) countDirections() {
	counts := map[string]int{}
	for _, call := range r.calls {
		counts[boardSelection(call.LineKey, call.DirectionKey)]++
	}
	if r.coverage.Status == "unavailable" || r.coverage.Status == "stale" || r.coverage.Status == "loading" {
		return
	}
	for n := range r.directions {
		dir := &r.directions[n]
		dir.Count = ptr(counts[boardSelection(dir.LineKey, dir.DirectionKey)])
	}
}
func boardSelection(line string, direction *string) string {
	if direction == nil {
		return line + "|unknown"
	}
	return line + "|" + *direction
}
func selectBoardCalls(calls []api.StopCall, line, direction string) []api.StopCall {
	selected := []api.StopCall{}
	for _, call := range calls {
		if (line == "" || call.LineKey == line) && matchesBoardDirection(call.DirectionKey, direction) {
			selected = append(selected, call)
		}
	}
	return selected
}
func matchesBoardDirection(key *string, want string) bool {
	return want == "" || want == "unknown" && key == nil || key != nil && *key == want
}

// Expose a line's full catalogue independently of calls in the selected window.
func boardCatalogue(d *StaticData, operator, stop string) []api.BoardDirection {
	dirs := []api.BoardDirection{}
	if d == nil || d.Schedule == nil {
		return dirs
	}
	idx := d.journeys(operator)
	seen := map[string]bool{}
	for _, t := range idx.stopTrips(d, operator, stop) {
		line := idx.lineFor(t)
		if !seen[line] {
			dirs = append(dirs, idx.directions[line]...)
			seen[line] = true
		}
	}
	return dirs
}

func boardPredictionOperator(stop string) string { op, _, _ := strings.Cut(stop, ":"); return op }
