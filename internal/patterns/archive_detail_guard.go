package patterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// fenceDetail preserves uncertain evidence until a reopen can verify it again.
func (s *Service) fenceDetail(err error, b *block) error {
	if b != nil {
		if s.protectedGenerations == nil {
			s.protectedGenerations = map[retentionClockKey]bool{}
		}
		s.protectedGenerations[retentionKey(*b)] = true
	}
	if s.detailFence == nil {
		s.detailFence = s.markPaused(fmt.Errorf("transport archive detail unavailable: %w", err))
	}
	return s.detailFence
}

// An unreadable checkpoint may refer to any retained detail hour. Protect
// their identities without decompressing historical generations during open.
func (s *Service) protectCheckpointDetails() {
	if s.protectedGenerations == nil {
		s.protectedGenerations = map[retentionClockKey]bool{}
	}
	for _, b := range s.index.Blocks {
		if metroDetailIdentity(b) {
			s.protectedGenerations[retentionKey(b)] = true
		}
	}
}

// detailBlock finds a unique source without treating an invalid key as absent.
func (s *Service) detailBlock(hour time.Time) (*block, error) {
	key := "metro:detail:" + hour.UTC().Format(time.RFC3339)
	var result *block
	ambiguous := false
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Key != key {
			continue
		}
		if result != nil || b.Kind != "detail" || b.Operator != "metro" || !b.Age.Equal(hour) {
			ambiguous = true
		}
		if result == nil {
			copy := *b
			result = &copy
		}
	}
	if ambiguous {
		for i := range s.index.Blocks {
			if s.index.Blocks[i].Key == key {
				s.fenceDetail(fmt.Errorf("ambiguous detail generation"), &s.index.Blocks[i])
			}
		}
		return nil, s.detailFence
	}
	return result, nil
}

// knownJSON rejects unknown or non-round-trippable fields rather than dropping
// them when an existing generation is replaced.
func knownJSON(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("extra JSON data")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return preserveJSONValues(raw, encoded)
}

func (s *Service) readDetailBlock(b block) ([]Receipt, error) {
	raw, err := s.decodedArchiveBlock(b)
	if err != nil {
		return nil, s.fenceDetail(err, &b)
	}
	records, err := decodeDetailRecords(raw, b)
	if err != nil {
		return nil, s.fenceDetail(err, &b)
	}
	return records, nil
}

func decodeDetailRecords(raw []byte, b block) ([]Receipt, error) {
	if len(raw) > maxDetailHourBytes {
		return nil, fmt.Errorf("detail byte limit")
	}
	if b.Kind == metroDetailSegmentKind && !validRecoveryDetailIdentity(b) {
		return nil, fmt.Errorf("invalid detail segment identity")
	}
	records, canonical, err := decodeDetailLines(raw, b)
	if err != nil {
		return nil, err
	}
	if err := validateDecodedDetail(raw, canonical, b, records); err != nil {
		return nil, err
	}
	return records, nil
}

func decodeDetailLines(raw []byte, b block) ([]Receipt, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var records []Receipt
	var canonical bytes.Buffer
	for {
		var encoded json.RawMessage
		err := decoder.Decode(&encoded)
		if err == io.EOF {
			break
		}
		if err != nil || len(records) >= maxDetailHourReceipts {
			return nil, nil, fmt.Errorf("invalid or oversized detail")
		}
		receipt, err := decodeDetailReceipt(encoded, records, b)
		if err != nil {
			return nil, nil, err
		}
		canonical.Write(encoded)
		canonical.WriteByte('\n')
		records = append(records, receipt)
	}
	return records, canonical.Bytes(), nil
}

func validateDecodedDetail(raw, canonical []byte, b block, records []Receipt) error {
	if !bytes.Equal(raw, canonical) || b.Samples > 0 && b.Samples != int64(len(records)) {
		return fmt.Errorf("detail representation is incomplete")
	}
	if b.Kind == metroDetailSegmentKind && (len(records) == 0 || !b.AsOf.Equal(records[len(records)-1].ReceivedAt)) {
		return fmt.Errorf("detail segment watermark invalid")
	}
	return nil
}

func decodeDetailReceipt(encoded []byte, earlier []Receipt, b block) (Receipt, error) {
	var receipt Receipt
	if err := knownJSON(encoded, &receipt); err != nil {
		return receipt, err
	}
	interval := time.Hour
	if b.Kind == metroDetailSegmentKind {
		interval = metroDetailSegmentInterval
	}
	if receipt.ReceivedAt.IsZero() || !receipt.ReceivedAt.UTC().Truncate(interval).Equal(b.Age) ||
		(len(earlier) > 0 && (receipt.ReceivedAt.Before(earlier[len(earlier)-1].ReceivedAt) || b.Kind == metroDetailSegmentKind && receipt.ReceivedAt.Equal(earlier[len(earlier)-1].ReceivedAt))) {
		return receipt, fmt.Errorf("detail receipt clock invalid")
	}
	return receipt, nil
}

func validateDetailReceipts(records []Receipt, hour time.Time) error {
	if _, err := detailSize(records); err != nil {
		return err
	}
	if len(records) > 0 && (hour.IsZero() || !hour.Equal(hour.UTC().Truncate(time.Hour))) {
		return fmt.Errorf("invalid checkpoint detail hour")
	}
	for i, r := range records {
		if r.ReceivedAt.IsZero() || !r.ReceivedAt.UTC().Truncate(time.Hour).Equal(hour) || (i > 0 && r.ReceivedAt.Before(records[i-1].ReceivedAt)) {
			return fmt.Errorf("invalid checkpoint detail clock")
		}
	}
	return nil
}

func receiptPrefix(prefix, full []Receipt) bool {
	if len(prefix) > len(full) {
		return false
	}
	for i := range prefix {
		a, err := json.Marshal(prefix[i])
		if err != nil {
			return false
		}
		b, err := json.Marshal(full[i])
		if err != nil || !bytes.Equal(a, b) {
			return false
		}
	}
	return true
}

func detailSize(records []Receipt) (int, error) {
	if len(records) > maxDetailHourReceipts {
		return 0, fmt.Errorf("detail receipt limit")
	}
	size := 0
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			return 0, err
		}
		size += len(raw) + 1
		if size > maxDetailHourBytes {
			return 0, fmt.Errorf("detail byte limit")
		}
	}
	return size, nil
}

func (s *Service) verifyDetailBase(b block, candidate []byte) error {
	if len(candidate) > maxDetailHourBytes {
		return s.fenceDetail(fmt.Errorf("detail byte limit"), nil)
	}
	prior, err := s.detailBlock(b.Age)
	if err != nil || prior == nil {
		return err
	}
	records, err := s.readDetailBlock(*prior)
	if err != nil {
		return err
	}
	if !receiptPrefix(records, s.detail) {
		return s.fenceDetail(fmt.Errorf("detail would omit retained receipts"), prior)
	}
	return nil
}

func (s *Service) loadDetailHour(hour time.Time) error {
	prior, err := s.detailBlock(hour)
	if err != nil || prior == nil {
		return err
	}
	records, err := s.readDetailBlock(*prior)
	if err != nil {
		return err
	}
	size, err := detailSize(records)
	if err != nil {
		return s.fenceDetail(err, prior)
	}
	s.detail, s.detailBytes = records, size
	if len(records) > 0 {
		s.lastSample = records[len(records)-1].ReceivedAt
	}
	return nil
}
