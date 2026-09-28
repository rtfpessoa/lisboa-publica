package patterns

import (
	"context"
	"time"
)

// View reads verified admitted generations with a bounded serialized file phase.
func (s *Service) View(ctx context.Context, stop string, episode ...string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	v := s.metroViewMetadata()
	stale := !freshSourceReceipt(s.engine.LastReceipt, now)
	if stale && v.Status == "collecting" {
		v.Status = "stale"
		v.Message += " A fonte já não é recente."
	}
	var err error
	v.StorageBytes, err = s.allocated()
	if err != nil {
		return v, err
	}
	if stop == "" {
		return v, nil
	}
	s.metroViewCases(&v, stop)
	selected := ""
	if len(episode) > 0 {
		selected = episode[0]
	}
	s.appendMetroForecasts(&v, stop, selected, now, stale)
	return s.patternView(ctx, v, stop)
}

func (s *Service) metroViewMetadata() View {
	v := View{Operator: "metro", Operators: s.operatorHistory(), CurrentDayType: dayType(time.Now()), Calendar: calendarVersion, Status: s.status, Message: s.message, Experimental: true, Profile: s.engine.Profile, LimitBytes: s.config.LimitBytes, SampleSeconds: int(s.config.SampleInterval / time.Second), BinSeconds: s.config.BinSeconds, DetailDays: s.config.DetailDays, AggregateMonths: s.config.AggregateMonths, Gaps: s.engine.Gaps, Patterns: []HourPattern{}, Forecasts: []Forecast{}, Evaluation: []EvaluationReport{}}
	if s.engine.LastReceipt.IsZero() && s.status == "collecting" {
		v.Status, v.Message = "waiting", "A aguardar a primeira amostra da API direta do Metro."
	}
	if s.engine.Limited {
		v.Message += " A janela efetiva de treino pode ser menor por limite de memória; consultar o suporte de cada componente."
	}
	if !s.engine.LastReceipt.IsZero() {
		at := s.engine.LastReceipt
		v.AsOf = &at
	}
	return v
}

func (s *Service) metroViewCases(v *View, stop string) {
	live := map[string]bool{}
	for _, g := range s.engine.Groups {
		live[g.ID] = true
	}
	for _, f := range s.engine.Cases {
		if f.Stop != stop || f.Evaluated {
			continue
		}
		// Evaluated cases are already counted by durable published aggregates.
		if live[f.Episode] {
			v.Pending++
		} else {
			v.LostReference++
		}
	}
}

func freshSourceReceipt(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= 90*time.Second
}

func (s *Service) appendMetroForecasts(v *View, stop, episode string, now time.Time, stale bool) {
	active := activeProviderEpisodes(s.engine)
	for _, f := range s.engine.Live {
		if f.Stop != stop && (episode == "" || f.Episode != episode) {
			continue
		}
		if stale || !active[f.Episode] || f.OwnValidUntil == nil || !f.OwnValidUntil.After(now) {
			withdrawOwnForecast(&f, "source_expired")
		}
		if f.OwnAt != nil && !f.OwnAt.After(now) {
			withdrawOwnForecast(&f, "estimated_arrival_already_past")
		}
		verifyMetroOfficialPoint(&f, now, stale)
		v.Forecasts = append(v.Forecasts, f)
	}
}

func verifyMetroOfficialPoint(f *Forecast, now time.Time, stale bool) {
	if f.OfficialAt == nil {
		return
	}
	fresh := f.SourceAt != nil && freshSourceReceipt(*f.SourceAt, now)
	if stale || !fresh || !f.OfficialAt.After(now) {
		f.OfficialAt = nil
	}
}
