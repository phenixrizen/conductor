// Package store persists small JSON documents in the server data directory.
// Every write goes to a temp file first and is renamed into place so readers
// never see a torn file; a mutex serialises writers in this process.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
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
// error, when the document does not exist yet.
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
	if err := json.Unmarshal(b, v); err != nil {
		return false, fmt.Errorf("store: parse %s: %w", name, err)
	}
	return true, nil
}

// Save writes v as indented JSON to a temp file in the data directory and
// renames it over name (mode 0600), so a crash leaves either the old or the new
// document, never a partial one.
func (s *Store) Save(name string, v any) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("store: bad name %q", name)
	}
	b, err := json.MarshalIndent(v, "", "  ")
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
	if _, err := tmp.Write(append(b, '\n')); err != nil {
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
	return os.Rename(tmp.Name(), final)
}
