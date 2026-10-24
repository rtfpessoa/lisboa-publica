package patterns

import (
	"errors"
	"fmt"
	"time"
)

const defaultMaintenanceInterval = time.Minute
const maintenanceRetryInterval = time.Second
const unownedReservationTTL = 5 * time.Second
const maxUnownedMaintenancePasses = 2

// MaintenanceProgress is a bounded snapshot of the background worker, independent
// of the archive mutex. Startup cleanup is separate from these worker passes.
type MaintenanceProgress struct {
	WorkerStartedAt, LastStartedAt, LastCompletedAt time.Time
	Interval                                        time.Duration
	LastResult                                      string
	InProgress, Stopped                             bool
}

// MaintenanceProgress can be read even when the worker is waiting for, or
// holding, the archive mutex. It does not report the archive's overall health.
func (s *Service) MaintenanceProgress() MaintenanceProgress {
	if progress := s.maintenanceProgress.Load(); progress != nil {
		return *progress
	}
	return MaintenanceProgress{LastResult: "never_started"}
}

// startMaintenance runs under the existing archive owner; Close stops and joins it.
func (s *Service) startMaintenance(pending bool) {
	s.maintenanceStop = make(chan struct{})
	s.maintenanceDone = make(chan struct{})
	s.maintenanceWake = make(chan struct{}, 1)
	interval := s.config.MaintenanceInterval
	if interval == 0 {
		interval = defaultMaintenanceInterval
	}
	s.maintenanceProgress.Store(&MaintenanceProgress{WorkerStartedAt: time.Now(), Interval: interval, LastResult: "never_started"})
	delay := interval
	if errors.Is(s.maintenanceError, errMaintenancePending) || (s.maintenanceError == nil && pending) {
		delay = maintenanceRetryInterval
	}
	go s.runMaintenance(interval, delay)
}

func (s *Service) runMaintenance(interval, delay time.Duration) {
	defer s.finishMaintenanceWorker()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for s.waitMaintenance(timer) {
		progress := s.beginMaintenancePass()
		s.mu.Lock()
		pending := s.maintenancePass(time.Now().UTC())
		delay = s.nextMaintenanceDelay(interval, pending)
		failed := s.maintenanceError != nil
		s.mu.Unlock()
		s.finishMaintenancePass(progress, pending, failed)
		timer.Reset(delay)
	}
}

func (s *Service) finishMaintenanceWorker() {
	progress := s.MaintenanceProgress()
	progress.Stopped = true
	s.maintenanceProgress.Store(&progress)
	close(s.maintenanceDone)
}

func (s *Service) beginMaintenancePass() MaintenanceProgress {
	progress := s.MaintenanceProgress()
	progress.InProgress = true
	progress.LastStartedAt = time.Now()
	s.maintenanceProgress.Store(&progress)
	return progress
}

func (s *Service) finishMaintenancePass(progress MaintenanceProgress, pending, failed bool) {
	progress.InProgress = false
	progress.LastResult = "completed"
	if pending {
		progress.LastResult = "pending"
	} else if failed {
		progress.LastResult = "error"
	}
	progress.LastCompletedAt = time.Now()
	s.maintenanceProgress.Store(&progress)
}

func (s *Service) waitMaintenance(timer *time.Timer) bool {
	select {
	case <-s.maintenanceStop:
		return false
	case <-timer.C:
	case <-s.maintenanceWake:
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	select {
	case <-s.maintenanceStop:
		return false
	default:
		return true
	}
}

func (s *Service) nextMaintenanceDelay(interval time.Duration, pending bool) time.Duration {
	delay := interval
	if pending {
		delay = maintenanceRetryInterval
	}
	if s.pressurePending && !s.pressurePinned {
		if remaining := time.Until(s.pressureDeadline); remaining < delay {
			delay = remaining
		}
	}
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}

// maintenancePass is called with the archive mutex held. It performs bounded
// oldest-first expiry and budget recovery without requiring provider records.
func (s *Service) maintenancePass(now time.Time) bool {
	s.expireUnownedReservation()
	if s.pressurePending && !s.pressurePinned {
		s.pressurePasses++
	}
	work := &cleanupWork{}
	if err := s.ensureSpaceFor(s.pressureBytes, s.pressureFiles, now, work); err != nil {
		return s.maintenanceFailure(err)
	}
	if s.pressureManifest && !s.pressureBuffered {
		return s.expireForManifest(now, work)
	}
	if s.pressureBuffered {
		if err := s.retryBufferedPublication(now, work); err != nil {
			return s.maintenanceFailure(err)
		}
	}
	s.clearPressureReservation()
	s.maintenanceError = nil
	if s.manifestUncertain {
		return s.maintenanceFailure(fmt.Errorf("archive manifest outcome uncertain; reopen required"))
	}
	pending, err := s.expireSome(now, work)
	if err != nil {
		return s.maintenanceFailure(err)
	}
	return pending
}

func (s *Service) maintenanceFailure(err error) bool {
	s.maintenanceError = s.markPaused(err)
	return !s.manifestUncertain && errors.Is(err, errMaintenancePending)
}

func (s *Service) expireForManifest(now time.Time, work *cleanupWork) bool {
	pending, err := s.expireSome(now, work)
	if err != nil {
		return s.maintenanceFailure(err)
	}
	// No payload is retained for this one-shot request. Release it after
	// one bounded expiry pass; its caller can submit a fresh candidate.
	s.clearPressureReservation()
	s.maintenanceError = nil
	return pending
}

func (s *Service) retryBufferedPublication(now time.Time, work *cleanupWork) error {
	// Keep the owner mutex until the retained publication has actually
	// consumed the reclaimed space. All nested admissions share this pass's
	// cleanup budget, so retrying cannot start an unbounded retirement loop.
	reservation := archiveReservation{bytes: s.pressureBytes, files: s.pressureFiles, pinned: true}
	s.clearPressureReservation()
	s.maintenanceWork = work
	err := s.flush(now)
	s.maintenanceWork = nil
	if err != nil {
		if !s.pressurePending {
			s.reserveMaintenance(reservation)
		}
		s.pressurePinned, s.pressureBuffered = true, true
	}
	return err
}

// A failed one-shot publication has no retained payload to retry. Limit its
// reclamation so an abandoned request cannot drive indefinite FIFO eviction.
func (s *Service) expireUnownedReservation() {
	if s.pressurePending && !s.pressurePinned && (s.pressurePasses >= maxUnownedMaintenancePasses || !time.Now().Before(s.pressureDeadline)) {
		s.clearPressureReservation()
	}
}

func (s *Service) clearPressureReservation() {
	s.pressurePending, s.pressureBytes, s.pressureFiles = false, 0, 0
	s.pressurePinned, s.pressureBuffered, s.pressureDeadline, s.pressurePasses = false, false, time.Time{}, 0
	s.pressureManifest = false
}

func (s *Service) reserveMaintenance(reservation archiveReservation) {
	first := !s.pressurePending
	s.pressurePending = true
	s.pressureBytes = max(s.pressureBytes, reservation.bytes)
	s.pressureFiles = max(s.pressureFiles, reservation.files)
	s.pressurePinned = s.pressurePinned || reservation.pinned
	if first {
		s.pressureDeadline = time.Now().Add(unownedReservationTTL)
		select {
		case s.maintenanceWake <- struct{}{}:
		default:
		}
	}
}

func (s *Service) cleanupBudget() *cleanupWork {
	if s.maintenanceWork != nil {
		return s.maintenanceWork
	}
	return &cleanupWork{}
}

// expireSome removes at most a bounded amount of verified expired data. A
// partial non-pressure pass does not block a capacity-safe admission.
func (s *Service) expireSome(now time.Time, work *cleanupWork) (bool, error) {
	if s.manifestUncertain {
		return false, fmt.Errorf("archive manifest outcome uncertain; reopen required")
	}
	s.closeRetainedBlocks(now)
	for work.retirements < maxRetirements {
		oldest, expired, pending := s.retirementCandidate(now, false, work)
		if pending {
			return true, nil
		}
		if !expired {
			return false, nil
		}
		if err := s.retireVerified(oldest, now, false, work); errors.Is(err, errMaintenancePending) {
			return true, nil
		} else if errors.Is(err, errRetentionChanged) {
			continue
		} else if err != nil {
			return true, err
		}
	}
	return true, nil
}
