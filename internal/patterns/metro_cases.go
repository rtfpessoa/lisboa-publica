package patterns

import "time"

func (e *engine) activeMetroEpisodes() map[string]bool {
	active := map[string]bool{}
	for _, g := range e.Groups {
		active[g.ID] = true
	}
	return active
}

func (e *engine) pruneMetroCases(sample metroSample) {
	active := e.activeMetroEpisodes()
	cases := e.Cases[:0]
	for _, f := range e.Cases {
		if f.Evaluated {
			continue
		}
		if !active[f.Episode] {
			e.loseMetroReference(f, sample)
			continue
		}
		cases = append(cases, f)
	}
	e.Cases = cases
	for key, selection := range e.Selections {
		if !active[selection.Episode] {
			delete(e.Selections, key)
		}
	}
}

func (e *engine) loseMetroReference(f Forecast, sample metroSample) {
	f.Result = "lost_support"
	e.Outcomes = append(e.Outcomes, f)
	agg := baseAggregate(aggregateRequest{f.IssuedAt, f.Route, f.Direction, f.Stop, f.Platform, f.Profile, f.Condition, "lost_reference:" + f.Function}, sample.config)
	agg.Count, agg.KnownAt = 1, sample.now.UnixNano()
	e.add(agg)
}

func (e *engine) issueMetroCases(sample metroSample) {
	if e.Replaying || sample.now.Sub(e.LastEvaluation) < sample.config.EvaluationInterval {
		return
	}
	e.LastEvaluation = sample.now
	for i, f := range e.Live {
		// Unassociated official rows cannot acquire an association reference.
		e.reportIssued(f, sample.config)
		if f.Episode == "" {
			continue
		}
		if len(e.Cases) >= maxPendingForecasts {
			break
		}
		f = e.selectMetroCase(f, sample.now)
		e.Live[i] = f
		e.Cases = append(e.Cases, f)
	}
}

func (e *engine) selectMetroCase(f Forecast, now time.Time) Forecast {
	if f.OwnAt == nil {
		return f
	}
	key := selectionKey(f)
	_, exists := e.Selections[key]
	f.Selected = !exists
	if !exists {
		e.Selections[key] = selection{f.Episode, now.UnixNano()}
	}
	return f
}
