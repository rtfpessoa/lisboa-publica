package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/patterns"
)

func clearArchiveEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"TRANSPORT_ARCHIVE_LIMIT_BYTES", "TRANSPORT_ARCHIVE_MIN_FREE_BYTES", "TRANSPORT_ARCHIVE_MIN_FREE_INODES", "TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS",
		"TRANSPORT_DETAIL_DAYS", "TRANSPORT_AGGREGATE_MONTHS", "TRANSPORT_TRAINING_DAYS", "TRANSPORT_CALIBRATION_DAYS", "TRANSPORT_BIN_SECONDS",
		"TRANSPORT_SAMPLE_SECONDS", "TRANSPORT_CHECKPOINT_SECONDS", "TRANSPORT_EVALUATION_SECONDS",
	} {
		t.Setenv(name, "")
	}
}

func TestPatternArchiveEnvironment(t *testing.T) {
	t.Run("compatible defaults", func(t *testing.T) {
		clearArchiveEnvironment(t)
		config := patterns.DefaultConfig(t.TempDir())
		if err := patternEnvironment(&config); err != nil {
			t.Fatal(err)
		}
		if config.LimitBytes != 10_000_000_000 || config.MinFreeBytes != 0 || config.MinFreeInodes != 0 || config.MaintenanceInterval != time.Minute {
			t.Fatalf("unexpected defaults: %+v", config)
		}
	})
	t.Run("operator settings", func(t *testing.T) {
		clearArchiveEnvironment(t)
		t.Setenv("TRANSPORT_ARCHIVE_LIMIT_BYTES", "1000000000")
		t.Setenv("TRANSPORT_ARCHIVE_MIN_FREE_BYTES", "2147483648")
		t.Setenv("TRANSPORT_ARCHIVE_MIN_FREE_INODES", "10000")
		t.Setenv("TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS", "30")
		config := patterns.DefaultConfig(t.TempDir())
		if err := patternEnvironment(&config); err != nil {
			t.Fatal(err)
		}
		if err := config.Validate(); err != nil {
			t.Fatal(err)
		}
		if config.LimitBytes != 1_000_000_000 || config.MinFreeBytes != 2<<30 || config.MinFreeInodes != 10000 || config.MaintenanceInterval != 30*time.Second {
			t.Fatalf("settings not forwarded: %+v", config)
		}
	})
	for _, value := range []string{"10", "3600"} {
		t.Run("maintenance boundary "+value, func(t *testing.T) {
			clearArchiveEnvironment(t)
			t.Setenv("TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS", value)
			config := patterns.DefaultConfig(t.TempDir())
			if err := patternEnvironment(&config); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct{ name, value string }{
		{"TRANSPORT_ARCHIVE_LIMIT_BYTES", "1048575"},
		{"TRANSPORT_ARCHIVE_MIN_FREE_BYTES", "-1"},
		{"TRANSPORT_ARCHIVE_MIN_FREE_BYTES", "9223372036854775808"},
		{"TRANSPORT_ARCHIVE_MIN_FREE_INODES", "not-a-number"},
		{"TRANSPORT_ARCHIVE_MIN_FREE_INODES", "-1"},
		{"TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS", "0"},
		{"TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS", "9"},
		{"TRANSPORT_ARCHIVE_MAINTENANCE_SECONDS", "3601"},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			clearArchiveEnvironment(t)
			t.Setenv(tc.name, tc.value)
			config := patterns.DefaultConfig(t.TempDir())
			if err := patternEnvironment(&config); err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("expected named configuration error: %v", err)
			}
		})
	}
}

func TestConfiguredArchiveReserveStopsWritesWhenUnavailable(t *testing.T) {
	clearArchiveEnvironment(t)
	t.Setenv("TRANSPORT_ARCHIVE_DIR", t.TempDir())
	t.Setenv("TRANSPORT_ARCHIVE_OPERATORS", "metro")
	t.Setenv("TRANSPORT_ARCHIVE_MIN_FREE_BYTES", "9223372036854775807")
	service, err := configurePatterns()
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	view, err := service.View(context.Background(), "")
	if err != nil || view.Status != "paused" {
		t.Fatalf("filesystem reserve was not enforced: status=%s error=%v", view.Status, err)
	}
	if err = service.Record(patterns.Receipt{ReceivedAt: time.Now().UTC()}, patterns.Topology{}); err == nil {
		t.Fatal("archive admitted a receipt without filesystem headroom")
	}
}
