package app

import (
	"context"
	"lisboapublica/internal/api"
	"net/http"
	"strings"
	"time"
)

// ListArrivals lists arrivals at a known stop in the requested immutable revision.
func (s *Server) ListArrivals(ctx context.Context, _ api.ListArrivalsRequestObject) (api.ListArrivalsResponseObject, error) {
	filter, err := s.arrivalFilter(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if strings.HasPrefix(filter.Revision, "a:") {
		return s.pinnedArrivalPage(filter, now)
	}
	return s.firstArrivalPage(ctx, filter, now)
}

func (s *Server) arrivalFilter(ctx context.Context) (Filter, error) {
	f, err := s.filter(ctx, false)
	if err == nil && !boundedArrivalFilter(f) {
		err = fail(http.StatusBadRequest, "selection_limit", "Seleção de chegadas demasiado longa.")
	}
	if err == nil {
		f = ownArrivalFilter(f)
		if f.Revision == "" && request(ctx).URL.Query().Get("to") == "" {
			f.To = f.From.Add(time.Hour)
		}
	}
	return f, err
}

func boundedArrivalFilter(f Filter) bool {
	return len(f.Operators) <= len(providers) && len(f.Stop) <= 2*cpMaxStopBytes && len(f.Route) <= 2*cpMaxIdentifierBytes && len(f.Q) <= cpMaxNameBytes && len(f.Sort) <= cpMaxLabelBytes && len(f.Revision) <= cpMaxIdentifierBytes
}

func ownArrivalFilter(f Filter) Filter {
	f.Stop = strings.Clone(f.Stop)
	f.Route = strings.Clone(f.Route)
	f.Q = strings.Clone(f.Q)
	f.Sort = strings.Clone(f.Sort)
	f.Revision = strings.Clone(f.Revision)
	for n, id := range f.Operators {
		f.Operators[n] = strings.Clone(id)
	}
	return f
}

func (s *Server) pinnedArrivalPage(f Filter, now time.Time) (api.ListArrivalsResponseObject, error) {
	view, err := s.Cache.arrivals.page(f, now)
	if err != nil {
		return nil, err
	}
	current, _ := s.Cache.state("")
	operator, _, _ := strings.Cut(f.Stop, ":")
	if current.Static[operator] != view.static {
		return nil, fail(http.StatusGone, "revision_expired", "A rede mudou. Volte a carregar a primeira página.")
	}
	return arrivalPage(view.rows, f, f.Revision, &view.availability), nil
}

func arrivalPage(rows []api.Arrival, f Filter, revision string, a *api.ArrivalAvailability) api.ListArrivals200JSONResponse {
	page, data := paginate(rows, f, revision)
	// Do not retain the snapshot or its immutable static network through this field.
	var availability *api.ArrivalAvailability
	if a != nil {
		availability = ptr(*a)
	}
	return api.ListArrivals200JSONResponse{Data: data, Page: page, Availability: availability}
}

func (s *Server) firstArrivalPage(ctx context.Context, f Filter, now time.Time) (api.ListArrivalsResponseObject, error) {
	state, revision, err := s.scheduleState(ctx, &f)
	if err != nil {
		return nil, err
	}
	planned, err := collectScheduledArrivals(ctx, state, f)
	if err != nil {
		return nil, err
	}
	read := arrivalRead{server: s, state: state, filter: f, now: now, revision: revision, planned: planned}
	return read.page()
}

type arrivalRead struct {
	server   *Server
	state    *State
	filter   Filter
	now      time.Time
	revision string
	planned  []api.Arrival
}

func (r arrivalRead) page() (api.ListArrivalsResponseObject, error) {
	operator, _, _ := strings.Cut(r.filter.Stop, ":")
	if operator == "cp" || operator == "metro" || r.filter.Revision != "" {
		return arrivalPage(r.planned, r.filter, r.revision, nil), nil
	}
	view := r.server.Cache.arrivals.request(r.filter.Stop, r.state.Static[operator], r.now)
	setArrivalPlannedCoverage(&view, r.filter)
	if len(view.rows) == 0 && view.static.Schedule != nil {
		availability := arrivalAvailability(view.availability, r.planned, r.filter)
		return arrivalPage(r.planned, r.filter, r.revision, &availability), nil
	}
	return r.publishedPage(view)
}

func (r arrivalRead) publishedPage(view arrivalSnapshot) (api.ListArrivalsResponseObject, error) {
	view.rows = mergeArrivalRows(r.planned, view.rows, r.filter, r.now, r.state)
	view.availability = arrivalAvailability(view.availability, view.rows, r.filter)
	revision, err := r.server.Cache.arrivals.pin(r.filter, view, r.now)
	if err != nil {
		return nil, err
	}
	return arrivalPage(view.rows, r.filter, revision, &view.availability), nil
}

func setArrivalPlannedCoverage(v *arrivalSnapshot, f Filter) {
	if v.static.Schedule != nil {
		v.availability.PlannedStatus = "ok"
		if !arrivalCalendarCovers(v.static, f) {
			v.availability.PlannedStatus = "partial"
		}
	}
}

func arrivalCalendarCovers(d *StaticData, f Filter) bool {
	starts := d.ValidFrom == "" || f.From.In(lisbon).Format("20060102") >= d.ValidFrom
	ends := d.ValidUntil == "" || f.To.Add(-time.Nanosecond).In(lisbon).Format("20060102") <= d.ValidUntil
	return starts && ends
}
