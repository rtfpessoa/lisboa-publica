package patterns

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStreamTransactionPublishesMoreThanWorkingSetLimit(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	items := make([]transactionItem, 2)
	for i := range items {
		value := byte(i + 1)
		items[i] = transactionItem{
			block:  block{Key: fmt.Sprintf("cm:correction:large:%d", i), Kind: "correction", Operator: "cm", Age: now},
			encode: func() ([]byte, error) { return bytes.Repeat([]byte{value}, 33<<20), nil },
		}
	}
	job := streamTransaction{service: s, context: context.Background(), items: items, now: now}
	if err := job.publish(); err != nil {
		t.Fatal(err)
	}
	if len(s.index.Blocks) != 2 {
		t.Fatalf("published %d generations", len(s.index.Blocks))
	}
	for _, b := range s.index.Blocks {
		if _, err := s.readBlock(b); err != nil {
			t.Fatal("published generation is unreadable", err)
		}
	}
}

func TestStreamTransactionRejectsChangedPayloadAndRollsBack(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	first := transactionItem{block: block{Key: "cm:correction:first", Kind: "correction", Operator: "cm", Age: now}, encode: func() ([]byte, error) { return []byte("first"), nil }}
	calls := 0
	second := transactionItem{block: block{Key: "cm:correction:second", Kind: "correction", Operator: "cm", Age: now}, encode: func() ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("original"), nil
		}
		return []byte("changed"), nil
	}}
	job := streamTransaction{service: s, context: context.Background(), items: []transactionItem{first, second}, now: now}
	if err := job.publish(); err == nil {
		t.Fatal("published a changed payload")
	}
	if len(s.index.Blocks) != 0 {
		t.Fatal("manifest contains a partial transaction")
	}
	entries, err := os.ReadDir(config.Directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".lock" && e.Name() != "manifest.json" {
			t.Fatal("staged generation was not rolled back", e.Name())
		}
	}
}

func TestStreamTransactionRejectsChangedStateAfterAdmission(t *testing.T) {
	s, err := Open(DefaultConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	calls := 0
	job := streamTransaction{service: s, context: context.Background(), now: now,
		items: []transactionItem{{block: block{Key: "cm:correction:test", Kind: "correction", Operator: "cm", Age: now}, encode: func() ([]byte, error) { return []byte("expected"), nil }}},
		validate: func() error {
			calls++
			if calls > 1 {
				return errors.New("state changed")
			}
			return nil
		},
	}
	if err := job.publish(); err == nil {
		t.Fatal("accepted stale state")
	}
	if len(s.index.Blocks) != 0 {
		t.Fatal("manifest changed")
	}
}

func TestStreamTransactionUncertainManifestPreservesGenerations(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	value := block{Key: "cm:correction:test", Kind: "correction", Operator: "cm", Age: now}
	if err := s.publish(value, []byte("old"), now); err != nil {
		t.Fatal(err)
	}
	prior := s.index.Blocks[0]
	job := streamTransaction{service: s, context: context.Background(), now: now,
		items: []transactionItem{{block: value, encode: func() ([]byte, error) { return []byte("new"), nil }}},
	}
	s.manifestSync = func(string) error { return errors.New("uncertain") }
	if err := job.publish(); err == nil || !s.manifestUncertain {
		t.Fatal("uncertain manifest accepted", err)
	}
	if _, err := os.Stat(filepath.Join(config.Directory, prior.File)); err != nil {
		t.Fatal("old generation lost", err)
	}
	if _, err := s.readBlock(s.index.Blocks[0]); err != nil {
		t.Fatal("new generation lost", err)
	}
}
