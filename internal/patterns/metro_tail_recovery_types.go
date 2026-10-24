package patterns

import (
	"encoding/hex"
	"fmt"
	"time"
)

// MetroTailRecovery records a deliberately unreplayed, verified detail tail.
// It remains in every later checkpoint so incomplete aggregates remain disclosed.
type MetroTailRecovery struct {
	Version          int       `json:"version"`
	ManifestSHA256   string    `json:"manifest_sha256"`
	CheckpointSHA256 string    `json:"checkpoint_sha256"`
	DetailSHA256     string    `json:"detail_sha256"`
	AggregateSHA256  string    `json:"aggregate_sha256"`
	DetailHour       time.Time `json:"detail_hour"`
	CheckpointAt     time.Time `json:"checkpoint_at"`
	TailAt           time.Time `json:"tail_at"`
	AffectedDay      string    `json:"affected_day"`
	CheckpointRows   int       `json:"checkpoint_rows"`
	TailOutcomes     int       `json:"tail_outcomes"`
	SampleSeconds    int       `json:"sample_seconds"`
	BinSeconds       int       `json:"bin_seconds"`
	PriorState       block     `json:"prior_state"`
}

// MetroTailRecoveryRequest pins the exact quiesced source and storage reserves.
type MetroTailRecoveryRequest struct {
	ManifestSHA256    string    `json:"manifest_sha256"`
	CheckpointSHA256  string    `json:"checkpoint_sha256"`
	DetailSHA256      string    `json:"detail_sha256"`
	AggregateSHA256   string    `json:"aggregate_sha256"`
	DetailHour        time.Time `json:"detail_hour"`
	CheckpointAt      time.Time `json:"checkpoint_at"`
	TailAt            time.Time `json:"tail_at"`
	AffectedDay       string    `json:"affected_day"`
	CheckpointRows    int       `json:"checkpoint_rows"`
	TailOutcomes      int       `json:"tail_outcomes"`
	SampleSeconds     int       `json:"sample_seconds"`
	BinSeconds        int       `json:"bin_seconds"`
	ArchiveLimitBytes int64     `json:"archive_limit_bytes"`
	MinFreeBytes      int64     `json:"min_free_bytes"`
	MinFreeInodes     int64     `json:"min_free_inodes"`
}

// MetroTailRecoveryResult contains only identities and counts, never receipts.
type MetroTailRecoveryResult struct {
	Status            string `json:"status"`
	OldManifestSHA256 string `json:"old_manifest_sha256"`
	NewManifestSHA256 string `json:"new_manifest_sha256"`
	OldStateSHA256    string `json:"old_state_sha256"`
	NewStateSHA256    string `json:"new_state_sha256"`
	DetailSHA256      string `json:"detail_sha256"`
	AggregateSHA256   string `json:"aggregate_sha256"`
	TailOutcomes      int    `json:"tail_outcomes"`
}

func validSHA256(v string) bool {
	if len(v) != 64 {
		return false
	}
	raw, err := hex.DecodeString(v)
	return err == nil && hex.EncodeToString(raw) == v
}

func (r MetroTailRecoveryRequest) marker(prior block) *MetroTailRecovery {
	return &MetroTailRecovery{Version: 1, ManifestSHA256: r.ManifestSHA256, CheckpointSHA256: r.CheckpointSHA256,
		DetailSHA256: r.DetailSHA256, AggregateSHA256: r.AggregateSHA256, DetailHour: r.DetailHour.UTC(),
		CheckpointAt: r.CheckpointAt.UTC(), TailAt: r.TailAt.UTC(), AffectedDay: r.AffectedDay,
		CheckpointRows: r.CheckpointRows, TailOutcomes: r.TailOutcomes, SampleSeconds: r.SampleSeconds,
		BinSeconds: r.BinSeconds, PriorState: prior}
}

func validMetroTailRecovery(m *MetroTailRecovery) bool {
	if m == nil {
		return true
	}
	return validMetroTailRecoverySources(m) && validMetroTailRecoveryBounds(m) && validMetroTailRecoveryClocks(m)
}

func validMetroTailRecoverySources(m *MetroTailRecovery) bool {
	return m.Version == 1 && validRecoveryHashes(m) && validRecoveryPriorState(m.PriorState, m.CheckpointSHA256)
}

func validRecoveryHashes(m *MetroTailRecovery) bool {
	return validSHA256(m.ManifestSHA256) && validSHA256(m.CheckpointSHA256) &&
		validSHA256(m.DetailSHA256) && validSHA256(m.AggregateSHA256)
}

func validRecoveryPriorState(prior block, hash string) bool {
	return prior.Hash == hash && prior.Key == "metro:state" && prior.Kind == "state" &&
		prior.Operator == "metro" && safeFilename(prior.File)
}

func validMetroTailRecoveryBounds(m *MetroTailRecovery) bool {
	return m.CheckpointRows >= 1 && m.CheckpointRows < maxDetailHourReceipts &&
		m.TailOutcomes >= 1 && m.TailOutcomes <= maxPendingForecasts && m.SampleSeconds >= 1 && m.BinSeconds >= 1
}

func validMetroTailRecoveryClocks(m *MetroTailRecovery) bool {
	day, err := time.Parse("2006-01-02", m.AffectedDay)
	return err == nil && day.Format("2006-01-02") == m.AffectedDay && validRecoveryHour(m) && validRecoveryTailClock(m)
}

func validRecoveryHour(m *MetroTailRecovery) bool {
	return !m.DetailHour.IsZero() && m.DetailHour.Equal(m.DetailHour.UTC().Truncate(time.Hour)) &&
		!m.CheckpointAt.IsZero() && m.CheckpointAt.UTC().Truncate(time.Hour).Equal(m.DetailHour)
}

func validRecoveryTailClock(m *MetroTailRecovery) bool {
	return m.TailAt.After(m.CheckpointAt) && m.TailAt.UTC().Truncate(time.Hour).Equal(m.DetailHour) &&
		m.TailAt.In(lisbon).Format("2006-01-02") == m.AffectedDay
}

func (r MetroTailRecoveryRequest) validate() error {
	if r.ArchiveLimitBytes <= 0 || r.MinFreeBytes <= 0 || r.MinFreeInodes <= 0 ||
		!validMetroTailRecovery(r.marker(block{Key: "metro:state", Kind: "state", Operator: "metro", File: "placeholder", Hash: r.CheckpointSHA256})) {
		return fmt.Errorf("invalid recovery request")
	}
	return nil
}
