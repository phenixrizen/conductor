package session

import (
	"bytes"
	"sync"
)

// Ring is a fixed-capacity byte buffer that keeps the most recent bytes.
type Ring struct {
	mu      sync.Mutex
	buf     []byte
	start   int // index of the oldest byte when full
	size    int // bytes currently stored
	wrapped bool
}

// NewRing allocates a ring of the given capacity (minimum 1).
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]byte, capacity)}
}

// Write appends p, discarding the oldest bytes when full.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	cap := len(r.buf)
	if n >= cap {
		copy(r.buf, p[n-cap:])
		r.start, r.size, r.wrapped = 0, cap, true
		return n, nil
	}
	end := (r.start + r.size) % cap
	first := copy(r.buf[end:], p)
	if first < n {
		copy(r.buf, p[first:])
	}
	if r.size+n > cap {
		overflow := r.size + n - cap
		r.start = (r.start + overflow) % cap
		r.size = cap
		r.wrapped = true
	} else {
		r.size += n
	}
	return n, nil
}

// Snapshot returns a copy of the stored bytes in order. When the buffer has
// wrapped, the copy starts after the first newline within the first 4 KiB so a
// replay is less likely to begin mid-escape-sequence.
func (r *Ring) Snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, r.size)
	cap := len(r.buf)
	first := copy(out, r.buf[r.start:min(r.start+r.size, cap)])
	if first < r.size {
		copy(out[first:], r.buf[:r.size-first])
	}
	if r.wrapped {
		limit := min(len(out), 4096)
		if i := bytes.IndexByte(out[:limit], '\n'); i >= 0 {
			out = out[i+1:]
		}
	}
	return out
}

// Len returns the number of stored bytes.
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.size
}

// Cap returns the capacity.
func (r *Ring) Cap() int { return len(r.buf) }
