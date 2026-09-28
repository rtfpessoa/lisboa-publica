package patterns

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInvalidManifestOpenNeverChangesArchive(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "manifest.json")
	original := []byte("{invalid-json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(DefaultConfig(directory)); err == nil {
		s.Close()
		t.Fatal("accepted invalid manifest")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("failed initialization overwrote existing manifest")
	}
}
