package patterns

import (
	"fmt"
	"time"
)

func (s *Service) restoreArchive() error {
	if err := s.readManifest(); err != nil {
		return err
	}
	if err := s.restoreMetroCheckpoint(); err == nil {
		_ = s.replayMetroDetails()
	} else {
		s.protectCheckpointDetails()
	}
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

func (s *Service) restoreMetroCheckpoint() error {
	found, err := s.metroCheckpointBlock()
	if err != nil || found == nil {
		return err
	}
	raw, err := s.decodedArchiveBlock(*found)
	if err != nil {
		return s.fenceDetail(err, found)
	}
	var cp checkpoint
	if err = knownJSON(raw, &cp); err != nil || !validMetroCheckpoint(cp) {
		return s.fenceDetail(fmt.Errorf("invalid or unknown Metro checkpoint"), found)
	}
	if err = validateDetailReceipts(cp.Detail, cp.Hour); err != nil {
		return s.fenceDetail(err, found)
	}
	s.applyCheckpoint(cp, *found)
	return nil
}

func (s *Service) metroCheckpointBlock() (*block, error) {
	var found *block
	ambiguous := false
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Kind != "state" && b.Key != "metro:state" {
			continue
		}
		if found != nil || b.Kind != "state" || b.Key != "metro:state" || b.Operator != "metro" {
			ambiguous = true
		}
		if found == nil {
			copy := *b
			found = &copy
		}
	}
	if ambiguous {
		for i := range s.index.Blocks {
			b := &s.index.Blocks[i]
			if b.Kind == "state" || b.Key == "metro:state" {
				s.fenceDetail(fmt.Errorf("ambiguous Metro checkpoint"), b)
			}
		}
		return nil, s.detailFence
	}
	return found, nil
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
	s.hour, s.detail = hour, receipts
	s.detailBytes, _ = detailSize(receipts)
	s.lastSample = s.engine.LastReceipt
	if len(receipts) > 0 && receipts[len(receipts)-1].ReceivedAt.After(s.lastSample) {
		s.lastSample = receipts[len(receipts)-1].ReceivedAt
	}
}

// Replay only the checkpoint hour and latest detail; older generations are not
// rewritten by recovery and remain independently retained in the manifest.
func (s *Service) replayMetroDetails() error {
	checkpointHour, checkpointRows := s.hour, s.detail
	if err := s.checkMetroRecoveryTail(checkpointHour); err != nil {
		return err
	}
	latest := s.latestMetroDetail(checkpointHour)
	if !checkpointHour.IsZero() && latest != nil && latest.Age.After(checkpointHour) && len(checkpointRows) > 0 {
		if err := s.verifyCheckpointHour(checkpointHour, checkpointRows); err != nil {
			return err
		}
	}
	if latest == nil {
		return nil
	}
	receipts, err := s.recoverMetroDetail(latest, checkpointHour, checkpointRows)
	if err != nil {
		return err
	}
	s.replayRecoveredMetroRows(receipts)
	s.setRecoveredMetroHour(latest.Age, receipts)
	return nil
}

// A multi-hour tail needs a separately bounded recovery strategy. Do not
// replace the checkpoint after replaying only its newest hour.
func (s *Service) checkMetroRecoveryTail(checkpointHour time.Time) error {
	hours := map[time.Time]bool{}
	for _, b := range s.index.Blocks {
		if b.Kind == "detail" && b.Operator == "metro" && b.Age.After(checkpointHour) {
			hours[b.Age.UTC()] = true
		}
	}
	if len(hours) <= 1 {
		return nil
	}
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Kind == "detail" && b.Operator == "metro" && !b.Age.Before(checkpointHour) {
			s.fenceDetail(fmt.Errorf("multiple post-checkpoint detail hours require recovery"), b)
		}
	}
	return s.detailFence
}

func (s *Service) latestMetroDetail(checkpointHour time.Time) *block {
	var latest *block
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Kind != "detail" || b.Operator != "metro" || b.Age.Before(checkpointHour) {
			continue
		}
		if latest == nil || b.Age.After(latest.Age) {
			copy := *b
			latest = &copy
		}
	}
	return latest
}

func (s *Service) recoverMetroDetail(latest *block, checkpointHour time.Time, checkpointRows []Receipt) ([]Receipt, error) {
	prior, err := s.detailBlock(latest.Age)
	if err != nil {
		return nil, err
	}
	if prior == nil || prior.Key != latest.Key {
		return nil, s.fenceDetail(fmt.Errorf("invalid detail identity"), latest)
	}
	receipts, err := s.readDetailBlock(*prior)
	if err != nil {
		return nil, err
	}
	if latest.Age.Equal(checkpointHour) {
		receipts, err = s.reconcileDetailRows(checkpointRows, receipts, prior)
		if err != nil {
			return nil, err
		}
	}
	return receipts, nil
}

func (s *Service) replayRecoveredMetroRows(receipts []Receipt) {
	for _, r := range receipts {
		if r.ReceivedAt.After(s.engine.LastReceipt) && r.Profile == s.topology.Profile && s.topology.Profile != "" {
			s.engine.step(r, s.topology, s.config)
		}
	}
}

func (s *Service) verifyCheckpointHour(hour time.Time, rows []Receipt) error {
	prior, err := s.detailBlock(hour)
	if err != nil {
		return err
	}
	if prior == nil {
		return s.fenceDetail(fmt.Errorf("checkpoint-only detail has no durable generation"), nil)
	}
	receipts, err := s.readDetailBlock(*prior)
	if err != nil {
		return err
	}
	if !receiptPrefix(rows, receipts) {
		return s.fenceDetail(fmt.Errorf("checkpoint detail is not fully retained"), prior)
	}
	return nil
}

func (s *Service) reconcileDetailRows(checkpointRows, receipts []Receipt, prior *block) ([]Receipt, error) {
	if receiptPrefix(checkpointRows, receipts) {
		return receipts, nil
	}
	if receiptPrefix(receipts, checkpointRows) {
		return checkpointRows, nil
	}
	return nil, s.fenceDetail(fmt.Errorf("checkpoint and detail conflict"), prior)
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
