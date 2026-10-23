package patterns

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMetroEventExpiryRejectsWholeBatchBeforePublication(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.maintenanceOnce.Do(func() { close(s.maintenanceStop) })
	<-s.maintenanceDone
	now := time.Now().UTC().Truncate(time.Second)
	cutoff := now.AddDate(0, 0, -7)
	makeEvent := func(id string, at time.Time) MetroEventRecord {
		return MetroEventRecord{ID: id, Journey: "journey", At: at, Payload: json.RawMessage(`{"proof":true}`)}
	}
	prior := makeEvent("prior", now)
	if err = s.RecordMetroEvents([]MetroEventRecord{prior}, now); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(config.Directory, "manifest.json")
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeFiles := eventFixtureFiles(t, config.Directory)
	expired := makeEvent("expired", cutoff.Add(-time.Nanosecond))
	boundary := makeEvent("boundary", cutoff)
	recent := makeEvent("recent", now.Add(-time.Hour))
	for _, batch := range [][]MetroEventRecord{
		{expired},
		{expired, makeEvent("also-expired", cutoff.Add(-time.Second))},
		{boundary, recent, expired},
		{expired, boundary, recent},
	} {
		if err = s.RecordMetroEvents(batch, now); !errors.Is(err, ErrMetroEventExpired) {
			t.Fatalf("expired batch was not explicitly rejected: %v", err)
		}
		afterManifest, readErr := os.ReadFile(manifestPath)
		if readErr != nil || string(afterManifest) != string(beforeManifest) || !reflect.DeepEqual(eventFixtureFiles(t, config.Directory), beforeFiles) {
			t.Fatal("expired batch changed archive files or manifest", readErr)
		}
	}
	retained, err := s.MetroJourneyEvents(context.Background(), "journey", now)
	if err != nil || len(retained) != 1 || retained[0].ID != prior.ID {
		t.Fatalf("expired batch partially published: %+v %v", retained, err)
	}
	if err = s.RecordMetroEvents([]MetroEventRecord{boundary, recent}, now); err != nil {
		t.Fatalf("seven-day boundary was rejected: %v", err)
	}
	retained, err = s.MetroJourneyEvents(context.Background(), "journey", now)
	seen := map[string]bool{}
	for _, event := range retained {
		seen[event.ID] = true
	}
	if err != nil || len(retained) != 3 || !seen[boundary.ID] || !seen[recent.ID] || !seen[prior.ID] {
		t.Fatalf("exact-boundary proof was not retained: %+v %v", retained, err)
	}
}

func eventFixtureFiles(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		files = append(files, entry.Name())
	}
	return files
}
