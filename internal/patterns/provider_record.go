package patterns

import (
	"fmt"
	"time"
)

type providerRecording struct {
	service            *Service
	state              *providerState
	receipt            ProviderReceipt
	sampled, committed bool
	backup             *providerState
	admittedBefore     map[string]bool
}

// RecordProvider archives normalized accepted publications without provider calls.
func (s *Service) RecordProvider(r ProviderReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateProviderInput(r); err != nil {
		return err
	}
	if !s.operators[r.Operator] {
		return nil
	}
	return s.recordEnabledProvider(r)
}

func (s *Service) validateProviderInput(r ProviderReceipt) error {
	if s.lock == nil {
		return fmt.Errorf("transport archive closed")
	}
	if !knownOperator(r.Operator) || r.Operator == "metro" {
		return fmt.Errorf("unsupported provider archive")
	}
	if s.operators[r.Operator] && !boundedProviderInput(r) {
		return fmt.Errorf("provider receipt limit")
	}
	return nil
}

func boundedProviderInput(r ProviderReceipt) bool {
	checks := []bool{!r.ReceivedAt.IsZero(), len(r.Rows) <= 20000, len(r.Journeys) <= maxProviderPaths, len(r.Predictions) <= maxProviderCalls, len(r.Cuts) <= maxProviderTracks}
	for _, valid := range checks {
		if !valid {
			return false
		}
	}
	return true
}

func (s *Service) recordEnabledProvider(r ProviderReceipt) (result error) {
	s.operatorEngine(r.Operator)
	state := s.providers[r.Operator]
	r.Profile = "normalized-source-v2-s" + durationNumber(s.config.SampleInterval)
	last := state.LastSample
	if r.Partial {
		last = state.PredictionSamples[r.PredictionStop]
	}
	if r.ReceivedAt.Before(state.LastInputAt) || !last.IsZero() && r.ReceivedAt.Before(last) {
		return fmt.Errorf("provider receipt clock regression")
	}
	sampled := providerSampleDue(last, r, s.config.SampleInterval)
	job := providerRecording{service: s, state: state, receipt: r, sampled: sampled}
	if err := job.prepareBackup(); err != nil {
		return err
	}
	defer func() { job.rollback(result) }()
	return job.record()
}

func (j *providerRecording) record() error {
	if !j.sampled {
		j.state.step(j.receipt, j.service.config, false, false)
		return nil
	}
	return j.recordSample()
}

func (j *providerRecording) recordSample() error {
	if err := j.mergePendingCuts(); err != nil {
		return err
	}
	j.state.step(j.receipt, j.service.config, true, false)
	j.retainSampleMetadata()
	if err := j.publishDetail(); err != nil {
		j.state.reset()
		return err
	}
	j.committed = true
	return j.checkpointAfterDetail()
}

func (j *providerRecording) mergePendingCuts() error {
	if j.receipt.Cuts == nil {
		j.receipt.Cuts = map[string]bool{}
	}
	for id, withdraw := range j.state.PendingCuts {
		j.receipt.Cuts[id] = j.receipt.Cuts[id] || withdraw
	}
	if len(j.receipt.Cuts) > maxProviderTracks {
		return fmt.Errorf("provider cut limit")
	}
	return nil
}

func (j *providerRecording) retainSampleMetadata() {
	r, p := &j.receipt, j.state
	r.Gap = r.Gap || p.PendingGap
	p.PendingGap, p.PendingCuts = false, map[string]bool{}
	if r.Partial {
		p.recordPredictionSample(*r)
	} else {
		p.LastSample = r.ReceivedAt
	}
	r.Outcomes = append([]Forecast{}, p.Engine.Outcomes...)
	if p.Engine.LastEvaluation.Equal(r.ReceivedAt) {
		r.Forecasts = append([]Forecast{}, p.Engine.Live...)
	}
}

func (p *providerState) recordPredictionSample(r ProviderReceipt) {
	if len(p.PredictionSamples) < maxProviderCalls || !p.PredictionSamples[r.PredictionStop].IsZero() {
		p.PredictionSamples[r.PredictionStop] = r.ReceivedAt
	} else {
		p.Engine.Limited = true
	}
}

func (j *providerRecording) checkpointAfterDetail() error {
	s := j.service
	if !s.lastCheckpoint.IsZero() && j.receipt.ReceivedAt.Sub(s.lastCheckpoint) < s.config.CheckpointInterval {
		return nil
	}
	if err := s.flush(j.receipt.ReceivedAt); err != nil {
		j.state.reset()
		return err
	}
	return nil
}

func providerSampleDue(last time.Time, r ProviderReceipt, interval time.Duration) bool {
	return last.IsZero() || r.ReceivedAt.Sub(last) >= interval || r.Gap || r.Error != ""
}
