package patterns

import (
	"bytes"
	"context"
	"fmt"
	"github.com/parquet-go/parquet-go"
	"io"
)

func (s *Service) patternView(ctx context.Context, v View, stop string) (View, error) {
	summary := stationPatternSummary{view: v, stop: stop, groups: map[string]*hourPatternSummary{}, collected: map[string]bool{}}
	for _, b := range s.index.Blocks {
		if err := ctx.Err(); err != nil {
			return summary.view, err
		}
		if b.Kind != "aggregate" || v.Operator != "" && b.Operator != v.Operator {
			continue
		}
		if err := s.readPatternBlock(ctx, b, &summary); err != nil {
			return summary.view, err
		}
	}
	return summary.finish()
}

func (s *Service) readPatternBlock(ctx context.Context, b block, summary *stationPatternSummary) error {
	reader, err := s.openPatternReader(b, summary)
	if reader == nil || err != nil {
		return err
	}
	err = summary.readRows(ctx, reader)
	closed := reader.Close()
	if err != nil {
		return err
	}
	return closed
}

func (s *Service) openPatternReader(b block, summary *stationPatternSummary) (*parquet.GenericReader[Aggregate], error) {
	blob, err := s.readBlock(b)
	if err != nil {
		summary.view.Status = "degraded"
		summary.view.Message = "Alguns blocos históricos estão indisponíveis; cobertura parcial."
		return nil, nil
	}
	file, err := parquet.OpenFile(bytes.NewReader(blob), int64(len(blob)))
	if err != nil || file.NumRows() > maxEngineAggregates {
		summary.view.Status = "degraded"
		return nil, nil
	}
	return parquet.NewGenericReader[Aggregate](file), nil
}

func (s *stationPatternSummary) readRows(ctx context.Context, reader *parquet.GenericReader[Aggregate]) error {
	batch := make([]Aggregate, 128)
	for {
		done, err := s.readBatch(ctx, reader, batch)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}
	return nil
}

func (s *stationPatternSummary) readBatch(ctx context.Context, reader *parquet.GenericReader[Aggregate], batch []Aggregate) (bool, error) {
	if err := ctx.Err(); err != nil {
		return true, err
	}
	n, err := reader.Read(batch)
	for _, a := range batch[:n] {
		if len(s.groups) > 5000 || len(s.reports.groups) > 5000 {
			return true, fmt.Errorf("station summary result limit")
		}
		s.add(a)
	}
	done := err == io.EOF
	if done {
		err = nil
	}
	return done, err
}
