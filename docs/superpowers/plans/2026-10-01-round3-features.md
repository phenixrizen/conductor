# Round 3 Features Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Revised 2026-10-01 against `31e86b3`** (plan 1 of round 3, all ten tasks, landed), **then re-checked against `55b9198`** (the three fix commits of plan 1's branch review, which landed meanwhile: `FileOwner` exported, a paging comment in `runCrews`, README and `config.go` lines moved). The Go and web code of Tasks 1–7 was taken from this file as written and applied to a scratch worktree of `55b9198`: gofmt clean, `go build ./... && go vet ./...`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck` and `npm --prefix web test` (183 tests) pass, and the five headless scripts, also taken from this file, pass against a server built from it. What changed, per task:
> - **Task 1:** `toplevel` keeps plan 1's `classifyRevParse` (the shared `rev-parse` path and its messages); `gitInit` makes its directory; the listing reads at most 2000 entries in chunks of 256, asks git at most once per listed entry (50) one call at a time, and logs a probed path at debug only; a lone `.` after the last separator now lists hidden directories (found by the headless check); two new tests pin the scan bound and the git-call bound; line references refreshed.
> - **Task 2:** `DirInput` is Nuxt UI's `UInputMenu` in `autocomplete` mode (the hand-rolled listbox is gone, and `moveActive`/`pickEntry` with it); a click pick takes the focus back to the field; the headless script asserts and makes a grandchild directory so the re-list after a pick is seen.
> - **Task 3:** `Store.Seed` is written on plan 1's per-crew store: it skips any id with a file in `crews/` (`taken`), usable or not, and commits the rest; the tests use `Get`'s error (`ErrNotFound`); a new test pins that an unreadable file is never overwritten; the tester starts `after cli` and its prompt says to wait for core's branch and merge both; the API test uses `e.crew`/`e.crewIDs`, saves the edit with the test catalog's agents and the 503 table gains the route; the Load-the-examples button works with the page's paging and `total`.
> - **Task 4:** `crew.ValidID` is exported from the store's `idPattern` (no second pattern); `otherOwner` and its test call `FileOwner` (the fix wave's name for `fileOwner`), and the fix wave's comment on offset paging moves with the loop into `crewList`; `conductor crews --ids` reuses one paging helper (`crewList`, extracted from `runCrews`, pages of 100, 4 MiB a page) under a 2 s budget for the whole listing; `CheckOwner` is in `home.go` and shares `ownedBy`'s rule (`otherOwner`); a missing rc file is made only in a directory of the caller's own; the bash script reads the ids in a `while read` loop with a prefix test (never `compgen -W` on server data) and zsh hands them to `compadd` as array elements; new tests prove a hostile id is never run and hold the completion table to every command's `-h`; `fmt` and gofmt fixed.
> - **Task 5:** localStorage is the sidebar's one source (`UDashboardGroup :persistent="false"`, the collapsed state bound to `useSidebar`); rail groups carry a unique `key` (a run in two sections no longer duplicates a `v-for` key); the rail footer shows the same utility buttons, stacked, with the full template written out; the layout file is the one plan 1 left (`RunNameAsks`).
> - **Task 6:** anchors and line references refreshed for plan 1's pages; `FullscreenButton` takes a `size` (the join headers use `sm`); the headless script asserts, joins through the name form and counts fullscreen requests through a stub instead of reading the browser's refusal.
> - **Task 7:** `Available` extends plan 1's `catalogEntry`/`entry()` (which gains the lookup cache), `saveAgentRequest` takes `available` and ignores it (`TestCatalogSaveKeepsMaskedEnvValues` stays green), `os/exec` leaves `catalog.go`; the check route asks now and keeps the answer (`lookupCache.check`); a saved override inherits the built-in's `site` as it does `adapter` and `signal`; the site rule lives with plan 1's form rules in `utils/agentForm.ts`; select items use plan 1's `agentIcon()`; `TestDefaultsHaveSites` checks shape only.
> - **Task 8:** every replaced sentence is quoted from the current text; `AGENTS.md` and `docs/architecture.md` rows for `internal/cli` and `cmd/conductor`; the built-in sites are a user item under Open verification.
> - **Everywhere:** the headless test server runs with a fresh `HOME`, its own `CONDUCTOR_DATA_DIR` and `CONDUCTOR_PUBLIC_URL=http://127.0.0.1:8099` (the example config's `publicUrl` is the user's own server on :8080, and without a data directory the server would take the checkout's `conductor.d`); every headless script asserts and exits 1 on the first failure; verification step 4 installs into a temporary rc file with `--rc`.

**Goal:** The round 3 features: a working-directory picker fed by the server with a git check, four example crews seeded once, shell completion for the CLI, a sidebar that collapses to an icon rail instead of disappearing, document fullscreen on every page, and agent availability (which catalog agents are installed on the server) on the Agents page, the Launch dialog, the crew editor and the crew launch.

**Architecture:** Two small admin routes (`GET /api/paths`, `GET /api/git/check`) reuse `resolveCwd` for confinement and the crew package's argv `git` calls (the `rev-parse` path a launch with worktrees takes) for the git state; the picker (`DirInput`, a `UInputMenu`) and the crew editor read them. Example crews are plain data (`crew.Examples`) saved through a once-only `Store.Seed` on the per-crew store, reached from `conductor serve --examples`, `CONDUCTOR_EXAMPLES=1` and `POST /api/crews/examples`. Completion scripts are generated in `internal/cli` from a static table of the subcommands and flags (held to the commands' own flag sets by a test), and read crew ids live from `conductor crews --ids`. On the client, the sidebar's "hidden" flag becomes a two-mode state (`full` | `rail`) kept in localStorage and rendered through Nuxt UI's `UDashboardSidebar` collapse, whose own cookie persistence is turned off; fullscreen becomes shared state with one `FullscreenButton` component and one shortcut registration per layout. Availability is the `exec.LookPath` check `POST /api/catalog/check` already runs, behind a 30 s per-program cache on the server (`lookupCache`), reported as `available` on the catalog's entries next to a new `site` field, and consulted by the crew launch's `checkLaunch`.

**Tech Stack:** Go stdlib (`net/http` mux, `os/exec` with argv only, `flag`), Nuxt 4 + Nuxt UI 4 (`UDashboardSidebar` collapse, `UDashboardGroup` persistence, `UNavigationMenu` collapsed, `UInputMenu` in `autocomplete` mode, `UTooltip`), vitest for pure utilities, headless Chromium through the playwright-core already installed in the scratchpad. No new dependency.

**Spec:** `docs/features.md` § "Round 3: polish, examples and completion (planned 2026-10-01)" → "Decisions" (the seven bullets: sidebar rail, example crews, working-directory autocomplete, git check, CLI completion, fullscreen everywhere, agent availability). The "Deferred items" and "Crew storage" parts of that section are plan 1 of round 3 (`docs/superpowers/plans/2026-10-01-round3-deferred.md`), which has landed (`31e86b3`, with its review's fixes up to `55b9198`). What this plan relies on from it, by its current name: crews live one file each at `<dataDir>/crews/<id>.json` (the data directory defaults to `~/.conductor`; `config.ResolveDataDir`); the crew store (`internal/crew/store.go`) is `NewStore(st)`, `Get` (`(Crew, error)`: `ErrNotFound`, `ErrUnreadable`), `Put`, `Create`, `Update`, `Duplicate`, `Delete` (`(bool, error)`), `List(offset, limit)` (`[]Summary`, total), its mutex `mu`, `taken()` (every id with a file, usable or not) and `commit`; `GET /api/crews?offset=0&limit=100` answers `{crews: [summaries], total}` (limit at most 500) and `GET /api/crews/{id}` answers `{crew}`; `inRepo` reports `rev-parse` failures other than "not a repository" with git's message (`classifyRevParse`); `api.catalogEntry{Agent; Source; Replaces}` and `entry()` shape the catalog routes' agents, `saveAgentRequest` takes and ignores what the listing adds; `ownedBy` (with the own-home exception) is in `internal/agents/home.go`, `FileOwner` in `owner_unix.go`; `conductor crews` pages by 100 with a 4 MiB cap a page (`crewsPage`, `maxCrewsReply`); `web/app/utils/agentIcons.ts` (`AGENT_ICONS`, `agentIcon()`) feeds `nuxt.config.ts`'s icon bundle; `web/app/utils/agentForm.ts` holds the add-agent form's rules; the crew and run API tests live in `internal/api/crews_test.go` (`crewBody`, `crewMember`, `e.crew`, `e.crewIDs`, `e.sendCrew`, `e.storedCrew`, `wantAPIError`), the shared helpers (`newTestEnv`, `e.do`, `adminToken`, `errorCode`, `agentBody`, `e.save`, `e.catalogAgent`) in `internal/api/api_test.go`.

## Global Constraints

Copied from the spec and `AGENTS.md`; every task's requirements include them.

- `GET /api/paths?prefix=<path>&limit=50` (admin) lists child directories of the longest existing directory in the prefix, confined to `allowedRoots` through the same resolution as `resolveCwd` (symlinks resolved, nothing outside a root), at most 50 entries, hidden directories only when the typed segment starts with a dot, each entry with `git: {repo, commits}`.
- `GET /api/git/check?cwd=<path>` (admin) answers `{inRepo, toplevel, hasCommit, message}` with the same rules the launch uses (`git rev-parse` as argv through `crew.git`, `classifyRevParse`'s messages, under the allowed roots); the launch handler's 409 stays as the authority.
- Server session working directories go through `resolveCwd`; file reads go through `session.ResolvePath`. Do not bypass either.
- Commands are argv arrays. Never build a shell string from user input. (`git` runs as `exec.CommandContext(ctx, "git", "-C", dir, args...)` through `crew.git`. The completion scripts never expand server data: zsh passes the crew ids to `compadd` as array elements; bash reads them in a `while read` loop, compares each with the typed prefix as a string and adds it to `COMPREPLY` as it is, never through `compgen -W`, which expands what it is given; and `conductor crews --ids` prints only ids shaped like a crew's, `crew.ValidID`.)
- Keep per-connection bounds: `prefix` and `cwd` queries are at most 4096 bytes without NUL; a listing reads at most 2000 directory entries, 256 at a time, before it sorts them and keeps at most 50; the git calls of one request run one at a time, at most one per listed entry, under a shared 5 s deadline; a path a client asks about is logged at debug level only.
- Example crews: ids `example-todo-app`, `example-test-fixer`, `example-docs-writer`, `example-dependency-upgrade`; an id that has a file in `crews/`, usable or not, is left alone; members use the `claude` and `codex` built-ins; `cwd` is the server's default working directory; isolation `worktree`. The examples are data, not built-ins: editing or deleting them is normal.
- CLI completion: `conductor crews --ids` prints ids only, one per line, exit 0 and silent when the server is unreachable or the token is missing, using `CONDUCTOR_SERVER` and `CONDUCTOR_ADMIN_TOKEN` from the environment; it pages through every crew as `conductor crews` does. `conductor completion install` appends one marked `source` line to `~/.zshrc` or `~/.bashrc` (idempotent; refuses a file not owned by the user). No new dependency.
- `internal/cli` holds flags, help and completion text (the completion table, the two script generators and the rc-file edit); no business logic: the seeding and the git state live in `internal/crew` and `internal/api`, the ownership rule in `internal/agents`.
- Stdlib first: `net/http` mux with method patterns, `log/slog`, `encoding/json` with `DisallowUnknownFields`. New Go dependencies need a reason in the PR (there are none here).
- An API route needs: handler in `internal/api`, auth via `requireAdmin` or `authenticate`, a test in the api test package (crew routes in `crews_test.go`, the others in their own file or `api_test.go`), and the client call in `web/app/composables/useSessions.ts`.
- Sidebar rail: the rail keeps every function; `meta+B` / Alt+B toggles rail and full width; the choice is persisted per browser in localStorage alone (`conductor.sidebar.mode`), Nuxt UI's own collapse persistence being off for the dashboard; narrow screens keep the slideover.
- Fullscreen: one button in every page header and one shortcut registration; the wall and carousel keep their buttons; `F` is ignored while an input or textarea has focus, as the other letter shortcuts already are.
- Agent availability: `GET /api/catalog` reports `available` per agent (whether `command[0]` resolves on the server through the same check `POST /api/catalog/check` uses, re-evaluated per request and cached for 30 s) and `site` (an optional `https` URL). The Launch dialog's server tab lists only available agents; the "My machine" tab lists every agent because availability there is the host's; the launch route refuses a crew whose member agent is not installed with `invalid_crew` naming it.
- UI components come from Nuxt UI; use the brand palette in `web/app/app.config.ts` and `docs/design/brand.md`. The junction mark is artwork, never a status light (the rail shows the mark as an image and the attention dot on the avatars, never on the mark). An agent icon outside `AGENT_ICONS` is shown through `agentIcon()`; a status icon named in a source file is bundled by the scan.
- Do not commit `internal/web/dist` contents (only `.gitkeep`), `web/.nuxt` or `web/.output`. Commit `web/package-lock.json`.
- Checks before finishing: `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test`, `make web-build`, `make build-go`. A check that cannot run is a reported limitation, not a pass.

## Review Focus

The five inputs most likely to bite a person, each pinned to the task whose tests exercise it.

1. **A prefix that walks through a symbolic link to a directory outside the roots** (`<root>/escape/…`, `escape -> /elsewhere`). Expected: `200` listing from the longest allowed ancestor, the link itself never listed and the target's children never shown; a prefix wholly outside (`/etc`, `<root>/..`) is `400 invalid_cwd`. Pinned in Task 1 (`TestPathsNeverLeaveTheRoots`).
2. **A server (or a man in the middle on `CONDUCTOR_SERVER`) that answers `conductor crews --ids` with an id carrying spaces, quotes, `$(…)` or control characters.** Expected: the id is dropped, the valid ones printed, exit 0; and whatever a `conductor` on the command line prints, the scripts never run it. Pinned in Task 4 (`TestCrewsIDsPrintsOnlyWellFormedIDs`, `TestCompletionNeverRunsWhatTheServerSends`).
3. **`conductor completion install` under `sudo`, where `~/.zshrc` belongs to another user.** Expected: refused with the owner named, nothing written. Pinned in Task 4 (`TestCheckOwnerRefusesAnotherUsersFile` in `internal/agents`; `installCompletionLine` calls `CheckOwner` on the file, or on its directory when it is missing).
4. **A browser that still holds the old `conductor.sidebar.hidden=1` key from before the rail, or a Nuxt UI sidebar cookie from an older build.** Expected: the first load shows the rail (hidden became the rail), the old key is removed and `conductor.sidebar.mode=rail` written, a later `meta+B` works as usual, and no cookie decides the mode. Pinned in Task 5 (`readSidebarMode` vitest and `rail-check.js`).
5. **Pressing `f` while typing a crew name, a goal, a reply or the session filter.** Expected: the letter is typed and nothing goes fullscreen; `f` on the page body toggles it. Pinned in Task 6 (`fullscreen-check.js`).

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/api/paths.go` (new), `paths_test.go` (new) | `GET /api/paths`, `GET /api/git/check`: prefix splitting, bounded confined listing, git marks |
| `internal/crew/worktree.go`, `worktree_test.go` | `GitState` (the launch's repo rules as one answer), `toplevel` |
| `internal/crew/examples.go` (new), `examples_test.go` (new) | the four example crews as data; `Seed`'s tests |
| `internal/crew/store.go` | `Store.Seed`: once-only per id (Task 3); `ValidID` (Task 4) |
| `internal/api/crews.go`, `crews_test.go`, `server.go` | `POST /api/crews/examples`, `SeedExampleCrews`, routes |
| `internal/config/config.go`, `config_test.go` | `Examples` from `CONDUCTOR_EXAMPLES` |
| `internal/cli/serve.go`, `serve_test.go` | `--examples` |
| `internal/cli/completion.go` (new), `completion_test.go` (new) | `conductor completion zsh|bash|install`: the static table, the two generators, the rc line |
| `internal/cli/up.go`, `up_test.go`, `root.go` | `conductor crews --ids`, the paging helper `crewList`, dispatch and usage |
| `internal/agents/home.go`, `owner_test.go` | `CheckOwner` (ownership of any path, by `ownedBy`'s rule) |
| `web/app/utils/dirInput.ts` (new, +test), `web/app/components/DirInput.vue` (new) | the picker's pure parts and the `UInputMenu` combobox |
| `web/app/components/CrewEditor.vue`, `LaunchSessionModal.vue` | use `DirInput`; the editor's git line and launch tooltip |
| `web/app/utils/sidebar.ts` (new, +test), `web/app/composables/useSidebar.ts`, `web/app/components/SidebarRail.vue` (new), `web/app/layouts/default.vue` | rail mode, migration, rail rendering |
| `web/app/components/SidebarReveal.vue` (deleted) and its eight users | the reveal button is replaced by the rail's expand button |
| `web/app/composables/useFullscreenToggle.ts`, `web/app/components/FullscreenButton.vue` (new), `web/app/layouts/bare.vue`, every page header | fullscreen everywhere |
| `web/app/composables/useShortcuts.ts` (+test) | the `F` row under Everywhere, once; the rail's `meta+B` label |
| `internal/api/lookup.go` (new, +test), `internal/api/catalog.go`, `runs.go`, `server.go` | the 30 s program lookup cache, `available` on the catalog's entries, the launch refusal |
| `internal/catalog/catalog.go`, `defaults.go`, `catalog_test.go` | `Site` on agents, validated and inherited; the built-ins' sites |
| `web/app/utils/agents.ts` (new, +test), `web/app/utils/agentForm.ts` (+test), `web/app/composables/useServerHost.ts` (new) | availability helpers for the three surfaces; the form's site rule; the server's host name, read once |
| `web/app/pages/agents.vue`, `components/AddAgentSlideover.vue`, `LaunchSessionModal.vue`, `CrewMembersTable.vue`, `CrewEditor.vue` | greyed cards with the note and the site link; the `site` field; the filtered server tab; the marked select |
| `web/app/composables/useSessions.ts` | `listPaths`, `gitCheck`, `loadExampleCrews`, `available`/`site` and their types |
| `web/app/pages/crews/[[id]].vue` | "Load the examples" on the empty page |
| `docs/protocol.md`, `README.md`, `docs/features.md`, `docs/architecture.md`, `AGENTS.md` | docs |

Tasks 1–4 are independent of each other but for one link: Task 4's completion table lists Task 3's `serve --examples`, and its `TestCompletionSpecMatchesEveryFlag` holds the table to the commands. Task 2 needs Task 1's routes; Task 7 touches `LaunchSessionModal.vue` and `CrewEditor.vue` after Task 2 did, and `default.vue`'s neighbours after Tasks 5 and 6; Task 8 documents everything. Run them in order. Line numbers below are at `55b9198`; where an earlier task of this plan moved a line, the quoted text is the anchor.

Headless checks use the Playwright already installed at `/tmp/claude-1000/-home-nater-go-src-github-com-phenixrizen-conductor/a75c70c0-928a-4c03-ab5e-19119aca1b6c/scratchpad/pw` (`playwright-core`; Chromium under `~/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`; launch with `args: ['--no-sandbox', '--use-gl=swiftshader']`; navigate with `waitUntil: 'commit'` then wait for a selector). Every script asserts with `node:assert/strict`, prints `PASS <name>` at the end and exits 1 at the first failed check; a step passes only on exit 0. The scripts are written into that directory and run from it. The test server for them is built with `make web-build && make build-go` and started from the repository with a home and a data directory of its own and its public URL on the test port (the example config's `publicUrl` is the user's own server on :8080, and with no data directory set the server would fall back to the checkout's `conductor.d`):

```bash
PW=/tmp/claude-1000/-home-nater-go-src-github-com-phenixrizen-conductor/a75c70c0-928a-4c03-ab5e-19119aca1b6c/scratchpad/pw
mkdir -p $PW/home && DATA=$(mktemp -d $PW/data.XXXXXX)
HOME=$PW/home CONDUCTOR_DATA_DIR=$DATA CONDUCTOR_PUBLIC_URL=http://127.0.0.1:8099 \
  bin/conductor serve --config conductor.example.json --listen 127.0.0.1:8099 > $PW/server.log 2>&1 &
echo $! > $PW/server.pid
# … the step's checks …
kill $(cat $PW/server.pid); rm -rf "$DATA"
```

Admin token `dev-admin-token-change-me`; the allowed root is `.`, the repository, so a temporary directory tree for the picker goes under `./tmp-picker/` and is removed afterwards. Kill the test server by pid, never `pkill -f conductor`.

---

### Task 1: `GET /api/paths` and `GET /api/git/check`

**Files:**
- Create: `internal/api/paths.go`, `internal/api/paths_test.go`
- Modify: `internal/crew/worktree.go:48-63` (`inRepo` → `toplevel` + `inRepo` + `State` + `GitState`; `classifyRevParse` at `:65-75` stays and is called from `toplevel`), `internal/crew/worktree_test.go` (append), `internal/api/server.go:235` (routes, after `POST /api/crews/{id}/launch`), `web/app/composables/useSessions.ts:272-274` (types after `RunInfo`) and `:356-357` (calls after `whoami`)

**Interfaces:**
- Consumes: `(*Server).resolveCwd(cwd string) (string, error)` (`internal/api/sessions.go:216`); from `internal/crew/worktree.go` (plan 1's): `git`, `headCommit`, `classifyRevParse`, `ErrNoGit`, `ErrNotRepo`, `errNotInRepo`, `errNoCommit`; `requireAdmin`, `writeJSON`, `writeError` (`internal/api`); test helpers `newTestEnv`, `e.do`, `e.root`, `adminToken`, `errorCode` (`api_test.go`), `newRepo`, `runGit` (`worktree_test.go`).
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
func toplevel(ctx context.Context, dir string) (string, error) // git -C dir rev-parse --show-toplevel; ErrNoGit / ctx's / classifyRevParse (matches ErrNotRepo)
const msgCanWorktree = "a git repository with a commit: a crew with worktrees can launch here"
// internal/api/paths.go
type pathGit struct{ Repo, Commits bool }                 // json repo, commits
type pathEntry struct{ Name, Path string; Git pathGit }   // json name, path, git
type pathsReply struct{ Dir string; Entries []pathEntry; Truncated bool }
type gitCheckReply struct{ InRepo bool; Toplevel string; HasCommit bool; Message string } // json inRepo, toplevel (omitempty), hasCommit, message
const maxPathEntries, maxPathQuery, gitCheckTimeout, pathReadChunk = 50, 4096, 5 * time.Second, 256
var maxPathScan = 2000                      // a test lowers it
var gitState = crew.GitState                // a test counts the calls
func (s *Server) splitPrefix(prefix string) (dir, seg string, err error)
func readCandidates(dir, seg string) (out []candidate, capped bool, err error)
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

- [ ] **Step 1: Write the failing crew test for `GitState`** at the end of `internal/crew/worktree_test.go` (the file's `newRepo`, `runGit` helpers exist; it imports `os`, `path/filepath`, `strings`, `testing` already):

```go
// GitState answers in one call what a launch with worktrees checks: in a
// repository with a commit (from its top or below it), in one without, or in
// no repository, each with the words the launch's refusal would use.
func TestGitStateReportsRepoCommitAndPlain(t *testing.T) {
	repo := newRepo(t)
	st, err := GitState(t.Context(), repo)
	if err != nil || !st.InRepo || st.Toplevel != repo || !st.HasCommit || st.Message != msgCanWorktree {
		t.Fatalf("repo: %+v %v", st, err)
	}
	sub := filepath.Join(repo, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if st, _ := GitState(t.Context(), sub); !st.InRepo || st.Toplevel != repo || !st.HasCommit {
		t.Fatalf("below the top: %+v", st)
	}
	empty, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, empty, "init", "-q")
	if st, err := GitState(t.Context(), empty); err != nil || !st.InRepo || st.Toplevel != empty || st.HasCommit || st.Message != errNoCommit.Error() {
		t.Fatalf("no commit: %+v %v", st, err)
	}
	if st, err := GitState(t.Context(), t.TempDir()); err != nil || st.InRepo || st.HasCommit || st.Message != errNotInRepo.Error() {
		t.Fatalf("plain: %+v %v", st, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/crew -run TestGitStateReportsRepoCommitAndPlain`
Expected: FAIL to compile, `undefined: GitState` (and `msgCanWorktree`).

- [ ] **Step 3: Implement `toplevel` and `GitState`** in `internal/crew/worktree.go`: replace the whole `inRepo` function (`:48-63`, from its comment `// inRepo checks that dir is in a git working tree: git -C dir rev-parse` to its closing brace) with the block below, and in the comment of `classifyRevParse` (`:65`) change "the error inRepo reports" to "the error toplevel reports". `CheckRepo` and `AddWorktree` keep calling `inRepo`, which now goes through `toplevel`: one `rev-parse` path, one argv rule, plan 1's messages.

```go
// toplevel is the top of the git working tree dir is in: git -C dir
// rev-parse --show-toplevel. The error is ErrNoGit when git is not on PATH,
// ctx's when ctx ended, and otherwise matches ErrNotRepo: "not in a git
// repository" when git says so, git's own message for any other failure
// (classifyRevParse).
func toplevel(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return "", ctx.Err()
		case errors.Is(err, exec.ErrNotFound):
			return "", ErrNoGit
		}
		return "", classifyRevParse(err)
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
	InRepo    bool   // dir is in a git working tree
	Toplevel  string // its top, when InRepo
	HasCommit bool   // HEAD is a commit to branch from
	Message   string // the words a launch's refusal uses, or that worktrees can be made
}

// msgCanWorktree is State.Message when worktrees can be made.
const msgCanWorktree = "a git repository with a commit: a crew with worktrees can launch here"

// GitState reports, in one answer, what CheckRepo checks of a crew's working
// directory before a launch with isolation "worktree", through the same
// rev-parse calls: whether dir is in a git working tree and its top, and
// whether HEAD is a commit to branch from. Message says so in the words the
// launch's refusal uses; a directory git refuses for another reason (dubious
// ownership, permissions) is InRepo false with git's message. The error is
// ErrNoGit when git is not on PATH, or ctx's.
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
Expected: PASS (the existing `CheckRepo`, `AddWorktree`, `TestClassifyRevParsePassesGitsMessageOn` and engine tests still pass through `inRepo`).

- [ ] **Step 5: Write the failing API tests** in `internal/api/paths_test.go`:

```go
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/crew"
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
	raw, _ := out["entries"].([]any)
	for _, e := range raw {
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
	// A hidden directory shows only once the dot is typed, the dot alone included.
	if _, out := e.paths(filepath.Join(root, ".h"), ""); !slices.Equal(entryNames(out), []string{".hidden"}) {
		t.Fatalf("prefix .h: %v", out)
	}
	if _, out := e.paths(root+string(filepath.Separator)+".", ""); out["dir"] != root || !slices.Equal(entryNames(out), []string{".hidden"}) {
		t.Fatalf("prefix .: %v", out)
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
		mkdirs(t, root, fmt.Sprintf("d%02d", i))
	}
	if _, out := e.paths(root, ""); len(entryNames(out)) != 50 || out["truncated"] != true {
		t.Fatalf("default limit: %d %v", len(entryNames(out)), out["truncated"])
	}
	if _, out := e.paths(root, "5"); !slices.Equal(entryNames(out), []string{"d00", "d01", "d02", "d03", "d04"}) || out["truncated"] != true {
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
	if status, out := e.paths(root+"\x00", ""); status != http.StatusBadRequest || errorCode(out) != "invalid_request" {
		t.Fatalf("NUL: %d %v", status, out)
	}
	if resp, out := e.do("GET", "/api/paths?prefix="+url.QueryEscape(root), "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d %v", resp.StatusCode, out)
	}
}

// A listing reads at most maxPathScan entries of a directory, however many
// it holds, and says it may have missed some.
func TestPathsReadsABoundedNumberOfEntries(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	for i := range 30 {
		mkdirs(t, root, fmt.Sprintf("d%02d", i))
	}
	was := maxPathScan
	maxPathScan = 10
	t.Cleanup(func() { maxPathScan = was })
	if _, out := e.paths(root, ""); len(entryNames(out)) != 10 || out["truncated"] != true {
		t.Fatalf("capped read: %v", out)
	}
}

// One listing asks git about each listed entry with a .git of its own and,
// once, about the directory for the others: never more than the limit, one
// call at a time.
func TestPathsAsksGitAtMostOncePerEntry(t *testing.T) {
	e := newTestEnv(t, nil)
	root := realRoot(t, e.root)
	for i := range 60 {
		mkdirs(t, root, filepath.Join(fmt.Sprintf("r%02d", i), ".git"))
	}
	mkdirs(t, root, "zplain") // after the 50 listed, so the directory's marks are never needed
	var asked []string
	was := gitState
	gitState = func(_ context.Context, dir string) (crew.State, error) {
		asked = append(asked, dir)
		return crew.State{InRepo: true, HasCommit: dir != root}, nil
	}
	t.Cleanup(func() { gitState = was })
	_, out := e.paths(root, "")
	if len(entryNames(out)) != 50 || len(asked) != 50 || slices.Contains(asked, root) {
		t.Fatalf("all repositories: %d entries, asked %d: %v", len(entryNames(out)), len(asked), asked)
	}
	asked = nil
	_, out = e.paths(filepath.Join(root, "z"), "")
	if r, c := entryGit(out, "zplain"); !r || c || !slices.Equal(asked, []string{root}) {
		t.Fatalf("zplain carries the directory's marks: %v %v, asked %v", r, c, asked)
	}
}

// gitInit makes dir, then makes it a git repository, with one commit when
// commit is set. It skips the test when git is not installed. HOME is a
// temporary directory so that no configuration of the user running the tests
// applies.
func gitInit(t *testing.T, dir string, commit bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
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
Expected: FAIL to compile, `undefined: maxPathScan` and `gitState`: the listing does not exist yet.

- [ ] **Step 7: Implement `internal/api/paths.go`**

```go
package api

import (
	"cmp"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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
	// gitCheckTimeout bounds the git calls of one request, which run one at a time.
	gitCheckTimeout = 5 * time.Second
	// pathReadChunk is how many directory entries one read takes.
	pathReadChunk = 256
)

// maxPathScan bounds the directory entries one listing reads, before they
// are sorted and cut to the limit: a directory of a million entries costs
// no more than one of maxPathScan. A test lowers it.
var maxPathScan = 2000

// gitState is crew.GitState, which a test replaces to count the calls.
var gitState = crew.GitState

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
// a directory itself. A lone "." after the last separator is the start of a
// hidden name, which filepath.Clean would drop. An empty prefix is the
// server's default working directory. A prefix no part of which qualifies is
// errNoAllowedDir.
func (s *Server) splitPrefix(prefix string) (dir, seg string, err error) {
	raw := cmp.Or(prefix, s.cfg.DefaultCwd)
	if strings.HasSuffix(raw, string(filepath.Separator)+".") {
		dir, seg, err := s.splitPrefix(strings.TrimSuffix(raw, "."))
		if err == nil && seg == "" {
			seg = "."
		}
		return dir, seg, err
	}
	cur := filepath.Clean(raw)
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

// candidate is a directory entry that may be listed: its name matches what
// is typed, and it is a directory or a symbolic link.
type candidate struct {
	name string
	link bool
}

// readCandidates reads at most maxPathScan entries of dir, pathReadChunk at a
// time, and keeps the directories and symbolic links whose names start with
// seg, hidden ones only when seg starts with a dot. capped reports that the
// directory has entries it did not read.
func readCandidates(dir, seg string) (out []candidate, capped bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	for read := 0; read < maxPathScan; {
		des, err := f.ReadDir(min(pathReadChunk, maxPathScan-read))
		read += len(des)
		for _, e := range des {
			name := e.Name()
			if !strings.HasPrefix(name, seg) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(seg, ".")) {
				continue
			}
			switch {
			case e.IsDir():
				out = append(out, candidate{name: name})
			case e.Type()&os.ModeSymlink != 0:
				out = append(out, candidate{name: name, link: true})
			}
		}
		if errors.Is(err, io.EOF) {
			return out, false, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	return out, true, nil
}

// listPaths lists the child directories of the directory prefix names (see
// splitPrefix) whose names start with the element typed after it: at most
// limit of them in name order, from at most maxPathScan entries read, hidden
// ones only when that element starts with a dot, a symbolic link only when it
// leads under an allowed root. A child with a .git of its own is asked git
// for its marks; the others carry their parent's, asked once and only when
// needed, so one listing runs GitState at most limit times, one after another.
func (s *Server) listPaths(ctx context.Context, prefix string, limit int) (pathsReply, error) {
	dir, seg, err := s.splitPrefix(prefix)
	if err != nil {
		return pathsReply{}, err
	}
	cands, capped, err := readCandidates(dir, seg)
	if err != nil {
		return pathsReply{}, err
	}
	slices.SortFunc(cands, func(a, b candidate) int { return strings.Compare(a.name, b.name) })
	out := pathsReply{Dir: dir, Entries: []pathEntry{}, Truncated: capped}
	var parent *pathGit
	for _, c := range cands {
		path := filepath.Join(dir, c.name)
		if c.link {
			if _, err := s.resolveCwd(path); err != nil {
				continue
			}
		}
		if len(out.Entries) == limit {
			out.Truncated = true
			break
		}
		var mark pathGit
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			if mark, err = gitMark(ctx, path); err != nil {
				return pathsReply{}, err
			}
		} else {
			if parent == nil {
				m, err := gitMark(ctx, dir)
				if err != nil {
					return pathsReply{}, err
				}
				parent = &m
			}
			mark = *parent
		}
		out.Entries = append(out.Entries, pathEntry{Name: c.name, Path: path, Git: mark})
	}
	return out, nil
}

// gitMark is the git state of dir as the picker shows it. Without git on the
// server nothing is marked.
func gitMark(ctx context.Context, dir string) (pathGit, error) {
	st, err := gitState(ctx, dir)
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

// handleListPaths answers GET /api/paths?prefix=<path>&limit=<n>. What a
// client asks about is logged at debug only.
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
		s.log.Debug("list paths failed", "prefix", prefix, "err", err)
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
	st, err := gitState(ctx, cwd)
	switch {
	case errors.Is(err, crew.ErrNoGit):
		writeJSON(w, http.StatusOK, gitCheckReply{Message: crew.ErrNoGit.Error()})
		return
	case err != nil:
		s.log.Debug("git check failed", "cwd", cwd, "err", err)
		writeError(w, http.StatusInternalServerError, "git_failed", "could not run git")
		return
	}
	writeJSON(w, http.StatusOK, gitCheckReply{InRepo: st.InRepo, Toplevel: st.Toplevel, HasCommit: st.HasCommit, Message: st.Message})
}
```

Routes, in `internal/api/server.go` `Handler()`, after `mux.HandleFunc("POST /api/crews/{id}/launch", …)` (`:235`):

```go
	mux.HandleFunc("GET /api/paths", s.requireAdmin(s.handleListPaths))
	mux.HandleFunc("GET /api/git/check", s.requireAdmin(s.handleGitCheck))
```

- [ ] **Step 8: Run the API tests**

Run: `go test -race ./internal/api -run 'TestPaths|TestGitCheck'`
Expected: PASS (the two git tests skip where `git` is missing and say so; this box has git: they must run).

- [ ] **Step 9: Add the client calls** in `web/app/composables/useSessions.ts` — the types between the end of `RunInfo` (`:272`) and `export function useSessions()` (`:274`), the calls after `whoami` (`:357`):

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
     * The child directories of the longest existing directory in `prefix`, under the allowed roots (symlinks resolved), at most 50 of the
     * first 2000 entries read, each marked when it is a git repository (`repo`) with a commit (`commits`). Hidden directories show once
     * the typed element starts with a dot. 400 `invalid_cwd` when no part of the prefix is under a root.
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
- Modify: `web/app/components/CrewEditor.vue:3-5` (imports), `:16` (after `const live = useAttention()`), `:25-30` (after `launchBlocked`), `:114-116` (the launch tooltip), `:129-131` (the working-directory field); `web/app/components/LaunchSessionModal.vue:133-135`

**Interfaces:**
- Consumes: `useSessions().listPaths`, `useSessions().gitCheck`, `PathEntry`, `PathGit`, `GitCheck` (Task 1); `useAdminToken().hasToken`; Nuxt UI `UInputMenu` (`mode="autocomplete"`: the model is the text; `items`, `value-key`, `label-key`, `ignore-filter`, `open-on-focus`, `v-model:open`, slots `item-leading`, `item-label`, `item-trailing`, `empty`, `content-bottom`; exposes `inputRef`); `CrewEditor`'s `set`, `crew`, `launchBlocked` (plan 1's file).
- Produces:
```ts
// web/app/utils/dirInput.ts
export const DIR_DEBOUNCE_MS = 150
export function dirQuery(text: string): string                       // the prefix sent: the text trimmed
export function gitMark(git: PathGit): { label: string; tone: 'success' | 'warning' | 'neutral' }
export type GitCheckView = GitCheck & { error?: boolean }           // error: the check itself failed (its message says why)
export function gitCheckLine(check: GitCheckView | null, isolation: 'none' | 'worktree'): { text: string; tone: 'success' | 'warning' | 'neutral'; blocks: boolean }
// web/app/components/DirInput.vue — <DirInput v-model="text" placeholder name />; data-dir-input on the root; options are role="option"
```

- [ ] **Step 1: Write the failing vitest** `web/app/utils/dirInput.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { DIR_DEBOUNCE_MS, dirQuery, gitCheckLine, gitMark } from './dirInput'

describe('dirInput', () => {
  it('sends the text trimmed, and empty for the server default', () => {
    expect(dirQuery('  /srv/work/a ')).toBe('/srv/work/a')
    expect(dirQuery('   ')).toBe('')
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
Expected: FAIL, `Cannot find module './dirInput'` (or "Failed to resolve import").

- [ ] **Step 3: Implement `web/app/utils/dirInput.ts`**

```ts
import type { GitCheck, PathGit } from '~/composables/useSessions'

/** How long the picker waits after a keystroke before asking the server. */
export const DIR_DEBOUNCE_MS = 150

/** The prefix sent for what is typed: the text trimmed; empty means the server's default directory. */
export function dirQuery(text: string): string {
  return text.trim()
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

Nuxt UI's `UInputMenu` in `autocomplete` mode renders it, as the docs comment says: the model stays the text, the server's entries are its items with the git mark in the `item-trailing` slot, and its own filter is off. (It was tried first and kept: the headless check in Step 8 picks with Enter and with a click and sees the list follow; no hand-rolled listbox is needed.) Two things it does not do alone are done here: a pick closes its list, so the list of the picked directory opens it again; and a click on an option takes the focus away, so the field takes it back.

```vue
<script setup lang="ts">
import type { PathEntry } from '~/composables/useSessions'
import { DIR_DEBOUNCE_MS, dirQuery, gitMark } from '~/utils/dirInput'

/**
 * A working-directory field completed from the server: as the text changes,
 * GET /api/paths lists the child directories of the longest existing
 * directory in it (under the allowed roots), each marked when it is a git
 * repository with a commit. Nuxt UI's UInputMenu in autocomplete mode is the
 * combobox: the text is the model and stays editable, the server's entries
 * are its items (its own filter is off: the server filters by what is
 * typed), the arrow keys move, Enter or a click picks (the entry's path
 * replaces the text and the list opens again on its children), Esc closes.
 * While the list is open, Enter picks the highlighted entry (the first until
 * the arrows move); with it closed, Enter belongs to the form around. The
 * model is the text, whatever is picked.
 */
const model = defineModel<string>({ default: '' })
defineProps<{ placeholder?: string; name?: string }>()

const api = useSessions()
const admin = useAdminToken()

const menu = useTemplateRef<{ inputRef?: HTMLInputElement }>('menu')
const open = ref(false)
const entries = ref<PathEntry[]>([])
const truncated = ref(false)
const loading = ref(false)
const problem = ref('')
let timer: number | undefined
// Replies may come back out of order: only the latest request's counts.
let seq = 0
// The text is an entry's path: picked (Enter, or a click, which takes the focus away), or typed in full.
let picked = false

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
  } catch (e) {
    if (n !== seq) return
    entries.value = []
    truncated.value = false
    problem.value = (e as Error).message
  } finally {
    if (n === seq) {
      loading.value = false
      // A pick closes the list: it opens again on the picked directory's children, the focus back in the field.
      const input = menu.value?.inputRef
      if (input && (picked || document.activeElement === input)) {
        picked = false
        input.focus()
        open.value = true
      }
    }
  }
}

// Typing and picking both change the text: either lists again, after a pause.
watch(model, (text) => {
  if (entries.value.some((e) => e.path === text)) picked = true
  window.clearTimeout(timer)
  timer = window.setTimeout(fetchNow, DIR_DEBOUNCE_MS)
})

function onFocus() {
  if (!entries.value.length && !loading.value) fetchNow()
}

onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <div data-dir-input>
    <UInputMenu
      ref="menu"
      v-model="model"
      v-model:open="open"
      mode="autocomplete"
      :items="entries"
      value-key="path"
      label-key="name"
      ignore-filter
      open-on-focus
      :loading="loading"
      :placeholder="placeholder"
      :name="name"
      autocapitalize="off"
      autocomplete="off"
      spellcheck="false"
      :ui="{ base: 'font-mono' }"
      class="w-full"
      @focus="onFocus"
    >
      <template #item-leading>
        <UIcon name="i-lucide-folder" class="size-4 flex-none text-muted" />
      </template>
      <template #item-label="{ item }">
        <span class="font-mono text-xs">{{ item.name }}</span>
      </template>
      <template #item-trailing="{ item }">
        <UBadge v-if="gitMark(item.git).label" :label="gitMark(item.git).label" :color="gitMark(item.git).tone" variant="subtle" size="sm" />
      </template>
      <template #empty>
        <span v-if="problem" class="text-warning">{{ problem }}</span>
        <span v-else-if="loading">Looking…</span>
        <span v-else>No directory here.</span>
      </template>
      <template #content-bottom>
        <p v-if="truncated" class="px-2 py-1 text-[11px] text-muted">More here than listed: keep typing to narrow it.</p>
      </template>
    </UInputMenu>
  </div>
</template>
```

- [ ] **Step 6: Use it in the crew editor, with the git line and the launch tooltip** (`web/app/components/CrewEditor.vue`)

Imports (`:3-5`), between `~/utils/attention` and `~/utils/sessions`:

```ts
import { gitCheckLine, type GitCheckView } from '~/utils/dirInput'
```

After `const live = useAttention()` (`:16`):

```ts
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
```

After the `launchBlocked` computed (`:25-30`; `launchHint` reads it, and a `const` is not usable before its line):

```ts
/** The launch button's tooltip: what blocks it, else why the server would refuse it (it stays enabled: the 409 answers). */
const launchHint = computed(() => launchBlocked.value || (gitLine.value.blocks ? gitLine.value.text : ''))
```

Template: the launch tooltip (`:114`) reads `launchHint`:

```vue
        <UTooltip :text="launchHint" :disabled="!launchHint">
```

(the `UButton` inside keeps `:disabled="!!launchBlocked || saving"`: the button stays enabled when only `gitLine.blocks` holds; the tooltip explains, the launch handler's 409 answers). The working-directory field (`:129-131`) becomes:

```vue
        <UFormField label="Working directory" hint="allowed root" name="cwd">
          <DirInput :model-value="crew.cwd" placeholder="server default" name="cwd" @update:model-value="set('cwd', $event)" />
          <p v-if="gitLine.text" class="mt-1 text-xs" :class="{ 'text-success': gitLine.tone === 'success', 'text-warning': gitLine.tone === 'warning', 'text-muted': gitLine.tone === 'neutral' }" data-git-state>{{ gitLine.text }}</p>
        </UFormField>
```

- [ ] **Step 7: Use it in the Launch dialog** (`web/app/components/LaunchSessionModal.vue:133-135`): the `UInput` inside the working-directory field becomes

```vue
            <DirInput v-model="state.cwd" :placeholder="selected?.cwd || 'server default'" name="cwd" />
```

While its list is open, Enter picks an entry; with the list closed (Esc), Enter submits the dialog's form as before.

- [ ] **Step 8: Typecheck, then the headless check**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: clean, all vitest files pass.

Build, make a tree under the repository (the example config's allowed root: `api` a repository without a commit and a child, `app` plain, `.hidden`), start the test server as in the header, then run the check:

```bash
make web-build && make build-go
mkdir -p tmp-picker/api/pkg tmp-picker/app tmp-picker/.hidden && git -C tmp-picker/api init -q
```

`$PW/dirinput-check.js`:

```js
// Task 2: the picker lists, picks and lists again; the editor's git line warns. Exits 1 at the first failed check.
const { chromium } = require('playwright-core')
const assert = require('node:assert/strict')
const base = process.env.BASE || 'http://127.0.0.1:8099'
const root = process.argv[2] // the repository's absolute path: the test server's allowed root
;(async () => {
  assert.ok(root, 'usage: node dirinput-check.js <repository path>')
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  try {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await ctx.addInitScript(() => localStorage.setItem('conductor.adminToken', 'dev-admin-token-change-me'))
    const page = await ctx.newPage()
    await page.goto(base + '/crews', { waitUntil: 'commit' })
    await page.getByRole('button', { name: 'New crew' }).first().click()
    const field = page.locator('[data-dir-input] input')
    const options = page.getByRole('option')
    const optionTexts = async () => (await options.allInnerTexts()).map((t) => t.replace(/\s+/g, ' ').trim())
    await field.fill(root + '/tmp-picker/a')
    await page.waitForFunction(() => document.querySelectorAll('[role="option"]').length === 2)
    const listed = await optionTexts()
    console.log('options', listed)
    assert.equal(listed[0], 'api git, no commit', 'api first, marked "git, no commit" (git init, no commit)')
    assert.match(listed[1], /^app\b/, 'app second (it carries the repository\'s mark)')
    // Enter picks the highlighted entry, the first: its path replaces the text and the list opens on its children.
    await field.press('Enter')
    await page.waitForFunction((p) => document.querySelector('[data-dir-input] input')?.value === p, root + '/tmp-picker/api')
    await page.waitForFunction(() => [...document.querySelectorAll('[role="option"]')].some((o) => o.textContent?.startsWith('pkg')))
    console.log('picked', await field.inputValue(), 'then', await optionTexts())
    // A new crew is isolation worktree: the line under the field explains the refusal.
    await page.waitForFunction(() => document.querySelector('[data-git-state]')?.textContent?.includes('Launch would be refused (not_a_repo)'))
    console.log('git line', await page.locator('[data-git-state]').innerText())
    // A click picks too, and the list follows.
    await field.fill(root + '/tmp-picker/a')
    await page.waitForFunction(() => document.querySelectorAll('[role="option"]').length === 2)
    await options.nth(1).click()
    await page.waitForFunction((p) => document.querySelector('[data-dir-input] input')?.value === p, root + '/tmp-picker/app')
    // The focus comes back to the field once the picked directory is listed.
    await page.waitForFunction(() => !!document.activeElement?.closest('[data-dir-input]'), null, { timeout: 5000 })
    // Hidden directories show once the dot is typed.
    await field.fill(root + '/tmp-picker/.')
    await page.waitForFunction(() => [...document.querySelectorAll('[role="option"]')].some((o) => o.textContent?.startsWith('.hidden')))
    console.log('hidden', await optionTexts())
    await field.press('Escape')
    await page.screenshot({ path: 'dirinput.png' })
    console.log('PASS dirinput-check')
  } finally {
    await browser.close()
  }
})().catch((e) => {
  console.error('FAIL', e.message)
  process.exit(1)
})
```

Run: `cd $PW && node dirinput-check.js /home/nater/go/src/github.com/phenixrizen/conductor` (the repository's absolute path).
Expected: exit 0 and `PASS dirinput-check`; on the way it prints the two options (`api git, no commit`, then `app git`: `app` has no `.git` of its own and carries the repository's mark), the picked path with `pkg` listed under it, the warning line ending in `Launch would be refused (not_a_repo).` (a new crew is `worktree`), and `.hidden`. Then `kill $(cat $PW/server.pid); rm -rf "$DATA" tmp-picker`.

- [ ] **Step 9: Commit**

```bash
git add web/app/utils/dirInput.ts web/app/utils/dirInput.test.ts web/app/components/DirInput.vue web/app/components/CrewEditor.vue web/app/components/LaunchSessionModal.vue
git commit -m "web: working-directory picker with git marks and the editor's git line"
```

---

### Task 3: Example crews

**Files:**
- Create: `internal/crew/examples.go`, `internal/crew/examples_test.go`
- Modify: `internal/crew/store.go:347` (`Seed`, before `commit`), `internal/config/config.go:113-114` (`Examples`, after `GeneratedAdminToken`) and `:208-210` (`applyEnv`, after `CONDUCTOR_DEV`), `internal/config/config_test.go` (append), `internal/cli/serve.go:33` (flag), `:63-65` (after `if *dev`), `:100-103` (after `api.New`), `internal/cli/serve_test.go` (append), `internal/api/crews.go:3-12` (imports) and the end of the file, `internal/api/server.go:231` (route), `internal/api/crews_test.go:389-396` (the 503 table) and the end of the file, `web/app/composables/useSessions.ts:314-316` (after `duplicateCrew`), `web/app/pages/crews/[[id]].vue:187` (before `fail`) and `:468-474` (the empty state)

**Interfaces:**
- Consumes: `crew.Crew`, `crew.Member`, `crew.Start`, `StartImmediately`, `StartAfter`, `WhereServer`, `IsolationWorktree`, `validateWithID`, `maxCwd`, `ErrInvalid` (`internal/crew/crew.go`, `store.go`); plan 1's store: `s.mu`, `s.taken()` (every id with a file in `crews/`, a link or an unreadable file included), `s.commit(c)`, `Get` (`ErrNotFound`), `Update`, `Delete`, `NewStore`; `config.Config.DefaultCwd`; `(*Server).crewStoreError`; test helpers `newStore` (`crew_test.go`), `serveUntilListening`, `logLines`, `writeServeConfig`, `clearConductorEnv` (`serve_test.go`), `newTestEnv`, `e.do`, `e.crew`, `e.crewIDs`, `e.sendCrew`, `e.storedCrew`, `crewMember`, `adminToken`; on the page: `api`, `refresh`, `router`, `toast`, `fail`, `page`/`total` (plan 1's paging).
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
	"errors"
	"os"
	"path/filepath"
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
// after cli (a start condition names one member), told to wait for core's
// branch and merge both.
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
	tester := todo.Members[3].Prompt
	for _, part := range []string{"crew/<run>/core", "crew/<run>/cli", "wait until"} {
		if !strings.Contains(tester, part) {
			t.Errorf("the tester's prompt does not say %q: %s", part, tester)
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
	c, err := s.Get("example-todo-app")
	if err != nil {
		t.Fatal(err)
	}
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
	if got, err := s.Get("example-todo-app"); err != nil || got.Goal != "edited" {
		t.Fatalf("the edit was lost: %q %v", got.Goal, err)
	}
}

// A file with an example's id that the store cannot read is skipped and left
// as it is, never overwritten by the seed.
func TestSeedLeavesAnUnreadableFileAlone(t *testing.T) {
	s, st := newStore(t)
	path := filepath.Join(st.Dir(), "crews", "example-test-fixer.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	added, skipped, err := s.Seed(Examples("/w", time.Now()))
	if err != nil || !slices.Equal(skipped, []string{"example-test-fixer"}) || len(added) != 3 {
		t.Fatalf("%v %v %v", added, skipped, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Fatalf("the unreadable file was overwritten: %s", b)
	}
}

// A crew the store refuses stops the seed, naming it; the ones before it stay.
func TestSeedStopsAtAnInvalidCrew(t *testing.T) {
	s, _ := newStore(t)
	list := Examples("/w", time.Now())
	list[1].Cwd = strings.Repeat("x", maxCwd+1)
	added, _, err := s.Seed(list)
	if err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "example-test-fixer") || !slices.Equal(added, []string{"example-todo-app"}) {
		t.Fatalf("%v %v", added, err)
	}
	if _, err := s.Get("example-docs-writer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("seeding went on past the error: %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/crew -run 'TestExamples|TestTodoApp|TestSeed'`
Expected: FAIL to compile, `undefined: Examples`, `undefined: ExampleIDs`, `s.Seed undefined`.

- [ ] **Step 3: Write `internal/crew/examples.go`**

The todo app's tester starts after `cli`: a start condition names one member, so it cannot wait for both builders; its prompt has it wait for core's branch and merge both.

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
	first := Start{When: StartImmediately}
	after := func(m string) Start { return Start{When: StartAfter, Member: m} }
	return []Crew{
		crew("example-todo-app", "Example: todo app",
			"Build a small command-line todo app in this repository: add, list and done commands, items kept in a JSON file in the user's home directory, with tests.",
			member("lead", "claude", "You lead a crew of four on this goal: $GOAL. Plan, do not build. Write PLAN.md at the top of the repository with the data model, the command-line interface and the file layout, then split the work into two parts, core (storage and the data model) and cli (the commands), naming the files each part owns and the interface between them. Commit PLAN.md and stop: the builders start when you report done.", first),
			member("core", "claude", "You build the core part of PLAN.md at the top of the repository, for this goal: $GOAL. Work only in the files PLAN.md gives to core, with unit tests, and commit as you go. Report done when the storage and the data model are complete and their tests pass.", after("lead")),
			member("cli", "codex", "You build the cli part of PLAN.md at the top of the repository, for this goal: $GOAL. Work only in the files PLAN.md gives to cli, against the interface PLAN.md says core exposes, and commit as you go. Report done when every command works end to end.", after("lead")),
			// A start condition names one member: the tester starts after cli
			// and waits for core's branch itself.
			member("tester", "codex", "You test the todo app built for this goal: $GOAL. You start once cli reports done; core may still be at work. The builders commit on the branches crew/<run>/core and crew/<run>/cli of this repository (your run is in $CONDUCTOR_RUN): wait until crew/<run>/core holds the core part PLAN.md describes, then merge both branches into your worktree, run the whole test suite, add the tests PLAN.md asks for that are missing, and change test code only. Hand a bug in the app to its owner with: conductor notify --event handoff --to core --message \"what and where\" (or --to cli). Report done when the suite is green.", after("cli")),
		),
		crew("example-test-fixer", "Example: test fixer",
			"Make this repository's test suite pass without weakening a test.",
			member("triage", "claude", "Run this repository's test suite for this goal: $GOAL. Write TRIAGE.md at the top of the repository: every failing test, its cause as far as you can tell, grouped by the production file to change. Commit it and report done. Fix nothing yourself.", first),
			member("fixer", "codex", "Fix the failures TRIAGE.md at the top of the repository lists, for this goal: $GOAL. Change production code before tests; never delete, skip or loosen a test. Commit once per group in TRIAGE.md and report done when the whole suite passes.", after("triage")),
		),
		crew("example-docs-writer", "Example: docs writer",
			"Write a README for this repository that a new contributor can build, run and configure from.",
			member("reader", "claude", "Read this repository for this goal: $GOAL: the build files, the entry points, the configuration and the tests. Write NOTES.md at the top of the repository with what the project does, how it is built and run, and how it is configured, every fact with the file it comes from. Commit it and report done.", first),
			member("writer", "codex", "Write README.md from NOTES.md at the top of the repository, for this goal: $GOAL: what it is, a quick start, configuration, development. Keep every command exactly as it is run; mark anything NOTES.md does not establish as an open question rather than guessing. Commit it and report done.", after("reader")),
		),
		crew("example-dependency-upgrade", "Example: dependency upgrade",
			"Upgrade this repository's direct dependencies to their latest compatible versions, one at a time, with the test suite passing after each.",
			member("scout", "codex", "Survey this repository's direct dependencies for this goal: $GOAL. Write UPGRADES.md at the top of the repository: each dependency with its current and latest versions, the ones furthest behind first, and a note on any whose release notes announce a breaking change. Commit it and report done. Upgrade nothing yourself.", first),
			member("upgrader", "claude", "Upgrade the dependencies UPGRADES.md at the top of the repository lists, for this goal: $GOAL, one dependency per commit, running the test suite after each and fixing what the upgrade breaks. Skip, and note in UPGRADES.md, any that cannot be made to pass. Report done with the list of what moved.", after("scout")),
		),
	}
}
```

And `Seed` in `internal/crew/store.go`, after `Delete` and before the comment of `commit` (`:347`). It goes by `taken`, the store's own list of every id with a file, so a file it cannot read is skipped like any other and never overwritten:

```go
// Seed saves the crews of list whose ids have no file in the crews
// directory, and returns the ids it added and the ids it skipped, each in
// list's order. A file with the id, usable or not (taken), is left alone
// whatever it holds: seeding twice changes nothing, a crew edited after a
// seed stays as the editor left it, and a file the store cannot read is
// never overwritten. The first crew that cannot be saved stops the seed,
// naming it; the ones saved before it stay.
func (s *Store) Seed(list []Crew) (added, skipped []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	taken, err := s.taken()
	if err != nil {
		return nil, nil, err
	}
	added, skipped = []string{}, []string{}
	for _, c := range list {
		if taken[c.ID] {
			skipped = append(skipped, c.ID)
			continue
		}
		if err := s.commit(c); err != nil {
			return added, skipped, fmt.Errorf("seed %s: %w", c.ID, err)
		}
		taken[c.ID] = true
		added = append(added, c.ID)
	}
	return added, skipped, nil
}
```

- [ ] **Step 4: Run the crew tests**

Run: `go test -race ./internal/crew`
Expected: PASS.

- [ ] **Step 5: Write the failing config and serve tests**

At the end of `internal/config/config_test.go` (it imports `os`, `path/filepath`, `strings`, `testing` already):

```go
// CONDUCTOR_EXAMPLES turns the example-crew seeding on; it is not a config-file key.
func TestExamplesComesFromTheEnvironmentOnly(t *testing.T) {
	for _, v := range []string{"1", "true"} {
		t.Setenv("CONDUCTOR_EXAMPLES", v)
		if cfg, err := Load(""); err != nil || !cfg.Examples {
			t.Fatalf("CONDUCTOR_EXAMPLES=%s: %+v %v", v, cfg, err)
		}
	}
	for _, v := range []string{"", "0", "yes"} {
		t.Setenv("CONDUCTOR_EXAMPLES", v)
		if cfg, err := Load(""); err != nil || cfg.Examples {
			t.Fatalf("CONDUCTOR_EXAMPLES=%q: %+v %v", v, cfg, err)
		}
	}
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"examples": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "examples") {
		t.Fatalf("a config file with examples should be rejected as an unknown field: %v", err)
	}
}
```

At the end of `internal/cli/serve_test.go`:

```go
// --examples (or CONDUCTOR_EXAMPLES=1) seeds the four example crews into the
// data directory once, one file each: the second start adds nothing and says
// so, and without either nothing is seeded.
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
	if lines := logLines(logs, "example crews", `added="[example-todo-app example-test-fixer example-docs-writer example-dependency-upgrade]"`, "skipped=[]"); len(lines) != 1 {
		t.Fatalf("first start:\n%s", logs)
	}
	for _, id := range []string{"example-todo-app", "example-test-fixer", "example-docs-writer", "example-dependency-upgrade"} {
		if _, err := os.Stat(filepath.Join(data, "crews", id+".json")); err != nil {
			t.Fatal(err)
		}
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

Run: `go test ./internal/config -run TestExamplesComes; go test ./internal/cli -run TestServeSeeds`
Expected: FAIL to compile (`cfg.Examples undefined`); `flag provided but not defined: -examples`.

- [ ] **Step 7: Implement the config field, the flag and the seeding**

`internal/config/config.go`, in `Config` after `GeneratedAdminToken` (`:113-114`):

```go
	// Examples seeds the example crews once at startup: CONDUCTOR_EXAMPLES=1
	// (or true), or conductor serve --examples. Not a config-file key: the
	// decoder ignores it, so an "examples" key in the file is rejected.
	Examples bool `json:"-"`
```

In `applyEnv`, after the `CONDUCTOR_DEV` block (`:208-210`):

```go
	if v := getenv("CONDUCTOR_EXAMPLES"); v == "1" || v == "true" {
		cfg.Examples = true
	}
```

`internal/cli/serve.go`: the flag after `logLevel` (`:33`):

```go
	examples := fs.Bool("examples", false, "seed the example crews once (env CONDUCTOR_EXAMPLES=1); a crew whose id exists is left alone")
```

after `if *dev { cfg.Dev = true }` (`:63-65`):

```go
	if *examples {
		cfg.Examples = true
	}
```

after `srv, err := api.New(cfg, cat, log, ui, st)` and its error check (`:100-103`):

```go
	if cfg.Examples {
		added, skipped, err := srv.SeedExampleCrews()
		if err != nil {
			return 1, fmt.Errorf("seed the example crews: %w", err)
		}
		log.Info("example crews", "added", added, "skipped", skipped)
	}
```

`internal/api/crews.go`: `"time"` joins the imports (`:3-12`, after `"strings"`), and at the end of the file:

```go
// SeedExampleCrews saves the example crews (crew.Examples) whose ids have no
// file in the crews directory, with the server's default working directory,
// and returns what it added and what it skipped. conductor serve --examples
// and POST /api/crews/examples both come here.
func (s *Server) SeedExampleCrews() (added, skipped []string, err error) {
	if s.crews == nil {
		return nil, nil, errors.New("no data directory is configured")
	}
	return s.crews.Seed(crew.Examples(s.cfg.DefaultCwd, time.Now().UTC()))
}

// handleSeedExamples answers POST /api/crews/examples with {added, skipped}.
// The examples' agents, the claude and codex built-ins, are not checked
// against the catalog: a catalog that hides them still gets the examples,
// the editor shows the agent as not in the catalog, and the launch refuses
// it.
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

Route in `server.go`, after `mux.HandleFunc("POST /api/crews", …)` (`:231`):

```go
	mux.HandleFunc("POST /api/crews/examples", s.requireAdmin(s.handleSeedExamples))
```

- [ ] **Step 8: Run the config and serve tests**

Run: `go test -race ./internal/config -run TestExamplesComes && go test -race ./internal/cli -run TestServeSeeds`
Expected: PASS.

- [ ] **Step 9: Write the API test** in `internal/api/crews_test.go`: the 503 table of `TestCrewRoutesNeedAStore` (`:389-396`) gains a row after `{"POST", "/api/crews/crew/launch", nil},`:

```go
		{"POST", "/api/crews/examples", nil},
```

and at the end of the file:

```go
// POST /api/crews/examples seeds the examples once, one file each, with the
// server's default working directory: a second call adds nothing, an edit
// survives it, a deleted example comes back, and the list pages them with the
// rest.
func TestSeedExamplesOnce(t *testing.T) {
	e := newTestEnv(t, nil)
	if resp, _ := e.do("POST", "/api/crews/examples", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	resp, out := e.do("POST", "/api/crews/examples", adminToken, nil)
	if resp.StatusCode != http.StatusOK || len(out["added"].([]any)) != 4 || len(out["skipped"].([]any)) != 0 {
		t.Fatalf("first seed: %d %v", resp.StatusCode, out)
	}
	if ids := e.crewIDs(); !slices.Equal(ids, []string{"example-dependency-upgrade", "example-docs-writer", "example-test-fixer", "example-todo-app"}) {
		t.Fatalf("listed by name: %v", ids)
	}
	c := e.crew("example-todo-app")
	if c["cwd"] != e.root || c["isolation"] != "worktree" || len(c["members"].([]any)) != 4 || e.storedCrew("example-todo-app") == nil {
		t.Fatalf("todo app: %v", c)
	}
	if m := crewMember(c, 3); m["name"] != "tester" || m["agentId"] != "codex" || m["start"].(map[string]any)["member"] != "cli" {
		t.Fatalf("tester: %v", m)
	}
	// Edit it as the editor would, then seed again: the edit stays.
	for _, k := range []string{"id", "createdAt", "updatedAt"} {
		delete(c, k)
	}
	c["goal"] = "edited"
	// The test catalog has neither claude nor codex: save it with its own agents.
	for i := range c["members"].([]any) {
		crewMember(c, i)["agentId"] = "cat"
	}
	e.sendCrew("PUT", "/api/crews/example-todo-app", c, http.StatusOK)
	if resp, _ := e.do("DELETE", "/api/crews/example-docs-writer", adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	_, out = e.do("POST", "/api/crews/examples", adminToken, nil)
	if added := out["added"].([]any); len(added) != 1 || added[0] != "example-docs-writer" || len(out["skipped"].([]any)) != 3 {
		t.Fatalf("second seed: %v", out)
	}
	if got := e.crew("example-todo-app"); got["goal"] != "edited" {
		t.Fatalf("the edit was lost: %v", got)
	}
}
```

- [ ] **Step 10: Run it**

Run: `go test -race ./internal/api -run 'TestSeedExamplesOnce|TestCrewRoutesNeedAStore'`
Expected: PASS (the handler exists from Step 7; this step confirms the route end to end, through plan 1's `GET /api/crews/{id}` and the paged list).

- [ ] **Step 11: The client call and the "Load the examples" button**

`web/app/composables/useSessions.ts`, after `duplicateCrew` (`:314-316`):

```ts
    /**
     * Seeds the four example crews, as `conductor serve --examples` does: each is saved unless a file with its id exists, which is left
     * alone. Returns the ids added and skipped, in the examples' order. 503 `store_unavailable` without a data directory.
     */
    loadExampleCrews: () => request<{ added: string[]; skipped: string[] }>('/api/crews/examples', { method: 'POST' }),
```

`web/app/pages/crews/[[id]].vue`, script, before `function fail` (`:187`):

```ts
const seeding = ref(false)

/**
 * Seeds the example crews and opens the first one; what already existed is left as it is. The button shows on the empty page only, which
 * is page 1 with a total of 0 (refresh steps back from a page past the end), so reading the list again lists the examples there.
 */
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
    if (first) await router.push(`/crews/${encodeURIComponent(first)}`)
  } catch (e) {
    fail('Loading the examples failed', e)
  } finally {
    seeding.value = false
  }
}
```

Template, the `UEmpty` on the empty page (`:468-474`) becomes:

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

Headless: build (`make web-build && make build-go`) and start the test server as in the header (its data directory is new, so there are no crews), then `$PW/examples-check.js`:

```js
// Task 3: the empty Crews page loads the examples. Needs a data directory without crews. Exits 1 at the first failed check.
const { chromium } = require('playwright-core')
const assert = require('node:assert/strict')
const base = process.env.BASE || 'http://127.0.0.1:8099'
;(async () => {
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  try {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await ctx.addInitScript(() => localStorage.setItem('conductor.adminToken', 'dev-admin-token-change-me'))
    const page = await ctx.newPage()
    await page.goto(base + '/crews', { waitUntil: 'commit' })
    await page.waitForSelector('[data-crews-empty]')
    await page.getByRole('button', { name: 'Load the examples' }).click()
    await page.waitForURL(/\/crews\/example-todo-app$/)
    await page.waitForFunction(() => document.querySelectorAll('[data-crew-item]').length === 4)
    const items = await page.locator('[data-crew-item]').allInnerTexts()
    console.log('list', items.map((t) => t.split('\n')[0]))
    assert.deepEqual(items.map((t) => t.split('\n')[0]), ['Example: dependency upgrade', 'Example: docs writer', 'Example: test fixer', 'Example: todo app'], 'the four examples, by name')
    await page.waitForFunction(() => (document.querySelector('[aria-label="Crew name"]'))?.value === 'Example: todo app')
    const members = await page.locator('[data-crew-editor] [aria-label^="Agent for"]').count()
    assert.equal(members, 4, 'lead, core, cli and tester')
    // A second load adds nothing.
    const again = await page.evaluate(() => fetch('/api/crews/examples', { method: 'POST', headers: { Authorization: 'Bearer dev-admin-token-change-me' } }).then((r) => r.json()))
    assert.deepEqual(again, { added: [], skipped: ['example-todo-app', 'example-test-fixer', 'example-docs-writer', 'example-dependency-upgrade'] })
    await page.screenshot({ path: 'examples.png' })
    console.log('PASS examples-check')
  } finally {
    await browser.close()
  }
})().catch((e) => {
  console.error('FAIL', e.message)
  process.exit(1)
})
```

Run: `cd $PW && node examples-check.js`
Expected: exit 0, `PASS examples-check` (four items by name, the URL `/crews/example-todo-app`, four member rows, a second load adds nothing). Kill the server by pid and remove its data directory.

```bash
git add internal/crew/examples.go internal/crew/examples_test.go internal/crew/store.go internal/config/config.go internal/config/config_test.go internal/cli/serve.go internal/cli/serve_test.go internal/api/crews.go internal/api/crews_test.go internal/api/server.go web/app/composables/useSessions.ts 'web/app/pages/crews/[[id]].vue'
git commit -m "crews: example crews seeded once by --examples, the env or the Crews page"
```

---

### Task 4: CLI completion

**Files:**
- Create: `internal/cli/completion.go`, `internal/cli/completion_test.go`
- Modify: `internal/crew/store.go:28-30` (`ValidID` after `idPattern`), `internal/agents/home.go:67-85` (`ownedBy` → `ownedBy` + `otherOwner` + `CheckOwner`), `internal/agents/owner_test.go` (append), `internal/cli/up.go:3-19` (imports), `:37-46` (`crewsUsage`), `:148-214` (`crewsPage` … `runCrews` → `runCrews`, `runCrewIDs`, `crewLine`, `crewList`), `internal/cli/up_test.go` (`TestRunDispatchesUpAndCrews` at `:435-454`, and new tests), `internal/cli/root.go:13-31` (usage) and `:52-53` (dispatch)

**Interfaces:**
- Consumes: plan 1's `idPattern` (`internal/crew/store.go:30`); `ownedBy`, `geteuid`, `ownHome`, `writable` (`internal/agents/home.go`) and `FileOwner` (`owner_unix.go`, exported by the review's fix `5d67228`); plan 1's paging in `runCrews` (`crewsPage = 100`, `maxCrewsReply`, `doLimit`); `apiFlags`, `addAPIFlags`, `apiClient`, `parseInterspersed`, `homeOf`, `hookPayloads` (`internal/cli`); `agents.All()`; `version.String()`; test helpers `stubServer`, `runWith`, `crews`, `noSecret`, `secretToken`, `clearConductorEnv` (`up_test.go`, `serve_test.go`).
- Produces:
```go
// internal/crew/store.go
func ValidID(id string) bool // idPattern, exported for code that handles ids the store did not make
// internal/agents/home.go
func CheckOwner(path string) error                                    // nil when the caller owns path, or the system cannot say
func otherOwner(path string, fi fs.FileInfo, uid int) (who string, other bool) // ownedBy's rule, shared
// internal/cli/up.go
const idsTimeout = 2 * time.Second
func runCrewIDs(ctx context.Context, api apiFlags, stdout io.Writer) (int, error) // conductor crews --ids
type crewLine struct{ ID, Name string; Members []struct{ Name string } }
func (c *apiClient) crewList(ctx context.Context) ([]crewLine, error)            // pages through GET /api/crews, for crews and crews --ids
// internal/cli/completion.go
const completionMark = "# conductor completion"
func runCompletion(args []string, stdout, stderr io.Writer) (int, error) // zsh | bash | install
func completionSpec() []commandSpec
func zshScript(spec []commandSpec) string
func bashScript(spec []commandSpec) string
func completionLine(shell string) string
func installCompletionLine(path, shell string) (changed bool, err error)
```

- [ ] **Step 1: Write the failing test for `CheckOwner`** at the end of `internal/agents/owner_test.go` (it imports `errors`, `io/fs`, `os`, `path/filepath`, `strings`, `testing` already):

```go
// CheckOwner passes a file of one's own and refuses another user's, naming
// the file and saying nothing was written; a missing path is its own error;
// where the system cannot say who owns a file, it passes.
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
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := FileOwner(fi); !ok {
		t.Skip("this platform does not report who owns a file")
	}
	was := geteuid
	geteuid = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { geteuid = was })
	if err := CheckOwner(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "nothing written") {
		t.Fatalf("another user's file: %v", err)
	}
	// The own-home exception passes the process's home itself, never a file in it.
	t.Setenv("HOME", filepath.Dir(path))
	if err := CheckOwner(path); err == nil {
		t.Fatal("a file in the process's own home passed as if it were the home")
	}
}
```

- [ ] **Step 2: Run it, see it fail, implement**

Run: `go test ./internal/agents -run TestCheckOwner` → FAIL, `undefined: CheckOwner`. In `internal/agents/home.go`, replace `ownedBy` (`:67-85`, from its comment `// ownedBy refuses home, which fi describes` to its closing brace) with `ownedBy`, `otherOwner` and `CheckOwner`. `ownedBy` keeps its message and its tests; `CheckOwner` says what a completion install would and nothing about hooks:

```go
// ownedBy refuses home, which fi describes, unless the user uid owns it, or
// it is the process's own home (HOME) and the process may write it, as in a
// container whose home belongs to another uid. Root never passes the second
// way: under sudo, HOME may still name the invoking user's home, which root
// can always write.
func ownedBy(home string, fi fs.FileInfo, uid int) error {
	who, other := otherOwner(home, fi, uid)
	if !other {
		return nil
	}
	return fmt.Errorf("%s belongs to %s, and what Conductor wrote there would not: run it as %s, for example sudo -u %s conductor hooks install …", home, who, who, who)
}

// otherOwner names the owner of path, which fi describes, when ownedBy's rule
// refuses it for the user uid; other is false when the rule passes it,
// including where the system does not say who owns a file.
func otherOwner(path string, fi fs.FileInfo, uid int) (who string, other bool) {
	owner, ok := FileOwner(fi)
	if !ok || owner == uid {
		return "", false
	}
	if uid != 0 && ownHome(path) && writable(path) {
		return "", false
	}
	who = strconv.Itoa(owner)
	if u, err := user.LookupId(who); err == nil && u.Username != "" {
		who = u.Username
	}
	return who, true
}

// CheckOwner refuses path unless the user running conductor owns it, by the
// rule CheckHome holds a home to (ownedBy: its own-home exception passes the
// process's home directory itself, never a file in it); where the system does
// not say who owns a file, it passes. A missing path is its own error
// (fs.ErrNotExist). conductor completion install checks the rc file it
// appends to, or the directory it makes one in: under sudo, another user's
// ~/.zshrc is refused.
func CheckOwner(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if who, other := otherOwner(path, fi, geteuid()); other {
		return fmt.Errorf("%s belongs to %s, not to the user running conductor: nothing written; run it as %s, for example sudo -u %s conductor completion install", path, who, who, who)
	}
	return nil
}
```

Run again: PASS (and `go test ./internal/agents` stays green: `TestOwnedByRefusesAnotherUsersHome`, `TestOwnedByAcceptsTheProcesssOwnWritableHome`, `TestOwnHomeExceptionNeverCoversRoot` pin `ownedBy` unchanged).

- [ ] **Step 3: Write the failing tests for `crews --ids`** at the end of `internal/cli/up_test.go`:

```go
// conductor crews --ids prints ids only, one per line, paging through the
// list as conductor crews does, and leaves out anything not shaped like a
// crew id (crew.ValidID): the output is fed to a shell's completion.
func TestCrewsIDsPrintsOnlyWellFormedIDs(t *testing.T) {
	clearConductorEnv(t)
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
	if !slices.Equal(pages, []string{"offset=0&limit=100", "offset=5&limit=100"}) {
		t.Fatalf("pages: %v", pages)
	}
}

// Without a token, without a server to reach, or refused, --ids prints
// nothing and exits 0: a completion must never put an error on the command
// line.
func TestCrewsIDsIsSilentWhenItCannotAsk(t *testing.T) {
	clearConductorEnv(t)
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

// A server that does not answer is given idsTimeout in all, not a request's
// timeout: completion does not hang the shell.
func TestCrewsIDsGivesUpQuickly(t *testing.T) {
	clearConductorEnv(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	start := time.Now()
	code, stdout, _, err := crews(t, "--server", srv.URL, "--token", secretToken, "--ids")
	if code != 0 || err != nil || stdout != "" || time.Since(start) > idsTimeout+time.Second {
		t.Fatalf("exit %d %v %q after %s", code, err, stdout, time.Since(start))
	}
}
```

- [ ] **Step 4: Run them, see them fail, implement `--ids`**

Run: `go test ./internal/cli -run 'TestCrewsIDs'` → FAIL to compile, `undefined: idsTimeout` (neither the flag nor its budget exists yet).

In `internal/crew/store.go`, after `idPattern` (`:28-30`):

```go
// ValidID reports whether id has the shape of a crew ID (idPattern), for code
// that handles IDs the store did not make: conductor crews --ids prints only
// these, since a shell's completion reads what it prints.
func ValidID(id string) bool { return idPattern.MatchString(id) }
```

In `internal/cli/up.go`: the imports (`:3-19`) gain, after the standard library and an empty line,

```go
	"github.com/phenixrizen/conductor/internal/crew"
```

the `conductor crews` lines of `crewsUsage` (`:41-42`) become:

```
  conductor crews [--server URL] [--token T] [--ids]
      list the saved crews: id, name and members; with --ids the ids only,
      one per line, for shell completion (nothing, and exit 0, when the
      server cannot be asked)
```

and everything from `// crewsPage is how many crews conductor crews asks for at a time.` (`:148`) to the end of `runCrews` (`:214`) is replaced by the block below: the paging loop of plan 1 moves, as it is (its comment on offset paging included), into `crewList`, which `runCrews` and `runCrewIDs` both call. `--ids` gives the whole listing `idsTimeout` (the requests' own 30 s are cut to it by the context) and prints only what `crew.ValidID` accepts:

```go
// crewsPage is how many crews conductor crews asks for at a time.
const crewsPage = 100

// idsTimeout bounds all of conductor crews --ids: a completion that waits is
// worse than one that offers nothing.
const idsTimeout = 2 * time.Second

// runCrews lists the saved crews, one per line, or with --ids their ids.
func runCrews(ctx context.Context, args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("crews", flag.ContinueOnError)
	fs.SetOutput(stderr)
	api := addAPIFlags(fs)
	ids := fs.Bool("ids", false, "print the crew ids only, one per line, for shell completion: silent and exit 0 when the server cannot be reached or the token is missing")
	fs.Usage = func() {
		fmt.Fprint(stderr, crewsUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2, errors.New("crews: takes no arguments")
	}
	if *ids {
		return runCrewIDs(ctx, api, stdout)
	}
	c, err := api.client()
	if err != nil {
		return 2, err
	}
	all, err := c.crewList(ctx)
	if err != nil {
		return 1, err
	}
	if len(all) == 0 {
		fmt.Fprintln(stdout, "no crews")
		return 0, nil
	}
	for _, cr := range all {
		names := make([]string, len(cr.Members))
		for i, m := range cr.Members {
			names[i] = m.Name
		}
		fmt.Fprintf(stdout, "%s\t%s\t%d members: %s\n", cr.ID, cr.Name, len(names), strings.Join(names, ", "))
	}
	return 0, nil
}

// runCrewIDs prints the saved crews' ids, one per line, for shell
// completion: nothing and exit 0 when no client can be made (no token) or the
// server does not answer within idsTimeout, and never an error, since the
// output lands on a command line. An id not shaped like a crew's
// (crew.ValidID) is left out.
func runCrewIDs(ctx context.Context, api apiFlags, stdout io.Writer) (int, error) {
	c, err := api.client()
	if err != nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, idsTimeout)
	defer cancel()
	all, err := c.crewList(ctx)
	if err != nil {
		return 0, nil
	}
	for _, cr := range all {
		if crew.ValidID(cr.ID) {
			fmt.Fprintln(stdout, cr.ID)
		}
	}
	return 0, nil
}

// crewLine is a crew as conductor crews reads it from the list.
type crewLine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Members []struct {
		Name string `json:"name"`
	} `json:"members"`
}

// crewList reads every saved crew from GET /api/crews, crewsPage at a time,
// each page at most maxCrewsReply bytes. As in runUp, unknown fields are
// ignored on purpose. A reply without total (an older server) is one page.
func (c *apiClient) crewList(ctx context.Context) ([]crewLine, error) {
	var all []crewLine
	// Pages by offset and limit, with no snapshot across them: a crew made
	// or deleted between two pages shifts the ones after it, so one may be
	// listed twice or not at all. That is inherent to offset paging; a run
	// again lists them as they are.
	for offset := 0; ; {
		var reply struct {
			Crews []crewLine `json:"crews"`
			Total int        `json:"total"`
		}
		if err := c.doLimit(ctx, http.MethodGet, fmt.Sprintf("/api/crews?offset=%d&limit=%d", offset, crewsPage), &reply, maxCrewsReply); err != nil {
			return nil, err
		}
		all = append(all, reply.Crews...)
		offset += len(reply.Crews)
		if len(reply.Crews) == 0 || offset >= reply.Total {
			return all, nil
		}
	}
}
```

Run: `go test -race ./internal/cli -run 'TestCrews|TestUp'` → PASS (plan 1's `TestCrewsList`, `TestCrewsPagesThroughTheList`, `TestCrewsOversizedReply`, `TestCrewsEmpty`, `TestCrewsErrors` pin the moved loop).

- [ ] **Step 5: Write the failing completion tests** `internal/cli/completion_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fakeConductor is a conductor that answers `crews --ids` with ids, for the
// scripts to run as the command typed (words[1] / COMP_WORDS[0]).
func fakeConductor(t *testing.T, ids ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "conductor")
	body := "#!/bin/sh\n[ \"$1\" = crews ] && [ \"$2\" = --ids ] && cat <<'IDS'\n" + strings.Join(ids, "\n") + "\nIDS\nexit 0\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// runCompletionWith runs `conductor completion args...` and returns what it printed.
func runCompletionWith(t *testing.T, args ...string) (int, string, string, error) {
	t.Helper()
	return runWith(t, func(_ context.Context, a []string, o, e io.Writer) (int, error) { return runCompletion(a, o, e) }, args...)
}

// scriptFile writes the completion script for shell to a file.
func scriptFile(t *testing.T, shell string) string {
	t.Helper()
	code, stdout, stderr, err := runCompletionWith(t, shell)
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
// the candidates, one per line. dir is the working directory, for file
// completion.
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
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
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
			bin := fakeConductor(t, "alpha", "beta")
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
			if got := complete(t, sh, script, dir, bin, "notify", "--"); !has(got, "--claude-hook", "--goose-hook", "--quiet") {
				t.Fatalf("notify flags: %v", got)
			}
			if sh == "bash" {
				if got := complete(t, sh, script, dir, bin, "up", "a"); !slices.Equal(got, []string{"alpha"}) {
					t.Fatalf("bash filters by the typed prefix: %v", got)
				}
			}
		})
	}
}

// Review Focus 2: whatever `crews --ids` prints reaches the candidates as
// data. A line holding shell syntax is never run, and a typed prefix with
// pattern characters is compared as text.
func TestCompletionNeverRunsWhatTheServerSends(t *testing.T) {
	for _, sh := range []string{"zsh", "bash"} {
		t.Run(sh, func(t *testing.T) {
			script := scriptFile(t, sh)
			dir := t.TempDir()
			marker := filepath.Join(dir, "ran")
			bin := fakeConductor(t, "alpha", "$(touch "+marker+")", "`touch "+marker+"`", "a b", "*")
			got := complete(t, sh, script, dir, bin, "up", "")
			if _, err := os.Stat(marker); err == nil {
				t.Fatalf("an id was run as a command: %v", got)
			}
			if !slices.Contains(got, "alpha") || !slices.Contains(got, "*") || slices.Contains(got, "x.json") {
				t.Fatalf("candidates: %q", got)
			}
			if sh == "bash" {
				if got := complete(t, sh, script, dir, bin, "up", "*"); !slices.Equal(got, []string{"*"}) {
					t.Fatalf("a typed * is text, not a pattern: %q", got)
				}
			}
		})
	}
}

// The scripts know every flag the commands define, and no other: the table
// in completionSpec is checked against each command's -h.
func TestCompletionSpecMatchesEveryFlag(t *testing.T) {
	ctx := t.Context()
	helps := map[string][][]string{
		"serve": {{"serve", "-h"}}, "host": {{"host", "-h"}}, "notify": {{"notify", "-h"}}, "up": {{"up", "-h"}}, "crews": {{"crews", "-h"}},
		"hooks": {{"hooks", "install", "-h"}, {"hooks", "status", "-h"}}, "completion": {{"completion", "install", "-h"}},
	}
	flagLine := regexp.MustCompile(`(?m)^  -([a-z][a-z0-9-]*)`)
	for _, c := range completionSpec() {
		want := map[string]bool{}
		for _, args := range helps[c.name] {
			var stderr bytes.Buffer
			if code, err := Run(ctx, args, strings.NewReader(""), io.Discard, &stderr); code != 0 || err != nil {
				t.Fatalf("%v: exit %d %v", args, code, err)
			}
			for _, m := range flagLine.FindAllStringSubmatch(stderr.String(), -1) {
				want["--"+m[1]] = true
			}
		}
		got := map[string]bool{}
		for _, f := range c.flags {
			got[f.name] = true
		}
		if !maps.Equal(got, want) {
			t.Errorf("%s: the table has %v, the command %v", c.name, slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
		}
	}
}

// conductor completion prints a script for zsh or bash and refuses the rest.
func TestCompletionUsage(t *testing.T) {
	if code, out, _, err := runCompletionWith(t, "zsh"); code != 0 || err != nil || !strings.HasPrefix(out, "#compdef conductor\n") || !strings.Contains(out, "compdef _conductor conductor") {
		t.Fatalf("zsh: %d %v\n%s", code, err, out)
	}
	if code, out, _, err := runCompletionWith(t, "bash"); code != 0 || err != nil || !strings.Contains(out, "complete -F _conductor conductor") {
		t.Fatalf("bash: %d %v\n%s", code, err, out)
	}
	for _, args := range [][]string{{}, {"fish"}, {"zsh", "extra"}} {
		if code, _, _, err := runCompletionWith(t, args...); code != 2 || err == nil {
			t.Fatalf("%v: %d %v", args, code, err)
		}
	}
}

// completion install appends one marked line to the shell's rc file, once;
// the shell comes from $SHELL unless --shell says; a missing file is made.
func TestCompletionInstall(t *testing.T) {
	clearConductorEnv(t)
	home := os.Getenv("HOME")
	t.Setenv("SHELL", "/bin/zsh")
	install := func(args ...string) (int, string, string, error) {
		return runCompletionWith(t, append([]string{"install"}, args...)...)
	}
	rc := filepath.Join(home, ".zshrc")
	code, out, _, err := install()
	if code != 0 || err != nil || !strings.Contains(out, rc) {
		t.Fatalf("first: %d %v\n%s", code, err, out)
	}
	want := completionLine("zsh") + "\n"
	if b, _ := os.ReadFile(rc); string(b) != want {
		t.Fatalf("rc:\n%s", b)
	}
	if fi, _ := os.Stat(rc); fi.Mode().Perm() != 0o600 {
		t.Fatalf("a new rc file is %v", fi.Mode().Perm())
	}
	code, out, _, err = install()
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
	if code, _, _, err := install("--shell", "bash", "--rc", custom); code != 0 || err != nil {
		t.Fatalf("custom: %d %v", code, err)
	}
	if b, _ := os.ReadFile(custom); string(b) != "alias l='ls'\n"+completionLine("bash")+"\n" {
		t.Fatalf("custom rc:\n%s", b)
	}
	t.Setenv("SHELL", "/usr/bin/fish")
	if code, _, _, err := install(); code != 2 || err == nil {
		t.Fatalf("fish: %d %v", code, err)
	}
}
```

- [ ] **Step 6: Run them to see them fail**

Run: `go test ./internal/cli -run TestCompletion`
Expected: FAIL to compile, `undefined: runCompletion`, `completionSpec`, `completionLine`.

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
	"io/fs"
	"os"
	"path/filepath"
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

// commandSpec is a subcommand as the scripts know it: its flags (those of
// the FlagSet its run function makes; TestCompletionSpecMatchesEveryFlag
// holds the two together), the words its first argument may be, the words its
// second may be after each first, and whether its argument is a crew id.
type commandSpec struct {
	name   string
	flags  []flagSpec
	words  []string
	after  map[string][]string
	crewID bool
}

// completionSpec lists the subcommands of root.go with the flags their run
// functions define. The notify payload flags come from hookPayloads and the
// adapters from agents.All, so those follow on their own.
func completionSpec() []commandSpec {
	levels := []string{"debug", "info", "warn", "error"}
	api := func(more ...flagSpec) []flagSpec {
		return append([]flagSpec{{name: "--server", kind: flagValue}, {name: "--token", kind: flagValue}}, more...)
	}
	notifyFlags := []flagSpec{
		{name: "--state", kind: flagEnum, values: []string{"needs_input", "working", "done", "clear"}},
		{name: "--message", kind: flagValue},
		{name: "--event", kind: flagEnum, values: []string{"progress", "artifact", "handoff", "tool_use", "tool_denied", "error"}},
		{name: "--url", kind: flagValue},
		{name: "--to", kind: flagValue},
		{name: "--tool", kind: flagValue},
		{name: "--codex"},
	}
	for _, h := range hookPayloads {
		notifyFlags = append(notifyFlags, flagSpec{name: "--" + h.name})
	}
	notifyFlags = append(notifyFlags, flagSpec{name: "--quiet"})
	adapters := []string{"all"}
	for _, a := range agents.All() {
		adapters = append(adapters, a.ID)
	}
	return []commandSpec{
		{name: "serve", flags: []flagSpec{{name: "--config", kind: flagFile}, {name: "--listen", kind: flagValue}, {name: "--dev"}, {name: "--log-level", kind: flagEnum, values: levels}, {name: "--examples"}}},
		{name: "host", flags: []flagSpec{{name: "--server", kind: flagValue}, {name: "--token", kind: flagValue}, {name: "--name", kind: flagValue}, {name: "--host-name", kind: flagValue}, {name: "--agent", kind: flagValue}, {name: "--cwd", kind: flagFile}, {name: "--relay-only"}, {name: "--no-local"}, {name: "--stun", kind: flagValue}, {name: "--scrollback", kind: flagValue}, {name: "--file-view", kind: flagEnum, values: []string{"view", "control", "off"}}, {name: "--signal-pattern", kind: flagValue}, {name: "--log-level", kind: flagEnum, values: levels}}},
		{name: "notify", flags: notifyFlags},
		{name: "up", flags: api(flagSpec{name: "--open"}), crewID: true},
		{name: "crews", flags: api(flagSpec{name: "--ids"})},
		{name: "hooks", flags: []flagSpec{{name: "--home", kind: flagFile}, {name: "--data-dir", kind: flagFile}}, words: []string{"install", "status"}, after: map[string][]string{"install": adapters}},
		{name: "skill"},
		{name: "completion", flags: []flagSpec{{name: "--shell", kind: flagEnum, values: []string{"zsh", "bash"}}, {name: "--rc", kind: flagFile}}, words: []string{"zsh", "bash", "install"}},
		{name: "version"},
	}
}

func commandNames(spec []commandSpec) string {
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
// CURRENT that hands compadd what fits, bound with compdef once compinit has
// run. The crew ids come from `crews --ids` run as the command typed
// (words[1]), so a conductor not on PATH still completes; they reach compadd
// as array elements, never as shell text.
func zshScript(spec []commandSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#compdef conductor\n# conductor completion for zsh, generated by conductor %s. Load it after compinit with: source <(conductor completion zsh)\n", version.String())
	b.WriteString("_conductor() {\n  local cur=${words[CURRENT]} cmd=${words[2]} sub=${words[3]} prev=${words[CURRENT-1]}\n")
	fmt.Fprintf(&b, "  if (( CURRENT == 2 )); then compadd -- %s; return; fi\n", commandNames(spec))
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
			fmt.Fprintf(&b, "    %s) local -a ids; ids=(${(f)\"$(\"${words[1]}\" crews --ids 2>/dev/null)\"}); (( ${#ids} )) && compadd -- \"${ids[@]}\" ;;\n", c.name)
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
	b.WriteString("  esac\n}\nif (( $+functions[compdef] )); then compdef _conductor conductor; fi\n")
	return b.String()
}

// bashScript is the bash completion script for spec, the same shape on
// COMP_WORDS, COMP_CWORD and COMPREPLY; compgen -W filters the script's own
// words by the typed prefix. The crew ids come from `crews --ids` run as
// COMP_WORDS[0] and are never expanded: a read loop compares each with the
// typed prefix as a string and adds it to COMPREPLY as it is.
func bashScript(spec []commandSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# conductor completion for bash, generated by conductor %s. Load it with: source <(conductor completion bash)\n", version.String())
	b.WriteString("_conductor() {\n  local cur=${COMP_WORDS[COMP_CWORD]} cmd=${COMP_WORDS[1]} sub=${COMP_WORDS[2]} prev=${COMP_WORDS[COMP_CWORD-1]}\n  COMPREPLY=()\n")
	fmt.Fprintf(&b, "  if (( COMP_CWORD == 1 )); then COMPREPLY=($(compgen -W %q -- \"$cur\")); return; fi\n", commandNames(spec))
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
			fmt.Fprintf(&b, "    %s)\n      local id\n      while IFS= read -r id; do [[ $id == \"$cur\"* ]] && COMPREPLY+=(\"$id\"); done < <(\"${COMP_WORDS[0]}\" crews --ids 2>/dev/null) ;;\n", c.name)
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
// file must be the caller's own (agents.CheckOwner), and a missing file is
// made, 0600, only in a directory of the caller's own: under sudo, another
// user's rc file or home is refused.
func installCompletionLine(path, shell string) (bool, error) {
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := agents.CheckOwner(filepath.Dir(path)); err != nil {
			return false, err
		}
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

`internal/cli/root.go`: the dispatch case after `case "skill":` (`:52-53`):

```go
	case "completion":
		return runCompletion(args[1:], stdout, stderr)
```

and in `usage` the `conductor crews` line (`:22-23`) and two new entries after `conductor skill` (`:27`):

```
  conductor crews [--server URL] [--token T] [--ids]
                               list the saved crews (--ids: ids only, for completion)
```

```
  conductor completion zsh|bash
                               print a shell completion script
  conductor completion install [--shell zsh|bash] [--rc FILE]
                               add the line that loads it to ~/.zshrc or ~/.bashrc
```

In `internal/cli/up_test.go`, `TestRunDispatchesUpAndCrews` (`:435-454`) checks both: its help list becomes `[]string{"conductor up <crew-id>", "conductor crews", "--ids", "conductor completion zsh|bash", "conductor completion install"}`, and after the `up` case, before the closing brace:

```go
	out.Reset()
	if code, err := Run(t.Context(), []string{"completion", "zsh"}, nil, &out, &errOut); code != 0 || err != nil || !strings.HasPrefix(out.String(), "#compdef conductor\n") {
		t.Fatalf("completion: exit %d %v\n%s", code, err, out.String())
	}
```

- [ ] **Step 8: Run the whole CLI package**

Run: `go test -race -count=1 ./internal/cli ./internal/agents ./internal/crew && make lint`
Expected: PASS and clean; the shell tests skip where zsh or bash is missing and say so (this box has both: they must run). `TestCompletionSpecMatchesEveryFlag` fails if a command gains a flag the table lacks; add it to `completionSpec` then.

- [ ] **Step 9: Try it in a real shell** (reported, not automated)

```bash
go run ./cmd/conductor completion zsh > $PW/c.zsh && zsh -ic 'source "$0"; (( $+_comps[conductor] )) && echo bound' $PW/c.zsh
go run ./cmd/conductor completion bash > $PW/c.bash && bash -ic 'source "$0"; complete -p conductor' $PW/c.bash
```

Expected: `bound` (zsh, after the user's compinit) and `complete -F _conductor conductor`, with no error from the scripts (a line from the user's own rc files is theirs). Interactive TAB behaviour is the spec's "Open verification (round 3)" item.

- [ ] **Step 10: Commit**

```bash
git add internal/cli/completion.go internal/cli/completion_test.go internal/cli/up.go internal/cli/up_test.go internal/cli/root.go internal/crew/store.go internal/agents/home.go internal/agents/owner_test.go
git commit -m "cli: completion scripts for zsh and bash, crews --ids, completion install"
```

---

### Task 5: The sidebar rail

**Files:**
- Create: `web/app/utils/sidebar.ts`, `web/app/utils/sidebar.test.ts`, `web/app/components/SidebarRail.vue`
- Modify: `web/app/composables/useSidebar.ts` (whole file), `web/app/layouts/default.vue:24` (after `list`), `:93` and `:102` (the `/` and `alt_s` shortcuts), `:112-178` (the group's opening tag and the sidebar), `web/app/composables/useShortcuts.ts:23` (the `meta+B` row's label)
- Delete: `web/app/components/SidebarReveal.vue`, and its `<template #leading><SidebarReveal /></template>` blocks in `web/app/pages/index.vue:35-37`, `events.vue:83-85`, `agents.vue:127-129`, `crews/[[id]].vue:398-400`, `sessions/[id].vue:274-276`, `carousel.vue:332-334` and `components/CrewRunHeader.vue:91-93`; in `pages/wall.vue:187` only the `<SidebarReveal />` line (the back button in that `#leading` stays)

**Interfaces:**
- Consumes: `groupSessions`, `SessionGroupKey` (`web/app/utils/sessions.ts`), `SessionInfo`, `useAttention`, `useEvents().routes`, `useLaunchModal`, `SessionAvatar`, `SessionSidebar` (its `focusFilter`, `data-session-list`), the `crewRunNames` state and `sidebarRun`/`sidebarRunName` that `default.vue` keeps (plan 1's `RunNameAsks` code stays as it is); Nuxt UI `UDashboardGroup` (`persistent`: false turns off its cookie for every resizable in the group, the sidebar's width and collapsed state), `UDashboardSidebar` (`collapsible`, `v-model:collapsed`, slot prop `collapsed`, root `min-w-16`), `UNavigationMenu` (`collapsed`, `tooltip`), `UTooltip`/`UPopover` (`content: { side }`).
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
export interface RailGroup { key: string; runId?: string; label?: string; items: RailItem[] } // key: unique, `${section}:${runId ?? ''}`
export function railGroups(sessions: readonly SessionInfo[], runNames?: Readonly<Record<string, string>>): RailGroup[]
// web/app/composables/useSidebar.ts
export function useSidebar(): { mode: Ref<SidebarMode>; rail: ComputedRef<boolean>; collapse(): void; expand(): void; toggle(): void }
// web/app/components/SidebarRail.vue — props { runId?: string; runName?: string }, emits 'search'; data-rail, data-rail-session
// web/app/layouts/default.vue — data-sidebar-tools on the utility buttons, data-rail-expand on the rail's expand button
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
  ({ id: 'id', name: 'name', kind: 'server', agentId: 'claude', command: [], cwd: '/w', status: 'running', cols: 80, rows: 24, viewers: 0, createdAt: '2026-10-01T09:00:00Z', ...over }) as SessionInfo

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
    // The new key wins over an old one left beside it.
    expect(readSidebarMode(memory({ [SIDEBAR_KEY]: 'full', [LEGACY_SIDEBAR_KEY]: '1' }))).toBe('full')
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
    const s = memory()
    writeSidebarMode(s, 'rail')
    expect(readSidebarMode(s)).toBe('rail')
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

  it('gives every group its own key, a run in two sections included', () => {
    const list = [
      session({ id: 'a', crew: { runId: 'r1', crewId: 'c', member: 'a' }, attention: { state: 'needs_input', since: '2026-10-01T09:06:00Z' } }),
      session({ id: 'b', crew: { runId: 'r1', crewId: 'c', member: 'b' } }),
      session({ id: 'c', crew: { runId: 'r1', crewId: 'c', member: 'c' }, status: 'exited' }),
      session({ id: 'd' }),
    ]
    const keys = railGroups(list).map((g) => g.key)
    expect(keys).toEqual(['needs:r1', 'running:', 'running:r1', 'exited:r1'])
    expect(new Set(keys).size).toBe(keys.length)
  })

  it('shows a starting session with the idle dot', () => {
    expect(railGroups([session({ status: 'starting' })])[0]!.items[0]!.dot).toBe('idle')
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `npm --prefix web test -- sidebar`
Expected: FAIL, the module `./sidebar` cannot be found.

- [ ] **Step 3: Implement `web/app/utils/sidebar.ts`**

The groups carry a `key` of their own: one run can have a member that needs you and another that runs, so the run id alone is not unique in the list.

```ts
import type { SessionInfo } from '~/composables/useSessions'
import { groupSessions, type SessionGroupKey } from './sessions'

/** The desktop sidebar's two modes: everything, or an icon rail. */
export type SidebarMode = 'full' | 'rail'
/** The one place the mode is kept: localStorage, per browser. Nuxt UI's own collapse cookie is off (the layout's UDashboardGroup). */
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
  /** Unique in the list: a run's members can sit in several sections (one needs you, one runs), each its own group. */
  key: string
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
  const section = (key: SessionGroupKey, list: SessionInfo[], dot: (s: SessionInfo) => RailDot) => {
    const loose = list.filter((s) => !s.crew)
    if (loose.length) out.push({ key: `${key}:`, items: loose.map((s) => item(s, dot(s))) })
    const byRun = new Map<string, SessionInfo[]>()
    for (const s of list) if (s.crew) byRun.set(s.crew.runId, [...(byRun.get(s.crew.runId) ?? []), s])
    for (const [runId, members] of byRun) out.push({ key: `${key}:${runId}`, runId, label: runNames[runId] || members[0]?.crew?.crewId, items: members.map((s) => item(s, dot(s))) })
  }
  section('needs', g.needs, () => 'needs')
  section('running', g.running, (s) => (s.status === 'running' ? 'running' : 'idle'))
  section('exited', g.exited, () => 'exited')
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
 * The desktop sidebar's mode: full, or the icon rail. Kept in localStorage
 * alone (the layout turns Nuxt UI's own collapse cookie off and binds the
 * sidebar's collapsed state to this), per browser, so a wall left on a spare
 * monitor keeps its rail after a reload; an old "hidden" choice reads as the
 * rail. On narrow screens the sidebar is a slideover and the mode is ignored.
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
 * the pages, the utility buttons and the expand button are the layout's
 * header and footer.
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
      <template v-for="g in groups" :key="g.key">
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

- [ ] **Step 7: Rework the layout** (`web/app/layouts/default.vue`, as plan 1 left it)

Script, after `const list = useTemplateRef<{ focusFilter: () => void }>('list')` (`:24`):

```ts
// The sidebar's collapsed state is the rail, and useSidebar (localStorage) is
// its one source: the group's own persistence is off (persistent false below),
// so no Nuxt UI cookie can bring back another state on load.
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

In `defineShortcuts`, `'/': () => list.value?.focusFilter(),` (`:93`) becomes `'/': focusFilter,` and `alt_s: { ...inTerminal, handler: () => list.value?.focusFilter() },` (`:102`) becomes `alt_s: { ...inTerminal, handler: focusFilter },`; the `meta_b` and `alt_b` rows stay (`sidebar.toggle()` now toggles rail and full).

Template: from `<UDashboardGroup>` (`:112`) to `</UDashboardSidebar>` (`:178`) becomes the block below. `:persistent="false"` on the group is what makes localStorage the one source: Nuxt UI would otherwise keep the collapsed state (and the width) in a cookie and, on load, push a collapsed cookie into the model over ours. The sidebar's width is then the default 18 % after a reload; dragging it still works for the visit. The footer keeps every utility button in both modes, in a row on the full sidebar and stacked, tooltips to the right, on the rail:

```vue
  <UDashboardGroup :persistent="false">
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
        <!-- The same utility buttons in both modes: in a row on the full sidebar, stacked with their tooltips to the right on the rail. -->
        <div class="flex items-center px-1 pt-1" :class="collapsed ? 'flex-col gap-1' : 'justify-between'" data-sidebar-tools>
          <UPopover :content="collapsed ? { side: 'right' } : undefined">
            <UTooltip text="Alerts" :content="collapsed ? { side: 'right' } : undefined">
              <UButton :icon="alerts.settings.value.notifications ? 'i-lucide-bell-ring' : 'i-lucide-bell'" color="neutral" variant="ghost" size="sm" aria-label="Alerts" />
            </UTooltip>
            <template #content>
              <div class="p-3 flex flex-col gap-3 w-64">
                <p class="text-xs text-muted">When a session needs input, and for events routed to Browser on the Events page:</p>
                <USwitch :model-value="alerts.settings.value.notifications" label="Browser notification" :description="alerts.permission.value === 'denied' ? 'Blocked by the browser' : undefined" :disabled="alerts.permission.value === 'denied' || alerts.permission.value === 'unsupported'" @update:model-value="alerts.setNotifications" />
                <USwitch :model-value="alerts.settings.value.chime" label="Chime" @update:model-value="alerts.setChime" />
                <p class="text-xs text-muted">The tab title and favicon always show the count.</p>
              </div>
            </template>
          </UPopover>
          <UPopover v-model:open="nameOpen" :content="collapsed ? { side: 'right' } : undefined" @update:open="(o: boolean) => o && (nameDraft = identity.name.value)">
            <UTooltip :text="identity.name.value ? `You are ${identity.name.value}` : 'Set your name'" :content="collapsed ? { side: 'right' } : undefined">
              <UButton :icon="identity.name.value ? 'i-lucide-user-round-check' : 'i-lucide-user-round'" color="neutral" variant="ghost" size="sm" aria-label="Your name" />
            </UTooltip>
            <template #content>
              <form class="p-3 flex flex-col gap-2 w-64" @submit.prevent="saveName">
                <p class="text-xs text-muted">Shown to others on a session. Defaults to the server's user; a label, not a login.</p>
                <UInput v-model="nameDraft" placeholder="Your name" size="sm" maxlength="40" />
                <UButton type="submit" label="Save" size="sm" class="self-end" />
              </form>
            </template>
          </UPopover>
          <UTooltip text="Keyboard shortcuts" :kbds="['?']" :content="collapsed ? { side: 'right' } : undefined">
            <UButton icon="i-lucide-keyboard" color="neutral" variant="ghost" size="sm" aria-label="Keyboard shortcuts" @click="shortcuts.show()" />
          </UTooltip>
          <UTooltip :text="hasToken ? 'Admin token set' : 'Set admin token'" :content="collapsed ? { side: 'right' } : undefined">
            <UButton :icon="hasToken ? 'i-lucide-key-round' : 'i-lucide-lock'" :color="hasToken ? 'neutral' : 'warning'" variant="ghost" size="sm" :aria-label="hasToken ? 'Admin token set' : 'Set admin token'" @click="showToken = true" />
          </UTooltip>
          <UTooltip text="Toggle theme" :content="collapsed ? { side: 'right' } : undefined">
            <UButton icon="i-lucide-sun-moon" color="neutral" variant="ghost" size="sm" aria-label="Toggle theme" @click="toggleTheme" />
          </UTooltip>
          <UTooltip v-if="hasToken" text="Forget token" :content="collapsed ? { side: 'right' } : undefined">
            <UButton icon="i-lucide-log-out" color="neutral" variant="ghost" size="sm" aria-label="Forget token" @click="clear()" />
          </UTooltip>
          <UTooltip v-if="collapsed" text="Expand sidebar" :kbds="['meta', 'B']" :content="{ side: 'right' }">
            <UButton icon="i-lucide-panel-left-open" color="neutral" variant="ghost" size="sm" aria-label="Expand sidebar" class="hidden lg:inline-flex" data-rail-expand @click="sidebar.expand()" />
          </UTooltip>
        </div>
      </template>
    </UDashboardSidebar>
```

The slot prop `collapsed` is what decides, not `sidebar.rail` directly: in the slideover (below `lg`) Nuxt UI passes `collapsed: false`, so narrow screens keep the full sidebar. The collapsed root keeps Nuxt UI's `min-w-16` (64 px): that is the rail's width; nothing else sets it.

Then delete `web/app/components/SidebarReveal.vue` and its uses listed under Files: in the seven navbars whose `#leading` holds nothing else, the three lines

```vue
        <template #leading>
          <SidebarReveal />
        </template>
```

(indented as each file has it), and in `wall.vue` the one line `<SidebarReveal />`. `UDashboardNavbar` renders its own toggle on narrow screens (its `toggle` prop defaults to true), so nothing replaces it there. In `useShortcuts.ts` the Everywhere row (`:23`) becomes `{ keys: ['meta', 'B'], label: 'Collapse the sidebar to the rail, or expand it (Alt+B in a terminal)' },`.

- [ ] **Step 8: Typecheck and vitest**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: clean; `grep -rn "SidebarReveal\|sidebar.hidden\|sidebar.hide\|sidebar.show" web/app` finds only `LEGACY_SIDEBAR_KEY`'s value in `utils/sidebar.ts`.

- [ ] **Step 9: Headless check** — start the test server as in the header (after `make web-build && make build-go`), launch one `shell` session with `curl -s -X POST -H 'Authorization: Bearer dev-admin-token-change-me' -H 'Content-Type: application/json' -d '{"agentId":"shell"}' http://127.0.0.1:8099/api/sessions`, then `$PW/rail-check.js`:

```js
// Task 5: the rail, its migration from the hidden flag, its persistence, and the slideover on a phone. Exits 1 at the first failed check.
const { chromium } = require('playwright-core')
const assert = require('node:assert/strict')
const base = process.env.BASE || 'http://127.0.0.1:8099'
;(async () => {
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  try {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    // Review Focus 4: a browser from before the rail, with the old hidden flag (set once, before the first load).
    await ctx.addInitScript(() => {
      localStorage.setItem('conductor.adminToken', 'dev-admin-token-change-me')
      if (!sessionStorage.getItem('seeded')) {
        sessionStorage.setItem('seeded', '1')
        localStorage.setItem('conductor.sidebar.hidden', '1')
      }
    })
    const page = await ctx.newPage()
    const keys = () => page.evaluate(() => [localStorage.getItem('conductor.sidebar.mode'), localStorage.getItem('conductor.sidebar.hidden')])
    await page.goto(base + '/agents', { waitUntil: 'commit' })
    await page.waitForSelector('[data-rail]')
    const root = page.locator('[data-slot="root"][data-collapsed="true"]').first()
    const width = (await root.boundingBox())?.width
    console.log('rail width', width)
    assert.equal(Math.round(width ?? 0), 64, 'the rail is 64 px wide')
    assert.deepEqual(await keys(), ['rail', null], 'the hidden flag became the rail and is gone')
    await page.waitForSelector('[data-rail-session]')
    const avatars = await page.locator('[data-rail-session]').count()
    console.log('avatars', avatars)
    assert.ok(avatars >= 1, 'a session shows as an avatar')
    const footerLinks = await page.locator('[data-slot="footer"] nav a').count()
    const tools = await page.locator('[data-sidebar-tools] button').count()
    console.log('page icons', footerLinks, 'tools', tools)
    assert.equal(footerLinks, 5, 'the five pages as icons')
    assert.equal(tools, 7, 'alerts, name, shortcuts, token, theme, forget token and expand, stacked')
    await page.locator('[data-rail-session]').first().click()
    await page.waitForURL(/\/sessions\//)
    // Back to a page without a terminal, where the plain chord reaches the page.
    await page.goto(base + '/agents', { waitUntil: 'commit' })
    await page.waitForSelector('[data-rail]')
    await page.locator('body').click({ position: { x: 900, y: 600 } })
    await page.keyboard.press('Control+b') // meta_b is Ctrl+B off a Mac
    await page.waitForSelector('[data-session-list]')
    assert.deepEqual(await keys(), ['full', null], 'meta+B expands, and says so in the one key')
    await page.reload({ waitUntil: 'commit' })
    await page.waitForSelector('[data-session-list]') // the choice survives a reload
    await page.locator('body').click({ position: { x: 900, y: 600 } })
    await page.keyboard.press('Control+b')
    await page.waitForSelector('[data-rail]')
    await page.locator('[data-rail] [aria-label="Filter sessions"]').click()
    await page.waitForSelector('[data-session-list]')
    await page.waitForFunction(() => document.activeElement?.getAttribute('placeholder') === 'Filter sessions, paths, people')
    await page.locator('[aria-label="Collapse sidebar"]').click()
    await page.waitForSelector('[data-rail-expand]')
    assert.deepEqual(await keys(), ['rail', null])
    await page.locator('[data-rail-expand]').click()
    await page.waitForSelector('[data-session-list]')
    assert.deepEqual(await keys(), ['full', null])
    await page.screenshot({ path: 'rail.png' })
    await page.setViewportSize({ width: 600, height: 900 })
    await page.waitForFunction(() => getComputedStyle(document.querySelector('[data-slot="root"]')).display === 'none')
    console.log('PASS rail-check')
  } finally {
    await browser.close()
  }
})().catch((e) => {
  console.error('FAIL', e.message)
  process.exit(1)
})
```

Run: `cd $PW && node rail-check.js`
Expected: exit 0, `PASS rail-check` (rail 64 px wide; keys `['rail', null]` after the first load; one avatar; five page icons and seven stacked utility buttons; the toggles and the reload as asserted; the desktop sidebar hidden at 600 px). `rail.png` shows the full sidebar after the last expand. Then kill the server by pid and remove its data directory.

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
- Modify: `web/app/composables/useFullscreenToggle.ts` (whole file), `web/app/layouts/default.vue:13` (after `const launch = useLaunchModal()`), `web/app/layouts/bare.vue` (whole file), `web/app/composables/useShortcuts.ts:31` (the `F` row after `G R`), `:42` and `:54` (the wall's and the carousel's `F` rows), `web/app/pages/wall.vue:21,88,95,213-215`, `web/app/pages/carousel.vue:14,276,280,354-356`, `web/app/pages/index.vue:34-38`, `events.vue:86-88`, `agents.vue:130-133`, `crews/[[id]].vue:401-403`, `sessions/[id].vue:287-296`, `components/CrewRunHeader.vue:105-110`, `pages/join/[token].vue:219` and `:245-248` (line numbers at `55b9198`; Task 5 removed the `#leading` blocks above some of them, so the quoted text is the anchor)

**Interfaces:**
- Consumes: Nuxt UI `defineShortcuts` (a plain key never fires while an input, textarea or contenteditable has focus unless the shortcut sets `usingInput: true`; `alt_*` chords are what a focused terminal hands back, see `ALT_PASSTHROUGH_CODES`, which already lists `KeyF`); `useState`.
- Produces:
```ts
export function useFullscreenToggle(): {
  fullscreen: Ref<boolean>   // shared app state, follows the document's fullscreenchange
  toggle(): void             // document in or out of fullscreen
  listen(): void             // binds the state to the document for the calling component's life: each layout calls it once
  shortcuts(): void          // registers F and Alt+F for the calling layout, once
}
// <FullscreenButton size? /> — size 'sm' | 'md' (default); data-fullscreen; icon maximize / minimize; tooltip "Toggle fullscreen" with the F kbd
```

- [ ] **Step 1: Write the failing vitest** `web/app/composables/useShortcuts.test.ts` (the module's constants import nothing from Nuxt, so it loads under vitest's node environment; the second case pins Task 5's label):

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

describe('the sidebar shortcut', () => {
  it('says it toggles the rail', () => {
    expect(GLOBAL_SHORTCUTS.rows.find((r) => r.keys.join('+') === 'meta+B')?.label).toBe('Collapse the sidebar to the rail, or expand it (Alt+B in a terminal)')
  })
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `npm --prefix web test -- useShortcuts`
Expected: FAIL — `GLOBAL_SHORTCUTS` has no `F` row, the wall and carousel groups have one each.

- [ ] **Step 3: Move the row** in `web/app/composables/useShortcuts.ts`: add `{ keys: ['F'], label: 'Toggle fullscreen (Alt+F in a terminal)' },` to `GLOBAL_SHORTCUTS.rows` after the `G R` row (`:31`); delete the `F` rows of `WALL_SHORTCUTS` (`:42`) and `CAROUSEL_SHORTCUTS` (`:54`).

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
/** The fullscreen toggle of every page header; the layout listens for the change and registers the F key. */
withDefaults(defineProps<{ size?: 'sm' | 'md' }>(), { size: 'md' })
const fs = useFullscreenToggle()
</script>

<template>
  <UTooltip text="Toggle fullscreen" :kbds="['F']">
    <UButton :icon="fs.fullscreen.value ? 'i-lucide-minimize' : 'i-lucide-maximize'" color="neutral" variant="ghost" :size="size" :aria-label="fs.fullscreen.value ? 'Exit fullscreen' : 'Enter fullscreen'" data-fullscreen @click="fs.toggle" />
  </UTooltip>
</template>
```

`web/app/layouts/default.vue` script, after `const launch = useLaunchModal()` (`:13`):

```ts
// Fullscreen: one listener and one F shortcut for every page under this layout.
const fs = useFullscreenToggle()
fs.listen()
fs.shortcuts()
```

`web/app/layouts/bare.vue` (the join page's layout) becomes:

```vue
<script setup lang="ts">
// The join page's layout: fullscreen as under the default layout, the F key and the change listener once.
const fs = useFullscreenToggle()
fs.listen()
fs.shortcuts()
</script>

<template>
  <div class="h-full flex flex-col bg-default">
    <slot />
  </div>
</template>
```

Pages:
- `wall.vue` — delete `const fs = useFullscreenToggle()` (`:21`) and the `f: () => fs.toggle(),` (`:88`) and `alt_f: { ...inTerminal, handler: () => fs.toggle() },` (`:95`) rows of its `defineShortcuts`, and replace the tooltip and button at the end of its `#right` slot (`:213-215`, `<UTooltip text="Toggle fullscreen" :kbds="['F']">` … `</UTooltip>`) with `<FullscreenButton />`.
- `carousel.vue` — the same three edits: `const fs` (`:14`), the `f` (`:276`) and `alt_f` (`:280`) rows, and the tooltip and button at the end of `#right` (`:354-356`) become `<FullscreenButton />`.
- `index.vue` — its navbar, empty once Task 5 took the `#leading` block, gets a `#right` slot with just the button:

```vue
      <UDashboardNavbar title="Sessions">
        <template #right>
          <FullscreenButton />
        </template>
      </UDashboardNavbar>
```

- `events.vue` (after the Refresh button, `:87`), `agents.vue` (after the Launch agent button, `:132`), `crews/[[id]].vue` (after the New crew button, `:402`), `sessions/[id].vue` (after the `UDropdownMenu`, `:293-295`), `components/CrewRunHeader.vue` (after the Stop all button, `:109`) — add `<FullscreenButton />` as the last element of the navbar's `#right` slot.
- `join/[token].vue` — in each of its two `<header>` elements, `<FullscreenButton size="sm" />` as the last element: after the run header's refresh button (`:219`, `aria-label="Refresh the members"`) and after the session header's `open path[:line]` form (`:245-247`), before `</header>`.

- [ ] **Step 6: Typecheck**

Run: `npm --prefix web run typecheck && npm --prefix web test`
Expected: clean; `grep -rn "alt_f\|useFullscreenToggle()" web/app/pages` finds nothing (the pages only use the component).

- [ ] **Step 7: Headless check** — start the test server as in the header (after `make web-build && make build-go`; its public URL is the test server, so the link below opens there), with one `shell` session and one view link: `curl -s -X POST -H 'Authorization: Bearer dev-admin-token-change-me' -H 'Content-Type: application/json' -d '{"role":"view"}' http://127.0.0.1:8099/api/sessions/<id>/links` (its `url` is the join page). Headless Chromium may refuse a synthetic request for fullscreen, so the script counts the requests through a stub of `Element.prototype.requestFullscreen` rather than reading the browser's answer. `$PW/fullscreen-check.js`:

```js
// Task 6: one fullscreen button on every page, and F typed into a field stays a letter. Exits 1 at the first failed check.
const { chromium } = require('playwright-core')
const assert = require('node:assert/strict')
const base = process.env.BASE || 'http://127.0.0.1:8099'
const [sessionId, joinUrl] = process.argv.slice(2)
;(async () => {
  assert.ok(sessionId && joinUrl?.startsWith(base + '/join/'), 'usage: node fullscreen-check.js <session id> <view link url on the test server>')
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  try {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await ctx.addInitScript(() => {
      localStorage.setItem('conductor.adminToken', 'dev-admin-token-change-me')
      // Headless Chromium may refuse fullscreen without a real gesture: count the requests instead.
      window.__fullscreenRequests = 0
      Element.prototype.requestFullscreen = function () {
        window.__fullscreenRequests++
        return Promise.resolve()
      }
    })
    const requests = () => page.evaluate(() => window.__fullscreenRequests)
    const page = await ctx.newPage()
    for (const path of ['/', '/wall', '/carousel', '/agents', '/crews', '/events', `/sessions/${sessionId}`]) {
      await page.goto(base + path, { waitUntil: 'commit' })
      await page.waitForSelector('[data-fullscreen]')
      const n = await page.locator('[data-fullscreen]').count()
      console.log(path, 'buttons', n)
      assert.equal(n, 1, `${path}: one fullscreen button`)
    }
    // The join page asks for a name first; its headers come after.
    await page.goto(joinUrl, { waitUntil: 'commit' })
    await page.getByPlaceholder('Priya Shah').fill('checker')
    await page.getByRole('button', { name: 'Join session' }).click()
    await page.waitForSelector('[data-fullscreen]')
    assert.equal(await page.locator('[data-fullscreen]').count(), 1, '/join: one fullscreen button')
    // Review Focus 5: F while typing is a letter.
    await page.goto(base + '/crews', { waitUntil: 'commit' })
    await page.getByRole('button', { name: 'New crew' }).first().click()
    const name = page.locator('[aria-label="Crew name"]')
    await name.fill('')
    await name.press('f')
    assert.equal(await name.inputValue(), 'f', 'f typed into the crew name is a letter')
    assert.equal(await requests(), 0, 'and asks for no fullscreen')
    // F on the page body asks for fullscreen, once.
    await page.locator('body').click({ position: { x: 1300, y: 850 } })
    await page.keyboard.press('f')
    await page.waitForFunction(() => window.__fullscreenRequests === 1)
    // So does the button.
    await page.locator('[data-fullscreen]').click()
    await page.waitForFunction(() => window.__fullscreenRequests === 2)
    await page.screenshot({ path: 'fullscreen.png' })
    console.log('PASS fullscreen-check')
  } finally {
    await browser.close()
  }
})().catch((e) => {
  console.error('FAIL', e.message)
  process.exit(1)
})
```

Run: `cd $PW && node fullscreen-check.js <id> <url>`
Expected: exit 0 and `PASS fullscreen-check`: one button on each of the seven pages and on the join page, `f` typed into the crew name stays there and asks for nothing, `f` on the page body asks once and the button once more. Kill the server by pid. Entering fullscreen for real is checked by hand (Verification 6).

- [ ] **Step 8: Commit**

```bash
git add web/app/components/FullscreenButton.vue web/app/composables/useFullscreenToggle.ts web/app/composables/useShortcuts.ts web/app/composables/useShortcuts.test.ts web/app/layouts web/app/pages web/app/components/CrewRunHeader.vue
git commit -m "web: fullscreen toggle and the F key on every page"
```

---

### Task 7: Agent availability

Spec: `docs/features.md` § Round 3 → Decisions → "Agent availability" (added 2026-10-01): `GET /api/catalog` reports `available` per agent (whether `command[0]` resolves on the server through the same `exec.LookPath` check `POST /api/catalog/check` uses, evaluated per request with a 30 s cache keyed by the command, so a freshly installed CLI shows up without a restart) and `site` (the agent's website: a field on the built-ins, an optional validated `https` URL on saved agents, exposed in the add-agent form). The Agents page greys out an agent that is not installed, says "Not installed on <host>" (the host from `GET /api/integrations`) and links to its site; the Launch dialog's server tab lists only available agents and, when none is, says so with a link to the Agents page; the "My machine" tab keeps every agent (availability there is the host's). The crew editor's agent select marks unavailable agents the same way, and `POST /api/crews/{id}/launch` refuses a crew whose member agent is not installed with `400 invalid_crew` naming the member and the agent, checked at launch next to `CheckAgents`.

(`GET /api/integrations`' `installable` is another thing: whether an adapter has a file to install hooks into. The adapters' own `BinaryPath` looks up `conductor`, not an agent. Availability is the agent's `command[0]` through `exec.LookPath`, the resolution `POST /api/catalog/check` uses, and the two go through one cache here.)

**Files:**
- Create: `internal/api/lookup.go`, `internal/api/lookup_test.go`, `web/app/utils/agents.ts`, `web/app/utils/agents.test.ts`, `web/app/composables/useServerHost.ts`
- Modify: `internal/catalog/catalog.go:5-16` (imports), `:27` (`Site` after `Icon`), `:100-108` (`maxSite`), `:210-212` (`validate`, after the icon check), `:260` (`validateSite` before `validateSignal`), `:340-373` (`inherit`), `internal/catalog/defaults.go` (the built-ins' sites), `internal/catalog/catalog_test.go` (append), `internal/api/catalog.go:3-15` (imports: `os/exec` goes), `:21-49` (`catalogEntry`, `entry`, `saveAgentRequest`), `:60`, `:117`, `:200` (`entry`'s callers), `:208-228` (`handleCheckCommand`), `internal/api/server.go:46-48` (`lookups` field) and `:150` (`New`), `internal/api/runs.go:66-79` (`checkLaunch`, `installed`), `:104` and `:187` (its callers), `internal/api/api_test.go:2053` (`entry`'s new argument) and before `func (e *testEnv) serve` (`:988`), `internal/api/crews_test.go` (append), `web/app/composables/useSessions.ts:60` (`AgentInfo`, after `replaces`), `:78` (`AgentInput`, after `icon`), `web/app/utils/agentForm.ts` and `agentForm.test.ts`, `web/app/pages/agents.vue:4`, `:17`, `:29`, `:142-168`, `web/app/components/AddAgentSlideover.vue:219-221`, `web/app/components/LaunchSessionModal.vue:2-5`, `:23`, `:31-32`, `:105`, `:117`, `web/app/components/CrewMembersTable.vue:2-4`, `:15`, `:32`, `web/app/components/CrewEditor.vue` (the `host` through to the table), `docs/protocol.md` (the catalog rows, in Task 8)

**Interfaces:**
- Consumes: `catalog.Agent`, `validate`, `inherit`, `Agent.Redacted`, `Catalog.List`/`Get`/`ApplyOverlay`, `defaults()`; `exec.LookPath`; plan 1's `api.catalogEntry{Agent; Source; Replaces}`, `entry(a, cat, base)`, `saveAgentRequest{Agent; Source; Replaces}`, `handleCatalog`, `saveAgent`, `unhideAgent`; `crew.ErrInvalid`, `checkLaunch`; test helpers `newTestEnv`, `e.save`, `agentBody`, `e.catalogAgent`, `e.sendCrew`, `e.crewBody`, `crewMember`, `e.stopEverything`, `wantAPIError`; `useSessions().integrations()` (`{ integrations, host, webhooks }`); plan 1's `utils/agentForm.ts` (`AgentForm`, `Field`, `formFromAgent`, `formErrors`, `agentPayload`) and `utils/agentIcons.ts` (`agentIcon()`).
- Produces:
```go
// internal/catalog/catalog.go
Agent.Site string `json:"site,omitempty"`   // https URL with a host, no user info, ≤ 200 bytes; inherited by an override that omits it
func validateSite(site string) error
// internal/api/lookup.go
type lookupCache struct{ /* mu, entries map[string]lookupEntry, look func(string) (string, error), now func() time.Time */ }
func newLookupCache() *lookupCache
func (c *lookupCache) found(program string) (path string, ok bool) // cached 30 s per program
func (c *lookupCache) check(program string) (path string, ok bool) // asks now, keeps the answer
const lookupTTL = 30 * time.Second
// internal/api/catalog.go
type catalogEntry struct { catalog.Agent; Source, Replaces catalog.Source; Available bool `json:"available"` }
func entry(a catalog.Agent, cat, base catalog.Catalog, lookups *lookupCache) catalogEntry
type saveAgentRequest struct { catalog.Agent; Source, Replaces, Available json.RawMessage } // all three ignored
// internal/api/runs.go
func checkLaunch(members []crew.Member, cat catalog.Catalog, installed func(program string) bool) error
func (s *Server) installed(program string) bool
```
```ts
// web/app/composables/useSessions.ts
AgentInfo.available?: boolean; AgentInfo.site?: string; AgentInput.site?: string
// web/app/utils/agents.ts
export function isAvailable(a: Pick<AgentInfo, 'available'>): boolean               // undefined (an older server) counts as available
export function notInstalled(host: string): string                                  // "Not installed on <host>" / "Not installed on the server"
export function serverAgents<T extends Pick<AgentInfo, 'available'>>(list: readonly T[]): T[]
export function agentItem(a: AgentInfo, host: string): { label: string; value: string; icon: string } // marked, never disabled; icon through agentIcon()
// web/app/utils/agentForm.ts
AgentForm.site: string; Field gains 'site'
export function siteError(site: string): string                                      // '' when empty or https://host…
// web/app/composables/useServerHost.ts
export function useServerHost(): { host: Ref<string>; load(): Promise<void> }
```

- [ ] **Step 1: Write the failing catalog tests** at the end of `internal/catalog/catalog_test.go`:

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

// Every built-in but the shell names a website of the right shape (an https
// URL with a host, which validate checks); whether each is the agent's real
// site is checked by hand (docs/features.md, open verification of round 3).
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

// A saved override that leaves the site out keeps the built-in's, as it keeps
// its adapter and signal; one with a site of its own keeps that.
func TestOverlayInheritsTheSite(t *testing.T) {
	c := Default()
	if err := c.ApplyOverlay(Overlay{Agents: []Agent{
		{ID: "claude", Name: "Claude, mine", Command: []string{"claude"}},
		{ID: "codex", Name: "Codex", Command: []string{"codex"}, Site: "https://example.com/codex"},
	}}); err != nil {
		t.Fatal(err)
	}
	if a, _ := c.Get("claude"); a.Site != "https://claude.com/claude-code" {
		t.Fatalf("claude: %q", a.Site)
	}
	if a, _ := c.Get("codex"); a.Site != "https://example.com/codex" {
		t.Fatalf("codex: %q", a.Site)
	}
}
```

- [ ] **Step 2: Run them, see them fail, implement the field**

Run: `go test ./internal/catalog -run 'TestSiteValidation|TestDefaultsHaveSites|TestOverlayInheritsTheSite'` → FAIL to compile, `a.Site undefined`.

`internal/catalog/catalog.go`: `"net/url"` joins the imports (`:5-16`, after `"maps"`); the field after `Icon` (`:27`):

```go
	// Site is the agent's website, which the Agents page links to beside an
	// agent that is not installed on the server: an https URL, or empty.
	Site string `json:"site,omitempty"`
```

the bound at the end of the limits block (`:100-108`, after `maxSignalPattern`):

```go
	maxSite           = 200  // bytes in site
```

in `validate`, after the icon check (`:210-212`):

```go
	if a.Site != "" {
		if err := validateSite(a.Site); err != nil {
			return fmt.Errorf("agent %s: site: %w", a.ID, err)
		}
	}
```

before the comment of `validateSignal` (`:260`):

```go
// validateSite accepts an https URL with a host and no user info, at most
// maxSite bytes, so that the Agents page can link to it as it is.
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

and in `inherit` (`:340-373`): its comment's "an adapter or a signal it omits" becomes "an adapter, a signal or a site it omits", and inside `if had { … }`, after the signal block:

```go
		if a.Site == "" {
			a.Site = prev.Site
		}
```

(`clone` and `Redacted` need no change: `Site` is a string.) `internal/catalog/defaults.go`: the comment of `defaults` gains "and its website as best known (to be checked in a browser: the open verification of round 3 in docs/features.md)" after "the signal its adapter reports through", and each built-in but `shell` gets `Site:` on the line after its `Icon:`, from the agents' own sites as best known (the test checks their shape only; whether each is right is a user item under Open verification in Task 8):

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

for example `Site:        "https://claude.com/claude-code",` (gofmt aligns it with the fields around it).

Run: `go test -race ./internal/catalog` → PASS (`TestBuiltInIconsAreInTheWorkbenchBundle` is untouched: no icon changes).

- [ ] **Step 3: Write the failing lookup-cache test** `internal/api/lookup_test.go`:

```go
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
// meanwhile is seen at most this long after.
const lookupTTL = 30 * time.Second

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
	defer c.mu.Unlock()
	if e, ok := c.entries[program]; ok && c.now().Sub(e.at) < lookupTTL {
		return e.path, e.ok
	}
	return c.ask(program)
}

// check asks the system now, whatever the cache holds, and keeps the
// answer: the add-agent form's check, which the catalog then agrees with.
func (c *lookupCache) check(program string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ask(program)
}

// ask looks program up and keeps the answer, dropping the answers that have
// expired so the cache holds only programs asked about lately. The caller
// holds c.mu.
func (c *lookupCache) ask(program string) (string, bool) {
	now := c.now()
	for p, e := range c.entries {
		if now.Sub(e.at) >= lookupTTL {
			delete(c.entries, p)
		}
	}
	path, err := c.look(program)
	e := lookupEntry{path: path, ok: err == nil, at: now}
	c.entries[program] = e
	return e.path, e.ok
}
```

`internal/api/server.go`: the field after `fileDeny` (`:46-48`):

```go
	// lookups says, for 30 s at a time, whether the programs of the catalog's
	// agents resolve on this server (see lookupCache).
	lookups *lookupCache
```

and `lookups:  newLookupCache(),` in `New`'s literal after `fileDeny: fileDeny(cfg, st),` (`:150`).

Run: `go test ./internal/api -run TestLookupCacheExpires` → PASS.

- [ ] **Step 5: Write the failing API tests**

In `internal/api/api_test.go`, after `TestCatalogCheckCommand` and before the comment of `func (e *testEnv) serve` (`:988`):

```go
// GET /api/catalog says which agents are installed on this server: cat is,
// an agent whose program does not exist is not, by the check POST
// /api/catalog/check runs; site travels with the agent. An agent read there,
// available included, saves back as it is.
func TestCatalogReportsAvailability(t *testing.T) {
	e := newTestEnv(t, nil)
	ghost := agentBody("ghost")
	ghost["command"] = []string{"definitely-not-a-real-binary-xyz"}
	ghost["site"] = "https://example.com/ghost"
	if out := e.save(ghost); out["agent"].(map[string]any)["available"] != false {
		t.Fatalf("save reply: %v", out)
	}
	if a := e.catalogAgent("cat"); a["available"] != true {
		t.Fatalf("cat: %v", a)
	}
	listed := e.catalogAgent("ghost")
	if listed["available"] != false || listed["site"] != "https://example.com/ghost" {
		t.Fatalf("ghost: %v", listed)
	}
	listed["description"] = "edited"
	e.save(listed)
	bad := agentBody("badsite")
	bad["site"] = "http://example.com"
	if resp, out := e.do("POST", "/api/catalog", adminToken, bad); resp.StatusCode != http.StatusBadRequest || errorCode(out) != "invalid_agent" {
		t.Fatalf("http site: %d %v", resp.StatusCode, out)
	}
}
```

At the end of `internal/api/crews_test.go`:

```go
// A crew with a member whose agent is not installed on the server is refused
// at launch, before any session starts, naming the member and the agent; so
// is such a member added to a run.
func TestLaunchRefusesAnAgentNotInstalled(t *testing.T) {
	e := newTestEnv(t, nil)
	e.stopEverything(t)
	ghost := agentBody("ghost")
	ghost["command"] = []string{"definitely-not-a-real-binary-xyz"}
	e.save(ghost)
	body := e.crewBody("Ghost crew")
	body["isolation"] = "none"
	crewMember(body, 1)["agentId"] = "ghost"
	c := e.sendCrew("POST", "/api/crews", body, http.StatusCreated)
	resp, out := e.do("POST", "/api/crews/"+c["id"].(string)+"/launch", adminToken, nil)
	wantAPIError(t, "launch", resp, out, http.StatusBadRequest, "invalid_crew", `member "tests": agent "ghost" is not installed on the server`)
	if _, out := e.do("GET", "/api/sessions", adminToken, nil); len(out["sessions"].([]any)) != 0 {
		t.Fatalf("a session was started: %v", out)
	}
	crewMember(body, 1)["agentId"] = "cat"
	c = e.sendCrew("POST", "/api/crews", body, http.StatusCreated)
	resp, out = e.do("POST", "/api/crews/"+c["id"].(string)+"/launch", adminToken, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
	runID := out["run"].(map[string]any)["id"].(string)
	resp, out = e.do("POST", "/api/runs/"+runID+"/members", adminToken, map[string]any{"name": "late", "agentId": "ghost", "prompt": "", "start": map[string]any{"when": "manual"}})
	wantAPIError(t, "add member", resp, out, http.StatusBadRequest, "invalid_crew", `member "late": agent "ghost" is not installed on the server`)
}
```

- [ ] **Step 6: Run them to see them fail, then implement**

Run: `go test ./internal/api -run 'TestCatalogReportsAvailability|TestLaunchRefusesAnAgentNotInstalled'` → FAIL (`available` missing from the entries; the launch answers 201).

`internal/api/catalog.go`: `"os/exec"` leaves the imports (`:3-15`; the cache does the lookups now). `catalogEntry`, `entry` and `saveAgentRequest` (`:21-49`) become:

```go
// catalogEntry is an agent as the catalog routes answer with it: env values
// masked, where the catalog took it from and, for a saved agent that replaces
// a built-in or configured one, where that one came from (deleting the saved
// agent brings it back), and whether its program resolves on this server
// (lookups: cached 30 s). Its site is the agent's own field.
type catalogEntry struct {
	catalog.Agent
	Source    catalog.Source `json:"source,omitempty"`
	Replaces  catalog.Source `json:"replaces,omitempty"`
	Available bool           `json:"available"`
}

// entry is a as the routes show it, by the effective catalog cat, the
// configured one, base, and the server's program lookups.
func entry(a catalog.Agent, cat, base catalog.Catalog, lookups *lookupCache) catalogEntry {
	e := catalogEntry{Agent: a.Redacted(), Source: cat.Source(a.ID)}
	if e.Source == catalog.SourceSaved {
		e.Replaces = base.Source(a.ID)
	}
	if len(a.Command) > 0 {
		_, e.Available = lookups.found(a.Command[0])
	}
	return e
}

// saveAgentRequest is the body of POST /api/catalog: an agent, as a client
// builds it or as GET /api/catalog lists it. source, replaces and available,
// which the listing adds, are taken and ignored, so an agent read there can be
// sent back as it is; any other field the agent does not have is refused.
type saveAgentRequest struct {
	catalog.Agent
	Source    json.RawMessage `json:"source,omitempty"`
	Replaces  json.RawMessage `json:"replaces,omitempty"`
	Available json.RawMessage `json:"available,omitempty"`
}
```

Their callers pass the cache: `entry(a, cat, s.base)` in `handleCatalog` (`:60`), `entry(saved, cat, s.base)` in `saveAgent` (`:117`) and `entry(a, cat, s.base)` in `unhideAgent` (`:200`) each gain `, s.lookups` as the last argument. In `handleCheckCommand` (`:208-228`) the comment's "Only command[0] is looked up and nothing is run." becomes "Only command[0] is looked up, now and not from the cache, which keeps the answer for GET /api/catalog; nothing is run.", and

```go
	path, err := exec.LookPath(req.Command[0])
	if err != nil {
```

becomes

```go
	path, ok := s.lookups.check(req.Command[0])
	if !ok {
```

In `internal/api/api_test.go`, `TestCatalogListsWhereEachAgentComesFrom` calls `entry` directly (`:2053`): its call gains `e.srv.lookups`:

```go
	if b, err := json.Marshal(entry(catalog.Agent{ID: "gone", Name: "gone", Command: []string{"x"}}, e.srv.Catalog(), e.srv.base, e.srv.lookups)); err != nil || strings.Contains(string(b), `"source"`) {
```

`internal/api/runs.go`, `checkLaunch` (`:66-79`) becomes, with the server helper after it:

```go
// checkLaunch reports the first member whose agent the catalog lacks, whose
// agent's program is not installed on this server (installed, the server's
// lookups), or who is given arguments its agent does not take, so that a
// launch refuses it before any session starts. The error matches
// crew.ErrInvalid.
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

// installed reports whether a program resolves on this server (lookups:
// cached 30 s), for checkLaunch.
func (s *Server) installed(program string) bool {
	_, ok := s.lookups.found(program)
	return ok
}
```

and `handleLaunchCrew` (`:104`) and `handleAddRunMember` (`:187`) call `checkLaunch(c.Members, s.Catalog(), s.installed)` and `checkLaunch([]crew.Member{m}, s.Catalog(), s.installed)`.

Run: `go test -race -count=1 ./internal/api ./internal/catalog && make lint` → PASS. (`TestCatalogSaveKeepsMaskedEnvValues` sends back an agent read from the listing, `available` included: `saveAgentRequest` takes and ignores it. `TestCreateSessionWithAnAdapterAndAMissingCommand` and the other session tests keep passing: launching a session is unchanged; only crews are refused.)

- [ ] **Step 7: Commit the server side**

```bash
git add internal/catalog internal/api/lookup.go internal/api/lookup_test.go internal/api/catalog.go internal/api/server.go internal/api/runs.go internal/api/api_test.go internal/api/crews_test.go
git commit -m "catalog: available and site per agent; a crew launch refuses an agent not installed"
```

- [ ] **Step 8: Write the failing vitests**

`web/app/utils/agents.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { AgentInfo } from '~/composables/useSessions'
import { agentItem, isAvailable, notInstalled, serverAgents } from './agents'

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

  it('marks an unavailable agent in a select without disabling it, and shows the bundled icon or the generic one', () => {
    expect(agentItem(agent({ id: 'claude', name: 'Claude Code', icon: 'i-lucide-sparkles', available: false }), 'build-1')).toEqual({ label: 'Claude Code · not installed on build-1', value: 'claude', icon: 'i-lucide-circle-off' })
    expect(agentItem(agent({ id: 'claude', name: 'Claude Code', icon: 'i-lucide-sparkles', available: true }), 'build-1')).toEqual({ label: 'Claude Code', value: 'claude', icon: 'i-lucide-sparkles' })
    expect(agentItem(agent({ id: 'sh', name: 'Shell', icon: 'i-lucide-not-bundled' }), 'build-1')).toEqual({ label: 'Shell', value: 'sh', icon: 'i-lucide-bot' })
  })
})
```

In `web/app/utils/agentForm.test.ts`, `siteError` joins the import from `./agentForm`, and at the end of the file:

```ts
describe('the site field', () => {
  it('round-trips the site, and sends none when it is empty', () => {
    const f = formFromAgent({ ...keyed, site: 'https://example.com/keyed' }, counter())
    expect(f.site).toBe('https://example.com/keyed')
    expect(agentPayload(f, keyed).site).toBe('https://example.com/keyed')
    expect(agentPayload({ ...f, site: '  ' }, keyed).site).toBeUndefined()
  })
  it('accepts an empty or https site and refuses the rest, as the server does', () => {
    expect(siteError('')).toBe('')
    expect(siteError('https://example.com/x')).toBe('')
    for (const bad of ['http://example.com', 'example.com', 'https://user:pw@example.com', 'javascript:alert(1)', `https://example.com/${'a'.repeat(200)}`]) {
      expect(siteError(bad)).toBe('An https:// address, or nothing')
    }
    const f = formFromAgent(keyed, counter())
    expect(formErrors({ ...f, site: 'http://example.com' }).site).toBe('An https:// address, or nothing')
  })
})
```

- [ ] **Step 9: Run them, see them fail, implement**

Run: `npm --prefix web test -- agents.test agentForm` → FAIL (the module `./agents` is missing; `siteError` is not exported).

`web/app/utils/agents.ts`:

```ts
import type { AgentInfo } from '~/composables/useSessions'
import { agentIcon } from './agentIcons'

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

/** A select item for an agent, marked when it is not installed; never disabled, since a crew may be edited before its agents are installed. */
export function agentItem(a: AgentInfo, host: string): { label: string; value: string; icon: string } {
  if (isAvailable(a)) return { label: a.name, value: a.id, icon: agentIcon(a.icon) }
  return { label: `${a.name} · ${notInstalled(host).toLowerCase()}`, value: a.id, icon: 'i-lucide-circle-off' }
}
```

`web/app/utils/agentForm.ts` (plan 1's form rules; the site rule joins them): `AgentForm` gains, after `description`,

```ts
  /** The agent's website: an https:// address, or empty. */
  site: string
```

`Field` becomes `'name' | 'id' | 'command' | 'pattern' | 'env' | 'site'`; `formFromAgent` sets `site: a?.site ?? '',` after `description`; `formErrors` adds, after the pattern rule,

```ts
  const site = siteError(f.site)
  if (site) e.site = site
```

`agentPayload` sends `site: f.site.trim() || undefined,` after `description`; and before the comment of `formErrors`:

```ts
/** The server's rule for an agent's site (validateSite in internal/catalog), in words: '' when empty or an https URL with a host and no user info. */
export function siteError(site: string): string {
  const s = site.trim()
  if (!s) return ''
  try {
    const u = new URL(s)
    if (u.protocol === 'https:' && u.hostname && !u.username && !u.password && s.length <= 200 && !/\s/.test(s)) return ''
  } catch {
    /* not a URL */
  }
  return 'An https:// address, or nothing'
}
```

`web/app/composables/useSessions.ts` — on `AgentInfo`, after `replaces` (`:60`):

```ts
  /** Whether command[0] resolves on the server (the check of POST /api/catalog/check, cached 30 s). Missing from an older server. */
  available?: boolean
  /** The agent's website, an https URL, when known: a built-in's, or what was saved with the agent. */
  site?: string
```

on `AgentInput`, after `icon` (`:78`):

```ts
  /** An https:// URL with a host, at most 200 bytes; left out, an agent that replaces a built-in keeps the built-in's. */
  site?: string
```

Run: `npm --prefix web test -- agents.test agentForm` → PASS.

- [ ] **Step 10: The host composable and the three surfaces**

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

`web/app/pages/agents.vue` — script: `import { isAvailable, notInstalled } from '~/utils/agents'` after the `agentIcons` import (`:4`), `const serverHost = useServerHost()` after `const launch = useLaunchModal()` (`:17`), and `serverHost.load()` in `refresh()` after `error.value = ''` (`:29`). Template, the card (`:142-168`, from `<UCard v-for="a in agents" :key="a.id">` to its `</UCard>`) becomes:

```vue
        <UCard v-for="a in agents" :key="a.id" :class="!isAvailable(a) && 'opacity-60'" :data-agent="a.id" :data-available="isAvailable(a)">
          <div class="flex items-start gap-3">
            <UIcon :name="agentIcon(a.icon)" class="size-6 text-primary flex-none mt-0.5" />
            <div class="min-w-0 flex-1">
              <div class="font-medium">{{ a.name }} <span class="text-xs text-muted font-mono">{{ a.id }}</span></div>
              <p v-if="a.description" class="text-sm text-muted">{{ a.description }}</p>
              <code class="block text-xs mt-2 truncate" :title="joinArgv(a.command)">{{ joinArgv(a.command) }}</code>
              <div class="mt-2 flex flex-wrap gap-2">
                <UBadge v-if="!isAvailable(a)" :label="notInstalled(serverHost.host.value)" icon="i-lucide-circle-off" color="warning" variant="subtle" size="sm" data-not-installed />
                <UBadge :label="signalBadge(a).label" :title="signalBadge(a).title" color="neutral" variant="subtle" size="sm" />
                <UBadge v-if="a.allowArgs" label="accepts args" color="neutral" variant="subtle" size="sm" />
                <UBadge v-if="a.cwd" :label="a.cwd" color="neutral" variant="subtle" size="sm" />
              </div>
              <div class="mt-3 -mb-1 flex justify-end gap-1">
                <UButton v-if="a.site" label="Website" icon="i-lucide-external-link" size="xs" color="neutral" variant="ghost" :to="a.site" target="_blank" rel="noopener noreferrer" :aria-label="`${a.name} website`" />
                <UButton label="Edit" icon="i-lucide-pencil" size="xs" color="neutral" variant="ghost" :aria-label="`Edit ${a.name}`" @click="editAgent(a)" />
                <UButton
                  :label="removalText(removalOf(a), a.name, a.id).button"
                  :icon="removalText(removalOf(a), a.name, a.id).icon"
                  size="xs"
                  color="neutral"
                  variant="ghost"
                  :aria-label="`${removalText(removalOf(a), a.name, a.id).button} ${a.name}`"
                  @click="askHide(a)"
                />
              </div>
            </div>
          </div>
        </UCard>
```

`web/app/components/AddAgentSlideover.vue` — the form's `site` comes from `formFromAgent` and goes out through `agentPayload` (above); the field goes after Description (`:219-221`):

```vue
        <UFormField label="Website" name="site" hint="optional, https" :error="shown.site">
          <UInput v-model="form.site" type="url" placeholder="https://" maxlength="200" autocapitalize="off" spellcheck="false" class="w-full" />
          <template #help>The Agents page links to it when the agent is not installed on the server.</template>
        </UFormField>
```

`web/app/components/LaunchSessionModal.vue` — script: `import { serverAgents } from '~/utils/agents'` before the `argv` import (`:4`); after `selected` (`:23`):

```ts
/** The server tab offers the agents installed on the server; My machine offers every agent: what is installed there is the host's. */
const offered = computed(() => (state.runsOn === 'server' ? serverAgents(agents.value) : agents.value))
// The pick stays one the tab offers.
watch(offered, (list) => {
  if (!list.some((a) => a.id === state.agentId)) state.agentId = list[0]?.id ?? ''
})
```

the default pick (`:32`) becomes `if (!state.agentId && offered.value[0]) state.agentId = offered.value[0].id`. Template: `v-for="a in agents"` (`:105`) becomes `v-for="a in offered"`, and the empty line (`:117`) becomes:

```vue
          <p v-if="!offered.length && !loading" class="col-span-full text-sm text-muted" data-none-available>
            <template v-if="state.runsOn === 'server' && agents.length">No agent in the catalog is installed on this server. <NuxtLink to="/agents" class="underline" @click="open = false">See the Agents page</NuxtLink> for what is missing, or run one on your machine.</template>
            <template v-else>No agents in the catalog.</template>
          </p>
```

`web/app/components/CrewMembersTable.vue` — `import { agentItem } from '~/utils/agents'` after the `agentIcons` import (`:3`, which stays: the template's select icon uses `agentIcon`); the props (`:15`) gain `host?: string` with default `''`:

```ts
const props = withDefaults(defineProps<{ agents: AgentInfo[]; others?: string[]; single?: boolean; host?: string }>(), { others: () => [], single: false, host: '' })
```

and `agentItems` (`:32`) becomes:

```ts
/** The agent select: an agent not installed on the server (`host` names it) is marked, never disabled. */
const agentItems = computed(() => props.agents.map((a) => agentItem(a, props.host)))
```

`web/app/components/CrewEditor.vue` passes it: after `const api = useSessions()` (Task 2), `const serverHost = useServerHost()` and `serverHost.load()`; the table (`:173` at `55b9198`) gets `:host="serverHost.host.value"` after `:agents="agents"`. (`CrewRunHeader.vue`'s add-member table keeps the default: its note says "the server".)

- [ ] **Step 11: Typecheck, vitest, headless check**

Run: `npm --prefix web run typecheck && npm --prefix web test` → clean.

Headless: build (`make web-build && make build-go`) and start the test server as in the header, then `$PW/availability-check.js`; it saves `ghost`, an agent whose program does not exist, through the API, hides the installed agents for one step, and puts everything back whatever happens:

```js
// Task 7: availability on the Agents page, the Launch dialog and the crew editor. Exits 1 at the first failed check.
// Saves an agent "ghost" whose program does not exist, hides the installed agents for one step and puts everything back.
const { chromium } = require('playwright-core')
const assert = require('node:assert/strict')
const base = process.env.BASE || 'http://127.0.0.1:8099'
const auth = { Authorization: 'Bearer dev-admin-token-change-me', 'Content-Type': 'application/json' }
const api = (path, method = 'GET', body) => fetch(base + path, { method, headers: auth, body: body && JSON.stringify(body) }).then((r) => (r.status === 204 ? null : r.json()))
;(async () => {
  await api('/api/catalog', 'POST', { id: 'ghost', name: 'Ghost CLI', command: ['definitely-not-a-real-binary-xyz'], allowArgs: true, site: 'https://example.com/ghost' })
  const { host } = await api('/api/integrations')
  const browser = await chromium.launch({ executablePath: `${process.env.HOME}/.cache/ms-playwright/chromium-1117/chrome-linux/chrome`, args: ['--no-sandbox', '--use-gl=swiftshader'] })
  let hidden = []
  try {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await ctx.addInitScript(() => localStorage.setItem('conductor.adminToken', 'dev-admin-token-change-me'))
    const page = await ctx.newPage()
    await page.goto(base + '/agents', { waitUntil: 'commit' })
    await page.waitForSelector('[data-agent="ghost"]')
    assert.equal(await page.locator('[data-agent="ghost"]').getAttribute('data-available'), 'false', 'ghost is greyed')
    await page.waitForFunction((h) => document.querySelector('[data-agent="ghost"] [data-not-installed]')?.textContent?.trim() === `Not installed on ${h || 'the server'}`, host)
    assert.equal(await page.locator('[data-agent="ghost"] a[aria-label="Ghost CLI website"]').getAttribute('href'), 'https://example.com/ghost', 'its website is linked')
    assert.equal(await page.locator('[data-agent="shell"]').getAttribute('data-available'), 'true', 'the shell is installed')
    // The Launch dialog: the server tab leaves ghost out, My machine lists it.
    await page.locator('body').click({ position: { x: 1300, y: 850 } })
    await page.keyboard.press('n')
    const radios = page.locator('[role="radiogroup"][aria-label="Agent"] [role="radio"]')
    await radios.first().waitFor()
    assert.ok(!(await radios.allInnerTexts()).includes('Ghost CLI'), 'the server tab leaves ghost out')
    await page.getByRole('button', { name: 'My machine' }).click()
    await page.waitForFunction(() => [...document.querySelectorAll('[role="radiogroup"][aria-label="Agent"] [role="radio"]')].some((r) => r.textContent?.includes('Ghost CLI')))
    await page.keyboard.press('Escape')
    // Nothing installed: the server tab says so and links to the Agents page.
    hidden = (await api('/api/catalog')).agents.filter((a) => a.available).map((a) => a.id)
    for (const id of hidden) await api(`/api/catalog/${id}`, 'DELETE')
    await page.reload({ waitUntil: 'commit' })
    await page.waitForSelector('[data-agent="ghost"]')
    await page.locator('body').click({ position: { x: 1300, y: 850 } })
    await page.keyboard.press('n')
    await page.waitForSelector('[data-none-available] a[href="/agents"]')
    await page.keyboard.press('Escape')
    for (const id of hidden.splice(0)) await api(`/api/catalog/${id}/unhide`, 'POST')
    // The crew editor marks ghost in the agent select, without disabling it.
    await page.goto(base + '/crews', { waitUntil: 'commit' })
    await page.getByRole('button', { name: 'New crew' }).first().click()
    await page.locator('[aria-label^="Agent for"]').first().click()
    await page.waitForFunction((h) => [...document.querySelectorAll('[role="option"]')].some((o) => o.textContent?.includes(`Ghost CLI · not installed on ${h || 'the server'}`) && o.getAttribute('aria-disabled') !== 'true'), host)
    await page.keyboard.press('Escape')
    await page.screenshot({ path: 'availability.png' })
    console.log('PASS availability-check')
  } finally {
    for (const id of hidden) await api(`/api/catalog/${id}/unhide`, 'POST')
    await api('/api/catalog/ghost', 'DELETE')
    await browser.close()
  }
})().catch((e) => {
  console.error('FAIL', e.message)
  process.exit(1)
})
```

Run: `cd $PW && node availability-check.js`
Expected: exit 0, `PASS availability-check`: `ghost` greyed with `Not installed on <host>` and its Website link, the shell available; the server tab without Ghost CLI and My machine with it; with nothing installed, the server tab's link to the Agents page; the crew editor's select listing `Ghost CLI · not installed on <host>`, not disabled. Kill the server by pid.

- [ ] **Step 12: Commit**

```bash
git add web/app/utils/agents.ts web/app/utils/agents.test.ts web/app/utils/agentForm.ts web/app/utils/agentForm.test.ts web/app/composables/useServerHost.ts web/app/composables/useSessions.ts web/app/pages/agents.vue web/app/components/AddAgentSlideover.vue web/app/components/LaunchSessionModal.vue web/app/components/CrewMembersTable.vue web/app/components/CrewEditor.vue
git commit -m "web: agents not installed on the server are greyed, filtered from a server launch and marked in crews"
```

---

### Task 8: Docs

**Files:**
- Modify: `docs/protocol.md:404-418` (the HTTP API table), `README.md:122-131` (Sidebar and keyboard shortcuts), `:364-471` (Crews), `:530-585` (Agent catalog), a new "Shell completion" section before Configuration (`:472`), `:492` (the Configuration table), `docs/features.md:15-22` (Delivered) and `:270-273` (Open verification, round 3), `docs/architecture.md:88-89` (Packages), `AGENTS.md:10-11` (Map)

**Interfaces:** none produced; every row states what Tasks 1, 3, 4 and 7 implemented, in the table's own style. Each edit below quotes the current text it replaces or follows (at `55b9198`).

- [ ] **Step 1: `docs/protocol.md`**

The `GET /api/catalog` row (`:404`): its end, "for a saved agent that replaces a built-in or configured one, `replaces` (where that one came from) |", becomes:

```
for a saved agent that replaces a built-in or configured one, `replaces` (where that one came from); and `available`, whether `command[0]` resolves on the server (the check of `POST /api/catalog/check`, its answer kept 30 s per program), and `site` when the agent has a website (an `https` URL: a built-in's, or the saved agent's) |
```

The `POST /api/catalog` row (`:405`): "`source` and `replaces` in the body are ignored" becomes "`source`, `replaces` and `available` in the body are ignored"; "a saved agent that leaves out `adapter` or `signal` takes those of the agent it replaces" becomes "a saved agent that leaves out `adapter`, `signal` or `site` takes those of the agent it replaces"; and before "`400 invalid_agent` carries the validation message" goes "`site`, when present, must be an `https://` URL with a host and no user info, at most 200 bytes;".

The `POST /api/catalog/check` row (`:408`): "whether `command[0]` resolves on the server (`exec.LookPath`); nothing is run" becomes "whether `command[0]` resolves on the server (`exec.LookPath`), asked now and kept 30 s for the `available` of `GET /api/catalog`; nothing is run". After that row, two new rows:

```
| `GET /api/paths` | admin | `prefix` (≤ 4096 bytes, no NUL) and `limit` (1–50, default 50) in the query; reply `{dir, entries, truncated}`: `dir` is the longest leading part of `prefix` that is an existing directory under an allowed root (symbolic links resolved, as a session's `cwd` is checked; the server's default working directory for an empty `prefix`), `entries` its child directories whose names start with the path element typed after `dir` (all of them when `prefix` names a directory), in name order, at most `limit` of the first 2000 entries the directory gives, hidden ones only when that element starts with a dot (a lone `.` after the last separator included), a symbolic link only when it leads under an allowed root, each `{name, path, git: {repo, commits}}` (`repo`: in a git working tree; `commits`: its `HEAD` is a commit; a child with no `.git` of its own carries its parent's marks; neither without `git` on the server; git is asked at most once per entry, one call at a time, all within 5 s), `truncated` when more match than `limit` or the directory has more than 2000 entries; `400 invalid_cwd` when no part of `prefix` is such a directory, `400 invalid_request` for a bad `prefix` or `limit`; `500 list_failed` when the directory cannot be read or git does not answer in time; the path asked about is logged at debug level only |
| `GET /api/git/check` | admin | `cwd` in the query (≤ 4096 bytes, no NUL; the server's default working directory when empty); reply `{inRepo, toplevel?, hasCommit, message}` by the rules a launch with `isolation: worktree` applies (`git -C <cwd> rev-parse`, under the allowed roots): `inRepo` when `cwd` is in a git working tree, `toplevel` its top, `hasCommit` when `HEAD` is a commit, `message` the words the launch's refusal would use (git's own for a repository it refuses, owned by another user or unreadable), or that worktrees can be made, or that `git` is not installed (then `inRepo:false`); a preview: the launch's `409 not_a_repo` stays the authority; `400 invalid_cwd` as for a session; `500 git_failed` when git cannot be run |
```

After the `POST /api/crews` row (`:411`), a new row:

```
| `POST /api/crews/examples` | admin | seed the example crews (`example-todo-app`, `example-test-fixer`, `example-docs-writer`, `example-dependency-upgrade`), as `conductor serve --examples` does: each is saved unless a file with its `id` exists in `crews/`, usable or not, which is left alone whatever it holds; reply `{added, skipped}`, the ids each way in that order; their `cwd` is the server's default working directory, their agents the `claude` and `codex` built-ins, not checked against the catalog (the launch checks); `503 store_unavailable` without a data directory; `500 store_failed` when one cannot be saved (the ones before it stay) |
```

The `POST /api/crews/{id}/launch` row (`:415`): "`400 invalid_crew` for a crew with no members, one that runs on a host, an agent the catalog does not have, arguments to an agent that takes none," becomes "`400 invalid_crew` for a crew with no members, one that runs on a host, an agent the catalog does not have, an agent whose program is not installed on the server (`command[0]` does not resolve, by the check of `POST /api/catalog/check`; the message names the member and the agent), arguments to an agent that takes none,". The `POST /api/runs/{run}/members` row (`:418`) needs nothing: its "or an agent as at launch" covers the new refusal.

- [ ] **Step 2: `README.md`**

In "Sidebar and keyboard shortcuts" (`:124-127`), the sentences

```
The sidebar hides completely with the panel button in its header or
**Ctrl+B** (**⌘B** on a Mac); the choice is remembered per browser, and a
button in every page's navbar brings it back. Press **?** (or use
**Shortcuts** in the sidebar) for the list of shortcuts on the current screen.
```

become

```
The sidebar collapses to an icon rail with the panel button in its header or
**Ctrl+B** (**⌘B** on a Mac). The rail keeps everything: the mark, a Launch
button, a search button that opens the full sidebar on its filter, the pages
and the sidebar's buttons as icons with tooltips, and every session as its
agent's initials with the amber dot when it needs you, the members of a running
crew together under its name. Click one to open it; the panel button at the
bottom of the rail brings the full sidebar back. The choice is remembered per
browser. On a phone the sidebar is a drawer. **F** toggles fullscreen on every
page (**Alt+F** in a terminal); every page header has the button, and the key
is ignored while you type in a field. Press **?** (or use **Shortcuts** in the
sidebar) for the list of shortcuts on the current screen.
```

and the rest of that paragraph ("Plain keys reach the agent …", the display name) stays as it is.

In "Crews", after the first paragraph (it ends "the **My machine** option is disabled.", `:375`):

```
The working-directory field completes as you type: the server lists the
directories under its allowed roots, at most 50 at a time (hidden ones once
you type the dot), marking the ones that are git repositories with a commit,
which a crew with worktrees needs. The editor says under the field whether
the crew could launch there with worktrees; the launch itself still decides
(`409 not_a_repo`). The Launch dialog's working directory completes the same
way. A crew whose member's agent is not installed on the server is refused at
launch (`invalid_crew`, naming the member).

**Example crews.** `conductor serve --examples` (or `CONDUCTOR_EXAMPLES=1`)
adds four example crews the first time: `example-todo-app` (a lead that
plans, two builders after it, a tester after the second builder),
`example-test-fixer`, `example-docs-writer` and `example-dependency-upgrade`,
each using Claude Code and Codex, the server's default working directory and a
worktree per member. They are ordinary crews once saved: edit or delete them
freely. A crew whose id has a file is never touched, so edits survive the
flag, and a deleted example comes back on the next `--examples`. The empty
**Crews** page offers **Load the examples**, which does the same
(`POST /api/crews/examples`).
```

In its "From a shell" block (`:445-451`), after the `conductor crews` line (`:448`):

```
conductor crews --ids                  # the ids only, one per line (for shell completion)
```

In "Agent catalog", the sentence "Such an entry inherits what it leaves out: the original's `adapter` and `signal`, and" (`:560-561`) becomes "Such an entry inherits what it leaves out: the original's `adapter`, `signal` and `site`, and". After the paragraph that ends "an agent whose program is missing there can still be saved, for use with `conductor host`." (`:572-573`):

```
**Installed agents.** The Agents page says which agents are installed on the
server (the program of their command resolves there, checked on every visit
and kept for 30 seconds) and links to the website of one that is not: the
built-ins name theirs, and the form takes one (`site`, an `https://` address)
for an agent you add. The Launch dialog's **Server** tab offers only the
installed ones (**My machine** offers them all: what is installed there is
your machine's business), the crew editor marks the others, and a crew whose
member's agent is not installed is refused at launch.
```

and in the limits paragraph, "`icon` matches `[a-z0-9][a-z0-9:-]{0,63}`, and a signal" (`:581`) becomes "`icon` matches `[a-z0-9][a-z0-9:-]{0,63}`, `site` is an `https://` URL with a host and no user info of at most 200 bytes, and a signal".

A new section before "## Configuration" (`:472`):

```
## Shell completion

`conductor completion zsh` or `conductor completion bash` prints a completion
script: subcommands, flags and their values, and for `conductor up` the crew
ids, read from the server as you type through `conductor crews --ids` (which
prints ids only and stays silent when `CONDUCTOR_SERVER` cannot be reached or
`CONDUCTOR_ADMIN_TOKEN` is not set). The ids are only ever offered as words:
nothing the server answers is run by your shell. Load it with
`source <(conductor completion zsh)` in `~/.zshrc` (after `compinit`), or let
`conductor completion install` append that line, marked, to `~/.zshrc` or
`~/.bashrc` (the shell from `$SHELL`, or `--shell`; the file from `--rc`); run
again it changes nothing, and it refuses a file, or a home, that is not yours.
```

In the Configuration table, after the `webhooks` row (`:492`):

```
| — | `CONDUCTOR_EXAMPLES` | off | `1` seeds the example crews once at startup, as `conductor serve --examples` does; not a config-file key |
```

- [ ] **Step 3: `docs/features.md`** — a new "Delivered" entry after the 2026-09-30 one (`:15-22`), before "## Planned":

```
## Delivered (2026-10-01)

- **Round 3**: the deferred items of rounds 1 and 2 and the crew storage of
  plan 1; then the sidebar rail, example crews, the working-directory picker
  with the git check, agent availability, shell completion and fullscreen on
  every page. See the README (Sidebar and keyboard shortcuts, Crews, Agent
  catalog, Shell completion) and `docs/protocol.md` (`GET /api/paths`,
  `GET /api/git/check`, `POST /api/crews/examples`, `available` and `site` on
  `GET /api/catalog`). The round 3 decisions below describe what exists.
```

and under "Open verification (round 3)" (`:270-273`), after "- The completion scripts in a real zsh and bash session.":

```
- The built-in agents' `site` URLs (`internal/catalog/defaults.go`), each
  opened in a browser: they ship as best known and a test checks their shape
  only.
- The picker, the rail and the Launch dialog's server tab on a real phone (the
  slideover) and at 1024 px; entering fullscreen with the `F` key in a real
  browser (the headless check counts the request; headless Chromium may refuse
  it).
- The sidebar's width is no longer remembered across reloads (the rail's state
  is localStorage's alone, Nuxt UI's cookie off): confirm that is acceptable.
```

- [ ] **Step 4: `docs/architecture.md` and `AGENTS.md`**

`docs/architecture.md` (Packages, `:88-89`): the `cmd/conductor` row's list "`serve`, `host`, `notify`, `hooks`, `skill`, `up`, `crews`, `version`" gains `completion` after `crews`, and "| `internal/cli` | flag parsing only |" becomes "| `internal/cli` | flag parsing, help and completion text (the completion table and scripts, the rc-file line); no business logic |".

`AGENTS.md` (Map, `:10-11`): the `cmd/conductor` row's list gains `completion` after `crews`, and "| `internal/cli` | flags only; no business logic |" becomes "| `internal/cli` | flags, help and completion text; no business logic |".

- [ ] **Step 5: Check the docs and commit**

Run: `python3 scripts/brand_assets.py --check && make lint` (nothing in Go changed; the brand check keeps the README's asset references honest).

```bash
git add docs/protocol.md README.md docs/features.md docs/architecture.md AGENTS.md
git commit -m "docs: round 3 routes, rail, examples, availability and completion"
```

---

## Verification

1. The full gate: `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test`, `make web-build`, `make build-go`, `python3 scripts/brand_assets.py --check`. Report any check that could not run (no zsh, no git, no Chromium) by name.
2. With a home and a data directory of their own (`HOME=$PW/home CONDUCTOR_DATA_DIR=$(mktemp -d $PW/data.XXXXXX) CONDUCTOR_PUBLIC_URL=http://127.0.0.1:8099 bin/conductor serve --config conductor.example.json --listen 127.0.0.1:8099 --examples`), the log says `example crews added="[example-todo-app example-test-fixer example-docs-writer example-dependency-upgrade]"`; a second start with the same data directory says `added=[]`; the Crews page lists `Example: todo app` with lead, core, cli and tester (the tester after cli); editing its goal and restarting with `--examples` keeps the edit.
3. In the crew editor, typing the repository's path into the working directory lists its directories with `git` marks; picking one (Enter or a click) lists its children; a directory with no commit shows the warning line and the Launch tooltip repeats it; the launch of such a crew with worktrees answers `409 not_a_repo` (the button was not disabled).
4. `source <(bin/conductor completion zsh)` in an interactive zsh after `compinit`, then `conductor up <TAB>` with `CONDUCTOR_SERVER=http://127.0.0.1:8099` and `CONDUCTOR_ADMIN_TOKEN` set lists the crew ids and, with the token unset, lists nothing and prints nothing; the same in bash. `T=$(mktemp $PW/rc.XXXXXX); bin/conductor completion install --shell zsh --rc $T` appends the one line, and a second run reports `nothing to change`; `rm $T`. (Installing into the real `~/.zshrc` is the user's call.)
5. In a browser: **Ctrl+B** collapses the sidebar to the rail on `/wall`; the rail shows the mark, Launch, search, the sessions' initials with the amber dot on one flagged `needs_input` (through `POST /api/sessions/{id}/attention`), the five page icons with tooltips, the utility buttons stacked and the expand button; reload keeps the rail; a 600 px wide window shows the drawer, not the rail.
6. **F** on `/events` enters fullscreen and again leaves it; typing `f` into the session filter or the crew name field inserts the letter; `/join/<token>` has the button and the key.
7. On the Agents page a saved agent with a program that does not exist is greyed with "Not installed on <host>" and a Website link; the Launch dialog's Server tab does not list it while My machine does; a crew with it as a member is refused at launch with the member named.
