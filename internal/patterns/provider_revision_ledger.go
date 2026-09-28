package patterns

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ProviderCorrection revises an exact retained observation, preserving its
// published journey/stop context and every original issuance value.
type ProviderCorrection struct {
	ReceivedAt   time.Time   `json:"received_at"`
	ExpectedHash string      `json:"expected_hash"`
	Row          Observation `json:"row"`
	Evidence     string      `json:"evidence"`
}

func (s *Service) providerCorrections(operator string) ([]ProviderCorrection, error) {
	var values []ProviderCorrection
	used := 0
	blocks := append([]block{}, s.index.Blocks...)
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Age.Equal(blocks[j].Age) {
			return blocks[i].AsOf.Before(blocks[j].AsOf)
		}
		return blocks[i].Age.Before(blocks[j].Age)
	})
	for _, b := range blocks {
		if b.Operator != operator || b.Kind != "correction" {
			continue
		}
		changes, err := s.readProviderLedger(b, &used)
		if err != nil {
			return nil, err
		}
		values = append(values, changes...)
	}
	return values, nil
}
func applyProviderCorrections(r *ProviderReceipt, values []ProviderCorrection) {
	for _, change := range values {
		if !r.ReceivedAt.Equal(change.ReceivedAt) {
			continue
		}
		for i, row := range r.Rows {
			if digest(row) == change.ExpectedHash {
				r.Rows[i] = change.Row
			}
		}
	}
}

func (s *Service) readProviderLedger(b block, used *int) ([]ProviderCorrection, error) {
	raw, err := s.revisionBlock(b, used, "provider revision ledger limit")
	if err != nil {
		return nil, err
	}
	var changes []ProviderCorrection
	if err := json.Unmarshal(raw, &changes); err != nil || len(changes) > 1000 {
		return nil, fmt.Errorf("provider revision ledger invalid")
	}
	return changes, nil
}
