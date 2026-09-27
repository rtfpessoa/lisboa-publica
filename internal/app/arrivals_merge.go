package app

import (
	"fmt"
	"lisboapublica/internal/api"
	"strings"
	"time"
)

func arrivalInstanceKey(a api.Arrival) string {
	if a.PlanId == nil || a.SourceTripId == nil || a.ServiceDate == nil || a.StopSequence == nil {
		return ""
	}
	trip := *a.SourceTripId
	p, _ := providerByID(a.OperatorId)
	prefix := "[" + *a.PlanId + "][" + p.Agency + "]"
	if strings.HasPrefix(trip, prefix) {
		trip = qualify(a.OperatorId, strings.TrimPrefix(trip, prefix))
	}
	return *a.PlanId + ":" + trip + ":" + a.ServiceDate.Time.Format("20060102") + ":" + fmt.Sprint(*a.StopSequence)
}

func mergeArrivalRows(planned, published []api.Arrival, f Filter, now time.Time, state *State) []api.Arrival {
	out := []api.Arrival{}
	matched := map[string]bool{}
	for _, a := range published {
		if !arrivalInWindow(a, f, now) {
			continue
		}
		if a.VehicleId != nil && !currentArrivalVehicle(state, a, now) {
			a.VehicleId = nil
		}
		out = append(out, a)
		if k := arrivalInstanceKey(a); k != "" {
			matched[k] = true
		}
	}
	for _, a := range planned {
		if !matched[arrivalInstanceKey(a)] {
			out = append(out, a)
		}
	}
	sortArrivals(out)
	return out
}

func arrivalInWindow(a api.Arrival, f Filter, now time.Time) bool {
	at := a.ExpectedAt
	if at == nil {
		at = a.ScheduledAt
	}
	valid := at != nil && (f.Route == "" || a.RouteId == f.Route)
	if valid {
		valid = !at.Before(f.From) && at.Before(f.To)
	}
	return valid && (a.ValidUntil == nil || now.Before(*a.ValidUntil))
}

func arrivalAvailability(a api.ArrivalAvailability, rows []api.Arrival, f Filter) api.ArrivalAvailability {
	planned := arrivalHasKind(rows, "scheduled")
	predicted := arrivalHasKind(rows, "prediction")
	if planned && a.PlannedStatus == "unavailable" {
		a.PlannedStatus = "ok"
	}
	if a.CoverageUntil != nil && f.To.After(*a.CoverageUntil) && a.Status == "ok" {
		a.Status = "partial"
		if strings.HasPrefix(f.Stop, "cm:") {
			a.PlannedStatus = "partial"
		}
	}
	a.Message = arrivalStatusMessage(a.Status, planned, predicted)
	return a
}

func arrivalHasKind(rows []api.Arrival, kind api.ArrivalKind) bool {
	for _, r := range rows {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

func arrivalStatusMessage(status api.ArrivalAvailabilityStatus, planned, predicted bool) string {
	message := ""
	switch status {
	case "loading":
		message = "A carregar próximas passagens…"
	case "partial":
		message = "Dados publicados incompletos para este intervalo."
	case "stale":
		message = "Dados antigos. Previsões atuais indisponíveis."
	case "error", "unavailable":
		message = arrivalFallbackMessage(planned)
	case "ok":
		message = arrivalPublishedMessage(planned, predicted)
	}
	return message
}

func arrivalFallbackMessage(planned bool) string {
	if planned {
		return "Previsões indisponíveis; mostramos horários planeados."
	}
	return "Próximas passagens indisponíveis."
}

func arrivalPublishedMessage(planned, predicted bool) string {
	if predicted {
		return "Previsões publicadas pelo operador."
	}
	if planned {
		return arrivalFallbackMessage(true)
	}
	return "Sem passagens publicadas para este intervalo."
}
