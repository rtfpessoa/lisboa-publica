package app

// Changed equal-clock rows are corrections, never extra movement samples.
// Only the same retained stop/adjacent segment and platform may retract a model
// estimate; delayed/reused-reference forecasts cannot correct another episode.
func retractMetroMovementCorrections(t *metroTrack, current map[string]metroPoint) {
	for n, c := range t.Train.Calls {
		if c.Departure.Inferred == nil {
			continue
		}
		codes := []string{t.Codes[n]}
		if n+1 < len(t.Codes) {
			codes = append(codes, t.Codes[n+1])
		}
		for _, code := range codes {
			old, exists := t.Points[code]
			point, found := current[code]
			if !exists || !found || old.Platform != point.Platform || !metroPointClockConflict(old, point) {
				continue
			}
			if point.Clock.Before(c.Departure.Inferred.WindowStart) {
				continue
			}
			withdrawMetroDeparture(t, n, point.Clock, "Publicação corrigida no mesmo relógio/visita; estimativa retirada")
			break
		}
	}
}
