package app

import (
	"archive/zip"
	"bytes"
	"encoding/csv"

	"fmt"
	"io"

	"strings"
)

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
	rd, e := f.Open()
	if e != nil {
		return e
	}
	defer rd.Close()
	csvr := csv.NewReader(io.LimitReader(rd, maxGTFSExpandedBytes))
	csvr.FieldsPerRecord = -1
	csvr.ReuseRecord = true
	headers, e := csvr.Read()
	if e != nil {
		return e
	}
	headers = append([]string(nil), headers...)
	for i := range headers {
		headers[i] = strings.TrimPrefix(strings.TrimSpace(headers[i]), "\ufeff")
	}
	count := 0
	m := make(map[string]string, len(headers))
	for {
		row, e := csvr.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if len(row) != len(headers) {
			return fmt.Errorf("malformed %s row", name)
		}
		count++
		if count > maxGTFSRows {
			return fmt.Errorf("GTFS row limit")
		}
		for i, k := range headers {
			m[k] = row[i]
		}
		if e = visit(m); e != nil {
			return e
		}
	}
	return nil
}
