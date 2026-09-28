package patterns

import (
	"strconv"
	"time"
)

// MetroPopupSignal binds the popup's independently retained original transitions
// to a supported existing forecast episode. Reference equality alone is insufficient.
type MetroPopupSignal struct {
	Stop       string
	Start, End time.Time
}

// MetroPopupForecastQuery carries one association episode's identity and evidence.
type MetroPopupForecastQuery struct {
	Train, Route, Direction, Profile string
	Signals                          []MetroPopupSignal
	Now                              time.Time
}

func (s *Service) MetroPopupForecasts(q MetroPopupForecastQuery) []Forecast {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Forecast{}
	if s.lock == nil || s.status != "collecting" || !freshSourceReceipt(s.engine.LastReceipt, q.Now) {
		return out
	}
	g := s.engine.Groups[groupKey(groupIdentity{Train: q.Train, Route: q.Route, Direction: q.Direction})]
	if !s.supportsMetroPopup(g, q) {
		return out
	}
	for _, f := range s.engine.Live {
		if metroPopupForecastMatches(f, g, q) {
			copy := f
			copy.Components = append([]Component{}, f.Components...)
			out = append(out, copy)
		}
	}
	return out
}
func (s *Service) supportsMetroPopup(g *group, q MetroPopupForecastQuery) bool {
	if g == nil || !g.Active {
		return false
	}
	profile := q.Profile + "-s" + strconv.Itoa(int(s.config.SampleInterval/time.Second)) + "-b" + strconv.Itoa(s.config.BinSeconds)
	return s.topology.Profile == profile && matchedMetroPopupSignals(g.Signals, q.Signals) >= 3
}
func matchedMetroPopupSignals(signals []signal, candidates []MetroPopupSignal) int {
	used := map[string]bool{}
	matched := 0
	for _, signal := range signals {
		if signal.Supported && matchMetroPopupSignal(signal, candidates, used) {
			matched++
		}
	}
	return matched
}
func matchMetroPopupSignal(signal signal, candidates []MetroPopupSignal, used map[string]bool) bool {
	for _, candidate := range candidates {
		key := candidate.Stop + candidate.Start.String() + candidate.End.String()
		window := !candidate.Start.Before(signal.L) && !candidate.End.After(signal.U)
		if signal.Stop == candidate.Stop && !used[key] && window {
			used[key] = true
			return true
		}
	}
	return false
}
func metroPopupForecastMatches(f Forecast, g *group, q MetroPopupForecastQuery) bool {
	identity := f.Episode == g.ID && f.Train == q.Train && f.Route == q.Route && f.Direction == q.Direction
	return identity && metroPopupForecastFresh(f, q.Now)
}
func metroPopupForecastFresh(f Forecast, now time.Time) bool {
	return f.OwnAt != nil && f.OwnValidUntil != nil && f.OwnValidUntil.After(now) && f.OwnAt.After(now)
}
