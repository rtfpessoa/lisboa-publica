package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Decode entities one at a time; the multi-agency raw feed is never retained in State.
func decodeCPFeed(blob []byte) (*cpFeed, error) {
	return decodeCPFeedContext(context.Background(), blob)
}

func decodeCPFeedContext(ctx context.Context, blob []byte) (*cpFeed, error) {
	d, err := cpDecoder(ctx, blob)
	if err != nil {
		return nil, err
	}
	feed := &cpFeed{}
	err = decodeCPWrapper(d, feed)
	if err == nil {
		err = completeCPFeed(d, feed)
	}
	return feed, err
}

func cpDecoder(ctx context.Context, blob []byte) (*json.Decoder, error) {
	if len(blob) > providerJSONBytes || !boundedCPJSON(blob) {
		return nil, fmt.Errorf("CP response capacity")
	}
	d := json.NewDecoder(cpContextReader{ctx: ctx, Reader: bytes.NewReader(blob)})
	return d, cpObjectStart(d)
}

func completeCPFeed(d *json.Decoder, feed *cpFeed) error {
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("CP trailing data")
	}
	if !validCPHeader(feed) {
		return fmt.Errorf("unsupported CP feed header")
	}
	return nil
}

func validCPHeader(feed *cpFeed) bool {
	return feed.Header.Version == "2.0" && feed.Header.Incrementality == "FULL_DATASET" && feed.Header.Timestamp > 0
}

func decodeCPWrapper(d *json.Decoder, feed *cpFeed) error {
	seenData := false
	for d.More() {
		key, err := d.Token()
		if err == nil {
			err = decodeCPWrapperField(d, feed, key, &seenData)
		}
		if err != nil {
			return err
		}
	}
	_, err := d.Token()
	if err == nil && !seenData {
		err = fmt.Errorf("CP data missing")
	}
	return err
}

func decodeCPWrapperField(d *json.Decoder, feed *cpFeed, key any, seen *bool) error {
	switch key {
	case "data":
		if *seen {
			return fmt.Errorf("duplicate CP data")
		}
		*seen = true
		return decodeCPData(d, feed)
	case "error":
		return decodeCPSourceError(d)
	}
	return skipCPValue(d)
}

func decodeCPSourceError(d *json.Decoder) error {
	value, err := d.Token()
	if err != nil || value != nil {
		return fmt.Errorf("CP source error")
	}
	return nil
}

func cpObjectStart(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return fmt.Errorf("invalid CP object")
	}
	return nil
}

func decodeCPData(d *json.Decoder, feed *cpFeed) error {
	if err := cpObjectStart(d); err != nil {
		return err
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err == nil {
			err = decodeCPDataField(d, feed, fmt.Sprint(key), seen)
		}
		if err != nil {
			return err
		}
	}
	_, err := d.Token()
	return err
}

func decodeCPDataField(d *json.Decoder, feed *cpFeed, name string, seen map[string]bool) error {
	if seen[name] {
		return fmt.Errorf("duplicate CP feed field")
	}
	seen[name] = true
	var err error
	switch name {
	case "header":
		err = d.Decode(&feed.Header)
	case "entity":
		err = decodeCPEntities(d, feed)
	default:
		err = skipCPValue(d)
	}
	return err
}

// Interrupt streaming decoding when the collector's shared deadline expires.
type cpContextReader struct {
	ctx context.Context
	*bytes.Reader
}

func (r cpContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
