package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"unicode/utf8"
)

// A nested command must have a single interpretation too: duplicate task scopes
// or digests cannot be silently selected by a downstream decoder.
func decodeStrictCommand(w http.ResponseWriter, r *http.Request, value any) error {
	var raw json.RawMessage
	if err := decode(w, r, &raw); err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return errors.New("command must be UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return errors.New("command nesting limit exceeded")
		}
		token, err := d.Token()
		if err != nil {
			return err
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
					return errors.New("duplicate command field")
				}
				seen[name] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(value)
}
