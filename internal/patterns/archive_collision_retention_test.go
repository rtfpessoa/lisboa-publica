package patterns

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func collisionAggregate(t *testing.T, directory, key string, day time.Time, n int) block {
	t.Helper()
	local := day.In(lisbon)
	date := local.Format("2006-01-02")
	start, err := time.ParseInLocation("2006-01-02", date, lisbon)
	if err != nil {
		t.Fatal(err)
	}
	row := Aggregate{Operator: "metro", Date: date, Stop: fmt.Sprint(n), KnownAt: start.Add(time.Hour).UnixNano(), InputFrom: start.UnixNano()}
	blob, err := verifiedAggregateBlob([]Aggregate{row})
	if err != nil {
		t.Fatal(err)
	}
	b := namedGeneration(block{Key: key, Kind: "aggregate", Operator: "metro", Date: date, Age: start.UTC(), Closed: true}, blob)
	if err = os.WriteFile(filepath.Join(directory, b.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	return b
}

func writeCollisionFixture(t *testing.T, config Config, base time.Time, key string, invalidFirst bool) []block {
	t.Helper()
	rows := []Receipt{durabilityReceipt(base, "retained")}
	writeDurabilityFixture(t, config, rows, fixtureCheckpoint(t, base.Truncate(time.Hour), rows))
	m := readFixtureManifest(t, config.Directory)
	conflicts := []block{collisionAggregate(t, config.Directory, key, base, 1), collisionAggregate(t, config.Directory, key, base, 2)}
	if invalidFirst {
		m.Blocks = append(conflicts, m.Blocks...)
	} else {
		m.Blocks = append(m.Blocks, conflicts...)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(config.Directory, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return conflicts
}

func TestWrongKindCollisionsSurviveExpiryAndPressure(t *testing.T) {
	for _, identity := range []string{"detail", "state"} {
		for _, invalidFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_first_%t", identity, invalidFirst), func(t *testing.T) {
				config := DefaultConfig(t.TempDir())
				config.DetailDays = 1
				config.AggregateMonths = 1
				base := time.Now().UTC().AddDate(0, -10, 0).Truncate(time.Hour).Add(5 * time.Minute)
				key := "metro:detail:" + base.Truncate(time.Hour).Format(time.RFC3339)
				if identity == "state" {
					key = "metro:state"
				}
				conflicts := writeCollisionFixture(t, config, base, key, invalidFirst)
				s, err := Open(config)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				if s.detailFence == nil {
					t.Fatal("collision did not fence")
				}
				s.mu.Lock()
				now := time.Now().UTC()
				for _, b := range conflicts {
					if !s.protectedGenerations[retentionKey(b)] || !s.blockExpired(b, now) || !s.pressureEligible(b, now) {
						s.mu.Unlock()
						t.Fatalf("collision %s was not protected and otherwise eligible", b.File)
					}
				}
				_, _ = s.expireSome(now, &cleanupWork{})
				_ = s.ensureSpaceFor(s.config.LimitBytes, 0, now, &cleanupWork{})
				s.mu.Unlock()
				after := readFixtureManifest(t, config.Directory)
				for _, b := range conflicts {
					if !fixtureHasBlock(after, b) {
						t.Fatalf("manifest removed %s", b.File)
					}
					if _, err := os.Stat(filepath.Join(config.Directory, b.File)); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestUnreadableCheckpointProtectsWrongKindDetailKeys(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	config.DetailDays, config.AggregateMonths = 1, 1
	base := time.Now().UTC().AddDate(0, -10, 0).Truncate(time.Hour).Add(5 * time.Minute)
	key := "metro:detail:" + base.Truncate(time.Hour).Format(time.RFC3339)
	conflicts := writeCollisionFixture(t, config, base, key, false)
	m := readFixtureManifest(t, config.Directory)
	for _, b := range m.Blocks {
		if b.Kind == "state" {
			if err := os.WriteFile(filepath.Join(config.Directory, b.File), []byte("corrupt checkpoint"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence == nil {
		t.Fatal("unreadable checkpoint did not fence")
	}
	s.mu.Lock()
	now := time.Now().UTC()
	for _, b := range conflicts {
		if !s.protectedGenerations[retentionKey(b)] || !s.blockExpired(b, now) || !s.pressureEligible(b, now) {
			s.mu.Unlock()
			t.Fatal("checkpoint uncertainty did not protect eligible conflicting generation")
		}
	}
	_, _ = s.expireSome(now, &cleanupWork{})
	_ = s.ensureSpaceFor(s.config.LimitBytes, 0, now, &cleanupWork{})
	s.mu.Unlock()
	after := readFixtureManifest(t, config.Directory)
	for _, b := range conflicts {
		if !fixtureHasBlock(after, b) {
			t.Fatal("checkpoint uncertainty allowed retirement")
		}
		if _, err := os.Stat(filepath.Join(config.Directory, b.File)); err != nil {
			t.Fatal(err)
		}
	}
}
