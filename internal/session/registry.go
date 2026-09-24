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

// Registry indexes live and recently ended sessions.
type Registry struct {
	mu    sync.RWMutex
	max   int
	items map[string]Driver
	// OnRemove is called after a session leaves the registry (e.g. to delete links).
	OnRemove func(id string)
}

// NewRegistry creates a registry holding at most max sessions.
func NewRegistry(max int) *Registry {
	return &Registry{max: max, items: map[string]Driver{}}
}

// Add inserts d, enforcing the capacity limit against non-ended sessions.
func (r *Registry) Add(d Driver) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	active := 0
	for _, it := range r.items {
		if !it.Info().Status.Ended() {
			active++
		}
	}
	if active >= r.max {
		return ErrTooManySessions
	}
	r.items[d.Info().ID] = d
	return nil
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
	r.mu.RLock()
	out := make([]Info, 0, len(r.items))
	for _, d := range r.items {
		out = append(out, d.Info())
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Remove deletes a session from the index.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	_, ok := r.items[id]
	delete(r.items, id)
	r.mu.Unlock()
	if ok && r.OnRemove != nil {
		r.OnRemove(id)
	}
}

// Count returns the number of indexed sessions.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// Each calls fn for every driver.
func (r *Registry) Each(fn func(Driver)) {
	r.mu.RLock()
	list := make([]Driver, 0, len(r.items))
	for _, d := range r.items {
		list = append(list, d)
	}
	r.mu.RUnlock()
	for _, d := range list {
		fn(d)
	}
}

// GC removes ended sessions older than retention. It returns the removed IDs.
func (r *Registry) GC(retention time.Duration, now time.Time) []string {
	var expired []string
	r.mu.RLock()
	for id, d := range r.items {
		info := d.Info()
		if info.Status.Ended() && info.EndedAt != nil && now.Sub(*info.EndedAt) >= retention {
			expired = append(expired, id)
		}
	}
	r.mu.RUnlock()
	for _, id := range expired {
		r.Remove(id)
	}
	return expired
}
