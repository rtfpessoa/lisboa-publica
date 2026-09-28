package patterns

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
)

func (s *Service) restoreArchive() error {
	if err := s.readManifest(); err != nil {
		return err
	}
	for _, b := range s.index.Blocks {
		if b.Kind == "state" {
			s.restoreCheckpoint(b)
		}
	}
	s.replayMetroDetails()
	s.retireUnverifiedMetroDays()
	if err := s.loadComponentHistory(); err != nil {
		s.status, s.message = "degraded", "Parte do histórico está indisponível."
	}
	if err := s.restoreProviders(); err != nil {
		s.status, s.message = "degraded", "Parte do histórico de transportes está indisponível."
	}
	s.resetRecoveredContinuity()
	return s.reconcile()
}

func (s *Service) restoreCheckpoint(b block) {
	raw, err := s.decodedArchiveBlock(b)
	if err != nil {
		s.status, s.message = "degraded", "Estado anterior indisponível; nova continuidade após reinício."
		return
	}
	var cp checkpoint
	if json.Unmarshal(raw, &cp) != nil || !validMetroCheckpoint(cp) {
		s.status = "degraded"
		return
	}
	s.applyCheckpoint(cp, b)
}

func validMetroCheckpoint(cp checkpoint) bool {
	if cp.Engine == nil || cp.Engine.Aggregates == nil {
		return false
	}
	if len(cp.Engine.Aggregates) > maxEngineAggregates || len(cp.Engine.Cases) > maxPendingForecasts {
		return false
	}
	return validProviderCheckpoint(cp.Providers)
}

func (s *Service) decodedArchiveBlock(b block) ([]byte, error) {
	blob, err := s.readBlock(b)
	if err != nil {
		return nil, err
	}
	return s.decoder.DecodeAll(blob, nil)
}

func (s *Service) applyCheckpoint(cp checkpoint, b block) {
	if cp.BinSeconds != s.config.BinSeconds || cp.SampleSeconds != int(s.config.SampleInterval/time.Second) {
		s.message = "Configuração alterada; novo perfil experimental."
	}
	s.providers = cp.Providers
	if s.providers == nil {
		s.providers = map[string]*providerState{}
	}
	s.engine = cp.Engine
	normalized := map[string]Aggregate{}
	for _, a := range s.engine.Aggregates {
		normalized[aggregateKey(a)] = a
	}
	s.engine.Aggregates = normalized
	s.lastCheckpoint = b.Age
	s.topology = cp.Topology
	s.setRecoveredMetroHour(cp.Hour, cp.Detail)
}

func (s *Service) setRecoveredMetroHour(hour time.Time, receipts []Receipt) {
	s.hour, s.detail, s.lastSample = hour, receipts, s.engine.LastReceipt
	data, _ := json.Marshal(receipts)
	s.detailBytes = len(data)
}

// Detail may have been published before a crash interrupted checkpointing.
func (s *Service) replayMetroDetails() {
	for _, b := range s.index.Blocks {
		if b.Kind != "detail" || s.topology.Profile == "" || b.Age.Before(s.hour) {
			continue
		}
		receipts := s.retainedMetroReceipts(b)
		for _, r := range receipts {
			if r.ReceivedAt.After(s.engine.LastReceipt) && r.Profile == s.topology.Profile {
				s.engine.step(r, s.topology, s.config)
			}
		}
		if len(receipts) > 0 {
			s.setRecoveredMetroHour(b.Age, receipts)
		}
	}
}

func (s *Service) retainedMetroReceipts(b block) []Receipt {
	raw, err := s.decodedArchiveBlock(b)
	if err != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var receipts []Receipt
	for len(receipts) < 1201 {
		var r Receipt
		if err = decoder.Decode(&r); err != nil {
			break
		}
		receipts = append(receipts, r)
	}
	if err != io.EOF {
		return nil
	}
	return receipts
}

func (s *Service) retireUnverifiedMetroDays() {
	admitted := map[string]bool{}
	for _, b := range s.index.Blocks {
		if b.Kind != "aggregate" || b.Operator != "metro" {
			continue
		}
		if _, err := s.readBlock(b); err == nil {
			admitted[b.Date] = true
		}
	}
	for key, a := range s.engine.Aggregates {
		if !admitted[a.Date] {
			delete(s.engine.Aggregates, key)
		}
	}
}

func (s *Service) resetRecoveredContinuity() {
	// A restart proves no continuity, even after degraded recovery.
	for _, p := range s.providers {
		p.Engine.RecoveryThrough = nil
		p.reset()
	}
	s.engine.resetContinuity()
	if !s.engine.LastReceipt.IsZero() {
		s.engine.Gaps++
	}
}
