package app

import (
	"lisboapublica/internal/api"
	"time"
)

func (s *Server) popupCoverage(state *State, operator string) api.PopupCoverage {
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
	applyPopupPredictionCoverage(&out, predictionsFor(state, operator))
	return out
}

func applyPopupPredictionCoverage(out *api.PopupCoverage, predictions *CPData) {
	if predictions == nil {
		return
	}
	out.Message = predictions.Availability.Message + " Tempos reais anteriores sem fonte comprovada."
	if popupPredictionsStale(predictions) {
		out.Status = "stale"
		out.Message = "Previsões antigas ou indisponíveis; consulte os horários planeados. Tempos reais anteriores sem fonte comprovada."
	}
}
func popupPredictionsStale(predictions *CPData) bool {
	a := predictions.Availability
	return a.Status == "stale" || a.Status == "error" || a.PublishedAt != nil && time.Since(*a.PublishedAt) > sourceFreshness
}
