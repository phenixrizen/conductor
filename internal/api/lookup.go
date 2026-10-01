package api

import (
	"os/exec"
	"sync"
	"time"
)

// lookupTTL is how long an answer about a program is kept: a CLI installed
// meanwhile is seen at most this long after.
const lookupTTL = 30 * time.Second

// maxLookups bounds the answers kept at once. The catalog's agents need a few
// dozen; past the bound an answer is given but not kept.
const maxLookups = 256

type lookupEntry struct {
	path string
	ok   bool
	at   time.Time
}

// lookupCache answers whether programs resolve on this server, the way exec
// resolves them when a session launches (exec.LookPath, the check of POST
// /api/catalog/check), keeping each answer for lookupTTL. GET /api/catalog
// marks every agent with it and a crew launch refuses a member whose program
// it cannot find. Nothing is run.
type lookupCache struct {
	mu      sync.Mutex
	entries map[string]lookupEntry
	look    func(program string) (string, error) // exec.LookPath; tests replace it
	now     func() time.Time
}

func newLookupCache() *lookupCache {
	return &lookupCache{entries: map[string]lookupEntry{}, look: exec.LookPath, now: time.Now}
}

// found reports whether program resolves, and to what, asking the system at
// most once per lookupTTL for each program.
func (c *lookupCache) found(program string) (string, bool) {
	c.mu.Lock()
	e, ok := c.entries[program]
	c.mu.Unlock()
	if ok && c.now().Sub(e.at) < lookupTTL {
		return e.path, e.ok
	}
	return c.ask(program)
}

// check asks the system now, whatever the cache holds, and keeps the
// answer: the add-agent form's check, which the catalog then agrees with.
func (c *lookupCache) check(program string) (string, bool) {
	return c.ask(program)
}

// ask looks program up and keeps the answer, dropping the answers that have
// expired so the cache holds only programs asked about lately. The lookup runs
// without c.mu: on a slow PATH (a stat per directory) it holds up the request
// that asked and no other. Of two lookups of one program at once, the one that
// started later is kept.
func (c *lookupCache) ask(program string) (string, bool) {
	at := c.now()
	path, err := c.look(program)
	e := lookupEntry{path: path, ok: err == nil, at: at}
	c.mu.Lock()
	defer c.mu.Unlock()
	for p, old := range c.entries {
		if at.Sub(old.at) >= lookupTTL {
			delete(c.entries, p)
		}
	}
	old, had := c.entries[program]
	switch {
	case had && old.at.After(at):
		return e.path, e.ok
	case had || len(c.entries) < maxLookups:
		c.entries[program] = e
	}
	return e.path, e.ok
}
