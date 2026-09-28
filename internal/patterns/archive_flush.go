package patterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/parquet-go/parquet-go"
	parquetZstd "github.com/parquet-go/parquet-go/compress/zstd"
	"sort"
	"time"
)

func (s *Service) flush(now time.Time) error {
	if s.encoder == nil || s.engine == nil {
		return nil
	}
	steps := []func(time.Time) error{s.prepareFlush, s.flushMetroDetail, s.flushMetroAggregates, s.flushProviders, s.flushCheckpoint}
	for _, step := range steps {
		if err := step(now); err != nil {
			return err
		}
	}
	s.acknowledgeFlush(now)
	return nil
}

func (s *Service) prepareFlush(now time.Time) error {
	if err := s.reconcile(); err != nil {
		return err
	}
	return s.ensureSpace(manifestReserve, now)
}

func (s *Service) flushMetroDetail(now time.Time) error {
	if len(s.detail) == 0 {
		return nil
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	for _, r := range s.detail {
		if err := encoder.Encode(r); err != nil {
			return err
		}
	}
	blob, err := s.verifiedDetailBlob(raw.Bytes())
	if err != nil {
		return err
	}
	b := block{AsOf: s.lastSample, Samples: int64(len(s.detail)), Key: "metro:detail:" + s.hour.Format(time.RFC3339), Kind: "detail", Operator: "metro", Age: s.hour, Closed: s.hour.Before(now.UTC().Truncate(time.Hour))}
	return s.publish(b, blob, now)
}

func (s *Service) verifiedDetailBlob(raw []byte) ([]byte, error) {
	blob := s.encoder.EncodeAll(raw, nil)
	decoded, err := s.decoder.DecodeAll(blob, nil)
	if err != nil || !bytes.Equal(decoded, raw) {
		return nil, fmt.Errorf("detail roundtrip failed")
	}
	return blob, nil
}

func (s *Service) changedMetroDays() (map[string][]Aggregate, []string) {
	byDay, changed := map[string][]Aggregate{}, map[string]bool{}
	for date := range s.engine.DirtyDays {
		changed[date] = true
	}
	for _, a := range s.engine.Aggregates {
		byDay[a.Date] = append(byDay[a.Date], a)
		if a.KnownAt > s.lastCheckpoint.UnixNano() {
			changed[a.Date] = true
		}
	}
	dates := []string{}
	for date := range changed {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	return byDay, dates
}

func (s *Service) flushMetroAggregates(now time.Time) error {
	byDay, dates := s.changedMetroDays()
	for _, date := range dates {
		if err := s.flushMetroDay(date, byDay[date], now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) flushMetroDay(date string, rows []Aggregate, now time.Time) error {
	sort.Slice(rows, func(i, j int) bool { return digest(rows[i]) < digest(rows[j]) })
	blob, err := verifiedAggregateBlob(rows)
	if err != nil {
		return err
	}
	age, err := time.ParseInLocation("2006-01-02", date, lisbon)
	if err != nil {
		return err
	}
	b := block{Key: "metro:aggregate:" + date, Kind: "aggregate", Operator: "metro", Date: date, Age: age.UTC(), Closed: date < now.In(lisbon).Format("2006-01-02")}
	return s.publish(b, blob, now)
}

func verifiedAggregateBlob(rows []Aggregate) ([]byte, error) {
	var buf bytes.Buffer
	if err := parquet.Write(&buf, rows, parquet.Compression(&parquetZstd.Codec{}), parquet.MaxRowsPerRowGroup(1024)); err != nil {
		return nil, err
	}
	decoded, err := parquet.Read[Aggregate](bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || digest(decoded) != digest(rows) {
		return nil, fmt.Errorf("aggregate roundtrip failed")
	}
	return buf.Bytes(), nil
}

func (s *Service) encodeCheckpoint() ([]byte, error) {
	cp := checkpoint{s.providers, s.engine, s.hour, s.detail, s.config.BinSeconds, int(s.config.SampleInterval / time.Second), s.topology}
	raw, err := json.Marshal(cp)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBlockBytes {
		return nil, fmt.Errorf("archive checkpoint limit")
	}
	return s.encoder.EncodeAll(raw, nil), nil
}

func (s *Service) flushCheckpoint(now time.Time) error {
	blob, err := s.encodeCheckpoint()
	if err != nil {
		return err
	}
	// FIFO during reservation may retire training; serialize the admitted state again.
	if err = s.ensureSpace(generationReserve(blob), now); err != nil {
		return err
	}
	blob, err = s.encodeCheckpoint()
	if err != nil {
		return err
	}
	return s.publish(block{Key: "metro:state", Kind: "state", Operator: "metro", Age: now}, blob, now)
}

func (s *Service) acknowledgeFlush(now time.Time) {
	s.lastCheckpoint = now
	s.engine.DirtyDays = map[string]bool{}
	for _, p := range s.providers {
		p.Engine.DirtyDays = map[string]bool{}
	}
	s.status, s.message = "collecting", "Estimativas experimentais; sinais da mesma fonte, sem validação física."
}
