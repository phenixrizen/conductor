// Package store persists small JSON documents in the server data directory:
// a directory of JSON documents, which may hold directories of its own (Sub).
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
	"time"
)

// namePattern limits document names to a flat, lowercase file name so a caller
// can never address a path outside the data directory.
var namePattern = regexp.MustCompile(`^[a-z0-9-]+\.json$`)

// Store is a directory of JSON documents such as catalog.json. It may hold
// directories of its own, each a Store (Sub), such as crews/ with one document
// per crew.
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

// subPattern limits a sub-store's name to a flat, lower-case directory name.
var subPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Sub opens the directory name inside s as a store of its own, made 0700 when
// missing and refused when it cannot be written, as Open does.
func (s *Store) Sub(name string) (*Store, error) {
	if !subPattern.MatchString(name) {
		return nil, fmt.Errorf("store: bad name %q", name)
	}
	return Open(filepath.Join(s.dir, name))
}

// Entry is a document in a store, as List reports it.
type Entry struct {
	Name    string // the document's name, such as "todo-app.json"
	Size    int64
	ModTime time.Time
}

// List returns the documents in the store, sorted by name: the regular files
// whose names a document may have. Temp files, the write probe, links,
// directories and anything else are left out.
func (s *Store) List() ([]Entry, error) {
	des, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, de := range des {
		if !de.Type().IsRegular() || !namePattern.MatchString(de.Name()) {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue // removed meanwhile
		}
		out = append(out, Entry{Name: de.Name(), Size: fi.Size(), ModTime: fi.ModTime()})
	}
	return out, nil
}

// Delete removes the document called name. One that is not there is no error.
func (s *Store) Delete(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("store: bad name %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

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

// ErrChanged is what LoadLimit's error wraps when the file it opened is not
// the one it checked: another was renamed into its place in between, as a save
// does. It passes: a caller may read again.
var ErrChanged = errors.New("changed while it was opened")

// LoadLimit is Load for a document of at most limit bytes. A larger one is an
// error that says so, and is not read past the limit. Only a regular file is
// read: a symbolic link, which List leaves out too, is refused rather than
// followed (Lstat), and so is anything else in the document's place.
func (s *Store) LoadLimit(name string, v any, limit int64) (bool, error) {
	if !namePattern.MatchString(name) {
		return false, fmt.Errorf("store: bad name %q", name)
	}
	path := filepath.Join(s.dir, name)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		what := "not a regular file"
		if fi.Mode()&os.ModeSymlink != 0 {
			what = "a symbolic link, which is not followed"
		}
		return false, fmt.Errorf("store: %s is %s", name, what)
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	// The file opened is the one checked: a link put in its place meanwhile
	// is refused too.
	ofi, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !os.SameFile(fi, ofi) {
		return false, fmt.Errorf("store: %s %w", name, ErrChanged)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return false, err
	}
	if int64(len(b)) > limit {
		return false, fmt.Errorf("store: %s is more than %d bytes", name, limit)
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
