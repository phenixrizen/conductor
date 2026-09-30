package crew

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/store"
)

// crewsFile is the document in the data directory that holds every crew.
const crewsFile = "crews.json"

// maxCrews bounds how many crews a data directory holds.
const maxCrews = 50

// maxID bounds an ID. Derived IDs are at most 40 characters from the name
// plus a suffix; duplicates of duplicates are cut to stay within it.
const maxID = 64

// idPattern is what a crew ID looks like: a derived ID always matches it, and
// a hand-edited crews.json is held to it.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Errors a Store returns besides validation and save errors.
var (
	ErrNotFound     = errors.New("no such crew")
	ErrTooManyCrews = fmt.Errorf("too many crews (at most %d)", maxCrews)
)

// crewsDoc is the shape of crews.json.
type crewsDoc struct {
	Crews []Crew `json:"crews"`
}

// Store holds the saved crews in memory and in crews.json. Every change is
// saved before it is made in memory, so a failed save changes nothing. Crews
// go in and come out as copies: a caller can never change a stored crew.
type Store struct {
	st  *store.Store
	now func() time.Time // stamps CreatedAt and UpdatedAt

	mu    sync.Mutex
	crews map[string]Crew
}

// NewStore loads the crews saved in st, which must not be nil. A missing
// crews.json is an empty store. A file that cannot be parsed, or that holds an
// invalid crew, an ID twice or more than 50 crews, is an error naming its path:
// startup must fail rather than let the next save overwrite it.
func NewStore(st *store.Store) (*Store, error) {
	s := &Store{st: st, now: func() time.Time { return time.Now().UTC() }, crews: map[string]Crew{}}
	path := filepath.Join(st.Dir(), crewsFile)
	var doc crewsDoc
	found, err := st.Load(crewsFile, &doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !found {
		return s, nil
	}
	if len(doc.Crews) > maxCrews {
		return nil, fmt.Errorf("%s: %d crews, at most %d", path, len(doc.Crews), maxCrews)
	}
	for i, c := range doc.Crews {
		if err := c.validateWithID(); err != nil {
			return nil, fmt.Errorf("%s: crews[%d]: %w", path, i, err)
		}
		if _, dup := s.crews[c.ID]; dup {
			return nil, fmt.Errorf("%s: crews[%d]: id %q is used twice", path, i, c.ID)
		}
		s.crews[c.ID] = c.stored()
	}
	return s, nil
}

// validateWithID is Validate plus the ID, which the store keys crews by.
func (c Crew) validateWithID() error {
	if !idPattern.MatchString(c.ID) {
		return fmt.Errorf("id %q must match %s", c.ID, idPattern)
	}
	return c.Validate()
}

// List returns every crew ordered by name, ignoring case, then by ID.
func (s *Store) List() []Crew {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sorted(s.crews)
}

// Get returns the crew with the given ID.
func (s *Store) Get(id string) (Crew, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.crews[id]
	if !ok {
		return Crew{}, false
	}
	return c.clone(), true
}

// Put validates c and saves it under c.ID, adding it or replacing the crew
// with that ID. It stores c as it is, times included; Create, Update and
// Duplicate are the operations that set the ID and the times.
func (s *Store) Put(c Crew) error {
	if err := c.validateWithID(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commit(c)
}

// Create saves c as a new crew. Its ID comes from its name, lower case with
// every other run of characters a dash and at most 40 of them ("crew" when
// nothing is left), then -2, -3 and so on when a crew has it already.
// CreatedAt and UpdatedAt are now. The ID and times c holds are ignored.
func (s *Store) Create(c Crew) (Crew, error) {
	if err := c.Validate(); err != nil {
		return Crew{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.crews) >= maxCrews {
		return Crew{}, ErrTooManyCrews
	}
	c.ID = s.freeID(cmp.Or(slug(c.Name), "crew"), "")
	c.CreatedAt = s.now()
	c.UpdatedAt = c.CreatedAt
	if err := s.commit(c); err != nil {
		return Crew{}, err
	}
	return s.crews[c.ID].clone(), nil
}

// Update replaces the crew with the given ID by c. The ID and CreatedAt stay
// as they were, whatever c holds, and UpdatedAt is now.
func (s *Store) Update(id string, c Crew) (Crew, error) {
	if err := c.Validate(); err != nil {
		return Crew{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.crews[id]
	if !ok {
		return Crew{}, ErrNotFound
	}
	c.ID, c.CreatedAt, c.UpdatedAt = id, old.CreatedAt, s.now()
	if err := s.commit(c); err != nil {
		return Crew{}, err
	}
	return s.crews[id].clone(), nil
}

// Duplicate saves a copy of the crew with the given ID as a new crew: its ID
// is <id>-copy (then <id>-copy-2 and so on), its name "<name> copy", and its
// CreatedAt and UpdatedAt now.
func (s *Store) Duplicate(id string) (Crew, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.crews[id]
	if !ok {
		return Crew{}, ErrNotFound
	}
	if len(s.crews) >= maxCrews {
		return Crew{}, ErrTooManyCrews
	}
	c := src.clone()
	c.ID = s.freeID(id, "-copy")
	c.Name = copyName(src.Name)
	c.CreatedAt = s.now()
	c.UpdatedAt = c.CreatedAt
	if err := s.commit(c); err != nil {
		return Crew{}, err
	}
	return s.crews[c.ID].clone(), nil
}

// Delete removes the crew with the given ID and reports whether there was one.
func (s *Store) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.crews[id]; !ok {
		return false, nil
	}
	next := maps.Clone(s.crews)
	delete(next, id)
	if err := s.save(next); err != nil {
		return false, err
	}
	s.crews = next
	return true, nil
}

// commit adds c, or replaces the crew with its ID, and saves. A new crew past
// maxCrews is refused, and when the save fails the crews stay as they were.
// The caller holds s.mu and has validated c.
func (s *Store) commit(c Crew) error {
	if _, exists := s.crews[c.ID]; !exists && len(s.crews) >= maxCrews {
		return ErrTooManyCrews
	}
	next := maps.Clone(s.crews)
	next[c.ID] = c.stored()
	if err := s.save(next); err != nil {
		return err
	}
	s.crews = next
	return nil
}

// stored returns the copy of c the store keeps: it shares no slice with c, and
// its members are listed as [] rather than null when there are none.
func (c Crew) stored() Crew {
	c = c.clone()
	if c.Members == nil {
		c.Members = []Member{}
	}
	return c
}

// save writes crews to crews.json, ordered as List orders them.
func (s *Store) save(crews map[string]Crew) error {
	if err := s.st.Save(crewsFile, crewsDoc{Crews: sorted(crews)}); err != nil {
		return fmt.Errorf("save %s: %w", crewsFile, err)
	}
	return nil
}

// sorted returns copies of crews ordered by name, ignoring case, then by ID.
func sorted(crews map[string]Crew) []Crew {
	out := make([]Crew, 0, len(crews))
	for _, c := range crews {
		out = append(out, c.clone())
	}
	slices.SortFunc(out, func(a, b Crew) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
	})
	return out
}

// freeID returns stem+tag, or stem+tag+"-2", stem+tag+"-3" and so on, the
// first that no crew has, with stem cut so that the ID stays within maxID.
// stem starts with a letter or digit, as slugs and IDs do. Every candidate
// differs from the others, so one of the first len(s.crews)+1 is free. The
// caller holds s.mu.
func (s *Store) freeID(stem, tag string) string {
	for n := 1; ; n++ {
		end := tag
		if n > 1 {
			end += "-" + strconv.Itoa(n)
		}
		id := strings.TrimRight(stem[:min(len(stem), maxID-len(end))], "-") + end
		if _, taken := s.crews[id]; !taken {
			return id
		}
	}
}

// maxSlug bounds the part of an ID that comes from the name.
const maxSlug = 40

// slug derives an ID from a crew name by the rule the web's slugId follows:
// lower case, every run of characters other than a-z and 0-9 one dash, no
// dash at either end, at most 40 characters. It is empty when nothing usable
// is left.
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	out := b.String()
	return strings.TrimRight(out[:min(len(out), maxSlug)], "-")
}

// copyName is the name of a duplicate: "<name> copy", with name cut so that
// it stays within maxName characters.
func copyName(name string) string {
	const suffix = " copy" // ASCII: as many characters as bytes
	r := []rune(name)
	return strings.TrimSpace(string(r[:min(len(r), maxName-len(suffix))])) + suffix
}
