// Package reviewinput reads explicit command previews for the CLI and terminal.
// Request files retain their own idempotency key so losing a response or access
// does not silently turn the same intended work into a second command.
package reviewinput

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/domain"
)

const MaxBytes = 1 << 20

type Draft struct {
	IdempotencyKey string          `json:"idempotencyKey"`
	Input          json.RawMessage `json:"input"`
	Digest         string          `json:"digest"`
	Kind           string          `json:"kind"`
}

// Read accepts a regular file, never terminal stdin, a pipe, device or executable
// request. The descriptor is checked after nonblocking open to close FIFO races.
func Read(ctx context.Context, path, kind string) (Draft, error) {
	if ctx.Err() != nil {
		return Draft{}, ctx.Err()
	}
	if path == "" || path == "-" {
		return Draft{}, errors.New("select a regular request JSON file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Draft{}, errors.New("request file unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return Draft{}, errors.New("request file must be regular and at most 1 MiB")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return Draft{}, errors.New("request file read failed")
	}
	if ctx.Err() != nil {
		return Draft{}, ctx.Err()
	}
	return Decode(b, kind)
}

func Decode(data []byte, kind string) (Draft, error) {
	var envelope struct {
		IdempotencyKey string          `json:"idempotencyKey"`
		Input          json.RawMessage `json:"input"`
	}
	if len(data) > MaxBytes || Strict(data, &envelope) != nil || domain.ValidateCollectionKey(envelope.IdempotencyKey) != nil {
		return Draft{}, errors.New("request requires strict UTF-8 JSON with idempotencyKey and input, at most 1 MiB")
	}
	var value any
	var err error
	switch kind {
	case "runtime":
		var in domain.RuntimeInput
		if err = Strict(envelope.Input, &in); err == nil {
			err = domain.ValidateRuntimeInput(in, time.Now().UTC())
		}
		value = in
	case "graph":
		var in domain.GraphInput
		if err = Strict(envelope.Input, &in); err == nil {
			in, err = domain.NormalizeGraphInput(in)
		}
		value = in
	case "run":
		var in domain.CoordinationPlan
		if err = Strict(envelope.Input, &in); err == nil {
			in, err = domain.NormalizeCoordinationPlan(in)
		}
		value = in
	case "delivery":
		var in domain.DeliveryInput
		if err = Strict(envelope.Input, &in); err == nil {
			err = domain.ValidateDeliveryInput(in)
		}
		value = in
	case "tracker-link":
		var in domain.TrackerLinkInput
		if err = Strict(envelope.Input, &in); err == nil {
			in, err = domain.NormalizeTrackerLink(in)
		}
		value = in
	case "delivery-reconcile":
		var in struct {
			Digest string `json:"digest"`
		}
		err = Strict(envelope.Input, &in)
		if err == nil && !domain.IsLowerHex(in.Digest, 64) {
			err = domain.ErrInvalidInput
		}
		value = in
	case "tracker-sync":
		var in domain.TrackerSyncInput
		if err = Strict(envelope.Input, &in); err == nil {
			err = domain.ValidateTrackerSync(in)
		}
		value = in
	default:
		return Draft{}, errors.New("unknown request kind")
	}
	if err != nil {
		return Draft{}, errors.New("request input does not match the documented " + kind + " schema")
	}
	b, err := json.Marshal(value)
	if err != nil {
		return Draft{}, err
	}
	// The preview also binds the key: changing only that field after inspection
	// could otherwise create duplicate work while keeping the same input hash.
	digest, err := domain.JSONDigest(struct {
		Kind  string          `json:"kind"`
		Key   string          `json:"idempotencyKey"`
		Input json.RawMessage `json:"input"`
	}{kind, envelope.IdempotencyKey, b})
	return Draft{IdempotencyKey: envelope.IdempotencyKey, Input: b, Digest: digest, Kind: kind}, err
}

// Strict rejects duplicate and case-aliased fields before typed decoding. Unlike
// ordinary json.Unmarshal, no later field can replace a fact shown in preview.
func Strict(data []byte, target any) error {
	if !utf8.Valid(data) || len(data) == 0 {
		return domain.ErrInvalidInput
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := walk(d, 0); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return domain.ErrInvalidInput
	}
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer {
		return domain.ErrInvalidInput
	}
	if err := shape(data, t.Elem(), 0); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}
func walk(d *json.Decoder, depth int) error {
	if depth > 32 {
		return domain.ErrInvalidInput
	}
	token, err := d.Token()
	if err != nil || token == nil {
		return domain.ErrInvalidInput
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return domain.ErrInvalidInput
			}
			seen[name] = true
			if err = walk(d, depth+1); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') {
			return domain.ErrInvalidInput
		}
	case json.Delim('['):
		for d.More() {
			if err = walk(d, depth+1); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim(']') {
			return domain.ErrInvalidInput
		}
	}
	return nil
}
func shape(raw []byte, t reflect.Type, depth int) error {
	if depth > 32 {
		return domain.ErrInvalidInput
	}
	// Runtime windows use the standard strict RFC3339 timestamp decoder. Treat
	// time.Time as its wire string, not as an object of unexported Go fields.
	if t == reflect.TypeOf(time.Time{}) {
		var stamp time.Time
		return json.Unmarshal(raw, &stamp)
	}
	if t == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		return shape(raw, t.Elem(), depth+1)
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return domain.ErrInvalidInput
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				fields[name] = f.Type
			}
		}
		for name, child := range object {
			ft, ok := fields[name]
			if !ok {
				return domain.ErrInvalidInput
			}
			if err := shape(child, ft, depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return domain.ErrInvalidInput
		}
		for _, v := range values {
			if err := shape(v, t.Elem(), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
