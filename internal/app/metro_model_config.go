package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"lisboapublica/internal/patterns"
)

// ConfigureMetroModels installs only reviewed, reproducible original-input
// replay configurations before ingestion starts. Empty means unavailable.
func (c *Cache) ConfigureMetroModels(path string) error {
	if path == "" {
		return nil
	}
	raw, err := readMetroModelFile(path)
	if err != nil {
		return err
	}
	return c.installMetroModels(raw)
}
func readMetroModelFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err == nil && len(raw) > 8<<20 {
		err = fmt.Errorf("Metro model allowlist exceeds 8 MiB")
	}
	return raw, err
}
func decodeMetroModels(raw []byte) ([]patterns.MetroModelAdmission, error) {
	var entries []patterns.MetroModelAdmission
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(&entries)
	if err == nil {
		err = checkMetroModelDocument(dec, len(entries))
	}
	return entries, err
}
func checkMetroModelDocument(dec *json.Decoder, count int) error {
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("allowlist must contain one JSON document")
	}
	if count > 32 {
		return fmt.Errorf("Metro model allowlist exceeds 32 configurations")
	}
	return nil
}
func (c *Cache) installMetroModels(raw []byte) error {
	entries, err := decodeMetroModels(raw)
	if err != nil {
		return err
	}
	models, err := qualifyMetroModels(entries)
	if err == nil {
		c.metroRuntime.mu.Lock()
		c.metroRuntime.models = models
		c.metroRuntime.mu.Unlock()
	}
	return err
}
func qualifyMetroModels(entries []patterns.MetroModelAdmission) (map[string]patterns.MetroQualifiedModel, error) {
	models := map[string]patterns.MetroQualifiedModel{}
	for _, entry := range entries {
		qualified, err := patterns.QualifyMetroModel(entry)
		if err != nil {
			return nil, fmt.Errorf("Metro configuration %s/%s: %w", entry.Profile, entry.Direction, err)
		}
		key := entry.Profile + "|" + entry.Direction
		if _, exists := models[key]; exists {
			return nil, fmt.Errorf("duplicate Metro model binding")
		}
		models[key] = qualified
	}
	return models, nil
}
