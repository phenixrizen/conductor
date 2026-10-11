package session

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNotFound is returned for unknown session IDs.
var ErrNotFound = errors.New("session: not found")

// ErrTooManySessions is returned when the registry is full.
var ErrTooManySessions = errors.New("session: too many sessions")

// Registry indexes live and recently ended sessions. It asks a session for
// its description (Info) holding none of its own locks, from a copy of its
// index: a session that is slow to answer holds up whoever asks it, never a
// lookup or the removal of another.
type Registry struct {
	mu    sync.RWMutex
	max   int
	items map[string]Driver
	// addMu makes Add's count and insert one step for the other Adds: only
	// an Add raises the count.
	addMu sync.Mutex
	// OnRemove is called after a session leaves the registry (e.g. to delete
	// links), with the session as it was.
	OnRemove func(id string, d Driver)
}

// NewRegistry creates a registry holding at most max sessions.
func NewRegistry(max int) *Registry {
	return &Registry{max: max, items: map[string]Driver{}}
}

// Add inserts d, enforcing the capacity limit against non-ended sessions.
func (r *Registry) Add(d Driver) error {
	id := d.Info().ID
	r.addMu.Lock()
	defer r.addMu.Unlock()
	active := 0
	for _, it := range r.snapshot() {
		if !it.Info().Status.Ended() {
			active++
		}
	}
	if active >= r.max {
		return ErrTooManySessions
	}
	r.mu.Lock()
	r.items[id] = d
	r.mu.Unlock()
	return nil
}

// snapshot copies the index's sessions.
func (r *Registry) snapshot() []Driver {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Driver, 0, len(r.items))
	for _, d := range r.items {
		list = append(list, d)
	}
	return list
}

// Get returns the session with the given ID.
func (r *Registry) Get(id string) (Driver, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.items[id]
	return d, ok
}

// List returns session descriptions, newest first.
func (r *Registry) List() []Info {
	list := r.snapshot()
	out := make([]Info, 0, len(list))
	for _, d := range list {
		out = append(out, d.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Remove deletes a session from the index.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	d, ok := r.items[id]
	delete(r.items, id)
	r.mu.Unlock()
	if ok && r.OnRemove != nil {
		r.OnRemove(id, d)
	}
}

// removeIf deletes the session id from the index if it is still d.
func (r *Registry) removeIf(id string, d Driver) bool {
	r.mu.Lock()
	ok := r.items[id] == d
	if ok {
		delete(r.items, id)
	}
	r.mu.Unlock()
	if ok && r.OnRemove != nil {
		r.OnRemove(id, d)
	}
	return ok
}

// Count returns the number of indexed sessions.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// Each calls fn for every driver.
func (r *Registry) Each(fn func(Driver)) {
	for _, d := range r.snapshot() {
		fn(d)
	}
}

// GC removes ended sessions older than retention. It returns the removed IDs.
func (r *Registry) GC(retention time.Duration, now time.Time) []string {
	var expired []string
	for _, d := range r.snapshot() {
		info := d.Info()
		if info.Status.Ended() && info.EndedAt != nil && now.Sub(*info.EndedAt) >= retention && r.removeIf(info.ID, d) {
			expired = append(expired, info.ID)
		}
	}
	return expired
}
