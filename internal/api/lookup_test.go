package api

import (
	"errors"
	"testing"
	"time"
)

// The cache asks the system once per program and again after 30 s, so a CLI
// installed while the server runs shows up without a restart; a check asks at
// once and the cache keeps its answer.
func TestLookupCacheExpires(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	asked := map[string]int{}
	present := map[string]bool{"cat": true}
	c := newLookupCache()
	c.now = func() time.Time { return now }
	c.look = func(program string) (string, error) {
		asked[program]++
		if present[program] {
			return "/bin/" + program, nil
		}
		return "", errors.New("not found")
	}
	if p, ok := c.found("cat"); !ok || p != "/bin/cat" {
		t.Fatalf("cat: %q %v", p, ok)
	}
	if _, ok := c.found("ghost"); ok {
		t.Fatal("ghost found")
	}
	c.found("cat")
	c.found("ghost")
	if asked["cat"] != 1 || asked["ghost"] != 1 {
		t.Fatalf("asked twice within the TTL: %v", asked)
	}
	present["ghost"] = true
	if p, ok := c.check("ghost"); !ok || p != "/bin/ghost" || asked["ghost"] != 2 {
		t.Fatalf("a check asks at once: %q %v %v", p, ok, asked)
	}
	if _, ok := c.found("ghost"); !ok || asked["ghost"] != 2 {
		t.Fatalf("the check's answer is kept: %v", asked)
	}
	delete(present, "cat")
	now = now.Add(lookupTTL + time.Second)
	if _, ok := c.found("cat"); ok || asked["cat"] != 2 {
		t.Fatalf("after the TTL cat should be looked up again: %v", asked)
	}
	if len(c.entries) != 1 {
		t.Fatalf("expired answers are dropped: %v", c.entries)
	}
}
