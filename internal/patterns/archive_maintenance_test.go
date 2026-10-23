package patterns

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestArchiveConvergesAfterLimitIsLowered(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 30
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	old := block{Kind: "detail", Operator: "metro", Age: now.AddDate(0, 0, -8)}
	old.Key = "metro:detail:" + old.Age.Format(time.RFC3339)
	random := make([]byte, 1300<<10)
	_, _ = rand.New(rand.NewSource(4)).Read(random)
	raw, err := json.Marshal(Receipt{ReceivedAt: old.Age.Add(time.Minute), Operator: "metro", Raw: mustJSON(t, random)})
	if err != nil {
		t.Fatal(err)
	}
	blob := s.encoder.EncodeAll(append(raw, '\n'), nil)
	if err = s.publish(old, blob, now); err != nil {
		t.Fatal(err)
	}
	oldFile := s.index.Blocks[0].File
	used, err := s.allocated()
	if err != nil || used <= 1<<20 {
		t.Fatalf("fixture must exceed target limit: %d %v", used, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	config.LimitBytes = 1 << 20
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = os.Stat(filepath.Join(config.Directory, oldFile)); !os.IsNotExist(err) {
		t.Fatalf("old eligible generation was not removed: %v", err)
	}
	used, err = s.allocated()
	if err != nil || used > config.LimitBytes {
		t.Fatalf("archive remains over limit: %d %v", used, err)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestIdleArchiveMaintenanceRetiresExpiredAndJoins(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = 500 * time.Millisecond
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	recent := checkpointFixture(t, s, "recent", now.Add(-time.Hour), now)
	old := checkpointFixture(t, s, "old", now.AddDate(0, 0, -8), now)
	changed := make(chan struct{}, 1)
	s.mu.Lock()
	s.manifestSync = func(path string) error {
		err := syncDirectory(path)
		if err == nil {
			select {
			case changed <- struct{}{}:
			default:
			}
		}
		return err
	}
	s.mu.Unlock()
	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("idle maintenance did not publish an expiry")
	}
	s.mu.Lock()
	_, oldErr := os.Stat(filepath.Join(config.Directory, old.File))
	_, recentErr := os.Stat(filepath.Join(config.Directory, recent.File))
	s.mu.Unlock()
	if !os.IsNotExist(oldErr) || recentErr != nil {
		t.Fatalf("wrong retention: old=%v recent=%v", oldErr, recentErr)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.maintenanceDone:
	default:
		t.Fatal("Close returned before maintenance stopped")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}

func checkpointFixture(t *testing.T, s *Service, name string, age, now time.Time) block {
	t.Helper()
	value := MetroJourneyCheckpoint{Journey: name, Revision: 1, SourceAt: age, Generation: "test", CommittedAt: age, Payload: json.RawMessage(`{}`)}
	s.mu.Lock()
	defer s.mu.Unlock()
	blob, err := s.encodeMetroCheckpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	b := block{Key: "metro-popup-checkpoint:" + name, Kind: "popup-checkpoint", Operator: "metro", Age: age}
	if err = s.publish(b, blob, now); err != nil {
		t.Fatal(err)
	}
	for _, got := range s.index.Blocks {
		if got.Key == b.Key {
			return got
		}
	}
	t.Fatal("fixture generation missing")
	return block{}
}

func TestArchiveFilesystemReserveRetiresOldAndProtectsRecent(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	recent := checkpointFixture(t, s, "recent", now.Add(-time.Hour), now)
	old := checkpointFixture(t, s, "old", now.AddDate(0, 0, -8), now)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.MinFreeBytes, s.config.MinFreeInodes = 2<<20, 3
	s.spaceProbe = func(string) (archiveSpace, error) {
		_, err := os.Stat(filepath.Join(config.Directory, old.File))
		if err == nil {
			return archiveSpace{bytes: 1 << 20, inodes: 2}, nil
		}
		return archiveSpace{bytes: 4 << 20, inodes: 9}, nil
	}
	if err = s.ensureSpaceFor(4096, 2, now, &cleanupWork{}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(config.Directory, old.File)); !os.IsNotExist(err) {
		t.Fatalf("old file retained: %v", err)
	}
	if _, err = os.Stat(filepath.Join(config.Directory, recent.File)); err != nil {
		t.Fatalf("recent file removed: %v", err)
	}
}

func TestArchiveFilesystemProbeFailureAndNoManifestSpaceFailClosed(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	old := checkpointFixture(t, s, "old", now.AddDate(0, 0, -8), now)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.MinFreeBytes = 1
	s.spaceProbe = func(string) (archiveSpace, error) { return archiveSpace{}, errors.New("synthetic statfs failure") }
	if err = s.ensureSpace(0, now); err == nil {
		t.Fatal("missing filesystem measurement admitted")
	}
	if s.maintenanceError == nil {
		t.Fatal("measurement failure was not reflected in status")
	}
	if _, err = os.Stat(filepath.Join(config.Directory, old.File)); err != nil {
		t.Fatal("data removed without measurement", err)
	}
	s.spaceProbe = func(string) (archiveSpace, error) { return archiveSpace{bytes: 1, inodes: 1}, nil }
	if err = s.ensureSpace(4096, now); err == nil {
		t.Fatal("retirement succeeded without space for manifest")
	}
	if _, err = os.Stat(filepath.Join(config.Directory, old.File)); err != nil {
		t.Fatal("data removed without durable manifest", err)
	}
}

func TestArchiveCandidateChecksRemainBoundedAcrossManifestChanges(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	s.mu.Lock()
	defer s.mu.Unlock()
	blocks := manifest{Version: 1}
	name := "metro-invalid-shared.json.zst"
	blob := []byte("invalid generation")
	if err = os.WriteFile(filepath.Join(config.Directory, name), blob, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxRetentionChecks+2; i++ {
		age := now.AddDate(0, 0, -20).Add(time.Duration(i) * time.Minute)
		blocks.Blocks = append(blocks.Blocks, block{Key: fmt.Sprintf("metro-popup-checkpoint:%02d", i), File: name, Hash: digestBytes(blob), Kind: "popup-checkpoint", Operator: "metro", Age: age, Closed: true})
	}
	// The first real candidate follows older unreadable generations.
	age := now.AddDate(0, 0, -8)
	value := MetroJourneyCheckpoint{Journey: "eligible", Revision: 1, SourceAt: age, Generation: "g", CommittedAt: age, Payload: json.RawMessage(`{}`)}
	blob, err = s.encodeMetroCheckpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	eligible := namedGeneration(block{Key: "metro-popup-checkpoint:eligible", Kind: "popup-checkpoint", Operator: "metro", Age: age, Closed: true}, blob)
	if err = os.WriteFile(filepath.Join(config.Directory, eligible.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	blocks.Blocks = append(blocks.Blocks, eligible)
	if err = s.saveManifest(blocks); err != nil {
		t.Fatal(err)
	}
	work := &cleanupWork{}
	if _, _, pending := s.retirementCandidate(now, false, work); !pending || work.checks != maxRetentionChecks {
		t.Fatalf("candidate verification not bounded: checks=%d pending=%v", work.checks, pending)
	}
	// Manifest updates between passes do not invalidate already verified older
	// entries or permit a newer candidate to leapfrog a newly inserted older one.
	older := now.AddDate(0, 0, -9)
	value.Journey, value.SourceAt, value.CommittedAt = "inserted", older, older
	insertedBlob, err := s.encodeMetroCheckpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	inserted := namedGeneration(block{Key: "metro-popup-checkpoint:inserted", Kind: "popup-checkpoint", Operator: "metro", Age: older, Closed: true}, insertedBlob)
	if err = os.WriteFile(filepath.Join(config.Directory, inserted.File), insertedBlob, 0600); err != nil {
		t.Fatal(err)
	}
	next := manifest{Version: 1, Blocks: append(append([]block{}, s.index.Blocks...), inserted)}
	if err = s.saveManifest(next); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 3; pass++ {
		work = &cleanupWork{}
		index, found, pending := s.retirementCandidate(now, false, work)
		if work.checks > maxRetentionChecks {
			t.Fatalf("too many generation checks: %d", work.checks)
		}
		if pending {
			continue
		}
		if !found || s.index.Blocks[index].File != inserted.File {
			t.Fatalf("did not select the new oldest valid candidate: found=%v", found)
		}
		return
	}
	t.Fatal("candidate discovery did not make progress across passes")
}

func TestArchiveReverifiesBeforeRetiringCachedCandidate(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	old := checkpointFixture(t, s, "old", now.AddDate(0, 0, -8), now)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeRetainedBlocks(now)
	work := &cleanupWork{}
	index, found, pending := s.retirementCandidate(now, false, work)
	if !found || pending {
		t.Fatal("failed to find valid old generation")
	}
	path := filepath.Join(config.Directory, old.File)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err = os.WriteFile(path, data, info.Mode()); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err = s.retireVerified(index, now, false, work); !errors.Is(err, errRetentionChanged) {
		t.Fatalf("corrupt cached generation was not fenced: %v", err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("corrupt generation was deleted", err)
	}
	if len(s.index.Blocks) != 1 {
		t.Fatal("corrupt generation was removed from manifest")
	}
}

func TestArchivePressureRetainsExactSevenDayBoundary(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Second)
	boundary := now.AddDate(0, 0, -7)
	protected := checkpointFixture(t, s, "boundary", boundary, now)
	older := checkpointFixture(t, s, "older", boundary.Add(-time.Nanosecond), now)
	s.mu.Lock()
	defer s.mu.Unlock()
	used, err := s.allocated()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ensureSpace(config.LimitBytes-used+1, now); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(config.Directory, older.File)); !os.IsNotExist(err) {
		t.Fatalf("older generation retained: %v", err)
	}
	if _, err = os.Stat(filepath.Join(config.Directory, protected.File)); err != nil {
		t.Fatalf("seven-day boundary removed: %v", err)
	}
}

func TestArchiveRetirementSyncUncertaintyRequiresReopen(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	recent := checkpointFixture(t, s, "recent", now.Add(-time.Hour), now)
	old := checkpointFixture(t, s, "old", now.AddDate(0, 0, -8), now)
	s.mu.Lock()
	s.manifestSync = func(string) error { return errors.New("synthetic directory sync failure") }
	_, err = s.expireSome(now, &cleanupWork{})
	if err == nil || !s.manifestUncertain {
		s.mu.Unlock()
		t.Fatal("uncertain retirement was not fenced")
	}
	if _, err = os.Stat(filepath.Join(config.Directory, old.File)); err != nil {
		s.mu.Unlock()
		t.Fatal("old generation deleted before certainty", err)
	}
	if _, err = os.Stat(filepath.Join(config.Directory, recent.File)); err != nil {
		s.mu.Unlock()
		t.Fatal("recent generation deleted", err)
	}
	if err = s.ensureSpace(0, now); err == nil {
		s.mu.Unlock()
		t.Fatal("uncertain archive accepted writes")
	}
	s.mu.Unlock()
	if err = s.Close(); err == nil {
		t.Fatal("close hid uncertain manifest")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = os.Stat(filepath.Join(config.Directory, recent.File)); err != nil {
		t.Fatal("reopen lost surviving generation", err)
	}
}

func TestSharedGenerationSurvivesRetirementAndReplacement(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	age := now.AddDate(0, 0, -8)
	value := MetroJourneyCheckpoint{Journey: "shared", Revision: 1, SourceAt: age, Generation: "g", CommittedAt: age, Payload: json.RawMessage(`{}`)}
	s.mu.Lock()
	defer s.mu.Unlock()
	blob, err := s.encodeMetroCheckpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	old := namedGeneration(block{Key: "metro-popup-checkpoint:old", Kind: "popup-checkpoint", Operator: "metro", Age: age, Closed: true}, blob)
	first, second := old, old
	first.Key, first.Age = "metro-popup-checkpoint:first", now.Add(-time.Hour)
	second.Key, second.Age = "metro-popup-checkpoint:second", now.Add(-time.Hour)
	if err = os.WriteFile(filepath.Join(config.Directory, old.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.saveManifest(manifest{Version: 1, Blocks: []block{old, first, second}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.expireSome(now, &cleanupWork{}); err != nil {
		t.Fatal(err)
	}
	if len(s.index.Blocks) != 2 {
		t.Fatal("old entry was not retired")
	}
	if _, err = s.readBlock(second); err != nil {
		t.Fatal("retirement unlinked shared generation", err)
	}
	value.Revision = 2
	replacement, err := s.encodeMetroCheckpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.publish(first, replacement, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.readBlock(second); err != nil {
		t.Fatal("replacement unlinked shared generation", err)
	}
	txn := &archiveTransaction{service: s}
	txn.removeFiles([]string{old.File})
	if _, err = s.readBlock(second); err != nil {
		t.Fatal("transaction cleanup unlinked shared generation", err)
	}
}

func TestArchiveMaintenanceHonorsFailedPublicationReservation(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 30
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	var old []block
	s.mu.Lock()
	for i := 0; i < 4; i++ {
		age := now.AddDate(0, 0, -8).Add(time.Duration(i) * time.Hour)
		random := make([]byte, 256<<10)
		_, _ = rand.New(rand.NewSource(int64(i + 10))).Read(random)
		raw, marshalErr := json.Marshal(Receipt{ReceivedAt: age.Add(time.Minute), Operator: "metro", Raw: mustJSON(t, random)})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		blob := s.encoder.EncodeAll(append(raw, '\n'), nil)
		b := block{Key: "metro:detail:" + age.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: age}
		if err = s.publish(b, blob, now); err != nil {
			t.Fatal(err)
		}
		old = append(old, s.index.Blocks[len(s.index.Blocks)-1])
	}
	info, err := os.Stat(filepath.Join(config.Directory, old[0].File))
	if err != nil {
		t.Fatal(err)
	}
	fileBytes := info.Sys().(*syscall.Stat_t).Blocks * 512
	used, err := s.allocated()
	if err != nil {
		t.Fatal(err)
	}
	s.config.LimitBytes = used + fileBytes/2
	reserve := 2*fileBytes + 4096
	build := func() manifest { return s.index }
	err = s.admitManifestFiles(build, reserve, 2, now)
	if !errors.Is(err, errMaintenancePending) || !s.pressurePending {
		t.Fatalf("publication did not enter bounded maintenance: %v", err)
	}
	if s.pressureBytes < reserve || s.pressureFiles != 3 {
		t.Fatalf("lost reservation: bytes=%d files=%d", s.pressureBytes, s.pressureFiles)
	}
	usedAfter, err := s.allocated()
	if err != nil || usedAfter >= s.config.LimitBytes || usedAfter+reserve <= s.config.LimitBytes {
		t.Fatalf("fixture must fit without but exceed limit with reservation: used=%d limit=%d reserve=%d err=%v", usedAfter, s.config.LimitBytes, reserve, err)
	}
	if got := s.metroViewMetadata().Status; got != "paused" {
		t.Fatalf("pending pressure status = %s", got)
	}
	// A waiting publication cannot consume the space reclaimed for this one.
	if err = s.admitManifestFiles(build, 4096, 1, now); !errors.Is(err, errMaintenancePending) {
		t.Fatalf("other admission passed the pressure barrier: %v", err)
	}
	s.mu.Unlock()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("background maintenance did not satisfy the reservation")
		case <-tick.C:
			s.mu.Lock()
			pending, maintenanceErr := s.pressurePending, s.maintenanceError
			remaining, usedErr := s.allocated()
			s.mu.Unlock()
			if !pending {
				if maintenanceErr != nil || usedErr != nil || remaining+reserve > s.config.LimitBytes {
					t.Fatalf("reservation falsely cleared: used=%d reserve=%d error=%v allocation=%v", remaining, reserve, maintenanceErr, usedErr)
				}
				if _, err = os.Stat(filepath.Join(config.Directory, old[1].File)); !os.IsNotExist(err) {
					t.Fatalf("background did not retire a second generation: %v", err)
				}
				return
			}
		}
	}
}

func TestArchiveMaintenanceHardFailureBacksOffAndPreservesDetail(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = 4 * time.Second
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	probes := make(chan struct{}, 10)
	now := time.Now().UTC()
	receipt := testReceipt(now, testRow("A", "x", now, 100))
	s.mu.Lock()
	s.detail = []Receipt{receipt}
	s.detailBytes = 100
	before := digest(s.detail)
	s.config.MinFreeBytes = 1
	s.spaceProbe = func(string) (archiveSpace, error) {
		probes <- struct{}{}
		return archiveSpace{}, errors.New("synthetic filesystem failure")
	}
	err = s.ensureSpaceFor(4096, 2, now, &cleanupWork{})
	if err == nil || !s.pressurePending {
		t.Fatal("failure did not retain reservation", err)
	}
	s.mu.Unlock()
	<-probes // initial admission
	select {
	case <-probes: // immediate background retry
	case <-time.After(2 * time.Second):
		t.Fatal("maintenance did not wake for pending reservation")
	}
	if err = s.Record(receipt, testTopology()); err == nil {
		t.Fatal("record admitted while pressure was unresolved")
	}
	s.mu.Lock()
	if digest(s.detail) != before || !s.pendingMetroDeliveryGap || s.metroViewMetadata().Status != "paused" {
		t.Fatal("blocked receipt discarded buffered evidence or omitted pause/gap")
	}
	s.mu.Unlock()
	select {
	case <-probes:
		t.Fatal("hard failure was retried at the short progress cadence")
	case <-time.After(1300 * time.Millisecond):
	}
	s.mu.Lock()
	if s.maintenanceError == nil || !s.pressurePending {
		t.Fatal("hard failure or reservation was cleared prematurely")
	}
	s.spaceProbe = func(string) (archiveSpace, error) { return archiveSpace{bytes: 1 << 30, inodes: 100}, nil }
	s.maintenancePass(now)
	if s.maintenanceError != nil || s.pressurePending || digest(s.detail) != before {
		t.Fatal("recovery did not preserve buffered evidence")
	}
	s.mu.Unlock()
}

func TestAbandonedReservationStopsReclamation(t *testing.T) {
	for _, reason := range []string{"deadline", "pass limit"} {
		t.Run(reason, func(t *testing.T) {
			config := DefaultConfig(t.TempDir())
			config.DetailDays = 30
			s, err := Open(config)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.mu.Lock()
			defer s.mu.Unlock()
			now := time.Now().UTC().Truncate(time.Hour)
			age := now.AddDate(0, 0, -8)
			raw, err := json.Marshal(Receipt{ReceivedAt: age.Add(time.Minute), Operator: "metro"})
			if err != nil {
				t.Fatal(err)
			}
			blob := s.encoder.EncodeAll(append(raw, '\n'), nil)
			old := block{Key: "metro:detail:" + age.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: age}
			if err = s.publish(old, blob, now); err != nil {
				t.Fatal(err)
			}
			old = s.index.Blocks[0]
			used, err := s.allocated()
			if err != nil {
				t.Fatal(err)
			}
			s.config.LimitBytes = used + 4096
			if err = s.ensureSpaceFor(8192, 2, now, &cleanupWork{retirements: maxRetirements}); !errors.Is(err, errMaintenancePending) {
				t.Fatalf("expected reservation to require cleanup: %v", err)
			}
			if reason == "deadline" {
				s.pressureDeadline = time.Now().Add(-time.Second)
			} else {
				s.pressurePasses = maxUnownedMaintenancePasses
			}
			s.maintenancePass(now)
			if s.pressurePending || s.maintenanceError != nil {
				t.Fatal("abandoned reservation was not released")
			}
			if _, err = os.Stat(filepath.Join(config.Directory, old.File)); err != nil {
				t.Fatal("abandoned reservation evicted a retained generation", err)
			}
			if err = s.ensureSpaceFor(s.config.LimitBytes+1, 2, now, &cleanupWork{}); err == nil || s.pressurePending {
				t.Fatal("impossible reservation triggered cleanup", err)
			}
			if _, err = os.Stat(filepath.Join(config.Directory, old.File)); err != nil {
				t.Fatal("impossible reservation evicted a retained generation", err)
			}
		})
	}
}

func TestBufferedReservationDoesNotExpire(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 30
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Hour)
	age := now.AddDate(0, 0, -8)
	raw, err := json.Marshal(Receipt{ReceivedAt: age.Add(time.Minute), Operator: "metro"})
	if err != nil {
		t.Fatal(err)
	}
	blob := s.encoder.EncodeAll(append(raw, '\n'), nil)
	old := block{Key: "metro:detail:" + age.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: age}
	if err = s.publish(old, blob, now); err != nil {
		t.Fatal(err)
	}
	old = s.index.Blocks[0]
	used, err := s.allocated()
	if err != nil {
		t.Fatal(err)
	}
	s.config.LimitBytes = used + 4096
	s.detail = []Receipt{{ReceivedAt: now}}
	err = s.ensureSpaceFor(8192, 2, now, &cleanupWork{retirements: maxRetirements})
	if !errors.Is(err, errMaintenancePending) {
		t.Fatal(err)
	}
	s.pause(err)
	s.pressureDeadline = time.Now().Add(-time.Second)
	s.pressurePasses = maxUnownedMaintenancePasses
	s.maintenancePass(now)
	if _, err = os.Stat(filepath.Join(config.Directory, old.File)); !os.IsNotExist(err) {
		t.Fatalf("buffered reservation expired before cleanup: %v", err)
	}
	if len(s.detail) != 1 {
		t.Fatal("buffered evidence was discarded")
	}
}

func TestFailedPublicationKeepsReferencedGeneration(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	value := MetroJourneyCheckpoint{Journey: "shared", Revision: 1, SourceAt: now, Generation: "g", CommittedAt: now, Payload: json.RawMessage(`{}`)}
	blob, err := s.encodeMetroCheckpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	b := namedGeneration(block{Key: "metro-popup-checkpoint:new", Kind: "popup-checkpoint", Operator: "metro", Age: now}, blob)
	shared := b
	shared.Key = "metro-popup-checkpoint:existing"
	if err = os.WriteFile(filepath.Join(config.Directory, b.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.saveManifest(manifest{Version: 1, Blocks: []block{shared}}); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(config.Directory, ".manifest.tmp")
	if err = os.WriteFile(blocker, []byte("injected pre-rename failure"), 0600); err != nil {
		t.Fatal(err)
	}
	next := manifest{Version: 1, Blocks: []block{shared, b}}
	err = s.commitGeneration(next, b, "", blob)
	if removeErr := os.Remove(blocker); removeErr != nil {
		t.Fatal(removeErr)
	}
	if err == nil || s.manifestUncertain {
		t.Fatalf("expected pre-rename failure, got %v", err)
	}
	if _, err = s.readBlock(shared); err != nil {
		t.Fatal("failed publication unlinked a referenced generation", err)
	}
}

type manifestPressureFixture struct {
	service    *Service
	now        time.Time
	payload    []byte
	detail     block
	receipt    Receipt
	competitor block
}

func newManifestPressureFixture(t *testing.T) manifestPressureFixture {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Run passes explicitly to attempt a competitor before the retained retry.
	s.maintenanceOnce.Do(func() { close(s.maintenanceStop) })
	<-s.maintenanceDone
	now := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	s.mu.Lock()
	defer s.mu.Unlock()
	age := now.AddDate(0, 0, -8).Truncate(time.Hour)
	value := MetroJourneyCheckpoint{Journey: "old", Revision: 1, SourceAt: age, Generation: "g", CommittedAt: age, Payload: json.RawMessage(`{}`)}
	payload, err := s.encodeMetroCheckpoint(value)
	if err != nil {
		t.Fatal(err)
	}
	name := "metro-shared"
	if err = os.WriteFile(filepath.Join(config.Directory, name), payload, 0600); err != nil {
		t.Fatal(err)
	}
	m := manifest{Version: 1}
	for i := 0; i < 10; i++ {
		m.Blocks = append(m.Blocks, block{Key: fmt.Sprintf("metro-popup-checkpoint:%d", i), File: name, Hash: digestBytes(payload), Kind: "popup-checkpoint", Operator: "metro", Age: age.Add(time.Duration(i) * time.Hour), Closed: true})
	}
	m.Blocks = append(m.Blocks, block{Key: "metro-popup-checkpoint:recent", File: name, Hash: digestBytes(payload), Kind: "popup-checkpoint", Operator: "metro", Age: now})
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Blocks[len(m.Blocks)-1].Key += strings.Repeat("p", maxManifestBytes-len(raw))
	if err = s.saveManifest(m); err != nil {
		t.Fatal(err)
	}
	detail := namedGeneration(block{Key: "metro:detail:" + now.Truncate(time.Hour).Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: now.Truncate(time.Hour), AsOf: now, Samples: 1}, []byte("candidate"))
	// The actual detail admission needs more than one bounded expiry pass.
	afterFirst := manifest{Version: 1, Blocks: append([]block{}, m.Blocks[1:]...)}
	check, _, _ := replacementManifest(afterFirst, detail)
	if _, err = encodeManifest(check); !errors.Is(err, errManifestLimit) {
		t.Fatalf("fixture does not require another expiry pass: %v", err)
	}
	return manifestPressureFixture{
		service: s, now: now, payload: payload, detail: detail,
		receipt:    testReceipt(now, testRow("A", "x", now, 100)),
		competitor: block{Key: "metro-popup-checkpoint:competitor", Kind: "popup-checkpoint", Operator: "metro", Age: now},
	}
}

func TestManifestPressureRetainsAndPublishesMetroBeforeCompetitor(t *testing.T) {
	f := newManifestPressureFixture(t)
	s := f.service
	if err := s.Record(f.receipt, testTopology()); !errors.Is(err, errMaintenancePending) {
		t.Fatalf("record did not wait for bounded manifest cleanup: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	assertManifestPressurePinned(t, f)
	if err := s.publish(f.competitor, f.payload, f.now); !errors.Is(err, errMaintenancePending) {
		t.Fatalf("competitor bypassed retained publication: %v", err)
	}
	finishManifestPressure(t, f)
	assertManifestPublication(t, f)
}

func assertManifestPressurePinned(t *testing.T, f manifestPressureFixture) {
	t.Helper()
	s := f.service
	if !s.pressurePending || !s.pressureManifest || !s.pressureBuffered || !s.pressurePinned || len(s.detail) != 1 || !s.detail[0].ReceivedAt.Equal(f.receipt.ReceivedAt) {
		t.Fatalf("manifest pressure: pending=%v manifest=%v buffered=%v pinned=%v detail=%d error=%v", s.pressurePending, s.pressureManifest, s.pressureBuffered, s.pressurePinned, len(s.detail), s.maintenanceError)
	}
}

func finishManifestPressure(t *testing.T, f manifestPressureFixture) {
	t.Helper()
	s := f.service
	oldCount := func() int {
		n := 0
		for _, b := range s.index.Blocks {
			if b.Kind == "popup-checkpoint" && len(b.Key) < 100 {
				n++
			}
		}
		return n
	}
	for i := 0; i < 16 && s.pressurePending; i++ {
		before := oldCount()
		s.maintenancePass(f.now)
		if before-oldCount() > maxRetirements {
			t.Fatal("buffered flush exceeded the maintenance retirement budget")
		}
	}
	if s.pressurePending || s.maintenanceError != nil {
		t.Fatalf("buffered publication did not recover: %v", s.maintenanceError)
	}
}

func assertManifestPublication(t *testing.T, f manifestPressureFixture) {
	t.Helper()
	s := f.service
	var published block
	for _, b := range s.index.Blocks {
		if b.Key == f.detail.Key {
			published = b
		}
		if b.Key == f.competitor.Key {
			t.Fatal("competitor was published before retained Metro data")
		}
	}
	if published.File == "" {
		t.Fatal("maintenance released the reservation before Metro detail was published")
	}
	encoded, err := s.readBlock(published)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := s.decoder.DecodeAll(encoded, nil)
	if err != nil || !strings.Contains(string(decoded), f.receipt.ReceivedAt.Format(time.RFC3339)) {
		t.Fatalf("published detail did not retain the sample: %v", err)
	}
}

func TestBufferedRetryHoldsReservationUntilPublication(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.maintenanceOnce.Do(func() { close(s.maintenanceStop) })
	<-s.maintenanceDone
	now := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	receipt := testReceipt(now, testRow("A", "x", now, 100))
	started, release, workerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.mu.Lock()
	s.hour, s.lastSample = now.Truncate(time.Hour), receipt.ReceivedAt
	s.detail = []Receipt{receipt}
	s.detailBytes = 100
	s.config.MinFreeBytes = 1
	s.spaceProbe = func(string) (archiveSpace, error) { return archiveSpace{}, errors.New("injected unavailable space") }
	err = s.ensureSpaceFor(4096, 2, now, &cleanupWork{})
	if err == nil {
		s.mu.Unlock()
		t.Fatal("fixture did not block the publication")
	}
	s.pause(err)
	s.spaceProbe = func(string) (archiveSpace, error) { return archiveSpace{bytes: 1 << 30, inodes: 100}, nil }
	first := true
	s.manifestSync = func(path string) error {
		if first {
			first = false
			close(started)
			<-release
		}
		return syncDirectory(path)
	}
	s.mu.Unlock()
	go func() {
		s.mu.Lock()
		s.maintenancePass(now)
		s.mu.Unlock()
		close(workerDone)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("maintenance did not retry the buffered publication")
	}
	competitorDone := make(chan error, 1)
	go func() { competitorDone <- s.Record(testReceipt(now.Add(time.Minute)), testTopology()) }()
	premature := false
	select {
	case <-competitorDone:
		premature = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-workerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("buffered retry did not finish")
	}
	if premature {
		t.Fatal("competing record entered before buffered publication completed")
	}
	select {
	case err = <-competitorDone:
		if err != nil {
			t.Fatal("competing record did not resume", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("competing record did not resume")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifestSync = nil
	if s.pressurePending || s.maintenanceError != nil {
		t.Fatal("completed publication left a pressure barrier")
	}
	for _, b := range s.index.Blocks {
		if b.Kind != "detail" {
			continue
		}
		encoded, err := s.readBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := s.decoder.DecodeAll(encoded, nil)
		if err == nil && strings.Contains(string(decoded), receipt.ReceivedAt.Format(time.RFC3339)) {
			return
		}
	}
	t.Fatal("buffered detail was not published before releasing its reservation")
}

func TestManifestPressureWithNoExpiredDataKeepsBufferedEvidence(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	name := "metro-protected"
	if err = os.WriteFile(filepath.Join(s.config.Directory, name), []byte("protected"), 0600); err != nil {
		t.Fatal(err)
	}
	m := manifest{Version: 1, Blocks: []block{{Key: "protected", File: name, Hash: digestBytes([]byte("protected")), Kind: "correction", Operator: "metro", Age: now}}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Blocks[0].Key += strings.Repeat("p", maxManifestBytes-len(raw))
	if err = s.saveManifest(m); err != nil {
		t.Fatal(err)
	}
	s.hour, s.lastSample = now.Truncate(time.Hour), now
	s.detail = []Receipt{{ReceivedAt: now}}
	b := namedGeneration(block{Key: "metro:detail:" + s.hour.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: s.hour}, []byte("candidate"))
	build := func() manifest { next, _, _ := replacementManifest(s.index, b); return next }
	err = s.admitManifest(build, 4096, now)
	if !errors.Is(err, errManifestLimit) || !s.pressureManifest {
		t.Fatalf("irreducible manifest was admitted: %v", err)
	}
	s.pause(err)
	if pending := s.maintenancePass(now); pending {
		t.Fatal("irreducible manifest pressure retried at the progress cadence")
	}
	if !s.pressurePending || !s.pressurePinned || !s.pressureBuffered || !errors.Is(s.maintenanceError, errManifestLimit) || len(s.detail) != 1 {
		t.Fatal("irreducible manifest pressure lost its buffer or status")
	}
}

func TestManifestRetryAccountsForReplacementOffset(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.maintenanceOnce.Do(func() { close(s.maintenanceStop) })
	<-s.maintenanceDone
	now := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	initial := testReceipt(now, testRow("A", "x", now, 100))
	if err = s.Record(initial, testTopology()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	age := now.AddDate(0, 0, -8).Truncate(time.Hour)
	value := MetroJourneyCheckpoint{Journey: "old", Revision: 1, SourceAt: age, Generation: "g", CommittedAt: age, Payload: json.RawMessage(`{}`)}
	payload, err := s.encodeMetroCheckpoint(value)
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	name := "metro-shared-replacement"
	if err = os.WriteFile(filepath.Join(config.Directory, name), payload, 0600); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	m := manifest{Version: 1, Blocks: append([]block{}, s.index.Blocks...)}
	old := block{Key: "metro-popup-checkpoint:old", File: name, Hash: digestBytes(payload), Kind: "popup-checkpoint", Operator: "metro", Age: age, Closed: true}
	protected := block{Key: "protected", File: name, Hash: digestBytes(payload), Kind: "correction", Operator: "metro", Age: now}
	m.Blocks = append(m.Blocks, old, protected)
	raw, err := json.Marshal(m)
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	m.Blocks[len(m.Blocks)-1].Key += strings.Repeat("p", maxManifestBytes-len(raw))
	if err = s.saveManifest(m); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	afterExpired := manifest{Version: 1}
	var replacement block
	for _, b := range m.Blocks {
		if b.Key != old.Key {
			afterExpired.Blocks = append(afterExpired.Blocks, b)
		}
		if b.Kind == "detail" {
			replacement = b
		}
	}
	replacement.Samples = 10
	replacement.Hash = strings.Repeat("0", len(replacement.Hash))
	actual, _, _ := replacementManifest(afterExpired, replacement)
	actualRaw, err := encodeManifest(actual)
	if err != nil {
		s.mu.Unlock()
		t.Fatalf("one expiry should make the replacement fit: %v", err)
	}
	baseRaw, _ := json.Marshal(afterExpired)
	entryRaw, _ := json.Marshal(replacement)
	if len(actualRaw) > maxManifestBytes || len(baseRaw)+len(entryRaw)+1 <= maxManifestBytes {
		s.mu.Unlock()
		t.Fatal("fixture does not expose the replacement-offset overestimate")
	}
	// The next detail replacement changes the sample count from one digit to
	// two. Its existing entry offsets almost the entire replacement size.
	for len(s.detail) < 9 {
		s.detail = append(s.detail, initial)
	}
	s.detailBytes = 9 * 100
	s.maintenanceWork = &cleanupWork{retirements: maxRetirements}
	s.mu.Unlock()
	next := testReceipt(now.Add(time.Minute), testRow("A", "x", now.Add(time.Minute), 100))
	err = s.Record(next, testTopology())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maintenanceWork = nil
	if !errors.Is(err, errMaintenancePending) || !s.pressureManifest || !s.pressureBuffered {
		t.Fatalf("replacement did not enter retained manifest pressure: %v", err)
	}
	s.maintenancePass(next.ReceivedAt)
	if s.pressurePending || s.maintenanceError != nil {
		t.Fatalf("replacement stayed blocked after its one expired block was retired: %v", s.maintenanceError)
	}
	oldPresent, detailPublished := false, false
	for _, b := range s.index.Blocks {
		oldPresent = oldPresent || b.Key == old.Key
		detailPublished = detailPublished || b.Kind == "detail" && b.Samples == 10
	}
	if oldPresent || !detailPublished {
		t.Fatalf("old entry retained=%v, ten-sample replacement published=%v", oldPresent, detailPublished)
	}
}

func TestReopenSyncsSelectedManifestBeforeUnlinkingOrphans(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	oldName, newName := "metro-old", "metro-new"
	for _, name := range []string{oldName, newName} {
		if err = os.WriteFile(filepath.Join(config.Directory, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := block{Key: "old", File: oldName, Hash: digestBytes([]byte(oldName)), Kind: "correction", Operator: "metro"}
	current := block{Key: "new", File: newName, Hash: digestBytes([]byte(newName)), Kind: "correction", Operator: "metro"}
	if err = s.saveManifest(manifest{Version: 1, Blocks: []block{old}}); err != nil {
		t.Fatal(err)
	}
	s.manifestSync = func(string) error { return errors.New("injected post-rename failure") }
	if err = s.saveManifest(manifest{Version: 1, Blocks: []block{current}}); err == nil || !s.manifestUncertain {
		t.Fatal("manifest outcome did not become uncertain", err)
	}
	if err = s.Close(); err == nil {
		t.Fatal("uncertain close unexpectedly succeeded")
	}
	openWithSync := func(syncFn func(string) error) (*Service, error) {
		lock, err := acquireArchiveLock(config.Directory)
		if err != nil {
			return nil, err
		}
		owned := &Service{providers: map[string]*providerState{}, operators: map[string]bool{"metro": true}, config: config, index: manifest{Version: 1, Blocks: []block{}}, metroArchiveState: metroArchiveState{engine: newEngine(), status: "collecting"}, archiveResources: archiveResources{lock: lock, reconcileSync: syncFn}}
		return openOwnedArchive(owned)
	}
	orphan := filepath.Join(config.Directory, oldName)
	_, err = openWithSync(func(string) error {
		if _, statErr := os.Stat(orphan); statErr != nil {
			t.Fatal("orphan unlinked before selected manifest sync", statErr)
		}
		return errors.New("injected pre-reconcile sync failure")
	})
	if err == nil {
		t.Fatal("reopen ignored failed selected manifest sync")
	}
	if _, err = os.Stat(orphan); err != nil {
		t.Fatal("failed reopen removed orphan", err)
	}
	calls := 0
	reopened, err := openWithSync(func(path string) error {
		calls++
		_, statErr := os.Stat(orphan)
		if calls == 1 && statErr != nil || calls == 2 && !os.IsNotExist(statErr) {
			t.Fatalf("unexpected orphan state at sync %d: %v", calls, statErr)
		}
		return syncDirectory(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected sync before and after orphan cleanup, got %d", calls)
	}
	reopened.reconcileSync = nil
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
}
