package patterns

import (
	"context"
	"fmt"
	"time"
)

type providerReadContext struct {
	state          *providerState
	now            time.Time
	enabled, stale bool
	active         map[string]bool
}

// ProviderView separates current forecast support from retained statistical history.
func (s *Service) ProviderView(ctx context.Context, operator, stop, episode string) (View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !knownOperator(operator) || operator == "metro" {
		return View{}, fmt.Errorf("invalid provider selection")
	}
	e := s.operatorEngine(operator)
	state := s.providers[operator]
	now := time.Now().UTC()
	q := providerReadContext{state: state, now: now, enabled: s.operators[operator], active: activeProviderEpisodes(e)}
	q.stale = !q.enabled || state.LastReceipt.IsZero() || now.Sub(state.LastReceipt) > 90*time.Second
	v := s.providerViewMetadata(operator, q)
	var err error
	v.StorageBytes, err = s.allocated()
	if err != nil {
		return v, err
	}
	q.appendForecasts(&v, stop, episode)
	q.appendPendingCases(&v, stop)
	if stop == "" {
		return v, nil
	}
	return s.patternView(ctx, v, stop)
}

func activeProviderEpisodes(e *engine) map[string]bool {
	active := map[string]bool{}
	for _, g := range e.Groups {
		if g.Active {
			active[g.ID] = true
		}
	}
	return active
}

func (s *Service) providerViewMetadata(operator string, q providerReadContext) View {
	e := q.state.Engine
	v := View{Operator: operator, Operators: s.operatorHistory(), Status: "waiting", Message: "A aguardar dados publicados compatíveis. As estimativas usam estados nas paragens; não são validação física.", Experimental: true, Profile: e.Profile, Calendar: calendarVersion, CurrentDayType: dayType(time.Now()), LimitBytes: s.config.LimitBytes, SampleSeconds: int(s.config.SampleInterval / time.Second), BinSeconds: s.config.BinSeconds, DetailDays: s.config.DetailDays, AggregateMonths: s.config.AggregateMonths, Gaps: e.Gaps, Patterns: []HourPattern{}, Forecasts: []Forecast{}, Evaluation: []EvaluationReport{}}
	if !q.enabled {
		v.Status = "disabled"
	} else if !e.LastReceipt.IsZero() {
		v.Status = "collecting"
		at := e.LastReceipt
		v.AsOf = &at
	}
	if e.Limited {
		v.Message += " Suporte limitado pela capacidade ou por dados publicados parciais."
	}
	q.setCollectionStatus(&v)
	return v
}

func (q providerReadContext) setCollectionStatus(v *View) {
	if q.state.LastReceipt.IsZero() && q.enabled {
		v.Status = "waiting"
	}
	if q.state.FullError != "" && q.enabled {
		v.Status = "paused"
		v.Message += " Falha na última recolha de posições."
	}
	if q.stale && v.Status == "collecting" {
		v.Status = "stale"
		v.Message += " A fonte já não é recente."
	}
}

func (q providerReadContext) appendForecasts(v *View, stop, episode string) {
	for _, f := range q.state.Engine.Live {
		if f.Stop != stop && (episode == "" || f.Episode != episode) {
			continue
		}
		if q.stale || !q.active[f.Episode] {
			withdrawOwnForecast(&f, "source_expired")
		}
		if f.OwnAt != nil && !f.OwnAt.After(q.now) {
			withdrawOwnForecast(&f, "estimated_arrival_already_past")
		}
		q.verifyOfficialPoint(&f)
		v.Forecasts = append(v.Forecasts, f)
	}
}

func withdrawOwnForecast(f *Forecast, reason string) {
	if f.OwnAt != nil {
		f.Unavailable = reason
	}
	f.OwnAt, f.LowerAt, f.UpperAt = nil, nil, nil
}

func (q providerReadContext) verifyOfficialPoint(f *Forecast) {
	if f.OfficialAt == nil {
		return
	}
	trip := f.ProviderTrip
	if track := q.state.Tracks[f.Train]; track != nil {
		trip = track.Previous.Trip
	}
	for _, prediction := range q.state.Predictions {
		if !forecastPredictionMatches(*f, prediction, trip) || !freshPrediction(prediction, q.now) {
			continue
		}
		f.SourceAt = prediction.SourceAt
		return
	}
	f.OfficialAt = nil
}

func forecastPredictionMatches(f Forecast, p ProviderPrediction, trip string) bool {
	if p.Stop != f.Stop || p.Route != f.Route || p.Trip != trip {
		return false
	}
	return p.ExpectedAt.Equal(*f.OfficialAt)
}

func (q providerReadContext) appendPendingCases(v *View, stop string) {
	for _, f := range q.state.Engine.Cases {
		if f.Stop != stop || f.Evaluated {
			continue
		}
		if q.active[f.Episode] {
			v.Pending++
		} else {
			v.LostReference++
		}
	}
}
