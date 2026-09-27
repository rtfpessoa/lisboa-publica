package app

import (
	"bytes"
	"compress/flate"
	"io"
	"sync"
)

type visitCompressor struct {
	buffer bytes.Buffer
	writer *flate.Writer
}

var visitCompressors = sync.Pool{New: func() any {
	compressor := &visitCompressor{}
	compressor.writer, _ = flate.NewWriter(&compressor.buffer, flate.BestSpeed)
	return compressor
}}

func compressPackedVisits(raw []byte) []byte {
	compressor := visitCompressors.Get().(*visitCompressor)
	defer visitCompressors.Put(compressor)
	compressor.buffer.Reset()
	compressor.writer.Reset(&compressor.buffer)
	_, _ = compressor.writer.Write(raw)
	_ = compressor.writer.Close()
	if compressor.buffer.Len()+1 >= len(raw) {
		return bytes.Clone(raw)
	}
	return append([]byte{2}, compressor.buffer.Bytes()...)
}

func expandPackedVisits(blob []byte) []byte {
	if len(blob) == 0 || blob[0] != 2 {
		return blob
	}
	reader := flate.NewReader(bytes.NewReader(blob[1:]))
	defer reader.Close()
	// One trip cannot contain more rows than the bounded source archive.
	raw, err := io.ReadAll(io.LimitReader(reader, maxGTFSExpandedBytes+1))
	if err != nil || len(raw) > maxGTFSExpandedBytes {
		return nil
	}
	return raw
}
