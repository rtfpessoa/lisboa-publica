package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"lisboapublica/internal/patterns"
)

func parseTestOptions(args ...string) (maintenanceOptions, error) {
	flags := flag.NewFlagSet("patterns-maintenance", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return parseMaintenanceOptions(flags, append([]string{"-archive", "/tmp/archive", "-corrections", "/tmp/corrections"}, args...))
}

func TestMaintenanceArchiveReserveFlags(t *testing.T) {
	defaults, err := parseTestOptions()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.config.MinFreeBytes != 0 || defaults.config.MinFreeInodes != 0 || defaults.config.MaintenanceInterval != time.Minute {
		t.Fatalf("unexpected defaults: %+v", defaults.config)
	}
	configured, err := parseTestOptions("-limit", "1000000000", "-min-free-bytes", "2147483648", "-min-free-inodes", "10000", "-maintenance-interval", "30s")
	if err != nil {
		t.Fatal(err)
	}
	if configured.config.LimitBytes != 1_000_000_000 || configured.config.MinFreeBytes != 2<<30 || configured.config.MinFreeInodes != 10000 || configured.config.MaintenanceInterval != 30*time.Second {
		t.Fatalf("settings not forwarded: %+v", configured.config)
	}
	for _, tc := range []struct{ name, value string }{
		{"-min-free-bytes", "-1"}, {"-min-free-inodes", "-1"},
		{"-min-free-bytes", "invalid"}, {"-min-free-inodes", "9223372036854775808"},
		{"-maintenance-interval", "9s"}, {"-maintenance-interval", "61m"},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			if _, err := parseTestOptions(tc.name, tc.value); err == nil {
				t.Fatal("invalid option accepted")
			}
		})
	}
}

func TestMaintenanceReservesBlockRealCorrection(t *testing.T) {
	directory := t.TempDir()
	config := patterns.DefaultConfig(directory)
	service, err := patterns.Open(config)
	if err != nil {
		t.Fatal(err)
	}
	zone, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	stops := []string{"A", "B", "C", "D", "E", "F"}
	topology := patterns.Topology{Profile: "synthetic", Patterns: []patterns.Pattern{{Route: "line", Direction: "d", Destination: "F", Stops: stops, Sequences: []int{1, 2, 3, 4, 5, 6}}}}
	var original patterns.Row
	var received time.Time
	for i := 0; i < 6; i++ {
		at := base.Add(time.Duration(i) * 30 * time.Second)
		rows := make([]patterns.Row, 0, len(stops))
		for j, stop := range stops {
			seconds := 600
			if i >= 2*j+1 {
				seconds = 0
			}
			row := patterns.Row{Stop: stop, Platform: "1", Destination: "d", Clock: at.In(zone).Format("20060102150405"), Train: "x", ETA: json.RawMessage(strconv.Itoa(seconds))}
			rows = append(rows, row)
			if i == 3 && j == 1 {
				original, received = row, at.Add(time.Second)
			}
		}
		if err = service.Record(patterns.Receipt{ReceivedAt: at.Add(time.Second), Rows: rows, ServiceCondition: "reported_normal"}, topology); err != nil {
			t.Fatal(err)
		}
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	changed := original
	changed.ETA = json.RawMessage("300")
	changes, err := json.Marshal([]patterns.Correction{{ReceivedAt: received, ExpectedHash: fmt.Sprintf("%x", hash), Row: changed, Evidence: "synthetic source revision"}})
	if err != nil {
		t.Fatal(err)
	}
	correctionFile := filepath.Join(t.TempDir(), "corrections.json")
	if err = os.WriteFile(correctionFile, changes, 0600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "manifest.json")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"-min-free-bytes", "-min-free-inodes"} {
		flags := flag.NewFlagSet("patterns-maintenance", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		options, err := parseMaintenanceOptions(flags, []string{"-archive", directory, "-corrections", correctionFile, setting, strconv.FormatInt(math.MaxInt64, 10)})
		if err != nil {
			t.Fatal(err)
		}
		if err = maintainArchive(options); err == nil || !strings.Contains(err.Error(), "budget exhausted") {
			t.Fatalf("%s did not block correction on the actual filesystem: %v", setting, err)
		}
		after, err := os.ReadFile(manifestPath)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("blocked correction changed the manifest: %v", err)
		}
	}
	flags := flag.NewFlagSet("patterns-maintenance", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options, err := parseMaintenanceOptions(flags, []string{"-archive", directory, "-corrections", correctionFile})
	if err != nil {
		t.Fatal(err)
	}
	if err = maintainArchive(options); err != nil {
		t.Fatalf("control correction with sufficient headroom failed: %v", err)
	}
}
