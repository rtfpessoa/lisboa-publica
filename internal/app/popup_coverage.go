package app

import (
	"lisboapublica/internal/api"
	"strings"
	"time"
)

func (s *Server) popupCoverage(state *State, operator string) api.PopupCoverage {
	out := s.popupStaticCoverage(state, operator)
	if out.Status != "unavailable" {
		applyPopupPredictionCoverage(&out, predictionsFor(state, operator))
	}
	return out
}

func (s *Server) popupStaticCoverage(state *State, operator string) api.PopupCoverage {
	out := api.PopupCoverage{Status: "partial", Message: "Horários planeados; tempos reais anteriores sem fonte comprovada.", HistoryCollectionStatus: "unavailable"}
	if s.Store != nil {
		out.HistoryCollectionStatus = api.PopupCoverageHistoryCollectionStatus(s.Store.historyCollectionStatus())
	}
	d := state.Static[operator]
	op := state.Operators[operator]
	if d == nil || d.Schedule == nil {
		out.Status = "unavailable"
		out.Message = "Horários e percurso indisponíveis nesta fonte."
		return out
	}
	out.SourceUpdatedAt = nil
	if d.Schedule.HasFrequencies {
		out.Status = "unavailable"
		out.Message = "Horários por frequência sem instância de viagem inequívoca."
		return out
	}
	if op.StaticStatus == "error" || time.Since(d.Updated) > 12*time.Hour {
		out.Status = "stale"
		out.Message = "Rede desatualizada; horários por confirmar."
	}
	return out
}

func applyPopupPredictionCoverage(out *api.PopupCoverage, predictions *CPData) {
	if predictions == nil {
		return
	}
	out.Message = popupHistoryMessage(predictions.Availability.Message)
	if popupPredictionsStale(predictions) {
		out.Status = "stale"
		out.Message = "Previsões antigas ou indisponíveis; consulte os horários planeados. Tempos reais anteriores sem fonte comprovada."
	}
}

func popupHistoryMessage(message string) string {
	const limitation = "Tempos reais anteriores sem fonte comprovada."
	if strings.Contains(strings.ToLower(message), strings.ToLower(limitation)) {
		return message
	}
	return strings.TrimSpace(message) + " " + limitation
}
func popupPredictionsStale(predictions *CPData) bool {
	a := predictions.Availability
	return a.Status == "stale" || a.Status == "error" || a.PublishedAt != nil && time.Since(*a.PublishedAt) > sourceFreshness
}
