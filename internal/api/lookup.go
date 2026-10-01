package api

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// lookupTTL is how long an answer about a program is kept: a CLI installed
// meanwhile is seen at most this long after.
const lookupTTL = 30 * time.Second

// maxLookups bounds the answers kept at once. The catalog's agents need a few
// dozen; past the bound an answer is given but not kept (nothing is evicted)
// until answers expire and make room.
const maxLookups = 256

// lookupWorkers bounds the lookups one listing runs at once (warm).
const lookupWorkers = 8

type lookupEntry struct {
	path string
	ok   bool
	at   time.Time // when the lookup started
}

// lookupCall is a lookup under way. Its entry is set before done is closed.
type lookupCall struct {
	done chan struct{}
	at   time.Time
	look func(program string) (string, error)
	e    lookupEntry
}

// lookupCache answers whether programs resolve on this server, the way exec
// resolves them when a session launches (exec.LookPath, the check of POST
// /api/catalog/check), keeping each answer for lookupTTL. GET /api/catalog
// marks every agent with it and a crew launch refuses a member whose program
// it cannot find. Nothing is run.
//
// A lookup runs without the lock: on a slow PATH (a stat per directory, about
// 90 ms for a missing program on a WSL PATH that reaches into /mnt/c) it holds
// up only the askers of that program. Askers of a program whose lookup is
// under way wait for it rather than start another.
type lookupCache struct {
	mu       sync.Mutex
	entries  map[string]lookupEntry
	inflight map[string]*lookupCall
	look     func(program string) (string, error) // exec.LookPath; tests replace it
	now      func() time.Time
}

func newLookupCache() *lookupCache {
	return &lookupCache{entries: map[string]lookupEntry{}, inflight: map[string]*lookupCall{}, look: exec.LookPath, now: time.Now}
}

// found reports whether program resolves, and to what, asking the system at
// most once per lookupTTL for each program.
func (c *lookupCache) found(program string) (string, bool) {
	c.mu.Lock()
	if e, ok := c.entries[program]; ok && c.now().Sub(e.at) < lookupTTL {
		c.mu.Unlock()
		return e.path, e.ok
	}
	call, joined := c.inflight[program]
	if !joined {
		call = c.begin(program)
	}
	c.mu.Unlock()
	if joined {
		<-call.done
	} else {
		c.run(program, call)
	}
	return call.e.path, call.e.ok
}

// check asks the system now, whatever the cache holds or has under way, and
// keeps the answer: the add-agent form's check, which the catalog then agrees
// with.
func (c *lookupCache) check(program string) (string, bool) {
	c.mu.Lock()
	call := c.begin(program)
	c.mu.Unlock()
	c.run(program, call)
	return call.e.path, call.e.ok
}

// installed reports whether a session of program could start on this server.
// A relative path with a separator (./agent.sh, bin/agent) counts without a
// lookup: exec resolves it against the session's working directory, which the
// server's own cannot stand for. A bare name is looked up on the PATH, and any
// program of that name counts; an absolute path is looked up as it is.
func (c *lookupCache) installed(program string) bool {
	if relativePath(program) {
		return true
	}
	_, ok := c.found(program)
	return ok
}

// relativePath reports whether program names a file relative to the working
// directory rather than a program on the PATH.
func relativePath(program string) bool {
	return strings.ContainsAny(program, "/"+string(filepath.Separator)) && !filepath.IsAbs(program)
}

// warm looks up the programs the cache has no fresh answer for, each once and
// at most lookupWorkers at a time, so a listing of N uncached programs takes
// about one lookup's time, not N. Relative paths are skipped (installed).
func (c *lookupCache) warm(programs []string) {
	sem := make(chan struct{}, lookupWorkers)
	var wg sync.WaitGroup
	seen := map[string]bool{}
	for _, p := range programs {
		if seen[p] || relativePath(p) || c.fresh(p) {
			continue
		}
		seen[p] = true
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			c.found(p)
		})
	}
	wg.Wait()
}

// fresh reports whether the cache holds an answer about program younger than
// lookupTTL.
func (c *lookupCache) fresh(program string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[program]
	return ok && c.now().Sub(e.at) < lookupTTL
}

// begin registers a lookup of program as the one under way, which later
// askers wait for. The caller holds c.mu.
func (c *lookupCache) begin(program string) *lookupCall {
	call := &lookupCall{done: make(chan struct{}), at: c.now(), look: c.look}
	c.inflight[program] = call
	return call
}

// run looks program up without c.mu, then keeps the answer, dropping the
// answers that have expired so the cache holds only programs asked about
// lately, and wakes the askers waiting for it. Of two lookups of one program
// at once, the one that started later decides: an earlier one that ends after
// it answers with the later one's answer.
func (c *lookupCache) run(program string, call *lookupCall) {
	path, err := call.look(program)
	e := lookupEntry{path: path, ok: err == nil, at: call.at}
	c.mu.Lock()
	defer c.mu.Unlock()
	for p, old := range c.entries {
		if e.at.Sub(old.at) >= lookupTTL {
			delete(c.entries, p)
		}
	}
	old, had := c.entries[program]
	switch {
	case had && old.at.After(e.at):
		e = old
	case had || len(c.entries) < maxLookups:
		c.entries[program] = e
	}
	if c.inflight[program] == call {
		delete(c.inflight, program)
	}
	call.e = e
	close(call.done)
}
