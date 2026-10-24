// Command patterns-recover verifies and repairs one unsequenced Metro tail in
// an exclusively owned, offline archive. It never fetches provider data.
package main

import (
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
	archive := flag.String("archive", "", "absolute offline archive directory")
	requestPath := flag.String("request", "", "private JSON recovery request")
	flag.Parse()
	if *archive == "" || *requestPath == "" || flag.NArg() != 0 {
		return fmt.Errorf("archive and request are required")
	}
	request, err := readRequest(*requestPath)
	if err != nil {
		return err
	}
	result, err := patterns.RecoverMetroTail(*archive, request)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func readRequest(path string) (patterns.MetroTailRecoveryRequest, error) {
	var request patterns.MetroTailRecoveryRequest
	f, err := os.Open(path)
	if err != nil {
		return request, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return request, fmt.Errorf("invalid recovery request file")
	}
	decoder := json.NewDecoder(io.LimitReader(f, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("invalid recovery request")
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return request, fmt.Errorf("unexpected recovery request data")
	}
	return request, nil
}
