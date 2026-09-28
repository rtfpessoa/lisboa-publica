// metro-calibrate assesses explicitly collected offline evidence. It makes no network calls.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"lisboapublica/internal/patterns"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	input := flag.String("input", "", "offline collection JSON (maximum 8 MiB)")
	flag.Parse()
	if *input == "" {
		return fmt.Errorf("-input is required")
	}
	raw, dataset, err := readDataset(*input)
	if err != nil {
		return err
	}
	result, err := patterns.AssessMetroCalibration(dataset)
	if err != nil {
		return err
	}
	return writeAssessment(raw, dataset, result)
}
func readDataset(input string) ([]byte, patterns.MetroCalibrationDataset, error) {
	var dataset patterns.MetroCalibrationDataset
	raw, err := readInput(input)
	if err != nil {
		return nil, dataset, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&dataset); err != nil {
		return nil, dataset, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		err = fmt.Errorf("input must contain one JSON document")
	} else {
		err = nil
	}
	return raw, dataset, err
}
func readInput(input string) ([]byte, error) {
	f, err := os.Open(input)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if err == nil && len(raw) > 8*1024*1024 {
		err = fmt.Errorf("input exceeds 8 MiB")
	}
	return raw, err
}
func writeAssessment(raw []byte, dataset patterns.MetroCalibrationDataset, result patterns.MetroCalibrationAssessment) error {
	sum := sha256.Sum256(raw)
	report := struct {
		Tool                 string                              `json:"tool"`
		InputSHA256          string                              `json:"input_sha256"`
		SourceProvenance     string                              `json:"source_provenance"`
		ResolutionProvenance string                              `json:"resolution_provenance"`
		ModelProvenance      string                              `json:"model_provenance,omitempty"`
		Assessment           patterns.MetroCalibrationAssessment `json:"assessment"`
	}{"metro-calibrate-v2", hex.EncodeToString(sum[:]), dataset.SourceProvenance, dataset.ResolutionProvenance, dataset.ModelProvenance, result}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
