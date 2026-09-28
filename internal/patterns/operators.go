package patterns

import (
	"fmt"
	"time"
)

// Operators are enabled in stages. Collection does not certify physical events.
var operatorOrder = []string{"metro", "cm", "carris", "cp", "fertagus", "ttsl", "tcb", "mobi"}

type OperatorHistory struct {
	Operator           string     `json:"operator"`
	Enabled            bool       `json:"enabled"`
	Status             string     `json:"status"`
	Forecasts          bool       `json:"forecasts"`
	PhysicalValidation bool       `json:"physical_validation"`
	AsOf               *time.Time `json:"as_of"`
	Samples            int64      `json:"samples"`
}

// Observation retains published attributes and source time. Coordinates/status
// may be estimated; neither a receipt nor reported speed establishes movement.
type Observation struct {
	Journey         string    `json:"journey,omitempty"`
	ID              string    `json:"id"`
	SourceID        string    `json:"source_id"`
	SourceURL       string    `json:"source_url"`
	ObservedAt      time.Time `json:"observed_at"`
	Route           string    `json:"route_id,omitempty"`
	Trip            string    `json:"trip_id,omitempty"`
	Plan            string    `json:"plan_id,omitempty"`
	Pattern         string    `json:"pattern_id,omitempty"`
	OperationalDate string    `json:"operational_date,omitempty"`
	Stop            string    `json:"stop_id,omitempty"`
	SourceStop      string    `json:"source_stop_id,omitempty"`
	Status          string    `json:"status,omitempty"`
	PositionKind    string    `json:"position_kind"`
	Lat             float64   `json:"lat"`
	Lon             float64   `json:"lon"`
	SpeedKmh        *float64  `json:"speed_kmh"`
}
type ProviderReceipt struct {
	Limited        bool                       `json:"limited,omitempty"`
	Cuts           map[string]bool            `json:"cuts,omitempty"`
	CollectedAt    *time.Time                 `json:"collected_at,omitempty"`
	Gap            bool                       `json:"gap,omitempty"`
	ReceivedAt     time.Time                  `json:"received_at"`
	Operator       string                     `json:"operator"`
	Partial        bool                       `json:"partial,omitempty"`
	PredictionStop string                     `json:"prediction_stop,omitempty"`
	Predictions    []ProviderPrediction       `json:"predictions,omitempty"`
	Journeys       map[string]ProviderJourney `json:"journeys,omitempty"`
	Forecasts      []Forecast                 `json:"forecasts,omitempty"`
	Outcomes       []Forecast                 `json:"outcomes,omitempty"`
	Profile        string                     `json:"profile"`
	Error          string                     `json:"error,omitempty"`
	Rows           []Observation              `json:"rows"`
}

func knownOperator(value string) bool {
	for _, id := range operatorOrder {
		if id == value {
			return true
		}
	}
	return false
}

// ConfigureOperators admits an explicit prefix: Metro, then CM, then each
// operator separately. Disabling collection does not delete retained data.
func (s *Service) ConfigureOperators(operators []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ValidateOperatorStages(operators); err != nil {
		return err
	}
	enabled := map[string]bool{}
	for _, id := range operators {
		enabled[id] = true
	}
	s.operators = enabled
	return nil
}

func (s *Service) operatorHistory() []OperatorHistory {
	out := make([]OperatorHistory, 0, len(operatorOrder))
	for _, id := range operatorOrder {
		out = append(out, s.operatorSummary(id))
	}
	return out
}

func (s *Service) operatorSummary(id string) OperatorHistory {
	v := OperatorHistory{Operator: id, Enabled: s.operators[id], Status: "disabled", Forecasts: true}
	if v.Enabled {
		v.Status = "waiting"
	}
	for _, b := range s.index.Blocks {
		if operatorHistoryBlock(b, id) {
			addOperatorBlock(&v, b)
		}
	}
	if id == "metro" && !s.engine.LastReceipt.IsZero() {
		at := s.engine.LastReceipt
		v.AsOf = &at
	}
	if v.Enabled && v.AsOf != nil {
		v.Status = "experimental"
	}
	if id == "metro" && v.AsOf != nil {
		v.Status = s.status
	}
	return v
}

func operatorHistoryBlock(b block, id string) bool {
	if b.Operator != id {
		return false
	}
	return b.Kind == "observations" || id == "metro" && b.Kind == "detail"
}

func addOperatorBlock(v *OperatorHistory, b block) {
	v.Samples += b.Samples
	if v.AsOf == nil || b.AsOf.After(*v.AsOf) {
		at := b.AsOf
		v.AsOf = &at
	}
}

// ValidateOperatorStages requires the configured prefix of the operator rollout.
func ValidateOperatorStages(operators []string) error {
	if len(operators) == 0 || len(operators) > len(operatorOrder) {
		return fmt.Errorf("invalid archive operator stages")
	}
	for i, id := range operators {
		if id != operatorOrder[i] {
			return fmt.Errorf("archive operators must follow stages: %v", operatorOrder)
		}
	}
	return nil
}

// EnabledOperators is a detached startup snapshot. Runtime stage changes require
// restarting the collectors rather than sharing a mutable map with publication.
func (s *Service) EnabledOperators() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for id, enabled := range s.operators {
		out[id] = enabled
	}
	return out
}
