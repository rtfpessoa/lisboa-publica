// Command patterns-maintenance applies evidence-backed corrections during an
// exclusive maintenance window. It never fetches provider data or emits raw records.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"lisboapublica/internal/patterns"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	options, err := maintenanceOptionsFromFlags()
	if err != nil {
		return err
	}
	return maintainArchive(options)
}

func maintainArchive(options maintenanceOptions) error {
	payload, err := readCorrectionPayload(options.corrections)
	if err != nil {
		return err
	}
	service, err := patterns.Open(options.config)
	if err != nil {
		return err
	}
	defer service.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := reviseArchive(ctx, service, options.operator, payload)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func readCorrectionPayload(path string) (json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := validateCorrectionFile(f); err != nil {
		return nil, err
	}
	return decodeCorrectionPayload(f)
}

func decodeCorrectionPayload(f io.Reader) (json.RawMessage, error) {
	var payload json.RawMessage
	decoder := json.NewDecoder(io.LimitReader(f, (8<<20)+1))
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing correction data")
	}
	return payload, nil
}

func reviseArchive(ctx context.Context, service *patterns.Service, operator string, payload json.RawMessage) (patterns.ReprocessResult, error) {
	var err error
	var result patterns.ReprocessResult
	if operator == "metro" {
		var changes []patterns.Correction
		if err = json.Unmarshal(payload, &changes); err != nil {
			return result, err
		}
		result, err = service.Reprocess(ctx, changes, time.Now().UTC())
	} else {
		var changes []patterns.ProviderCorrection
		if err = json.Unmarshal(payload, &changes); err != nil {
			return result, err
		}
		result, err = service.ReprocessProvider(ctx, operator, changes, time.Now().UTC())
	}
	return result, err
}

type maintenanceOptions struct {
	operator, corrections string
	config                patterns.Config
}

func maintenanceOptionsFromFlags() (maintenanceOptions, error) {
	operator := flag.String("operator", "metro", "operator whose retained inputs are revised")
	directory := flag.String("archive", "", "server archive directory")
	corrections := flag.String("corrections", "", "private source correction JSON file")
	config := maintenanceConfigFlags()
	config.Directory = *directory
	if *directory == "" || *corrections == "" {
		return maintenanceOptions{}, fmt.Errorf("archive and corrections are required")
	}
	return maintenanceOptions{*operator, *corrections, config}, nil
}

func validateCorrectionFile(f *os.File) error {
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	if stat.Size() > 8<<20 {
		return fmt.Errorf("correction file limit")
	}
	return nil
}

func maintenanceConfigFlags() patterns.Config {
	config := patterns.DefaultConfig("pending")
	flag.DurationVar(&config.SampleInterval, "sample", config.SampleInterval, "configured detail cadence")
	flag.IntVar(&config.BinSeconds, "bin", config.BinSeconds, "configured histogram width in seconds")
	flag.Int64Var(&config.LimitBytes, "limit", config.LimitBytes, "allocated archive byte budget")
	flag.IntVar(&config.DetailDays, "detail-days", config.DetailDays, "configured detail retention")
	flag.IntVar(&config.AggregateMonths, "aggregate-months", config.AggregateMonths, "configured aggregate retention")
	flag.IntVar(&config.TrainingDays, "training-days", config.TrainingDays, "configured training window")
	flag.IntVar(&config.CalibrationDays, "calibration-days", config.CalibrationDays, "configured calibration window")
	flag.DurationVar(&config.CheckpointInterval, "checkpoint", config.CheckpointInterval, "configured checkpoint cadence")
	flag.DurationVar(&config.EvaluationInterval, "evaluation", config.EvaluationInterval, "configured evaluation cadence")
	flag.Parse()
	return config
}
