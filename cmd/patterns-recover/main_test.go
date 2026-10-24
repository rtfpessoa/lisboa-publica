package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRequestRejectsUnexpectedInput(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"object", `{"checkpoint_rows":99}`, true},
		{"unknown field", `{"unexpected":true}`, false},
		{"trailing value", `{} {}`, false},
		{"oversized", `{}` + strings.Repeat(" ", 64<<10), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			request, err := readRequest(path)
			if (err == nil) != tc.valid {
				t.Fatalf("request error: %v", err)
			}
			if tc.valid && request.CheckpointRows != 99 {
				t.Fatal("request data lost")
			}
		})
	}
}
