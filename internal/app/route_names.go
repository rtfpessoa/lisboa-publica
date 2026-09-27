package app

import "lisboapublica/internal/api"

// Search aliases only; published identifiers and route names are never rewritten.
func passengerRouteSearchName(route api.RouteDetail) string {
	if route.OperatorId == "metro" {
		return "Linha " + route.LongName
	}
	if route.OperatorId == "cp" {
		return map[string]string{"AP": "Alfa Pendular", "IC": "Intercidades", "R": "Regional", "IR": "InterRegional"}[route.ShortName]
	}
	return ""
}
