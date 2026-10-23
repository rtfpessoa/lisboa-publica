package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"lisboapublica/internal/api"
	"lisboapublica/internal/patterns"
)

func (s *Server) GetTransportPatterns(ctx context.Context, request api.GetTransportPatternsRequestObject) (api.GetTransportPatternsResponseObject, error) {
	operator := string(request.Params.OperatorId)
	if operator == "metro" {
		return s.metroTransportPatterns(ctx, request)
	}
	return s.providerTransportPatterns(ctx, request)
}

func (s *Server) providerTransportPatterns(ctx context.Context, request api.GetTransportPatternsRequestObject) (api.GetTransportPatternsResponseObject, error) {
	operator := string(request.Params.OperatorId)
	stop := selectedPatternStop(request.Params.StopId)
	state, err := s.Cache.state("")
	if err != nil {
		return nil, err
	}
	if err := s.validateProviderPatternStop(state, operator, stop); err != nil {
		return nil, err
	}
	view := patterns.View{Operator: operator, Status: "disabled", Message: "Recolha experimental ainda não configurada.", Experimental: true, Operators: []patterns.OperatorHistory{}, Patterns: []patterns.HourPattern{}, Forecasts: []patterns.Forecast{}, Evaluation: []patterns.EvaluationReport{}}
	s.readProviderPatternView(ctx, &view, request, stop)
	appendOfficialCache(&view, providerOfficialCache(s.Cache, state, operator, stop, time.Now().UTC()))
	response, err := patternAPIResponse(view)
	if err != nil {
		return nil, err
	}
	return api.GetTransportPatterns200JSONResponse(response), nil
}

func selectedPatternStop(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Server) metroTransportPatterns(ctx context.Context, request api.GetTransportPatternsRequestObject) (api.GetTransportPatternsResponseObject, error) {
	return s.metroTransportPatternsWithClaim(ctx, request, s.claimMetroPublication)
}
func (s *Server) metroTransportPatternsWithClaim(ctx context.Context, request api.GetTransportPatternsRequestObject, claim func(patterns.MetroCutSnapshot, bool) bool) (api.GetTransportPatternsResponseObject, error) {
	return s.metroTransportPatternsWith(ctx, request, s.GetMetroPatterns, claim)
}
func (s *Server) metroTransportPatternsWith(ctx context.Context, request api.GetTransportPatternsRequestObject, get func(context.Context, api.GetMetroPatternsRequestObject) (api.GetMetroPatternsResponseObject, error), claim func(patterns.MetroCutSnapshot, bool) bool) (api.GetTransportPatternsResponseObject, error) {
	owner, matched := s.serverMetroCut()
	var cut patterns.MetroCutSnapshot
	if owner != nil {
		cut = owner.Snapshot()
	}
	response, err := get(ctx, api.GetMetroPatternsRequestObject{Params: api.GetMetroPatternsParams{StopId: request.Params.StopId, Episode: request.Params.Episode}})
	if err != nil {
		return nil, err
	}
	value, ok := response.(api.GetMetroPatterns200JSONResponse)
	if !ok {
		return nil, fail(500, "patterns", "Resposta de padrões indisponível.")
	}
	result := api.MetroPatterns(value)
	if !matched || !claim(cut, false) {
		fenceMetroPatternResponse(&result)
		_ = claim(cut, true)
	}
	return api.GetTransportPatterns200JSONResponse(result), nil
}

func (s *Server) validateProviderPatternStop(state *State, operator, stop string) error {
	if stop == "" {
		return nil
	}
	if !strings.HasPrefix(stop, operator+":") {
		return fail(400, "selection", "A paragem tem de pertencer ao operador selecionado.")
	}
	data := state.Static[operator]
	if !containsPatternStop(data, stop) {
		return fail(404, "not_found", "Paragem desconhecida.")
	}
	if operator == "cm" {
		s.Cache.arrivals.request(stop, data, time.Now().UTC())
	}
	return nil
}

func containsPatternStop(data *StaticData, stop string) bool {
	if data == nil {
		return false
	}
	for _, row := range data.Stops {
		if row.Id == stop {
			return true
		}
	}
	return false
}

func patternAPIResponse(view patterns.View) (api.MetroPatterns, error) {
	raw, err := json.Marshal(view)
	if err != nil {
		return api.MetroPatterns{}, err
	}
	var response api.MetroPatterns
	err = json.Unmarshal(raw, &response)
	return response, err
}

func (s *Server) readProviderPatternView(ctx context.Context, view *patterns.View, request api.GetTransportPatternsRequestObject, stop string) {
	if s.Patterns != nil {
		episode := ""
		if request.Params.Episode != nil {
			episode = *request.Params.Episode
		}
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var err error
		*view, err = s.Patterns.ProviderView(bounded, string(request.Params.OperatorId), stop, episode)
		if err != nil {
			s.patternReadFailure(view, err)
		}
	}
}
