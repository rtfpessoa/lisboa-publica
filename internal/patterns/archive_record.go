package patterns

import (
	"encoding/json"
	"fmt"
	"time"
)

// MetroRecordKind identifies whether an input was admitted as a full sample.
type MetroRecordKind uint8

const (
	MetroUnsampled MetroRecordKind = iota
	MetroSampled
	MetroGapSampled
)

// MetroRecordResult describes buffer admission, not filesystem durability.
type MetroRecordResult struct {
	Kind          MetroRecordKind
	Appended      bool
	Cut           MetroCutSnapshot
	Acknowledged  bool
	FailureMarked bool
}

// Record consumes the existing response without performing provider requests.
func (s *Service) Record(receipt Receipt, topology Topology) error {
	_, err := s.RecordMetro(receipt, topology)
	return err
}

// RecordMetro is the synchronous standalone path. FIFO owners use their lease.
func (s *Service) RecordMetro(receipt Receipt, topology Topology) (MetroRecordResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metroFIFO != nil {
		return MetroRecordResult{}, fmt.Errorf("Metro FIFO owns delivery")
	}
	return s.recordMetro(receipt, topology, true)
}

func (s *Service) recordMetro(receipt Receipt, topology Topology, standalone bool) (MetroRecordResult, error) {
	result, err := s.acceptMetro(receipt, topology)
	if err != nil {
		err = s.markRecordFailure(&result, err)
		return result, err
	}
	if standalone && receipt.Error == "" && result.Kind == MetroGapSampled && result.Appended {
		result.Acknowledged = s.metroCut.Acknowledge(result.Cut)
	}
	return result, nil
}

func (s *Service) markRecordFailure(result *MetroRecordResult, err error) error {
	marked := s.markPaused(err)
	result.FailureMarked = true
	return marked
}

func (s *Service) acceptMetro(receipt Receipt, topology Topology) (MetroRecordResult, error) {
	var result MetroRecordResult
	if err := s.checkMetroAdmission(receipt); err != nil {
		return result, err
	}
	receipt, topology, result.FailureMarked = s.prepareMetroReceipt(receipt, topology)
	if s.skipMetroReceipt(receipt, topology) {
		return result, nil
	}
	return s.acceptMetroSample(receipt, topology, result)
}

func (s *Service) checkMetroAdmission(receipt Receipt) error {
	if s.lock == nil {
		return fmt.Errorf("transport archive closed")
	}
	if s.manifestUncertain || s.checkpointUncertain {
		return fmt.Errorf("archive outcome uncertain; reopen required")
	}
	if s.detailFence != nil {
		return s.detailFence
	}
	if len(receipt.Rows) > 2000 {
		return fmt.Errorf("Metro receipt exceeds archive limits")
	}
	s.expireUnownedReservation()
	if s.maintenanceError != nil {
		return s.maintenanceError
	}
	if s.pressurePending {
		return fmt.Errorf("archive maintenance pending")
	}
	return nil
}

func (s *Service) prepareMetroReceipt(receipt Receipt, topology Topology) (Receipt, Topology, bool) {
	markedFailure := false
	if receipt.DeliveryGap || receipt.Error != "" {
		s.metroCut.MarkPending()
		s.applyMetroCut()
		markedFailure = receipt.Error != ""
	}
	receipt.Operator = "metro"
	receipt.DeliveryGap = receipt.DeliveryGap || s.pendingMetroDeliveryGap
	topology.Profile = fmt.Sprintf("%s-s%d-b%d", topology.Profile, int(s.config.SampleInterval/time.Second), s.config.BinSeconds)
	receipt.Profile, receipt.Topology = topology.Profile, &topology
	return receipt, topology, markedFailure
}

func (s *Service) skipMetroReceipt(receipt Receipt, topology Topology) bool {
	if receipt.DeliveryGap && !s.lastSample.IsZero() && !receipt.ReceivedAt.After(s.lastSample) {
		return true
	}
	return s.observeUnsampledMetro(receipt, topology)
}

func (s *Service) acceptMetroSample(receipt Receipt, topology Topology, result MetroRecordResult) (MetroRecordResult, error) {
	result.Cut = s.metroCut.Snapshot()
	appended, err := s.recordMetroSample(receipt, topology)
	result.Appended = appended
	if appended {
		result.Kind = MetroSampled
		if receipt.DeliveryGap {
			result.Kind = MetroGapSampled
		}
	}
	if err == nil && receipt.DeliveryGap && receipt.Error == "" {
		s.pendingMetroDeliveryGap = false
	}
	return result, err
}

func (s *Service) observeUnsampledMetro(receipt Receipt, topology Topology) bool {
	if s.lastSample.IsZero() || receipt.ReceivedAt.Sub(s.lastSample) >= s.config.SampleInterval {
		return false
	}
	if !receipt.DeliveryGap && receipt.ReceivedAt.After(s.lastSample) {
		s.engine.observeIntermediate(receipt, topology, s.config)
	}
	return true
}

// MetroSampleStats returns the diagnostic counters of the last processed sample.
func (s *Service) MetroSampleStats() MetroSampleStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.LastMetroStats
}

func (s *Service) recordMetroSample(receipt Receipt, topology Topology) (bool, error) {
	if err := s.prepareMetroSample(receipt); err != nil {
		return false, err
	}
	previousReceipt := s.engine.LastReceipt
	s.engine.step(receipt, topology, s.config)
	receipt.Outcomes = append([]Forecast{}, s.engine.Outcomes...)
	if s.engine.LastEvaluation.Equal(receipt.ReceivedAt) {
		receipt.Forecasts = append([]Forecast{}, s.engine.Live...)
	}
	if err := s.appendMetroSample(receipt); err != nil {
		// Inference has already changed the engine. Persist only the previously
		// admitted detail, then fence all further engine writes until reopen.
		persistErr := s.flushMetroDetail(s.lastSample)
		s.engine.LastReceipt = previousReceipt
		if persistErr != nil {
			err = fmt.Errorf("%w; preserving earlier detail: %v", err, persistErr)
		}
		return false, s.markPaused(s.fenceDetail(err, nil))
	}
	s.lastSample, s.topology = receipt.ReceivedAt, topology
	return true, s.checkpointMetroSample(receipt.ReceivedAt)
}

func (s *Service) prepareMetroSample(receipt Receipt) error {
	raw, err := boundedMetroReceipt(receipt)
	if err != nil {
		return err
	}
	if err = s.advanceMetroHour(receipt.ReceivedAt); err != nil {
		return err
	}
	if len(s.detail) > 0 && !receipt.ReceivedAt.After(s.detail[len(s.detail)-1].ReceivedAt) {
		return s.pause(fmt.Errorf("detail receipt clock regressed"))
	}
	if len(s.detail) >= maxDetailHourReceipts || s.detailBytes+len(raw)+1 > maxDetailHourBytes {
		return s.pause(fmt.Errorf("detail hour exceeds bounded buffer"))
	}
	return nil
}

func boundedMetroReceipt(receipt Receipt) ([]byte, error) {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	if len(raw) > 2<<20 || len(receipt.Rows) > 2000 {
		return nil, fmt.Errorf("Metro receipt exceeds archive limits")
	}
	return raw, nil
}

func (s *Service) advanceMetroHour(at time.Time) error {
	next := at.UTC().Truncate(time.Hour)
	if !s.hour.IsZero() && !next.Equal(s.hour) {
		if next.Before(s.hour) {
			return s.pause(fmt.Errorf("detail hour clock regressed"))
		}
		if err := s.flush(at); err != nil {
			return s.pause(err)
		}
		s.detail, s.detailBytes = []Receipt{}, 0
	}
	if !next.Equal(s.hour) {
		if err := s.loadDetailHour(next); err != nil {
			return s.pause(err)
		}
	}
	s.hour = next
	return nil
}

func (s *Service) appendMetroSample(receipt Receipt) error {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if len(s.detail) >= maxDetailHourReceipts || s.detailBytes+len(raw)+1 > maxDetailHourBytes {
		return s.pause(fmt.Errorf("detail and forecasts exceed hour buffer"))
	}
	s.detail = append(s.detail, receipt)
	s.detailBytes += len(raw) + 1
	return nil
}

func (s *Service) checkpointMetroSample(at time.Time) error {
	if !s.lastCheckpoint.IsZero() && at.Sub(s.lastCheckpoint) < s.config.CheckpointInterval {
		return nil
	}
	if err := s.flush(at); err != nil {
		return s.pause(err)
	}
	return nil
}

func (s *Service) pause(err error) error {
	if s.pressurePending && len(s.detail) > 0 {
		s.pressurePinned = true
		s.pressureBuffered = true
	}
	return s.markPaused(err)
}

func (s *Service) markPaused(err error) error {
	s.status, s.message = "paused", "Recolha histórica pausada; o tempo real mantém-se. Existem lacunas."
	return s.markMetroFailure(err)
}

func (s *Service) applyMetroCut() {
	s.engine.resetContinuity()
	s.pendingMetroDeliveryGap = true
}

// InterruptMetroContinuity records a standalone ordered delivery loss.
func (s *Service) InterruptMetroContinuity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metroFIFO != nil || s.lock == nil {
		return
	}
	s.metroCut.MarkPending()
	s.applyMetroCut()
}
