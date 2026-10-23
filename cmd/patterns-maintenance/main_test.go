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
	if err = service.ConfigureOperators([]string{"metro", "cm"}); err != nil {
		t.Fatal(err)
	}
	received := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	path := patterns.ProviderJourney{Route: "cm:route", Direction: "0", Plan: "plan", Pattern: "published", Headsign: "B", Visits: []patterns.ProviderVisit{
		{Stop: "cm:A", Name: "A", Sequence: 1, Lat: 38.7, Lon: -9.1},
		{Stop: "cm:B", Name: "B", Sequence: 2, Lat: 38.701, Lon: -9.1},
	}}
	pathID := patterns.ProviderJourneyID(path)
	original := patterns.Observation{ID: "cm:vehicle", SourceID: "vehicle", SourceURL: "https://synthetic.invalid/positions", Journey: pathID, ObservedAt: received, Route: path.Route, Trip: "cm:trip", Plan: path.Plan, Pattern: path.Pattern, OperationalDate: received.Format("2006-01-02"), Stop: "cm:A", Status: "INCOMING_AT", PositionKind: "reported", Lat: 38.7, Lon: -9.1}
	receipt := patterns.ProviderReceipt{Operator: "cm", ReceivedAt: received, Journeys: map[string]patterns.ProviderJourney{pathID: path}, Rows: []patterns.Observation{original}}
	if err = service.RecordProvider(receipt); err != nil {
		t.Fatal(err)
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
	changed.PositionKind = "estimated"
	changes, err := json.Marshal([]patterns.ProviderCorrection{{ReceivedAt: received, ExpectedHash: fmt.Sprintf("%x", hash), Row: changed, Evidence: "synthetic source revision"}})
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
		options, err := parseMaintenanceOptions(flags, []string{"-archive", directory, "-corrections", correctionFile, "-operator", "cm", setting, strconv.FormatInt(math.MaxInt64, 10)})
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
	options, err := parseMaintenanceOptions(flags, []string{"-archive", directory, "-corrections", correctionFile, "-operator", "cm"})
	if err != nil {
		t.Fatal(err)
	}
	if err = maintainArchive(options); err != nil {
		t.Fatalf("control correction with sufficient headroom failed: %v", err)
	}
}

func TestMetroMaintenanceRejectsUnsequencedCorrections(t *testing.T) {
	directory := t.TempDir()
	service, err := patterns.Open(patterns.DefaultConfig(directory))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	row := patterns.Row{Stop: "A", Train: "synthetic", ETA: json.RawMessage("60")}
	if err = service.Record(patterns.Receipt{ReceivedAt: now, Rows: []patterns.Row{row}}, patterns.Topology{Profile: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	changes, err := json.Marshal([]patterns.Correction{{ReceivedAt: now, ExpectedHash: fmt.Sprintf("%x", hash), Row: row, Evidence: "synthetic source revision"}})
	if err != nil {
		t.Fatal(err)
	}
	corrections := filepath.Join(t.TempDir(), "corrections.json")
	if err = os.WriteFile(corrections, changes, 0600); err != nil {
		t.Fatal(err)
	}
	before := maintenanceArchiveBytes(t, directory)
	flags := flag.NewFlagSet("patterns-maintenance", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options, err := parseMaintenanceOptions(flags, []string{"-archive", directory, "-corrections", corrections})
	if err != nil {
		t.Fatal(err)
	}
	if err = maintainArchive(options); err == nil || !strings.Contains(err.Error(), "retained input order and restart boundaries cannot be verified") {
		t.Fatalf("unsequenced Metro correction was not rejected: %v", err)
	}
	after := maintenanceArchiveBytes(t, directory)
	if len(before) != len(after) {
		t.Fatal("rejected Metro correction changed the archive generation set")
	}
	for name, blob := range before {
		if !bytes.Equal(blob, after[name]) {
			t.Fatalf("rejected Metro correction changed %s", name)
		}
	}
}

func TestMetroMaintenanceValidatesInputBeforeOpeningArchive(t *testing.T) {
	for _, tc := range []struct {
		name, payload, want string
	}{
		{"not_array", `{}`, "cannot unmarshal"},
		{"null", `null`, "invalid reprocessing request"},
		{"empty", `[]`, "invalid reprocessing request"},
		{"too_many", `[` + strings.Repeat(`{},`, 1000) + `{}` + `]`, "invalid reprocessing request"},
		{"valid_count", `[{}]`, "retained input order and restart boundaries cannot be verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "archive")
			corrections := filepath.Join(root, "corrections.json")
			if err := os.WriteFile(corrections, []byte(tc.payload), 0600); err != nil {
				t.Fatal(err)
			}
			options := maintenanceOptions{operator: "metro", corrections: corrections, config: patterns.DefaultConfig(directory)}
			if err := maintainArchive(options); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected validation result: %v", err)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatalf("validation opened or created the archive: %v", err)
			}
		})
	}
}

func maintenanceArchiveBytes(t *testing.T, directory string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string][]byte{}
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = blob
	}
	return result
}
