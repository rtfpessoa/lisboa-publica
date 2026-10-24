package patterns

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
)

type aggregateClock struct {
	newest                    time.Time
	modified                  time.Time
	size                      int64
	hash                      string
	key, kind, operator, date string
	valid                     bool
}

type retentionClockKey struct {
	file, hash, key, kind, operator, date string
}

func retentionKey(b block) retentionClockKey {
	return retentionClockKey{b.File, b.Hash, b.Key, b.Kind, b.Operator, b.Date}
}

func cachedRetentionClock(c aggregateClock, b block, info os.FileInfo) bool {
	return c.hash == b.Hash && c.key == b.Key && c.kind == b.Kind && c.operator == b.Operator && c.date == b.Date && c.size == info.Size() && c.modified.Equal(info.ModTime())
}

func bucketKeyMatches(b block, age time.Time) bool {
	stamp := age.Format(time.RFC3339)
	switch b.Kind {
	case "detail":
		return b.Key == "metro:detail:"+stamp
	case metroDetailSegmentKind:
		return b.Key == metroDetailSegmentPrefix+stamp
	case "observations":
		prefix := b.Operator + ":observations:" + stamp
		return b.Key == prefix || strings.HasPrefix(b.Key, prefix+":")
	case "popup-events":
		return b.Key == "metro-popup-events:"+stamp
	case "metro-inputs":
		return b.Key == "metro-inputs:"+stamp
	default:
		return false
	}
}

// blockBoundary returns a conservative upper bound on represented clocks. A
// point is strictly older at the cutoff; an exclusive bucket end may equal it.
// Unknown legacy/correction formats are retained rather than guessed away.
func metadataBoundary(b block) (time.Time, bool, bool) {
	if b.Age.IsZero() {
		return time.Time{}, false, false
	}
	age := b.Age.UTC()
	switch b.Kind {
	case "popup-checkpoint":
		return age, true, strings.HasPrefix(b.Key, "metro-popup-checkpoint:")
	case "detail", "observations", "popup-events":
		return bucketBoundary(b, age, time.Hour)
	case "metro-inputs", metroDetailSegmentKind:
		return bucketBoundary(b, age, 5*time.Minute)
	case "aggregate":
		return aggregateBoundary(b, age)
	default:
		return time.Time{}, false, false
	}
}

func bucketBoundary(b block, age time.Time, interval time.Duration) (time.Time, bool, bool) {
	if !age.Equal(age.Truncate(interval)) || !bucketKeyMatches(b, age) {
		return time.Time{}, false, false
	}
	return age.Add(interval), false, true
}

func aggregateBoundary(b block, age time.Time) (time.Time, bool, bool) {
	start, err := time.ParseInLocation("2006-01-02", b.Date, lisbon)
	if err != nil || !age.Equal(start.UTC()) {
		return time.Time{}, false, false
	}
	return start.AddDate(0, 0, 1).UTC(), false, true
}

func (s *Service) blockBoundary(b block) (time.Time, bool, bool) {
	end, point, ok := metadataBoundary(b)
	if !ok {
		return end, point, false
	}
	var newest time.Time
	if b.Kind == "aggregate" {
		newest, ok = s.aggregateNewest(b)
	} else {
		newest, ok = s.recordNewest(b)
	}
	if !ok {
		return time.Time{}, false, false
	}
	if !newest.Before(end) {
		return newest, true, true
	}
	return end, point, true
}

// Timestamp-like values are only conservative storage bounds, never evidence
// of an actual arrival. Unreadable or ambiguous generations are retained.
func (s *Service) recordNewest(b block) (time.Time, bool) {
	info, err := os.Stat(filepath.Join(s.config.Directory, b.File))
	if err != nil {
		return time.Time{}, false
	}
	if cached, ok := s.aggregateClocks[retentionKey(b)]; ok && cachedRetentionClock(cached, b, info) {
		return cached.newest, cached.valid
	}
	if s.aggregateClocks == nil {
		s.aggregateClocks = make(map[retentionClockKey]aggregateClock)
	}
	latest, ok := s.readRecordNewest(b)
	s.aggregateClocks[retentionKey(b)] = aggregateClock{newest: latest, modified: info.ModTime(), size: info.Size(), hash: b.Hash, key: b.Key, kind: b.Kind, operator: b.Operator, date: b.Date, valid: ok}
	return latest, ok
}

func (s *Service) readRecordNewest(b block) (time.Time, bool) {
	blob, err := s.readBlock(b)
	if err != nil {
		return time.Time{}, false
	}
	raw, err := s.decoder.DecodeAll(blob, nil)
	if err != nil || len(raw) > recordSizeLimit(b.Kind) {
		return time.Time{}, false
	}
	if b.Kind == metroDetailSegmentKind {
		if _, err = decodeDetailRecords(raw, b); err != nil {
			return time.Time{}, false
		}
	}
	return scanRecordClocks(raw)
}

func recordSizeLimit(kind string) int {
	switch kind {
	case "metro-inputs":
		return 32 << 20
	case "popup-checkpoint":
		return maxMetroCheckpointBytes
	default:
		return maxDetailHourBytes
	}
}

func scanRecordClocks(raw []byte) (time.Time, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	scan := retentionClockScan{budget: 1000000}
	count := 0
	for {
		var value any
		err := dec.Decode(&value)
		if err == io.EOF {
			break
		}
		if err != nil || !scan.value(value, "", 0, false) {
			return time.Time{}, false
		}
		count++
	}
	return scan.latest, count > 0 && !scan.latest.IsZero()
}

type retentionClockScan struct {
	latest time.Time
	budget int
}

func (s *retentionClockScan) value(value any, key string, depth int, support bool) bool {
	s.budget--
	if s.budget < 0 || depth > 64 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		return s.object(v, depth, support || key == "model_departure_support")
	case []any:
		return s.array(v, key, depth, support)
	case string:
		return scanClockText(v, isClockKey(key) || looksLikeClockKey(key), &s.latest)
	case json.Number:
		return !(isClockKey(key) || looksLikeClockKey(key)) || scanClockText(string(v), true, &s.latest)
	}
	return true
}

func (s *retentionClockScan) object(values map[string]any, depth int, support bool) bool {
	for key, child := range values {
		// Some model support tables use millisecond clocks as object keys.
		if _, err := strconv.ParseInt(key, 10, 64); support && err == nil && !scanClockText(key, true, &s.latest) {
			return false
		}
		if !s.value(child, key, depth+1, support) {
			return false
		}
	}
	return true
}

func (s *retentionClockScan) array(values []any, key string, depth int, support bool) bool {
	for _, child := range values {
		if !s.value(child, key, depth+1, support) {
			return false
		}
	}
	return true
}

func isClockKey(key string) bool {
	switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
	case "at", "hora", "clock", "timestamp", "asof", "receivedat", "createdat", "sourceat", "sourceupdatedat", "publishedat", "observedat", "collectedat", "confirmedat", "committedat", "issuedat", "originat", "officialat", "ownat", "lowerat", "upperat", "windowstart", "windowend", "firstmovementat", "validuntil", "ownvaliduntil", "referenceupper", "referencelower", "knownat", "inputfrom", "underlyinginputat":
		return true
	default:
		return false
	}
}

func looksLikeClockKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, "_at") || strings.HasSuffix(lower, "_time") || strings.HasSuffix(lower, "_timestamp") || strings.HasSuffix(key, "At")
}

func scanClockText(v string, required bool, latest *time.Time) bool {
	if v == "" {
		return !required
	}
	t, ok := parseRetentionClock(v, required)
	if !ok {
		return false
	}
	if t.After(*latest) {
		*latest = t
	}
	return true
}

func parseRetentionClock(v string, required bool) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, true
	}
	if !required {
		return time.Time{}, true
	}
	if t, parsed, ok := parseCivilClock(v); parsed {
		return t, ok
	}
	return parseEpochClock(v)
}

func parseCivilClock(v string) (time.Time, bool, bool) {
	if len(v) != 14 {
		return time.Time{}, false, false
	}
	t, err := time.ParseInLocation("20060102150405", v, lisbon)
	if err != nil {
		return time.Time{}, false, false
	}
	if t.In(lisbon).Format("20060102150405") != v {
		return time.Time{}, true, false
	}
	// A repeated local hour has two valid UTC instants. Retain to the later one.
	if later := t.Add(time.Hour); later.In(lisbon).Format("20060102150405") == v {
		t = later
	}
	return t, true, true
}

func parseEpochClock(v string) (time.Time, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	switch len(v) {
	case 10:
		return time.Unix(n, 0), true
	case 13:
		return time.UnixMilli(n), true
	case 16:
		return time.UnixMicro(n), true
	case 19:
		return time.Unix(0, n), true
	default:
		return time.Time{}, false
	}
}

func (s *Service) aggregateNewest(b block) (time.Time, bool) {
	info, err := os.Stat(filepath.Join(s.config.Directory, b.File))
	if err != nil {
		return time.Time{}, false
	}
	if cached, ok := s.aggregateClocks[retentionKey(b)]; ok && cachedRetentionClock(cached, b, info) {
		return cached.newest, cached.valid
	}
	if s.aggregateClocks == nil {
		s.aggregateClocks = make(map[retentionClockKey]aggregateClock)
	}
	latest, ok := s.readAggregateNewest(b)
	s.aggregateClocks[retentionKey(b)] = aggregateClock{newest: latest, modified: info.ModTime(), size: info.Size(), hash: b.Hash, key: b.Key, kind: b.Kind, operator: b.Operator, date: b.Date, valid: ok}
	return latest, ok
}

func (s *Service) readAggregateNewest(b block) (time.Time, bool) {
	blob, err := s.readBlock(b)
	if err != nil {
		return time.Time{}, false
	}
	file, err := parquet.OpenFile(bytes.NewReader(blob), int64(len(blob)))
	if err != nil || file.NumRows() == 0 || file.NumRows() > maxEngineAggregates {
		return time.Time{}, false
	}
	reader := parquet.NewGenericReader[Aggregate](file)
	defer reader.Close()
	batch := make([]Aggregate, 128)
	latest := time.Time{}
	for {
		n, err := reader.Read(batch)
		if !aggregateBatchNewest(batch[:n], b, &latest) {
			return time.Time{}, false
		}
		if err == io.EOF {
			return latest, !latest.IsZero()
		}
		if err != nil || n == 0 {
			return time.Time{}, false
		}
	}
}

func aggregateBatchNewest(rows []Aggregate, b block, latest *time.Time) bool {
	for _, row := range rows {
		if row.Date != b.Date || row.Operator != b.Operator || row.KnownAt <= 0 {
			return false
		}
		clock := time.Unix(0, max(row.KnownAt, row.InputFrom)).UTC()
		if clock.After(*latest) {
			*latest = clock
		}
	}
	return true
}

func (s *Service) pastBoundary(b block, cutoff, now time.Time) bool {
	// Do not decompress young blocks when their bucket alone protects them.
	if !pastMetadataBoundary(b, cutoff, now) {
		return false
	}
	end, point, ok := s.blockBoundary(b)
	if !ok || end.After(now) {
		return false
	}
	if point {
		return end.Before(cutoff)
	}
	return !end.After(cutoff)
}

func pastMetadataBoundary(b block, cutoff, now time.Time) bool {
	end, point, ok := metadataBoundary(b)
	return ok && !end.After(now) && ((point && end.Before(cutoff)) || (!point && !end.After(cutoff)))
}

func (s *Service) expiryCutoff(b block, now time.Time) (time.Time, bool) {
	var cutoff time.Time
	switch b.Kind {
	case "popup-events", "popup-checkpoint", "metro-inputs":
		cutoff = now.AddDate(0, 0, -7)
	case "detail", metroDetailSegmentKind, "correction", "observations":
		cutoff = now.AddDate(0, 0, -s.config.DetailDays)
	case "aggregate":
		cutoff = now.In(lisbon).AddDate(0, -s.config.AggregateMonths, 0)
		if b.Date >= cutoff.Format("2006-01-02") {
			return time.Time{}, false
		}
	default:
		return time.Time{}, false
	}
	return cutoff, true
}

func (s *Service) blockExpired(b block, now time.Time) bool {
	cutoff, ok := s.expiryCutoff(b, now)
	return ok && s.pastBoundary(b, cutoff, now)
}

func (s *Service) pressureEligible(b block, now time.Time) bool {
	if b.Kind == "aggregate" {
		start, err := time.ParseInLocation("2006-01-02", b.Date, lisbon)
		if err != nil || start.AddDate(0, 0, 1).After(now.AddDate(0, 0, -7)) {
			return false
		}
	}
	return s.pastBoundary(b, now.AddDate(0, 0, -7), now)
}
