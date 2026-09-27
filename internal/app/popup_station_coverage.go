package app

import (
	"lisboapublica/internal/api"
	"time"
)

// A station board describes its assembled evidence, not the availability of
// an operator-wide feed that may supply none of the displayed predictions.
func (s *Server) stationCoverage(state *State, operator string, calls []api.StopCall, view arrivalSnapshot) api.PopupCoverage {
	out := s.popupStaticCoverage(state, operator)
	d := state.Static[operator]
	now := time.Now()
	planned := d != nil && d.Schedule != nil && !d.Schedule.HasFrequencies || stationHasSchedule(calls, now)
	predicted, clock := stationForecastClock(calls, now)
	out.SourceUpdatedAt = clock
	if planned || predicted {
		applyStationEvidenceCoverage(&out, planned, predicted)
	} else {
		applyStationArrivalAvailability(&out, view)
	}
	out.Message = popupHistoryMessage(out.Message)
	return out
}

// Timetable and forecast evidence take precedence over unrelated feed status.
func applyStationEvidenceCoverage(out *api.PopupCoverage, planned, predicted bool) {
	if planned && out.Status == "stale" {
		out.Message = "Rede desatualizada; horários por confirmar."
		return
	}
	out.Status = "partial"
	out.Message = "Horários planeados; sem previsões disponíveis para este quadro."
	if predicted {
		out.Message = "Previsões disponíveis; cobertura parcial."
		if !planned {
			out.Message += " Horários planeados indisponíveis."
		}
	}
}

// Without usable evidence, expose the requested-stop collector's availability.
func applyStationArrivalAvailability(out *api.PopupCoverage, view arrivalSnapshot) {
	if view.static == nil {
		return
	}
	switch view.availability.Status {
	case "loading":
		out.Status = "loading"
		out.Message = "A carregar previsões para esta paragem."
	case "stale", "error":
		out.Status = "stale"
		out.Message = "Previsões antigas ou indisponíveis; horários planeados indisponíveis."
	}
}

func stationHasSchedule(calls []api.StopCall, now time.Time) bool {
	for _, call := range calls {
		for _, value := range []api.CallTime{call.Arrival, call.Departure} {
			if value.Kind != "unavailable" && value.Schedule != nil && !value.Schedule.At.Before(now) {
				return true
			}
		}
	}
	return false
}

func stationForecastClock(calls []api.StopCall, now time.Time) (bool, *time.Time) {
	available := false
	var clock *time.Time
	for _, call := range calls {
		for _, value := range []api.CallTime{call.Arrival, call.Departure} {
			p := value.Prediction
			if value.Kind != "prediction" || p == nil || p.At.Before(now) || p.ValidUntil == nil || !now.Before(*p.ValidUntil) {
				continue
			}
			available = true
			if p.SourceUpdatedAt != nil && (clock == nil || p.SourceUpdatedAt.After(*clock)) {
				clock = p.SourceUpdatedAt
			}
		}
	}
	return available, clock
}
