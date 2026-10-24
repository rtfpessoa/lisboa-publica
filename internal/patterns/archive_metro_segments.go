package patterns

import (
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
		if b.Kind == metroDetailSegmentKind || strings.HasPrefix(b.Key, metroDetailSegmentPrefix) {
			return true
		}
	}
	return false
}

func (s *Service) hasUnprovenMetroSegments(checkpointHour time.Time) bool {
	for _, b := range s.index.Blocks {
		if b.Kind == metroDetailSegmentKind && (!s.checkpointRecovered || b.Age.Add(metroDetailSegmentInterval).After(checkpointHour)) {
			return true
		}
	}
	return false
}
