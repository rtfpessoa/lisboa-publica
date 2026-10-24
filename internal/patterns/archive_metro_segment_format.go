package patterns

import (
	"fmt"
	"time"
)

// The format marker makes older readers reject the manifest before opening an
// archive whose active segment checkpoint they cannot safely interpret.
const metroSegmentFormatKind = "metro-segment-format-v1"
const metroSegmentFormatKey = "metro:segment-format:v1"

var metroSegmentFormatAge = time.Unix(0, 0).UTC()

type metroSegmentFormat struct {
	Version int `json:"version"`
}

func (s *Service) restoreSegmentFormat() error {
	var found *block
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Kind != metroSegmentFormatKind && b.Key != metroSegmentFormatKey {
			continue
		}
		if found != nil || !validSegmentFormatBlock(*b) {
			return fmt.Errorf("invalid Metro segment format marker")
		}
		copy := *b
		found = &copy
	}
	if found == nil {
		return nil
	}
	if err := s.verifySegmentFormatBlock(*found); err != nil {
		return err
	}
	s.segmentFormat = true
	return nil
}

func validSegmentFormatBlock(b block) bool {
	return validSegmentFormatIdentity(b) && b.Age.Equal(metroSegmentFormatAge) && b.AsOf.IsZero() &&
		b.Samples == 0 && b.Date == "" && !b.Closed
}

func validSegmentFormatIdentity(b block) bool {
	return b.Kind == metroSegmentFormatKind && b.Key == metroSegmentFormatKey && b.Operator == "metro"
}

func (s *Service) verifySegmentFormatBlock(b block) error {
	raw, err := s.decodedArchiveBlock(b)
	if err != nil || len(raw) > 128 {
		return fmt.Errorf("Metro segment format marker unreadable")
	}
	var value metroSegmentFormat
	if err := knownJSON(raw, &value); err != nil || value.Version != 1 {
		return fmt.Errorf("invalid Metro segment format marker")
	}
	return nil
}

func (s *Service) segmentFormatItem() (transactionItem, error) {
	b := block{Key: metroSegmentFormatKey, Kind: metroSegmentFormatKind, Operator: "metro", Age: metroSegmentFormatAge}
	source, err := s.publicationSource(b.Key)
	if err != nil {
		return transactionItem{}, err
	}
	if source.block != nil {
		if !validSegmentFormatBlock(*source.block) || s.verifySegmentFormatBlock(*source.block) != nil {
			return transactionItem{}, s.fenceDetail(fmt.Errorf("invalid Metro segment format marker"), source.block)
		}
		return transactionItem{block: b, encode: func() ([]byte, error) { return s.readBlock(*source.block) }}, nil
	}
	if s.segmentFormat {
		return transactionItem{}, s.fenceDetail(fmt.Errorf("Metro segment format marker missing"), nil)
	}
	return transactionItem{block: b, encode: func() ([]byte, error) {
		return s.encoder.EncodeAll([]byte(`{"version":1}`), nil), nil
	}}, nil
}
