package patterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

func (s *Service) restoreProviders() error {
	ledgers, err := s.providerRevisionLedgers()
	if err != nil {
		return err
	}
	blocks := append([]block{}, s.index.Blocks...)
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Age.After(blocks[j].Age) })
	s.resetProviderDailyCaches()
	if err = s.restoreProviderDays(blocks); err != nil {
		return err
	}
	sort.Slice(blocks, func(i, j int) bool { return chronologicalProviderBlock(blocks[i], blocks[j]) })
	return s.replayProviderDetails(blocks, ledgers)
}

func chronologicalProviderBlock(a, b block) bool {
	if a.Age.Equal(b.Age) {
		return a.AsOf.Before(b.AsOf)
	}
	return a.Age.Before(b.Age)
}

func (s *Service) providerRevisionLedgers() (map[string][]ProviderCorrection, error) {
	ledgers := map[string][]ProviderCorrection{}
	for _, operator := range operatorOrder[1:] {
		values, err := s.providerCorrections(operator)
		if err != nil {
			return nil, err
		}
		ledgers[operator] = values
	}
	return ledgers, nil
}

func (s *Service) resetProviderDailyCaches() {
	for _, state := range s.providers {
		state.Engine.Aggregates = map[string]Aggregate{}
		state.Engine.ColdDays = map[string]bool{}
		state.Engine.DirtyDays = map[string]bool{}
		state.Engine.RecoveryThrough = map[string]int64{}
	}
}

func (s *Service) restoreProviderDays(blocks []block) error {
	for _, b := range blocks {
		if b.Operator == "metro" || b.Kind != "aggregate" {
			continue
		}
		if err := s.restoreProviderDay(b); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) replayProviderDetails(blocks []block, ledgers map[string][]ProviderCorrection) error {
	for _, b := range blocks {
		if b.Operator == "metro" || b.Kind != "observations" {
			continue
		}
		s.operatorEngine(b.Operator)
		state := s.providers[b.Operator]
		if !b.AsOf.After(state.Engine.LastReceipt) {
			continue
		}
		if err := s.replayProviderChunk(b, state, ledgers[b.Operator]); err != nil {
			return err
		}
	}
	for _, state := range s.providers {
		state.Engine.RecoveryThrough = nil
	}
	return nil
}

func (s *Service) replayProviderChunk(b block, state *providerState, ledger []ProviderCorrection) error {
	raw, err := s.decodedArchiveBlock(b)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var r ProviderReceipt
		err = decoder.Decode(&r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !boundedProviderReplay(r, b.Operator) {
			return fmt.Errorf("provider replay limit")
		}
		s.replayProviderReceipt(state, r, ledger)
	}
	return nil
}

func boundedProviderReplay(r ProviderReceipt, operator string) bool {
	checks := []bool{r.Operator == operator, len(r.Rows) <= 20000, len(r.Journeys) <= maxProviderPaths, len(r.Predictions) <= maxProviderCalls, len(r.Forecasts) <= maxProviderCalls, len(r.Cuts) <= maxProviderTracks}
	for _, valid := range checks {
		if !valid {
			return false
		}
	}
	return true
}

func (s *Service) replayProviderReceipt(state *providerState, r ProviderReceipt, ledger []ProviderCorrection) {
	// Bootstrap definitions may precede the checkpoint within the same chunk.
	for id, path := range r.Journeys {
		if validProviderPath(r.Operator, id, path) && len(state.Paths) < maxProviderPaths {
			state.Paths[id] = path
		}
	}
	if r.ReceivedAt.After(state.Engine.LastReceipt) {
		applyProviderCorrections(&r, ledger)
		state.step(r, s.config, true, true)
	}
	state.restoreReceiptCadence(r)
}

func (p *providerState) restoreReceiptCadence(r ProviderReceipt) {
	if !r.Partial && r.ReceivedAt.After(p.LastSample) {
		p.LastSample = r.ReceivedAt
	} else if r.Partial && len(p.PredictionSamples) < maxProviderCalls && r.ReceivedAt.After(p.PredictionSamples[r.PredictionStop]) {
		p.PredictionSamples[r.PredictionStop] = r.ReceivedAt
	}
}
