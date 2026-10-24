package patterns

import (
	"fmt"
	"strings"
	"time"
)

func (s *Service) restoreArchive() error {
	if err := s.readManifest(); err != nil {
		return err
	}
	formatErr := s.restoreSegmentFormat()
	if formatErr != nil {
		s.fenceDetail(formatErr, nil)
	}
	if formatErr == nil && s.restoreMetroCheckpoint() == nil {
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
	if err != nil {
		return err
	}
	if found == nil {
		if s.segmentFormat {
			return s.fenceDetail(fmt.Errorf("Metro segment checkpoint is missing"), nil)
		}
		return nil
	}
	raw, err := s.decodedArchiveBlock(*found)
	if err != nil {
		return s.fenceDetail(err, found)
	}
	var cp checkpoint
	if err = knownJSON(raw, &cp); err != nil || !validMetroCheckpoint(cp) {
		return s.fenceDetail(fmt.Errorf("invalid or unknown Metro checkpoint"), found)
	}
	if err = s.validateRestoredCheckpoint(cp, found); err != nil {
		return err
	}
	s.applyCheckpoint(cp, *found)
	s.checkpointRecovered = checkpointHasMetroEvidence(cp)
	return nil
}

func (s *Service) validateRestoredCheckpoint(cp checkpoint, found *block) error {
	if (cp.SegmentStart != nil) != s.segmentFormat {
		return s.fenceDetail(fmt.Errorf("Metro segment format and checkpoint conflict"), found)
	}
	if err := validateDetailReceipts(cp.Detail, cp.Hour); err != nil {
		return s.fenceDetail(err, found)
	}
	if cp.SegmentStart != nil {
		work, err := segmentRetentionWork(cp.Detail)
		if err != nil {
			return s.fenceDetail(err, found)
		}
		s.segmentClockWork = work
	}
	return nil
}

func checkpointHasMetroEvidence(cp checkpoint) bool {
	return !cp.Hour.IsZero() || len(cp.Detail) > 0 || !cp.Engine.LastReceipt.IsZero() || len(cp.Engine.Aggregates) > 0 || len(cp.Engine.Cases) > 0
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
	return validProviderCheckpoint(cp.Providers) && validMetroTailRecovery(cp.TailRecovery) && validCheckpointSegment(cp)
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
	s.tailRecovery = cp.TailRecovery
	normalized := map[string]Aggregate{}
	for _, a := range s.engine.Aggregates {
		normalized[aggregateKey(a)] = a
	}
	s.engine.Aggregates = normalized
	s.lastCheckpoint = b.Age
	s.topology = cp.Topology
	s.checkpointHour, s.checkpointDetail = cp.Hour, cp.Detail
	if cp.SegmentStart != nil {
		s.segmentStart, s.checkpointSegmentStart = *cp.SegmentStart, *cp.SegmentStart
	}
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

// A legacy tail has no durable restart or global insertion order. Retain it
// without inference when it extends a recovered checkpoint.
func (s *Service) replayMetroDetails() error {
	checkpointHour, checkpointRows := s.hour, s.detail
	if err := s.validateRecoveryDetailIdentities(); err != nil {
		return err
	}
	if s.hasUnprovenMetroSegments(checkpointHour) {
		return s.fenceMetroRecoveryTail()
	}
	if !s.checkpointSegmentStart.IsZero() {
		return s.verifyCheckpointSegment()
	}
	latest := s.latestMetroDetail(checkpointHour)
	if latest == nil {
		return nil
	}
	if s.checkpointRecovered && latest.Age.After(checkpointHour) {
		return s.fenceMetroRecoveryTail()
	}
	receipts, err := s.recoverMetroDetail(latest, checkpointHour, checkpointRows)
	if err != nil {
		return err
	}
	if s.checkpointRecovered && len(receipts) > len(checkpointRows) {
		return s.fenceMetroRecoveryTail()
	}
	// With no checkpoint, verified detail is retained as raw evidence only.
	// Its timestamps form the append watermark, not recovered inference.
	s.setRecoveredMetroHour(latest.Age, receipts)
	return nil
}

// Check reserved identities before choosing a tail or starting maintenance.
// Invalid or duplicate keys cannot be treated as an absent detail generation.
func (s *Service) validateRecoveryDetailIdentities() error {
	invalid := invalidRecoveryDetailIdentities(s.index.Blocks)
	if len(invalid) == 0 {
		return nil
	}
	segmented := hasMetroDetailSegments(s.index.Blocks)
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if invalid[b.Key] || segmented && metroDetailIdentity(*b) {
			s.fenceDetail(fmt.Errorf("ambiguous detail generation"), b)
		}
	}
	return s.fenceMetroRecoveryTail()
}

func (s *Service) fenceMetroRecoveryTail() error {
	err := fmt.Errorf("unproven post-checkpoint Metro detail order requires recovery")
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Kind == "state" || b.Key == "metro:state" || s.recoveryTailBlock(*b) {
			s.fenceDetail(err, b)
		}
	}
	return s.fenceDetail(err, nil)
}

func (s *Service) recoveryTailBlock(b block) bool {
	if !metroDetailIdentity(b) {
		return false
	}
	if b.Kind == metroDetailSegmentKind || strings.HasPrefix(b.Key, metroDetailSegmentPrefix) {
		return !validRecoveryDetailIdentity(b) || !s.checkpointRecovered || b.Age.Add(metroDetailSegmentInterval).After(s.hour)
	}
	hour, err := time.Parse(time.RFC3339, strings.TrimPrefix(b.Key, "metro:detail:"))
	return err != nil || !hour.Before(s.hour) || !b.Age.Before(s.hour)
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
	if !s.lastSample.IsZero() || !s.engine.LastReceipt.IsZero() || len(s.engine.Aggregates) > 0 || len(s.engine.Cases) > 0 {
		s.engine.Gaps++
		s.pendingMetroDeliveryGap = true
		s.metroCut.MarkPending()
	}
}
