package patterns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMetroSegmentWriterRollsOverLegacyAndRestarts(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(-time.Minute)
	for i, name := range []string{"legacy", "segment-one", "segment-two"} {
		at := start.Add(time.Duration(i) * time.Minute)
		if err := s.Record(durabilityReceipt(at, name), testTopology()); err != nil {
			t.Fatal(err)
		}
	}
	newHour := start.Truncate(time.Hour).Add(time.Hour)
	if !s.segmentStart.Equal(newHour) {
		t.Fatal("writer did not switch at the hour boundary")
	}
	if err := s.Record(durabilityReceipt(newHour.Add(5*time.Minute), "next-segment"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil || !s.segmentFormat || !s.checkpointSegmentStart.Equal(newHour.Add(5*time.Minute)) {
		t.Fatal("segmented checkpoint did not restore", s.detailFence)
	}
	rows, _ := detailForHour(t, s, start.Truncate(time.Hour))
	assertReceiptIdentities(t, rows, "legacy")
	want := map[time.Time][]string{newHour: {"segment-one", "segment-two"}, newHour.Add(5 * time.Minute): {"next-segment"}}
	for _, b := range s.index.Blocks {
		if b.Kind != metroDetailSegmentKind {
			continue
		}
		rows, err := s.readDetailBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		assertReceiptIdentities(t, rows, want[b.Age]...)
		delete(want, b.Age)
	}
	if len(want) != 0 {
		t.Fatal("missing retained segment", want)
	}
}

func TestMetroSegmentWriterRetainsBusyHourOverLegacyLimit(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour)
	if err := s.Record(durabilityReceipt(start.Add(-time.Minute), "legacy"), testTopology()); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(strings.Repeat("x", 180<<10))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		at := start.Add(time.Duration(i) * 30 * time.Second)
		receipt := durabilityReceipt(at, "train")
		receipt.Raw = raw
		if err := s.Record(receipt, testTopology()); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil {
		t.Fatal(s.detailFence)
	}
	segments, total, bytes := 0, 0, 0
	for _, b := range s.index.Blocks {
		if b.Kind != metroDetailSegmentKind || !b.Age.Truncate(time.Hour).Equal(start) {
			continue
		}
		rows, err := s.readDetailBlock(b)
		if err != nil {
			t.Fatal(err)
		}
		segments++
		for _, row := range rows {
			total++
			bytes += len(row.Raw)
		}
	}
	if segments != 12 || total != 120 || bytes <= maxDetailHourBytes {
		t.Fatalf("segments=%d receipts=%d raw bytes=%d", segments, total, bytes)
	}
	for _, b := range s.index.Blocks {
		if b.Kind == metroSegmentFormatKind {
			if _, err := os.Stat(filepath.Join(config.Directory, b.File)); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing format marker")
}

func TestMetroSegmentRetentionBudgetSurvivesRestartAndResumes(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour)
	if err := s.Record(durabilityReceipt(start.Add(-time.Minute), "legacy"), testTopology()); err != nil {
		t.Fatal(err)
	}
	first := durabilityReceipt(start, "first")
	first.Raw = json.RawMessage("[" + strings.Repeat("0,", 600000) + "0]")
	if err := s.Record(first, testTopology()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil || s.segmentClockWork < 600000 {
		t.Fatal("retention budget not restored", s.detailFence, s.segmentClockWork)
	}
	rejected := durabilityReceipt(start.Add(30*time.Second), "rejected")
	rejected.Raw = json.RawMessage("[" + strings.Repeat("0,", 500000) + "0]")
	if err := s.Record(rejected, testTopology()); err == nil {
		t.Fatal("accepted a segment exceeding retention scan budget")
	}
	if len(s.detail) != 1 || !s.engine.LastReceipt.Equal(first.ReceivedAt) {
		t.Fatalf("failed candidate changed retained history: detail=%d last=%s want=%s", len(s.detail), s.engine.LastReceipt, first.ReceivedAt)
	}
	if err := s.Record(durabilityReceipt(start.Add(5*time.Minute), "resumed"), testTopology()); err != nil {
		t.Fatal("next segment did not resume", err)
	}
	if len(s.detail) != 1 || !s.detail[0].DeliveryGap {
		t.Fatal("resumption did not disclose the gap")
	}
}

func TestMetroSegmentEncodingFailurePreservesUncheckpointedDetail(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	rows := []Receipt{{ReceivedAt: start, Operator: "metro"}, {ReceivedAt: start.Add(time.Second), Operator: "metro"}}
	installDetailFixture(t, s, start, rows)
	s.mu.Lock()
	s.segmentStart = start
	s.engine.Cases = []Forecast{{Episode: "lost", IssuedAt: start, Components: []Component{{Seconds: math.NaN()}}}}
	s.mu.Unlock()
	if err := s.Record(Receipt{ReceivedAt: start.Add(time.Minute), DeliveryGap: true, Rows: []Row{}}, testTopology()); err == nil || s.detailFence == nil {
		t.Fatal("unencodable outcome did not fence collection")
	}
	read := func(s *Service) []Receipt {
		t.Helper()
		for _, b := range s.index.Blocks {
			if b.Kind == metroDetailSegmentKind && b.Age.Equal(start) {
				result, err := s.readDetailBlock(b)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
		}
		t.Fatal("uncheckpointed segment was not preserved")
		return nil
	}
	if !reflect.DeepEqual(read(s), rows) {
		t.Fatal("preserved segment changed admitted receipts")
	}
	if err = s.Close(); err == nil {
		t.Fatal("fenced archive closed without error")
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence == nil || !reflect.DeepEqual(read(s), rows) {
		t.Fatal("restart accepted or lost the unproven preserved segment")
	}
}

func TestProviderRevisionPublishesBufferedMetroSegment(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	original := seedProviderArchiveSequence(t, s, syntheticProviderPath("cm"), base)
	if err = s.Record(durabilityReceipt(base.Add(-time.Minute), "legacy"), testTopology()); err != nil {
		t.Fatal(err)
	}
	start := base.Truncate(time.Hour).Add(time.Hour)
	if err = s.Record(durabilityReceipt(start, "buffered"), testTopology()); err != nil {
		t.Fatal(err)
	}
	change := ProviderCorrection{ReceivedAt: original.ReceivedAt, ExpectedHash: digest(original.Rows[0]), Row: original.Rows[0], Evidence: "Synthetic position correction."}
	change.Row.PositionKind = "estimated"
	if _, err = s.ReprocessProvider(context.Background(), "cm", []ProviderCorrection{change}, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil || !s.segmentFormat || !s.segmentStart.Equal(start) {
		t.Fatalf("revision lost shared Metro segment: %v", s.detailFence)
	}
	assertReceiptIdentities(t, s.detail, "buffered")
	ledger, err := s.providerCorrections("cm")
	if err != nil || len(ledger) != 1 {
		t.Fatal("provider revision did not survive restart", err)
	}
}

func TestMetroSegmentRolloverUncertainManifestKeepsAdmittedDetail(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Hour).Add(5 * time.Minute)
	rows := sizedDetail(t, start, maxDetailHourBytes-(64<<10))
	installDetailFixture(t, s, start, rows)
	s.segmentStart = start
	if s.segmentClockWork, err = segmentRetentionWork(rows); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		s.engine.Cases = append(s.engine.Cases, Forecast{Route: strings.Repeat("x", 2000), IssuedAt: start, Episode: fmt.Sprint(i)})
	}
	r := Receipt{ReceivedAt: start.Add(time.Minute), DeliveryGap: true, Rows: []Row{}}
	if err = s.Record(r, testTopology()); err == nil || s.detailLimitError == nil {
		t.Fatal("expected bounded segment refusal")
	}
	s.manifestSync = func(string) error { return errors.New("injected manifest sync failure") }
	r.ReceivedAt = start.Add(5 * time.Minute)
	if err = s.Record(r, testTopology()); err == nil || !s.manifestUncertain {
		t.Fatal("rollover did not fence uncertain publication")
	}
	if !s.segmentStart.Equal(start) || len(s.detail) != len(rows) {
		t.Fatal("failed rollover changed admitted detail")
	}
	s.manifestSync = nil
	r.ReceivedAt = r.ReceivedAt.Add(time.Minute)
	if err = s.Record(r, testTopology()); err == nil {
		t.Fatal("uncertain rollover admitted later data")
	}
	if err = s.Close(); err == nil {
		t.Fatal("uncertain publication closed successfully")
	}
}

func TestMetroSegmentUpgradeFromSaturatedLegacyHour(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(10 * time.Minute)
	rows := sizedDetail(t, base, maxDetailHourBytes)
	installDetailFixture(t, s, base, rows)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if s.detailFence != nil || len(s.detail) != len(rows) || !s.segmentStart.IsZero() {
		t.Fatal("saturated legacy hour did not restore", s.detailFence)
	}
	rejected := Receipt{ReceivedAt: base.Add(time.Minute), Rows: []Row{}}
	if err = s.Record(rejected, testTopology()); err == nil {
		t.Fatal("full legacy hour accepted another receipt")
	}
	if len(s.detail) != len(rows) || !s.segmentStart.IsZero() {
		t.Fatal("rejected receipt changed legacy detail")
	}
	nextHour := base.Truncate(time.Hour).Add(time.Hour)
	if err = s.Record(durabilityReceipt(nextHour, "resumed"), testTopology()); err != nil {
		t.Fatal("upgrade did not resume at the next UTC hour", err)
	}
	if !s.segmentStart.Equal(nextHour) || len(s.detail) != 1 || !s.detail[0].DeliveryGap {
		t.Fatal("segment did not begin with a disclosed gap")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.detailFence != nil || !s.segmentFormat || !s.segmentStart.Equal(nextHour) || len(s.detail) != 1 || !s.detail[0].DeliveryGap {
		t.Fatal("segment did not survive restart", s.detailFence)
	}
	prior, _ := detailForHour(t, s, base.Truncate(time.Hour))
	if !reflect.DeepEqual(prior, rows) {
		t.Fatal("upgrade changed the saturated legacy hour")
	}
}

func TestMetroSegmentFlushPreservesEmptyHotAggregateDay(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Now().UTC().Truncate(time.Hour)
	if err = s.Record(durabilityReceipt(start.Add(-time.Minute), "legacy"), testTopology()); err != nil {
		t.Fatal(err)
	}
	if err = s.Record(durabilityReceipt(start, "segment"), testTopology()); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.flush(start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	date := start.In(lisbon).Format("2006-01-02")
	key := "metro:aggregate:" + date
	before, err := s.publicationSource(key)
	if err != nil || before.block == nil {
		t.Fatal("missing aggregate fixture", err)
	}
	for key, a := range s.engine.Aggregates {
		if a.Date == date {
			delete(s.engine.Aggregates, key)
		}
	}
	s.engine.DirtyDays[date] = true
	if err = s.flush(start.Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := s.publicationSource(key)
	if err != nil || after.block == nil || *after.block != *before.block {
		t.Fatal("empty hot day replaced retained Metro aggregate", err)
	}
}

func TestMetroSegmentFlushRebuildsAfterPressureRetirement(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Hour)
	old := now.AddDate(0, 0, -8)
	a := baseAggregate(aggregateRequest{old, "line", "d", "A", "1", "p", "reported_normal", "signals"}, config)
	a.Count, a.KnownAt = 1, old.UnixNano()
	s.engine.add(a)
	if err = s.flush(old); err != nil {
		t.Fatal(err)
	}
	var day block
	for _, b := range s.index.Blocks {
		if b.Kind == "aggregate" && b.Operator == "metro" {
			day = b
		}
	}
	if day.File == "" {
		t.Fatal("missing old day")
	}
	prior, err := s.publicationSource("metro:state")
	if err != nil || prior.block == nil {
		t.Fatal("missing old checkpoint", err)
	}
	s.hour, s.segmentStart = now, now
	s.detail = []Receipt{{ReceivedAt: now, Operator: "metro"}}
	s.detailBytes, err = detailSize(s.detail)
	if err != nil {
		t.Fatal(err)
	}
	s.lastSample, s.engine.LastReceipt = now, now
	s.config.MinFreeBytes = 2 << 20
	probes := 0
	s.spaceProbe = func(string) (archiveSpace, error) {
		probes++
		if probes > 1 {
			if _, err := os.Stat(filepath.Join(config.Directory, day.File)); err == nil {
				return archiveSpace{bytes: 1 << 20, inodes: 100}, nil
			}
		}
		return archiveSpace{bytes: 1 << 40, inodes: 1 << 30}, nil
	}
	if err = s.flush(now.Add(time.Minute)); err == nil {
		t.Fatal("published a checkpoint prepared before pressure retirement")
	}
	if _, err = os.Stat(filepath.Join(config.Directory, day.File)); !os.IsNotExist(err) {
		t.Fatal("old aggregate was not retired", err)
	}
	if len(s.engine.Aggregates) != 0 || s.detailFence != nil {
		t.Fatal("pressure retirement did not preserve safe state")
	}
	current, err := s.publicationSource("metro:state")
	if err != nil || current.block == nil || current.block.File != prior.block.File {
		t.Fatal("stale checkpoint was published", err)
	}
	if err = s.flush(now.Add(2 * time.Minute)); err != nil {
		t.Fatal("rebuilt segment transaction could not progress", err)
	}
	if s.detailFence != nil || !s.segmentFormat {
		t.Fatal("rebuilt transaction did not publish a valid segment")
	}
}
