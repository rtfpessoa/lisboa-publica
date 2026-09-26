package app

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"errors"

	"fmt"
	"io"

	"strings"
)

var errGTFSIntegrity = errors.New("GTFS archive integrity failure")

var errGTFSResource = errors.New("GTFS resource limit")

type gtfsArchive map[string]*zip.File

func openGTFS(blob []byte) (gtfsArchive, error) {
	archive, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		return nil, err
	}
	files, err := indexGTFS(archive.File)
	if err != nil {
		return nil, err
	}
	return files, files.validateRequired()
}

func indexGTFS(entries []*zip.File) (gtfsArchive, error) {
	if len(entries) > maxGTFSEntries {
		return nil, fmt.Errorf("GTFS entry limit")
	}
	files := map[string]*zip.File{}
	var size uint64
	for _, f := range entries {
		if f.UncompressedSize64 > maxGTFSExpandedBytes-size {
			return nil, fmt.Errorf("GTFS expanded archive exceeds512MiB")
		}
		size += f.UncompressedSize64
		n := strings.TrimPrefix(f.Name, "./")
		if strings.Contains(n, "/") {
			continue
		}
		if files[n] != nil {
			return nil, fmt.Errorf("duplicate GTFS entry %s", n)
		}
		files[n] = f
	}
	return gtfsArchive(files), nil
}

func (files gtfsArchive) validateRequired() error {
	for _, name := range []string{"routes.txt", "stops.txt", "trips.txt", "stop_times.txt"} {
		if files[name] == nil {
			return fmt.Errorf("missing %s", name)
		}
	}
	if files["calendar.txt"] == nil && files["calendar_dates.txt"] == nil {
		return fmt.Errorf("missing GTFS service calendars")
	}
	return nil
}
func (files gtfsArchive) read(name string, visit func(map[string]string) error) error {
	f := files[name]
	if f == nil {
		return nil
	}
	rd, err := f.Open()
	if err == nil {
		defer rd.Close()
		err = readGTFSTable(io.LimitReader(rd, maxGTFSExpandedBytes), name, visit)
		// Optional-table syntax failure must not hide a corrupt ZIP stream.
		if err != nil {
			if integrity := verifyGTFSStream(f); integrity != nil {
				err = integrity
			}
		}
	} else {
		err = fmt.Errorf("%w: %w", errGTFSIntegrity, err)
	}
	return err
}

func verifyGTFSStream(f *zip.File) error {
	check, err := f.Open()
	if err != nil {
		return fmt.Errorf("%w: %w", errGTFSIntegrity, err)
	}
	defer check.Close()
	n, err := io.Copy(io.Discard, io.LimitReader(check, maxGTFSExpandedBytes+1))
	if err != nil {
		err = fmt.Errorf("%w: %w", errGTFSIntegrity, err)
	}
	if n > maxGTFSExpandedBytes {
		err = errGTFSResource
	}
	return err
}

func readGTFSTable(rd io.Reader, name string, visit func(map[string]string) error) error {
	csvr := csv.NewReader(rd)
	csvr.FieldsPerRecord = -1
	csvr.ReuseRecord = true
	headers, err := csvr.Read()
	if err != nil {
		return err
	}
	headers = append([]string(nil), headers...)
	for i := range headers {
		headers[i] = strings.TrimPrefix(strings.TrimSpace(headers[i]), "\ufeff")
	}
	return visitGTFSRows(csvr, headers, name, visit)
}

func visitGTFSRows(csvr *csv.Reader, headers []string, name string, visit func(map[string]string) error) error {
	m := make(map[string]string, len(headers))
	for count := 1; ; count++ {
		row, err := csvr.Read()
		if err == io.EOF {
			return nil
		}
		if err == nil {
			err = validateGTFSRow(row, headers, name, count)
		}
		if err == nil {
			for i, k := range headers {
				m[k] = row[i]
			}
			err = visit(m)
		}
		if err != nil {
			return err
		}
	}
}

func validateGTFSRow(row, headers []string, name string, count int) error {
	if len(row) != len(headers) {
		return fmt.Errorf("malformed %s row", name)
	}
	if count > maxGTFSRows {
		return fmt.Errorf("%w: GTFS row limit", errGTFSResource)
	}
	return nil
}
