package app

import (
	"context"
	"fmt"
	"lisboapublica/internal/api"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Filter freezes selectors, pagination and the requested source time window.
type Filter struct {
	Limit, Offset        int
	Revision             string
	Operators            []string
	Route, Q, Stop, Sort string
	From, To             time.Time
	HourStart, HourEnd   int
	Weekdays             bool
}

func (s *Server) filter(ctx context.Context, historical bool) (Filter, error) {
	query := request(ctx).URL.Query()
	filter := Filter{Limit: defaultPageSize, Revision: query.Get("revision"), Route: query.Get("route_id"), Q: strings.ToLower(query.Get("q")), Stop: query.Get("stop_id"), Sort: query.Get("sort"), HourEnd: 24}
	readFilterPagination(&filter, query)
	if err := readFilterSelectors(&filter, query); err != nil {
		return filter, err
	}
	return s.completeRequestFilter(filter, query, historical)
}

func (f Filter) selected(p string) bool { return len(f.Operators) == 0 || contains(f.Operators, p) }

func paginate[T any](items []T, f Filter, revision string) (api.Page, []T) {
	total := len(items)
	start := min(f.Offset, total)
	end := min(start+f.Limit, total)
	return api.Page{Limit: f.Limit, Offset: f.Offset, Total: total, HasMore: end < total, Revision: optional(revision)}, items[start:end]
}

func readFilterPagination(filter *Filter, query url.Values) {
	if v := query.Get("limit"); v != "" {
		filter.Limit, _ = strconv.Atoi(v)
	}
	if v := query.Get("offset"); v != "" {
		filter.Offset, _ = strconv.Atoi(v)
	}
}

func (s *Server) validateFilterRange(filter Filter, now time.Time, historical bool) error {
	if !filter.To.After(filter.From) {
		return fail(http.StatusBadRequest, "range", "Intervalo de datas inválido.")
	}
	if historical {
		return validateHistoricalFilterRange(filter, now, s.Store.retentionDays())
	}
	return validateScheduledFilterRange(filter)
}

func validateHistoricalFilterRange(filter Filter, now time.Time, retentionDays int) error {
	if filter.From.Before(now.AddDate(0, 0, -retentionDays)) {
		return fail(http.StatusGone, "history_expired", fmt.Sprintf("Histórico disponível apenas nos últimos %d dias.", retentionDays))
	}
	if filter.To.After(now.Add(time.Minute)) || filter.To.Sub(filter.From) > time.Duration(retentionDays)*24*time.Hour+time.Hour {
		return fail(http.StatusBadRequest, "range", "Intervalo histórico fora dos limites.")
	}
	return nil
}

func validateScheduledFilterRange(filter Filter) error {
	if filter.To.Sub(filter.From) > maximumScheduleWindow {
		return fail(http.StatusBadRequest, "range", "Horários limitados a48 horas por pedido.")
	}
	return nil
}

func readFilterHours(filter *Filter, query url.Values) error {
	if v := query.Get("hour_start"); v != "" {
		filter.HourStart, _ = strconv.Atoi(v)
	}
	if v := query.Get("hour_end"); v != "" {
		filter.HourEnd, _ = strconv.Atoi(v)
	}
	if filter.HourStart >= filter.HourEnd {
		return fail(http.StatusBadRequest, "hours", "Intervalo horário inválido.")
	}
	filter.Weekdays = query.Get("weekdays_only") == "true"
	return nil
}

func (s *Server) completeRequestFilter(filter Filter, query url.Values, historical bool) (Filter, error) {
	now := time.Now().UTC()
	if err := readFilterWindow(&filter, query, now, historical); err != nil {
		return filter, err
	}
	steps := []func() error{func() error { return s.validateFilterRange(filter, now, historical) }, func() error { return readFilterHours(&filter, query) }}
	for _, step := range steps {
		if err := step(); err != nil {
			return filter, err
		}
	}
	return filter, nil
}

func readFilterWindow(filter *Filter, query url.Values, now time.Time, historical bool) error {
	if historical {
		local := now.In(lisbon)
		filter.From = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, lisbon)
		filter.To = now
	} else {
		filter.From = now
		filter.To = now.Add(24 * time.Hour)
	}
	if err := readFilterDates(filter, query, historical); err != nil {
		return err
	}
	return nil
}
