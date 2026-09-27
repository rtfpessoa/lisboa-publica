package app

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
)

const snapshotArchiveLimit = 64 << 10
const snapshotArchiveVersion = "LPH1"
const snapshotArchiveMinimumSaving = 512
const snapshotArchiveRowOverhead = 128

// Archive the exact database JSON text; the small projection preserves original values.
func archiveSnapshot(raw []byte) ([]byte, []byte, error) {
	if len(raw) > snapshotArchiveLimit || !json.Valid(raw) {
		return nil, nil, fmt.Errorf("invalid snapshot archive input")
	}
	projected, err := snapshotProjection(raw)
	if err != nil {
		return nil, nil, err
	}
	archive, err := gzipSnapshot(raw)
	return projected, archive, err
}
func snapshotProjection(raw []byte) ([]byte, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	projection := map[string]json.RawMessage{}
	for _, key := range []string{"source_id", "model", "license_plate", "typology", "propulsion"} {
		if value, ok := values[key]; ok {
			projection[key] = value
		}
	}
	projected, err := json.Marshal(projection)
	if err != nil {
		return nil, err
	}
	return projected, nil
}
func gzipSnapshot(raw []byte) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(snapshotArchiveVersion)
	writer := gzip.NewWriter(&b)
	var err error
	if _, err = writer.Write(raw); err == nil {
		err = writer.Close()
	} else {
		_ = writer.Close()
	}
	if err == nil && b.Len() > snapshotArchiveLimit+len(snapshotArchiveVersion) {
		err = fmt.Errorf("snapshot archive capacity")
	}
	return b.Bytes(), err
}

// Used by offline analysis; interactive history queries read typed facts and the projection.
func decodeSnapshotArchive(archive []byte) ([]byte, error) {
	if len(archive) > snapshotArchiveLimit+len(snapshotArchiveVersion) || !bytes.HasPrefix(archive, []byte(snapshotArchiveVersion)) {
		return nil, fmt.Errorf("unsupported snapshot archive")
	}
	input := bytes.NewReader(archive[len(snapshotArchiveVersion):])
	reader, err := gzip.NewReader(input)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	reader.Multistream(false)
	raw, err := io.ReadAll(io.LimitReader(reader, snapshotArchiveLimit+1))
	if err == nil && (len(raw) > snapshotArchiveLimit || input.Len() != 0 || !json.Valid(raw)) {
		err = fmt.Errorf("invalid snapshot archive")
	}
	return raw, err
}
