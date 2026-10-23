package patterns

import (
	"encoding/json"
	"fmt"
	"time"
)

// Record consumes the existing response without performing provider requests.
func (s *Service) Record(receipt Receipt, topology Topology) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return fmt.Errorf("transport archive closed")
	}
	if s.detailFence != nil {
		return s.markPaused(s.detailFence)
	}
	if len(receipt.Rows) > 2000 {
		return fmt.Errorf("Metro receipt exceeds archive limits")
	}
	s.expireUnownedReservation()
	if s.pressurePending {
		return s.markPaused(s.maintenanceError)
	}
	receipt.Operator = "metro"
	receipt.DeliveryGap = receipt.DeliveryGap || s.pendingMetroDeliveryGap
	topology.Profile = fmt.Sprintf("%s-s%d-b%d", topology.Profile, int(s.config.SampleInterval/time.Second), s.config.BinSeconds)
	receipt.Profile, receipt.Topology = topology.Profile, &topology
	if !receipt.DeliveryGap && s.observeUnsampledMetro(receipt, topology) {
		return nil
	}
	err := s.recordMetroSample(receipt, topology)
	if err == nil && receipt.DeliveryGap {
		s.pendingMetroDeliveryGap = false
	}
	return err
}

func (s *Service) observeUnsampledMetro(receipt Receipt, topology Topology) bool {
	if s.lastSample.IsZero() || receipt.ReceivedAt.Sub(s.lastSample) >= s.config.SampleInterval {
		return false
	}
	if receipt.ReceivedAt.After(s.lastSample) {
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

func (s *Service) recordMetroSample(receipt Receipt, topology Topology) error {
	if err := s.prepareMetroSample(receipt); err != nil {
		return err
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
		return s.markPaused(s.fenceDetail(err, nil))
	}
	s.lastSample, s.topology = receipt.ReceivedAt, topology
	return s.checkpointMetroSample(receipt.ReceivedAt)
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
	s.engine.resetContinuity()
	s.pendingMetroDeliveryGap = true
	return err
}

// InterruptMetroContinuity records an ordered delivery loss without changing
// frozen forecasts, aggregate history or source clocks.
func (s *Service) InterruptMetroContinuity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine.resetContinuity()
	s.pendingMetroDeliveryGap = true
}
