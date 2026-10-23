package patterns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// preserveJSONValues allows fields added by newer versions while requiring
// every original field and value to survive the typed round trip. Object keys
// must retain their marshaled order; ambiguous or duplicate keys are rejected.
func preserveJSONValues(original, encoded []byte) error {
	a, b := json.NewDecoder(bytes.NewReader(original)), json.NewDecoder(bytes.NewReader(encoded))
	a.UseNumber()
	b.UseNumber()
	if err := compareJSONValue(a, b); err != nil {
		return err
	}
	if _, err := a.Token(); err != io.EOF {
		return fmt.Errorf("extra original JSON")
	}
	if _, err := b.Token(); err != io.EOF {
		return fmt.Errorf("extra encoded JSON")
	}
	return nil
}

func compareJSONValue(a, b *json.Decoder) error {
	x, err := a.Token()
	if err != nil {
		return err
	}
	y, err := b.Token()
	if err != nil || x != y {
		return fmt.Errorf("JSON value changed")
	}
	switch x {
	case json.Delim('{'):
		return compareJSONObject(a, b)
	case json.Delim('['):
		for a.More() && b.More() {
			if err := compareJSONValue(a, b); err != nil {
				return err
			}
		}
		if a.More() || b.More() {
			return fmt.Errorf("JSON array changed")
		}
		_, err = a.Token()
		if err == nil {
			_, err = b.Token()
		}
		return err
	default:
		return nil
	}
}

func compareJSONObject(a, b *json.Decoder) error {
	seen := map[string]bool{}
	for a.More() {
		keyToken, err := a.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok || seen[key] {
			return fmt.Errorf("duplicate or invalid JSON key")
		}
		seen[key] = true
		if err = seekJSONKey(b, key); err != nil {
			return err
		}
		if err = compareJSONValue(a, b); err != nil {
			return err
		}
	}
	for b.More() {
		if err := discardJSONField(b); err != nil {
			return err
		}
	}
	_, err := a.Token()
	if err == nil {
		_, err = b.Token()
	}
	return err
}

func seekJSONKey(decoder *json.Decoder, key string) error {
	for decoder.More() {
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if value == key {
			return nil
		}
		var discarded json.RawMessage
		if err = decoder.Decode(&discarded); err != nil {
			return err
		}
	}
	return fmt.Errorf("JSON field not preserved")
}

func discardJSONField(decoder *json.Decoder) error {
	if _, err := decoder.Token(); err != nil {
		return err
	}
	var discarded json.RawMessage
	return decoder.Decode(&discarded)
}
