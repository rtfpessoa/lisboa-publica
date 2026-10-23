package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"lisboapublica/internal/patterns"
)

func configurePatterns() (*patterns.Service, error) {
	directory := env("TRANSPORT_ARCHIVE_DIR", "")
	if directory == "" {
		return nil, nil
	}
	config := patterns.DefaultConfig(directory)
	if err := patternEnvironment(&config); err != nil {
		return nil, err
	}
	return openPatternStages(config)
}

func openPatternStages(config patterns.Config) (*patterns.Service, error) {
	operators := strings.Split(env("TRANSPORT_ARCHIVE_OPERATORS", "metro"), ",")
	if err := patterns.ValidateOperatorStages(operators); err != nil {
		return nil, err
	}
	service, err := patterns.Open(config)
	if err != nil {
		return nil, err
	}
	if err = service.ConfigureOperators(operators); err != nil {
		_ = service.Close()
		return nil, err
	}
	return service, nil
}

func patternEnvironment(config *patterns.Config) error {
	if err := patternLimits(config); err != nil {
		return err
	}
	if err := patternIntegers(config); err != nil {
		return err
	}
	if err := patternDurations(config); err != nil {
		return err
	}
	seconds, err := strconv.Atoi(env("TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS", "60"))
	if err != nil || seconds < 10 || seconds > 3600 {
		return fmt.Errorf("invalid TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS")
	}
	config.MaintenanceInterval = time.Duration(seconds) * time.Second
	return nil
}

func patternLimits(config *patterns.Config) error {
	limits := []struct {
		name   string
		target *int64
		min    int64
	}{
		{"TRANSPORT_ARCHIVE_LIMIT_BYTES", &config.LimitBytes, 1 << 20},
		{"TRANSPORT_ARCHIVE_MIN_FREE_BYTES", &config.MinFreeBytes, 0},
		{"TRANSPORT_ARCHIVE_MIN_FREE_INODES", &config.MinFreeInodes, 0},
	}
	for _, setting := range limits {
		value, err := strconv.ParseInt(env(setting.name, strconv.FormatInt(*setting.target, 10)), 10, 64)
		if err != nil || value < setting.min {
			return fmt.Errorf("invalid %s", setting.name)
		}
		*setting.target = value
	}
	return nil
}

func patternIntegers(config *patterns.Config) error {
	integers := []struct {
		name   string
		target *int
	}{
		{"TRANSPORT_DETAIL_DAYS", &config.DetailDays}, {"TRANSPORT_AGGREGATE_MONTHS", &config.AggregateMonths},
		{"TRANSPORT_TRAINING_DAYS", &config.TrainingDays}, {"TRANSPORT_CALIBRATION_DAYS", &config.CalibrationDays},
		{"TRANSPORT_BIN_SECONDS", &config.BinSeconds},
	}
	for _, setting := range integers {
		value, err := strconv.Atoi(env(setting.name, strconv.Itoa(*setting.target)))
		if err != nil {
			return fmt.Errorf("invalid %s", setting.name)
		}
		*setting.target = value
	}
	return nil
}

func patternDurations(config *patterns.Config) error {
	durations := []struct {
		name   string
		target *time.Duration
	}{
		{"TRANSPORT_SAMPLE_SECONDS", &config.SampleInterval}, {"TRANSPORT_CHECKPOINT_SECONDS", &config.CheckpointInterval}, {"TRANSPORT_EVALUATION_SECONDS", &config.EvaluationInterval},
	}
	for _, setting := range durations {
		seconds, err := strconv.Atoi(env(setting.name, strconv.Itoa(int(*setting.target/time.Second))))
		if err != nil || seconds < 1 || seconds > 3600 {
			return fmt.Errorf("invalid %s", setting.name)
		}
		*setting.target = time.Duration(seconds) * time.Second
	}
	return nil
}
