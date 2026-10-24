package patterns

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const metroDetailSegmentKind = "metro-detail-segment"
const metroDetailSegmentPrefix = "metro:detail-segment:"
const metroDetailSegmentInterval = 5 * time.Minute

func metroDetailIdentity(b block) bool {
	return b.Operator == "metro" && (b.Kind == "detail" || b.Kind == metroDetailSegmentKind) ||
		strings.HasPrefix(b.Key, "metro:detail:") || strings.HasPrefix(b.Key, metroDetailSegmentPrefix)
}

func segmentRetentionWork(rows []Receipt) (int, error) {
	used := 0
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			return 0, err
		}
		_, work, ok := scanRecordClocksBudget(raw, maxRetentionClockNodes-used)
		if !ok {
			return 0, fmt.Errorf("segment retention clocks exceed scan limit")
		}
		used += work
	}
	return used, nil
}

func (s *Service) checkpointSegment(b block) bool {
	return b.Kind == metroDetailSegmentKind && !s.checkpointSegmentStart.IsZero() && b.Age.Equal(s.checkpointSegmentStart)
}

func validRecoveryDetailIdentity(b block) bool {
	prefix, interval := "metro:detail:", time.Hour
	if b.Kind == metroDetailSegmentKind {
		prefix, interval = metroDetailSegmentPrefix, metroDetailSegmentInterval
	} else if b.Kind != "detail" {
		return false
	}
	if b.Operator != "metro" || !strings.HasPrefix(b.Key, prefix) {
		return false
	}
	start, err := time.Parse(time.RFC3339, strings.TrimPrefix(b.Key, prefix))
	if err != nil || !start.Equal(start.UTC().Truncate(interval)) || !b.Age.Equal(start) || b.Key != prefix+start.UTC().Format(time.RFC3339) {
		return false
	}
	if b.Kind == metroDetailSegmentKind {
		return b.Samples > 0 && !b.AsOf.Before(start) && b.AsOf.Before(start.Add(interval))
	}
	return true
}

func invalidRecoveryDetailIdentities(blocks []block) map[string]bool {
	counts, hours := recoveryDetailCounts(blocks)
	invalid := map[string]bool{}
	for _, b := range blocks {
		if !metroDetailIdentity(b) {
			continue
		}
		if counts[b.Key] > 1 || !validRecoveryDetailIdentity(b) {
			invalid[b.Key] = true
		}
		if b.Kind == metroDetailSegmentKind && validRecoveryDetailIdentity(b) && hours[b.Age.UTC().Truncate(time.Hour)] {
			invalid[b.Key] = true
			invalid["metro:detail:"+b.Age.UTC().Truncate(time.Hour).Format(time.RFC3339)] = true
		}
	}
	return invalid
}

func recoveryDetailCounts(blocks []block) (map[string]int, map[time.Time]bool) {
	counts := make(map[string]int, len(blocks))
	hours := map[time.Time]bool{}
	for _, b := range blocks {
		counts[b.Key]++
		if b.Kind == "detail" && validRecoveryDetailIdentity(b) {
			hours[b.Age.UTC()] = true
		}
	}
	return counts, hours
}

func hasMetroDetailSegments(blocks []block) bool {
	for _, b := range blocks {
		if b.Kind == metroDetailSegmentKind || strings.HasPrefix(b.Key, metroDetailSegmentPrefix) ||
			b.Kind == metroSegmentFormatKind || b.Key == metroSegmentFormatKey {
			return true
		}
	}
	return false
}

func (s *Service) hasUnprovenMetroSegments(checkpointHour time.Time) bool {
	for _, b := range s.index.Blocks {
		if b.Kind != metroDetailSegmentKind {
			continue
		}
		if !s.checkpointRecovered || (!s.checkpointSegmentStart.IsZero() && b.Age.After(s.checkpointSegmentStart)) ||
			(s.checkpointSegmentStart.IsZero() && b.Age.Add(metroDetailSegmentInterval).After(checkpointHour)) {
			return true
		}
	}
	return false
}

func validCheckpointSegment(cp checkpoint) bool {
	if cp.SegmentStart == nil {
		return true
	}
	start := *cp.SegmentStart
	if start.IsZero() || !start.Equal(start.UTC().Truncate(metroDetailSegmentInterval)) ||
		!cp.Hour.Equal(start.UTC().Truncate(time.Hour)) || len(cp.Detail) == 0 {
		return false
	}
	for i, row := range cp.Detail {
		if !row.ReceivedAt.UTC().Truncate(metroDetailSegmentInterval).Equal(start) ||
			(i > 0 && !row.ReceivedAt.After(cp.Detail[i-1].ReceivedAt)) {
			return false
		}
	}
	return true
}

func (s *Service) verifyCheckpointSegment() error {
	key := metroDetailSegmentPrefix + s.checkpointSegmentStart.UTC().Format(time.RFC3339)
	var current *block
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Kind == "detail" && b.Operator == "metro" && !b.Age.Before(s.checkpointHour) {
			return s.fenceMetroRecoveryTail()
		}
		if b.Key == key {
			copy := *b
			current = &copy
		}
	}
	if current == nil {
		return s.fenceDetail(fmt.Errorf("checkpoint segment is missing"), nil)
	}
	rows, err := s.readDetailBlock(*current)
	if err != nil {
		return err
	}
	if len(rows) != len(s.checkpointDetail) || !receiptPrefix(rows, s.checkpointDetail) {
		return s.fenceDetail(fmt.Errorf("checkpoint and segment conflict"), current)
	}
	return nil
}
