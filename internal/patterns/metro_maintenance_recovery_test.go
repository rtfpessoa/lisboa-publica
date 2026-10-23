package patterns

import (
	"errors"
	"testing"
	"time"
)

type metroTestRecorder func(time.Time, string) (MetroRecordResult, error)

func TestMetroMaintenanceFailureRequiresSuccessfulPassBeforeRecovery(t *testing.T) {
	for _, delivery := range []string{"standalone", "fifo"} {
		t.Run(delivery, func(t *testing.T) {
			s, record, base := maintenanceRecoveryFixture(t, delivery)
			assertMaintenanceBlocksMetro(t, s, record, base)
			assertMetroRecoversAfterMaintenance(t, s, record, base, delivery)
		})
	}
}

func maintenanceRecoveryFixture(t *testing.T, delivery string) (*Service, metroTestRecorder, time.Time) {
	t.Helper()
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Drive maintenance explicitly so the failing and recovering passes
	// cannot race a background retry.
	s.maintenanceOnce.Do(func() { close(s.maintenanceStop) })
	<-s.maintenanceDone
	base := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	if err = s.Record(durabilityReceipt(base, "before"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if delivery == "fifo" {
		lease, err := s.AcquireMetroFIFO()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = lease.Close() })
		return s, func(at time.Time, id string) (MetroRecordResult, error) {
			return lease.RecordMetro(durabilityReceipt(at, id), testTopology())
		}, base
	}
	return s, func(at time.Time, id string) (MetroRecordResult, error) {
		return s.RecordMetro(durabilityReceipt(at, id), testTopology())
	}, base
}

func assertMaintenanceBlocksMetro(t *testing.T, s *Service, record metroTestRecorder, base time.Time) {
	t.Helper()
	ready := s.MetroCut().Snapshot()
	s.mu.Lock()
	s.config.MinFreeBytes = 1
	s.spaceProbe = func(string) (archiveSpace, error) {
		return archiveSpace{}, errors.New("injected filesystem probe failure")
	}
	s.maintenancePass(base.Add(time.Second))
	if s.maintenanceError == nil || s.pressurePending {
		s.mu.Unlock()
		t.Fatal("fixture did not produce an unreserved maintenance failure")
	}
	before, sample, checkpoint := digest(s.detail), s.lastSample, s.lastCheckpoint
	s.mu.Unlock()
	failedCut := s.MetroCut().Snapshot()
	if !failedCut.Pending || s.MetroCut().Claim(ready) {
		t.Fatal("failed maintenance did not invalidate the prior cut")
	}
	result, err := record(base.Add(30*time.Second), "blocked")
	if err == nil || result.Appended || result.Acknowledged || !result.FailureMarked || result.Kind != MetroUnsampled || !s.MetroCut().Snapshot().Pending {
		t.Fatalf("maintenance failure admitted a due gap sample: %+v %v", result, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if digest(s.detail) != before || !s.lastSample.Equal(sample) || !s.lastCheckpoint.Equal(checkpoint) {
		t.Fatal("blocked receipt changed the retained checkpoint or buffer")
	}
}

func assertMetroRecoversAfterMaintenance(t *testing.T, s *Service, record metroTestRecorder, base time.Time, delivery string) {
	t.Helper()
	s.mu.Lock()
	s.spaceProbe = func(string) (archiveSpace, error) { return archiveSpace{bytes: 1 << 50, inodes: 1 << 40}, nil }
	s.maintenancePass(base.Add(2 * time.Second))
	if s.maintenanceError != nil || s.pressurePending {
		s.mu.Unlock()
		t.Fatal("successful maintenance did not clear the failure")
	}
	s.mu.Unlock()
	if !s.MetroCut().Snapshot().Pending {
		t.Fatal("maintenance alone cleared the ordered gap")
	}
	result, err := record(base.Add(31*time.Second), "after")
	if err != nil || !result.Appended || result.Kind != MetroGapSampled {
		t.Fatalf("recovered sample did not carry the gap: %+v %v", result, err)
	}
	if delivery == "fifo" {
		if result.Acknowledged || !s.MetroCut().Acknowledge(result.Cut) {
			t.Fatal("FIFO owner could not acknowledge the recovered cut")
		}
	} else if !result.Acknowledged {
		t.Fatal("standalone recovery did not acknowledge its cut")
	}
	if s.MetroCut().Snapshot().Pending {
		t.Fatal("successful ordered recovery left the cut pending")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.detail) != 2 || !s.detail[1].DeliveryGap {
		t.Fatal("recovered buffer did not retain exactly the ordered gap sample")
	}
}
