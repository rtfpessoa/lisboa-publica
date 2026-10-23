package patterns

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type pressureFixture struct {
	service                  *Service
	now                      time.Time
	aggregate, input, recent block
	used                     int64
}

// The aggregate is older but remains inside its normal retention period.
// The input is normally expired, and the recent detail has a deliberately
// shorter configured normal retention than the pressure floor.
func makePressureFixture(t *testing.T) pressureFixture {
	t.Helper()
	config := DefaultConfig(t.TempDir())
	config.DetailDays = 2
	config.AggregateMonths = 12
	config.MaintenanceInterval = time.Hour
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	s.maintenanceOnce.Do(func() { close(s.maintenanceStop) })
	<-s.maintenanceDone
	now := time.Now().UTC().Truncate(time.Hour)
	s.mu.Lock()
	defer s.mu.Unlock()
	date := now.In(lisbon).AddDate(0, -10, 0).Format("2006-01-02")
	age, err := time.ParseInLocation("2006-01-02", date, lisbon)
	if err != nil {
		t.Fatal(err)
	}
	row := Aggregate{Operator: "metro", Date: date, KnownAt: age.Add(time.Hour).UnixNano(), InputFrom: age.UnixNano()}
	data, err := verifiedAggregateBlob([]Aggregate{row})
	if err != nil {
		t.Fatal(err)
	}
	aggregate := namedGeneration(block{Key: "metro:aggregate:" + date, Kind: "aggregate", Operator: "metro", Date: date, Age: age.UTC(), Closed: true}, data)
	if err = os.WriteFile(filepath.Join(config.Directory, aggregate.File), data, 0600); err != nil {
		t.Fatal(err)
	}
	inputAge := now.AddDate(0, 0, -8).Truncate(5 * time.Minute)
	raw, err := json.Marshal(MetroEventRecord{ID: "old", At: inputAge.Add(time.Second), Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	data = s.encoder.EncodeAll(append(raw, '\n'), nil)
	input := namedGeneration(block{Key: "metro-inputs:" + inputAge.Format(time.RFC3339), Kind: "metro-inputs", Operator: "metro", Age: inputAge, Closed: true}, data)
	if err = os.WriteFile(filepath.Join(config.Directory, input.File), data, 0600); err != nil {
		t.Fatal(err)
	}
	recentAge := now.AddDate(0, 0, -3)
	raw, err = json.Marshal(Receipt{ReceivedAt: recentAge.Add(time.Minute), Operator: "metro"})
	if err != nil {
		t.Fatal(err)
	}
	data = s.encoder.EncodeAll(append(raw, '\n'), nil)
	recent := namedGeneration(block{Key: "metro:detail:" + recentAge.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: recentAge, Closed: true}, data)
	if err = os.WriteFile(filepath.Join(config.Directory, recent.File), data, 0600); err != nil {
		t.Fatal(err)
	}
	// A referenced protected generation makes the lowered startup limit valid.
	filler := make([]byte, 2<<20)
	_, _ = rand.New(rand.NewSource(91)).Read(filler)
	protected := namedGeneration(block{Key: "protected", Kind: "correction", Operator: "metro", Age: now}, filler)
	if err = os.WriteFile(filepath.Join(config.Directory, protected.File), filler, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.saveManifest(manifest{Version: 1, Blocks: []block{input, recent, protected, aggregate}}); err != nil {
		t.Fatal(err)
	}
	used, err := s.allocated()
	if err != nil || used < 2<<20 {
		t.Fatalf("fixture allocation %d: %v", used, err)
	}
	return pressureFixture{s, now, aggregate, input, recent, used}
}

func assertPressureOrder(t *testing.T, f pressureFixture) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(f.service.config.Directory, f.aggregate.File)); !os.IsNotExist(err) {
		t.Fatalf("older pressure-eligible aggregate was not retired: %v", err)
	}
	for _, b := range []block{f.input, f.recent} {
		if _, err := os.Stat(filepath.Join(f.service.config.Directory, b.File)); err != nil {
			t.Fatalf("newer %s was retired first: %v", b.Kind, err)
		}
	}
}

func TestArchivePressureUsesGlobalAgeOrder(t *testing.T) {
	for _, mode := range []string{"space check", "admission", "startup"} {
		t.Run(mode, func(t *testing.T) {
			f := makePressureFixture(t)
			s := f.service
			defer s.Close()
			if mode == "startup" {
				config := s.config
				config.LimitBytes = f.used - 1
				s.mu.Lock()
				s.releaseArchiveResources()
				s.lock = nil
				s.mu.Unlock()
				reopened, err := Open(config)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				f.service = reopened
				reopened.mu.Lock()
				defer reopened.mu.Unlock()
				assertPressureOrder(t, f)
				return
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.config.LimitBytes = f.used + 4096
			if mode == "space check" {
				if err := s.ensureSpaceFor(8192, 2, f.now, &cleanupWork{}); err != nil {
					t.Fatal(err)
				}
			} else {
				value := MetroJourneyCheckpoint{Journey: "new", Revision: 1, SourceAt: f.now, Generation: "g", CommittedAt: f.now, Payload: json.RawMessage(`{}`)}
				data, err := s.encodeMetroCheckpoint(value)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.publish(block{Key: "metro-popup-checkpoint:new", Kind: "popup-checkpoint", Operator: "metro", Age: f.now}, data, f.now); err != nil {
					t.Fatal(err)
				}
			}
			assertPressureOrder(t, f)
		})
	}
}

func TestArchiveNormalExpiryKeepsItsOwnRetentionOrder(t *testing.T) {
	f := makePressureFixture(t)
	s := f.service
	defer s.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.expireSome(f.now, &cleanupWork{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.config.Directory, f.input.File)); !os.IsNotExist(err) {
		t.Fatalf("normally expired input was not retired: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.config.Directory, f.aggregate.File)); err != nil {
		t.Fatalf("unexpired aggregate was retired without pressure: %v", err)
	}
}
