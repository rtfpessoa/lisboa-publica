package patterns

import "fmt"

// MetroFIFO is an exclusive delivery lease; it does not hold the archive mutex.
type MetroFIFO struct {
	service *Service
	closed  bool
}

// AcquireMetroFIFO serializes the transfer from direct calls to a single FIFO worker.
func (s *Service) AcquireMetroFIFO() (*MetroFIFO, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil || s.metroFIFO != nil {
		return nil, fmt.Errorf("Metro FIFO unavailable")
	}
	lease := &MetroFIFO{service: s}
	s.metroFIFO = lease
	return lease, nil
}

func (l *MetroFIFO) validLocked() bool {
	return l != nil && l.service != nil && !l.closed && l.service.lock != nil && l.service.metroFIFO == l
}

// RecordMetro processes one task at its ordered FIFO position.
func (l *MetroFIFO) RecordMetro(r Receipt, t Topology) (MetroRecordResult, error) {
	if l == nil || l.service == nil {
		return MetroRecordResult{}, fmt.Errorf("Metro FIFO unavailable")
	}
	s := l.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if !l.validLocked() {
		return MetroRecordResult{}, fmt.Errorf("Metro FIFO unavailable")
	}
	return s.recordMetro(r, t, false)
}

// InterruptMetroContinuity applies an already-signaled loss at the FIFO boundary.
func (l *MetroFIFO) InterruptMetroContinuity() error {
	if l == nil || l.service == nil {
		return fmt.Errorf("Metro FIFO unavailable")
	}
	s := l.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if !l.validLocked() {
		return fmt.Errorf("Metro FIFO unavailable")
	}
	s.applyMetroCut()
	return nil
}

// Close releases ownership only after the caller has stopped and accounted for its worker.
func (l *MetroFIFO) Close() error {
	if l == nil || l.service == nil {
		return fmt.Errorf("Metro FIFO unavailable")
	}
	s := l.service
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.closed {
		return nil
	}
	if !l.validLocked() {
		return fmt.Errorf("Metro FIFO unavailable")
	}
	l.closed = true
	s.metroFIFO = nil
	return nil
}
