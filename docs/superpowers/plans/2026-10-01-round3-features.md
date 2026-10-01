# Round 3 Features Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The round 3 features: a working-directory picker fed by the server with a git check, four example crews seeded once, shell completion for the CLI, a sidebar that collapses to an icon rail instead of disappearing, document fullscreen on every page, and agent availability (which catalog agents are installed on the server) on the Agents page, the Launch dialog, the crew editor and the crew launch.

**Architecture:** Two small admin routes (`GET /api/paths`, `GET /api/git/check`) reuse `resolveCwd` for confinement and the crew package's argv `git` calls for the git state; the picker (`DirInput`) and the crew editor read them. Example crews are plain data (`crew.Examples`) saved through a once-only `Store.Seed`, reached from `conductor serve --examples`, `CONDUCTOR_EXAMPLES=1` and `POST /api/crews/examples`. Completion scripts are generated in `internal/cli` from a static table of the subcommands and flags, and read crew ids live from `conductor crews --ids`. On the client, the sidebar's "hidden" flag becomes a two-mode state (`full` | `rail`) rendered through Nuxt UI's `UDashboardSidebar` collapse; fullscreen becomes shared state with one `FullscreenButton` component and one shortcut registration per layout. Availability is the `exec.LookPath` check `POST /api/catalog/check` already runs, behind a 30 s per-program cache on the server (`lookupCache`), reported as `available` on `GET /api/catalog` next to a new `site` field, and consulted by the crew launch's `checkLaunch`.

**Tech Stack:** Go stdlib (`net/http` mux, `os/exec` with argv only, `flag`), Nuxt 4 + Nuxt UI 4 (`UDashboardSidebar` collapse, `UNavigationMenu` collapsed, `UTooltip`, `UInput`), vitest for pure utilities, headless Chromium through the playwright-core already installed in the scratchpad. No new dependency.

**Spec:** `docs/features.md` § "Round 3: polish, examples and completion (planned 2026-10-01)" → "Decisions" (the seven bullets: sidebar rail, example crews, working-directory autocomplete, git check, CLI completion, fullscreen everywhere, agent availability). The "Deferred items" and "Crew storage" parts of that section are plan 1 of round 3, which runs **before** this plan and is assumed done here: crews live one per file at `conductor.d/crews/<id>.json`; `GET /api/crews?offset=0&limit=100` answers `{crews: [summaries], total}` (limit at most 500) and `GET /api/crews/{id}` answers `{crew}`; the crew store keeps `Get`, `Put`, `Create`, `Update`, `Duplicate`, `Delete`, `List`, its mutex `mu`, its `crews` map and a per-crew `commit`; `inRepo` reports `rev-parse` failures other than "not a repository" with git's message; `api_test.go` is split by route family (crews and runs in `internal/api/crews_test.go`), the helpers `newTestEnv`, `e.do`, `adminToken`, `errorCode` staying in `api_test.go`. Where a step below touches plan 1's code it says so and names the contract it relies on.

## Global Constraints

Copied from the spec and `AGENTS.md`; every task's requirements include them.

- `GET /api/paths?prefix=<path>&limit=50` (admin) lists child directories of the longest existing directory in the prefix, confined to `allowedRoots` through the same resolution as `resolveCwd` (symlinks resolved, nothing outside a root), at most 50 entries, hidden directories only when the typed segment starts with a dot, each entry with `git: {repo, commits}`.
- `GET /api/git/check?cwd=<path>` (admin) answers `{inRepo, toplevel, hasCommit, message}` with the same rules the launch uses (`git rev-parse` as argv, under the allowed roots); the launch handler's 409 stays as the authority.
- Server session working directories go through `resolveCwd`; file reads go through `session.ResolvePath`. Do not bypass either.
- Commands are argv arrays. Never build a shell string from user input. (`git` runs as `exec.CommandContext(ctx, "git", "-C", dir, args...)` through `crew.git`; the completion scripts never interpolate a crew id into shell text — ids are data passed to `compadd`/`compgen`.)
- Keep per-connection bounds: `prefix` and `cwd` queries are at most 4096 bytes without NUL; a listing is at most 50 entries; the git calls of one request share a 5 s deadline.
- Example crews: ids `example-todo-app`, `example-test-fixer`, `example-docs-writer`, `example-dependency-upgrade`; a crew whose id exists is left alone; members use the `claude` and `codex` built-ins; `cwd` is the server's default working directory; isolation `worktree`. The examples are data, not built-ins: editing or deleting them is normal.
- CLI completion: `conductor crews --ids` prints ids only, one per line, exit 0 and silent when the server is unreachable or the token is missing, using `CONDUCTOR_SERVER` and `CONDUCTOR_ADMIN_TOKEN` from the environment. `conductor completion install` appends one marked `source` line to `~/.zshrc` or `~/.bashrc` (idempotent; refuses a file not owned by the user). No new dependency.
- `internal/cli` holds flags only; no business logic (the completion generator and the rc-file edit are CLI plumbing, the seeding and the git state are not and live in `internal/crew` and `internal/api`).
- Stdlib first: `net/http` mux with method patterns, `log/slog`, `encoding/json` with `DisallowUnknownFields`. New Go dependencies need a reason in the PR (there are none here).
- An API route needs: handler in `internal/api`, auth via `requireAdmin` or `authenticate`, a test in the api test package, and the client call in `web/app/composables/useSessions.ts`.
- Sidebar rail: the rail keeps every function; `meta+B` / Alt+B toggles rail and full width; the choice is persisted per browser as before; narrow screens keep the slideover.
- Fullscreen: one button in every page header and one shortcut registration; the wall and carousel keep their buttons; `F` is ignored while an input or textarea has focus, as the other letter shortcuts already are.
- Agent availability: `GET /api/catalog` reports `available` per agent (whether `command[0]` resolves on the server through the same check `POST /api/catalog/check` uses, re-evaluated per request and cached for 30 s) and `site` (an optional `https` URL). The Launch dialog's server tab lists only available agents; the "My machine" tab lists every agent because availability there is the host's; the launch route refuses a crew whose member agent is not installed with `invalid_crew` naming it.
- UI components come from Nuxt UI; use the brand palette in `web/app/app.config.ts` and `docs/design/brand.md`. The junction mark is artwork, never a status light (the rail shows the mark as an image and the attention dot on the avatars, never on the mark).
- Do not commit `internal/web/dist` contents (only `.gitkeep`), `web/.nuxt` or `web/.output`. Commit `web/package-lock.json`.
- Checks before finishing: `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test`, `make web-build`, `make build-go`. A check that cannot run is a reported limitation, not a pass.

## Review Focus

The five inputs most likely to bite a person, each pinned to the task whose tests exercise it.

1. **A prefix that walks through a symbolic link to a directory outside the roots** (`<root>/escape/…`, `escape -> /elsewhere`). Expected: `200` listing from the longest allowed ancestor, the link itself never listed and the target's children never shown; a prefix wholly outside (`/etc`, `<root>/..`) is `400 invalid_cwd`. Pinned in Task 1 (`TestPathsNeverLeaveTheRoots`).
2. **A server (or a man in the middle on `CONDUCTOR_SERVER`) that answers `conductor crews --ids` with an id carrying spaces, quotes or control characters.** Expected: the id is dropped, the valid ones printed, exit 0; nothing a shell would re-read as syntax ever reaches the command line. Pinned in Task 4 (`TestCrewsIDsPrintsOnlyWellFormedIDs`).
3. **`conductor completion install` under `sudo`, where `~/.zshrc` belongs to another user.** Expected: refused with the owner named, nothing written. Pinned in Task 4 (`TestCheckOwnerRefusesAnotherUsersFile` in `internal/agents`, and the install path that calls it).
4. **A browser that still holds the old `conductor.sidebar.hidden=1` key from before the rail.** Expected: the first load shows the rail (hidden became the rail), the old key is removed and `conductor.sidebar.mode=rail` written, and a later `meta+B` works as usual. Pinned in Task 5 (`readSidebarMode` vitest and the headless check).
5. **Pressing `f` while typing a crew name, a goal, a reply or the session filter.** Expected: the letter is typed and nothing goes fullscreen; `f` on the page body toggles it. Pinned in Task 6 (headless check `fullscreen-check.js`).

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/api/paths.go` (new), `paths_test.go` (new) | `GET /api/paths`, `GET /api/git/check`: prefix splitting, confined listing, git marks |
| `internal/crew/worktree.go`, `worktree_test.go` | `GitState` (the launch's repo rules as one answer), `toplevel` |
| `internal/crew/examples.go` (new), `examples_test.go` (new) | the four example crews as data |
| `internal/crew/store.go`, `crew_test.go` | `Store.Seed`: once-only per id |
| `internal/api/crews.go`, `crews_test.go`, `server.go` | `POST /api/crews/examples`, `SeedExampleCrews`, routes |
| `internal/config/config.go`, `config_test.go` | `Examples` from `CONDUCTOR_EXAMPLES` |
| `internal/cli/serve.go`, `serve_test.go` | `--examples` |
| `internal/cli/completion.go` (new), `completion_test.go` (new) | `conductor completion zsh|bash|install`: the static table, the two generators, the rc line |
| `internal/cli/up.go`, `up_test.go`, `root.go` | `conductor crews --ids`, dispatch and usage |
| `internal/agents/merge.go`, `owner_test.go` | `CheckOwner` (ownership of any path) |
| `web/app/utils/dirInput.ts` (new, +test), `web/app/components/DirInput.vue` (new) | the picker's pure parts and the combobox |
| `web/app/components/CrewEditor.vue`, `LaunchSessionModal.vue` | use `DirInput`; the editor's git line and launch tooltip |
| `web/app/utils/sidebar.ts` (new, +test), `web/app/composables/useSidebar.ts`, `web/app/components/SidebarRail.vue` (new), `web/app/layouts/default.vue` | rail mode, migration, rail rendering |
| `web/app/components/SidebarReveal.vue` (deleted) and its seven users | the reveal button is replaced by the rail's expand button |
| `web/app/composables/useFullscreenToggle.ts`, `web/app/components/FullscreenButton.vue` (new), `web/app/layouts/bare.vue`, every page header | fullscreen everywhere |
| `web/app/composables/useShortcuts.ts` (+test) | the `F` row under Everywhere, once |
| `internal/api/lookup.go` (new, +test), `internal/api/catalog.go`, `runs.go` | the 30 s program lookup cache, `available` on the catalog listing, the launch refusal |
| `internal/catalog/catalog.go`, `defaults.go` | `Site` on agents, validated; the built-ins' sites |
| `web/app/utils/agents.ts` (new, +test), `web/app/composables/useServerHost.ts` (new) | availability helpers for the three surfaces; the server's host name, read once |
| `web/app/pages/agents.vue`, `components/AddAgentSlideover.vue`, `LaunchSessionModal.vue`, `CrewMembersTable.vue` | greyed cards with the note and the site link; the `site` field; the filtered server tab; the marked select |
| `web/app/composables/useSessions.ts` | `listPaths`, `gitCheck`, `loadExampleCrews`, `available`/`site` and their types |
| `web/app/pages/crews/[[id]].vue` | "Load the examples" on the empty page |
| `docs/protocol.md`, `README.md`, `docs/features.md` | docs |

Tasks 1–4 are independent of each other; Task 2 needs Task 1's routes; Task 7 touches `LaunchSessionModal.vue` and `CrewEditor.vue` after Task 2 did; Task 8 documents everything. Run them in order.

Headless checks use the Playwright already installed at `/tmp/claude-1000/-home-nater-go-src-github-com-phenixrizen-conductor/a75c70c0-928a-4c03-ab5e-19119aca1b6c/scratchpad/pw` (`playwright-core`; Chromium under `~/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`; launch with `args: ['--no-sandbox', '--use-gl=swiftshader']`; navigate with `waitUntil: 'commit'` then wait for a selector). The test server for them is `bin/conductor serve --config conductor.example.json --listen 127.0.0.1:8099` after `make web-build && make build-go` (admin token `dev-admin-token-change-me`, allowed root `.` = the repository, so a temporary directory tree for the picker goes under `./tmp-picker/` and is removed afterwards). Kill the test server by pid, never `pkill -f conductor`.

---

### Task 1: `GET /api/paths` and `GET /api/git/check`

**Files:**
- Create: `internal/api/paths.go`, `internal/api/paths_test.go`
- Modify: `internal/crew/worktree.go:48-62` (`inRepo` → `toplevel` + `GitState`), `internal/crew/worktree_test.go`, `internal/api/server.go:472` (routes), `web/app/composables/useSessions.ts:253` (client calls and types)

**Interfaces:**
- Consumes: `(*Server).resolveCwd(cwd string) (string, error)` (`internal/api/sessions.go:231`), `crew.git`, `crew.headCommit`, `crew.ErrNoGit`, `crew.ErrNotRepo`, `errNotInRepo`, `errNoCommit` (`internal/crew/worktree.go`), `requireAdmin`, `writeJSON`, `writeError`, `errorCode` and `newTestEnv`/`e.do`/`adminToken` from `api_test.go`.
- Produces:
```go
// internal/crew/worktree.go
type State struct {
	InRepo    bool   // dir is in a git working tree
	Toplevel  string // its top, when InRepo
	HasCommit bool   // HEAD is a commit to branch from
	Message   string // the words a launch's refusal uses, or that worktrees can be made
}
func GitState(ctx context.Context, dir string) (State, error) // error: ErrNoGit, or ctx's
func toplevel(ctx context.Context, dir string) (string, error) // git -C dir rev-parse --show-toplevel; ErrNoGit / matches ErrNotRepo
// internal/api/paths.go
type pathGit struct{ Repo, Commits bool }                 // json repo, commits
type pathEntry struct{ Name, Path string; Git pathGit }   // json name, path, git
type pathsReply struct{ Dir string; Entries []pathEntry; Truncated bool }
type gitCheckReply struct{ InRepo bool; Toplevel string; HasCommit bool; Message string } // json inRepo, toplevel (omitempty), hasCommit, message
const maxPathEntries = 50
func (s *Server) splitPrefix(prefix string) (dir, seg string, err error)
func (s *Server) listPaths(ctx context.Context, prefix string, limit int) (pathsReply, error)
func (s *Server) handleListPaths(w http.ResponseWriter, r *http.Request)  // GET /api/paths (admin)
func (s *Server) handleGitCheck(w http.ResponseWriter, r *http.Request)   // GET /api/git/check (admin)
```
```ts
// web/app/composables/useSessions.ts
export interface PathGit { repo: boolean; commits: boolean }
export interface PathEntry { name: string; path: string; git: PathGit }
export interface PathsReply { dir: string; entries: PathEntry[]; truncated: boolean }
export interface GitCheck { inRepo: boolean; toplevel?: string; hasCommit: boolean; message: string }
listPaths: (prefix: string, limit?: number) => Promise<PathsReply>
gitCheck: (cwd: string) => Promise<GitCheck>
```

- [ ] **Step 1: Write the failing crew test for `GitState`** in `internal/crew/worktree_test.go` (the file's `newRepo`, `runGit`, `writeFile` helpers exist):

```go
// GitState answers in one call what Launch checks with worktrees: in a
// repository with a commit, in one without, or in no repository, each with
// the words the launch's refusal would use.
func TestGitStateReportsRepoCommitAndPlain(t *testing.T) {
	repo := newRepo(t)
	st, err := GitState(t.Context(), repo)
	if err != nil || !st.InRepo || st.Toplevel != repo || !st.HasCommit || st.Message == "" {
		t.Fatalf("repo: %+v %v", st, err)
	}
	sub := filepath.Join(repo, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if st, _ := GitState(t.Context(), sub); !st.InRepo || st.Toplevel != repo || !st.HasCommit {
		t.Fatalf("below the top: %+v", st)
	}
	empty := t.TempDir()
	runGit(t, empty, "init", "-q")
	if st, err := GitState(t.Context(), empty); err != nil || !st.InRepo || st.HasCommit || !strings.Contains(st.Message, "without a commit") {
		t.Fatalf("no commit: %+v %v", st, err)
	}
	if st, err := GitState(t.Context(), t.TempDir()); err != nil || st.InRepo || st.HasCommit || !strings.Contains(st.Message, "not in a git repository") {
		t.Fatalf("plain: %+v %v", st, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/crew -run TestGitStateReportsRepoCommitAndPlain`
Expected: FAIL to compile, `undefined: GitState`.

- [ ] **Step 3: Implement `toplevel` and `GitState`** in `internal/crew/worktree.go`, replacing `inRepo` (keep plan 1's classification of other `rev-parse` failures inside `toplevel`):

```go
// toplevel is the top of the git working tree dir is in: git -C dir
// rev-parse --show-toplevel. The error is ErrNoGit when git is not on PATH,
// ctx's when ctx ended, and matches ErrNotRepo otherwise.
func toplevel(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return "", ctx.Err()
		case errors.Is(err, exec.ErrNotFound):
			return "", ErrNoGit
		}
		return "", errNotInRepo
	}
	return strings.TrimSpace(out), nil
}

// inRepo checks that dir is in a git working tree (see toplevel).
func inRepo(ctx context.Context, dir string) error {
	_, err := toplevel(ctx, dir)
	return err
}

// State is what GitState reports of a directory.
type State struct {
	InRepo    bool
	Toplevel  string
	HasCommit bool
	Message   string
}

// msgCanWorktree is State.Message when worktrees can be made.
const msgCanWorktree = "a git repository with a commit: a crew with worktrees can launch here"

// GitState reports, in one answer, what Launch checks of a crew's working
// directory with isolation "worktree": whether dir is in a git working tree
// and its top, and whether HEAD is a commit to branch from. Message says so
// in the words Launch's refusal uses. The error is ErrNoGit when git is not
// on PATH, or ctx's; a directory git refuses for another reason (dubious
// ownership) is reported in Message with InRepo false.
func GitState(ctx context.Context, dir string) (State, error) {
	top, err := toplevel(ctx, dir)
	if err != nil {
		if errors.Is(err, ErrNotRepo) {
			return State{Message: err.Error()}, nil
		}
		return State{}, err
	}
	st := State{InRepo: true, Toplevel: top}
	if _, err := headCommit(ctx, dir); err != nil {
		if ctx.Err() != nil {
			return State{}, ctx.Err()
		}
		st.Message = errNoCommit.Error()
		return st, nil
	}
	st.HasCommit = true
	st.Message = msgCanWorktree
	return st, nil
}
```

- [ ] **Step 4: Run the crew tests**

Run: `go test -race ./internal/crew`
Expected: PASS (the existing `CheckRepo`, `AddWorktree` and engine tests still pass through `inRepo`).

- [ ] **Step 5: Write the failing API tests** in `internal/api/paths_test.go`:

```go
package api

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// paths asks GET /api/paths as the admin and returns the status and reply.
func (e *testEnv) paths(prefix, limit string) (int, map[string]any) {
	e.t.Helper()
	q := url.Values{"prefix": {prefix}}
	if limit != "" {
		q.Set("limit", limit)
	}
	resp, out := e.do("GET", "/api/paths?"+q.Encode(), adminToken, nil)
	return resp.StatusCode, out
}

// entryNames returns the names a paths reply lists, in order.
func entryNames(out map[string]any) []string {
	names := []string{}
	raw, _ := out["entries"].([]any)
	for _, e := range raw {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	return names
}

// entryGit returns the git marks of the entry named name.
func entryGit(out map[string]any, name string) (repo, commits bool) {
	for _, e := range out["entries"].([]any) {
		m := e.(map[string]any)
		if m["name"] == name {
			g := m["git"].(map[string]any)
			return g["repo"] == true, g["commits"] == true
		}
	}
	return false, false
}

func mkdirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// realRoot is the test root with its symlinks resolved, as the server reports it.
func realRoot(t *testing.T, root string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestPathsListsChildDirectoriesUnderTheRoots(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	mkdirs(t, root, "api", "app", "docs", ".hidden")
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory: all of its child directories, hidden ones and files left out.
	status, out := e.paths(root, "")
	if status != http.StatusOK || out["dir"] != root || out["truncated"] != false {
		t.Fatalf("%d %v", status, out)
	}
	if got := entryNames(out); !slices.Equal(got, []string{"api", "app", "docs"}) {
		t.Fatalf("entries: %v", got)
	}
	if p := out["entries"].([]any)[0].(map[string]any)["path"]; p != filepath.Join(root, "api") {
		t.Fatalf("path: %v", p)
	}
	// A trailing separator means the same.
	if _, out := e.paths(root+string(filepath.Separator), ""); len(entryNames(out)) != 3 {
		t.Fatalf("trailing separator: %v", out)
	}
	// A typed element: the children whose names start with it.
	if _, out := e.paths(filepath.Join(root, "ap"), ""); !slices.Equal(entryNames(out), []string{"api", "app"}) {
		t.Fatalf("prefix ap: %v", out)
	}
	// A hidden directory shows only once the dot is typed.
	if _, out := e.paths(filepath.Join(root, ".h"), ""); !slices.Equal(entryNames(out), []string{".hidden"}) {
		t.Fatalf("prefix .h: %v", out)
	}
	// A deeper path that does not exist lists from the longest directory that does.
	if _, out := e.paths(filepath.Join(root, "nope", "deeper"), ""); out["dir"] != root || len(entryNames(out)) != 0 {
		t.Fatalf("missing deeper: %v", out)
	}
	// An empty prefix is the server's default working directory, the root here.
	if _, out := e.paths("", ""); out["dir"] != root || len(entryNames(out)) != 3 {
		t.Fatalf("empty prefix: %v", out)
	}
}

func TestPathsNeverLeaveTheRoots(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	outside := t.TempDir()
	mkdirs(t, outside, "secret")
	mkdirs(t, root, "inside")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "inside"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	// Wholly outside: refused.
	for _, prefix := range []string{outside, filepath.Join(root, ".."), "/", filepath.Join(root, "..", filepath.Base(outside), "sec")} {
		if status, out := e.paths(prefix, ""); status != http.StatusBadRequest || errorCode(out) != "invalid_cwd" {
			t.Fatalf("%s: %d %v", prefix, status, out)
		}
	}
	// Through the link: the listing falls back to the root, and never shows the link or what is behind it.
	for _, prefix := range []string{filepath.Join(root, "escape"), filepath.Join(root, "escape", "sec"), filepath.Join(root, "escape") + string(filepath.Separator)} {
		status, out := e.paths(prefix, "")
		if status != http.StatusOK || out["dir"] != root || slices.Contains(entryNames(out), "escape") || slices.Contains(entryNames(out), "secret") {
			t.Fatalf("%s: %d %v", prefix, status, out)
		}
	}
	// A link that stays inside is listed; one that leaves is not.
	if _, out := e.paths(root, ""); !slices.Equal(entryNames(out), []string{"alias", "inside"}) {
		t.Fatalf("root: %v", entryNames(out))
	}
}

func TestPathsBoundsTheListing(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	for i := range 60 {
		mkdirs(t, root, "d"+string(rune('a'+i/26))+string(rune('a'+i%26)))
	}
	if _, out := e.paths(root, ""); len(entryNames(out)) != 50 || out["truncated"] != true {
		t.Fatalf("default limit: %d %v", len(entryNames(out)), out["truncated"])
	}
	if _, out := e.paths(root, "5"); len(entryNames(out)) != 5 || out["truncated"] != true {
		t.Fatalf("limit 5: %v", out)
	}
	if _, out := e.paths(root, "500"); len(entryNames(out)) != 50 {
		t.Fatalf("limit 500 is clamped: %v", len(entryNames(out)))
	}
	for _, bad := range []string{"0", "-1", "x"} {
		if status, out := e.paths(root, bad); status != http.StatusBadRequest || errorCode(out) != "invalid_request" {
			t.Fatalf("limit %s: %d %v", bad, status, out)
		}
	}
	if status, out := e.paths(strings.Repeat("a", 4097), ""); status != http.StatusBadRequest || errorCode(out) != "invalid_request" {
		t.Fatalf("long prefix: %d %v", status, out)
	}
	if resp, out := e.do("GET", "/api/paths?prefix="+url.QueryEscape(root), "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %v", resp.StatusCode, out)
	}
}

// gitInit makes dir a git repository, with one commit when commit is set. It
// skips the test when git is not installed. HOME is a temporary directory so
// that no configuration of the user running the tests applies.
func gitInit(t *testing.T, dir string, commit bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	mkdirs(t, dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if commit {
		run("-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
	}
}

func TestPathsMarksGitRepositories(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	gitInit(t, filepath.Join(root, "repo"), true)
	gitInit(t, filepath.Join(root, "fresh"), false)
	mkdirs(t, root, "plain", filepath.Join("repo", "pkg"))
	_, out := e.paths(root, "")
	for name, want := range map[string][2]bool{"repo": {true, true}, "fresh": {true, false}, "plain": {false, false}} {
		if repo, commits := entryGit(out, name); repo != want[0] || commits != want[1] {
			t.Fatalf("%s: repo %v commits %v", name, repo, commits)
		}
	}
	// A child of a repository, with no .git of its own, carries its parent's marks.
	if _, out := e.paths(filepath.Join(root, "repo"), ""); func() bool { r, c := entryGit(out, "pkg"); return !r || !c }() {
		t.Fatalf("pkg below repo: %v", out)
	}
}

func TestGitCheckAnswersByTheLaunchRules(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	gitInit(t, filepath.Join(root, "repo"), true)
	gitInit(t, filepath.Join(root, "fresh"), false)
	mkdirs(t, root, "plain", filepath.Join("repo", "pkg"))
	check := func(cwd string) (int, map[string]any) {
		t.Helper()
		resp, out := e.do("GET", "/api/git/check?cwd="+url.QueryEscape(cwd), adminToken, nil)
		return resp.StatusCode, out
	}
	if status, out := check(filepath.Join(root, "repo", "pkg")); status != http.StatusOK || out["inRepo"] != true || out["toplevel"] != filepath.Join(root, "repo") || out["hasCommit"] != true {
		t.Fatalf("repo: %d %v", status, out)
	}
	if _, out := check(filepath.Join(root, "fresh")); out["inRepo"] != true || out["hasCommit"] != false || !strings.Contains(out["message"].(string), "without a commit") {
		t.Fatalf("fresh: %v", out)
	}
	if _, out := check(filepath.Join(root, "plain")); out["inRepo"] != false || out["hasCommit"] != false || !strings.Contains(out["message"].(string), "not in a git repository") {
		t.Fatalf("plain: %v", out)
	}
	// Empty is the default working directory; outside the roots is refused as a launch would.
	if _, out := check(""); out["inRepo"] != false {
		t.Fatalf("default cwd: %v", out)
	}
	if status, out := check(t.TempDir()); status != http.StatusBadRequest || errorCode(out) != "invalid_cwd" {
		t.Fatalf("outside: %d %v", status, out)
	}
	if resp, _ := e.do("GET", "/api/git/check?cwd="+url.QueryEscape(root), "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
}
```

- [ ] **Step 6: Run them to see them fail**

Run: `go test ./internal/api -run 'TestPaths|TestGitCheck'`
Expected: FAIL — `404 not_found` on every request (the routes do not exist).

- [ ] **Step 7: Implement `internal/api/paths.go`**

```go
package api

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/crew"
)

// The working-directory pickers (the crew editor and the Launch dialog)
// complete what is typed from GET /api/paths and show whether a crew with
// worktrees could launch there from GET /api/git/check. Both say what exists
// under the allowed roots and nothing else: a prefix is resolved as a
// session's working directory is (resolveCwd), so a symbolic link out of the
// roots leads nowhere.

const (
	// maxPathEntries bounds one listing: a page for a picker, not a file system.
	maxPathEntries = 50
	// maxPathQuery bounds the prefix and cwd query values, as a crew's cwd is bounded.
	maxPathQuery = 4096
	// gitCheckTimeout bounds the git calls of one request.
	gitCheckTimeout = 5 * time.Second
)

// pathGit marks a listed directory: in a git working tree, and one whose
// HEAD is a commit, which a crew with worktrees needs.
type pathGit struct {
	Repo    bool `json:"repo"`
	Commits bool `json:"commits"`
}

type pathEntry struct {
	Name string  `json:"name"`
	Path string  `json:"path"`
	Git  pathGit `json:"git"`
}

type pathsReply struct {
	Dir       string      `json:"dir"`
	Entries   []pathEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

type gitCheckReply struct {
	InRepo    bool   `json:"inRepo"`
	Toplevel  string `json:"toplevel,omitempty"`
	HasCommit bool   `json:"hasCommit"`
	Message   string `json:"message"`
}

// errNoAllowedDir says that no leading part of a prefix is a directory under
// the allowed roots.
var errNoAllowedDir = errors.New("no directory in the prefix is under the allowed roots")

// splitPrefix finds the longest leading part of prefix that resolveCwd
// accepts (an existing directory under an allowed root, symbolic links
// resolved) and the path element typed after it, "" when prefix names such
// a directory itself. An empty prefix is the server's default working
// directory. A prefix no part of which qualifies is errNoAllowedDir.
func (s *Server) splitPrefix(prefix string) (dir, seg string, err error) {
	cur := filepath.Clean(cmp.Or(prefix, s.cfg.DefaultCwd))
	for {
		if real, err := s.resolveCwd(cur); err == nil {
			return real, seg, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", errNoAllowedDir
		}
		seg = filepath.Base(cur)
		cur = parent
	}
}

// listPaths lists the child directories of the directory prefix names (see
// splitPrefix) whose names start with the element typed after it: at most
// limit of them in name order, hidden ones only when that element starts
// with a dot, a symbolic link only when it leads under an allowed root. A
// child with a .git of its own is asked git for its marks; the others carry
// their parent's.
func (s *Server) listPaths(ctx context.Context, prefix string, limit int) (pathsReply, error) {
	dir, seg, err := s.splitPrefix(prefix)
	if err != nil {
		return pathsReply{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return pathsReply{}, err
	}
	parent, err := gitMark(ctx, dir)
	if err != nil {
		return pathsReply{}, err
	}
	out := pathsReply{Dir: dir, Entries: []pathEntry{}}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, seg) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(seg, ".")) {
			continue
		}
		path := filepath.Join(dir, name)
		if e.Type()&os.ModeSymlink != 0 {
			if _, err := s.resolveCwd(path); err != nil {
				continue
			}
		} else if !e.IsDir() {
			continue
		}
		if len(out.Entries) == limit {
			out.Truncated = true
			break
		}
		mark := parent
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			if mark, err = gitMark(ctx, path); err != nil {
				return pathsReply{}, err
			}
		}
		out.Entries = append(out.Entries, pathEntry{Name: name, Path: path, Git: mark})
	}
	return out, nil
}

// gitMark is the git state of dir as the picker shows it. Without git on the
// server nothing is marked.
func gitMark(ctx context.Context, dir string) (pathGit, error) {
	st, err := crew.GitState(ctx, dir)
	if errors.Is(err, crew.ErrNoGit) {
		return pathGit{}, nil
	}
	if err != nil {
		return pathGit{}, err
	}
	return pathGit{Repo: st.InRepo, Commits: st.HasCommit}, nil
}

// pathQuery reads a bounded path from the query, or answers the request.
func pathQuery(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	v := r.URL.Query().Get(key)
	if len(v) > maxPathQuery || strings.ContainsRune(v, 0) {
		writeError(w, http.StatusBadRequest, "invalid_request", key+" must be at most 4096 bytes without NUL")
		return "", false
	}
	return v, true
}

// handleListPaths answers GET /api/paths?prefix=<path>&limit=<n>.
func (s *Server) handleListPaths(w http.ResponseWriter, r *http.Request) {
	prefix, ok := pathQuery(w, r, "prefix")
	if !ok {
		return
	}
	limit := maxPathEntries
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be a positive integer")
			return
		}
		limit = min(n, maxPathEntries)
	}
	ctx, cancel := context.WithTimeout(r.Context(), gitCheckTimeout)
	defer cancel()
	reply, err := s.listPaths(ctx, prefix, limit)
	switch {
	case errors.Is(err, errNoAllowedDir):
		writeError(w, http.StatusBadRequest, "invalid_cwd", err.Error())
		return
	case err != nil:
		s.log.Warn("list paths failed", "err", err)
		writeError(w, http.StatusInternalServerError, "list_failed", "could not list the directory")
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// handleGitCheck answers GET /api/git/check?cwd=<path>: whether a crew with
// worktrees could launch in cwd (the default working directory when empty),
// by the launch's own rules. A preview: the launch's 409 is the authority.
func (s *Server) handleGitCheck(w http.ResponseWriter, r *http.Request) {
	cwdInput, ok := pathQuery(w, r, "cwd")
	if !ok {
		return
	}
	cwd, err := s.resolveCwd(cmp.Or(cwdInput, s.cfg.DefaultCwd))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cwd", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gitCheckTimeout)
	defer cancel()
	st, err := crew.GitState(ctx, cwd)
	switch {
	case errors.Is(err, crew.ErrNoGit):
		writeJSON(w, http.StatusOK, gitCheckReply{Message: crew.ErrNoGit.Error()})
		return
	case err != nil:
		s.log.Warn("git check failed", "cwd", cwd, "err", err)
		writeError(w, http.StatusInternalServerError, "git_failed", "could not run git")
		return
	}
	writeJSON(w, http.StatusOK, gitCheckReply{InRepo: st.InRepo, Toplevel: st.Toplevel, HasCommit: st.HasCommit, Message: st.Message})
}
```

Routes, in `internal/api/server.go` `Handler()` next to the crews routes:

```go
	mux.HandleFunc("GET /api/paths", s.requireAdmin(s.handleListPaths))
	mux.HandleFunc("GET /api/git/check", s.requireAdmin(s.handleGitCheck))
```

- [ ] **Step 8: Run the API tests**

Run: `go test -race ./internal/api -run 'TestPaths|TestGitCheck'`
Expected: PASS (the two git tests skip where `git` is missing and say so).

- [ ] **Step 9: Add the client calls** in `web/app/composables/useSessions.ts` — the types after `RunInfo`, the calls after `whoami`:

```ts
export interface PathGit {
  repo: boolean
  commits: boolean
}
/** One directory GET /api/paths lists: its name, its full path, and whether it is a git repository with a commit. */
export interface PathEntry {
  name: string
  path: string
  git: PathGit
}
export interface PathsReply {
  dir: string
  entries: PathEntry[]
  truncated: boolean
}
/** What GET /api/git/check says of a working directory, by the launch's rules for worktrees. */
export interface GitCheck {
  inRepo: boolean
  toplevel?: string
  hasCommit: boolean
  message: string
}
```

```ts
    /**
     * The child directories of the longest existing directory in `prefix`, under the allowed roots (symlinks resolved), at most 50, each
     * marked when it is a git repository (`repo`) with a commit (`commits`). Hidden directories show once the typed element starts with a dot.
     * 400 `invalid_cwd` when no part of the prefix is under a root.
     */
    listPaths: (prefix: string, limit = 50) => request<PathsReply>('/api/paths', { query: { prefix, limit: String(limit) } }),
    /** Whether a crew with worktrees could launch in `cwd` (the server's default when empty). A preview: the launch's 409 `not_a_repo` decides. */
    gitCheck: (cwd: string) => request<GitCheck>('/api/git/check', { query: { cwd } }),
```

- [ ] **Step 10: Typecheck and lint, then commit**

Run: `make lint && npm --prefix web run typecheck`
Expected: clean.

```bash
git add internal/crew/worktree.go internal/crew/worktree_test.go internal/api/paths.go internal/api/paths_test.go internal/api/server.go web/app/composables/useSessions.ts
git commit -m "api: directory listing and git check under the allowed roots"
```

---

### Task 2: The `DirInput` combobox, the editor's git line, the Launch dialog

**Files:**
- Create: `web/app/utils/dirInput.ts`, `web/app/utils/dirInput.test.ts`, `web/app/components/DirInput.vue`
- Modify: `web/app/components/CrewEditor.vue:128-131` (the working-directory field), `:21-30` (`launchBlocked`), `:114-116` (the launch tooltip); `web/app/components/LaunchSessionModal.vue:335-337`

**Interfaces:**
- Consumes: `useSessions().listPaths`, `useSessions().gitCheck`, `PathEntry`, `GitCheck` (Task 1); `useAdminToken().hasToken`.
- Produces:
```ts
// web/app/utils/dirInput.ts
export const DIR_DEBOUNCE_MS = 150
export function dirQuery(text: string): string                       // the prefix sent: the text trimmed
export function pickEntry(entry: PathEntry): string                 // the field after a pick: entry.path
export function moveActive(active: number, delta: 1 | -1, count: number): number // -1 is no row; wraps
export function gitMark(git: PathGit): { label: string; tone: 'success' | 'warning' | 'neutral' }
export type GitCheckView = GitCheck & { error?: boolean }           // error: the check itself failed (its message says why)
export function gitCheckLine(check: GitCheckView | null, isolation: 'none' | 'worktree'): { text: string; tone: 'success' | 'warning' | 'neutral'; blocks: boolean }
// web/app/components/DirInput.vue — <DirInput v-model="text" placeholder name />; data-dir-input on the root, data-dir-list on the list
```

- [ ] **Step 1: Write the failing vitest** `web/app/utils/dirInput.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { PathEntry } from '~/composables/useSessions'
import { DIR_DEBOUNCE_MS, dirQuery, gitCheckLine, gitMark, moveActive, pickEntry } from './dirInput'

const api: PathEntry = { name: 'api', path: '/srv/work/api', git: { repo: true, commits: true } }

describe('dirInput', () => {
  it('sends the text trimmed, and empty for the server default', () => {
    expect(dirQuery('  /srv/work/a ')).toBe('/srv/work/a')
    expect(dirQuery('   ')).toBe('')
  })

  it('picks the entry as its path, so the next listing shows its children', () => {
    expect(pickEntry(api)).toBe('/srv/work/api')
  })

  it('moves the active row with wrapping, from nowhere to the ends', () => {
    expect(moveActive(-1, 1, 3)).toBe(0)
    expect(moveActive(-1, -1, 3)).toBe(2)
    expect(moveActive(2, 1, 3)).toBe(0)
    expect(moveActive(0, -1, 3)).toBe(2)
    expect(moveActive(1, 1, 0)).toBe(-1)
  })

  it('marks a repository with a commit, warns on one without, says nothing otherwise', () => {
    expect(gitMark({ repo: true, commits: true })).toEqual({ label: 'git', tone: 'success' })
    expect(gitMark({ repo: true, commits: false })).toEqual({ label: 'git, no commit', tone: 'warning' })
    expect(gitMark({ repo: false, commits: false })).toEqual({ label: '', tone: 'neutral' })
  })

  it('explains the launch with worktrees, and only informs without them', () => {
    const ok = { inRepo: true, toplevel: '/srv/work', hasCommit: true, message: 'a git repository with a commit: a crew with worktrees can launch here' }
    const fresh = { inRepo: true, toplevel: '/srv/work', hasCommit: false, message: 'the working directory is a git repository without a commit: a worktree needs one to branch from' }
    const plain = { inRepo: false, hasCommit: false, message: 'the working directory is not in a git repository' }
    expect(gitCheckLine(null, 'worktree')).toEqual({ text: '', tone: 'neutral', blocks: false })
    expect(gitCheckLine(ok, 'worktree')).toEqual({ text: 'Git repository at /srv/work: worktrees can be made.', tone: 'success', blocks: false })
    expect(gitCheckLine(fresh, 'worktree')).toEqual({ text: `${fresh.message}. Launch would be refused (not_a_repo).`, tone: 'warning', blocks: true })
    expect(gitCheckLine(plain, 'worktree')).toEqual({ text: `${plain.message}. Launch would be refused (not_a_repo).`, tone: 'warning', blocks: true })
    expect(gitCheckLine(plain, 'none')).toEqual({ text: 'Not a git repository; fine with a shared working directory.', tone: 'neutral', blocks: false })
    expect(gitCheckLine(ok, 'none')).toEqual({ text: 'Git repository at /srv/work.', tone: 'neutral', blocks: false })
    expect(gitCheckLine({ ...plain, error: true, message: 'working directory is outside the allowed roots' }, 'none')).toEqual({ text: 'working directory is outside the allowed roots', tone: 'warning', blocks: false })
  })

  it('debounces for longer than a keystroke and shorter than a pause', () => {
    expect(DIR_DEBOUNCE_MS).toBeGreaterThanOrEqual(100)
    expect(DIR_DEBOUNCE_MS).toBeLessThanOrEqual(300)
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `npm --prefix web test -- dirInput`
Expected: FAIL, `Cannot find module './dirInput'`.

- [ ] **Step 3: Implement `web/app/utils/dirInput.ts`**

```ts
import type { GitCheck, PathEntry, PathGit } from '~/composables/useSessions'

/** How long the picker waits after a keystroke before asking the server. */
export const DIR_DEBOUNCE_MS = 150

/** The prefix sent for what is typed: the text trimmed; empty means the server's default directory. */
export function dirQuery(text: string): string {
  return text.trim()
}

/** The field's value after an entry is chosen: its path, so the next listing shows its children. */
export function pickEntry(entry: PathEntry): string {
  return entry.path
}

/** Moves the active row by delta over count rows, wrapping; -1 is no row, from which down goes to the first and up to the last. */
export function moveActive(active: number, delta: 1 | -1, count: number): number {
  if (count === 0) return -1
  if (active < 0) return delta > 0 ? 0 : count - 1
  return (active + delta + count) % count
}

/** The mark beside a listed directory: whether a crew with worktrees could use it. */
export function gitMark(git: PathGit): { label: string; tone: 'success' | 'warning' | 'neutral' } {
  if (git.repo && git.commits) return { label: 'git', tone: 'success' }
  if (git.repo) return { label: 'git, no commit', tone: 'warning' }
  return { label: '', tone: 'neutral' }
}

/** A git check, or the failure of one (`error`: the message is the request's error). */
export type GitCheckView = GitCheck & { error?: boolean }

/**
 * What the crew editor says under the working directory. With worktrees the
 * line explains a launch the server would refuse (`blocks`, for the launch
 * button's tooltip); without them it only informs. The launch's 409 stays
 * the authority: the button is never disabled by this.
 */
export function gitCheckLine(check: GitCheckView | null, isolation: 'none' | 'worktree'): { text: string; tone: 'success' | 'warning' | 'neutral'; blocks: boolean } {
  if (!check) return { text: '', tone: 'neutral', blocks: false }
  if (check.error) return { text: check.message, tone: 'warning', blocks: false }
  if (isolation === 'worktree') {
    if (check.inRepo && check.hasCommit) return { text: `Git repository at ${check.toplevel}: worktrees can be made.`, tone: 'success', blocks: false }
    return { text: `${check.message}. Launch would be refused (not_a_repo).`, tone: 'warning', blocks: true }
  }
  if (check.inRepo) return { text: `Git repository at ${check.toplevel}.`, tone: 'neutral', blocks: false }
  return { text: 'Not a git repository; fine with a shared working directory.', tone: 'neutral', blocks: false }
}
```

- [ ] **Step 4: Run the vitest**

Run: `npm --prefix web test -- dirInput`
Expected: PASS.

- [ ] **Step 5: Write `web/app/components/DirInput.vue`**

A `UInput` with a listbox under it. `UInputMenu` was considered and not used: picking an entry must keep the text editable and re-list that entry's children, which a select-style menu fights.

```vue
<script setup lang="ts">
import type { PathEntry } from '~/composables/useSessions'
import { DIR_DEBOUNCE_MS, dirQuery, gitMark, moveActive, pickEntry } from '~/utils/dirInput'

/**
 * A working-directory field completed from the server: as the text changes,
 * GET /api/paths lists the child directories of the longest existing
 * directory in it (under the allowed roots), each marked when it is a git
 * repository with a commit. Arrow keys move, Enter or a click picks (the path
 * replaces the text and the next listing shows its children), Esc closes.
 * The model is the text, whatever is picked.
 */
const model = defineModel<string>({ default: '' })
defineProps<{ placeholder?: string; name?: string }>()

const api = useSessions()
const admin = useAdminToken()

const open = ref(false)
const entries = ref<PathEntry[]>([])
const truncated = ref(false)
const active = ref(-1)
const loading = ref(false)
const problem = ref('')
const listId = useId()
let timer: number | undefined
// Replies may come back out of order: only the latest request's counts.
let seq = 0

async function fetchNow() {
  if (!admin.hasToken.value) return
  const n = ++seq
  loading.value = true
  try {
    const r = await api.listPaths(dirQuery(model.value))
    if (n !== seq) return
    entries.value = r.entries
    truncated.value = r.truncated
    problem.value = ''
    active.value = -1
  } catch (e) {
    if (n !== seq) return
    entries.value = []
    truncated.value = false
    problem.value = (e as Error).message
  } finally {
    if (n === seq) loading.value = false
  }
}

function schedule() {
  window.clearTimeout(timer)
  timer = window.setTimeout(fetchNow, DIR_DEBOUNCE_MS)
}

function onInput(v: string | number) {
  model.value = String(v)
  open.value = true
  schedule()
}

function onFocus() {
  open.value = true
  if (!entries.value.length) schedule()
}

function pick(e: PathEntry) {
  model.value = pickEntry(e)
  active.value = -1
  schedule()
}

function onKey(e: KeyboardEvent) {
  switch (e.key) {
    case 'ArrowDown':
      e.preventDefault()
      open.value = true
      active.value = moveActive(active.value, 1, entries.value.length)
      break
    case 'ArrowUp':
      e.preventDefault()
      open.value = true
      active.value = moveActive(active.value, -1, entries.value.length)
      break
    case 'Enter': {
      const sel = entries.value[active.value]
      if (open.value && sel) {
        e.preventDefault()
        pick(sel)
      }
      break
    }
    case 'Escape':
      if (open.value) {
        e.preventDefault()
        e.stopPropagation()
        open.value = false
      }
      break
  }
}

function onFocusOut(e: FocusEvent) {
  const root = e.currentTarget as HTMLElement
  if (!root.contains(e.relatedTarget as Node | null)) open.value = false
}

onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <div class="relative" data-dir-input @focusout="onFocusOut">
    <UInput
      :model-value="model"
      :placeholder="placeholder"
      :name="name"
      autocapitalize="off"
      autocomplete="off"
      spellcheck="false"
      :loading="loading"
      :ui="{ base: 'font-mono' }"
      class="w-full"
      role="combobox"
      :aria-expanded="open"
      :aria-controls="listId"
      :aria-activedescendant="active >= 0 ? `${listId}-${active}` : undefined"
      @update:model-value="onInput"
      @focus="onFocus"
      @keydown="onKey"
    />
    <div v-if="open && (entries.length || problem || truncated)" :id="listId" role="listbox" class="absolute z-20 mt-1 max-h-64 w-full overflow-y-auto rounded-md border border-default bg-default p-1 shadow-lg" data-dir-list>
      <p v-if="problem" class="px-2 py-1 text-xs text-warning">{{ problem }}</p>
      <button
        v-for="(e, i) in entries"
        :id="`${listId}-${i}`"
        :key="e.path"
        type="button"
        role="option"
        :aria-selected="i === active"
        tabindex="-1"
        class="flex w-full items-center gap-2 rounded px-2 py-1 text-left font-mono text-xs"
        :class="i === active ? 'bg-elevated' : 'hover:bg-elevated/60'"
        @mousedown.prevent
        @mousemove="active = i"
        @click="pick(e)"
      >
        <UIcon name="i-lucide-folder" class="size-3.5 flex-none text-muted" />
        <span class="truncate">{{ e.name }}</span>
        <UBadge v-if="gitMark(e.git).label" :label="gitMark(e.git).label" :color="gitMark(e.git).tone" variant="subtle" size="xs" class="ml-auto flex-none" />
      </button>
      <p v-if="truncated" class="px-2 py-1 text-[11px] text-muted">More than 50 here: keep typing to narrow it.</p>
    </div>
  </div>
</template>
```

- [ ] **Step 6: Use it in the crew editor, with the git line and the launch tooltip** (`web/app/components/CrewEditor.vue`)

Script additions: the import and the git block go after `const live = useAttention()`; `launchHint` goes after `launchBlocked` (it reads it, and a `const` is not usable before its line):

```ts
import { gitCheckLine, type GitCheckView } from '~/utils/dirInput'

const api = useSessions()

// The git state of the working directory, read 300 ms after it last
// changed; stale replies are dropped. The line it makes explains a launch
// with worktrees the server would refuse; the server still decides.
const gitCheck = ref<GitCheckView | null>(null)
let gitTimer: number | undefined
let gitSeq = 0
async function checkGit() {
  const n = ++gitSeq
  try {
    const r = await api.gitCheck(crew.value.cwd.trim())
    if (n === gitSeq) gitCheck.value = r
  } catch (e) {
    if (n === gitSeq) gitCheck.value = { inRepo: false, hasCommit: false, message: (e as Error).message, error: true }
  }
}
watch(
  () => crew.value.cwd,
  () => {
    window.clearTimeout(gitTimer)
    gitTimer = window.setTimeout(checkGit, 300)
  },
  { immediate: true },
)
onBeforeUnmount(() => window.clearTimeout(gitTimer))
const gitLine = computed(() => gitCheckLine(gitCheck.value, crew.value.isolation))
const launchHint = computed(() => launchBlocked.value || (gitLine.value.blocks ? gitLine.value.text : ''))
```

Template: replace the working-directory `UFormField` body and the launch tooltip:

```vue
        <UFormField label="Working directory" hint="allowed root" name="cwd">
          <DirInput :model-value="crew.cwd" placeholder="server default" name="cwd" @update:model-value="set('cwd', $event)" />
          <p v-if="gitLine.text" class="mt-1 text-xs" :class="{ 'text-success': gitLine.tone === 'success', 'text-warning': gitLine.tone === 'warning', 'text-muted': gitLine.tone === 'neutral' }" data-git-state>{{ gitLine.text }}</p>
        </UFormField>
```

```vue
        <UTooltip :text="launchHint" :disabled="!launchHint">
          <UButton :label="launchLabel" icon="i-lucide-play" :loading="launching" :disabled="!!launchBlocked || saving" data-launch @click="emit('launch')" />
        </UTooltip>
```

The button stays enabled when only `gitLine.blocks` holds: the tooltip explains, the launch handler's 409 answers.

- [ ] **Step 7: Use it in the Launch dialog** (`web/app/components/LaunchSessionModal.vue:335-337`):

```vue
          <UFormField label="Working directory" name="cwd" hint="must be under an allowed root">
            <DirInput v-model="state.cwd" :placeholder="selected?.cwd || 'server default'" name="cwd" />
          </UFormField>
```

- [ ] **Step 8: Typecheck, then the headless check**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: clean, all vitest files pass.

Build and start the test server, make a tree under the repository (the example config's allowed root), then run the check:

```bash
make web-build && make build-go
mkdir -p tmp-picker/api tmp-picker/app tmp-picker/.hidden && git -C tmp-picker/api init -q 2>/dev/null || true
bin/conductor serve --config conductor.example.json --listen 127.0.0.1:8099 & echo $! > /tmp/claude-1000/-home-nater-go-src-github-com-phenixrizen-conductor/a75c70c0-928a-4c03-ab5e-19119aca1b6c/scratchpad/pw/server.pid
```

`scratchpad/pw/dirinput-check.js`:

```js
const { chromium } = require('playwright-core')
const base = 'http://127.0.0.1:8099'
const root = process.argv[2] // absolute path of the repository
;(async () => {
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  await ctx.addInitScript(() => localStorage.setItem('conductor.adminToken', 'dev-admin-token-change-me'))
  const page = await ctx.newPage()
  await page.goto(base + '/crews', { waitUntil: 'commit' })
  await page.getByRole('button', { name: 'New crew' }).click()
  const field = page.locator('[data-dir-input] input')
  await field.fill(root + '/tmp-picker/a')
  await page.waitForSelector('[data-dir-list] [role="option"]')
  console.log('options', await page.locator('[data-dir-list] [role="option"]').allInnerTexts()) // api (git …), app; no .hidden
  await field.press('ArrowDown')
  await field.press('Enter')
  console.log('picked', await field.inputValue()) // <root>/tmp-picker/api
  await page.waitForSelector('[data-git-state]')
  console.log('git line', await page.locator('[data-git-state]').innerText()) // mentions a commit or its absence; with worktree isolation a warning
  await field.fill(root + '/tmp-picker/.')
  await page.waitForSelector('[data-dir-list] [role="option"]')
  console.log('hidden', await page.locator('[data-dir-list] [role="option"]').allInnerTexts()) // .hidden
  await page.screenshot({ path: 'dirinput.png' })
  await browser.close()
})()
```

Run: `cd scratchpad/pw && node dirinput-check.js "$(pwd -P | sed 's#/tmp/claude.*##')"` — pass the repository's absolute path explicitly, e.g. `node dirinput-check.js /home/nater/go/src/github.com/phenixrizen/conductor`.
Expected: `options` lists `api` and `app` (the api row carries `git, no commit` since `git init` made no commit), `picked` is the api path, `git line` is the warning line ending in `Launch would be refused (not_a_repo).` (a new crew is `worktree`), `hidden` lists `.hidden`. Then `kill $(cat scratchpad/pw/server.pid); rm -rf tmp-picker`.

- [ ] **Step 9: Commit**

```bash
git add web/app/utils/dirInput.ts web/app/utils/dirInput.test.ts web/app/components/DirInput.vue web/app/components/CrewEditor.vue web/app/components/LaunchSessionModal.vue
git commit -m "web: working-directory picker with git marks and the editor's git line"
```

---

### Task 3: Example crews

**Files:**
- Create: `internal/crew/examples.go`, `internal/crew/examples_test.go`
- Modify: `internal/crew/store.go` (`Seed`), `internal/config/config.go:109-116` (`Examples`) and `:168-240` (`applyEnv`), `internal/config/config_test.go`, `internal/cli/serve.go:88-153`, `internal/cli/serve_test.go`, `internal/api/crews.go`, `internal/api/server.go:472` (route), `internal/api/crews_test.go` (plan 1's file), `web/app/composables/useSessions.ts`, `web/app/pages/crews/[[id]].vue:386-392`

**Interfaces:**
- Consumes: `crew.Crew`, `crew.Member`, `crew.Start`, `StartImmediately`, `StartAfter`, `WhereServer`, `IsolationWorktree`, `validateWithID` (`internal/crew/crew.go`); the store's `mu`, `crews` map and `commit` as plan 1 leaves them; `config.Config.DefaultCwd`; `serveUntilListening`, `logLines`, `writeServeConfig`, `clearConductorEnv` (`internal/cli/serve_test.go`, `cli_test.go`); `newTestEnv`, `e.do`, `e.sendCrew`, `adminToken`.
- Produces:
```go
// internal/crew/examples.go
var ExampleIDs = []string{"example-todo-app", "example-test-fixer", "example-docs-writer", "example-dependency-upgrade"}
func Examples(cwd string, now time.Time) []Crew
// internal/crew/store.go
func (s *Store) Seed(list []Crew) (added, skipped []string, err error)
// internal/config/config.go
Config.Examples bool `json:"-"`            // CONDUCTOR_EXAMPLES=1|true, or conductor serve --examples
// internal/api
func (s *Server) SeedExampleCrews() (added, skipped []string, err error)
func (s *Server) handleSeedExamples(w http.ResponseWriter, r *http.Request) // POST /api/crews/examples (admin) → 200 {added, skipped}
```
```ts
loadExampleCrews: () => Promise<{ added: string[]; skipped: string[] }>
```

- [ ] **Step 1: Write the failing crew tests** `internal/crew/examples_test.go`:

```go
package crew

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Every example is a crew the store would accept: the four ids in order,
// claude and codex members with role prompts that use the goal, the server's
// working directory, a worktree per member.
func TestExamplesAreValidCrews(t *testing.T) {
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	list := Examples("/srv/work", now)
	var ids []string
	for _, c := range list {
		ids = append(ids, c.ID)
		if err := c.validateWithID(); err != nil {
			t.Errorf("%s: %v", c.ID, err)
		}
		if c.Cwd != "/srv/work" || c.Isolation != IsolationWorktree || c.Where != WhereServer || !c.OpenAfterLaunch || !c.CreatedAt.Equal(now) || !c.UpdatedAt.Equal(now) {
			t.Errorf("%s: %+v", c.ID, c)
		}
		if len(c.Members) < 2 || !strings.HasPrefix(c.Name, "Example: ") {
			t.Errorf("%s: %d members, name %q", c.ID, len(c.Members), c.Name)
		}
		for _, m := range c.Members {
			if m.AgentID != "claude" && m.AgentID != "codex" {
				t.Errorf("%s/%s: agent %q", c.ID, m.Name, m.AgentID)
			}
			if !strings.Contains(m.Prompt, "$GOAL") {
				t.Errorf("%s/%s: the prompt does not use $GOAL", c.ID, m.Name)
			}
		}
	}
	if !slices.Equal(ids, ExampleIDs) {
		t.Fatalf("ids: %v", ids)
	}
}

// The todo app: a lead that plans, two builders after the lead, a tester
// after the builders (after the last of them; the model names one member).
func TestTodoAppStartsLeadThenBuildersThenTester(t *testing.T) {
	todo := Examples("/w", time.Now())[0]
	want := map[string]Start{
		"lead":   {When: StartImmediately},
		"core":   {When: StartAfter, Member: "lead"},
		"cli":    {When: StartAfter, Member: "lead"},
		"tester": {When: StartAfter, Member: "cli"},
	}
	if len(todo.Members) != len(want) {
		t.Fatalf("members: %+v", todo.Members)
	}
	for _, m := range todo.Members {
		if m.Start != want[m.Name] {
			t.Errorf("%s: %+v", m.Name, m.Start)
		}
	}
}

// Seed adds what is missing and leaves what exists alone: a second seed adds
// nothing and an edit survives it; a deleted example comes back.
func TestSeedAddsOnceAndLeavesEditsAlone(t *testing.T) {
	s, _ := newStore(t)
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	added, skipped, err := s.Seed(Examples("/w", now))
	if err != nil || !slices.Equal(added, ExampleIDs) || len(skipped) != 0 {
		t.Fatalf("first seed: %v %v %v", added, skipped, err)
	}
	c, _ := s.Get("example-todo-app")
	c.Goal = "edited"
	if _, err := s.Update(c.ID, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete("example-docs-writer"); err != nil {
		t.Fatal(err)
	}
	added, skipped, err = s.Seed(Examples("/w", now))
	if err != nil || !slices.Equal(added, []string{"example-docs-writer"}) || !slices.Equal(skipped, []string{"example-todo-app", "example-test-fixer", "example-dependency-upgrade"}) {
		t.Fatalf("second seed: %v %v %v", added, skipped, err)
	}
	if got, _ := s.Get("example-todo-app"); got.Goal != "edited" {
		t.Fatalf("the edit was lost: %q", got.Goal)
	}
}

// A crew the store refuses stops the seed, naming it; the ones before it stay.
func TestSeedStopsAtAnInvalidCrew(t *testing.T) {
	s, _ := newStore(t)
	list := Examples("/w", time.Now())
	list[1].Cwd = strings.Repeat("x", maxCwd+1)
	added, _, err := s.Seed(list)
	if err == nil || !strings.Contains(err.Error(), "example-test-fixer") || !slices.Equal(added, []string{"example-todo-app"}) {
		t.Fatalf("%v %v", added, err)
	}
	if _, ok := s.Get("example-docs-writer"); ok {
		t.Fatal("seeding went on past the error")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/crew -run 'TestExamples|TestTodoApp|TestSeed'`
Expected: FAIL to compile, `undefined: Examples`, `s.Seed undefined`.

- [ ] **Step 3: Write `internal/crew/examples.go`**

```go
package crew

import "time"

// ExampleIDs are the ids of the example crews, in the order Examples lists them.
var ExampleIDs = []string{"example-todo-app", "example-test-fixer", "example-docs-writer", "example-dependency-upgrade"}

// Examples returns the example crews: data like any saved crew, which
// conductor serve --examples and POST /api/crews/examples seed once
// (Store.Seed) and which are edited or deleted like any other. Their members
// use the claude and codex built-ins; every crew works in cwd, the server's
// default working directory, with a worktree per member, and opens its view
// after launch. now stamps them.
func Examples(cwd string, now time.Time) []Crew {
	crew := func(id, name, goal string, members ...Member) Crew {
		return Crew{ID: id, Name: name, Goal: goal, Cwd: cwd, Where: WhereServer, Isolation: IsolationWorktree, OpenAfterLaunch: true, Members: members, CreatedAt: now, UpdatedAt: now}
	}
	member := func(name, agent, prompt string, start Start) Member {
		return Member{Name: name, AgentID: agent, Prompt: prompt, Start: start}
	}
	now_ := Start{When: StartImmediately}
	after := func(m string) Start { return Start{When: StartAfter, Member: m} }
	return []Crew{
		crew("example-todo-app", "Example: todo app",
			"Build a small command-line todo app in this repository: add, list and done commands, items kept in a JSON file in the user's home directory, with tests.",
			member("lead", "claude", "You lead a crew of four on this goal: $GOAL. Plan, do not build. Write PLAN.md at the top of the repository with the data model, the command-line interface and the file layout, then split the work into two parts, core (storage and the data model) and cli (the commands), naming the files each part owns and the interface between them. Commit PLAN.md and stop: the builders start when you report done.", now_),
			member("core", "claude", "You build the core part of PLAN.md at the top of the repository, for this goal: $GOAL. Work only in the files PLAN.md gives to core, with unit tests, and commit as you go. Report done when the storage and the data model are complete and their tests pass.", after("lead")),
			member("cli", "codex", "You build the cli part of PLAN.md at the top of the repository, for this goal: $GOAL. Work only in the files PLAN.md gives to cli, against the interface PLAN.md says core exposes, and commit as you go. Report done when every command works end to end.", after("lead")),
			member("tester", "codex", "You test the todo app built for this goal: $GOAL. The core and cli members of this crew work on the branches crew/<run>/core and crew/<run>/cli: merge both into your worktree, run the whole test suite, add the tests PLAN.md asks for that are missing, and change test code only. Hand a bug in the app to its owner with: conductor notify --event handoff --to core --message \"what and where\" (or --to cli). Report done when the suite is green.", after("cli")),
		),
		crew("example-test-fixer", "Example: test fixer",
			"Make this repository's test suite pass without weakening a test.",
			member("triage", "claude", "Run this repository's test suite for this goal: $GOAL. Write TRIAGE.md at the top of the repository: every failing test, its cause as far as you can tell, grouped by the production file to change. Commit it and report done. Fix nothing yourself.", now_),
			member("fixer", "codex", "Fix the failures TRIAGE.md at the top of the repository lists, for this goal: $GOAL. Change production code before tests; never delete, skip or loosen a test. Commit once per group in TRIAGE.md and report done when the whole suite passes.", after("triage")),
		),
		crew("example-docs-writer", "Example: docs writer",
			"Write a README for this repository that a new contributor can build, run and configure from.",
			member("reader", "claude", "Read this repository for this goal: $GOAL: the build files, the entry points, the configuration and the tests. Write NOTES.md at the top of the repository with what the project does, how it is built and run, and how it is configured, every fact with the file it comes from. Commit it and report done.", now_),
			member("writer", "codex", "Write README.md from NOTES.md at the top of the repository, for this goal: $GOAL: what it is, a quick start, configuration, development. Keep every command exactly as it is run; mark anything NOTES.md does not establish as an open question rather than guessing. Commit it and report done.", after("reader")),
		),
		crew("example-dependency-upgrade", "Example: dependency upgrade",
			"Upgrade this repository's direct dependencies to their latest compatible versions, one at a time, with the test suite passing after each.",
			member("scout", "codex", "Survey this repository's direct dependencies for this goal: $GOAL. Write UPGRADES.md at the top of the repository: each dependency with its current and latest versions, the ones furthest behind first, and a note on any whose release notes announce a breaking change. Commit it and report done. Upgrade nothing yourself.", now_),
			member("upgrader", "claude", "Upgrade the dependencies UPGRADES.md at the top of the repository lists, for this goal: $GOAL, one dependency per commit, running the test suite after each and fixing what the upgrade breaks. Skip, and note in UPGRADES.md, any that cannot be made to pass. Report done with the list of what moved.", after("scout")),
		),
	}
}
```

And `Seed` in `internal/crew/store.go` (after `Delete`; `s.crews` and `s.commit` are the map and the per-crew save plan 1 leaves — adapt the two identifiers if plan 1 renamed them, nothing else):

```go
// Seed saves the crews of list whose ids the store does not have and returns
// the ids it added and the ids it skipped, each in list's order. A crew with
// the id of a saved one is left alone whatever it holds: seeding twice changes
// nothing, and a crew edited after a seed stays as the editor left it. The
// first crew that cannot be saved stops the seed, naming it; the ones saved
// before it stay.
func (s *Store) Seed(list []Crew) (added, skipped []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	added, skipped = []string{}, []string{}
	for _, c := range list {
		if _, exists := s.crews[c.ID]; exists {
			skipped = append(skipped, c.ID)
			continue
		}
		if err := s.commit(c); err != nil {
			return added, skipped, fmt.Errorf("seed %s: %w", c.ID, err)
		}
		added = append(added, c.ID)
	}
	return added, skipped, nil
}
```

- [ ] **Step 4: Run the crew tests**

Run: `go test -race ./internal/crew`
Expected: PASS.

- [ ] **Step 5: Write the failing config and serve tests**

`internal/config/config_test.go`:

```go
// CONDUCTOR_EXAMPLES turns the example-crew seeding on; it is not a config-file key.
func TestExamplesComesFromTheEnvironmentOnly(t *testing.T) {
	t.Setenv("CONDUCTOR_EXAMPLES", "1")
	cfg, err := Load("")
	if err != nil || !cfg.Examples {
		t.Fatalf("env: %v %v", cfg, err)
	}
	t.Setenv("CONDUCTOR_EXAMPLES", "")
	if cfg, err := Load(""); err != nil || cfg.Examples {
		t.Fatalf("unset: %v %v", cfg, err)
	}
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"examples": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a config file with examples should be rejected as an unknown field")
	}
}
```

`internal/cli/serve_test.go`:

```go
// --examples (or CONDUCTOR_EXAMPLES=1) seeds the four example crews into the
// data directory once: the second start adds nothing and says so.
func TestServeSeedsTheExamplesOnce(t *testing.T) {
	clearConductorEnv(t)
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "state")
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q, "dataDir": %q}`, work, work, data))
	logs := serveUntilListening(t, "--config", cfg, "--examples")
	if lines := logLines(logs, "example crews", "added=\"[example-todo-app example-test-fixer example-docs-writer example-dependency-upgrade]\""); len(lines) != 1 {
		t.Fatalf("first start:\n%s", logs)
	}
	// Plan 1's layout: one file per crew.
	if _, err := os.Stat(filepath.Join(data, "crews", "example-todo-app.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_EXAMPLES", "1")
	logs = serveUntilListening(t, "--config", cfg)
	if lines := logLines(logs, "example crews", "added=[]"); len(lines) != 1 {
		t.Fatalf("second start:\n%s", logs)
	}
	t.Setenv("CONDUCTOR_EXAMPLES", "")
	logs = serveUntilListening(t, "--config", cfg)
	if lines := logLines(logs, "example crews"); len(lines) != 0 {
		t.Fatalf("without the flag nothing is seeded:\n%s", logs)
	}
}
```

- [ ] **Step 6: Run them to see them fail**

Run: `go test ./internal/config -run TestExamplesComes && go test ./internal/cli -run TestServeSeeds`
Expected: FAIL to compile (`cfg.Examples undefined`); `flag provided but not defined: -examples`.

- [ ] **Step 7: Implement the config field, the flag and the seeding**

`internal/config/config.go`, in `Config` after `GeneratedAdminToken`:

```go
	// Examples seeds the example crews once at startup: CONDUCTOR_EXAMPLES=1
	// or conductor serve --examples. Not a config-file key.
	Examples bool `json:"-"`
```

In `applyEnv`, next to `CONDUCTOR_DEV`:

```go
	if v := getenv("CONDUCTOR_EXAMPLES"); v == "1" || v == "true" {
		cfg.Examples = true
	}
```

`internal/cli/serve.go`: the flag after `logLevel`:

```go
	examples := fs.Bool("examples", false, "seed the example crews once (env CONDUCTOR_EXAMPLES=1); a crew whose id exists is left alone")
```

after `if *dev { cfg.Dev = true }`:

```go
	if *examples {
		cfg.Examples = true
	}
```

after `srv, err := api.New(cfg, cat, log, ui, st)` succeeds:

```go
	if cfg.Examples {
		added, skipped, err := srv.SeedExampleCrews()
		if err != nil {
			return 1, fmt.Errorf("seed the example crews: %w", err)
		}
		log.Info("example crews", "added", added, "skipped", skipped)
	}
```

`internal/api/crews.go`:

```go
// SeedExampleCrews saves the example crews the store does not have
// (crew.Examples), with the server's default working directory, and returns
// what it added and what it skipped. conductor serve --examples and
// POST /api/crews/examples both come here.
func (s *Server) SeedExampleCrews() (added, skipped []string, err error) {
	if s.crews == nil {
		return nil, nil, errors.New("no data directory is configured")
	}
	return s.crews.Seed(crew.Examples(s.cfg.DefaultCwd, time.Now().UTC()))
}

// handleSeedExamples answers POST /api/crews/examples with {added, skipped}.
// The examples' agents, the claude and codex built-ins, are not checked
// against the catalog: a catalog that hides them still gets the examples,
// the editor shows the agent as unknown, and the launch refuses it.
func (s *Server) handleSeedExamples(w http.ResponseWriter, r *http.Request) {
	if s.crews == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	added, skipped, err := s.SeedExampleCrews()
	if err != nil {
		s.crewStoreError(w, "examples", err)
		return
	}
	s.log.Info("example crews seeded", "added", added, "skipped", skipped)
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
}
```

(`crews.go` needs `"time"` in its imports.) Route in `server.go`, before `PUT /api/crews/{id}`:

```go
	mux.HandleFunc("POST /api/crews/examples", s.requireAdmin(s.handleSeedExamples))
```

- [ ] **Step 8: Run the config and serve tests**

Run: `go test -race ./internal/config ./internal/cli -run 'TestExamplesComes|TestServeSeeds'`
Expected: PASS.

- [ ] **Step 9: Write the failing API test** in `internal/api/crews_test.go`:

```go
// POST /api/crews/examples seeds the examples once: a second call adds
// nothing, an edit survives it, a deleted example comes back.
func TestSeedExamplesOnce(t *testing.T) {
	e := newTestEnv(t, nil)
	if resp, _ := e.do("POST", "/api/crews/examples", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	resp, out := e.do("POST", "/api/crews/examples", adminToken, nil)
	if resp.StatusCode != http.StatusOK || len(out["added"].([]any)) != 4 || len(out["skipped"].([]any)) != 0 {
		t.Fatalf("first seed: %d %v", resp.StatusCode, out)
	}
	resp, got := e.do("GET", "/api/crews/example-todo-app", adminToken, nil)
	c, _ := got["crew"].(map[string]any)
	if resp.StatusCode != http.StatusOK || c == nil || c["cwd"] != e.root || c["isolation"] != "worktree" || len(c["members"].([]any)) != 4 {
		t.Fatalf("todo app: %d %v", resp.StatusCode, got)
	}
	if m := crewMember(c, 3); m["name"] != "tester" || m["agentId"] != "codex" || m["start"].(map[string]any)["member"] != "cli" {
		t.Fatalf("tester: %v", m)
	}
	// Edit it as the editor would, then seed again: the edit stays.
	for _, k := range []string{"id", "createdAt", "updatedAt"} {
		delete(c, k)
	}
	c["goal"] = "edited"
	e.sendCrew("PUT", "/api/crews/example-todo-app", c, http.StatusOK)
	if resp, _ := e.do("DELETE", "/api/crews/example-docs-writer", adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	_, out = e.do("POST", "/api/crews/examples", adminToken, nil)
	if added := out["added"].([]any); len(added) != 1 || added[0] != "example-docs-writer" || len(out["skipped"].([]any)) != 3 {
		t.Fatalf("second seed: %v", out)
	}
	if _, got := e.do("GET", "/api/crews/example-todo-app", adminToken, nil); got["crew"].(map[string]any)["goal"] != "edited" {
		t.Fatalf("the edit was lost: %v", got)
	}
}
```

- [ ] **Step 10: Run it**

Run: `go test -race ./internal/api -run TestSeedExamplesOnce`
Expected: PASS (the handler exists from Step 7; this step confirms the route end to end). If `GET /api/crews/{id}` answers a different envelope than `{crew}` after plan 1, follow plan 1's shape and adjust the test's reads, not the handler.

- [ ] **Step 11: The client call and the "Load the examples" button**

`web/app/composables/useSessions.ts`, after `duplicateCrew`:

```ts
    /** Seeds the four example crews, as `conductor serve --examples` does; a crew whose id exists is left alone. Returns the ids added and skipped. */
    loadExampleCrews: () => request<{ added: string[]; skipped: string[] }>('/api/crews/examples', { method: 'POST' }),
```

`web/app/pages/crews/[[id]].vue`, script (after `newCrew`):

```ts
const seeding = ref(false)

/** Seeds the examples and opens the first one; what already existed is left as it is. */
async function loadExamples() {
  seeding.value = true
  try {
    const r = await api.loadExampleCrews()
    await refresh()
    toast.add({
      title: r.added.length ? `${r.added.length} example ${r.added.length === 1 ? 'crew' : 'crews'} added` : 'The examples are here already',
      description: 'Edit or delete them like any crew.',
      icon: 'i-lucide-package-open',
      color: 'success',
    })
    const first = r.added[0] ?? r.skipped[0]
    if (first) router.push(`/crews/${encodeURIComponent(first)}`)
  } catch (e) {
    fail('Loading the examples failed', e)
  } finally {
    seeding.value = false
  }
}
```

Template, the `UEmpty` on the empty page:

```vue
          <UEmpty
            v-else-if="loaded && !list.length"
            icon="i-lucide-users"
            title="No crews yet"
            description="A crew is a saved team of agents: each with a role prompt, its own git worktree and a start condition. Launch it here or with conductor up <crew>. The examples show four shapes of crew; they are ordinary crews once loaded."
            :actions="[
              { label: 'New crew', icon: 'i-lucide-plus', onClick: newCrew },
              { label: 'Load the examples', icon: 'i-lucide-package-open', color: 'neutral', variant: 'outline', loading: seeding, onClick: loadExamples },
            ]"
            data-crews-empty
          />
```

- [ ] **Step 12: Typecheck, lint, headless check, commit**

Run: `npm --prefix web run typecheck && make lint`
Expected: clean.

Headless: with a fresh data directory (`CONDUCTOR_DATA_DIR=$(mktemp -d) bin/conductor serve --config conductor.example.json --listen 127.0.0.1:8099 &`), open `/crews`, wait for `[data-crews-empty]`, click `Load the examples`, wait for `[data-crew-item]`; expect four items and the URL `/crews/example-todo-app`; the editor shows `Example: todo app` with four member rows. Screenshot `examples.png`. Kill the server by pid.

```bash
git add internal/crew/examples.go internal/crew/examples_test.go internal/crew/store.go internal/config/config.go internal/config/config_test.go internal/cli/serve.go internal/cli/serve_test.go internal/api/crews.go internal/api/crews_test.go internal/api/server.go web/app/composables/useSessions.ts 'web/app/pages/crews/[[id]].vue'
git commit -m "crews: example crews seeded once by --examples, the env or the Crews page"
```

---

### Task 4: CLI completion

**Files:**
- Create: `internal/cli/completion.go`, `internal/cli/completion_test.go`
- Modify: `internal/cli/up.go:144-192` (`--ids`), `:224-265` (`apiClient.timeout`), `internal/cli/up_test.go`, `internal/cli/root.go:13-63` (usage and dispatch), `internal/agents/merge.go:52-77` (`CheckOwner`), `internal/agents/owner_test.go`

**Interfaces:**
- Consumes: `apiFlags`, `addAPIFlags`, `apiClient.do`, `parseInterspersed`, `homeOf`, `adapterIDs` (`internal/cli`); `fileOwner`, `geteuid` (`internal/agents`); `version.String()`; test helpers `stubServer`, `runWith`, `crews`, `noSecret`, `secretToken` (`up_test.go`).
- Produces:
```go
// internal/agents/merge.go
func CheckOwner(path string) error // nil when the caller owns path, or the system cannot say
// internal/cli/up.go
func runCrewIDs(ctx context.Context, api apiFlags, stdout io.Writer) (int, error) // conductor crews --ids
func (c *apiClient) crewIDs(ctx context.Context) ([]string, error)               // pages through GET /api/crews
var crewIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
// internal/cli/completion.go
const completionMark = "# conductor completion"
func runCompletion(args []string, stdout, stderr io.Writer) (int, error) // zsh | bash | install
func completionSpec() []commandSpec
func zshScript(spec []commandSpec) string
func bashScript(spec []commandSpec) string
func completionLine(shell string) string
func installCompletionLine(path, shell string) (changed bool, err error)
```

- [ ] **Step 1: Write the failing test for `CheckOwner`** in `internal/agents/owner_test.go`:

```go
// CheckOwner passes a file of one's own and refuses another user's, naming
// the file; where the system cannot say who owns a file, it passes.
func TestCheckOwnerRefusesAnotherUsersFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckOwner(path); err != nil {
		t.Fatalf("own file: %v", err)
	}
	if err := CheckOwner(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	fi, _ := os.Stat(path)
	if _, ok := fileOwner(fi); !ok {
		t.Skip("this platform does not report who owns a file")
	}
	was := geteuid
	geteuid = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { geteuid = was })
	if err := CheckOwner(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("another user's file: %v", err)
	}
}
```

- [ ] **Step 2: Run it, see it fail, implement**

Run: `go test ./internal/agents -run TestCheckOwner` → FAIL, `undefined: CheckOwner`. Add to `internal/agents/merge.go` after `ownedBy`:

```go
// CheckOwner refuses path unless the user running conductor owns it, where
// the system says who owns a file (Unix); elsewhere it passes. A missing
// path is its own error. conductor completion install uses it for the rc
// file it appends to.
func CheckOwner(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	owner, ok := fileOwner(fi)
	if !ok || owner == geteuid() {
		return nil
	}
	who := strconv.Itoa(owner)
	if u, err := user.LookupId(who); err == nil && u.Username != "" {
		who = u.Username
	}
	return fmt.Errorf("%s belongs to %s, not to the user running conductor: nothing written", path, who)
}
```

Run again: PASS.

- [ ] **Step 3: Write the failing tests for `crews --ids`** in `internal/cli/up_test.go`:

```go
// conductor crews --ids prints ids only, one per line, paging through the
// list, and leaves out anything that is not shaped like a crew id: the
// output is fed to a shell's completion.
func TestCrewsIDsPrintsOnlyWellFormedIDs(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("offset") == "0" {
			fmt.Fprint(w, `{"crews":[{"id":"alpha"},{"id":"bad id"},{"id":"evil\u001b[31m"},{"id":"$(rm)"},{"id":"-flag"}],"total":6}`)
			return
		}
		fmt.Fprint(w, `{"crews":[{"id":"beta-2"}],"total":6}`)
	}))
	t.Cleanup(srv.Close)
	code, stdout, stderr, err := crews(t, "--server", srv.URL, "--token", secretToken, "--ids")
	if code != 0 || err != nil || stdout != "alpha\nbeta-2\n" || stderr != "" {
		t.Fatalf("exit %d %v\nstdout: %q\nstderr: %q", code, err, stdout, stderr)
	}
	if len(pages) != 2 || !strings.Contains(pages[0], "offset=0") || !strings.Contains(pages[1], "offset=5") {
		t.Fatalf("pages: %v", pages)
	}
}

// Without a token, or without a server to reach, --ids prints nothing and
// exits 0: a completion must never put an error on the command line.
func TestCrewsIDsIsSilentWhenItCannotAsk(t *testing.T) {
	t.Setenv("CONDUCTOR_ADMIN_TOKEN", "")
	t.Setenv("CONDUCTOR_SERVER", "")
	code, stdout, stderr, err := crews(t, "--ids")
	if code != 0 || err != nil || stdout != "" || stderr != "" {
		t.Fatalf("no token: exit %d %v %q %q", code, err, stdout, stderr)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	code, stdout, stderr, err = crews(t, "--server", closed.URL, "--token", secretToken, "--ids")
	if code != 0 || err != nil || stdout != "" || stderr != "" {
		t.Fatalf("unreachable: exit %d %v %q %q", code, err, stdout, stderr)
	}
	srv, _ := stubServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthorized","message":"bad token"}}`)
	code, stdout, stderr, err = crews(t, "--server", srv.URL, "--token", secretToken, "--ids")
	if code != 0 || err != nil || stdout != "" || stderr != "" {
		t.Fatalf("refused: exit %d %v %q %q", code, err, stdout, stderr)
	}
	noSecret(t, stdout, stderr, err)
}
```

- [ ] **Step 4: Run them, see them fail, implement `--ids`**

Run: `go test ./internal/cli -run TestCrewsIDs` → FAIL, `flag provided but not defined: -ids`.

In `internal/cli/up.go`: the `apiClient` gains a timeout, `do` uses it:

```go
type apiClient struct {
	base    string
	host    string
	token   string
	timeout time.Duration // per request; requestTimeout when zero
}
```

```go
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(c.timeout, requestTimeout))
```

and `transportError`'s deadline message uses `cmp.Or(c.timeout, requestTimeout)` too. Then:

```go
// idsTimeout bounds conductor crews --ids: a completion that waits is worse
// than one that offers nothing.
const idsTimeout = 2 * time.Second

// crewIDPattern is a crew id as the server makes them; --ids prints nothing
// else, since its output is read back by a shell.
var crewIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
```

In `runCrews`, the flag after `api := addAPIFlags(fs)`, and the dispatch before `c, err := api.client()`:

```go
	ids := fs.Bool("ids", false, "print the crew ids only, one per line, for shell completion: silent and exit 0 when the server cannot be reached or the token is missing")
```

```go
	if *ids {
		return runCrewIDs(ctx, api, stdout)
	}
```

```go
// runCrewIDs prints the saved crews' ids, one per line, for shell
// completion: nothing and exit 0 when no client can be made (no token) or
// the server does not answer, and never an error, since the output lands on
// a command line. Ids not shaped like the server's are left out.
func runCrewIDs(ctx context.Context, api apiFlags, stdout io.Writer) (int, error) {
	c, err := api.client()
	if err != nil {
		return 0, nil
	}
	c.timeout = idsTimeout
	ids, err := c.crewIDs(ctx)
	if err != nil {
		return 0, nil
	}
	for _, id := range ids {
		if crewIDPattern.MatchString(id) {
			fmt.Fprintln(stdout, id)
		}
	}
	return 0, nil
}

// crewIDs reads every crew id, page by page: GET /api/crews answers at most
// 500 per page with the total. A server that answers no total (an older
// one) gives one page.
func (c *apiClient) crewIDs(ctx context.Context) ([]string, error) {
	var ids []string
	for offset := 0; ; {
		var reply struct {
			Crews []struct {
				ID string `json:"id"`
			} `json:"crews"`
			Total int `json:"total"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/crews?offset=%d&limit=500", offset), &reply); err != nil {
			return nil, err
		}
		for _, cr := range reply.Crews {
			ids = append(ids, cr.ID)
		}
		offset += len(reply.Crews)
		if len(reply.Crews) == 0 || offset >= reply.Total {
			return ids, nil
		}
	}
}
```

(`up.go` needs `"regexp"` in its imports.) Run: `go test -race ./internal/cli -run 'TestCrews|TestUp'` → PASS.

- [ ] **Step 5: Write the failing completion tests** `internal/cli/completion_test.go`:

```go
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeConductor is a conductor that answers `crews --ids` with two ids, for
// the scripts to run as the command typed (words[1] / COMP_WORDS[0]).
func fakeConductor(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "conductor")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n[ \"$1\" = crews ] && [ \"$2\" = --ids ] && printf 'alpha\\nbeta\\n'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// scriptFile writes the completion script for shell to a file.
func scriptFile(t *testing.T, shell string) string {
	t.Helper()
	code, stdout, stderr, err := runWith(t, func(_ context.Context, args []string, stdout, stderr io.Writer) (int, error) { return runCompletion(args, stdout, stderr) }, shell)
	if code != 0 || err != nil {
		t.Fatalf("completion %s: exit %d %v\n%s", shell, code, err, stderr)
	}
	p := filepath.Join(t.TempDir(), "conductor."+shell)
	if err := os.WriteFile(p, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// complete runs the script under the real shell (skipped when absent) with
// the completion system stubbed, completing the last of words, and returns
// the candidates. dir is the working directory, for file completion.
func complete(t *testing.T, shell, script, dir string, words ...string) []string {
	t.Helper()
	if _, err := exec.LookPath(shell); err != nil {
		t.Skipf("%s is not installed", shell)
	}
	var cmd *exec.Cmd
	switch shell {
	case "zsh":
		cmd = exec.Command("zsh", "-f", "-c", `compdef() { :; }; _files() { print -r -- "<files>" }; compadd() { local a; for a; do [[ $a == -- ]] || print -r -- "$a"; done }; source "$1"; shift; words=("$@"); CURRENT=${#words}; _conductor`, "zsh", script)
	case "bash":
		cmd = exec.Command("bash", "--noprofile", "--norc", "-c", `source "$1"; shift; COMP_WORDS=("$@"); COMP_CWORD=$(( ${#COMP_WORDS[@]} - 1 )); _conductor; (( ${#COMPREPLY[@]} )) && printf '%s\n' "${COMPREPLY[@]}"; true`, "bash", script)
	}
	cmd.Args = append(cmd.Args, words...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", shell, err, out)
	}
	return strings.Fields(string(out))
}

// Both scripts parse under their shell.
func TestCompletionScriptsParse(t *testing.T) {
	for _, sh := range []string{"zsh", "bash"} {
		t.Run(sh, func(t *testing.T) {
			if _, err := exec.LookPath(sh); err != nil {
				t.Skipf("%s is not installed", sh)
			}
			if out, err := exec.Command(sh, "-n", scriptFile(t, sh)).CombinedOutput(); err != nil {
				t.Fatalf("%s -n: %v\n%s", sh, err, out)
			}
		})
	}
}

// The scripts complete subcommands, flags, flag values, positional words
// and, for up, the crew ids the typed conductor prints.
func TestCompletionCompletes(t *testing.T) {
	for _, sh := range []string{"zsh", "bash"} {
		t.Run(sh, func(t *testing.T) {
			script := scriptFile(t, sh)
			bin := fakeConductor(t)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "x.json"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			has := func(words []string, want ...string) bool {
				for _, w := range want {
					if !slices.Contains(words, w) {
						return false
					}
				}
				return true
			}
			if got := complete(t, sh, script, dir, bin, ""); !has(got, "serve", "host", "notify", "up", "crews", "hooks", "skill", "completion", "version") {
				t.Fatalf("subcommands: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "up", ""); !slices.Equal(got, []string{"alpha", "beta"}) {
				t.Fatalf("crew ids: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "up", "--"); !has(got, "--server", "--token", "--open") {
				t.Fatalf("up flags: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "up", "--server", ""); len(got) != 0 {
				t.Fatalf("a free value completes nothing: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "serve", "--log-level", ""); !slices.Equal(got, []string{"debug", "info", "warn", "error"}) {
				t.Fatalf("log level: %v", got)
			}
			got := complete(t, sh, script, dir, bin, "serve", "--config", "")
			if (sh == "zsh" && !slices.Equal(got, []string{"<files>"})) || (sh == "bash" && !slices.Equal(got, []string{"x.json"})) {
				t.Fatalf("files: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "hooks", ""); !slices.Equal(got, []string{"install", "status"}) {
				t.Fatalf("hooks words: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "hooks", "install", ""); !has(got, "all", "claude", "codex") {
				t.Fatalf("adapters: %v", got)
			}
			if got := complete(t, sh, script, dir, bin, "completion", ""); !slices.Equal(got, []string{"zsh", "bash", "install"}) {
				t.Fatalf("completion words: %v", got)
			}
			if sh == "bash" {
				if got := complete(t, sh, script, dir, bin, "up", "a"); !slices.Equal(got, []string{"alpha"}) {
					t.Fatalf("bash filters by the typed prefix: %v", got)
				}
			}
		})
	}
}

// conductor completion prints a script for zsh or bash and refuses the rest.
func TestCompletionUsage(t *testing.T) {
	run := func(args ...string) (int, string, string, error) {
		return runWith(t, func(_ context.Context, a []string, o, e io.Writer) (int, error) { return runCompletion(a, o, e) }, args...)
	}
	if code, out, _, err := run("zsh"); code != 0 || err != nil || !strings.HasPrefix(out, "#compdef conductor\n") || !strings.Contains(out, "compdef _conductor conductor") {
		t.Fatalf("zsh: %d %v\n%s", code, err, out)
	}
	if code, out, _, err := run("bash"); code != 0 || err != nil || !strings.Contains(out, "complete -F _conductor conductor") {
		t.Fatalf("bash: %d %v\n%s", code, err, out)
	}
	for _, args := range [][]string{{}, {"fish"}, {"zsh", "extra"}} {
		if code, _, _, err := run(args...); code != 2 || err == nil {
			t.Fatalf("%v: %d %v", args, code, err)
		}
	}
}

// completion install appends one marked line to the shell's rc file, once;
// the shell comes from $SHELL unless --shell says; a missing file is made.
func TestCompletionInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	run := func(args ...string) (int, string, string, error) {
		return runWith(t, func(_ context.Context, a []string, o, e io.Writer) (int, error) { return runCompletion(a, o, e) }, append([]string{"install"}, args...)...)
	}
	rc := filepath.Join(home, ".zshrc")
	code, out, _, err := run()
	if code != 0 || err != nil || !strings.Contains(out, rc) {
		t.Fatalf("first: %d %v\n%s", code, err, out)
	}
	want := completionLine("zsh") + "\n"
	if b, _ := os.ReadFile(rc); string(b) != want {
		t.Fatalf("rc:\n%s", b)
	}
	code, out, _, err = run()
	if code != 0 || err != nil || !strings.Contains(out, "nothing to change") {
		t.Fatalf("second: %d %v\n%s", code, err, out)
	}
	if b, _ := os.ReadFile(rc); string(b) != want {
		t.Fatalf("rc changed on the second install:\n%s", b)
	}
	// A file without a final newline gets one before the line; --shell and --rc override the defaults.
	custom := filepath.Join(home, "rc")
	if err := os.WriteFile(custom, []byte("alias l='ls'"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _, err := run("--shell", "bash", "--rc", custom); code != 0 || err != nil {
		t.Fatalf("custom: %d %v", code, err)
	}
	if b, _ := os.ReadFile(custom); string(b) != "alias l='ls'\n"+completionLine("bash")+"\n" {
		t.Fatalf("custom rc:\n%s", b)
	}
	t.Setenv("SHELL", "/usr/bin/fish")
	if code, _, _, err := run(); code != 2 || err == nil {
		t.Fatalf("fish: %d %v", code, err)
	}
}
```

(`completion_test.go` needs `"context"` and `"io"` in its imports.)

- [ ] **Step 6: Run them to see them fail**

Run: `go test ./internal/cli -run TestCompletion`
Expected: FAIL to compile, `undefined: runCompletion`.

- [ ] **Step 7: Write `internal/cli/completion.go`**

```go
package cli

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/phenixrizen/conductor/internal/agents"
	"github.com/phenixrizen/conductor/internal/version"
)

const completionUsage = `Usage:
  conductor completion zsh|bash
      print a completion script for the shell: subcommands, flags and their
      values, and for conductor up the crew ids, from conductor crews --ids
      as you type (CONDUCTOR_SERVER and CONDUCTOR_ADMIN_TOKEN from the
      environment; nothing without them or without the server)
  conductor completion install [--shell zsh|bash] [--rc FILE]
      add one marked line that loads the script to ~/.zshrc or ~/.bashrc,
      the shell from $SHELL unless --shell says; run again, it changes
      nothing; a file that is not yours is refused
`

// completionMark marks the line completion install writes, so that a second
// install finds it and writes nothing.
const completionMark = "# conductor completion"

// How a flag's value is completed.
type flagKind int

const (
	flagBool  flagKind = iota // takes no value
	flagValue                 // takes a value nothing can guess: completes nothing
	flagFile                  // takes a path
	flagEnum                  // takes one of values
)

type flagSpec struct {
	name   string
	kind   flagKind
	values []string
}

// commandSpec is a subcommand as the scripts know it: its flags (the FlagSet
// of its run function), the words its first argument may be, the words its
// second may be after each first, and whether its argument is a crew id.
type commandSpec struct {
	name   string
	flags  []flagSpec
	words  []string
	after  map[string][]string
	crewID bool
}

// completionSpec lists the subcommands of root.go with the flags their run
// functions define. A new flag or command is added here with it.
func completionSpec() []commandSpec {
	levels := []string{"debug", "info", "warn", "error"}
	api := []flagSpec{{name: "--server", kind: flagValue}, {name: "--token", kind: flagValue}}
	return []commandSpec{
		{name: "serve", flags: []flagSpec{{name: "--config", kind: flagFile}, {name: "--listen", kind: flagValue}, {name: "--dev"}, {name: "--log-level", kind: flagEnum, values: levels}, {name: "--examples"}}},
		{name: "host", flags: []flagSpec{{name: "--server", kind: flagValue}, {name: "--token", kind: flagValue}, {name: "--name", kind: flagValue}, {name: "--host-name", kind: flagValue}, {name: "--agent", kind: flagValue}, {name: "--cwd", kind: flagFile}, {name: "--relay-only"}, {name: "--no-local"}, {name: "--stun", kind: flagValue}, {name: "--scrollback", kind: flagValue}, {name: "--file-view", kind: flagEnum, values: []string{"view", "control", "off"}}, {name: "--signal-pattern", kind: flagValue}, {name: "--log-level", kind: flagEnum, values: levels}}},
		{name: "notify", flags: []flagSpec{{name: "--state", kind: flagEnum, values: []string{"needs_input", "working", "done", "clear"}}, {name: "--message", kind: flagValue}, {name: "--event", kind: flagEnum, values: []string{"progress", "artifact", "handoff", "tool_use", "tool_denied", "error"}}, {name: "--url", kind: flagValue}, {name: "--to", kind: flagValue}, {name: "--tool", kind: flagValue}, {name: "--codex"}, {name: "--claude-hook"}, {name: "--codex-hook"}, {name: "--copilot-hook"}, {name: "--cursor-hook"}, {name: "--agy-hook"}, {name: "--goose-hook"}, {name: "--quiet"}}},
		{name: "up", flags: append(slices.Clone(api), flagSpec{name: "--open"}), crewID: true},
		{name: "crews", flags: append(slices.Clone(api), flagSpec{name: "--ids"})},
		{name: "hooks", flags: []flagSpec{{name: "--home", kind: flagFile}, {name: "--data-dir", kind: flagFile}}, words: []string{"install", "status"}, after: map[string][]string{"install": append([]string{"all"}, strings.Split(adapterIDs(), ", ")...)}},
		{name: "skill"},
		{name: "completion", flags: []flagSpec{{name: "--shell", kind: flagEnum, values: []string{"zsh", "bash"}}, {name: "--rc", kind: flagFile}}, words: []string{"zsh", "bash", "install"}},
		{name: "version"},
	}
}

func names(spec []commandSpec) string {
	out := make([]string, len(spec))
	for i, c := range spec {
		out[i] = c.name
	}
	return strings.Join(out, " ")
}

func flagNames(flags []flagSpec) string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = f.name
	}
	return strings.Join(out, " ")
}

// zshScript is the zsh completion script for spec: a function on words and
// CURRENT that hands compadd what fits, bound with compdef. The crew ids come
// from `crews --ids` run as the command typed (words[1]), so a conductor not
// on PATH still completes; they are passed to compadd as words, never
// evaluated.
func zshScript(spec []commandSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#compdef conductor\n# conductor completion for zsh, generated by conductor %s. Load with: source <(conductor completion zsh)\n", version.String())
	b.WriteString("_conductor() {\n  local cur=${words[CURRENT]} cmd=${words[2]} sub=${words[3]} prev=${words[CURRENT-1]}\n")
	fmt.Fprintf(&b, "  if (( CURRENT == 2 )); then compadd -- %s; return; fi\n", names(spec))
	b.WriteString("  if [[ $cur == -* ]]; then\n    case $cmd in\n")
	for _, c := range spec {
		if len(c.flags) > 0 {
			fmt.Fprintf(&b, "      %s) compadd -- %s ;;\n", c.name, flagNames(c.flags))
		}
	}
	b.WriteString("    esac\n    return\n  fi\n  case \"$cmd $prev\" in\n")
	for _, c := range spec {
		for _, f := range c.flags {
			switch f.kind {
			case flagEnum:
				fmt.Fprintf(&b, "    %q) compadd -- %s; return ;;\n", c.name+" "+f.name, strings.Join(f.values, " "))
			case flagFile:
				fmt.Fprintf(&b, "    %q) _files; return ;;\n", c.name+" "+f.name)
			case flagValue:
				fmt.Fprintf(&b, "    %q) return ;;\n", c.name+" "+f.name)
			}
		}
	}
	b.WriteString("  esac\n  case $cmd in\n")
	for _, c := range spec {
		switch {
		case c.crewID:
			fmt.Fprintf(&b, "    %s) local -a ids; ids=(${(f)\"$(${words[1]} crews --ids 2>/dev/null)\"}); (( ${#ids} )) && compadd -- $ids ;;\n", c.name)
		case len(c.words) > 0:
			fmt.Fprintf(&b, "    %s)\n      if (( CURRENT == 3 )); then compadd -- %s\n", c.name, strings.Join(c.words, " "))
			for _, w := range c.words {
				if more := c.after[w]; len(more) > 0 {
					fmt.Fprintf(&b, "      elif [[ $sub == %s ]] && (( CURRENT == 4 )); then compadd -- %s\n", w, strings.Join(more, " "))
				}
			}
			b.WriteString("      fi ;;\n")
		}
	}
	b.WriteString("  esac\n}\ncompdef _conductor conductor\n")
	return b.String()
}

// bashScript is the bash completion script for spec, the same shape on
// COMP_WORDS, COMP_CWORD and COMPREPLY; compgen -W filters by the typed
// prefix. The crew ids come from `crews --ids` run as COMP_WORDS[0].
func bashScript(spec []commandSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# conductor completion for bash, generated by conductor %s. Load with: source <(conductor completion bash)\n", version.String())
	b.WriteString("_conductor() {\n  local cur=${COMP_WORDS[COMP_CWORD]} cmd=${COMP_WORDS[1]} sub=${COMP_WORDS[2]} prev=${COMP_WORDS[COMP_CWORD-1]}\n  COMPREPLY=()\n")
	fmt.Fprintf(&b, "  if (( COMP_CWORD == 1 )); then COMPREPLY=($(compgen -W %q -- \"$cur\")); return; fi\n", names(spec))
	b.WriteString("  if [[ $cur == -* ]]; then\n    case $cmd in\n")
	for _, c := range spec {
		if len(c.flags) > 0 {
			fmt.Fprintf(&b, "      %s) COMPREPLY=($(compgen -W %q -- \"$cur\")) ;;\n", c.name, flagNames(c.flags))
		}
	}
	b.WriteString("    esac\n    return\n  fi\n  case \"$cmd $prev\" in\n")
	for _, c := range spec {
		for _, f := range c.flags {
			switch f.kind {
			case flagEnum:
				fmt.Fprintf(&b, "    %q) COMPREPLY=($(compgen -W %q -- \"$cur\")); return ;;\n", c.name+" "+f.name, strings.Join(f.values, " "))
			case flagFile:
				fmt.Fprintf(&b, "    %q) COMPREPLY=($(compgen -f -- \"$cur\")); return ;;\n", c.name+" "+f.name)
			case flagValue:
				fmt.Fprintf(&b, "    %q) return ;;\n", c.name+" "+f.name)
			}
		}
	}
	b.WriteString("  esac\n  case $cmd in\n")
	for _, c := range spec {
		switch {
		case c.crewID:
			fmt.Fprintf(&b, "    %s) COMPREPLY=($(compgen -W \"$(\"${COMP_WORDS[0]}\" crews --ids 2>/dev/null)\" -- \"$cur\")) ;;\n", c.name)
		case len(c.words) > 0:
			fmt.Fprintf(&b, "    %s)\n      if (( COMP_CWORD == 2 )); then COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", c.name, strings.Join(c.words, " "))
			for _, w := range c.words {
				if more := c.after[w]; len(more) > 0 {
					fmt.Fprintf(&b, "      elif [[ $sub == %s ]] && (( COMP_CWORD == 3 )); then COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", w, strings.Join(more, " "))
				}
			}
			b.WriteString("      fi ;;\n")
		}
	}
	b.WriteString("  esac\n}\ncomplete -F _conductor conductor\n")
	return b.String()
}

// runCompletion prints a script or installs the line that loads it.
func runCompletion(args []string, stdout, stderr io.Writer) (int, error) {
	if len(args) == 0 {
		fmt.Fprint(stderr, completionUsage)
		return 2, errors.New("completion: zsh, bash or install is required")
	}
	switch args[0] {
	case "zsh", "bash":
		if len(args) != 1 {
			fmt.Fprint(stderr, completionUsage)
			return 2, fmt.Errorf("completion %s takes no arguments", args[0])
		}
		script := zshScript(completionSpec())
		if args[0] == "bash" {
			script = bashScript(completionSpec())
		}
		_, err := io.WriteString(stdout, script)
		return 0, err
	case "install":
		return runCompletionInstall(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stderr, completionUsage)
		return 0, nil
	}
	fmt.Fprint(stderr, completionUsage)
	return 2, fmt.Errorf("completion: unknown shell %q (zsh or bash)", args[0])
}

// runCompletionInstall appends the line to the rc file of the shell.
func runCompletionInstall(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("completion install", flag.ContinueOnError)
	fs.SetOutput(stderr)
	shell := fs.String("shell", "", "zsh or bash (default: the name of $SHELL)")
	rc := fs.String("rc", "", "the file to add the line to (default: ~/.zshrc or ~/.bashrc)")
	fs.Usage = func() {
		fmt.Fprint(stderr, completionUsage)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if len(rest) != 0 {
		fs.Usage()
		return 2, errors.New("completion install takes no arguments")
	}
	sh := cmp.Or(*shell, filepath.Base(os.Getenv("SHELL")))
	if sh != "zsh" && sh != "bash" {
		return 2, fmt.Errorf("completion install: say which shell with --shell zsh or --shell bash (SHELL is %q)", os.Getenv("SHELL"))
	}
	path := *rc
	if path == "" {
		home, err := homeOf("")
		if err != nil {
			return 1, err
		}
		path = filepath.Join(home, "."+sh+"rc")
	}
	changed, err := installCompletionLine(path, sh)
	if err != nil {
		return 1, fmt.Errorf("completion install: %w", err)
	}
	if !changed {
		fmt.Fprintf(stdout, "%s has the line already; nothing to change\n", path)
		return 0, nil
	}
	fmt.Fprintf(stdout, "wrote %s:\n  %s\nopen a new shell, or run that line, to complete conductor\n", path, completionLine(sh))
	return 0, nil
}

// completionLine is the line installed: it loads the script this conductor
// prints when conductor is on PATH, and is marked so install finds it again.
func completionLine(shell string) string {
	return "command -v conductor >/dev/null 2>&1 && source <(conductor completion " + shell + ") " + completionMark
}

// installCompletionLine appends completionLine(shell) to path unless a line
// of it carries completionMark already, and reports whether it wrote. The
// file must be the caller's own (agents.CheckOwner): under sudo, another
// user's rc file is refused. A missing file is made, 0600.
func installCompletionLine(path, shell string) (bool, error) {
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return false, err
	default:
		if err := agents.CheckOwner(path); err != nil {
			return false, err
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			if strings.Contains(line, completionMark) {
				return false, nil
			}
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	add := completionLine(shell) + "\n"
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		add = "\n" + add
	}
	if _, err := f.WriteString(add); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}
```

`internal/cli/root.go`: the dispatch case and the usage lines:

```go
	case "completion":
		return runCompletion(args[1:], stdout, stderr)
```

```
  conductor crews [--server URL] [--token T] [--ids]
                               list the saved crews (--ids: ids only, for completion)
  conductor completion zsh|bash
                               print a shell completion script
  conductor completion install [--shell zsh|bash] [--rc FILE]
                               add the line that loads it to ~/.zshrc or ~/.bashrc
```

Also add to `TestRunDispatchesUpAndCrews` in `up_test.go` (or a new `TestRunDispatchesCompletion`): `Run(ctx, []string{"completion", "zsh"}, nil, &out, &errOut)` → exit 0, `out` starts with `#compdef conductor`.

- [ ] **Step 8: Run the whole CLI package**

Run: `go test -race -count=1 ./internal/cli ./internal/agents && make lint`
Expected: PASS; the shell tests skip where zsh or bash is missing and say so (this box has both: they must run).

- [ ] **Step 9: Try it in a real shell** (reported, not automated)

`go run ./cmd/conductor completion zsh > /tmp/claude-1000/-home-nater-go-src-github-com-phenixrizen-conductor/a75c70c0-928a-4c03-ab5e-19119aca1b6c/scratchpad/c.zsh && zsh -ic 'source "$0"; echo ok' /tmp/claude-1000/-home-nater-go-src-github-com-phenixrizen-conductor/a75c70c0-928a-4c03-ab5e-19119aca1b6c/scratchpad/c.zsh` — expected `ok` with no warning. Interactive TAB behaviour is the spec's "Open verification (round 3)" item.

- [ ] **Step 10: Commit**

```bash
git add internal/cli/completion.go internal/cli/completion_test.go internal/cli/up.go internal/cli/up_test.go internal/cli/root.go internal/agents/merge.go internal/agents/owner_test.go
git commit -m "cli: completion scripts for zsh and bash, crews --ids, completion install"
```

---

### Task 5: The sidebar rail

**Files:**
- Create: `web/app/utils/sidebar.ts`, `web/app/utils/sidebar.test.ts`, `web/app/components/SidebarRail.vue`
- Modify: `web/app/composables/useSidebar.ts` (whole file), `web/app/layouts/default.vue:73-166` (shortcuts and the sidebar), `web/app/composables/useShortcuts.ts:231` (the row's label)
- Delete: `web/app/components/SidebarReveal.vue`, and its `<template #leading><SidebarReveal /></template>` blocks in `web/app/pages/index.vue:35-37`, `events.vue:83-85`, `agents.vue:133-135`, `crews/[[id]].vue:324-326`, `sessions/[id].vue:274-276`, `components/CrewRunHeader.vue:91-93`, `pages/carousel.vue:332-334`; in `pages/wall.vue:186-191` only the `<SidebarReveal />` line (the back button stays)

**Interfaces:**
- Consumes: `groupSessions` (`web/app/utils/sessions.ts`), `SessionInfo`, `useAttention`, `useEvents().routes`, `useLaunchModal`, `SessionAvatar`, `sidebarRunFor`, the `crewRunNames` state the layout keeps; Nuxt UI `UDashboardSidebar` (`collapsible`, `v-model:collapsed`, slot prop `collapsed`, root `min-w-16`), `UNavigationMenu` (`collapsed`, `tooltip`).
- Produces:
```ts
// web/app/utils/sidebar.ts
export type SidebarMode = 'full' | 'rail'
export const SIDEBAR_KEY = 'conductor.sidebar.mode'
export const LEGACY_SIDEBAR_KEY = 'conductor.sidebar.hidden'
export interface KeyValueStore { getItem(key: string): string | null; setItem(key: string, value: string): void; removeItem(key: string): void }
export function readSidebarMode(storage: KeyValueStore): SidebarMode
export function writeSidebarMode(storage: KeyValueStore, mode: SidebarMode): void
export type RailDot = 'needs' | 'running' | 'idle' | 'exited'
export interface RailItem { id: string; name: string; agentId: string; dot: RailDot; message?: string }
export interface RailGroup { runId?: string; label?: string; items: RailItem[] }
export function railGroups(sessions: readonly SessionInfo[], runNames?: Readonly<Record<string, string>>): RailGroup[]
// web/app/composables/useSidebar.ts
export function useSidebar(): { mode: Ref<SidebarMode>; rail: ComputedRef<boolean>; collapse(): void; expand(): void; toggle(): void }
// web/app/components/SidebarRail.vue — props { runId?: string; runName?: string }, emits 'search'; data-rail, data-rail-session
```

- [ ] **Step 1: Write the failing vitest** `web/app/utils/sidebar.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { SessionInfo } from '~/composables/useSessions'
import { LEGACY_SIDEBAR_KEY, SIDEBAR_KEY, railGroups, readSidebarMode, writeSidebarMode, type KeyValueStore } from './sidebar'

function memory(initial: Record<string, string> = {}): KeyValueStore & { data: Map<string, string> } {
  const data = new Map(Object.entries(initial))
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) }
}

const session = (over: Partial<SessionInfo>): SessionInfo =>
  ({ id: 'id', name: 'name', kind: 'server', agentId: 'claude', command: [], cwd: '/w', status: 'running', cols: 80, rows: 24, createdAt: '2026-10-01T09:00:00Z', ...over }) as SessionInfo

describe('readSidebarMode', () => {
  it('reads the saved mode', () => {
    expect(readSidebarMode(memory({ [SIDEBAR_KEY]: 'rail' }))).toBe('rail')
    expect(readSidebarMode(memory({ [SIDEBAR_KEY]: 'full' }))).toBe('full')
    expect(readSidebarMode(memory())).toBe('full')
    expect(readSidebarMode(memory({ [SIDEBAR_KEY]: 'sideways' }))).toBe('full')
  })

  it('moves an old hidden flag to the new key: hidden became the rail', () => {
    const s = memory({ [LEGACY_SIDEBAR_KEY]: '1' })
    expect(readSidebarMode(s)).toBe('rail')
    expect(s.data.get(SIDEBAR_KEY)).toBe('rail')
    expect(s.data.has(LEGACY_SIDEBAR_KEY)).toBe(false)
    const shown = memory({ [LEGACY_SIDEBAR_KEY]: '0' })
    expect(readSidebarMode(shown)).toBe('full')
    expect(shown.data.get(SIDEBAR_KEY)).toBe('full')
  })

  it('is full when storage throws, and writing never throws', () => {
    const broken: KeyValueStore = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
      removeItem: () => {},
    }
    expect(readSidebarMode(broken)).toBe('full')
    expect(() => writeSidebarMode(broken, 'rail')).not.toThrow()
  })
})

describe('railGroups', () => {
  it('keeps the sidebar order and puts the members of a run together under its name', () => {
    const list = [
      session({ id: 'a', name: 'alone', createdAt: '2026-10-01T09:05:00Z' }),
      session({ id: 'b', name: 'core', crew: { runId: 'r1', crewId: 'api-sweep', member: 'core' }, createdAt: '2026-10-01T09:04:00Z' }),
      session({ id: 'c', name: 'waits', attention: { state: 'needs_input', since: '2026-10-01T09:06:00Z', message: 'Allow?' } }),
      session({ id: 'd', name: 'lead', crew: { runId: 'r1', crewId: 'api-sweep', member: 'lead' }, createdAt: '2026-10-01T09:03:00Z' }),
      session({ id: 'e', name: 'other', crew: { runId: 'r2', crewId: 'docs', member: 'w' }, status: 'exited', endedAt: '2026-10-01T09:07:00Z' }),
    ]
    const groups = railGroups(list, { r1: 'API sweep' })
    expect(groups.map((g) => [g.label, g.items.map((i) => `${i.id}:${i.dot}`)])).toEqual([
      [undefined, ['c:needs']],
      [undefined, ['a:running']],
      ['API sweep', ['b:running', 'd:running']],
      ['docs', ['e:exited']],
    ])
    expect(groups[0]!.items[0]!.message).toBe('Allow?')
  })

  it('shows a starting session with the idle dot', () => {
    expect(railGroups([session({ status: 'starting' })])[0]!.items[0]!.dot).toBe('idle')
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `npm --prefix web test -- sidebar`
Expected: FAIL, `Cannot find module './sidebar'`.

- [ ] **Step 3: Implement `web/app/utils/sidebar.ts`**

```ts
import type { SessionInfo } from '~/composables/useSessions'
import { groupSessions } from './sessions'

/** The desktop sidebar's two modes: everything, or an icon rail. */
export type SidebarMode = 'full' | 'rail'
export const SIDEBAR_KEY = 'conductor.sidebar.mode'
/** The key before the rail existed: '1' meant hidden, which is now the rail. */
export const LEGACY_SIDEBAR_KEY = 'conductor.sidebar.hidden'

/** What localStorage offers; a fake in tests. */
export interface KeyValueStore {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

/** The mode saved for this browser. An old hidden flag is moved to the new key on the way. Anything unreadable is full. */
export function readSidebarMode(storage: KeyValueStore): SidebarMode {
  try {
    const mode = storage.getItem(SIDEBAR_KEY)
    if (mode === 'rail' || mode === 'full') return mode
    const legacy = storage.getItem(LEGACY_SIDEBAR_KEY)
    if (legacy !== null) {
      const migrated: SidebarMode = legacy === '1' ? 'rail' : 'full'
      storage.setItem(SIDEBAR_KEY, migrated)
      storage.removeItem(LEGACY_SIDEBAR_KEY)
      return migrated
    }
  } catch {
    /* no storage: full */
  }
  return 'full'
}

export function writeSidebarMode(storage: KeyValueStore, mode: SidebarMode): void {
  try {
    storage.setItem(SIDEBAR_KEY, mode)
  } catch {
    /* ignore */
  }
}

export type RailDot = 'needs' | 'running' | 'idle' | 'exited'
export interface RailItem {
  id: string
  name: string
  agentId: string
  dot: RailDot
  message?: string
}
export interface RailGroup {
  runId?: string
  label?: string
  items: RailItem[]
}

/**
 * The rail's avatars in the full sidebar's order (needs you, running,
 * exited), the members of one run kept together under a thin label, the
 * run's name from `runNames` or its crew's id. Within a section the
 * sessions of no run come first.
 */
export function railGroups(sessions: readonly SessionInfo[], runNames: Readonly<Record<string, string>> = {}): RailGroup[] {
  const g = groupSessions([...sessions])
  const out: RailGroup[] = []
  const item = (s: SessionInfo, dot: RailDot): RailItem => ({ id: s.id, name: s.name, agentId: s.agentId, dot, message: s.attention?.message || undefined })
  const section = (list: SessionInfo[], dot: (s: SessionInfo) => RailDot) => {
    const loose = list.filter((s) => !s.crew)
    if (loose.length) out.push({ items: loose.map((s) => item(s, dot(s))) })
    const byRun = new Map<string, SessionInfo[]>()
    for (const s of list) if (s.crew) byRun.set(s.crew.runId, [...(byRun.get(s.crew.runId) ?? []), s])
    for (const [runId, members] of byRun) out.push({ runId, label: runNames[runId] || members[0]?.crew?.crewId, items: members.map((s) => item(s, dot(s))) })
  }
  section(g.needs, () => 'needs')
  section(g.running, (s) => (s.status === 'running' ? 'running' : 'idle'))
  section(g.exited, () => 'exited')
  return out
}
```

- [ ] **Step 4: Run the vitest**

Run: `npm --prefix web test -- sidebar`
Expected: PASS.

- [ ] **Step 5: Rewrite `web/app/composables/useSidebar.ts`**

```ts
import { readSidebarMode, writeSidebarMode, type SidebarMode } from '~/utils/sidebar'

/**
 * The desktop sidebar's mode: full, or the icon rail. Persisted per browser,
 * so a wall left on a spare monitor keeps its rail after a reload; an old
 * "hidden" choice reads as the rail. On narrow screens the sidebar is a
 * slideover and the mode is ignored.
 */
export function useSidebar() {
  const mode = useState<SidebarMode>('sidebarMode', () => {
    if (!import.meta.client) return 'full'
    try {
      return readSidebarMode(localStorage)
    } catch {
      return 'full'
    }
  })
  const rail = computed(() => mode.value === 'rail')

  function set(m: SidebarMode) {
    mode.value = m
    if (import.meta.client) {
      try {
        writeSidebarMode(localStorage, m)
      } catch {
        /* ignore */
      }
    }
  }

  return {
    mode,
    rail,
    collapse: () => set('rail'),
    expand: () => set('full'),
    toggle: () => set(rail.value ? 'full' : 'rail'),
  }
}
```

- [ ] **Step 6: Write `web/app/components/SidebarRail.vue`**

```vue
<script setup lang="ts">
import { railGroups } from '~/utils/sidebar'

/**
 * The sidebar as a rail: Launch, a search button that opens the full
 * sidebar on its filter, and every session as its agent's avatar with the
 * attention dot, the members of a run together under its name. The mark,
 * the pages and the expand button are the layout's header and footer.
 */
const props = defineProps<{ runId?: string; runName?: string }>()
const emit = defineEmits<{ search: [] }>()

const attention = useAttention()
const events = useEvents()
const launch = useLaunchModal()
const route = useRoute()
const runNames = useState<Record<string, string>>('crewRunNames', () => ({}))

const shown = computed(() => (props.runId ? attention.sessions.value.filter((s) => s.crew?.runId === props.runId) : attention.sessions.value))
const names = computed(() => (props.runId && props.runName ? { ...runNames.value, [props.runId]: props.runName } : runNames.value))
const groups = computed(() => railGroups(shown.value, names.value))
/** The amber dot follows the Events page's Badge route for needs_input, as in the full sidebar. */
const needsDot = computed(() => events.routes.value.needs_input.badge)

function active(id: string) {
  return route.path === `/sessions/${id}`
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col items-center gap-2" data-rail>
    <UTooltip text="Launch agent" :kbds="['N']" :content="{ side: 'right' }">
      <UButton icon="i-lucide-plus" size="sm" aria-label="Launch agent" @click="launch.show()" />
    </UTooltip>
    <UTooltip text="Filter sessions" :kbds="['/']" :content="{ side: 'right' }">
      <UButton icon="i-lucide-search" color="neutral" variant="ghost" size="sm" aria-label="Filter sessions" @click="emit('search')" />
    </UTooltip>
    <div class="flex min-h-0 w-full flex-1 flex-col items-center gap-1 overflow-y-auto" data-rail-sessions>
      <template v-for="(g, gi) in groups" :key="g.runId ?? `loose-${gi}`">
        <span v-if="g.label" class="w-full truncate px-0.5 text-center text-[9px] font-semibold uppercase tracking-wider text-muted" :title="g.label">{{ g.label }}</span>
        <UTooltip v-for="it in g.items" :key="it.id" :text="it.message ? `${it.name} · ${it.message}` : it.name" :content="{ side: 'right' }">
          <NuxtLink
            :to="`/sessions/${it.id}`"
            class="relative grid place-items-center rounded-md p-0.5 transition-colors"
            :class="active(it.id) ? 'bg-default ring-1 ring-default shadow-xs' : 'hover:bg-elevated/60'"
            :aria-label="it.name"
            :aria-current="active(it.id) ? 'page' : undefined"
            data-rail-session
          >
            <SessionAvatar :agent-id="it.agentId" :solid="it.dot === 'needs'" :dashed="it.dot === 'exited'" />
            <span v-if="it.dot === 'needs' && needsDot" class="absolute -right-0.5 -top-0.5 size-2 rounded-full bg-warning ring-2 ring-default" aria-hidden="true" />
            <span v-else-if="it.dot === 'running'" class="absolute -right-0.5 -top-0.5 size-2 rounded-full bg-success ring-2 ring-default" aria-hidden="true" />
          </NuxtLink>
        </UTooltip>
      </template>
    </div>
  </div>
</template>
```

- [ ] **Step 7: Rework the layout** (`web/app/layouts/default.vue`)

Script: replace `const list = useTemplateRef…` block's use and the shortcuts:

```ts
const list = useTemplateRef<{ focusFilter: () => void }>('list')

// The rail bound to the sidebar's collapsed model: Nuxt UI mirrors it in
// its own cookie; ours (localStorage) is read first and wins on load.
const railModel = computed({
  get: () => sidebar.rail.value,
  set: (collapsed: boolean) => (collapsed ? sidebar.collapse() : sidebar.expand()),
})

/** The filter box: on the rail, the full sidebar opens first. */
async function focusFilter() {
  if (sidebar.rail.value) {
    sidebar.expand()
    await nextTick()
  }
  list.value?.focusFilter()
}
```

In `defineShortcuts`, `'/': focusFilter` and `alt_s: { ...inTerminal, handler: focusFilter }` (the `meta_b` / `alt_b` rows stay: `sidebar.toggle()` now toggles rail and full).

Template: the sidebar element becomes

```vue
    <UDashboardSidebar
      v-model:collapsed="railModel"
      collapsible
      :resizable="!sidebar.rail.value"
      :min-size="14"
      :default-size="18"
      :max-size="26"
      :ui="{
        header: sidebar.rail.value ? 'lg:px-0 lg:justify-center' : undefined,
        body: sidebar.rail.value ? 'gap-0 py-2 lg:px-1' : 'gap-0 py-2',
        footer: sidebar.rail.value ? 'border-t border-default flex-col items-center gap-1 lg:px-1' : 'border-t border-default flex-col items-stretch gap-1',
      }"
    >
      <template #header="{ collapsed }">
        <div v-if="collapsed" class="grid w-full place-items-center">
          <img src="/brand/conductor-mark.svg" alt="" class="size-7 dark:hidden" />
          <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-7 hidden dark:block" />
        </div>
        <div v-else class="flex items-center gap-2 px-1 w-full min-w-0">
          <img src="/brand/conductor-mark.svg" alt="" class="size-7 dark:hidden" />
          <img src="/brand/conductor-mark-reversed.svg" alt="" class="size-7 hidden dark:block" />
          <span class="font-semibold truncate">Conductor</span>
          <div class="flex-1" />
          <UTooltip text="Collapse to the rail" :kbds="['meta', 'B']">
            <UButton icon="i-lucide-panel-left-close" color="neutral" variant="ghost" size="sm" aria-label="Collapse sidebar" class="hidden lg:inline-flex" @click="sidebar.collapse()" />
          </UTooltip>
        </div>
      </template>

      <template #default="{ collapsed }">
        <SidebarRail v-if="collapsed" :run-id="sidebarRun" :run-name="sidebarRunName" @search="focusFilter" />
        <SessionSidebar v-else ref="list" :run-id="sidebarRun" :run-name="sidebarRunName" />
      </template>

      <template #footer="{ collapsed }">
        <UNavigationMenu :items="nav" orientation="vertical" :collapsed="collapsed" tooltip class="w-full" />
        <div class="flex items-center px-1 pt-1" :class="collapsed ? 'flex-col gap-1' : 'justify-between'">
          <!-- the Alerts, Your name, Keyboard shortcuts, token, theme and Forget token buttons exactly as they are today -->
          <UTooltip v-if="collapsed" text="Expand sidebar" :kbds="['meta', 'B']" :content="{ side: 'right' }">
            <UButton icon="i-lucide-panel-left-open" color="neutral" variant="ghost" size="sm" aria-label="Expand sidebar" class="hidden lg:inline-flex" data-rail-expand @click="sidebar.expand()" />
          </UTooltip>
        </div>
      </template>
    </UDashboardSidebar>
```

The slot prop `collapsed` is what decides, not `sidebar.rail` directly: in the slideover (below `lg`) Nuxt UI passes `collapsed: false`, so narrow screens keep the full sidebar. The collapsed root keeps Nuxt UI's `min-w-16` (64 px): that is the rail's width; nothing else sets it.

Then delete `web/app/components/SidebarReveal.vue` and its uses listed under Files; `UDashboardNavbar` renders its own toggle on narrow screens (its `toggle` prop defaults to true), so nothing replaces it there. In `useShortcuts.ts` the Everywhere row becomes `{ keys: ['meta', 'B'], label: 'Collapse the sidebar to the rail, or expand it (Alt+B in a terminal)' }`.

- [ ] **Step 8: Typecheck and vitest**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: clean; no file imports `SidebarReveal` any more (`grep -rn SidebarReveal web/app` is empty).

- [ ] **Step 9: Headless check** — `scratchpad/pw/rail-check.js`, server on :8099 as in Task 2 (any data dir), one `shell` session launched first with `curl -s -X POST -H 'Authorization: Bearer dev-admin-token-change-me' -H 'Content-Type: application/json' -d '{"agentId":"shell"}' http://127.0.0.1:8099/api/sessions`:

```js
const { chromium } = require('playwright-core')
const base = 'http://127.0.0.1:8099'
;(async () => {
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  // Review Focus 4: a browser from before the rail, with the old hidden flag.
  await ctx.addInitScript(() => {
    localStorage.setItem('conductor.adminToken', 'dev-admin-token-change-me')
    if (!localStorage.getItem('conductor.sidebar.mode')) localStorage.setItem('conductor.sidebar.hidden', '1')
  })
  const page = await ctx.newPage()
  await page.goto(base + '/wall', { waitUntil: 'commit' })
  await page.waitForSelector('[data-rail]')
  const root = page.locator('[data-slot="root"][data-collapsed="true"]').first()
  console.log('rail width', (await root.boundingBox())?.width) // 64
  console.log('keys', await page.evaluate(() => [localStorage.getItem('conductor.sidebar.mode'), localStorage.getItem('conductor.sidebar.hidden')])) // ['rail', null]
  console.log('avatars', await page.locator('[data-rail-session]').count()) // ≥ 1
  console.log('nav tooltips', await page.locator('[data-slot="footer"] a[aria-label], [data-slot="footer"] a').count()) // the five pages as icons
  await page.locator('[data-rail-session]').first().click()
  await page.waitForURL(/\/sessions\//)
  console.log('opened', page.url())
  await page.keyboard.press('Control+b') // meta_b is Ctrl+B off a Mac
  await page.waitForSelector('[data-session-list]')
  console.log('mode after toggle', await page.evaluate(() => localStorage.getItem('conductor.sidebar.mode'))) // 'full'
  await page.reload({ waitUntil: 'commit' })
  await page.waitForSelector('[data-session-list]') // persisted
  await page.keyboard.press('Control+b')
  await page.waitForSelector('[data-rail]')
  await page.locator('[data-rail] [aria-label="Filter sessions"]').click()
  await page.waitForSelector('[data-session-list]')
  console.log('filter focused', await page.evaluate(() => document.activeElement?.getAttribute('placeholder'))) // 'Filter sessions, paths, people'
  await page.locator('[aria-label="Collapse sidebar"]').click()
  await page.waitForSelector('[data-rail-expand]')
  await page.locator('[data-rail-expand]').click()
  await page.waitForSelector('[data-session-list]')
  await page.setViewportSize({ width: 600, height: 900 })
  console.log('narrow: desktop sidebar hidden', await page.locator('[data-slot="root"]').first().isHidden()) // true: the slideover takes over
  await page.screenshot({ path: 'rail.png' })
  await browser.close()
})()
```

Run: `cd scratchpad/pw && node rail-check.js`
Expected: the printed values as noted; `rail.png` shows the mark on top, Launch and search under it, the avatars, the page icons and the expand button at the bottom. Then kill the server by pid.

- [ ] **Step 10: Commit**

```bash
git add web/app/utils/sidebar.ts web/app/utils/sidebar.test.ts web/app/composables/useSidebar.ts web/app/components/SidebarRail.vue web/app/layouts/default.vue web/app/composables/useShortcuts.ts web/app/pages web/app/components/CrewRunHeader.vue
git rm web/app/components/SidebarReveal.vue
git commit -m "web: the sidebar collapses to an icon rail instead of hiding"
```

---

### Task 6: Fullscreen everywhere

**Files:**
- Create: `web/app/components/FullscreenButton.vue`, `web/app/composables/useShortcuts.test.ts`
- Modify: `web/app/composables/useFullscreenToggle.ts` (whole file), `web/app/layouts/default.vue` (listen + shortcuts), `web/app/layouts/bare.vue` (same), `web/app/composables/useShortcuts.ts:228-264` (the `F` row), `web/app/pages/wall.vue:21,86-98,213-215`, `web/app/pages/carousel.vue:14,271-281,354-356`, `web/app/pages/index.vue:34-38`, `events.vue:86-88`, `agents.vue:136-139`, `crews/[[id]].vue:327-329`, `sessions/[id].vue:287-296`, `components/CrewRunHeader.vue:105-110`, `pages/join/[token].vue:200-211,263-283`

**Interfaces:**
- Consumes: Nuxt UI `defineShortcuts` (a plain key never fires while an input, textarea or contenteditable has focus unless the shortcut sets `usingInput: true`; `alt_*` chords are what a focused terminal hands back, see `ALT_PASSTHROUGH_CODES`, which already lists `KeyF`).
- Produces:
```ts
export function useFullscreenToggle(): {
  fullscreen: Ref<boolean>   // shared app state, follows the document's fullscreenchange
  toggle(): void             // document in or out of fullscreen
  listen(): void             // binds the state to the document for the calling component's life: each layout calls it once
  shortcuts(): void          // registers F and Alt+F for the calling layout, once
}
// <FullscreenButton /> — data-fullscreen; icon maximize / minimize; tooltip "Toggle fullscreen" with the F kbd
```

- [ ] **Step 1: Write the failing vitest** `web/app/composables/useShortcuts.test.ts` (the module's constants import nothing from Nuxt, so it loads under vitest's node environment):

```ts
import { describe, expect, it } from 'vitest'
import { CAROUSEL_SHORTCUTS, GLOBAL_SHORTCUTS, WALL_SHORTCUTS, type ShortcutGroup } from './useShortcuts'

const fRows = (g: ShortcutGroup) => g.rows.filter((r) => r.keys.length === 1 && r.keys[0] === 'F')

describe('the fullscreen shortcut', () => {
  it('is listed once, under Everywhere, and no longer by the wall or the carousel', () => {
    expect(fRows(GLOBAL_SHORTCUTS)).toHaveLength(1)
    expect(fRows(GLOBAL_SHORTCUTS)[0]!.label).toBe('Toggle fullscreen (Alt+F in a terminal)')
    expect(fRows(WALL_SHORTCUTS)).toHaveLength(0)
    expect(fRows(CAROUSEL_SHORTCUTS)).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `npm --prefix web test -- useShortcuts`
Expected: FAIL — `GLOBAL_SHORTCUTS` has no `F` row, the wall and carousel groups have one each.

- [ ] **Step 3: Move the row** in `web/app/composables/useShortcuts.ts`: add `{ keys: ['F'], label: 'Toggle fullscreen (Alt+F in a terminal)' }` to `GLOBAL_SHORTCUTS.rows` after the `G R` row; delete the `F` rows from `WALL_SHORTCUTS` and `CAROUSEL_SHORTCUTS`.

Run: `npm --prefix web test -- useShortcuts` → PASS.

- [ ] **Step 4: Rewrite `web/app/composables/useFullscreenToggle.ts`**

```ts
/**
 * Document fullscreen, shared by every page: one state that follows the
 * browser's fullscreenchange event, bound by the layout (`listen`), toggled
 * by any FullscreenButton or the F shortcut (`shortcuts`, registered by the
 * layout, so no page registers it twice). Plain F never fires while a text
 * field has focus, like every plain key; Alt+F works inside a terminal.
 */
export function useFullscreenToggle() {
  const fullscreen = useState<boolean>('fullscreen', () => false)

  function toggle() {
    if (!import.meta.client) return
    if (document.fullscreenElement) document.exitFullscreen()
    else document.documentElement.requestFullscreen?.()
  }

  function listen() {
    const onChange = () => (fullscreen.value = !!document.fullscreenElement)
    onMounted(() => {
      onChange()
      document.addEventListener('fullscreenchange', onChange)
    })
    onBeforeUnmount(() => document.removeEventListener('fullscreenchange', onChange))
  }

  function shortcuts() {
    defineShortcuts({
      f: toggle,
      alt_f: { usingInput: true, handler: toggle },
    })
  }

  return { fullscreen, toggle, listen, shortcuts }
}
```

- [ ] **Step 5: The button, the layouts, the pages**

`web/app/components/FullscreenButton.vue`:

```vue
<script setup lang="ts">
const fs = useFullscreenToggle()
</script>

<template>
  <UTooltip text="Toggle fullscreen" :kbds="['F']">
    <UButton :icon="fs.fullscreen.value ? 'i-lucide-minimize' : 'i-lucide-maximize'" color="neutral" variant="ghost" :aria-label="fs.fullscreen.value ? 'Exit fullscreen' : 'Enter fullscreen'" data-fullscreen @click="fs.toggle" />
  </UTooltip>
</template>
```

`web/app/layouts/default.vue` script, after `const launch = useLaunchModal()`:

```ts
const fs = useFullscreenToggle()
fs.listen()
fs.shortcuts()
```

`web/app/layouts/bare.vue` (the join page's layout) gains a script:

```vue
<script setup lang="ts">
const fs = useFullscreenToggle()
fs.listen()
fs.shortcuts()
</script>
```

Pages: `wall.vue` — delete `const fs = useFullscreenToggle()`, the `f:` and `alt_f:` rows of its `defineShortcuts`, and replace its tooltip+button in `#right` with `<FullscreenButton />`. `carousel.vue` — the same three edits. `index.vue`, `events.vue`, `agents.vue`, `crews/[[id]].vue`, `CrewRunHeader.vue`, `sessions/[id].vue` — add `<FullscreenButton />` as the last element of the navbar's `#right` slot (for `index.vue`, which has no `#right`, add one with just the button). `join/[token].vue` — in each of its two `<header>` elements, add `<FullscreenButton />` after the last button (the run header's refresh button at line 210; the session header's trailing controls at line 263-283).

- [ ] **Step 6: Typecheck**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: clean; `grep -rn "alt_f\|useFullscreenToggle()" web/app/pages` finds nothing (the pages only use the component).

- [ ] **Step 7: Headless check** — `scratchpad/pw/fullscreen-check.js`, server on :8099 with one `shell` session and one view link made with `curl -s -X POST -H 'Authorization: Bearer dev-admin-token-change-me' -H 'Content-Type: application/json' -d '{"role":"view"}' http://127.0.0.1:8099/api/sessions/<id>/links` (its `url` is the join page):

```js
const { chromium } = require('playwright-core')
const base = 'http://127.0.0.1:8099'
const [sessionId, joinUrl] = process.argv.slice(2)
;(async () => {
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  await ctx.addInitScript(() => localStorage.setItem('conductor.adminToken', 'dev-admin-token-change-me'))
  const page = await ctx.newPage()
  for (const path of ['/', '/wall', '/carousel', '/agents', '/crews', '/events', `/sessions/${sessionId}`]) {
    await page.goto(base + path, { waitUntil: 'commit' })
    await page.waitForSelector('[data-fullscreen]')
    console.log(path, 'buttons', await page.locator('[data-fullscreen]').count()) // exactly 1 each
  }
  await page.goto(joinUrl, { waitUntil: 'commit' })
  await page.waitForSelector('[data-fullscreen]')
  console.log('/join buttons', await page.locator('[data-fullscreen]').count()) // 1
  // Review Focus 5: F while typing is a letter.
  await page.goto(base + '/crews', { waitUntil: 'commit' })
  await page.getByRole('button', { name: 'New crew' }).click()
  const name = page.locator('[aria-label="Crew name"]')
  await name.fill('')
  await name.press('f')
  console.log('typed', await name.inputValue(), 'fullscreen', await page.evaluate(() => !!document.fullscreenElement)) // 'f' false
  await page.goto(base + '/wall', { waitUntil: 'commit' })
  await page.waitForSelector('[data-fullscreen]')
  await page.locator('[data-session-list]').first().click({ position: { x: 5, y: 5 } }).catch(() => {}) // move focus off any input
  await page.keyboard.press('f')
  await page.waitForTimeout(300)
  const fsAfter = await page.evaluate(() => !!document.fullscreenElement)
  console.log('after f on the page', fsAfter, '(headless Chromium may refuse fullscreen without a user gesture: then check the icon)', await page.locator('[data-fullscreen]').getAttribute('aria-label'))
  await page.screenshot({ path: 'fullscreen.png' })
  await browser.close()
})()
```

Run: `cd scratchpad/pw && node fullscreen-check.js <id> <joinUrl>`
Expected: every page prints `buttons 1`; `typed f fullscreen false`. The last line is informational: when headless Chromium honours the synthetic key the value is `true` and the label `Exit fullscreen`; if it refuses, note it as a limitation of the check and confirm the toggle by clicking the button in a real browser (see Verification). Kill the server by pid.

- [ ] **Step 8: Commit**

```bash
git add web/app/components/FullscreenButton.vue web/app/composables/useFullscreenToggle.ts web/app/composables/useShortcuts.ts web/app/composables/useShortcuts.test.ts web/app/layouts web/app/pages web/app/components/CrewRunHeader.vue
git commit -m "web: fullscreen toggle and the F key on every page"
```

---

### Task 7: Agent availability

Spec: `docs/features.md` § Round 3 → Decisions → "Agent availability" (added 2026-10-01): `GET /api/catalog` reports `available` per agent (whether `command[0]` resolves on the server through the same `exec.LookPath` check `POST /api/catalog/check` uses, evaluated per request with a 30 s cache keyed by the command, so a freshly installed CLI shows up without a restart) and `site` (the agent's website: a field on the built-ins, an optional validated `https` URL on saved agents, exposed in the add-agent form). The Agents page greys out an agent that is not installed, says "Not installed on <host>" (the host from `GET /api/integrations`) and links to its site; the Launch dialog's server tab lists only available agents and, when none is, says so with a link to the Agents page; the "My machine" tab keeps every agent (availability there is the host's). The crew editor's agent select marks unavailable agents the same way, and `POST /api/crews/{id}/launch` refuses a crew whose member agent is not installed with `400 invalid_crew` naming the member and the agent, checked at launch next to `CheckAgents`.

**Files:**
- Create: `internal/api/lookup.go`, `internal/api/lookup_test.go`, `web/app/utils/agents.ts`, `web/app/utils/agents.test.ts`, `web/app/composables/useServerHost.ts`
- Modify: `internal/catalog/catalog.go:19-36` (`Site`), `:78-88` (`maxSite`), `:149-200` (`validate`), `internal/catalog/catalog_test.go`, `internal/catalog/defaults.go` (sites), `internal/api/catalog.go:21-33` (`handleCatalog`), `:150-170` (`handleCheckCommand`), `internal/api/server.go` (`lookups` field, `New`), `internal/api/runs.go:65-78` (`checkLaunch`), `:103` and `:170-180` (its callers), `internal/api/api_test.go` (catalog tests), `internal/api/crews_test.go` (launch refusal), `web/app/composables/useSessions.ts:42-75` (`available`, `site`), `web/app/pages/agents.vue`, `web/app/components/AddAgentSlideover.vue` (the `site` field), `web/app/components/LaunchSessionModal.vue`, `web/app/components/CrewMembersTable.vue:31-40`, `web/app/components/CrewEditor.vue` (the `host` prop through to the table), `docs/protocol.md` (the catalog rows, in Task 8)

**Interfaces:**
- Consumes: `catalog.Agent`, `catalog.validate`, `Agent.Redacted`, `Catalog.List`/`Get`; `exec.LookPath`; `crew.ErrInvalid`, `checkLaunch`; test helpers `newTestEnv`, `e.save`, `agentBody`, `e.catalogAgent`, `e.sendCrew`, `e.crewBody`; `useSessions().integrations()` (returns `{ integrations, host, webhooks }`); plan 1's `utils/agentForm.ts` for the form's error rules (the `site` rule goes next to them).
- Produces:
```go
// internal/catalog/catalog.go
Agent.Site string `json:"site,omitempty"`   // https URL with a host, no user info, ≤ 200 bytes
// internal/api/lookup.go
type lookupCache struct{ /* mu, entries map[string]lookupEntry, look func(string) (string, error), now func() time.Time */ }
func newLookupCache() *lookupCache
func (c *lookupCache) found(program string) (path string, ok bool) // cached 30 s per program
const lookupTTL = 30 * time.Second
// internal/api/catalog.go
type catalogEntry struct { catalog.Agent; Available bool `json:"available"` }
// internal/api/runs.go
func checkLaunch(members []crew.Member, cat catalog.Catalog, installed func(program string) bool) error
```
```ts
// web/app/composables/useSessions.ts
AgentInfo.available?: boolean; AgentInfo.site?: string; AgentInput.site?: string
// web/app/utils/agents.ts
export function isAvailable(a: Pick<AgentInfo, 'available'>): boolean               // undefined (an older server) counts as available
export function notInstalled(host: string): string                                  // "Not installed on <host>" / "Not installed on the server"
export function serverAgents<T extends Pick<AgentInfo, 'available'>>(list: readonly T[]): T[]
export function agentItem(a: AgentInfo, host: string): { label: string; value: string; icon: string; disabled?: boolean } // marked, never disabled
export function siteError(site: string): string                                      // '' when empty or https://host…
// web/app/composables/useServerHost.ts
export function useServerHost(): { host: Ref<string>; load(): Promise<void> }
```

- [ ] **Step 1: Write the failing catalog tests** in `internal/catalog/catalog_test.go`:

```go
// site is an optional https URL with a host and no user info, at most 200 bytes.
func TestSiteValidation(t *testing.T) {
	base := Agent{ID: "x", Name: "X", Command: []string{"x"}}
	for _, ok := range []string{"", "https://example.com", "https://example.com/docs/cli?x=1"} {
		a := base
		a.Site = ok
		if err := validate(a); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://example.com", "example.com", "https://", "https://user:pw@example.com", "https://example.com/" + strings.Repeat("a", 200), "javascript:alert(1)", "https://exa mple.com"} {
		a := base
		a.Site = bad
		if err := validate(a); err == nil || !strings.Contains(err.Error(), "site") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

// Every built-in but the shell names its website, and the sites pass validation.
func TestDefaultsHaveSites(t *testing.T) {
	for _, a := range defaults() {
		if a.ID == "shell" {
			if a.Site != "" {
				t.Fatalf("shell has a site: %q", a.Site)
			}
			continue
		}
		if a.Site == "" {
			t.Errorf("%s: no site", a.ID)
		}
		if err := validate(a); err != nil {
			t.Errorf("%s: %v", a.ID, err)
		}
	}
}
```

- [ ] **Step 2: Run them, see them fail, implement the field**

Run: `go test ./internal/catalog -run 'TestSiteValidation|TestDefaultsHaveSites'` → FAIL to compile, `a.Site undefined`.

`internal/catalog/catalog.go`: the field after `Icon`:

```go
	// Site is the agent's website, shown on the Agents page beside an agent
	// that is not installed: an https URL, or empty.
	Site string `json:"site,omitempty"`
```

the bound `maxSite = 200 // bytes in site` in the limits block, and in `validate` after the description check:

```go
	if a.Site != "" {
		if err := validateSite(a.Site); err != nil {
			return fmt.Errorf("agent %s: site: %w", a.ID, err)
		}
	}
```

```go
// validateSite accepts an https URL with a host and no user info, so that
// the Agents page can link to it as it is.
func validateSite(site string) error {
	if len(site) > maxSite {
		return fmt.Errorf("must be at most %d bytes", maxSite)
	}
	u, err := url.Parse(site)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(site, " \t\r\n") {
		return errors.New("must be an https:// URL with a host")
	}
	return nil
}
```

(`catalog.go` needs `"errors"` and `"net/url"` in its imports; `clone` and `Redacted` need no change, `Site` is a string.) `internal/catalog/defaults.go`: `Site` on each built-in, from the agents' own sites (the adapter matrix names the commands, not the sites; verify these in a browser, listed under Open verification in Task 8):

| ID | `Site` |
|---|---|
| `claude` | `https://claude.com/claude-code` |
| `codex` | `https://developers.openai.com/codex/cli` |
| `agy` | `https://antigravity.google` |
| `copilot` | `https://github.com/features/copilot/cli` |
| `cursor` | `https://cursor.com/cli` |
| `opencode` | `https://opencode.ai` |
| `pi` | `https://github.com/badlogic/pi-mono` |
| `omp` | `https://github.com/can1357/oh-my-pi` |
| `aider` | `https://aider.chat` |
| `goose` | `https://block.github.io/goose/` |
| `amp` | `https://ampcode.com` |
| `dsh` | `https://github.com/deepseek-ai/dsh` |
| `shell` | (none) |

Run: `go test -race ./internal/catalog` → PASS.

- [ ] **Step 3: Write the failing lookup-cache test** `internal/api/lookup_test.go`:

```go
package api

import (
	"errors"
	"testing"
	"time"
)

// The cache asks the system once per program and again after 30 s, so a CLI
// installed while the server runs shows up without a restart.
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
	now = now.Add(lookupTTL + time.Second)
	if _, ok := c.found("ghost"); !ok || asked["ghost"] != 2 {
		t.Fatalf("after the TTL ghost should be looked up again: %v", asked)
	}
}
```

- [ ] **Step 4: Run it, see it fail, implement `internal/api/lookup.go`**

Run: `go test ./internal/api -run TestLookupCacheExpires` → FAIL to compile.

```go
package api

import (
	"os/exec"
	"sync"
	"time"
)

// lookupTTL is how long an answer about a program is kept: a CLI installed
// meanwhile is seen on the next request after it.
const lookupTTL = 30 * time.Second

type lookupEntry struct {
	path string
	ok   bool
	at   time.Time
}

// lookupCache remembers whether programs resolve on this server, the way
// exec resolves them when a session launches (exec.LookPath), for lookupTTL.
// GET /api/catalog marks every agent with it and a crew launch refuses a
// member whose program it cannot find. Nothing is run.
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
	defer c.mu.Unlock()
	now := c.now()
	if e, ok := c.entries[program]; ok && now.Sub(e.at) < lookupTTL {
		return e.path, e.ok
	}
	path, err := c.look(program)
	e := lookupEntry{path: path, ok: err == nil, at: now}
	c.entries[program] = e
	return e.path, e.ok
}
```

`internal/api/server.go`: the field `lookups *lookupCache` on `Server` (after `fileDeny`) and `lookups: newLookupCache(),` in `New`'s literal.

Run: `go test ./internal/api -run TestLookupCacheExpires` → PASS.

- [ ] **Step 5: Write the failing API tests**

In `internal/api/api_test.go`, after `TestCatalogCheckCommand`:

```go
// GET /api/catalog says which agents are installed on this server: cat is,
// an agent whose program does not exist is not; the check that POST
// /api/catalog/check runs. site travels with the agent.
func TestCatalogReportsAvailability(t *testing.T) {
	e := newTestEnv(t, nil)
	ghost := agentBody("ghost")
	ghost["command"] = []string{"definitely-not-a-real-binary-xyz"}
	ghost["site"] = "https://example.com/ghost"
	e.save(ghost)
	if a := e.catalogAgent("cat"); a["available"] != true {
		t.Fatalf("cat: %v", a)
	}
	if a := e.catalogAgent("ghost"); a["available"] != false || a["site"] != "https://example.com/ghost" {
		t.Fatalf("ghost: %v", a)
	}
	bad := agentBody("badsite")
	bad["site"] = "http://example.com"
	if resp, out := e.do("POST", "/api/catalog", adminToken, bad); resp.StatusCode != http.StatusBadRequest || errorCode(out) != "invalid_agent" {
		t.Fatalf("http site: %d %v", resp.StatusCode, out)
	}
}
```

In `internal/api/crews_test.go`:

```go
// A crew with a member whose agent is not installed on the server is refused
// at launch, before any session starts, naming the member and the agent.
func TestLaunchRefusesAnAgentNotInstalled(t *testing.T) {
	e := newTestEnv(t, nil)
	ghost := agentBody("ghost")
	ghost["command"] = []string{"definitely-not-a-real-binary-xyz"}
	e.save(ghost)
	body := e.crewBody("Ghost crew")
	body["isolation"] = "none"
	crewMember(body, 1)["agentId"] = "ghost"
	c := e.sendCrew("POST", "/api/crews", body, http.StatusCreated)
	resp, out := e.do("POST", "/api/crews/"+c["id"].(string)+"/launch", adminToken, nil)
	msg, _ := out["error"].(map[string]any)["message"].(string)
	if resp.StatusCode != http.StatusBadRequest || errorCode(out) != "invalid_crew" || !strings.Contains(msg, `member "tests"`) || !strings.Contains(msg, `"ghost"`) || !strings.Contains(msg, "not installed") {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	if _, out := e.do("GET", "/api/sessions", adminToken, nil); len(out["sessions"].([]any)) != 0 {
		t.Fatalf("a session was started: %v", out)
	}
}
```

- [ ] **Step 6: Run them to see them fail, then implement**

Run: `go test ./internal/api -run 'TestCatalogReportsAvailability|TestLaunchRefusesAnAgentNotInstalled'` → FAIL (`available` missing; the launch answers 201 or a `start_failed`).

`internal/api/catalog.go`, `handleCatalog`:

```go
// catalogEntry is an agent as GET /api/catalog lists it: redacted, with
// whether its program resolves on this server right now.
type catalogEntry struct {
	catalog.Agent
	Available bool `json:"available"`
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	s.catalogMu.Lock()
	cat, hidden := s.catalog, uniqueIDs(s.overlay.Hidden)
	s.catalogMu.Unlock()
	list := cat.List()
	out := make([]catalogEntry, 0, len(list))
	for _, a := range list {
		_, ok := s.lookups.found(a.Command[0])
		out = append(out, catalogEntry{Agent: a.Redacted(), Available: ok})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out, "hidden": hidden})
}
```

`handleCheckCommand` keeps asking the system directly (the add-agent form wants the answer now, not a 30 s old one) but through the cache's `look`, so both are one check:

```go
	path, err := s.lookups.look(req.Command[0])
```

`internal/api/runs.go`, `checkLaunch`:

```go
// checkLaunch reports the first member whose agent the catalog lacks, whose
// program is not installed on this server (installed, the lookup cache), or
// who is given arguments its agent does not take, so that a launch refuses
// it before any session starts. The error matches crew.ErrInvalid.
func checkLaunch(members []crew.Member, cat catalog.Catalog, installed func(program string) bool) error {
	if err := (crew.Crew{Members: members}).CheckAgents(cat); err != nil {
		return err
	}
	for _, m := range members {
		a, _ := cat.Get(m.AgentID)
		if !installed(a.Command[0]) {
			return fmt.Errorf("%w: member %q: agent %q is not installed on the server (%s was not found)", crew.ErrInvalid, m.Name, m.AgentID, a.Command[0])
		}
		if len(m.Args) > 0 && !a.AllowArgs {
			return fmt.Errorf("%w: member %q: agent %q takes no extra arguments", crew.ErrInvalid, m.Name, m.AgentID)
		}
	}
	return nil
}
```

and a server helper both callers pass:

```go
// installed reports whether a program resolves on this server (cached).
func (s *Server) installed(program string) bool {
	_, ok := s.lookups.found(program)
	return ok
}
```

so `handleLaunchCrew` and `handleAddRunMember` call `checkLaunch(c.Members, s.Catalog(), s.installed)` / `checkLaunch([]crew.Member{m}, s.Catalog(), s.installed)`.

Run: `go test -race -count=1 ./internal/api ./internal/catalog && make lint` → PASS. (`TestCreateSessionWithAnAdapterAndAMissingCommand` and the other catalog tests keep passing: launching a session is unchanged; only crews are refused.)

- [ ] **Step 7: Commit the server side**

```bash
git add internal/catalog internal/api/lookup.go internal/api/lookup_test.go internal/api/catalog.go internal/api/server.go internal/api/runs.go internal/api/api_test.go internal/api/crews_test.go
git commit -m "catalog: available and site per agent; a crew launch refuses an agent not installed"
```

- [ ] **Step 8: Write the failing vitest** `web/app/utils/agents.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { AgentInfo } from '~/composables/useSessions'
import { agentItem, isAvailable, notInstalled, serverAgents, siteError } from './agents'

const agent = (over: Partial<AgentInfo>): AgentInfo => ({ id: 'a', name: 'Agent', command: ['a'], allowArgs: true, ...over })

describe('agent availability', () => {
  it('treats a missing flag (an older server) as available', () => {
    expect(isAvailable({ available: undefined })).toBe(true)
    expect(isAvailable({ available: true })).toBe(true)
    expect(isAvailable({ available: false })).toBe(false)
  })

  it('keeps only the installed agents for the server tab', () => {
    expect(serverAgents([agent({ id: 'x', available: true }), agent({ id: 'y', available: false }), agent({ id: 'z' })]).map((a) => a.id)).toEqual(['x', 'z'])
  })

  it('names the host, or the server when it is unknown', () => {
    expect(notInstalled('build-1')).toBe('Not installed on build-1')
    expect(notInstalled('')).toBe('Not installed on the server')
  })

  it('marks an unavailable agent in a select without disabling it', () => {
    expect(agentItem(agent({ id: 'claude', name: 'Claude Code', icon: 'i-lucide-sparkles', available: false }), 'build-1')).toEqual({ label: 'Claude Code · not installed on build-1', value: 'claude', icon: 'i-lucide-circle-off' })
    expect(agentItem(agent({ id: 'sh', name: 'Shell', available: true }), 'build-1')).toEqual({ label: 'Shell', value: 'sh', icon: 'i-lucide-terminal' })
  })

  it('accepts an empty or https site and refuses the rest', () => {
    expect(siteError('')).toBe('')
    expect(siteError('https://example.com/x')).toBe('')
    expect(siteError('http://example.com')).toBe('An https:// address, or nothing')
    expect(siteError('example.com')).toBe('An https:// address, or nothing')
  })
})
```

- [ ] **Step 9: Run it, see it fail, implement `web/app/utils/agents.ts`**

Run: `npm --prefix web test -- agents.test` → FAIL, module missing.

```ts
import type { AgentInfo } from '~/composables/useSessions'

/** Whether the server can launch the agent now. A server that does not say (older) is taken to mean yes. */
export function isAvailable(a: Pick<AgentInfo, 'available'>): boolean {
  return a.available !== false
}

/** The note on an agent whose program the server did not find; host is the server's host name, or empty. */
export function notInstalled(host: string): string {
  return `Not installed on ${host || 'the server'}`
}

/** The agents the Launch dialog's server tab offers. */
export function serverAgents<T extends Pick<AgentInfo, 'available'>>(list: readonly T[]): T[] {
  return list.filter(isAvailable)
}

/** A select item for an agent, marked when it is not installed; never disabled, since a crew may be edited for another server. */
export function agentItem(a: AgentInfo, host: string): { label: string; value: string; icon: string } {
  if (isAvailable(a)) return { label: a.name, value: a.id, icon: a.icon || 'i-lucide-terminal' }
  return { label: `${a.name} · ${notInstalled(host).toLowerCase()}`, value: a.id, icon: 'i-lucide-circle-off' }
}

/** The add-agent form's rule for the site field, the server's rule in words. */
export function siteError(site: string): string {
  const s = site.trim()
  if (!s) return ''
  try {
    const u = new URL(s)
    if (u.protocol === 'https:' && u.hostname && !u.username && !u.password) return ''
  } catch {
    /* not a URL */
  }
  return 'An https:// address, or nothing'
}
```

Run: `npm --prefix web test -- agents.test` → PASS.

- [ ] **Step 10: The client types, the host composable and the three surfaces**

`web/app/composables/useSessions.ts` — on `AgentInfo`: `/** Whether command[0] resolves on the server (GET /api/catalog; cached 30 s there). Missing on an older server. */ available?: boolean` and `/** The agent's website, when known. */ site?: string`; on `AgentInput`: `site?: string`.

`web/app/composables/useServerHost.ts`:

```ts
/** The server's host name from GET /api/integrations, read once per app for the "Not installed on <host>" notes; empty until known or when the server cannot tell. */
export function useServerHost() {
  const host = useState<string>('serverHost', () => '')
  const asked = useState<boolean>('serverHostAsked', () => false)
  const api = useSessions()
  async function load() {
    if (asked.value) return
    asked.value = true
    try {
      host.value = (await api.integrations()).host
    } catch {
      asked.value = false
    }
  }
  return { host, load }
}
```

`web/app/pages/agents.vue` — script: `import { isAvailable, notInstalled } from '~/utils/agents'`, `const serverHost = useServerHost()`, and `serverHost.load()` inside `refresh()` after the catalog read. Template, the card:

```vue
        <UCard v-for="a in agents" :key="a.id" :class="!isAvailable(a) && 'opacity-60'" :data-agent="a.id" :data-available="isAvailable(a)">
          <div class="flex items-start gap-3">
            <UIcon :name="a.icon || 'i-lucide-terminal'" class="size-6 text-primary flex-none mt-0.5" />
            <div class="min-w-0 flex-1">
              <div class="font-medium">{{ a.name }} <span class="text-xs text-muted font-mono">{{ a.id }}</span></div>
              <p v-if="a.description" class="text-sm text-muted">{{ a.description }}</p>
              <code class="block text-xs mt-2 truncate" :title="joinArgv(a.command)">{{ joinArgv(a.command) }}</code>
              <div class="mt-2 flex flex-wrap gap-2">
                <UBadge v-if="!isAvailable(a)" :label="notInstalled(serverHost.host.value)" color="warning" variant="subtle" size="sm" data-not-installed />
                <UBadge :label="signalBadge(a).label" :title="signalBadge(a).title" color="neutral" variant="subtle" size="sm" />
                <UBadge v-if="a.allowArgs" label="accepts args" color="neutral" variant="subtle" size="sm" />
                <UBadge v-if="a.cwd" :label="a.cwd" color="neutral" variant="subtle" size="sm" />
              </div>
              <div class="mt-3 -mb-1 flex justify-end gap-1">
                <UButton v-if="a.site" label="Website" icon="i-lucide-external-link" size="xs" color="neutral" variant="ghost" :to="a.site" target="_blank" rel="noopener noreferrer" :aria-label="`${a.name} website`" />
                <UButton label="Edit" icon="i-lucide-pencil" size="xs" color="neutral" variant="ghost" :aria-label="`Edit ${a.name}`" @click="editAgent(a)" />
                <UButton label="Hide" icon="i-lucide-eye-off" size="xs" color="neutral" variant="ghost" :aria-label="`Hide ${a.name}`" @click="askHide(a)" />
              </div>
            </div>
          </div>
        </UCard>
```

`web/app/components/AddAgentSlideover.vue` — `site: ''` on `form`, `form.site = a?.site ?? ''` in the reset, `site: form.site.trim() || undefined` in `payload()`, the rule `if (siteError(form.site)) e.site = siteError(form.site)` with `'site'` added to `Field` (where plan 1 moved the rules, in `utils/agentForm.ts`, the same line goes there), and the field after Description:

```vue
        <UFormField label="Website" name="site" hint="optional, https" :error="shown.site">
          <UInput v-model="form.site" type="url" placeholder="https://" autocapitalize="off" spellcheck="false" class="w-full" />
        </UFormField>
```

`web/app/components/LaunchSessionModal.vue` — script: `import { serverAgents } from '~/utils/agents'`; `const offered = computed(() => (state.runsOn === 'server' ? serverAgents(agents.value) : agents.value))`; the default pick becomes `if (!state.agentId && offered.value[0]) state.agentId = offered.value[0].id` and a watch keeps it valid: `watch(offered, (list) => { if (!list.some((a) => a.id === state.agentId)) state.agentId = list[0]?.id ?? '' })`. Template: `v-for="a in offered"`, and the empty line becomes:

```vue
          <p v-if="!offered.length && !loading" class="col-span-full text-sm text-muted" data-none-available>
            <template v-if="state.runsOn === 'server' && agents.length">No agent in the catalog is installed on this server. <NuxtLink to="/agents" class="underline" @click="open = false">See the Agents page</NuxtLink> for what is missing, or run one on your machine.</template>
            <template v-else>No agents in the catalog.</template>
          </p>
```

`web/app/components/CrewMembersTable.vue` — a `host?: string` prop (default `''`), `import { agentItem } from '~/utils/agents'`, and `const agentItems = computed(() => props.agents.map((a) => agentItem(a, props.host)))`. `CrewEditor.vue` passes it: `const serverHost = useServerHost()`, `serverHost.load()` at setup, `<CrewMembersTable … :host="serverHost.host.value" />`.

- [ ] **Step 11: Typecheck, vitest, headless check**

Run: `npm --prefix web run typecheck && npm --prefix web test` → clean.

Headless (`scratchpad/pw/availability-check.js`): seed a saved agent whose program does not exist, `curl -s -X POST -H 'Authorization: Bearer dev-admin-token-change-me' -H 'Content-Type: application/json' -d '{"id":"ghost","name":"Ghost CLI","command":["definitely-not-a-real-binary-xyz"],"allowArgs":true,"site":"https://example.com/ghost"}' http://127.0.0.1:8099/api/catalog`, then:

```js
  await page.goto(base + '/agents', { waitUntil: 'commit' })
  await page.waitForSelector('[data-agent="ghost"]')
  console.log('ghost greyed', await page.locator('[data-agent="ghost"]').getAttribute('data-available')) // 'false'
  console.log('note', await page.locator('[data-agent="ghost"] [data-not-installed]').innerText()) // Not installed on <hostname>
  console.log('site', await page.locator('[data-agent="ghost"] a[aria-label="Ghost CLI website"]').getAttribute('href')) // https://example.com/ghost
  console.log('shell available', await page.locator('[data-agent="shell"]').getAttribute('data-available')) // 'true'
  await page.keyboard.press('n')
  await page.waitForSelector('[role="radiogroup"][aria-label="Agent"]')
  console.log('server tab', await page.locator('[role="radiogroup"][aria-label="Agent"] [role="radio"]').allInnerTexts()) // no Ghost CLI
  await page.getByRole('button', { name: 'My machine' }).click()
  console.log('my machine tab', await page.locator('[role="radiogroup"][aria-label="Agent"] [role="radio"]').allInnerTexts()) // Ghost CLI listed
```

Then `DELETE /api/catalog/shell` and `example-tool` (hide them) and reopen the dialog: `[data-none-available]` shows the Agents-page link; restore them afterwards (`POST /api/catalog/{id}/unhide`). On `/crews`, a new crew's agent select lists `Ghost CLI · not installed on <host>`. Delete `ghost` (`DELETE /api/catalog/ghost`) and kill the server by pid.

- [ ] **Step 12: Commit**

```bash
git add web/app/utils/agents.ts web/app/utils/agents.test.ts web/app/composables/useServerHost.ts web/app/composables/useSessions.ts web/app/pages/agents.vue web/app/components/AddAgentSlideover.vue web/app/components/LaunchSessionModal.vue web/app/components/CrewMembersTable.vue web/app/components/CrewEditor.vue
git commit -m "web: agents not installed on the server are greyed, filtered from a server launch and marked in crews"
```

---

### Task 8: Docs

**Files:**
- Modify: `docs/protocol.md:384-419` (the HTTP API table), `README.md:122-131` (Sidebar and keyboard shortcuts), `:338-443` (Crews), `:444-466` (Configuration), a new "Shell completion" section before Configuration, `docs/features.md:15-22` (Delivered) and `:269-272` (Open verification, round 3)

**Interfaces:** none produced; every row states what Tasks 1, 3 and 7 implemented, in the table's own style.

- [ ] **Step 1: `docs/protocol.md`** — three new rows after `POST /api/catalog/check`, and two edits:

```
| `GET /api/paths` | admin | `prefix` (≤ 4096 bytes) and `limit` (1–50, default 50) in the query; reply `{dir, entries, truncated}`: `dir` is the longest leading part of `prefix` that is an existing directory under an allowed root (symbolic links resolved, as a session's `cwd` is checked; the server's default working directory for an empty `prefix`), `entries` its child directories whose names start with the path element typed after `dir` (none when `prefix` names a directory), in name order, hidden ones only when that element starts with a dot, a symbolic link only when it leads under an allowed root, each `{name, path, git: {repo, commits}}` (`repo`: in a git working tree; `commits`: its `HEAD` is a commit; a child with no `.git` of its own carries its parent's marks; neither without `git` on the server), `truncated` when more than `limit` fit; `400 invalid_cwd` when no part of `prefix` is such a directory, `400 invalid_request` for a bad `prefix` or `limit`; `500 list_failed` when the directory cannot be read |
| `GET /api/git/check` | admin | `cwd` in the query (the server's default working directory when empty); reply `{inRepo, toplevel?, hasCommit, message}` by the rules a launch with `isolation: worktree` applies (`git -C <cwd> rev-parse`, under the allowed roots): `inRepo` when `cwd` is in a git working tree, `toplevel` its top, `hasCommit` when `HEAD` is a commit, `message` the words the launch's refusal would use, or that worktrees can be made, or that `git` is not installed (then `inRepo:false`); a preview: the launch's `409 not_a_repo` stays the authority; `400 invalid_cwd` as for a session; `500 git_failed` when git cannot be run |
| `POST /api/crews/examples` | admin | seed the example crews (`example-todo-app`, `example-test-fixer`, `example-docs-writer`, `example-dependency-upgrade`), as `conductor serve --examples` does: each is saved unless a crew with its `id` exists, which is left alone whatever it holds; reply `{added, skipped}`, the ids each way; their `cwd` is the server's default working directory, their agents the `claude` and `codex` built-ins, not checked against the catalog (the launch checks); `503 store_unavailable` without a data directory; `500 store_failed` when one cannot be saved (the ones before it stay) |
```

The `GET /api/catalog` row gains: "each agent carries `available`, whether `command[0]` resolves on the server (the check of `POST /api/catalog/check`, cached 30 s per program), and `site` when the agent has a website (an `https` URL; a built-in's, or the saved agent's)". The `POST /api/catalog` row gains: "`site`, when present, must be an `https://` URL with a host and no user info, at most 200 bytes". The `POST /api/crews/{id}/launch` row's `400 invalid_crew` list gains "a member whose agent's program is not installed on the server (`command[0]` does not resolve), named with the member". The `POST /api/runs/{run}/members` row gains the same.

- [ ] **Step 2: `README.md`**

Replace the first paragraph of "Sidebar and keyboard shortcuts" with:

```
The sidebar collapses to an icon rail with the panel button in its header or
**Ctrl+B** (**⌘B** on a Mac). The rail keeps everything: the mark, a Launch
button, a search button that opens the full sidebar on its filter, the pages
as icons with tooltips, and every session as its agent's initials with the
amber dot when it needs you, the members of a running crew together under its
name. Click one to open it; the panel button at the bottom of the rail brings
the full sidebar back. The choice is remembered per browser. On a phone the
sidebar is a drawer. **F** toggles fullscreen on every page (**Alt+F** in a
terminal); every page header has the button, and the key is ignored while you
type in a field. Press **?** (or use **Shortcuts** in the sidebar) for the
list of shortcuts on the current screen.
```

keeping the rest of that section's sentences (plain keys, Alt, the display name) as they are.

In "Crews", after the paragraph on `$GOAL`, add:

```
The working-directory field completes as you type: the server lists the
directories under its allowed roots, at most 50 at a time (hidden ones once
you type the dot), marking the ones that are git repositories with a commit,
which a crew with worktrees needs. The editor says under the field whether
the crew could launch there with worktrees; the launch itself still decides
(`409 not_a_repo`). The Launch dialog's working directory completes the same
way.

**Example crews.** `conductor serve --examples` (or `CONDUCTOR_EXAMPLES=1`)
adds four example crews the first time: `example-todo-app` (a lead that
plans, two builders after it, a tester after them), `example-test-fixer`,
`example-docs-writer` and `example-dependency-upgrade`, each using Claude
Code and Codex, the server's default working directory and a worktree per
member. They are ordinary crews once saved: edit or delete them freely. A
crew whose id exists is never touched, so edits survive the flag, and a
deleted example comes back on the next `--examples`. The empty **Crews** page
offers **Load the examples**, which does the same (`POST /api/crews/examples`).

**Installed agents.** The Agents page says which agents are installed on the
server (the program of their command resolves there, checked on every visit
and cached for 30 seconds) and links to the website of one that is not; the
Launch dialog's **Server** tab offers only the installed ones (**My machine**
offers them all: what is installed there is your machine's business), and a
crew whose member's agent is not installed is refused at launch.
```

A new section before "Configuration":

```
## Shell completion

`conductor completion zsh` or `conductor completion bash` prints a completion
script: subcommands, flags and their values, and for `conductor up` the crew
ids, read from the server as you type through `conductor crews --ids` (which
prints ids only and stays silent when `CONDUCTOR_SERVER` cannot be reached or
`CONDUCTOR_ADMIN_TOKEN` is not set). Load it with
`source <(conductor completion zsh)` in `~/.zshrc`, or let
`conductor completion install` append that line, marked, to `~/.zshrc` or
`~/.bashrc` (the shell from `$SHELL`, or `--shell`; the file from `--rc`); run
again it changes nothing, and it refuses a file that is not yours.
```

In the Configuration table, after the `webhooks` row:

```
| — | `CONDUCTOR_EXAMPLES` | off | `1` seeds the example crews once at startup, as `conductor serve --examples` does; not a config-file key |
```

- [ ] **Step 3: `docs/features.md`** — a new "Delivered" entry after the 2026-09-30 one:

```
## Delivered (2026-10-01)

- **Round 3**: the sidebar rail, example crews, the working-directory picker
  with the git check, agent availability, shell completion and fullscreen on
  every page. See the README (Sidebar and keyboard shortcuts, Crews, Shell
  completion) and `docs/protocol.md` (`GET /api/paths`, `GET /api/git/check`,
  `POST /api/crews/examples`, `available` and `site` on `GET /api/catalog`).
  The round 3 decisions below describe what exists.
```

and under "Open verification (round 3)" add:

```
- The built-in agents' `site` URLs (`internal/catalog/defaults.go`) in a browser.
- The picker, the rail and the Launch dialog's server tab on a real phone (the
  slideover) and at 1024 px; entering fullscreen with the `F` key in a real
  browser (headless Chromium may not honour a synthetic key for it).
```

- [ ] **Step 4: Check the docs and commit**

Run: `python3 scripts/brand_assets.py --check && make lint` (nothing in Go changed; the brand check keeps the README's asset references honest).

```bash
git add docs/protocol.md README.md docs/features.md
git commit -m "docs: round 3 routes, rail, examples, availability and completion"
```

---

## Verification

1. The full gate: `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test`, `make web-build`, `make build-go`, `python3 scripts/brand_assets.py --check`. Report any check that could not run (no zsh, no git, no Chromium) by name.
2. `bin/conductor serve --config conductor.example.json --examples` on a fresh data directory logs the four ids as added; a second start logs them as skipped; the Crews page lists `Example: todo app` with lead, core, cli, tester and their start conditions; editing its goal and restarting with `--examples` keeps the edit.
3. In the crew editor, typing the repository's path into the working directory lists its directories with `git` marks; picking one re-lists its children; a directory with no commit shows the warning line and the Launch tooltip repeats it; the launch of such a crew with worktrees answers `409 not_a_repo` (the button was not disabled).
4. `source <(bin/conductor completion zsh)` in an interactive zsh, then `conductor up <TAB>` with `CONDUCTOR_ADMIN_TOKEN` set lists the crew ids and, with the token unset, lists nothing and prints nothing; the same in bash. `bin/conductor completion install` on this box appends the one line and reports `nothing to change` on a second run.
5. In a browser: **Ctrl+B** collapses the sidebar to the rail on `/wall`; the rail shows the mark, Launch, search, the sessions' initials with the amber dot on one flagged `needs_input` (through `POST /api/sessions/{id}/attention`), the five page icons with tooltips and the expand button; reload keeps the rail; a 600 px wide window shows the drawer, not the rail.
6. **F** on `/events` enters fullscreen and again leaves it; typing `f` into the session filter or the crew name field inserts the letter; `/join/<token>` has the button and the key.
7. On the Agents page a saved agent with a program that does not exist is greyed with "Not installed on <host>" and a Website link; the Launch dialog's Server tab does not list it while My machine does; a crew with it as a member is refused at launch with the member named.
