package crew

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/phenixrizen/conductor/internal/store"
)

// legacyFile is the file an older Conductor kept every crew in.
const legacyFile = "crews.json"

// legacyDoc is the shape of crews.json.
type legacyDoc struct {
	Crews []Crew `json:"crews"`
}

// migrate moves the crews of crews.json in the data directory st, if there is
// one, to a file each in dir, then renames crews.json to crews.json.migrated
// (or .migrated.2 and on, when that is taken), so the move happens once and
// the renamed file keeps every crew as crews.json had it. It never overwrites
// a crew file. One that holds exactly what the move would write is the first
// half of a move that stopped, and is left as it is. One that holds anything
// else (a crew made or edited since, a link) is kept too, and notices says
// so, naming both files: that crew stays in crews.json.migrated only.
// crews.json is held to what it always was: one that cannot be parsed, or
// that holds an invalid crew or an ID twice, stops startup with an error
// naming it, and nothing is moved.
func migrate(st, dir *store.Store) (notices []error, err error) {
	path := filepath.Join(st.Dir(), legacyFile)
	var doc legacyDoc
	found, err := st.Load(legacyFile, &doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !found {
		return nil, nil
	}
	seen := make(map[string]bool, len(doc.Crews))
	for i, c := range doc.Crews {
		c = c.canonical()
		if err := c.validateWithID(); err != nil {
			return nil, fmt.Errorf("%s: crews[%d]: %w", path, i, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("%s: crews[%d]: id %q is used twice", path, i, c.ID)
		}
		seen[c.ID] = true
		doc.Crews[i] = c
	}
	migrated := freeName(path + ".migrated")
	for _, c := range doc.Crews {
		file := filepath.Join(dir.Dir(), c.ID+".json")
		want, err := store.Encode(c)
		if err != nil {
			return nil, fmt.Errorf("%s: crew %s: %w", path, c.ID, err)
		}
		fi, err := os.Lstat(file)
		switch {
		case err == nil:
			if !fi.Mode().IsRegular() || !holds(file, want) {
				notices = append(notices, fmt.Errorf("%s: crew %s is not moved: %s exists and is kept; %s keeps the crew as %s had it", path, quote(c.ID), file, migrated, legacyFile))
			}
			continue
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%s: move crew %s to %s: %w", path, c.ID, dir.Dir(), err)
		}
		if err := dir.Save(c.ID+".json", c); err != nil {
			return nil, fmt.Errorf("%s: move crew %s to %s: %w", path, c.ID, dir.Dir(), err)
		}
	}
	if err := os.Rename(path, migrated); err != nil {
		return nil, fmt.Errorf("%s: the crews are moved, but the file could not be renamed: %w", path, err)
	}
	return notices, nil
}

// holds reports whether the regular file p holds exactly want. It reads at
// most one byte more than want: a larger file holds something else.
func holds(p string, want []byte) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(len(want))+1))
	return err == nil && bytes.Equal(b, want)
}

// freeName returns p, or p.2, p.3 and so on, the first that does not exist.
func freeName(p string) string {
	for n := 1; ; n++ {
		q := p
		if n > 1 {
			q = p + "." + strconv.Itoa(n)
		}
		if _, err := os.Lstat(q); errors.Is(err, fs.ErrNotExist) {
			return q
		}
	}
}
