package patterns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

type tailFixture struct {
	directory string
	request   MetroTailRecoveryRequest
	state     block
	detail    block
	day       block
	other     block
}

func makeTailFixture(t *testing.T, mutate func(*checkpoint, *[]Receipt, *[]Aggregate)) tailFixture {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour + 10*time.Minute)
	tailAt := at.Add(30 * time.Second)
	day := at.In(lisbon).Format("2006-01-02")
	lower, upper := tailAt.Add(-3*time.Second), tailAt
	first := durabilityReceipt(at, "first")
	first.ReceivedAt = at
	match := Forecast{ID: "matched", Episode: "old-episode", IssuedAt: at.Add(-time.Minute), Route: "metro:route", Direction: "A", Stop: "A", Function: "waiting", Result: "pending"}
	remain := Forecast{ID: "remaining", Episode: "old-episode", IssuedAt: at.Add(-time.Minute), Route: "metro:route", Direction: "A", Stop: "A", Function: "waiting", Result: "pending"}
	outcome := match
	outcome.Evaluated, outcome.Result = true, "evaluated_proxy"
	outcome.ReferenceLower, outcome.ReferenceUpper = &lower, &upper
	tail := durabilityReceipt(tailAt, "tail")
	tail.ReceivedAt = tailAt
	tail.Outcomes = []Forecast{outcome}
	eng := newEngine()
	eng.LastReceipt, eng.Cases = at, []Forecast{match, remain}
	eng.Groups["old"] = &group{ID: "old-episode", Pairs: map[string]bool{}}
	aggregates := []Aggregate{{Operator: "metro", Date: day, Route: "metro:route", Kind: "receipts", Count: 5, KnownAt: at.UnixNano(), InputFrom: at.UnixNano()}}
	eng.Aggregates[aggregateKey(aggregates[0])] = aggregates[0]
	eng.DirtyDays[day] = true
	cp := checkpoint{Providers: map[string]*providerState{"cm": newProvider("cm")}, Engine: eng, Hour: at.Truncate(time.Hour), Detail: []Receipt{first}, SampleSeconds: 30, BinSeconds: 30}
	rows := []Receipt{first, tail}
	if mutate != nil {
		mutate(&cp, &rows, &aggregates)
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(9)))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	stateRaw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	stateBlob := encoder.EncodeAll(stateRaw, nil)
	state := namedGeneration(block{Key: "metro:state", Kind: "state", Operator: "metro", Age: at}, stateBlob)
	var detailRaw bytes.Buffer
	for _, row := range rows {
		if err = json.NewEncoder(&detailRaw).Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	detailBlob := encoder.EncodeAll(detailRaw.Bytes(), nil)
	detail := namedGeneration(block{Key: "metro:detail:" + cp.Hour.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: cp.Hour, AsOf: tailAt, Closed: true, Samples: int64(len(rows))}, detailBlob)
	dayBlob, err := verifiedAggregateBlob(aggregates)
	if err != nil {
		t.Fatal(err)
	}
	dayAt, err := time.ParseInLocation("2006-01-02", day, lisbon)
	if err != nil {
		t.Fatal(err)
	}
	dayBlock := namedGeneration(block{Key: "metro:aggregate:" + day, Kind: "aggregate", Operator: "metro", Date: day, Age: dayAt.UTC()}, dayBlob)
	otherBlob := encoder.EncodeAll([]byte(`[{"id":"preserved"}]`), nil)
	other := namedGeneration(block{Key: "metro-popup-events:" + cp.Hour.Format(time.RFC3339), Kind: "popup-events", Operator: "metro", Age: cp.Hour}, otherBlob)
	for b, blob := range map[block][]byte{state: stateBlob, detail: detailBlob, dayBlock: dayBlob, other: otherBlob} {
		if err = os.WriteFile(filepath.Join(directory, b.File), blob, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m := manifest{Version: 1, Blocks: []block{state, detail, dayBlock, other}}
	manifestRaw, err := encodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "manifest.json"), manifestRaw, 0600); err != nil {
		t.Fatal(err)
	}
	r := MetroTailRecoveryRequest{ManifestSHA256: digestBytes(manifestRaw), CheckpointSHA256: state.Hash,
		DetailSHA256: detail.Hash, AggregateSHA256: dayBlock.Hash, DetailHour: cp.Hour, CheckpointAt: at,
		TailAt: tailAt, AffectedDay: day, CheckpointRows: 1, TailOutcomes: 1, SampleSeconds: 30, BinSeconds: 30,
		ArchiveLimitBytes: 1 << 30, MinFreeBytes: 1, MinFreeInodes: 1}
	return tailFixture{directory: directory, request: r, state: state, detail: detail, day: dayBlock, other: other}
}

func inventory(t *testing.T, directory string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = digestBytes(raw)
	}
	return out
}

func TestMetroTailRecoveryPreservesEvidenceAndRestarts(t *testing.T) {
	f := makeTailFixture(t, nil)
	before := inventory(t, f.directory)
	result, err := RecoverMetroTail(f.directory, f.request)
	if err != nil || result.Status != "repaired" || result.NewStateSHA256 == f.state.Hash || result.NewManifestSHA256 == f.request.ManifestSHA256 {
		t.Fatalf("repair: %+v %v", result, err)
	}
	after := inventory(t, f.directory)
	for _, name := range []string{f.state.File, f.detail.File, f.day.File, f.other.File} {
		if after[name] != before[name] {
			t.Fatalf("source changed: %s", name)
		}
	}
	repeat, err := RecoverMetroTail(f.directory, f.request)
	if err != nil || repeat.Status != "already_applied" || repeat.NewStateSHA256 != result.NewStateSHA256 || repeat.NewManifestSHA256 != result.NewManifestSHA256 {
		t.Fatalf("repeat: %+v %v", repeat, err)
	}
}

func TestMetroTailRecoveryOldCheckpointDecoderRejectsMarker(t *testing.T) {
	f := makeTailFixture(t, nil)
	if _, err := RecoverMetroTail(f.directory, f.request); err != nil {
		t.Fatal(err)
	}
	// Older checkpoint decoders reject the additional field instead of silently
	// treating raw-only evidence as fully incorporated into the aggregates.
	m := readFixtureManifest(t, f.directory)
	var state block
	for _, b := range m.Blocks {
		if b.Key == "metro:state" {
			state = b
		}
	}
	compressed, err := os.ReadFile(filepath.Join(f.directory, state.File))
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	raw, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Providers                 map[string]*providerState
		Engine                    *engine
		Hour                      time.Time
		Detail                    []Receipt
		BinSeconds, SampleSeconds int
		Topology                  Topology
	}
	if err = knownJSON(raw, &old); err == nil {
		t.Fatal("old checkpoint decoder accepted the recovery boundary")
	}
}

func TestMetroTailRecoveryRestartsRetainGapAndEvidence(t *testing.T) {
	f := makeTailFixture(t, nil)
	if _, err := RecoverMetroTail(f.directory, f.request); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(f.directory)
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if s.detailFence != nil || s.tailRecovery == nil || s.engine.Gaps < 1 || !s.pendingMetroDeliveryGap || !s.MetroCut().Snapshot().Pending {
		t.Fatalf("missing recovery cut: fence=%v", s.detailFence)
	}
	if !strings.Contains(s.metroViewMetadata().Message, "Histórico parcial") || len(s.engine.Cases) != 1 || s.engine.Cases[0].ID != "remaining" {
		t.Fatal("partial history or matching case retirement missing")
	}
	resultSample, err := s.RecordMetro(durabilityReceipt(f.request.TailAt.Add(31*time.Second), "after"), testTopology())
	if err != nil || resultSample.Kind != MetroGapSampled || !resultSample.Appended || !resultSample.Acknowledged {
		t.Fatalf("post-recovery gap: %+v %v", resultSample, err)
	}
	for _, out := range s.detail[len(s.detail)-1].Outcomes {
		if out.ID == "matched" {
			t.Fatal("raw-only evaluated outcome was emitted a second time")
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	verifyRecoveryAfterRestarts(t, f, config)
}

func verifyRecoveryAfterRestarts(t *testing.T, f tailFixture, config Config) {
	t.Helper()
	for i := 0; i < 2; i++ {
		s, err := Open(config)
		if err != nil {
			t.Fatal(err)
		}
		if s.detailFence != nil || s.tailRecovery == nil || s.engine.Gaps < 1 || !strings.Contains(s.metroViewMetadata().Message, "Histórico parcial") {
			t.Fatalf("restart %d lost recovery boundary: %v", i, s.detailFence)
		}
		original, err := os.ReadFile(filepath.Join(f.directory, f.state.File))
		if err != nil || digestBytes(original) != f.state.Hash {
			t.Fatalf("restart %d lost the original checkpoint", i)
		}
		rows, _ := detailForHour(t, s, f.request.DetailHour)
		if len(rows) != 3 || len(rows[1].Outcomes) != 1 || rows[1].Outcomes[0].ID != "matched" || !rows[2].DeliveryGap {
			t.Fatal("raw outcome or durable gap lost")
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMetroTailRecoveryRepeatRejectsChangedCheckpoint(t *testing.T) {
	f := makeTailFixture(t, nil)
	if _, err := RecoverMetroTail(f.directory, f.request); err != nil {
		t.Fatal(err)
	}
	m := readFixtureManifest(t, f.directory)
	var state block
	for _, b := range m.Blocks {
		if b.Key == "metro:state" {
			state = b
		}
	}
	blob, err := os.ReadFile(filepath.Join(f.directory, state.File))
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decoder.DecodeAll(blob, nil)
	decoder.Close()
	if err != nil {
		t.Fatal(err)
	}
	var cp checkpoint
	if err = knownJSON(raw, &cp); err != nil {
		t.Fatal(err)
	}
	cp.Engine.Cases = nil // A different writer silently dropped a pending case.
	raw, err = json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	blob = encoder.EncodeAll(raw, nil)
	encoder.Close()
	changed := namedGeneration(block{Key: state.Key, Kind: state.Kind, Operator: state.Operator, Age: state.Age}, blob)
	if err = os.WriteFile(filepath.Join(f.directory, changed.File), blob, 0600); err != nil {
		t.Fatal(err)
	}
	m, _, _ = replacementManifest(m, changed)
	raw, err = encodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.directory, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	before := inventory(t, f.directory)
	if _, err = RecoverMetroTail(f.directory, f.request); err == nil {
		t.Fatal("repeat accepted a changed recovered checkpoint")
	}
	if !reflect.DeepEqual(before, inventory(t, f.directory)) {
		t.Fatal("repeat changed archive")
	}
}

func TestMetroTailRecoveryRefusesMismatchesWithoutWriting(t *testing.T) {
	cases := map[string]func(*tailFixture){
		"manifest hash":       func(f *tailFixture) { f.request.ManifestSHA256 = strings.Repeat("0", 64) },
		"checkpoint hash":     func(f *tailFixture) { f.request.CheckpointSHA256 = strings.Repeat("0", 64) },
		"detail hash":         func(f *tailFixture) { f.request.DetailSHA256 = strings.Repeat("0", 64) },
		"aggregate hash":      func(f *tailFixture) { f.request.AggregateSHA256 = strings.Repeat("0", 64) },
		"wrong count":         func(f *tailFixture) { f.request.CheckpointRows++ },
		"wrong outcome count": func(f *tailFixture) { f.request.TailOutcomes++ },
		"wrong sample":        func(f *tailFixture) { f.request.SampleSeconds++ },
		"different affected day": func(f *tailFixture) {
			f.request.AffectedDay = f.request.TailAt.In(lisbon).AddDate(0, 0, -1).Format("2006-01-02")
		},
		"wrong tail clock":   func(f *tailFixture) { f.request.TailAt = f.request.TailAt.Add(time.Second) },
		"missing budget":     func(f *tailFixture) { f.request.MinFreeBytes = 0 },
		"exhausted budget":   func(f *tailFixture) { f.request.ArchiveLimitBytes = 1 },
		"impossible reserve": func(f *tailFixture) { f.request.MinFreeBytes = 1 << 62 },
	}
	keys := make([]string, 0, len(cases))
	for k := range cases {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, name := range keys {
		t.Run(name, func(t *testing.T) {
			f := makeTailFixture(t, nil)
			cases[name](&f)
			before := inventory(t, f.directory)
			if _, err := RecoverMetroTail(f.directory, f.request); err == nil {
				t.Fatal("unexpected success")
			}
			if after := inventory(t, f.directory); !reflect.DeepEqual(before, after) {
				t.Fatal("failed preflight modified the archive")
			}
		})
	}
}

func TestMetroTailRecoveryRejectsSemanticMismatch(t *testing.T) {
	cases := map[string]func(*checkpoint, *[]Receipt, *[]Aggregate){
		"prefix":             func(_ *checkpoint, r *[]Receipt, _ *[]Aggregate) { (*r)[0].Raw = json.RawMessage(`{"changed":true}`) },
		"forecast issuance":  func(_ *checkpoint, r *[]Receipt, _ *[]Aggregate) { (*r)[1].Forecasts = []Forecast{{ID: "new"}} },
		"outcome mismatch":   func(_ *checkpoint, r *[]Receipt, _ *[]Aggregate) { (*r)[1].Outcomes[0].Stop = "other" },
		"wrong outcome type": func(_ *checkpoint, r *[]Receipt, _ *[]Aggregate) { (*r)[1].Outcomes[0].Result = "lost_support" },
		"duplicate pending": func(c *checkpoint, _ *[]Receipt, _ *[]Aggregate) {
			c.Engine.Cases = append(c.Engine.Cases, c.Engine.Cases[0])
		},
		"different day": func(_ *checkpoint, r *[]Receipt, _ *[]Aggregate) {
			(*r)[1].Outcomes[0].IssuedAt = (*r)[1].Outcomes[0].IssuedAt.AddDate(0, 0, -1)
		},
		"other case day": func(c *checkpoint, _ *[]Receipt, _ *[]Aggregate) {
			c.Engine.Cases[1].IssuedAt = c.Engine.Cases[1].IssuedAt.AddDate(0, 0, -1)
		},
		"other dirty day": func(c *checkpoint, _ *[]Receipt, _ *[]Aggregate) {
			c.Engine.DirtyDays["2026-01-01"] = true
		},
		"other association day": func(c *checkpoint, _ *[]Receipt, _ *[]Aggregate) {
			earlier := c.Engine.LastReceipt.AddDate(0, 0, -1)
			c.Engine.Groups["old"].Signals = []signal{{L: earlier, U: earlier}}
		},
		"other previous day": func(c *checkpoint, _ *[]Receipt, _ *[]Aggregate) {
			c.Engine.Previous["old"] = priorRow{Clock: c.Engine.LastReceipt.AddDate(0, 0, -1)}
		},
		"aggregate changed": func(_ *checkpoint, _ *[]Receipt, a *[]Aggregate) { (*a)[0].Count++ },
		"source gap":        func(_ *checkpoint, r *[]Receipt, _ *[]Aggregate) { (*r)[1].DeliveryGap = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := makeTailFixture(t, mutate)
			before := inventory(t, f.directory)
			if _, err := RecoverMetroTail(f.directory, f.request); err == nil {
				t.Fatal("unexpected success")
			}
			if !reflect.DeepEqual(before, inventory(t, f.directory)) {
				t.Fatal("failed preflight modified archive")
			}
		})
	}
}

func TestMetroTailRecoveryCommitFailuresAreRepeatable(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprintf("directory_sync_%d", failAt), func(t *testing.T) {
			f := makeTailFixture(t, nil)
			owner, err := openMetroRecoveryOwner(f.directory, f.request)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			owner.syncDir = func(path string) error {
				calls++
				if calls == failAt {
					return errors.New("injected sync failure")
				}
				return syncDirectory(path)
			}
			_, err = owner.recover()
			owner.close()
			if err == nil {
				t.Fatal("expected injected failure")
			}
			if failAt == 2 && !strings.Contains(err.Error(), "uncertain") {
				t.Fatalf("missing uncertain outcome: %v", err)
			}
			retry, err := RecoverMetroTail(f.directory, f.request)
			if err != nil {
				t.Fatal(err)
			}
			want := "repaired"
			if failAt == 2 {
				want = "already_applied"
			}
			if retry.Status != want {
				t.Fatalf("got %s want %s", retry.Status, want)
			}
		})
	}
}
