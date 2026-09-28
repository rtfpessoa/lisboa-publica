package patterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type providerDetailChunk struct {
	raw     []byte
	samples int64
	key     string
	hour    time.Time
}

func (j *providerRecording) publishDetail() error {
	chunk, err := j.loadDetailChunk()
	if err != nil {
		return err
	}
	raw, err := j.appendDetailChunk(chunk)
	if err != nil {
		return err
	}
	blob, err := j.verifiedDetailBlob(raw)
	if err != nil {
		return err
	}
	b := block{Key: chunk.key, Kind: "observations", Operator: j.receipt.Operator, Age: chunk.hour, AsOf: j.receipt.ReceivedAt, Samples: chunk.samples + 1}
	return j.service.publish(b, blob, j.receipt.ReceivedAt)
}

func (j *providerRecording) loadDetailChunk() (*providerDetailChunk, error) {
	hour := j.receipt.ReceivedAt.UTC().Truncate(time.Hour)
	chunk := &providerDetailChunk{hour: hour, key: j.receipt.Operator + ":observations:" + hour.Format(time.RFC3339)}
	current := j.service.currentProviderChunk(j.receipt.Operator, hour)
	if current == nil {
		return chunk, nil
	}
	raw, err := j.service.decodedArchiveBlock(*current)
	if err != nil {
		return nil, err
	}
	chunk.raw, chunk.samples, chunk.key = raw, current.Samples, current.Key
	return chunk, nil
}

func (s *Service) currentProviderChunk(operator string, hour time.Time) *block {
	var current *block
	for i := range s.index.Blocks {
		b := &s.index.Blocks[i]
		if b.Kind != "observations" || b.Operator != operator || !b.Age.Equal(hour) {
			continue
		}
		if current == nil || b.AsOf.After(current.AsOf) {
			current = b
		}
	}
	return current
}

func providerChunkDefinitions(raw []byte) (map[string]bool, error) {
	definitions := map[string]bool{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var prior ProviderReceipt
		err := decoder.Decode(&prior)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		for id := range prior.Journeys {
			definitions[id] = true
		}
	}
	return definitions, nil
}

func (j *providerRecording) appendDetailChunk(chunk *providerDetailChunk) ([]byte, error) {
	definitions, err := providerChunkDefinitions(chunk.raw)
	if err != nil {
		return nil, err
	}
	r := j.receipt
	r.Journeys = map[string]ProviderJourney{}
	for id, path := range j.state.Paths {
		if !definitions[id] {
			r.Journeys[id] = path
		}
	}
	addition, err := encodeProviderDetail(r)
	if err != nil {
		return nil, err
	}
	if len(chunk.raw)+len(addition) > maxDetailHourBytes {
		return j.rolloverDetailChunk(chunk, r)
	}
	return append(chunk.raw, addition...), nil
}

func encodeProviderDetail(r ProviderReceipt) ([]byte, error) {
	var addition bytes.Buffer
	if err := json.NewEncoder(&addition).Encode(r); err != nil {
		return nil, err
	}
	if addition.Len() > maxDetailHourBytes {
		return nil, fmt.Errorf("provider receipt size limit")
	}
	return addition.Bytes(), nil
}

func (j *providerRecording) rolloverDetailChunk(chunk *providerDetailChunk, r ProviderReceipt) ([]byte, error) {
	// Every independently retained chunk bootstraps its full bounded dictionary.
	r.Journeys = j.state.Paths
	raw, err := encodeProviderDetail(r)
	chunk.samples = 0
	chunk.key = j.receipt.Operator + ":observations:" + chunk.hour.Format(time.RFC3339) + ":" + j.receipt.ReceivedAt.Format(time.RFC3339Nano)
	return raw, err
}

func (j *providerRecording) verifiedDetailBlob(raw []byte) ([]byte, error) {
	blob := j.service.encoder.EncodeAll(raw, nil)
	decoded, err := j.service.decoder.DecodeAll(blob, nil)
	if err != nil || !bytes.Equal(raw, decoded) {
		return nil, fmt.Errorf("provider archive roundtrip")
	}
	return blob, nil
}
