# Data Directory and Catalog Editing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the server a writable data directory and let admins add, edit and hide catalog agents from the Agents page (mockup 2c), including how each agent signals that it needs a human.

**Architecture:** A new `internal/store` package does atomic JSON file writes into `cfg.DataDir`. The catalog becomes an overlay: the read-only config catalog is loaded first, then `dataDir/catalog.json` upserts or hides entries and is the only file the API writes. Three admin routes (`POST /api/catalog`, `DELETE /api/catalog/{id}`, `POST /api/catalog/check`) back a slideover on the Agents page. The `Agent` model gains `adapter` and `signal`, which Plan B (events and adapters) consumes.

**Tech Stack:** Go stdlib (`encoding/json`, `os`, `os/exec.LookPath`), Nuxt UI (`USlideover`, `UFormField`, `USwitch`), vitest.

**Spec:** `docs/features.md` § "Round 2" (Decisions → Persistence) and mockup screen 2c.

## Global Constraints

- `AGENTS.md` rules apply: stdlib first, `DisallowUnknownFields`, argv arrays only, tokens compared with `share.Equal`, an API route needs a handler, auth, an `api_test.go` test and a client call in `useSessions.ts`.
- Catalog ids match `^[a-z0-9-]{1,32}$` (existing `idPattern`); names ≤ 60 runes, descriptions ≤ 200, command ≤ 32 args of ≤ 4096 bytes, env ≤ 32 keys.
- `dataDir` is created `0700`; files are written `0600` via temp file + rename. Never write into the config file.
- Signal kinds: `hook`, `bell`, `pattern`, `none`. `pattern` is RE2, ≤ 200 bytes, compiled at validation time.
- The server catalog is the source of truth; hosts may still run any argv with `conductor host`.

## Review Focus

1. **Two admins save the same agent id at once.** Expected: last write wins without a torn file. Pinned in Task 1 (store test writes concurrently and re-reads valid JSON).
2. **An id that shadows a built-in.** Expected: the overlay replaces the built-in for launches; deleting it restores the built-in; hiding a built-in removes it from the launch dialog but keeps running sessions. Pinned in Task 2.
3. **A command that does not exist on the server.** Expected: the check reports `found:false`, Save still works (hosts may have it), Test launch fails with the existing `start_failed`. Pinned in Task 3.
4. **A regex that never terminates or is huge.** Expected: rejected at save with `invalid_signal`; RE2 has no catastrophic backtracking, so length is the only limit. Pinned in Task 2.
5. **Data directory unwritable.** Expected: `serve` refuses to start with a clear error rather than failing on the first save. Pinned in Task 1.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/config/config.go` | `DataDir` field, env override, default derivation, validation |
| `internal/store/store.go` (new) + `store_test.go` | atomic JSON load/save in the data dir |
| `internal/catalog/catalog.go` | `Agent.Adapter`, `Agent.Signal`, `Signal` type, `Overlay`, `Upsert`, `Hide`, `Remove`, validation |
| `internal/catalog/catalog_test.go` | validation and overlay tests |
| `internal/api/catalog.go` | `POST /api/catalog`, `DELETE /api/catalog/{id}`, `POST /api/catalog/check` |
| `internal/api/server.go` | routes, catalog overlay wiring, `store` field |
| `internal/cli/serve.go` | opens the store, applies the overlay before `api.New` |
| `web/app/composables/useSessions.ts` | `AgentInfo` fields, `saveAgent`, `deleteAgent`, `hideAgent`, `checkCommand` |
| `web/app/components/AddAgentSlideover.vue` (new) | the 2c form |
| `web/app/components/ArgvInput.vue` (new) | chip input for argv |
| `web/app/utils/argv.ts` (new, +test) | `slugId`, `splitArgs` (moved from LaunchSessionModal), `joinArgv` |
| `web/app/pages/agents.vue` | Add / edit / hide buttons, signal badge |
| `docs/protocol.md`, `README.md` | route docs, dataDir docs |

---

### Task 1: Data directory and atomic store

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/store/store.go`, `internal/store/store_test.go`
- Modify: `internal/cli/serve.go`

**Interfaces:**
- Produces:
```go
// config
DataDir string `json:"dataDir"` // env CONDUCTOR_DATA_DIR; default "conductor.d" next to the config file, or ./conductor.d
func (c *Config) ResolveDataDir(configPath string) // sets DataDir when empty; called by serve before Validate
// store
type Store struct{ /* dir, mu */ }
func Open(dir string) (*Store, error)               // MkdirAll 0700; fails if not writable
func (s *Store) Load(name string, v any) (bool, error) // false when the file is absent
func (s *Store) Save(name string, v any) error       // temp file + rename, 0600
func (s *Store) Dir() string
```

- [ ] **Step 1: Failing tests** `internal/store/store_test.go`

```go
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type doc struct {
	N int `json:"n"`
}

func TestOpenCreatesDirAndRejectsUnwritable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "conductor.d")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir %v %v", fi, err)
	}
	if s.Dir() != dir {
		t.Fatalf("Dir %q", s.Dir())
	}
	if _, err := Open(filepath.Join(os.DevNull, "x")); err == nil {
		t.Fatal("expected error for unwritable parent")
	}
}

func TestLoadMissingSaveRoundTrip(t *testing.T) {
	s, _ := Open(t.TempDir())
	var d doc
	if ok, err := s.Load("catalog.json", &d); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
	if err := s.Save("catalog.json", doc{N: 7}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Load("catalog.json", &d); !ok || err != nil || d.N != 7 {
		t.Fatalf("round trip: %v %v %+v", ok, err, d)
	}
	fi, _ := os.Stat(filepath.Join(s.Dir(), "catalog.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v", fi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(s.Dir(), "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestSaveRejectsBadNames(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, name := range []string{"../x.json", "a/b.json", "x.txt", "", "X.json"} {
		if err := s.Save(name, doc{}); err == nil {
			t.Fatalf("%q accepted", name)
		}
	}
}

func TestConcurrentSavesLeaveValidJSON(t *testing.T) {
	s, _ := Open(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = s.Save("c.json", doc{N: i}) }(i)
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(s.Dir(), "c.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d doc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("torn file: %v %q", err, b)
	}
}
```
Run: `go test ./internal/store/` → FAIL (package missing).

- [ ] **Step 2: Implement `store.go`**

```go
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

var namePattern = regexp.MustCompile(`^[a-z0-9-]+\.json$`)

type Store struct {
	dir string
	mu  sync.Mutex
}

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

func (s *Store) Dir() string { return s.dir }

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
```
`CreateTemp` names the scratch file `catalog.json.<rand>.tmp`; the glob in the test catches any leftover.

- [ ] **Step 3: Config** — in `config.go` add the field after `CatalogPath` with comment `// DataDir is the writable directory for UI-managed state: catalog overlay, crews, generated hook assets.`; `applyEnv` reads `CONDUCTOR_DATA_DIR`; add:

```go
// ResolveDataDir fills DataDir from the config file location when unset:
// <dir of configPath>/conductor.d, or ./conductor.d without a config file.
func (c *Config) ResolveDataDir(configPath string) {
	if c.DataDir != "" {
		return
	}
	base := "."
	if configPath != "" {
		base = filepath.Dir(configPath)
	}
	c.DataDir = filepath.Join(base, "conductor.d")
}
```
Call it in `internal/cli/serve.go` right after `config.Load` (before the server is built), then `st, err := store.Open(cfg.DataDir)`; pass `st` to `api.New(cfg, cat, log, web, st)` (new parameter; tests pass `nil` → the API treats a nil store as read-only and returns `503 store_unavailable` on writes). Add a config test: `TestResolveDataDirDefaults` (with and without a config path; env override wins).

- [ ] **Step 4: Run** `go test ./internal/store/ ./internal/config/ ./internal/api/` → PASS. Docs: README configuration table row `dataDir | CONDUCTOR_DATA_DIR | conductor.d next to the config | UI-managed state`. Commit `config: writable data directory with an atomic JSON store`.

---

### Task 2: Catalog overlay, adapter and signal fields

**Files:**
- Modify: `internal/catalog/catalog.go`, `internal/catalog/catalog_test.go`, `internal/catalog/defaults.go` (add `Adapter`/`Signal` to built-ins: claude → `hook`, codex → `hook`, agy → `bell`, shell → `none`)

**Interfaces:**
```go
type Signal struct {
	Kind    string `json:"kind"`              // hook | bell | pattern | none
	Pattern string `json:"pattern,omitempty"` // RE2 on the last screen line, kind=pattern only
	// ToolEvents asks hook adapters to report tool use as events (chatty; off by default).
	ToolEvents bool `json:"toolEvents,omitempty"`
}
type Agent struct { …existing…; Adapter string `json:"adapter,omitempty"`; Signal *Signal `json:"signal,omitempty"`; EnvPassthrough []string `json:"envPassthrough,omitempty"` }
type Overlay struct { Agents []Agent `json:"agents"`; Hidden []string `json:"hidden,omitempty"` }
func (c *Catalog) Upsert(a Agent) error          // validate, replace or append (keeps order)
func (c *Catalog) Hide(id string) bool           // removes from List/Get; returns whether it existed
func (c *Catalog) ApplyOverlay(o Overlay) error  // Upsert each, then Hide each
func (a Agent) EffectiveSignal() Signal          // default: {Kind:"bell"} when nil
```

- [ ] **Step 1: Failing tests** (append to `catalog_test.go`)

```go
func TestSignalValidation(t *testing.T) {
	base := Agent{ID: "x", Name: "X", Command: []string{"x"}}
	ok := base
	ok.Signal = &Signal{Kind: "pattern", Pattern: `^> $`}
	if err := validate(ok); err != nil {
		t.Fatal(err)
	}
	for _, s := range []Signal{{Kind: "nope"}, {Kind: "pattern"}, {Kind: "pattern", Pattern: "("}, {Kind: "pattern", Pattern: strings.Repeat("a", 201)}, {Kind: "bell", Pattern: "x"}} {
		a := base
		a.Signal = &s
		if err := validate(a); err == nil {
			t.Fatalf("signal %+v accepted", s)
		}
	}
	if got := base.EffectiveSignal(); got.Kind != "bell" {
		t.Fatalf("default signal %+v", got)
	}
}

func TestOverlayUpsertsAndHides(t *testing.T) {
	c := Default()
	if err := c.ApplyOverlay(Overlay{
		Agents: []Agent{{ID: "claude", Name: "Claude (opus)", Command: []string{"claude", "--model", "opus"}, AllowArgs: true}, {ID: "aider", Name: "Aider", Command: []string{"aider"}}},
		Hidden: []string{"shell"},
	}); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.Get("claude"); a.Name != "Claude (opus)" || a.Command[2] != "opus" {
		t.Fatalf("upsert did not replace: %+v", a)
	}
	if _, ok := c.Get("shell"); ok {
		t.Fatal("hidden agent still visible")
	}
	if _, ok := c.Get("aider"); !ok {
		t.Fatal("new agent missing")
	}
	ids := []string{}
	for _, a := range c.List() {
		ids = append(ids, a.ID)
	}
	if ids[0] != "claude" || ids[len(ids)-1] != "aider" {
		t.Fatalf("order: %v", ids)
	}
	if err := c.Upsert(Agent{ID: "Bad ID", Name: "x", Command: []string{"x"}}); err == nil {
		t.Fatal("invalid id accepted")
	}
}
```
Run → FAIL (undefined Signal, Overlay, ApplyOverlay).

- [ ] **Step 2: Implement** the types above; extend `validate`:

```go
if a.Signal != nil {
	switch a.Signal.Kind {
	case "hook", "bell", "none":
		if a.Signal.Pattern != "" { return errors.New("signal: pattern only applies to kind=pattern") }
	case "pattern":
		if a.Signal.Pattern == "" || len(a.Signal.Pattern) > 200 { return errors.New("signal: pattern required, at most 200 bytes") }
		if _, err := regexp.Compile(a.Signal.Pattern); err != nil { return fmt.Errorf("signal: %w", err) }
	default:
		return fmt.Errorf("signal: unknown kind %q", a.Signal.Kind)
	}
}
if len(a.EnvPassthrough) > 32 { return errors.New("too many envPassthrough entries") }
```
`Hide` deletes from `agents` and `order`. `Upsert` validates, then replaces in place if present (keep position) else appends. `Redacted()` keeps `Adapter`/`Signal`/`EnvPassthrough` (they are not secrets). Update `defaults.go` with adapters and signals for the four built-ins (full list of new built-ins lands in Plan B).

- [ ] **Step 3: Run** `go test ./internal/catalog/` → PASS. Commit `catalog: overlay, adapter and signal fields`.

---

### Task 3: Catalog API routes

**Files:**
- Modify: `internal/api/catalog.go`, `internal/api/server.go`, `internal/api/api_test.go`, `internal/cli/serve.go`, `docs/protocol.md` (HTTP routes section, add if missing), `web/app/composables/useSessions.ts`

**Interfaces:**
- `POST /api/catalog` body `catalog.Agent` (DisallowUnknownFields) → 200 `{agent}`; 400 `invalid_agent` with the validate message; 503 `store_unavailable` when no store.
- `DELETE /api/catalog/{id}` → 204; removes the overlay entry if one exists, otherwise hides the built-in; 404 when unknown.
- `POST /api/catalog/check` body `{"command": ["aider", "--model", "x"]}` → 200 `{"found": true, "path": "/usr/local/bin/aider"}` or `{"found": false}`; only `command[0]` is looked up (`exec.LookPath`, absolute paths allowed); never runs it.
- Server: `s.store *store.Store`; `s.catalogMu sync.Mutex`; overlay persisted as `catalog.json` (`catalog.Overlay`) after every change; `s.catalog` is replaced atomically (value copy under mutex, `List` readers see a consistent snapshot).
- Client: `saveAgent(a: AgentInput)`, `deleteAgent(id)`, `checkCommand(argv)`; `AgentInfo` gains `adapter?`, `signal?`, `envPassthrough?`, `env?` (redacted values already omitted server-side: check `Redacted()` and keep keys with value `""` so the UI can show "set on server").

- [ ] **Step 1: Failing test** (append to `api_test.go`)

```go
func TestCatalogEditingPersistsOverlay(t *testing.T) {
	e := newTestEnv(t, nil) // newTestEnv now opens a store in t.TempDir(); see Task 1 Step 3
	body := map[string]any{"id": "aider", "name": "Aider", "command": []string{"aider", "--no-auto-commits"}, "allowArgs": true, "signal": map[string]any{"kind": "pattern", "pattern": `^> $`}}
	resp, out := e.do("POST", "/api/catalog", adminToken, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %v", resp.StatusCode, out)
	}
	_, list := e.do("GET", "/api/catalog", adminToken, nil)
	var found bool
	for _, raw := range list["agents"].([]any) {
		a := raw.(map[string]any)
		if a["id"] == "aider" && a["signal"].(map[string]any)["kind"] == "pattern" {
			found = true
		}
	}
	if !found {
		t.Fatalf("saved agent missing from catalog: %v", list)
	}
	// Persisted: the overlay file names the agent.
	var ov catalog.Overlay
	if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != 1 || ov.Agents[0].ID != "aider" {
		t.Fatalf("overlay: %v %v %+v", ok, err, ov)
	}
	// Hiding a built-in.
	if resp, _ := e.do("DELETE", "/api/catalog/cat", adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("hide: %d", resp.StatusCode)
	}
	if resp, _ := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "cat"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("hidden agent still launchable: %d", resp.StatusCode)
	}
	// Validation and unknown fields.
	if resp, out := e.do("POST", "/api/catalog", adminToken, map[string]any{"id": "bad id", "name": "x", "command": []string{"x"}}); resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "invalid_agent" {
		t.Fatalf("bad id: %d %v", resp.StatusCode, out)
	}
	if resp, _ := e.do("POST", "/api/catalog", adminToken, map[string]any{"id": "x", "name": "x", "command": []string{"x"}, "bogus": 1}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", resp.StatusCode)
	}
	// Auth.
	if resp, _ := e.do("POST", "/api/catalog", "", body); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous save: %d", resp.StatusCode)
	}
}

func TestCatalogCheckCommand(t *testing.T) {
	e := newTestEnv(t, nil)
	_, out := e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{"/bin/sh", "-c", "x"}})
	if out["found"] != true || out["path"] != "/bin/sh" {
		t.Fatalf("sh: %v", out)
	}
	_, out = e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{"definitely-not-a-real-binary-xyz"}})
	if out["found"] != false {
		t.Fatalf("missing: %v", out)
	}
	if resp, _ := e.do("POST", "/api/catalog/check", adminToken, map[string]any{"command": []string{}}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty argv: %d", resp.StatusCode)
	}
}
```
`newTestEnv` gets `st, _ := store.Open(filepath.Join(t.TempDir(), "data"))` and passes it to `New`. Run → FAIL (404s).

- [ ] **Step 2: Implement** handlers in `catalog.go`:

```go
func (s *Server) handleSaveAgent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil { writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory"); return }
	var a catalog.Agent
	if err := decodeJSON(w, r, &a); err != nil { writeError(w, http.StatusBadRequest, "invalid_request", err.Error()); return }
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	next := s.catalog.Clone()
	if err := next.Upsert(a); err != nil { writeError(w, http.StatusBadRequest, "invalid_agent", err.Error()); return }
	ov := s.overlay
	ov.Agents = upsertOverlay(ov.Agents, a)
	ov.Hidden = without(ov.Hidden, a.ID)
	if err := s.store.Save("catalog.json", ov); err != nil { writeError(w, http.StatusInternalServerError, "store_failed", "could not save the catalog"); return }
	s.overlay, s.catalog = ov, next
	saved, _ := next.Get(a.ID)
	writeJSON(w, http.StatusOK, map[string]any{"agent": saved.Redacted()})
}
```
`handleDeleteAgent`: if the id is in `overlay.Agents` remove it (and rebuild the catalog from config + overlay via `s.rebuildCatalog()`), else if the id exists add to `Hidden`; 404 otherwise; save; 204. `handleCheckCommand`: decode `{command []string}`, require ≥ 1 arg, `exec.LookPath(argv[0])`. `Catalog.Clone()` is a shallow copy of maps/slices (add to catalog.go). `rebuildCatalog` = `cfg.LoadCatalog()` then `ApplyOverlay(overlay)`. Routes in `server.go`; `serve.go` loads the overlay at startup and applies it before `api.New` (or `api.New` does it from the store: choose `api.New` so tests cover it).

- [ ] **Step 3: Run** `go test -race ./internal/api/` → PASS. Docs: add a "HTTP API" table to `docs/protocol.md` if absent, with the three routes; README: "Agents page → Add agent". Commit `api: catalog editing routes persisted in the data directory`.

---

### Task 4: Add-agent slideover and Agents page

**Files:**
- Create: `web/app/utils/argv.ts`, `web/app/utils/argv.test.ts`, `web/app/components/ArgvInput.vue`, `web/app/components/AddAgentSlideover.vue`
- Modify: `web/app/pages/agents.vue`, `web/app/components/LaunchSessionModal.vue` (import `splitArgs` from utils), `web/app/composables/useSessions.ts`

**Interfaces:**
```ts
// utils/argv.ts
export function splitArgs(s: string): string[]      // moved from LaunchSessionModal, unchanged behaviour
export function slugId(name: string): string        // 'Claude (opus)' → 'claude-opus', ≤ 32 chars, [a-z0-9-]
export function joinArgv(argv: string[]): string    // display with shellQuote from hostCommand.ts
// useSessions.ts
export interface AgentSignal { kind: 'hook' | 'bell' | 'pattern' | 'none'; pattern?: string; toolEvents?: boolean }
export interface AgentInput { id: string; name: string; description?: string; command: string[]; allowArgs: boolean; env?: Record<string, string>; envPassthrough?: string[]; cwd?: string; icon?: string; adapter?: string; signal?: AgentSignal }
saveAgent(a: AgentInput): Promise<AgentInfo>; deleteAgent(id: string): Promise<void>; checkCommand(argv: string[]): Promise<{ found: boolean; path?: string }>
```
- `ArgvInput`: `v-model: string[]`; each chip is one argv element; typing then Enter or Space-outside-quotes adds a chip (use `splitArgs` on paste); Backspace on empty input removes the last chip.
- `AddAgentSlideover` props `{ agent?: AgentInfo }` (edit mode when set), `v-model:open`, emits `saved(agent)`. Fields as mockup 2c: Name, ID (auto-slug while untouched), Command chips + live check line ("Found on the server: /usr/local/bin/aider" / "Not found on the server; hosts may still have it", debounced 400 ms via `checkCommand`), Description, Environment rows (key + value; empty value = "pass through from server" → goes to `envPassthrough`), Accept extra arguments switch, "How does it tell Conductor it needs you?" four cards (Hook command: disabled with hint "pick an adapter" until Plan B; Bell / OSC 9·777 default; Screen pattern with a regex field; None), footer Cancel / Test launch (saves then `create({agentId})` and navigates) / Save to catalog.
- `agents.vue`: header buttons Add agent + Launch agent; cards get a signal badge (`hooks: n` / `bell / OSC` / `pattern` / `none`), an Edit (opens slideover) and a Hide action with confirm.

- [ ] **Step 1: Failing test** `argv.test.ts`

```ts
import { describe, expect, it } from 'vitest'
import { joinArgv, slugId, splitArgs } from './argv'
describe('slugId', () => {
  it('lowercases, dashes and trims to 32', () => {
    expect(slugId('Claude (opus)')).toBe('claude-opus')
    expect(slugId('  Aider ')).toBe('aider')
    expect(slugId('x'.repeat(40))).toHaveLength(32)
    expect(slugId('!!!')).toBe('')
  })
})
describe('splitArgs', () => {
  it('splits on whitespace and honours quotes', () => {
    expect(splitArgs(`aider --model "gpt 5" 'a b'`)).toEqual(['aider', '--model', 'gpt 5', 'a b'])
  })
})
describe('joinArgv', () => {
  it('quotes what needs quoting', () => {
    expect(joinArgv(['sh', '-c', 'echo hi'])).toBe("sh -c 'echo hi'")
  })
})
```
Run → FAIL. **Step 2:** implement (`slugId`: `name.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 32)`). **Step 3:** components and page as specified; typecheck. **Step 4:** browser check with the headless recipe in memory: open Agents, add `aider` with a pattern, see it in the launch dialog, hide `shell`, reload, still hidden. Commit `web: add-agent slideover and catalog editing`.

---

## Verification

1. `make lint && go test -race -count=1 ./... && npm --prefix web run typecheck && npm --prefix web test && make web-build && make build-go`.
2. Start `bin/conductor serve --config conductor.example.json`: `conductor.d/` appears next to the config; add an agent from the UI; `cat conductor.d/catalog.json` shows it; restart; the agent is still there and launchable.
3. `CONDUCTOR_DATA_DIR=/nonexistent/ro bin/conductor serve` exits with "not writable".
