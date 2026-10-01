package api

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/crew"
)

// stub replaces the system lookup and forgets every answer, under the cache's
// lock, so a running server sees the change safely.
func (c *lookupCache) stub(look func(program string) (string, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.look = look
	c.entries = map[string]lookupEntry{}
}

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

// Askers of one program at once wait for one lookup: those that come while it
// runs share its answer, and those that come after find it kept.
func TestLookupCacheCoalescesConcurrentAskers(t *testing.T) {
	c := newLookupCache()
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	c.look = func(program string) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return "/bin/" + program, nil
	}
	var wg sync.WaitGroup
	found := make([]bool, 10)
	for i := range found {
		wg.Go(func() { _, found[i] = c.found("slow") })
	}
	<-started
	time.Sleep(50 * time.Millisecond) // most askers reach the lookup in flight; a later one finds the answer kept
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d lookups for 10 askers of one program", n)
	}
	for i, ok := range found {
		if !ok {
			t.Fatalf("asker %d: not found", i)
		}
	}
}

// At most maxLookups answers are kept: past the bound an answer is given and
// not kept, nothing is evicted, and a program already kept is still
// refreshed. Answers that expire make room.
func TestLookupCacheKeepsAtMostMaxLookups(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	asked := map[string]int{}
	c := newLookupCache()
	c.now = func() time.Time { return now }
	c.look = func(program string) (string, error) {
		asked[program]++
		return "/bin/" + program, nil
	}
	for i := range maxLookups {
		c.found(fmt.Sprintf("p%d", i))
	}
	if len(c.entries) != maxLookups {
		t.Fatalf("%d answers kept, want %d", len(c.entries), maxLookups)
	}
	if p, ok := c.found("extra"); !ok || p != "/bin/extra" {
		t.Fatalf("the answer past the bound: %q %v", p, ok)
	}
	c.found("extra")
	if asked["extra"] != 2 || len(c.entries) != maxLookups {
		t.Fatalf("the answer past the bound was kept: asked %d, %d kept", asked["extra"], len(c.entries))
	}
	now = now.Add(time.Second)
	c.check("p0")
	if c.entries["p0"].at != now {
		t.Fatalf("a program already kept is not refreshed at the bound: %v", c.entries["p0"])
	}
	now = now.Add(lookupTTL)
	c.found("extra")
	if _, kept := c.entries["extra"]; !kept || len(c.entries) != 1 {
		t.Fatalf("expired answers do not make room: %d kept", len(c.entries))
	}
}

// A lookup that started earlier and ends later does not replace the answer
// of one that started after it: a found under way when the CLI is installed
// does not undo the add-agent form's check that sees it.
func TestLookupCacheKeepsTheNewerAnswer(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	c := newLookupCache()
	c.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	c.look = func(program string) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return "", errors.New("not found") // from before the install
		}
		return "/bin/" + program, nil
	}
	older := make(chan bool)
	go func() {
		_, ok := c.found("agent")
		older <- ok
	}()
	<-started
	mu.Lock()
	now = now.Add(time.Second)
	mu.Unlock()
	if p, ok := c.check("agent"); !ok || p != "/bin/agent" {
		t.Fatalf("check: %q %v", p, ok)
	}
	close(release)
	if !<-older {
		t.Fatal("the older found answered with its own, older answer")
	}
	if _, ok := c.found("agent"); !ok || calls.Load() != 2 {
		t.Fatalf("the check's answer was replaced: %v, %d lookups", ok, calls.Load())
	}
}

// A program named by a relative path with a separator (./agent.sh, bin/agent)
// counts as installed without a lookup: it resolves against the session's
// directory, which the server's own cannot stand for. A bare name and an
// absolute path are looked up.
func TestLookupCacheInstalledTakesARelativePath(t *testing.T) {
	c := newLookupCache()
	var asked []string
	c.look = func(program string) (string, error) {
		asked = append(asked, program)
		return "", errors.New("not found")
	}
	for _, p := range []string{"./x", "sub/x", "../x"} {
		if !c.installed(p) {
			t.Fatalf("%s: not installed", p)
		}
	}
	for _, p := range []string{"x", "/abs/x"} {
		if c.installed(p) {
			t.Fatalf("%s: installed", p)
		}
	}
	if len(asked) != 2 {
		t.Fatalf("looked up %q", asked)
	}
}

// The catalog and a crew launch agree on a relative program: listed as
// available, and not refused.
func TestCheckLaunchTakesARelativeProgram(t *testing.T) {
	e := newTestEnv(t, nil)
	for id, program := range map[string]string{"dot": "./x", "sub": "sub/x"} {
		a := agentBody(id)
		a["command"] = []string{program}
		if out := e.save(a); out["agent"].(map[string]any)["available"] != true {
			t.Fatalf("%s: %v", program, out)
		}
		if got := e.catalogAgent(id); got["available"] != true {
			t.Fatalf("%s listed: %v", program, got)
		}
		if err := checkLaunch([]crew.Member{{Name: "m", AgentID: id}}, e.srv.Catalog(), e.srv.installed); err != nil {
			t.Fatalf("%s: %v", program, err)
		}
	}
}

// A listing looks up the programs it has no answer for at once, at most
// lookupWorkers at a time, each once: a catalog of N uncached programs takes
// about one lookup's time, not N.
func TestCatalogLooksUpConcurrently(t *testing.T) {
	e := newTestEnv(t, nil)
	for i := range 8 {
		a := agentBody(fmt.Sprintf("slow-%d", i))
		a["command"] = []string{fmt.Sprintf("slow-program-%d", i)}
		e.save(a)
	}
	const delay = 250 * time.Millisecond
	var calls, running, most atomic.Int32
	e.srv.lookups.stub(func(program string) (string, error) {
		calls.Add(1)
		n := running.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		time.Sleep(delay)
		running.Add(-1)
		return "/bin/" + program, nil
	})
	start := time.Now()
	list := e.catalogList()
	took := time.Since(start)
	programs := map[string]bool{}
	for _, raw := range list {
		a := raw.(map[string]any)
		programs[a["command"].([]any)[0].(string)] = true
		if a["available"] != true {
			t.Fatalf("%v: not available", a["id"])
		}
	}
	if int(calls.Load()) != len(programs) {
		t.Fatalf("%d lookups for %d programs", calls.Load(), len(programs))
	}
	if m := most.Load(); m < 2 || m > lookupWorkers {
		t.Fatalf("%d lookups ran at once, want 2 to %d", m, lookupWorkers)
	}
	if serial := time.Duration(len(programs)) * delay; took > serial/2 {
		t.Fatalf("listing %d uncached programs took %v; one lookup takes %v, all in a row %v", len(programs), took, delay, serial)
	}
}
