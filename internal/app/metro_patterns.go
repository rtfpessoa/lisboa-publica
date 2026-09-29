package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func compactMetroName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, normalizeName(name))
}

func (m *MetroClient) recordPatterns(data *MetroData, static *StaticData, now time.Time) error {
	t := metroTopology(data, static)
	raw, _ := json.Marshal(data.Waits)
	var rows []patterns.Row
	_ = json.Unmarshal(raw, &rows)
	conditions := metroPatternConditions(data, static)
	receipt := patterns.Receipt{ReceivedAt: now, Rows: rows, Raw: data.RawWaits, ServiceCondition: "unknown", RouteConditions: conditions}
	if data.Status.Status != api.MetroStatusStatusOk {
		receipt.Error = string(data.Status.Status)
		receipt.Rows = nil
		receipt.Raw = nil
	}
	// Archive failure is visible in its status; it cannot suppress official live data.
	return m.History.Record(receipt, t)
}

func (s *Server) GetMetroPatterns(ctx context.Context, request api.GetMetroPatternsRequestObject) (api.GetMetroPatternsResponseObject, error) {
	view := patterns.View{Operator: "metro", Operators: []patterns.OperatorHistory{}, Status: "disabled", Message: "Recolha experimental ainda não configurada.", Experimental: true, Patterns: []patterns.HourPattern{}, Forecasts: []patterns.Forecast{}, Evaluation: []patterns.EvaluationReport{}}
	state, err := s.Cache.state("")
	if err != nil {
		return nil, err
	}
	stop, err := metroPatternStop(state, request.Params.StopId)
	if err != nil {
		return nil, err
	}
	s.readMetroPatternView(ctx, &view, request, stop)
	appendOfficialCache(&view, metroOfficialCache(state, stop, time.Now().UTC()))
	response, err := patternAPIResponse(view)
	if err != nil {
		return nil, err
	}
	return api.GetMetroPatterns200JSONResponse(response), nil
}

func metroPatternStop(state *State, selection *string) (string, error) {
	if selection == nil || state.Metro == nil {
		return "", nil
	}
	stations := map[string]MetroStation{}
	for _, station := range state.Metro.Stations {
		stations[station.ID] = station
	}
	stop := metroStationID(state.Static["metro"], state.Metro.Stations, stations, *selection)
	if _, ok := stations[stop]; !ok {
		return "", fail(404, "not_found", "Estação Metro desconhecida.")
	}
	return stop, nil
}

func metroPatternConditions(data *MetroData, static *StaticData) map[string]string {
	conditions := map[string]string{}
	ids := metroRouteIDs(static)
	for _, line := range data.Status.Lines {
		route := ids[normalizeName(line.Line)]
		if route == "" {
			continue
		}
		condition := "unknown"
		if metroLineStateFresh(data.Status) {
			if strings.EqualFold(strings.TrimSpace(line.State), "Normal") {
				condition = "reported_normal"
			} else if strings.TrimSpace(line.State) != "" {
				condition = "reported_disruption"
			}
		}
		if previous, ok := conditions[route]; ok && previous != condition {
			conditions[route] = "unknown"
		} else {
			conditions[route] = condition
		}
	}
	return conditions
}

func (s *Server) readMetroPatternView(ctx context.Context, view *patterns.View, request api.GetMetroPatternsRequestObject, stop string) {
	if s.Patterns != nil {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var err error
		episode := ""
		if request.Params.Episode != nil {
			episode = *request.Params.Episode
		}
		*view, err = s.Patterns.View(bounded, stop, episode)
		if err != nil {
			s.patternReadFailure(view, err)
		}
	}
}

func metroLineStateFresh(status api.MetroStatus) bool {
	return status.Status == api.MetroStatusStatusOk && status.CheckedAt != nil && status.LineStateUpdatedAt != nil && status.LineStateError == nil && !status.LineStateUpdatedAt.After(status.CheckedAt.Add(providerClockSkew)) && status.CheckedAt.Sub(*status.LineStateUpdatedAt) <= sourceFreshness
}
