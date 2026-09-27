package app

import (
	"encoding/json"
	"fmt"
)

// Bound individual entity values before encoding/json can buffer a large object.
// Wrapper/data objects may span the full16MiB; feed objects/entities are small.
type cpJSONBounds struct {
	starts                        []int
	inString, escaped             bool
	stringStart, nesting, numeric int
}

func boundedCPJSON(blob []byte) bool {
	state := cpJSONBounds{starts: make([]int, 0, cpJSONDepth)}
	for n, b := range blob {
		if !state.step(n, b) {
			return false
		}
	}
	return !state.inString && len(state.starts) == 0 && state.nesting == 0
}

func (s *cpJSONBounds) step(n int, b byte) bool {
	if s.inString {
		return s.stringByte(n, b)
	}
	if b == '"' {
		s.inString, s.stringStart = true, n
		return true
	}
	if cpNumberByte(b) {
		s.numeric++
	} else {
		s.numeric = 0
	}
	return s.numeric <= cpJSONNumericBytes && s.structureByte(n, b)
}

func cpNumberByte(b byte) bool {
	switch b {
	case '-', '+', '.', 'e', 'E':
		return true
	}
	return b >= '0' && b <= '9'
}

func (s *cpJSONBounds) stringByte(n int, b byte) bool {
	if s.escaped {
		s.escaped = false
	} else if b == '\\' {
		s.escaped = true
	} else if b == '"' {
		s.inString = false
	}
	return n-s.stringStart <= cpJSONStringBytes
}

func (s *cpJSONBounds) structureByte(n int, b byte) bool {
	switch b {
	case '[':
		s.nesting++
	case '{':
		s.nesting++
		s.starts = append(s.starts, n)
	case ']':
		s.nesting--
	case '}':
		s.nesting--
		if !s.closeObject(n) {
			return false
		}
	}
	return s.nesting >= 0 && s.nesting <= cpJSONDepth
}

func (s *cpJSONBounds) closeObject(n int) bool {
	count := len(s.starts)
	if count == 0 {
		return false
	}
	valid := count < cpEntityObjectDepth || n-s.starts[count-1] <= cpEntityBytes
	s.starts = s.starts[:count-1]
	return valid
}

func skipCPValue(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	return skipCPContainer(d, delim)
}

func skipCPContainer(d *json.Decoder, delim json.Delim) error {
	if delim != '{' && delim != '[' {
		return fmt.Errorf("invalid CP value")
	}
	for d.More() {
		var err error
		if delim == '{' {
			_, err = d.Token()
		}
		if err == nil {
			err = skipCPValue(d)
		}
		if err != nil {
			return err
		}
	}
	_, err := d.Token()
	return err
}
