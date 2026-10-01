// Package store persists small JSON documents in the server data directory.
// Every write goes to a temp file first and is renamed into place so readers
// never see a torn file; a mutex serialises writers in this process.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// namePattern limits document names to a flat, lowercase file name so a caller
// can never address a path outside the data directory.
var namePattern = regexp.MustCompile(`^[a-z0-9-]+\.json$`)

// Store is a directory of JSON documents such as catalog.json or crews.json.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open creates dir with mode 0700 when it is missing and fails when it is not
// writable, so a misconfigured data directory is caught at startup.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("store: empty directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create %s: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return nil, fmt.Errorf("store: %s is not writable: %w", dir, err)
	}
	probe.Close()
	os.Remove(probe.Name())
	return &Store{dir: dir}, nil
}

// Dir returns the directory the store reads from and writes to.
func (s *Store) Dir() string { return s.dir }

// Load decodes the document called name into v. It reports false, with no
// error, only when the document does not exist yet.
//
// Decoding is strict (DecodeStrict), as it is for the config file and the
// catalog file: an unknown field, an empty file or anything after the JSON
// value is an error, so a typo in a hand-edited file is caught instead of
// silently dropped. A load error is a real error: callers must surface it and
// never treat the document as empty, or their next Save would overwrite what
// is on disk. v may be partly filled after an error.
func (s *Store) Load(name string, v any) (bool, error) {
	if !namePattern.MatchString(name) {
		return false, fmt.Errorf("store: bad name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := DecodeStrict(b, v); err != nil {
		return false, fmt.Errorf("store: parse %s: %w", name, err)
	}
	return true, nil
}

// DecodeStrict decodes exactly one JSON value from b into v. Unknown fields
// are errors, and so are an empty b and anything but whitespace after the
// value: unlike json.Unmarshal, Decoder.Decode stops after the first value
// and would accept the rest. Every reader of a JSON file Conductor is given
// decodes with it: the store, the config file and the catalog file.
func DecodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) { // Decode only reports a bare EOF when there is no value at all
			return errors.New("empty document")
		}
		return err
	}
	var extra json.RawMessage
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("more than one JSON value")
	default:
		return fmt.Errorf("unexpected data after the JSON value: %w", err)
	}
}

// rename is os.Rename: the last step of a save. A test replaces it to make a
// save fail after the temp file is written.
var rename = os.Rename

// Encode is v as Save writes it: indented with two spaces, ending in a
// newline, with <, > and & as they are, so patterns and snippets stay
// hand-editable.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Save writes v as Encode writes it to a temp file in the data directory and
// renames it over name (mode 0600), so a crash leaves either the old or the new
// document, never a partial one.
func (s *Store) Save(name string, v any) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("store: bad name %q", name)
	}
	b, err := Encode(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	final := filepath.Join(s.dir, name)
	tmp, err := os.CreateTemp(s.dir, name+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return rename(tmp.Name(), final)
}
