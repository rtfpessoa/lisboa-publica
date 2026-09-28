// Package patterns collects source predictions and produces explicitly experimental
// transport patterns. It never treats ETA-derived signals as physical observations.
package patterns

import (
	"encoding/json"
	"fmt"
	"time"
)

var lisbon, _ = time.LoadLocation("Europe/Lisbon")

// Config controls independent sampling, inference resolution and storage budgets.
type Config struct {
	Directory                                                  string
	LimitBytes                                                 int64
	SampleInterval, CheckpointInterval, EvaluationInterval     time.Duration
	DetailDays, AggregateMonths, TrainingDays, CalibrationDays int
	BinSeconds                                                 int
}

// DefaultConfig returns the initial server-only sampling and retention policy.
func DefaultConfig(directory string) Config {
	return Config{directory, 10_000_000_000, 30 * time.Second, time.Minute, time.Minute, 7, 12, 30, 30, 30}
}

// Validate checks the independent archive configuration bounds.
func (c Config) Validate() error {
	valid := c.Directory != "" && c.LimitBytes >= 1<<20
	checks := []bool{
		validSampleInterval(c.SampleInterval),
		validSampleInterval(c.CheckpointInterval),
		validSampleInterval(c.EvaluationInterval),
		(configRange{1, 30}).contains(c.DetailDays),
		(configRange{1, 24}).contains(c.AggregateMonths),
		(configRange{1, 365}).contains(c.TrainingDays),
		(configRange{1, 365}).contains(c.CalibrationDays),
		(configRange{1, 600}).contains(c.BinSeconds),
	}
	for _, check := range checks {
		valid = valid && check
	}
	if !valid {
		return fmt.Errorf("invalid transport archive configuration")
	}
	return nil
}

func validSampleInterval(interval time.Duration) bool {
	return interval >= 30*time.Second && interval <= time.Hour
}

type configRange struct{ minimum, maximum int }

func (r configRange) contains(value int) bool {
	return value >= r.minimum && value <= r.maximum
}

// Row preserves the fields used by inference; the original JSON is retained separately.
type Row struct {
	Stop        string          `json:"stop_id"`
	Platform    string          `json:"cais"`
	Clock       string          `json:"hora"`
	Destination string          `json:"destino"`
	Train       string          `json:"comboio"`
	Train2      string          `json:"comboio2"`
	Train3      string          `json:"comboio3"`
	ETA         json.RawMessage `json:"tempoChegada1"`
	ETA2        json.RawMessage `json:"tempoChegada2"`
	ETA3        json.RawMessage `json:"tempoChegada3"`
}

type Station struct{ ID, Name string }

// Pattern is an independently planned order. Sequence numbers preserve omitted visits.
type Pattern struct {
	Route, Direction, Destination string
	Stops                         []string
	Sequences                     []int
}
type Topology struct {
	Profile  string
	Segments map[string]string
	Patterns []Pattern
	Stations []Station
}
type Receipt struct {
	Topology         *Topology         `json:"topology,omitempty"`
	ReceivedAt       time.Time         `json:"received_at"`
	Operator         string            `json:"operator"`
	Profile          string            `json:"profile"`
	ServiceCondition string            `json:"service_condition"`
	RouteConditions  map[string]string `json:"route_conditions,omitempty"`
	Rows             []Row             `json:"rows"`
	Raw              json.RawMessage   `json:"raw,omitempty"`
	Error            string            `json:"error,omitempty"`
	Forecasts        []Forecast        `json:"forecasts,omitempty"`
	Outcomes         []Forecast        `json:"outcomes,omitempty"`
}

// Aggregate is sparse, additive and retains local-hour/offset, platform, component,
// profile, condition and histogram axes. No unknown probability denominator is stored.
type Aggregate struct {
	EpisodeHash    string  `json:"episode_hash,omitempty" parquet:"episode_hash,dict"`
	ErrorHistogram bool    `json:"error_histogram,omitempty" parquet:"error_histogram"`
	Cohort         string  `json:"cohort,omitempty" parquet:"cohort,dict"`
	Compatibility  string  `json:"compatibility,omitempty" parquet:"compatibility,dict"`
	LowerBucket    int32   `json:"lower_bucket" parquet:"lower_bucket"`
	Calendar       string  `json:"calendar,omitempty" parquet:"calendar,dict"`
	Operator       string  `json:"operator" parquet:"operator,dict"`
	Date           string  `json:"date" parquet:"date,dict"`
	Hour           int32   `json:"hour" parquet:"hour"`
	Offset         int32   `json:"offset" parquet:"offset"`
	DayType        string  `json:"day_type" parquet:"day_type,dict"`
	Route          string  `json:"route" parquet:"route,dict"`
	Direction      string  `json:"direction" parquet:"direction,dict"`
	Stop           string  `json:"stop" parquet:"stop,dict"`
	Platform       string  `json:"platform" parquet:"platform,dict"`
	Target         string  `json:"target" parquet:"target,dict"`
	TargetPlatform string  `json:"target_platform" parquet:"target_platform,dict"`
	Profile        string  `json:"profile" parquet:"profile,dict"`
	Reference      string  `json:"reference" parquet:"reference,dict"`
	Condition      string  `json:"condition" parquet:"condition,dict"`
	Kind           string  `json:"kind" parquet:"kind,dict"`
	Mode           string  `json:"mode" parquet:"mode,dict"`
	Horizon        int32   `json:"horizon" parquet:"horizon"`
	Resolution     int32   `json:"resolution" parquet:"resolution"`
	Bucket         int32   `json:"bucket" parquet:"bucket"`
	Count          int64   `json:"count" parquet:"count"`
	Sum            float64 `json:"sum" parquet:"sum"`
	LowerSum       float64 `json:"lower_sum" parquet:"lower_sum"`
	UpperSum       float64 `json:"upper_sum" parquet:"upper_sum"`
	InputFrom      int64   `json:"input_from" parquet:"input_from"`
	KnownAt        int64   `json:"known_at" parquet:"known_at"`
}

type Component struct {
	targetPlatform     string
	Origin             string    `json:"origin"`
	Target             string    `json:"target"`
	OriginAt           time.Time `json:"origin_at"`
	Seconds            float64   `json:"seconds"`
	Samples            int64     `json:"samples"`
	Days               int       `json:"days"`
	HistoricalFallback bool      `json:"historical_fallback"`
	GeneralContext     bool      `json:"general_context"`
	OldestDate         string    `json:"oldest_date"`
	NewestDate         string    `json:"newest_date"`
}

type Forecast struct {
	// OwnValidUntil preserves the freshness bound of the original model anchor.
	OwnValidUntil      *time.Time  `json:"own_valid_until,omitempty"`
	ProviderTrip       string      `json:"provider_trip,omitempty"`
	Result             string      `json:"result"`
	ID                 string      `json:"id"`
	Episode            string      `json:"episode"`
	IssuedAt           time.Time   `json:"issued_at"`
	Route              string      `json:"route"`
	Direction          string      `json:"direction"`
	Stop               string      `json:"stop"`
	StopName           string      `json:"stop_name"`
	DestinationName    string      `json:"destination_name"`
	Platform           string      `json:"platform"`
	Train              string      `json:"train"`
	Function           string      `json:"function"`
	Mode               string      `json:"mode"`
	Profile            string      `json:"profile"`
	Condition          string      `json:"condition"`
	SourceAt           *time.Time  `json:"source_at"`
	OfficialAt         *time.Time  `json:"official_at"`
	OwnAt              *time.Time  `json:"own_at"`
	LowerAt            *time.Time  `json:"lower_at"`
	UpperAt            *time.Time  `json:"upper_at"`
	Unavailable        string      `json:"unavailable"`
	Components         []Component `json:"components"`
	CalibrationSamples int64       `json:"calibration_samples"`
	Selected           bool        `json:"selected"`
	Evaluated          bool        `json:"evaluated"`
	ReferenceLower     *time.Time  `json:"reference_lower"`
	ReferenceUpper     *time.Time  `json:"reference_upper"`
	ErrorLower         *float64    `json:"error_lower"`
	ErrorUpper         *float64    `json:"error_upper"`
}

type HourPattern struct {
	Profile              string   `json:"profile"`
	OffsetSeconds        int      `json:"offset_seconds"`
	ResolutionSeconds    int      `json:"resolution_seconds"`
	Condition            string   `json:"condition"`
	Hour                 int      `json:"hour"`
	Direction            string   `json:"direction"`
	Route                string   `json:"route"`
	Platform             string   `json:"platform"`
	DayType              string   `json:"day_type"`
	Signals              int64    `json:"signals"`
	Days                 int      `json:"days"`
	Probability          *float64 `json:"probability"`
	MeanHeadwaySeconds   *float64 `json:"mean_headway_seconds"`
	MeanComponentSeconds *float64 `json:"mean_component_seconds"`
	ComponentTarget      string   `json:"component_target"`
	ComponentSamples     int64    `json:"component_samples"`
}

type View struct {
	Operator           string             `json:"operator"`
	Operators          []OperatorHistory  `json:"operators"`
	CurrentDayType     string             `json:"current_day_type"`
	Calendar           string             `json:"calendar"`
	Status             string             `json:"status"`
	Message            string             `json:"message"`
	Experimental       bool               `json:"experimental"`
	PhysicalValidation bool               `json:"physical_validation"`
	AsOf               *time.Time         `json:"as_of"`
	Profile            string             `json:"profile"`
	StorageBytes       int64              `json:"storage_bytes"`
	LimitBytes         int64              `json:"limit_bytes"`
	SampleSeconds      int                `json:"sample_seconds"`
	BinSeconds         int                `json:"bin_seconds"`
	DetailDays         int                `json:"detail_days"`
	AggregateMonths    int                `json:"aggregate_months"`
	CollectedDays      int                `json:"collected_days"`
	Gaps               int64              `json:"gaps"`
	Pending            int                `json:"pending"`
	Evaluated          int                `json:"evaluated"`
	LostReference      int                `json:"lost_reference"`
	DwellSeconds       *float64           `json:"dwell_seconds"`
	SpeedKmh           *float64           `json:"speed_kmh"`
	Patterns           []HourPattern      `json:"patterns"`
	Forecasts          []Forecast         `json:"forecasts"`
	Evaluation         []EvaluationReport `json:"evaluation"`
}

// condition never spreads a line alert to another route.
func (r Receipt) condition(route string) string {
	if r.RouteConditions != nil {
		if c, ok := r.RouteConditions[route]; ok {
			return c
		}
		return "unknown"
	}
	if r.ServiceCondition == "" {
		return "unknown"
	}
	return r.ServiceCondition
}
