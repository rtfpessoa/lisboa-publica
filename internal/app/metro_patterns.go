package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"go.uber.org/zap"
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

// reportMetroSampleError surfaces an upstream failure that suppresses a pattern sample.
// It logs each distinct message once until the source recovers.
func (m *MetroClient) reportMetroSampleError(message string) {
	if m.Log == nil || message == "" || message == m.lastSampleError {
		return
	}
	m.lastSampleError = message
	m.Log.Warn("metro sample skipped", zap.String("message", message))
}

func (m *MetroClient) recordPatterns(data *MetroData, static *StaticData, now time.Time) error {
	return m.recordPatternsWith(data, static, now, m.History.RecordMetro)
}
func (m *MetroClient) recordPatternsWith(data *MetroData, static *StaticData, now time.Time, record func(patterns.Receipt, patterns.Topology) (patterns.MetroRecordResult, error)) error {
	receipt, t := m.patternReceipt(data, static, now)
	result, err := record(receipt, t)
	if err == nil {
		if result.Acknowledged && m.Cache != nil {
			r := m.Cache.metroRuntime
			r.mu.Lock()
			cut := result.Cut
			cut.Pending = false
			if !r.historyCutMismatch && r.historyCut == m.History.MetroCut() && r.historyCut.Claim(cut) {
				r.historyDeliveryGap = false
				r.historyCollecting(now)
			}
			r.mu.Unlock()
		}
		m.logMetroSample(false)
	}
	return err
}

func (m *MetroClient) patternReceipt(data *MetroData, static *StaticData, now time.Time) (patterns.Receipt, patterns.Topology) {
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
		m.reportMetroSampleError(data.Status.Message)
	} else {
		m.lastSampleError = ""
	}
	return receipt, t
}

// logMetroSample emits the bounded per-sample diagnostic counters at debug level.
func (m *MetroClient) logMetroSample(fifo bool) {
	if m.Log == nil || m.History == nil {
		return
	}
	stats := m.History.MetroSampleStats()
	if stats.SampledAt.IsZero() || !stats.SampledAt.After(m.lastSampleLogged) {
		return
	}
	m.lastSampleLogged = stats.SampledAt
	fields := []zap.Field{
		zap.Time("sampled_at", stats.SampledAt),
		zap.Bool("gap", stats.Gap),
		zap.Bool("empty", stats.Empty),
		zap.Int("rows", stats.Rows),
		zap.Int("contexts", stats.Contexts),
		zap.Int("admitted", stats.Admitted),
		zap.Int("first_slots", stats.FirstSlots),
		zap.Int("with_prior", stats.WithPrior),
		zap.Int("zero_eta", stats.ZeroETA),
		zap.Int("rejected_route", stats.RejectedRoute),
		zap.Int("rejected_duplicate", stats.RejectedDuplicate),
		zap.Int("rejected_clock", stats.RejectedClock),
		zap.Int("rejected_continuity", stats.RejectedContinuity),
		zap.Int("signals", stats.Signals),
		zap.Int("signals_applied", stats.SignalsApplied),
		zap.Int("groups_created", stats.GroupsCreated),
		zap.Int("groups_deleted_absent", stats.GroupsDeleted),
		zap.Int("active_groups", stats.ActiveGroups),
		zap.Int64("intermediate_cuts", stats.IntermediateCuts),
		zap.Int64("gaps", stats.Gaps),
	}
	if field, ok := m.metroDeliveryDiagnosticField(fifo); ok {
		fields = append(fields, field)
	}
	m.Log.Debug("metro sample", fields...)
}

func (s *Server) GetMetroPatterns(ctx context.Context, request api.GetMetroPatternsRequestObject) (api.GetMetroPatternsResponseObject, error) {
	return s.getMetroPatternsWith(ctx, request, s.buildMetroPatternResponse, s.claimMetroPublication)
}

func (s *Server) getMetroPatternsWith(ctx context.Context, request api.GetMetroPatternsRequestObject, build func(context.Context, api.GetMetroPatternsRequestObject) (api.MetroPatterns, error), claim func(patterns.MetroCutSnapshot, bool) bool) (api.GetMetroPatternsResponseObject, error) {
	owner, matched := s.serverMetroCut()
	var cut patterns.MetroCutSnapshot
	if owner != nil {
		cut = owner.Snapshot()
	}
	response, err := build(ctx, request)
	if err != nil {
		return nil, err
	}
	if !matched || !claim(cut, false) {
		fenceMetroPatternResponse(&response)
		_ = claim(cut, true)
	}
	return api.GetMetroPatterns200JSONResponse(response), nil
}

func (s *Server) buildMetroPatternResponse(ctx context.Context, request api.GetMetroPatternsRequestObject) (api.MetroPatterns, error) {
	view := patterns.View{Operator: "metro", Operators: []patterns.OperatorHistory{}, Status: "disabled", Message: "Recolha experimental ainda não configurada.", Experimental: true, Patterns: []patterns.HourPattern{}, Forecasts: []patterns.Forecast{}, Evaluation: []patterns.EvaluationReport{}}
	state, err := s.Cache.state("")
	if err != nil {
		return api.MetroPatterns{}, err
	}
	stop, err := metroPatternStop(state, request.Params.StopId)
	if err != nil {
		return api.MetroPatterns{}, err
	}
	s.readMetroPatternView(ctx, &view, request, stop)
	appendOfficialCache(&view, metroOfficialCache(state, stop, time.Now().UTC()))
	return patternAPIResponse(view)
}

func fenceMetroPatternResponse(response *api.MetroPatterns) {
	response.Status, response.Message = "paused", "Histórico do Metro temporariamente indisponível; existem lacunas."
	response.Pending = 0
	for n := range response.Forecasts {
		f := &response.Forecasts[n]
		f.Unavailable = "history_interrupted"
		f.OwnAt, f.LowerAt, f.UpperAt = nil, nil, nil
		f.Components = []api.MetroPatternComponent{}
		f.CalibrationSamples = 0
		f.ReferenceLower, f.ReferenceUpper, f.ErrorLower, f.ErrorUpper = nil, nil, nil, nil
		f.Selected, f.Evaluated, f.Result = false, false, "lost_support"
	}
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
