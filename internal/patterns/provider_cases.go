package patterns

func (p *providerState) finishProviderSample(r ProviderReceipt, c Config, replaying bool) {
	e := p.Engine
	e.LastReceipt = r.ReceivedAt
	p.retireCases(r.ReceivedAt, c)
	e.trim(r.ReceivedAt, c)
	e.Live = p.forecasts(r.ReceivedAt, c)
	if replaying {
		p.replayProviderCases(r, c)
		return
	}
	if !e.LastEvaluation.IsZero() && r.ReceivedAt.Sub(e.LastEvaluation) < c.EvaluationInterval {
		return
	}
	e.LastEvaluation = r.ReceivedAt
	for i := range e.Live {
		f := &e.Live[i]
		p.selectProviderCase(f)
		e.reportIssued(*f, c)
		p.retainProviderCase(*f)
	}
}

func (p *providerState) replayProviderCases(r ProviderReceipt, c Config) {
	for _, f := range r.Forecasts {
		p.Engine.reportIssued(f, c)
		if f.Episode != "" && len(p.Engine.Cases) < maxProviderCalls {
			p.Engine.Cases = append(p.Engine.Cases, f)
		}
	}
}

func (p *providerState) selectProviderCase(f *Forecast) {
	if f.OwnAt == nil {
		return
	}
	e := p.Engine
	key := selectionKey(*f)
	if _, exists := e.Selections[key]; !exists && len(e.Selections) < aggregateCapacity(e) {
		f.Selected = true
		e.Selections[key] = selection{Episode: f.Episode, At: f.IssuedAt.UnixNano()}
	}
}

func (p *providerState) retainProviderCase(f Forecast) {
	if f.Episode == "" {
		return
	}
	if len(p.Engine.Cases) < maxProviderCalls {
		p.Engine.Cases = append(p.Engine.Cases, f)
	} else {
		p.Engine.Limited = true
	}
}
