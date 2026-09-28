package patterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func (s *Service) revisionBlock(b block, used *int, message string) ([]byte, error) {
	raw, err := s.decodedArchiveBlock(b)
	if err != nil {
		return nil, err
	}
	*used += len(raw)
	if *used > maxBlockBytes {
		return nil, fmt.Errorf("%s", message)
	}
	return raw, nil
}

func decodeRevisionFrames[T any](raw []byte, limit int, message string, normalize func(*T)) ([]T, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	frames := []T{}
	for {
		var frame T
		err := decoder.Decode(&frame)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		normalize(&frame)
		frames = append(frames, frame)
		if len(frames) > limit {
			return nil, fmt.Errorf("%s", message)
		}
	}
	return frames, nil
}
