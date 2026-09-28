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

func (j *metroRevision) prepareBlocks() error {
	for date := range j.days {
		if err := j.prepareDay(date); err != nil {
			return err
		}
	}
	ledger, err := j.service.prepareRevisionLedger("metro", j.changes, j.now, false)
	if err != nil {
		return err
	}
	j.prepared = append(j.prepared, ledger)
	cp := checkpoint{j.service.providers, j.replacement, j.service.hour, j.service.detail, j.service.config.BinSeconds, int(j.service.config.SampleInterval / time.Second), j.service.topology}
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	j.prepared = append(j.prepared, preparedBlock{block{Key: "metro:state", Kind: "state", Operator: "metro", Age: j.now}, j.service.encoder.EncodeAll(raw, nil)})
	return nil
}

func aggregateDayRows(aggregates map[string]Aggregate, date string) []Aggregate {
	rows := []Aggregate{}
	for _, a := range aggregates {
		if a.Date == date {
			rows = append(rows, a)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return digest(rows[i]) < digest(rows[j]) })
	return rows
}

func revisionAggregateBlob(rows []Aggregate) ([]byte, error) {
	var buffer bytes.Buffer
	if err := parquet.Write(&buffer, rows, parquet.Compression(&parquetZstd.Codec{})); err != nil {
		return nil, err
	}
	decoded, err := parquet.Read[Aggregate](bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil || digest(decoded) != digest(rows) {
		return nil, fmt.Errorf("reprocessed aggregate roundtrip")
	}
	return buffer.Bytes(), nil
}

func (j *metroRevision) prepareDay(date string) error {
	blob, err := revisionAggregateBlob(aggregateDayRows(j.replacement.Aggregates, date))
	if err != nil {
		return err
	}
	age, err := time.ParseInLocation("2006-01-02", date, lisbon)
	if err != nil {
		return err
	}
	b := block{Key: "metro:aggregate:" + date, Kind: "aggregate", Operator: "metro", Date: date, Age: age.UTC(), Closed: date < j.now.In(lisbon).Format("2006-01-02")}
	j.prepared = append(j.prepared, preparedBlock{b, blob})
	return nil
}

func (s *Service) prepareRevisionLedger(operator string, changes any, now time.Time, asOf bool) (preparedBlock, error) {
	raw, err := json.Marshal(changes)
	b := block{Key: operator + ":correction:" + digest(changes), Kind: "correction", Operator: operator, Age: now.UTC().Truncate(time.Hour)}
	if asOf {
		b.AsOf = now
	}
	if err != nil {
		return preparedBlock{}, err
	}
	return preparedBlock{b, s.encoder.EncodeAll(raw, nil)}, nil
}

func (j *metroRevision) publish() error {
	if err := j.service.publishTransaction(j.context, j.prepared, j.sourceFiles, j.now); err != nil {
		return err
	}
	j.service.engine, j.service.lastCheckpoint = j.replacement, j.now
	j.service.engine.DirtyDays = map[string]bool{}
	return nil
}
