# Round 3 Plan 1: Deferred Items and Crew Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every open deferred item from rounds 1 and 2, move the default data directory to `~/.conductor`, and replace the single `crews.json` (and its 50-crew cap) with one file per crew, a paged summary list and a full-crew route.

**Architecture:** The work is ten review units grouped by theme, each going through the paths that exist (`resolveCwd`, `session.ResolvePath`, `createLocalSession`, `store.Save`) and adding no Go or npm dependency. The JSON readers share one strict decoder and one encoder in `internal/store`; the catalog gains sources and inheritance, and its editor lock is split from its snapshot lock; sessions hand the attention state to `OnActivity` with the entry; the adapters split into `home.go`, `mappers.go` and `hooks.go`. Crews keep one file per crew under `crews/` in the data directory, which now defaults to `~/.conductor`, and are listed from that directory a page at a time.

**Tech Stack:** Go 1.26 stdlib (`net/http`, `encoding/json`, `log/slog`, `os/exec` with argv), Nuxt 4.5.2 + @nuxt/ui 4.11.2 + Vue 3.5.43, vitest.

**Spec:** `docs/features.md`, section "Round 3: polish, examples and completion": "Deferred items to close (plan 1 of round 3)", "Data directory default" and "Crew storage". The spec is the authority. Item numbers below (#7, #19, …) are those of the triage that found the open items; "Triage items" lists each one in a line.

Commit messages follow the repository's `area: what changed` style. Add the commit trailers your harness asks for.

## Global Constraints

The rules from `AGENTS.md`, verbatim:

- Protocol changes touch three places together: `internal/proto`, `web/app/utils/protocol.ts` and `docs/protocol.md`. Every new frame or message gets a size limit and a test.
- Stdlib first: `net/http` mux with method patterns, `log/slog`, `encoding/json` with `DisallowUnknownFields`. New Go dependencies need a reason in the PR.
- Commands are argv arrays. Never build a shell string from user input.
- Compare tokens with `share.Equal`; store only hashes; never log query strings.
- Server session working directories go through `resolveCwd`; file reads go through `session.ResolvePath`. Do not bypass either.
- Keep per-connection bounds (read limits, queues, in-flight file requests).
- UI components come from Nuxt UI; use the brand palette in `web/app/app.config.ts` and `docs/design/brand.md`. The junction mark is artwork, never a status light.
- Do not commit `internal/web/dist` contents (only `.gitkeep`), `web/.nuxt` or `web/.output`. Commit `web/package-lock.json`.
- An API route needs a handler in `internal/api`, auth via `requireAdmin` or `authenticate`, a test, and the client call in `web/app/composables/useSessions.ts`.
- Pinned versions (`AGENTS.md`, "Pinned versions") do not change in this plan.

The binding limits and decisions from the spec, verbatim:

- "`config.Load` and `catalog.ReadFile` reject trailing data after the first JSON value, like the store and webhooks do; the store comment then tells the truth."
- "`store.Save` writes with `SetEscapeHTML(false)` so patterns and snippets stay hand-editable."
- "The catalog lock is not held across the fsync or the response: an editor lock for writers, the catalog lock only for the snapshot swap."
- "`validate` bounds `cwd`, `icon` and `adapter` (shape `^[a-z0-9-]{1,32}$`), env key and value lengths and `envPassthrough` name length, and checks the adapter id against the registry for config files too."
- "A saved override that omits `adapter` or `signal` inherits them from the built-in it replaces."
- "An override stores only the env keys whose value the editor changed; unchanged keys follow the base agent, so a secret rotated in the config reaches the agent (closes the "pinned env" item)."
- "Catalog entries carry `source` (`built-in`, `config`, `saved`) so the Agents page says Hidden or Removed truthfully."
- "The file-read deny list also covers files in the config file's directory whose name starts with the config's base name (`.bak`, `~` copies)."
- "`conductor notify` retries a 429 for attention words (bounded backoff within its 5 s budget)."
- "A hosted session with no host connected meters route reports like a connected one."
- "6to4, Teredo and SIIT forms are judged by their embedded IPv4 address; a `64:ff9b:1::/48` prefix of another length is refused with a clear message."
- "When neither `dataDir` nor `CONDUCTOR_DATA_DIR` is set, the data directory is `~/.conductor` (the home of the user running `conductor serve`), never next to the config file or in the working directory. For backward compatibility, when `~/.conductor` does not exist and the old default (`<config dir>/conductor.d` or `./conductor.d`) does, the server keeps using the old directory and logs a notice naming both paths and how to move. The host's hooks directory moves to `~/.conductor/hooks` the same way (old `~/.local/state/conductor/hooks` kept when present). The Docker image keeps its explicit `CONDUCTOR_DATA_DIR`."
- "One file per crew at `<dataDir>/crews/<id>.json`, written atomically by the store, which gains `List` and `Delete`; a corrupt file names itself in the startup error and the other crews still load."
- "`GET /api/crews?offset=0&limit=100` reads the directory and answers `{crews: [summaries], total}` (limit at most 500); `GET /api/crews/{id}` returns one crew in full for the editor; the Crews page pages through the list."
- "No cap on the number of crews; the per-crew size cap stays as a sanity bound on one file and its request body, at 1 MiB."
- "An existing `crews.json` is split into per-crew files on the first start and renamed `crews.json.migrated`."
- "Accepted as documented limitations (not changed): hosted crews stay future work."

Values this plan fixes where the spec leaves a number open (one line each, used verbatim by the tasks):

- Agent `cwd` at most 4096 bytes without NUL. `icon` matches `^[a-z0-9][a-z0-9:-]{0,63}$`. An env key and an `envPassthrough` name are at most 128 bytes, and an env value at most 16384 bytes.
- `conductor notify` waits 100 ms, 250 ms, 500 ms, 1 s and 2 s between attempts (at most six attempts), all inside the existing 5 s context.
- The crew list defaults to `limit=100`, takes `limit` from 1 to 500 and `offset` from 0 up. Any other value is `400 invalid_request`.
- `crew.MaxEncoded` is 1 MiB (1048576 bytes). It bounds a crew's file as `store.Encode` writes it, and `Validate` holds every crew a create or update sends to it. The request body itself may be up to 2 MiB (`maxCrewBody` in `internal/api/crews.go`), so a crew near the bound that a client sends indented, or escaped more than the server writes it, is accepted.
- The server counts `~/.conductor` as its data directory only once it holds server data: `catalog.json`, `crews/` or `crews.json`. `conductor host` writes `~/.conductor/hooks` for itself, so a `~/.conductor` that holds only `hooks/` (or nothing) does not end the legacy rule: an upgraded server with an old `conductor.d` keeps it, with the notice, until the operator moves it. A server that uses `~/.conductor` makes `crews/` there at its first start (Task 9).

## Review Focus

These are the five input classes the spec implies and nothing yet exercises, most likely first. Each one gets a test in the task that owns the code.

1. **An upgrade where `./conductor.d` (or `<config dir>/conductor.d`) exists and `~/.conductor` holds no server data** (it is missing, or holds only the `hooks/` a `conductor host` wrote). The server keeps the old directory, logs one warning that names both paths and how to move, and serves the old crews and catalog. Once `~/.conductor` holds server data (`catalog.json`, `crews/` or `crews.json`), it wins; a `~/.conductor` that holds only the `hooks/` a `conductor host` wrote does not, and the old directory is kept with the notice. With no home directory, startup fails and names `dataDir` and `CONDUCTOR_DATA_DIR`. Pinned in Task 1 (`TestServeKeepsAnOldDataDirectoryWithANotice`, `TestResolveDataDirDefaults`).
2. **A start that stopped halfway through the `crews.json` migration**, with some per-crew files written and `crews.json` not yet renamed. The next start finishes the move without duplicates or loss, and the start after it changes nothing. A `crews.json.migrated` from an earlier move is not overwritten, and neither is a per-crew file: a crew of `crews.json` whose file holds something else is skipped with a notice naming both files. Pinned in Task 9 (`TestMigrationFinishesAfterAnInterruptedStart`, `TestMigrationNeverOverwritesACrewFile`).
3. **A crew file that is corrupt (hand-edited, or truncated by a full disk).** The server starts, names the file in an error log, and lists the other crews. The bad file is never overwritten: a new crew with the same name takes another ID, an update answers `409 crew_unreadable`, and delete removes it. Pinned in Task 9 (`TestACorruptCrewFileIsSkippedAndNeverOverwritten`, `TestCrewRoutesAnswerForACorruptFile`).
4. **A secret rotated in the config file for a built-in or configured agent that an admin edited on the Agents page.** After a restart the agent runs with the new value. A key the admin changed keeps the admin's value, and a key the admin removed stays removed. Pinned in Task 2 (`TestCatalogOverrideFollowsTheBaseEnv`).
5. **`sudo conductor hooks install` with `HOME` preserved**, which is root writing into another user's home. The "own home" exception never covers root, so the refusal stands. Pinned in Task 6 (`TestOwnHomeExceptionNeverCoversRoot`).

## Triage items

The open items the triage of rounds 1 and 2 found, by number. Tasks name the ones they close. Numbers not listed were closed or dropped before this plan.

- **Strict JSON and the store.** #1 the store's write probe is untested. #2 the kept-previous-document test fails before the temp file. #3 the concurrent-save test reads only after the saves. #6 the store comment overclaims. #7 `config.Load` and `catalog.ReadFile` accept trailing data. #8 `store.Save` escapes `<`, `>` and `&`. #9 the catalog lock is held across the fsync and the response. #11 the end-to-end test assigns the catalog without the lock.
- **Catalog.** #12 `validate` does not bound `cwd` or `icon`, or shape `adapter`, and the adapter registry is checked for UI saves only. #13 `Upsert` keeps the caller's agent. #14 signal kinds are bare strings. #15 an override loses the replaced agent's adapter and signal. #16 `Clone` has a pointer receiver. #17 env keys, values and `envPassthrough` names are unbounded. #19 a saved copy pins env values and shadows a rotation in the config. #20 the catalog has no `source`, so the Agents page guesses Hidden or Removed. #21 no test launches an overridden built-in, hides every agent or deletes an added one.
- **Add-agent form.** #23 the form logic lives in the component, untested. #24 a typed `***` is sent as a value. #25 hand-rolled radio cards without arrow keys; `ArgvInput` copies ring classes. #26 the command check sends the whole argv. #27 an unclosed quote becomes a chip; `slugId` can end in a dash. #28 the id pattern duplicates the server's. #29 masked env, `signalOut` and the errors are untested.
- **File reads.** #30 copies of the config file (`.bak`, `~`) are readable.
- **Sessions and events.** #31 `oneLine` duplicates `CleanName`; `local_test.go` is long; the bell test polls. #32 a departing host's viewer gets two error frames and its sink is closed under `Pump`. #33 the attention state is read after the fact. #34 the SSE `activity` event has no state. #35 `conductor notify` drops a 429. #36 the host's no-launch-route line is at info. #37 a hosted session without a host spends no token.
- **Adapters and the CLI.** #38 `merge.go`, `notify.go` and `hostagent/agent.go` hold code that belongs elsewhere; the claude and cursor merge closures repeat. #39 a chmod failure on an asset stops `serve`. #40 `staleCommand` rewrites any absolute program. #41 `payloadFlags` repeats the hooks table. #42 `withYAMLListItem` mishandles a BOM and a flow-style root. #43 the hooks dir records no version. #44 a skill-only by-hand step prints the settings snippet. #45 the process's own home is refused when another uid owns it. #46 a catalog icon outside the bundle renders blank. #47 the aider card blames the missing home. #48 clipboard calls and code blocks are hand-rolled.
- **Docs.** #49 the README says the Events page shows a webhook's path; `protocol.md` names one cause of a missing hosted state.
- **Webhook addresses.** #50 6to4, Teredo and SIIT forms are not judged by their IPv4 address; a shorter-prefix `64:ff9b:1::/48` form is misread.
- **Crews, runs and links.** #51 `api_test.go` is long; the crew cap and the 503 checks are tested twice; the duplicate-names case asserts too little. #53 every `rev-parse` failure reads "not in a git repository". #54 a stop before the first start returns a stopped run without an error. #56 the handoff test has no sync point. #57 a run link can outlive a forgotten run; the forget-time disconnect is untracked; a second revoke is logged. #58 the Crews page loses a launch's link, refreshes once and never retries a run name. #59 the join page hand-codes its tiles; the crew view can leave an empty cell. #60 a run can be forgotten before its view link is minted. #61 the crew prompt placeholder may be cut short at narrow widths.

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/store/store.go` | `DecodeStrict`, `Encode`, `Save` without HTML escaping; later `Sub`, `List`, `Delete`, `LoadLimit` | 1, 9 |
| `internal/config/config.go` | strict `Load`; `ResolveDataDir` with the `~/.conductor` default and the legacy rule (`holdsServerData`); adapter check in `LoadCatalog` | 1, 2 |
| `internal/catalog/catalog.go`, `defaults.go` | strict `ReadFile`; bounds; signal kind constants; `Source`; `inherit`; value-receiver `Clone`; cloning `Upsert` | 1, 2 |
| `internal/api/catalog.go`, `server.go` | editor lock and snapshot lock; `catalogEntry` with `source`/`replaces`; `keepMaskedEnv`; tracked background work | 1, 2, 8 |
| `internal/agents/assets.go`, `home.go` (new), `merge.go`, `adapter.go`, `registry.go`, `owner_*.go`, `claude.go`, `cursor.go` | host hooks dir, `ModeError`, version record, own-home rule, `SnippetNeeded`, `CheckAdapter`, shared merge step | 1, 2, 6 |
| `web/app/utils/agentForm.ts` (new), `argv.ts`, `catalog.ts` (new), `agentIcons.ts` (new) | form logic, removal wording, icon fallback | 2, 3, 6 |
| `web/app/components/AddAgentSlideover.vue`, `ArgvInput.vue`, `CodeBlock.vue`, `IntegrationCard.vue`, `LaunchSessionModal.vue`, `ShareLinksModal.vue`, `FileBrowser.vue`, `JoinCrewGrid.vue` (new) | UI fixes and reuse | 3, 6, 8 |
| `internal/session/files.go`, `internal/api/files.go` | deny entries ending in `*` | 4 |
| `internal/session/local.go`, `activity.go`, `attention.go`, `record_test.go` (new) | state with `OnActivity`; `CleanName` reuse; test split | 5 |
| `internal/api/ws_viewer.go`, `internal/signal/hosted.go`, `internal/api/events.go`, `attention.go`, `webhooks.go` | close ordering, SSE `state`, hostless metering, comments that follow the state | 5 |
| `internal/notify/notify.go`, `mappers.go` (new) | 429 retry; mappers moved | 5, 6 |
| `internal/hostagent/agent.go`, `hooks.go` (new) | state with the entry; `injectHooks` moved, logged at warn | 1, 5, 6 |
| `internal/cli/serve.go`, `hooks.go`, `notify.go`, `up.go` | notice, `ModeError`, version skew, snippet only when needed, payload flags from one table, paged `conductor crews` | 1, 6, 9 |
| `internal/config/webhook.go` | `parseWebhooks` on `store.DecodeStrict`; 6to4, Teredo, SIIT, NAT64 local-use forms | 1, 7 |
| `internal/crew/run.go`, `handoff.go`, `worktree.go`, `store.go`, `migrate.go` (new), `crew.go` | launch and eviction fixes, sync hook, git messages, per-crew files, migration | 8, 9 |
| `internal/share/store.go`, `internal/api/links.go`, `runs.go`, `crews.go` | revoke reports a change; link creation under the run check; paged list; `GET /api/crews/{id}` | 8, 9 |
| `web/app/pages/crews/[[id]].vue`, `runs/[run].vue`, `join/[token].vue`, `agents.vue`, `layouts/default.vue`, `utils/crews.ts`, `utils/wall.ts`, `utils/events.ts`, `utils/protocol.ts`, `composables/useSessions.ts` | page fixes, paging, SSE `state` | 2, 5, 8, 9 |
| `internal/api/crews_test.go` (new) | crew and run tests split from `api_test.go` | 10 |
| `README.md`, `docs/protocol.md`, `docs/features.md`, `docs/architecture.md`, `conductor.example.json`, `AGENTS.md` | docs that change with each task, wording fixes | every task, 10 |

---

### Task 1: Strict JSON, the store, the catalog lock and the data directory default

Triage items #1, #2, #3, #6, #7, #8, #9 and #11, plus the spec's "Data directory default".

**Files:**
- Modify: `internal/store/store.go`, `internal/store/store_test.go`
- Modify: `internal/config/config.go` (`Load`, `ResolveDataDir`), `internal/config/webhook.go` (`parseWebhooks`), `internal/config/config_test.go`
- Modify: `internal/catalog/catalog.go` (`ReadFile`), `internal/catalog/catalog_test.go`
- Modify: `internal/api/server.go` (lock fields, `Catalog`), `internal/api/catalog.go` (handlers, `commitOverlay`), `internal/api/api_test.go`, `internal/api/ws_e2e_test.go:239`
- Modify: `internal/agents/assets.go` (`HostHooksDir`), `internal/agents/assets_test.go` (`TestHooksDirs`)
- Modify: `internal/hostagent/agent.go` (`injectHooks`, its `HostHooksDir` call)
- Modify: `internal/cli/serve.go`, `internal/cli/hooks.go` (`serveHooksDir`, `adoptHooksDir`, `dataDirUsage`), `internal/cli/serve_test.go`, `internal/cli/host_test.go`, `internal/cli/cli_test.go`
- Modify: `README.md` (config table, "Upgrading", "Wired at launch", hooks paragraph), `conductor.example.json`, `docs/features.md` (Round 2 "Persistence" line)

**Interfaces:**
- Consumes: nothing new.
- Produces:
  ```go
  package store
  func DecodeStrict(b []byte, v any) error            // exactly one JSON value; unknown fields, trailing data and empty input are errors
  func Encode(v any) ([]byte, error)                  // two-space indent, no HTML escaping, trailing newline: what Save writes
  var rename = os.Rename                              // test seam: a save's last step

  package config
  func (c *Config) ResolveDataDir(configPath string) (notice string, err error)
  func holdsServerData(dir string) bool              // catalog.json, crews/ or crews.json is in dir; hooks/ alone is not

  package agents
  func HostHooksDir() (dir string, legacy bool, err error) // ~/.conductor/hooks; the old XDG dir while that does not exist

  package api
  // Server fields: catalogEditMu sync.Mutex (writers, across the write); catalogMu sync.Mutex (the snapshot swap only)
  func (e *testEnv) setCatalog(cat catalog.Catalog)   // test helper

  package cli
  func serveHooksDir(dataDir string) (string, error)
  ```

- [ ] **Step 1: Write the failing store tests**

In `internal/store/store_test.go`, add `"errors"` and `"fmt"` to the imports, replace `TestConcurrentSavesLeaveValidJSON` and `TestFailedSaveKeepsPreviousDocument`, and add the two new tests:

```go
// A directory the server user cannot write is refused at Open, not at the
// first save: the write probe catches it.
func TestOpenRefusesADirectoryItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a directory of mode 0500")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if _, err := Open(dir); err == nil || !strings.Contains(err.Error(), "is not writable") {
		t.Fatalf("Open of a 0500 directory: %v", err)
	}
}

func TestConcurrentSavesLeaveValidJSON(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.Save("c.json", doc{N: -1}); err != nil {
		t.Fatal(err)
	}
	// A reader the whole time: it never finds the file torn or missing.
	stop := make(chan struct{})
	readErr := make(chan error, 1)
	go func() {
		defer close(readErr)
		for {
			select {
			case <-stop:
				return
			default:
			}
			var d doc
			if ok, err := s.Load("c.json", &d); !ok || err != nil {
				readErr <- fmt.Errorf("load during the saves: ok=%v err=%v", ok, err)
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = s.Save("c.json", doc{N: i}) }(i)
	}
	wg.Wait()
	close(stop)
	if err := <-readErr; err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir(), "c.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d doc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("torn file: %v %q", err, b)
	}
}

func TestFailedSaveKeepsPreviousDocument(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.Save("c.json", doc{N: 1}); err != nil {
		t.Fatal(err)
	}
	// An encode error stops the save before anything is written.
	if err := s.Save("c.json", make(chan int)); err == nil {
		t.Fatal("expected an encode error")
	}
	// A rename that fails stops it after the temp file is written in full.
	refused := errors.New("rename refused")
	rename = func(string, string) error { return refused }
	t.Cleanup(func() { rename = os.Rename })
	if err := s.Save("c.json", doc{N: 2}); !errors.Is(err, refused) {
		t.Fatalf("Save with a failing rename: %v", err)
	}
	var d doc
	if ok, err := s.Load("c.json", &d); !ok || err != nil || d.N != 1 {
		t.Fatalf("previous document lost: %v %v %+v", ok, err, d)
	}
	if left, _ := filepath.Glob(filepath.Join(s.Dir(), "*.tmp")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// Patterns and snippets are written as typed: <, > and & are not escaped, so
// a hand-edited catalog.json reads like what the Agents page shows.
func TestSaveWritesPatternsAsTyped(t *testing.T) {
	s, _ := Open(t.TempDir())
	type sig struct {
		Pattern string `json:"pattern"`
	}
	if err := s.Save("c.json", sig{Pattern: `^<a & b>$`}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.Dir(), "c.json"))
	if want := "{\n  \"pattern\": \"^<a & b>$\"\n}\n"; string(b) != want {
		t.Fatalf("file %q, want %q", b, want)
	}
	if enc, err := Encode(sig{Pattern: `^<a & b>$`}); err != nil || string(enc) != string(b) {
		t.Fatalf("Encode %q %v, want what Save wrote", enc, err)
	}
}
```

- [ ] **Step 2: Run the store tests to see them fail**

Run: `go test ./internal/store/ -count=1`
Expected: build failure, `undefined: rename` and `undefined: Encode`. `TestOpenRefusesADirectoryItCannotWrite` passes once the package builds: it pins behaviour that exists.

- [ ] **Step 3: Implement `DecodeStrict`, `Encode` and the rename seam**

In `internal/store/store.go`, rename `decodeStrict` to `DecodeStrict` (its body stays the same), give it this doc comment, and point `Load` at it:

```go
// DecodeStrict decodes exactly one JSON value from b into v. Unknown fields
// are errors, and so are an empty b and anything but whitespace after the
// value: unlike json.Unmarshal, Decoder.Decode stops after the first value
// and would accept the rest. Every reader of a JSON file Conductor is given
// decodes with it: the store, the config file and the catalog file.
func DecodeStrict(b []byte, v any) error {
```

Replace the second paragraph of the `Load` comment with:

```go
// Decoding is strict (DecodeStrict), as it is for the config file and the
// catalog file: an unknown field, an empty file or anything after the JSON
// value is an error, so a typo in a hand-edited file is caught instead of
// silently dropped. A load error is a real error: callers must surface it and
// never treat the document as empty, or their next Save would overwrite what
// is on disk. v may be partly filled after an error.
```

Add `Encode` and the seam, and make `Save` use them:

```go
// rename is os.Rename: the last step of a save. A test replaces it to make a
// save fail after the temp file is written.
var rename = os.Rename

// Encode is v as Save writes it: indented with two spaces, ending in a
// newline, with <, > and & as they are, so patterns and snippets stay
// hand-editable.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
```

In `Save`, replace `b, err := json.MarshalIndent(v, "", "  ")` with `b, err := Encode(v)`, write `b` itself (`tmp.Write(b)`, since `Encode` ends with the newline), and end with `return rename(tmp.Name(), final)`. Change the doc comment's "as indented JSON" to "as Encode writes it".

- [ ] **Step 4: Run the store tests to see them pass**

Run: `go test ./internal/store/ -count=1 -race`
Expected: PASS.

- [ ] **Step 5: Write the failing strict-reader tests**

Append to `internal/config/config_test.go`:

```go
// The config file is held to what the store holds its files to: one JSON
// value and nothing after it.
func TestLoadRejectsTrailingDataAndEmptyFiles(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"listen":":9000"} oops`, "after the JSON value"},
		{`{"listen":":9000"}{"listen":":9001"}`, "more than one JSON value"},
		{"", "empty document"},
	} {
		path := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: %v, want an error naming %s and saying %q", tc.body, err, path, tc.want)
		}
	}
}
```

Append to `internal/catalog/catalog_test.go` (add `"os"` and `"path/filepath"` to its imports):

```go
func TestReadFileRejectsTrailingData(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"agents":[]} x`, "after the JSON value"},
		{`{"agents":[]}{"agents":[]}`, "more than one JSON value"},
		{"", "empty document"},
	} {
		path := filepath.Join(t.TempDir(), "agents.json")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadFile(path); err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: %v", tc.body, err)
		}
	}
}
```

- [ ] **Step 6: Run them to see them fail**

Run: `go test ./internal/config/ ./internal/catalog/ -run 'TrailingData' -count=1`
Expected: FAIL. The first case of each passes with no error, and the empty file says `EOF`.

- [ ] **Step 7: Use `store.DecodeStrict` in both readers**

`internal/config/config.go` imports `github.com/phenixrizen/conductor/internal/store`. Drop the `bytes` import if nothing else uses it. The file branch of `Load` becomes:

```go
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := store.DecodeStrict(b, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		cfg.Path = path
		if abs, err := filepath.Abs(path); err == nil {
			cfg.Path = abs
		}
	}
```

`internal/config/webhook.go` reads `CONDUCTOR_WEBHOOKS` with the same decoder in place of its hand-rolled one. It drops the `bytes`, `encoding/json` and `io` imports, which nothing else there uses, and imports `internal/store`:

```go
// parseWebhooks reads the value of CONDUCTOR_WEBHOOKS: a JSON array of
// webhooks, as the config file holds them, and nothing after it
// (store.DecodeStrict, as for the config file itself).
func parseWebhooks(v string) ([]Webhook, error) {
	var hooks []Webhook
	if err := store.DecodeStrict([]byte(v), &hooks); err != nil {
		return nil, err
	}
	return hooks, nil
}
```

The existing `TestWebhooksFromFileAndEnvironment` pins it: every malformed value it lists (`[] []` included, now "more than one JSON value") is still refused with an error that names `CONDUCTOR_WEBHOOKS` and not the secret.

`internal/catalog/catalog.go` imports `internal/store` (which imports nothing of Conductor's, so no cycle) and drops `bytes`:

```go
// ReadFile parses a catalog file: one JSON value, no unknown field, nothing
// after it (store.DecodeStrict).
func ReadFile(path string) (File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read catalog: %w", err)
	}
	var f File
	if err := store.DecodeStrict(b, &f); err != nil {
		return File{}, fmt.Errorf("parse catalog %s: %w", path, err)
	}
	return f, nil
}
```

- [ ] **Step 8: Run them to see them pass**

Run: `go test ./internal/config/ ./internal/catalog/ ./internal/store/ -count=1`
Expected: PASS, `TestWebhooksFromFileAndEnvironment` included.

- [ ] **Step 9: Write the failing catalog-lock test**

Add to `internal/api/api_test.go`:

```go
// Readers and launches take the catalog lock only for the snapshot: an edit
// writing catalog.json (it holds the editor lock across the fsync) makes none
// of them wait.
func TestCatalogReadersDoNotWaitForAnEdit(t *testing.T) {
	e := newTestEnv(t, nil)
	e.srv.catalogEditMu.Lock() // an edit in the middle of its write
	defer e.srv.catalogEditMu.Unlock()
	statuses := make(chan int, 2)
	go func() {
		for _, r := range []struct{ method, path string; body any }{
			{"GET", "/api/catalog", nil},
			{"POST", "/api/sessions", map[string]any{"agentId": "cat"}},
		} {
			var rd io.Reader
			if r.body != nil {
				b, _ := json.Marshal(r.body)
				rd = bytes.NewReader(b)
			}
			req, _ := http.NewRequest(r.method, e.http.URL+r.path, rd)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			resp, err := e.client.Do(req)
			if err != nil {
				statuses <- 0
				continue
			}
			resp.Body.Close()
			statuses <- resp.StatusCode
		}
	}()
	for _, want := range []int{http.StatusOK, http.StatusCreated} {
		select {
		case got := <-statuses:
			if got != want {
				t.Fatalf("status %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a reader waited for the editor lock")
		}
	}
	e.stopEverything(t)
}
```

`e.stopEverything` already exists in `api_test.go`, and it stops the `cat` session the test started.

- [ ] **Step 10: Run it to see it fail**

Run: `go test ./internal/api/ -run TestCatalogReadersDoNotWaitForAnEdit -count=1`
Expected: build failure, `e.srv.catalogEditMu undefined`.

- [ ] **Step 11: Split the editor lock from the snapshot lock**

In `internal/api/server.go`, replace the `catalogMu` block of `Server` with:

```go
	// catalogEditMu serialises the catalog's editors. An edit holds it from
	// reading overlay to publishing the new catalog, across the write and
	// fsync of catalog.json, so two admins cannot lose each other's change.
	// Readers never take it.
	catalogEditMu sync.Mutex
	// catalogMu guards overlay and catalog for the instant they are read or
	// swapped. catalog is the effective catalog, base with overlay applied;
	// it is replaced as a whole and never edited in place, so a copy taken
	// under the lock (see Catalog) is a stable snapshot. An editor also holds
	// catalogEditMu, so it may read overlay without this lock.
	catalogMu sync.Mutex
	base      catalog.Catalog // the configured catalog, before the overlay; never changed after New
	overlay   catalog.Overlay // the UI-managed layer, as saved in catalog.json
	catalog   catalog.Catalog
```

In `internal/api/catalog.go`, the three handlers hold the editor lock for the edit, and answer after they release it. `commitOverlay` takes `catalogMu` only to publish:

```go
func (s *Server) handleSaveAgent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	var a catalog.Agent
	if err := decodeJSON(w, r, &a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	saved, aerr := s.saveAgent(a)
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": saved.Redacted()})
}

// saveAgent adds a to the overlay, or replaces the entry with its ID, and
// returns it as the catalog now lists it. It holds catalogEditMu from reading
// the stored agent to publishing the result; readers and launches wait for
// none of it.
func (s *Server) saveAgent(a catalog.Agent) (catalog.Agent, *apiError) {
	s.catalogEditMu.Lock()
	defer s.catalogEditMu.Unlock()
	// The stored agent is read under the editor lock, so no other save can
	// change it in between.
	stored, _ := s.Catalog().Get(a.ID)
	if err := restoreMaskedEnv(&a, stored); err != nil {
		return catalog.Agent{}, newAPIError(http.StatusBadRequest, "invalid_agent", err.Error())
	}
	// Check a by itself, so the message is about this agent and not an index
	// into the overlay.
	var probe catalog.Catalog
	if err := probe.Upsert(a); err != nil {
		return catalog.Agent{}, newAPIError(http.StatusBadRequest, "invalid_agent", err.Error())
	}
	if err := checkAdapter(a); err != nil {
		return catalog.Agent{}, newAPIError(http.StatusBadRequest, "invalid_agent", err.Error())
	}
	ov := s.overlay
	ov.Agents = upsertAgent(ov.Agents, a)
	ov.Hidden = withoutID(ov.Hidden, a.ID)
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", a.ID, "err", err)
		return catalog.Agent{}, newAPIError(http.StatusInternalServerError, "store_failed", "could not save the catalog")
	}
	s.log.Info("catalog agent saved", "agent", a.ID)
	saved, _ := s.Catalog().Get(a.ID)
	return saved, nil
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	if aerr := s.deleteAgent(r.PathValue("id")); aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteAgent removes the overlay entry with the given ID, or hides an agent
// that has none, under catalogEditMu.
func (s *Server) deleteAgent(id string) *apiError {
	s.catalogEditMu.Lock()
	defer s.catalogEditMu.Unlock()
	ov := s.overlay
	inOverlay := slices.ContainsFunc(ov.Agents, func(a catalog.Agent) bool { return a.ID == id })
	_, listed := s.Catalog().Get(id)
	action := "removed"
	switch {
	case inOverlay:
		ov.Agents = withoutAgent(ov.Agents, id)
	case listed:
		ov.Hidden = append(slices.Clone(ov.Hidden), id)
		action = "hidden"
	default:
		return newAPIError(http.StatusNotFound, "not_found", "no such agent")
	}
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", id, "err", err)
		return newAPIError(http.StatusInternalServerError, "store_failed", "could not save the catalog")
	}
	s.log.Info("catalog agent changed", "agent", id, "action", action)
	return nil
}

func (s *Server) handleUnhideAgent(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	a, found, aerr := s.unhideAgent(r.PathValue("id"))
	if aerr != nil {
		writeAPIError(w, aerr)
		return
	}
	out := map[string]any{}
	if found {
		out["agent"] = a.Redacted()
	}
	writeJSON(w, http.StatusOK, out)
}

// unhideAgent takes id off the hidden list under catalogEditMu and returns the
// agent it brings back, if one has that ID.
func (s *Server) unhideAgent(id string) (catalog.Agent, bool, *apiError) {
	s.catalogEditMu.Lock()
	defer s.catalogEditMu.Unlock()
	ov := s.overlay
	if !slices.Contains(ov.Hidden, id) {
		return catalog.Agent{}, false, newAPIError(http.StatusNotFound, "not_found", "no hidden agent with that id")
	}
	ov.Hidden = withoutID(ov.Hidden, id)
	if err := s.commitOverlay(ov); err != nil {
		s.log.Error("catalog save failed", "agent", id, "err", err)
		return catalog.Agent{}, false, newAPIError(http.StatusInternalServerError, "store_failed", "could not save the catalog")
	}
	s.log.Info("catalog agent changed", "agent", id, "action", "unhidden")
	a, ok := s.Catalog().Get(id)
	return a, ok, nil
}

// commitOverlay makes ov the current overlay. It builds the catalog ov yields
// from the configured one and writes ov to catalog.json without catalogMu.
// Only then does it publish both, under catalogMu for that instant, so a
// failure leaves the running catalog and the file as they were. Deriving the
// catalog from base and the overlay, as a restart does, keeps the two
// identical. Agents is saved as [] rather than null. The caller holds
// catalogEditMu.
func (s *Server) commitOverlay(ov catalog.Overlay) error {
	next := s.base.Clone()
	if err := next.ApplyOverlay(ov); err != nil {
		return fmt.Errorf("apply overlay: %w", err)
	}
	if ov.Agents == nil {
		ov.Agents = []catalog.Agent{}
	}
	if err := s.store.Save(catalogFile, ov); err != nil {
		return fmt.Errorf("save %s: %w", catalogFile, err)
	}
	s.catalogMu.Lock()
	s.overlay, s.catalog = ov, next
	s.catalogMu.Unlock()
	return nil
}
```

In `internal/api/ws_e2e_test.go`, replace `e.srv.catalog = cat` (line 239) with `e.setCatalog(cat)`, and add the helper next to `serve` in `api_test.go`:

```go
// setCatalog replaces the server's catalog as an edit publishes one: under
// the lock its readers take.
func (e *testEnv) setCatalog(cat catalog.Catalog) {
	e.srv.catalogMu.Lock()
	e.srv.catalog = cat
	e.srv.catalogMu.Unlock()
}
```

- [ ] **Step 12: Run the API tests to see them pass**

Run: `go test ./internal/api/ -count=1 -race -run 'Catalog|Attention'`
Expected: PASS, `TestCatalogConcurrentEditsAreAllKept` included.

- [ ] **Step 13: Write the failing data-directory tests**

In `internal/config/config_test.go`, replace `TestResolveDataDirDefaults` with:

```go
func TestResolveDataDirDefaults(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// home gives the test a home directory of its own.
	home := func(t *testing.T) string {
		t.Helper()
		h := t.TempDir()
		t.Setenv("HOME", h)
		return h
	}
	resolve := func(t *testing.T, cfg *Config, configPath string) string {
		t.Helper()
		notice, err := cfg.ResolveDataDir(configPath)
		if err != nil {
			t.Fatal(err)
		}
		// Agent processes started in other working directories are handed
		// paths under it.
		if !filepath.IsAbs(cfg.DataDir) {
			t.Fatalf("DataDir %q is not absolute", cfg.DataDir)
		}
		return notice
	}
	mkdir := func(t *testing.T, p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "conductor.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("unset, it is ~/.conductor wherever the config is", func(t *testing.T) {
		h := home(t)
		t.Chdir(t.TempDir())
		for _, configPath := range []string{"", "/etc/x/conductor.json", "conf/conductor.json"} {
			cfg := Defaults()
			if notice := resolve(t, cfg, configPath); notice != "" || cfg.DataDir != filepath.Join(h, ".conductor") {
				t.Fatalf("config %q: DataDir %q, notice %q", configPath, cfg.DataDir, notice)
			}
		}
	})

	t.Run("an old ./conductor.d is kept while ~/.conductor does not exist", func(t *testing.T) {
		h := home(t)
		dir := t.TempDir()
		t.Chdir(dir)
		old := filepath.Join(dir, "conductor.d")
		mkdir(t, old)
		cfg := Defaults()
		notice := resolve(t, cfg, "")
		if cfg.DataDir != old {
			t.Fatalf("DataDir %q, want the old %q", cfg.DataDir, old)
		}
		for _, want := range []string{old, filepath.Join(h, ".conductor"), "dataDir", "CONDUCTOR_DATA_DIR"} {
			if !strings.Contains(notice, want) {
				t.Errorf("notice %q does not mention %q", notice, want)
			}
		}
	})

	t.Run("an old conductor.d next to the config file is kept", func(t *testing.T) {
		home(t)
		t.Chdir(t.TempDir())
		dir := t.TempDir()
		old := filepath.Join(dir, "conductor.d")
		mkdir(t, old)
		cfg := Defaults()
		if notice := resolve(t, cfg, filepath.Join(dir, "conductor.json")); cfg.DataDir != old || notice == "" {
			t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
		}
	})

	// conductor host writes ~/.conductor/hooks for itself. That is no data of
	// a server's: an upgraded server keeps its old directory, and says so,
	// until the operator moves it.
	t.Run("host created ~/.conductor/hooks, legacy conductor.d present: legacy kept with the notice", func(t *testing.T) {
		h := home(t)
		dir := t.TempDir()
		t.Chdir(dir)
		old := filepath.Join(dir, "conductor.d")
		mkdir(t, old)
		mkdir(t, filepath.Join(h, ".conductor", "hooks"))
		cfg := Defaults()
		notice := resolve(t, cfg, "")
		if cfg.DataDir != old {
			t.Fatalf("DataDir %q, want the old %q", cfg.DataDir, old)
		}
		for _, want := range []string{old, filepath.Join(h, ".conductor"), "dataDir", "CONDUCTOR_DATA_DIR"} {
			if !strings.Contains(notice, want) {
				t.Errorf("notice %q does not mention %q", notice, want)
			}
		}
		// Without an old directory, that ~/.conductor is the data directory.
		if err := os.Remove(old); err != nil {
			t.Fatal(err)
		}
		cfg = Defaults()
		if notice := resolve(t, cfg, ""); cfg.DataDir != filepath.Join(h, ".conductor") || notice != "" {
			t.Fatalf("without the old directory: DataDir %q, notice %q", cfg.DataDir, notice)
		}
	})

	t.Run("~/.conductor wins once it holds server data", func(t *testing.T) {
		for _, data := range []string{"catalog.json", "crews", "crews.json"} {
			t.Run(data, func(t *testing.T) {
				h := home(t)
				dir := t.TempDir()
				t.Chdir(dir)
				mkdir(t, filepath.Join(dir, "conductor.d"))
				def := filepath.Join(h, ".conductor")
				mkdir(t, filepath.Join(def, "hooks"))
				if data == "crews" {
					mkdir(t, filepath.Join(def, data))
				} else if err := os.WriteFile(filepath.Join(def, data), []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg := Defaults()
				if notice := resolve(t, cfg, ""); cfg.DataDir != def || notice != "" {
					t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
				}
			})
		}
	})

	t.Run("a file called conductor.d is no old data directory", func(t *testing.T) {
		h := home(t)
		dir := t.TempDir()
		t.Chdir(dir)
		if err := os.WriteFile(filepath.Join(dir, "conductor.d"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Defaults()
		if notice := resolve(t, cfg, ""); cfg.DataDir != filepath.Join(h, ".conductor") || notice != "" {
			t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
		}
	})

	t.Run("without a home directory the error names the settings", func(t *testing.T) {
		t.Setenv("HOME", "")
		cfg := Defaults()
		_, err := cfg.ResolveDataDir("")
		if err == nil || !strings.Contains(err.Error(), "dataDir") || !strings.Contains(err.Error(), "CONDUCTOR_DATA_DIR") {
			t.Fatalf("no home: %v", err)
		}
	})

	t.Run("an absolute value from the config file is kept", func(t *testing.T) {
		home(t)
		t.Setenv("CONDUCTOR_DATA_DIR", "")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if notice := resolve(t, cfg, path); cfg.DataDir != "/srv/from-file" || notice != "" {
			t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
		}
	})

	t.Run("a relative value from the config file is made absolute", func(t *testing.T) {
		home(t)
		t.Setenv("CONDUCTOR_DATA_DIR", "")
		path := writeConfig(t, `{"dataDir":"state/data"}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		resolve(t, cfg, path)
		// Relative to the current directory like allowedRoots and defaultCwd,
		// not to the config file.
		if cfg.DataDir != filepath.Join(cwd, "state", "data") {
			t.Fatalf("DataDir %q", cfg.DataDir)
		}
	})

	t.Run("CONDUCTOR_DATA_DIR wins and is kept", func(t *testing.T) {
		home(t)
		t.Setenv("CONDUCTOR_DATA_DIR", "/srv/from-env")
		path := writeConfig(t, `{"dataDir":"/srv/from-file"}`)
		for _, configPath := range []string{path, ""} {
			cfg, err := Load(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if notice := resolve(t, cfg, configPath); cfg.DataDir != "/srv/from-env" || notice != "" {
				t.Fatalf("DataDir %q, notice %q", cfg.DataDir, notice)
			}
		}
	})

	t.Run("a relative CONDUCTOR_DATA_DIR is made absolute", func(t *testing.T) {
		home(t)
		t.Setenv("CONDUCTOR_DATA_DIR", "rel/dir")
		cfg, err := Load("")
		if err != nil {
			t.Fatal(err)
		}
		if resolve(t, cfg, ""); cfg.DataDir != filepath.Join(cwd, "rel", "dir") {
			t.Fatalf("DataDir %q", cfg.DataDir)
		}
	})

	// A value that was set stays relative when the working directory is gone:
	// filepath.Abs needs it, and the chosen value beats an empty one. The
	// default comes from the home directory and is absolute regardless.
	t.Run("the working directory is gone", func(t *testing.T) {
		h := home(t)
		gone := t.TempDir()
		t.Chdir(gone)
		if err := os.Remove(gone); err != nil {
			t.Skipf("cannot remove the working directory: %v", err)
		}
		if _, err := filepath.Abs("x"); err == nil {
			t.Skip("the working directory is still resolvable on this platform")
		}
		cfg := Defaults()
		cfg.DataDir = "rel/dir"
		if _, err := cfg.ResolveDataDir(""); err != nil || cfg.DataDir != "rel/dir" {
			t.Fatalf("explicit value: DataDir %q %v", cfg.DataDir, err)
		}
		cfg = Defaults()
		if _, err := cfg.ResolveDataDir(""); err != nil || cfg.DataDir != filepath.Join(h, ".conductor") {
			t.Fatalf("default: DataDir %q %v", cfg.DataDir, err)
		}
	})
}
```

In `internal/agents/assets_test.go`, replace `TestHooksDirs` with:

```go
func TestHooksDirs(t *testing.T) {
	if got := HooksDir("/var/lib/conductor"); got != "/var/lib/conductor/hooks" {
		t.Fatalf("HooksDir = %q", got)
	}
	if got := HooksDir(""); got != "" {
		t.Fatalf("HooksDir without a data dir = %q", got)
	}
	host := func(t *testing.T) (string, bool) {
		t.Helper()
		dir, legacy, err := HostHooksDir()
		if err != nil {
			t.Fatal(err)
		}
		return dir, legacy
	}
	mkdir := func(t *testing.T, p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("the default is ~/.conductor/hooks", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_STATE_HOME", t.TempDir()) // no old directory in it
		if dir, legacy := host(t); dir != filepath.Join(home, ".conductor", "hooks") || legacy {
			t.Fatalf("%q legacy=%v", dir, legacy)
		}
	})
	t.Run("an old directory under XDG_STATE_HOME is kept", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		state := t.TempDir()
		t.Setenv("XDG_STATE_HOME", state)
		old := filepath.Join(state, "conductor", "hooks")
		mkdir(t, old)
		if dir, legacy := host(t); dir != old || !legacy {
			t.Fatalf("%q legacy=%v, want %q", dir, legacy, old)
		}
	})
	// The XDG spec says to ignore a relative XDG path; so does an empty one.
	for _, xdg := range []string{"", "state"} {
		t.Run("an old ~/.local/state directory is kept, XDG_STATE_HOME="+xdg, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", xdg)
			old := filepath.Join(home, ".local", "state", "conductor", "hooks")
			mkdir(t, old)
			if dir, legacy := host(t); dir != old || !legacy {
				t.Fatalf("%q legacy=%v, want %q", dir, legacy, old)
			}
		})
	}
	t.Run("~/.conductor/hooks wins once it exists", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_STATE_HOME", "")
		mkdir(t, filepath.Join(home, ".local", "state", "conductor", "hooks"))
		mkdir(t, filepath.Join(home, ".conductor", "hooks"))
		if dir, legacy := host(t); dir != filepath.Join(home, ".conductor", "hooks") || legacy {
			t.Fatalf("%q legacy=%v", dir, legacy)
		}
	})
	// The host's directory is its own. A server's old ./conductor.d, which
	// the server keeps by the legacy rule, is not where the host writes, and
	// the host's ~/.conductor/hooks does not end that rule
	// (config.ResolveDataDir, TestResolveDataDirDefaults).
	t.Run("a server's old ./conductor.d is not the host's", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_STATE_HOME", "")
		cwd := t.TempDir()
		mkdir(t, filepath.Join(cwd, "conductor.d", "hooks"))
		t.Chdir(cwd)
		if dir, legacy := host(t); dir != filepath.Join(home, ".conductor", "hooks") || legacy {
			t.Fatalf("%q legacy=%v", dir, legacy)
		}
	})
}
```

In `internal/cli/serve_test.go`, give every test a home of its own, rewrite the two tests that relied on `conductor.d` next to the config, and add two. `clearConductorEnv` gains one line at its end:

```go
	// A home of the test's own: nothing a test starts reads or writes the
	// user's ~/.conductor.
	t.Setenv("HOME", t.TempDir())
```

```go
// Without dataDir the directory is ~/.conductor. Here the home is the
// allowed root: agents work there, so the server says so.
func TestServeWarnsWhenTheDataDirOverlapsAnAllowedRoot(t *testing.T) {
	clearConductorEnv(t)
	home := os.Getenv("HOME")
	cfg := writeServeConfig(t, t.TempDir(), fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q}`, home, home))
	logs := serveUntilListening(t, "--config", cfg)
	data := filepath.Join(home, ".conductor")
	if lines := logLines(logs, "level=WARN", "dataDir="+data, "allowedRoot="+home); len(lines) != 1 {
		t.Fatalf("no warning that %s is inside the allowed root %s:\n%s", data, home, logs)
	}
}

// A data directory that cannot be made stops the server, which says which
// settings choose another.
func TestServeNamesTheSettingWhenTheDataDirIsNotUsable(t *testing.T) {
	clearConductorEnv(t)
	cfg := writeServeConfig(t, t.TempDir(), `{"adminToken": "t"}`)
	data := filepath.Join(os.Getenv("HOME"), ".conductor")
	// A file where the directory should be defeats MkdirAll even for root.
	if err := os.WriteFile(data, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs syncBuffer
	code, err := runServe(context.Background(), []string{"--config", cfg, "--listen", "127.0.0.1:0"}, io.Discard, &logs)
	if code != 1 || err == nil {
		t.Fatalf("serve started with an unusable data directory: %d %v", code, err)
	}
	for _, want := range []string{data, "dataDir", "CONDUCTOR_DATA_DIR", "not a directory"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// An upgraded server whose data directory an older Conductor put next to the
// config keeps using it while ~/.conductor holds no server data, and says
// once, at warn, where it is, where the default is and how to move.
func TestServeKeepsAnOldDataDirectoryWithANotice(t *testing.T) {
	clearConductorEnv(t)
	dir := t.TempDir()
	work, old := filepath.Join(dir, "work"), filepath.Join(dir, "conductor.d")
	for _, d := range []string{work, old} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q}`, work, work))
	logs := serveUntilListening(t, "--config", cfg)
	def := filepath.Join(os.Getenv("HOME"), ".conductor")
	if lines := logLines(logs, "level=WARN", old, def); len(lines) != 1 {
		t.Fatalf("no notice naming %s and %s:\n%s", old, def, logs)
	}
	if lines := logLines(logs, "conductor serving", "dataDir="+old); len(lines) != 1 {
		t.Fatalf("the server does not use %s:\n%s", old, logs)
	}
	if _, err := os.Stat(def); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s was made: %v", def, err)
	}
}

// Without dataDir and without an old directory the server makes ~/.conductor,
// 0700, names it on its serving line and warns about nothing it holds. (The
// test binary embeds no web UI, which is a warning of its own.)
func TestServeUsesTheHomeDataDirectory(t *testing.T) {
	clearConductorEnv(t)
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := writeServeConfig(t, dir, fmt.Sprintf(`{"adminToken": "t", "allowedRoots": [%q], "defaultCwd": %q}`, work, work))
	logs := serveUntilListening(t, "--config", cfg)
	data := filepath.Join(os.Getenv("HOME"), ".conductor")
	if lines := logLines(logs, "conductor serving", "dataDir="+data); len(lines) != 1 {
		t.Fatalf("the serving line does not name %s:\n%s", data, logs)
	}
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%s: %v %v", data, fi, err)
	}
	if lines := logLines(logs, "level=WARN", ".conductor"); len(lines) != 0 {
		t.Fatalf("unexpected data directory warnings: %v", lines)
	}
}
```

Add `"errors"` and `"io/fs"` to `serve_test.go`'s imports if they are missing.

In `internal/cli/host_test.go`, `TestHostAgentFlagInjectsTheAdapter` keeps `XDG_STATE_HOME` set (it holds no old directory, so it is ignored), and its expected path becomes:

```go
	settings := filepath.Join(os.Getenv("HOME"), ".conductor", "hooks", "claude.json")
```

Add to `internal/cli/cli_test.go`:

```go
// conductor hooks takes the hooks from the data directory conductor serve
// would use: an old ./conductor.d while ~/.conductor holds only hooks/ (which
// conductor host writes there too), and ~/.conductor once it holds a
// server's data.
func TestHooksInstallTakesTheHomeDataDir(t *testing.T) {
	clearConductorEnv(t)
	t.Cleanup(agents.ForgetBinary())
	home := os.Getenv("HOME")
	fromHome := fakeBinary(t)
	serverAssets(t, filepath.Join(home, ".conductor"), fromHome)
	cwd, fromCwd := t.TempDir(), fakeBinary(t)
	serverAssets(t, filepath.Join(cwd, "conductor.d"), fromCwd)
	t.Chdir(cwd)
	install := func(want string) {
		t.Helper()
		target := t.TempDir()
		var out, errOut bytes.Buffer
		if code, err := runHooks(t.Context(), []string{"install", "copilot", "--home", target}, &out, &errOut); code != 0 || err != nil {
			t.Fatalf("exit %d %v\n%s%s", code, err, &out, &errOut)
		}
		b, _ := os.ReadFile(filepath.Join(target, ".copilot", "hooks", "conductor.json"))
		if !strings.Contains(string(b), `"`+want+` notify --copilot-hook"`) {
			t.Fatalf("installed, want %s:\n%s", want, b)
		}
	}
	install(fromCwd) // ~/.conductor holds hooks/ only
	if err := os.WriteFile(filepath.Join(home, ".conductor", "catalog.json"), []byte(`{"agents": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	install(fromHome)
}
```

- [ ] **Step 14: Run them to see them fail**

Run: `go test ./internal/config/ ./internal/agents/ ./internal/cli/ -count=1 -run 'ResolveDataDir|HooksDirs|Serve|HostAgentFlag|TakesTheHomeDataDir'`
Expected: build failures. `ResolveDataDir` returns nothing yet, and `HostHooksDir` returns two values.

- [ ] **Step 15: Implement the default and the legacy rule**

In `internal/config/config.go`, replace `ResolveDataDir`:

```go
// ResolveDataDir fills DataDir when neither dataDir nor CONDUCTOR_DATA_DIR set
// it: ~/.conductor, in the home of the user running conductor serve, never
// next to the config file or in the working directory. An older Conductor
// chose conductor.d next to the config file, or in the working directory
// without one. While ~/.conductor holds no server data (holdsServerData) and
// that directory exists, it is kept, and notice says so, naming both and how
// to move. conductor host writes ~/.conductor/hooks for itself, so hooks/
// alone does not end the rule. With no value set and no home directory, the
// error names the settings that choose one.
//
// The result is absolute: agent processes started in other working
// directories are handed paths under it. A value that was set is made
// absolute relative to the current directory, like allowedRoots and
// defaultCwd, and is kept as it is when the working directory cannot be
// determined.
func (c *Config) ResolveDataDir(configPath string) (notice string, err error) {
	if c.DataDir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil || !filepath.IsAbs(home) {
			return "", errors.New("the data directory defaults to ~/.conductor, but the home directory is unknown: set dataDir in the config, or CONDUCTOR_DATA_DIR")
		}
		def := filepath.Join(home, ".conductor")
		c.DataDir = def
		if !holdsServerData(def) {
			if old, aerr := filepath.Abs(legacyDataDir(configPath)); aerr == nil && isDir(old) {
				c.DataDir = old
				notice = fmt.Sprintf("using the data directory %s, where an older Conductor put it; the default is now %s, which holds no server data yet (catalog.json, crews/ or crews.json). To move it, stop the server, move the files in %s into %s (hooks/ need not move: the server writes it at every start) and start it again; to keep it where it is, set dataDir or CONDUCTOR_DATA_DIR to it", old, def, old, def)
			}
		}
	}
	if abs, aerr := filepath.Abs(c.DataDir); aerr == nil {
		c.DataDir = abs
	}
	return notice, nil
}

// serverData names what only a server writes in its data directory: the
// Agents page's overlay and the crews, in the layout of this version and of
// the one before. conductor host writes hooks/ into ~/.conductor too, so
// hooks/ is not among them.
var serverData = []string{"catalog.json", "crews", "crews.json"}

// holdsServerData reports whether dir holds anything in serverData, as a
// file, a directory or a link.
func holdsServerData(dir string) bool {
	for _, name := range serverData {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// legacyDataDir is where an older Conductor put the data directory by
// default: conductor.d next to the config file, or in the working directory
// without one.
func legacyDataDir(configPath string) string {
	base := "."
	if configPath != "" {
		base = filepath.Dir(configPath)
	}
	return filepath.Join(base, "conductor.d")
}

// isDir reports whether p is a directory, following links.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
```

In `internal/agents/assets.go`, replace `HostHooksDir`:

```go
// HostHooksDir is where conductor host keeps the hook assets it injects:
// hooks in ~/.conductor, the data directory conductor serve uses by default.
// An older Conductor kept them in $XDG_STATE_HOME/conductor/hooks, or in
// ~/.local/state/conductor/hooks when XDG_STATE_HOME is unset or, against the
// XDG spec, not absolute. While ~/.conductor/hooks does not exist and that
// directory does, it is kept, and legacy is true. conductor serve does not
// count a ~/.conductor that holds only hooks/ as its data
// (config.ResolveDataDir), so what the host writes here never moves a
// server's data directory.
func HostHooksDir() (dir string, legacy bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(home) {
		return "", false, fmt.Errorf("the home directory %q is not an absolute path", home)
	}
	def := filepath.Join(home, ".conductor", "hooks")
	if dirExists(def) {
		return def, false, nil
	}
	old := filepath.Join(home, ".local", "state", "conductor", "hooks")
	if state := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(state) {
		old = filepath.Join(state, "conductor", "hooks")
	}
	if dirExists(old) {
		return old, true, nil
	}
	return def, false, nil
}

// dirExists reports whether p is a directory, following links.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
```

When the same user runs `conductor serve` with the default data directory and `conductor host` on one machine, both write `~/.conductor/hooks`. That is intended: the assets are the same files for the same binary. When the host ran first and an upgraded server still has an old `conductor.d`, the server keeps that one with the notice: hooks/ alone is no server data.

In `internal/hostagent/agent.go`, `injectHooks` takes the new result:

```go
	dir := opts.HooksDir
	bin, err := agents.BinaryPath()
	if err == nil && dir == "" {
		var legacy bool
		if dir, legacy, err = agents.HostHooksDir(); err == nil && legacy {
			opts.Log.Info("the hook assets stay in the directory an older Conductor used; the default is now ~/.conductor/hooks, where they go once this one is removed", "dir", dir)
		}
	}
```

In `internal/cli/serve.go`, replace `cfg.ResolveDataDir(*configPath)` with:

```go
	notice, err := cfg.ResolveDataDir(*configPath)
	if err != nil {
		return 1, err
	}
	if notice != "" {
		log.Warn(notice)
	}
```

The overlap warning text becomes:

```go
		log.Warn("the data directory overlaps an allowed root: agents working there can read it and commit its secrets, and the file viewer refuses it; keep it outside allowedRoots (the default is ~/.conductor), or set dataDir or CONDUCTOR_DATA_DIR to a directory outside them", "dataDir", cfg.DataDir, "allowedRoot", root)
```

In `internal/cli/hooks.go`:

```go
const dataDirUsage = "data directory of conductor serve, whose hooks/ holds the hooks and names the binary they run (default: CONDUCTOR_DATA_DIR, else ~/.conductor, or ./conductor.d where an older conductor left one and ~/.conductor holds no server data)"

// serveHooksDir is the hooks dir of the data directory dataDir or, when it is
// empty, of the one conductor serve uses without dataDir in its config:
// CONDUCTOR_DATA_DIR, else ~/.conductor (or an older ./conductor.d while
// ~/.conductor holds no server data).
func serveHooksDir(dataDir string) (string, error) {
	cfg := config.Config{DataDir: dataDir}
	if cfg.DataDir == "" {
		cfg.DataDir = os.Getenv("CONDUCTOR_DATA_DIR")
	}
	if _, err := cfg.ResolveDataDir(""); err != nil {
		return "", err
	}
	return agents.HooksDir(cfg.DataDir), nil
}
```

`adoptHooksDir` starts with:

```go
	hooksDir, err := serveHooksDir(dataDir)
	if err != nil {
		return "", err
	}
```

Update the comments in `cli_test.go` (`TestHooksInstallFindsTheDataDirLikeServe`) and `serve_test.go` that still say `conductor.d in the current directory` or `next to the config file`, so they describe the new rule.

- [ ] **Step 16: Run them to see them pass**

Run: `go test ./internal/config/ ./internal/agents/ ./internal/cli/ ./internal/hostagent/ -count=1 -race`
Expected: PASS.

- [ ] **Step 17: Update the docs for the data directory**

- `README.md`, config table row: `| \`dataDir\` | \`CONDUCTOR_DATA_DIR\` | \`~/.conductor\` (an older \`conductor.d\` next to the config, or in the current directory, is kept while \`~/.conductor\` holds no server data) | UI-managed state; must be writable, best outside \`allowedRoots\` |`.
- `README.md`, "### Upgrading", replaced by:
  > The server keeps UI-managed state (agents added on the **Agents** page, crews, the hook files) in a data directory that it must be able to create and write at startup. Unless `dataDir` or `CONDUCTOR_DATA_DIR` says otherwise, that is `~/.conductor` in the home of the user running `conductor serve`. Earlier versions used `conductor.d` next to the config file, or in the current directory without one. A server that finds that old directory, and no server data in `~/.conductor` (`catalog.json`, `crews/` or `crews.json`; the `hooks/` that `conductor host` writes there does not count), keeps using it and logs a warning naming both paths. To move it, stop the server, move the files in it into `~/.conductor` (`hooks/` need not move: the server writes it at every start) and start it again; to keep it, set `dataDir` or `CONDUCTOR_DATA_DIR` to it. A directory the server cannot create stops it with `data directory … is not usable`. The Docker image sets `CONDUCTOR_DATA_DIR=/var/lib/conductor`, declared as a volume. At startup the server logs the directory it uses, and warns when it overlaps an allowed root: agents working there can read and commit its secrets.
- `README.md`, "Wired at launch": after "`conductor host --agent <id> -- <command>` does the same on your machine", add "with the hook files in `~/.conductor/hooks` (an older `~/.local/state/conductor/hooks` is kept while it exists)".
- `README.md`, the `conductor hooks` paragraph: "by default the one `conductor serve` uses without a config file" becomes "by default the one `conductor serve` uses without `dataDir`: `CONDUCTOR_DATA_DIR`, else `~/.conductor` (or an older `./conductor.d` while `~/.conductor` holds no server data)".
- `README.md`, the same paragraph (around line 186): "They refuse a `hooks/` that is not yours or not 0700, as `conductor serve` writes it (the 0755 `conductor.d` of a checkout is refused too), since its commands would go into your agents' configs." becomes "They refuse a `hooks/` that is not yours or not 0700, as `conductor serve` writes it (so a `conductor.d/hooks` that a checkout made 0755 is refused too, should an older `./conductor.d` still be the data directory), since its commands would go into your agents' configs."
- `conductor.example.json`: add `"dataDir": ""` after `"fileView": "view",`. An empty value means the default, `~/.conductor`, and the key is listed so readers find it.
- `docs/features.md`, Round 2 "Persistence": "default `conductor.d` next to the config file" becomes "default `~/.conductor` since round 3, `conductor.d` next to the config file before".

- [ ] **Step 18: Run the narrow gate and commit**

Run: `make lint && go test -race -count=1 ./internal/store/ ./internal/config/ ./internal/catalog/ ./internal/api/ ./internal/agents/ ./internal/cli/ ./internal/hostagent/`
Expected: PASS, no gofmt or vet output.

```bash
git add internal/store internal/config internal/catalog internal/api internal/agents/assets.go internal/agents/assets_test.go internal/hostagent/agent.go internal/cli README.md conductor.example.json docs/features.md
git commit -m "store, config: strict readers, unescaped saves, an editor lock apart from the snapshot, ~/.conductor by default"
```

---

### Task 2: Catalog validation, inheritance and source

Triage items #12, #13, #14, #15, #16, #17, #19, #20 and #21. The docs for what an override inherits (from the spec's "Docs" list) belong to this task.

**Files:**
- Modify: `internal/catalog/catalog.go`, `internal/catalog/defaults.go`, `internal/catalog/catalog_test.go`
- Modify: `internal/agents/registry.go` (`CheckAdapter`), `internal/agents/adapter_test.go`
- Modify: `internal/config/config.go` (`LoadCatalog`), `internal/config/config_test.go`
- Modify: `internal/api/catalog.go`, `internal/api/sessions.go:111`, `internal/api/api_test.go`
- Modify: `internal/hostagent/agent.go:312` (`catalog.SignalHook`)
- Create: `web/app/utils/catalog.ts`, `web/app/utils/catalog.test.ts`
- Modify: `web/app/composables/useSessions.ts` (`AgentInfo`, the `AgentInput.env` comment), `web/app/pages/agents.vue`
- Modify: `README.md` ("Adding agents from the UI", the limits paragraph, the security bullet about saved copies), `docs/protocol.md` (catalog rows, the catalog persistence paragraph)

**Interfaces:**
- Consumes (Task 1): `Server.catalogEditMu`, `Server.saveAgent`, `Server.unhideAgent`, `commitOverlay`.
- Produces:
  ```go
  package catalog
  const SignalHook, SignalBell, SignalPattern, SignalNone = "hook", "bell", "pattern", "none"
  type Source string
  const SourceBuiltIn Source = "built-in"; SourceConfig Source = "config"; SourceSaved Source = "saved"
  func (c Catalog) Source(id string) Source
  func (c Catalog) Clone() Catalog                       // value receiver now
  func (c *Catalog) Upsert(a Agent) error                // stores a.clone(), source SourceSaved
  func inherit(a, prev Agent, had bool) Agent

  package agents
  func CheckAdapter(id string) error                    // "" passes; unknown: `unknown adapter "x" (one of …)`

  package api
  type catalogEntry struct { catalog.Agent; Source catalog.Source `json:"source"`; Replaces catalog.Source `json:"replaces,omitempty"` }
  func entry(a catalog.Agent, cat, base catalog.Catalog) catalogEntry
  type saveAgentRequest struct { catalog.Agent; Source json.RawMessage `json:"source,omitempty"`; Replaces json.RawMessage `json:"replaces,omitempty"` }
  func keepMaskedEnv(a *catalog.Agent, saved, base catalog.Agent) error
  ```
  ```ts
  // web/app/composables/useSessions.ts, AgentInfo gains
  source?: 'built-in' | 'config' | 'saved'
  replaces?: 'built-in' | 'config'
  // web/app/utils/catalog.ts
  export type Removal = 'hide' | 'revert' | 'delete'
  export function removalOf(a: Pick<AgentInfo, 'source' | 'replaces'>): Removal
  export function removalText(r: Removal, name: string, id: string): { button: string; title: string; description: string; icon: string; toast: { title: string; description: string } }
  ```
- Shapes: an `icon` matches this plan's `^[a-z0-9][a-z0-9:-]{0,63}$` (`iconPattern`), an Iconify name such as `i-lucide-pi-square` or `lucide:bot`. The spec's `^[a-z0-9-]{1,32}$` is the adapter's shape (`idPattern`), not the icon's: an icon name is longer and may hold a colon.

- [ ] **Step 1: Write the failing catalog tests**

Append to `internal/catalog/catalog_test.go`:

```go
// Every field an agent carries into a launch or a page is bounded, whether it
// comes from the config or from the Agents page.
func TestValidateBoundsCwdIconAdapterAndEnv(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Agent)
		field  string // the field the error must name; empty when the agent is valid
	}{
		{"cwd of 4096 bytes", func(a *Agent) { a.Cwd = "/" + strings.Repeat("d", 4095) }, ""},
		{"cwd of 4097 bytes", func(a *Agent) { a.Cwd = "/" + strings.Repeat("d", 4096) }, "cwd"},
		{"cwd with NUL", func(a *Agent) { a.Cwd = "/srv\x00x" }, "cwd"},
		{"a lucide icon", func(a *Agent) { a.Icon = "i-lucide-pi-square" }, ""},
		{"icon of 64 bytes", func(a *Agent) { a.Icon = "i" + strings.Repeat("-", 63) }, ""},
		{"icon of 65 bytes", func(a *Agent) { a.Icon = "i" + strings.Repeat("-", 64) }, "icon"},
		{"icon with a space", func(a *Agent) { a.Icon = "i-lucide-x y" }, "icon"},
		{"icon in upper case", func(a *Agent) { a.Icon = "I-Lucide-X" }, "icon"},
		{"adapter", func(a *Agent) { a.Adapter = "claude" }, ""},
		{"adapter of 33 characters", func(a *Agent) { a.Adapter = strings.Repeat("a", 33) }, "adapter"},
		{"adapter with a slash", func(a *Agent) { a.Adapter = "../x" }, "adapter"},
		{"env key of 128 bytes", func(a *Agent) { a.Env = map[string]string{strings.Repeat("K", 128): "v"} }, ""},
		{"env key of 129 bytes", func(a *Agent) { a.Env = map[string]string{strings.Repeat("K", 129): "v"} }, "env"},
		{"env value of 16384 bytes", func(a *Agent) { a.Env = map[string]string{"K": strings.Repeat("v", 16384)} }, ""},
		{"env value of 16385 bytes", func(a *Agent) { a.Env = map[string]string{"K": strings.Repeat("v", 16385)} }, "env"},
		{"envPassthrough name of 128 bytes", func(a *Agent) { a.EnvPassthrough = []string{strings.Repeat("P", 128)} }, ""},
		{"envPassthrough name of 129 bytes", func(a *Agent) { a.EnvPassthrough = []string{strings.Repeat("P", 129)} }, "envPassthrough"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := Agent{ID: "x", Name: "X", Command: []string{"x"}}
			c.change(&a)
			err := validate(a)
			switch {
			case c.field == "" && err != nil:
				t.Fatalf("valid agent rejected: %v", err)
			case c.field != "" && err == nil:
				t.Fatal("agent over the limit accepted")
			case c.field != "" && !strings.Contains(err.Error(), c.field):
				t.Fatalf("error does not name %q: %v", c.field, err)
			}
		})
	}
}

// A saved agent that replaces another keeps what it leaves out: the adapter,
// the signal, and every env value it holds as the mask, which the Agents page
// stores for a key it did not change. A masked key the replaced agent does not
// have is dropped, and so is every masked value of an agent that replaces
// nothing.
func TestOverlayInheritsWhatAnOverrideLeavesOut(t *testing.T) {
	base, err := Load(File{DisableDefaults: true, Agents: []Agent{
		{ID: "keyed", Name: "keyed", Command: []string{"k"}, Adapter: "claude", Signal: &Signal{Kind: SignalPattern, Pattern: `^> $`},
			Env: map[string]string{"API_KEY": "v1", "REGION": "eu"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ov := Overlay{Agents: []Agent{
		{ID: "keyed", Name: "Keyed", Command: []string{"k2"}, Env: map[string]string{"API_KEY": RedactedValue, "REGION": "us", "GONE": RedactedValue}},
		{ID: "fresh", Name: "fresh", Command: []string{"f"}, Env: map[string]string{"X": RedactedValue, "Y": "y"}},
	}}
	got := base.Clone()
	if err := got.ApplyOverlay(ov); err != nil {
		t.Fatal(err)
	}
	k, _ := got.Get("keyed")
	if k.Command[0] != "k2" || k.Adapter != "claude" || k.Signal == nil || k.Signal.Kind != SignalPattern || k.Signal.Pattern != `^> $` ||
		!reflect.DeepEqual(k.Env, map[string]string{"API_KEY": "v1", "REGION": "us"}) {
		t.Fatalf("keyed %+v (signal %+v)", k, k.Signal)
	}
	if f, _ := got.Get("fresh"); !reflect.DeepEqual(f.Env, map[string]string{"Y": "y"}) || f.Adapter != "" || f.Signal != nil {
		t.Fatalf("fresh %+v", f)
	}
	// What an override names it keeps; an env it does not list is not inherited.
	own := base.Clone()
	if err := own.ApplyOverlay(Overlay{Agents: []Agent{{ID: "keyed", Name: "k", Command: []string{"k"}, Adapter: "codex", Signal: &Signal{Kind: SignalNone}}}}); err != nil {
		t.Fatal(err)
	}
	if k, _ := own.Get("keyed"); k.Adapter != "codex" || k.Signal.Kind != SignalNone || len(k.Env) != 0 {
		t.Fatalf("own %+v", k)
	}
	// Neither the overlay nor the base changes.
	if ov.Agents[0].Env["API_KEY"] != RedactedValue || ov.Agents[0].Adapter != "" {
		t.Fatal("ApplyOverlay changed the overlay")
	}
	if b, _ := base.Get("keyed"); b.Env["REGION"] != "eu" {
		t.Fatal("ApplyOverlay changed the base")
	}
}

func TestSourceOfEachAgent(t *testing.T) {
	c, err := Load(File{Agents: []Agent{
		{ID: "claude", Name: "Claude (pinned)", Command: []string{"claude"}},
		{ID: "my-tool", Name: "My tool", Command: []string{"mytool"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]Source{"codex": SourceBuiltIn, "claude": SourceConfig, "my-tool": SourceConfig, "nope": ""} {
		if got := c.Source(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
	err = c.ApplyOverlay(Overlay{
		Agents: []Agent{{ID: "codex", Name: "Codex", Command: []string{"codex"}}, {ID: "added", Name: "Added", Command: []string{"a"}}},
		Hidden: []string{"my-tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]Source{"codex": SourceSaved, "added": SourceSaved, "my-tool": "", "shell": SourceBuiltIn} {
		if got := c.Source(id); got != want {
			t.Errorf("after the overlay, %s: %q, want %q", id, got, want)
		}
	}
}

func TestUpsertStoresACopy(t *testing.T) {
	var c Catalog
	a := Agent{ID: "x", Name: "X", Command: []string{"x"}, Env: map[string]string{"K": "v"}, Signal: &Signal{Kind: SignalBell}}
	if err := c.Upsert(a); err != nil {
		t.Fatal(err)
	}
	a.Command[0], a.Env["K"], a.Signal.Kind = "changed", "changed", SignalNone
	if got, _ := c.Get("x"); got.Command[0] != "x" || got.Env["K"] != "v" || got.Signal.Kind != SignalBell {
		t.Fatalf("the catalog shares the caller's agent: %+v", got)
	}
	// Clone takes a value: a catalog a call returns clones without a variable.
	if ids := idsOf(Default().Clone()); ids != builtIns {
		t.Fatalf("clone lists %s", ids)
	}
}
```

Append to `internal/config/config_test.go` (import `github.com/phenixrizen/conductor/internal/catalog`):

```go
// An agent of the config that names an adapter Conductor does not have stops
// startup, as the Agents page refuses it.
func TestLoadCatalogChecksTheAdapter(t *testing.T) {
	cfg := Defaults()
	cfg.Catalog = catalog.File{Agents: []catalog.Agent{{ID: "g", Name: "g", Command: []string{"g"}, Adapter: "gemini"}}}
	if _, err := cfg.LoadCatalog(); err == nil || !strings.Contains(err.Error(), `"gemini"`) || !strings.Contains(err.Error(), "agent g") {
		t.Fatalf("inline: %v", err)
	}
	cfg.Catalog.Agents[0].Adapter = "claude"
	if _, err := cfg.LoadCatalog(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agents.json")
	if err := os.WriteFile(path, []byte(`{"agents":[{"id":"h","name":"h","command":["h"],"adapter":"nope"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg = Defaults()
	cfg.CatalogPath = path
	if _, err := cfg.LoadCatalog(); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("catalog file: %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/catalog/ ./internal/config/ -count=1`
Expected: build failure. `SignalPattern`, `Source` and `SourceBuiltIn` are undefined, and `Default().Clone()` cannot take the address of a call.

- [ ] **Step 3: Implement the catalog changes**

In `internal/catalog/catalog.go`:

```go
// Signal kinds (Signal.Kind).
const (
	SignalHook    = "hook"    // the agent's hooks report through its adapter
	SignalBell    = "bell"    // a terminal bell or an OSC notification; the default
	SignalPattern = "pattern" // Pattern matches the last line of the screen
	SignalNone    = "none"    // never flagged
)

// Source says where the catalog took an agent from.
type Source string

const (
	SourceBuiltIn Source = "built-in" // defaults.go
	SourceConfig  Source = "config"   // the catalog of the config file, or the catalog file
	SourceSaved   Source = "saved"    // the overlay the Agents page saves (catalog.json)
)
```

Add the patterns and limits next to `idPattern` and the existing limits:

```go
// iconPattern is what an icon name looks like: an Iconify name in a class
// (i-lucide-sparkles), at most 64 bytes.
var iconPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9:-]{0,63}$`)

const (
	maxCwd      = 4096     // bytes in cwd
	maxEnvKey   = 128      // bytes in an env key or an envPassthrough name
	maxEnvValue = 16 << 10 // bytes in an env value
)
```

`Catalog` gets `sources map[string]Source`. `add` takes the source:

```go
func (c *Catalog) add(a Agent, src Source) {
	if c.agents == nil {
		c.agents = map[string]Agent{}
	}
	if c.sources == nil {
		c.sources = map[string]Source{}
	}
	if _, exists := c.agents[a.ID]; !exists {
		c.order = append(c.order, a.ID)
	}
	c.agents[a.ID] = a
	c.sources[a.ID] = src
}
```

`Default` calls `c.add(a, SourceBuiltIn)`. `Load` calls `c.add(a, SourceBuiltIn)` for the defaults and `c.add(a, SourceConfig)` for `f.Agents`. Then:

```go
// Upsert validates a, then replaces the agent with the same ID in place (so it
// keeps its position) or appends a copy of a as a new one, from the overlay
// (SourceSaved). a is not kept: changing it later changes nothing here.
func (c *Catalog) Upsert(a Agent) error {
	if err := validate(a); err != nil {
		return err
	}
	c.add(a.clone(), SourceSaved)
	return nil
}

// Hide removes the agent with the given ID and reports whether it was there.
func (c *Catalog) Hide(id string) bool {
	if _, ok := c.agents[id]; !ok {
		return false
	}
	delete(c.agents, id)
	delete(c.sources, id)
	c.order = slices.DeleteFunc(c.order, func(other string) bool { return other == id })
	return true
}

// ApplyOverlay upserts every agent in o over the agent it replaces, with what
// it leaves out taken from that one (inherit), then hides every ID in
// o.Hidden, so hiding wins over an agent with the same ID. Hiding an ID that
// is not in the catalog is not an error. If any agent is invalid the catalog
// is unchanged.
func (c *Catalog) ApplyOverlay(o Overlay) error {
	next := c.Clone()
	for i, a := range o.Agents {
		prev, had := next.Get(a.ID)
		if err := next.Upsert(inherit(a, prev, had)); err != nil {
			return fmt.Errorf("agents[%d]: %w", i, err)
		}
	}
	for _, id := range o.Hidden {
		next.Hide(id)
	}
	*c = next
	return nil
}

// inherit returns a, an agent saved over prev (had says there was one), with
// what it leaves to prev filled in: an adapter or a signal it omits, and every
// env value it holds as RedactedValue. The Agents page stores the mask for a
// key whose value the editor did not change, so a value changed in the config
// reaches the agent. A masked key that prev does not have, and every masked
// value when there is no prev, is dropped. a itself is not changed.
func inherit(a, prev Agent, had bool) Agent {
	a = a.clone()
	if had {
		if a.Adapter == "" {
			a.Adapter = prev.Adapter
		}
		if a.Signal == nil && prev.Signal != nil {
			s := *prev.Signal
			a.Signal = &s
		}
	}
	for k, v := range a.Env {
		if v != RedactedValue {
			continue
		}
		if pv, ok := prev.Env[k]; had && ok {
			a.Env[k] = pv
		} else {
			delete(a.Env, k)
		}
	}
	return a
}

// Clone returns a deep copy: no map, slice or signal is shared with c. Upsert,
// Hide and ApplyOverlay change a catalog in place, so a catalog that other
// goroutines read should be cloned, changed and swapped in, not changed itself.
func (c Catalog) Clone() Catalog {
	out := Catalog{
		agents:  make(map[string]Agent, len(c.agents)),
		sources: maps.Clone(c.sources),
		order:   slices.Clone(c.order),
	}
	for id, a := range c.agents {
		out.agents[id] = a.clone()
	}
	return out
}

// Source says where the agent with the given ID comes from: built in, the
// config, or saved from the Agents page; "" when the catalog has no such agent.
func (c Catalog) Source(id string) Source { return c.sources[id] }
```

In `validate`, after the command checks:

```go
	if len(a.Cwd) > maxCwd || strings.ContainsRune(a.Cwd, 0) {
		return fmt.Errorf("agent %s: cwd must be at most %d bytes, without NUL", a.ID, maxCwd)
	}
	if a.Icon != "" && !iconPattern.MatchString(a.Icon) {
		return fmt.Errorf("agent %s: icon must match %s", a.ID, iconPattern)
	}
	// The adapter's shape; the registry of adapters is checked where it is
	// known (agents.CheckAdapter), for the config and for saved agents alike.
	if a.Adapter != "" && !idPattern.MatchString(a.Adapter) {
		return fmt.Errorf("agent %s: adapter must match %s", a.ID, idPattern)
	}
```

The env loop and the passthrough loop become:

```go
	for k, v := range a.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return fmt.Errorf("agent %s: invalid env entry %q", a.ID, cut(k))
		}
		if len(k) > maxEnvKey {
			return fmt.Errorf("agent %s: env key %q… is longer than %d bytes", a.ID, cut(k), maxEnvKey)
		}
		if len(v) > maxEnvValue {
			return fmt.Errorf("agent %s: env %s: the value is %d bytes, more than %d", a.ID, k, len(v), maxEnvValue)
		}
	}
	// (the signal check and the envPassthrough count between the loops stay as they are)
	for _, name := range a.EnvPassthrough {
		if len(name) > maxEnvKey {
			return fmt.Errorf("agent %s: envPassthrough name %q… is longer than %d bytes", a.ID, cut(name), maxEnvKey)
		}
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("agent %s: invalid envPassthrough entry %q", a.ID, name)
		}
	}
```

```go
// cut keeps the first 40 bytes of s for an error message, so an error never
// sends a long value back whole.
func cut(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}
```

Replace the string literals with the constants. In `validateSignal`, use `case SignalHook, SignalBell, SignalNone:` and `case SignalPattern:`. `EffectiveSignal` returns `Signal{Kind: SignalBell}`. In `defaults.go`, every `&Signal{Kind: "…"}` uses the constant. In `internal/api/sessions.go:111`, use `sig.Kind == catalog.SignalPattern`. In `internal/agents/registry.go:60`, use `sig.Kind != catalog.SignalHook`. In `internal/hostagent/agent.go:312`, use `catalog.Signal{Kind: catalog.SignalHook}`.

In `internal/agents/registry.go`, add:

```go
// CheckAdapter reports an error when id names no adapter Conductor has; ""
// (no adapter) passes. The error lists the adapters there are. The catalog
// cannot check this itself, since the adapters import it: the config's agents
// are checked when it is loaded (config.LoadCatalog), saved ones when they are
// saved and at startup (internal/api).
func CheckAdapter(id string) error {
	if id == "" {
		return nil
	}
	if _, ok := Get(id); ok {
		return nil
	}
	ids := make([]string, 0, len(registry))
	for _, a := range registry {
		ids = append(ids, a.ID)
	}
	return fmt.Errorf("unknown adapter %q (one of %s)", id, strings.Join(ids, ", "))
}
```

Add a test for it to `internal/agents/adapter_test.go`:

```go
func TestCheckAdapter(t *testing.T) {
	for _, id := range []string{"", "claude", "dsh"} {
		if err := CheckAdapter(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	if err := CheckAdapter("gemini"); err == nil || !strings.Contains(err.Error(), `"gemini"`) || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("gemini: %v", err)
	}
}
```

In `internal/config/config.go` (importing `internal/agents`, which imports only `internal/catalog`), `LoadCatalog` ends:

```go
	cat, err := catalog.Load(file)
	if err != nil {
		return catalog.Catalog{}, err
	}
	// The catalog cannot check an adapter (the adapters import it): the
	// config's agents are checked here, as the Agents page checks the ones it
	// saves.
	var errs []error
	for _, a := range file.Agents {
		if err := agents.CheckAdapter(a.Adapter); err != nil {
			errs = append(errs, fmt.Errorf("agent %s: %w", a.ID, err))
		}
	}
	if len(errs) > 0 {
		return catalog.Catalog{}, errors.Join(errs...)
	}
	return cat, nil
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/catalog/ ./internal/config/ ./internal/agents/ -count=1`
Expected: PASS. The existing `TestCloneIsIndependent` and `TestOverlayUpsertsAndHides` still pass.

- [ ] **Step 5: Write the failing API tests**

Add to `internal/api/api_test.go`:

```go
// The Agents page stores, for an agent that replaces a built-in or configured
// one, only the env values the admin changed; the others stay the mask, read
// as the replaced agent's value. So a secret rotated in the config reaches the
// agent at the next start, a value the admin set stays, and a key the admin
// removed stays removed. The adapter and signal left out come from the
// replaced agent too.
func TestCatalogOverrideFollowsTheBaseEnv(t *testing.T) {
	e := newTestEnv(t, nil)
	start := func(secret string) *testEnv {
		t.Helper()
		base, err := catalog.Load(catalog.File{DisableDefaults: true, Agents: []catalog.Agent{
			{ID: "cat", Name: "cat", Command: []string{"/bin/cat"}},
			{ID: "keyed", Name: "keyed", Command: []string{"/bin/cat"}, Adapter: "claude",
				Signal: &catalog.Signal{Kind: catalog.SignalPattern, Pattern: `^> $`},
				Env:    map[string]string{"API_KEY": secret, "REGION": "eu", "DEBUG": "1"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		srv, err := New(e.srv.cfg, base, e.srv.log, nil, e.srv.store)
		if err != nil {
			t.Fatal(err)
		}
		return e.serve(srv)
	}
	v1 := start("v1")
	listed := v1.catalogAgent("keyed")
	if listed["source"] != "config" {
		t.Fatalf("listed %v", listed)
	}
	// The admin edits the description and REGION, leaves API_KEY as read,
	// removes DEBUG, and sends neither adapter nor signal. source travels back
	// as it was read.
	listed["description"] = "edited"
	listed["env"] = map[string]any{"API_KEY": "***", "REGION": "us"}
	delete(listed, "adapter")
	delete(listed, "signal")
	out := v1.save(listed)
	if a := out["agent"].(map[string]any); a["source"] != "saved" || a["replaces"] != "config" {
		t.Fatalf("saved %v", a)
	}
	var ov catalog.Overlay
	if ok, err := v1.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != 1 ||
		!reflect.DeepEqual(ov.Agents[0].Env, map[string]string{"API_KEY": "***", "REGION": "us"}) || ov.Agents[0].Adapter != "" || ov.Agents[0].Signal != nil {
		t.Fatalf("overlay: %v %v %+v", ok, err, ov)
	}
	if strings.Contains(v1.overlayFile(), `"v1"`) {
		t.Fatalf("the secret was copied into catalog.json:\n%s", v1.overlayFile())
	}
	check := func(env *testEnv, secret string) {
		t.Helper()
		a, _ := env.srv.Catalog().Get("keyed")
		if a.Description != "edited" || !reflect.DeepEqual(a.Env, map[string]string{"API_KEY": secret, "REGION": "us"}) ||
			a.Adapter != "claude" || a.Signal == nil || a.Signal.Kind != catalog.SignalPattern {
			t.Fatalf("keyed runs with %+v (signal %+v)", a, a.Signal)
		}
	}
	check(v1, "v1")
	// The config rotates the secret: the next start reads it, through the same catalog.json.
	check(start("v2"), "v2")
}

func TestCatalogListsWhereEachAgentComesFrom(t *testing.T) {
	e := newTestEnv(t, nil) // cat, sh and exit come from the config
	e.save(map[string]any{"id": "cat", "name": "Cat mk2", "command": []string{"/bin/cat"}})
	e.save(agentBody("aider"))
	for id, want := range map[string][2]any{"cat": {"saved", "config"}, "sh": {"config", nil}, "exit": {"config", nil}, "aider": {"saved", nil}} {
		if a := e.catalogAgent(id); a["source"] != want[0] || a["replaces"] != want[1] {
			t.Errorf("%s: source %v, replaces %v; want %v", id, a["source"], a["replaces"], want)
		}
	}
	if c := e.del("sh"); c != http.StatusNoContent {
		t.Fatalf("hide: %d", c)
	}
	resp, out := e.do("POST", "/api/catalog/sh/unhide", adminToken, nil)
	if a, _ := out["agent"].(map[string]any); resp.StatusCode != http.StatusOK || a["source"] != "config" {
		t.Fatalf("unhide: %d %v", resp.StatusCode, out)
	}
}

// A built-in that a saved agent replaces launches as saved, with the signal
// the saved agent left out taken from the built-in.
func TestCatalogLaunchesAnOverriddenBuiltIn(t *testing.T) {
	e := newTestEnv(t, nil)
	srv, err := New(e.srv.cfg, catalog.Default(), e.srv.log, nil, e.srv.store)
	if err != nil {
		t.Fatal(err)
	}
	b := e.serve(srv)
	argv := []string{"/bin/sh", "-c", "echo OVERRIDE-RAN; exec /bin/cat"}
	b.save(map[string]any{"id": "shell", "name": "Shell", "command": argv})
	if a := b.catalogAgent("shell"); a["source"] != "saved" || a["replaces"] != "built-in" {
		t.Fatalf("listed %v", a)
	}
	if a, _ := b.srv.Catalog().Get("shell"); a.Signal == nil || a.Signal.Kind != catalog.SignalNone {
		t.Fatalf("the override lost the built-in's signal: %+v", a.Signal)
	}
	id := b.createSession("shell")
	c := dialViewer(t, b, id, adminToken)
	c.hello(80, 24)
	c.expectOutput("OVERRIDE-RAN")
	if d, _ := b.srv.registry.Get(id); !slices.Equal(d.Info().Command, argv) {
		t.Fatalf("command %q", d.Info().Command)
	}
}

// Every agent can be hidden: the catalog is then empty, launches are refused
// as for an unknown agent, a restart comes up empty, and Restore brings one back.
func TestCatalogHidesDownToNothing(t *testing.T) {
	e := newTestEnv(t, nil)
	for _, id := range []string{"cat", "sh", "exit"} {
		if c := e.del(id); c != http.StatusNoContent {
			t.Fatalf("hide %s: %d", id, c)
		}
	}
	if ids := e.catalogIDs(); len(ids) != 0 {
		t.Fatalf("listed %v", ids)
	}
	if hidden := e.catalogHidden(); !slices.Equal(hidden, []string{"cat", "sh", "exit"}) {
		t.Fatalf("hidden %v", hidden)
	}
	resp, out := e.do("POST", "/api/sessions", adminToken, map[string]any{"agentId": "cat"})
	if resp.StatusCode != http.StatusBadRequest || errorCode(out) != "invalid_agent" {
		t.Fatalf("launch: %d %v", resp.StatusCode, out)
	}
	again := e.restart()
	if ids := again.catalogIDs(); len(ids) != 0 {
		t.Fatalf("after a restart: %v", ids)
	}
	if resp, _ := again.do("POST", "/api/catalog/sh/unhide", adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("unhide: %d", resp.StatusCode)
	}
	if ids := again.catalogIDs(); !slices.Equal(ids, []string{"sh"}) {
		t.Fatalf("after unhiding sh: %v", ids)
	}
}

// An agent the Agents page added is deleted outright: not hidden, gone from
// catalog.json, and unknown to a second delete.
func TestCatalogDeletesAnAgentItAdded(t *testing.T) {
	e := newTestEnv(t, nil)
	e.save(agentBody("aider"))
	if c := e.del("aider"); c != http.StatusNoContent {
		t.Fatalf("delete: %d", c)
	}
	if e.catalogAgent("aider") != nil || slices.Contains(e.catalogHidden(), "aider") {
		t.Fatal("aider is still listed or hidden")
	}
	var ov catalog.Overlay
	if ok, err := e.srv.store.Load("catalog.json", &ov); !ok || err != nil || len(ov.Agents) != 0 || len(ov.Hidden) != 0 {
		t.Fatalf("overlay: %v %v %+v", ok, err, ov)
	}
	if c := e.del("aider"); c != http.StatusNotFound {
		t.Fatalf("second delete: %d", c)
	}
}
```

In `TestNewRefusesAnUnusableOverlay`, add the case:

```go
		{"unknown adapter", `{"agents": [{"id": "ok", "name": "x", "command": ["x"], "adapter": "gemini"}]}`, `"gemini"`},
```

- [ ] **Step 6: Run them to see them fail**

Run: `go test ./internal/api/ -count=1 -run 'TestCatalog|TestNewRefusesAnUnusableOverlay'`
Expected: FAIL. `source` is missing from the listing, the saved agent stores `v1`, the overridden `shell` has no signal, and the overlay with an unknown adapter starts.

- [ ] **Step 7: Implement entries, the request shape, `keepMaskedEnv` and the startup check**

In `internal/api/catalog.go` (add `"encoding/json"` to the imports):

```go
// catalogEntry is an agent as the catalog routes answer with it: env values
// masked, where the catalog took it from and, for a saved agent that replaces
// a built-in or configured one, where that one came from (deleting the saved
// agent brings it back).
type catalogEntry struct {
	catalog.Agent
	Source   catalog.Source `json:"source"`
	Replaces catalog.Source `json:"replaces,omitempty"`
}

// entry is a as the routes show it, by the effective catalog cat and the
// configured one, base.
func entry(a catalog.Agent, cat, base catalog.Catalog) catalogEntry {
	e := catalogEntry{Agent: a.Redacted(), Source: cat.Source(a.ID)}
	if e.Source == catalog.SourceSaved {
		e.Replaces = base.Source(a.ID)
	}
	return e
}

// saveAgentRequest is the body of POST /api/catalog: an agent, as a client
// builds it or as GET /api/catalog lists it. source and replaces, which the
// listing adds, are taken and ignored, so an agent read there can be sent back
// as it is; any other field the agent does not have is refused.
type saveAgentRequest struct {
	catalog.Agent
	Source   json.RawMessage `json:"source,omitempty"`
	Replaces json.RawMessage `json:"replaces,omitempty"`
}
```

`handleCatalog` lists entries:

```go
func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	s.catalogMu.Lock()
	cat, hidden := s.catalog, uniqueIDs(s.overlay.Hidden)
	s.catalogMu.Unlock()
	list := cat.List()
	out := make([]catalogEntry, 0, len(list))
	for _, a := range list {
		out = append(out, entry(a, cat, s.base))
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out, "hidden": hidden})
}
```

`handleSaveAgent` decodes a `saveAgentRequest`, passes `req.Agent` to `saveAgent`, and answers `{"agent": entry(saved, s.Catalog(), s.base)}`. `handleUnhideAgent` answers `entry(a, s.Catalog(), s.base)` in place of `a.Redacted()`. In `saveAgent`, the `stored` lookup and `restoreMaskedEnv(&a, stored)` become:

```go
	base, _ := s.base.Get(a.ID)
	if err := keepMaskedEnv(&a, savedAgent(s.overlay.Agents, a.ID), base); err != nil {
		return catalog.Agent{}, newAPIError(http.StatusBadRequest, "invalid_agent", err.Error())
	}
```

Delete `restoreMaskedEnv` and add:

```go
// keepMaskedEnv decides what the overlay stores for each env entry of a whose
// value is catalog.RedactedValue. GET /api/catalog shows that in place of every
// value, so a client that sends back what it read means "unchanged". If the
// overlay entry a replaces (saved) holds a value of its own for the key, that
// value is kept. Otherwise, if the agent a overrides (base) has the key, the
// mask itself is stored: ApplyOverlay reads it as base's value, so a change in
// the config reaches the agent. A masked key that neither has is an error.
// The first such key by name is reported, so the answer does not depend on map
// order.
func keepMaskedEnv(a *catalog.Agent, saved, base catalog.Agent) error {
	for _, k := range slices.Sorted(maps.Keys(a.Env)) {
		if a.Env[k] != catalog.RedactedValue {
			continue
		}
		if v, ok := saved.Env[k]; ok && v != catalog.RedactedValue {
			a.Env[k] = v
			continue
		}
		if _, ok := base.Env[k]; ok {
			continue // stays the mask: the base agent's value
		}
		return fmt.Errorf("env %s: value is redacted; set a real value", k)
	}
	return nil
}

// savedAgent returns the overlay's entry for id, the last one when a
// hand-edited file repeats it (the one that wins at startup), or the zero
// Agent.
func savedAgent(agents []catalog.Agent, id string) catalog.Agent {
	for i := len(agents) - 1; i >= 0; i-- {
		if agents[i].ID == id {
			return agents[i]
		}
	}
	return catalog.Agent{}
}
```

`checkAdapter` uses the registry's message:

```go
// checkAdapter rejects an agent whose adapter Conductor does not have.
func checkAdapter(a catalog.Agent) error {
	if err := agents.CheckAdapter(a.Adapter); err != nil {
		return fmt.Errorf("agent %s: %w", a.ID, err)
	}
	return nil
}
```

`loadCatalog` checks the overlay's adapters before it applies the overlay:

```go
	for i, a := range ov.Agents {
		if err := checkAdapter(a); err != nil {
			return catalog.Catalog{}, catalog.Overlay{}, fmt.Errorf("%s: agents[%d]: %w", path, i, err)
		}
	}
```

- [ ] **Step 8: Run the API tests to see them pass**

Run: `go test ./internal/api/ -count=1 -race`
Expected: PASS. `TestCatalogSaveKeepsMaskedEnvValues` passes unchanged: "keyed" is an agent the page added, so its stored values come back from the overlay.

- [ ] **Step 9: Write the failing web test**

Create `web/app/utils/catalog.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { removalOf, removalText } from './catalog'

describe('removalOf', () => {
  it('hides what comes from the built-ins or the config', () => {
    expect(removalOf({ source: 'built-in' })).toBe('hide')
    expect(removalOf({ source: 'config' })).toBe('hide')
    expect(removalOf({})).toBe('hide')
  })
  it('reverts a saved change and deletes a saved addition', () => {
    expect(removalOf({ source: 'saved', replaces: 'built-in' })).toBe('revert')
    expect(removalOf({ source: 'saved', replaces: 'config' })).toBe('revert')
    expect(removalOf({ source: 'saved' })).toBe('delete')
  })
})

describe('removalText', () => {
  it('says what each removal does', () => {
    expect(removalText('hide', 'Aider', 'aider')).toMatchObject({ button: 'Hide', toast: { title: 'Hidden' } })
    expect(removalText('revert', 'Claude Code', 'claude').toast.description).toBe('claude is back to its original definition.')
    expect(removalText('delete', 'Mine', 'mine')).toMatchObject({ button: 'Delete', title: 'Delete Mine?', toast: { title: 'Removed', description: 'Mine' } })
  })
})
```

Run: `npm --prefix web test -- catalog`
Expected: FAIL, the module `./catalog` does not exist.

- [ ] **Step 10: Implement the removal wording and the Agents page**

Create `web/app/utils/catalog.ts`:

```ts
import type { AgentInfo } from '~/composables/useSessions'

/** What removing an agent on the Agents page does: hide a built-in or configured one, revert a saved change, or delete a saved addition. */
export type Removal = 'hide' | 'revert' | 'delete'

/** The removal DELETE /api/catalog/{id} makes of `a`, from where the catalog took it. */
export function removalOf(a: Pick<AgentInfo, 'source' | 'replaces'>): Removal {
  if (a.source !== 'saved') return 'hide'
  return a.replaces ? 'revert' : 'delete'
}

/** The words for a removal: its button, its dialog and the toast after it. */
export function removalText(r: Removal, name: string, id: string) {
  switch (r) {
    case 'revert':
      return {
        button: 'Revert',
        title: `Revert ${name}?`,
        description: 'Your changes go and the original definition comes back. Sessions already running keep going.',
        icon: 'i-lucide-undo-2',
        toast: { title: 'Change removed', description: `${id} is back to its original definition.` },
      }
    case 'delete':
      return {
        button: 'Delete',
        title: `Delete ${name}?`,
        description: 'The agent you added goes. Sessions already running keep going.',
        icon: 'i-lucide-trash-2',
        toast: { title: 'Removed', description: name },
      }
    default:
      return {
        button: 'Hide',
        title: `Hide ${name}?`,
        description: 'It leaves the agent list and the Launch dialog and is listed under Hidden, where Restore brings it back. Sessions already running keep going.',
        icon: 'i-lucide-eye-off',
        toast: { title: 'Hidden', description: `${name} can be restored from the Hidden list.` },
      }
  }
}
```

In `web/app/composables/useSessions.ts`, the comment on `AgentInput.env` follows the new rule. It said the server "rejects it for a key the agent does not have", which no longer holds for an agent that replaces another:

```ts
  /**
   * A value of "***", as read from the catalog, means "unchanged": the value the saved agent holds for that key or, for an agent that
   * replaces a built-in or configured one, the replaced agent's value, which then follows the config. The server rejects it for a key
   * neither has.
   */
  env?: Record<string, string>
```

`AgentInfo` gains:

```ts
  /** Where the catalog took the agent from: built in, the config file (or catalog file), or saved from the Agents page. */
  source?: 'built-in' | 'config' | 'saved'
  /** For a saved agent that replaces a built-in or configured one: where that one came from. Deleting the saved agent brings it back. */
  replaces?: 'built-in' | 'config'
```

In `web/app/pages/agents.vue`, import `removalOf` and `removalText` and replace the inference in `confirmHide`:

```ts
const hideText = computed(() => (hideTarget.value ? removalText(removalOf(hideTarget.value), hideTarget.value.name, hideTarget.value.id) : undefined))

async function confirmHide() {
  const a = hideTarget.value
  if (!a) return
  const text = removalText(removalOf(a), a.name, a.id)
  hiding.value = true
  hideError.value = ''
  try {
    await api.deleteAgent(a.id)
    hideOpen.value = false
    toast.add({ title: text.toast.title, description: text.toast.description, icon: text.icon, color: 'neutral' })
    await refresh()
  } catch (e) {
    hideError.value = (e as Error).message
  } finally {
    hiding.value = false
  }
}
```

The card button uses `:label="removalText(removalOf(a), a.name, a.id).button"`, `:icon="removalText(removalOf(a), a.name, a.id).icon"` and `:aria-label="\`${removalText(removalOf(a), a.name, a.id).button} ${a.name}\`"`. The modal uses `:title="hideText?.title ?? 'Hide agent?'"` and `:description="hideText?.description"`, and its confirm button uses `:label="hideText?.button ?? 'Hide'"` and `:icon="hideText?.icon"`. Delete the generic paragraph in the modal body (the old line 197); only the `UAlert` for `hideError` stays.

Run: `npm --prefix web test -- catalog && npm --prefix web run typecheck`
Expected: PASS.

- [ ] **Step 11: Update the docs**

- `docs/protocol.md`, `GET /api/catalog` row: each agent also carries `source` (`built-in`, `config` or `saved`) and, for a saved agent that replaces a built-in or configured one, `replaces` (where that one came from).
- `docs/protocol.md`, `POST /api/catalog` row: `source` and `replaces` in the body are ignored. An `env` value of `***` keeps the value the saved entry holds for that key. For an agent that replaces a built-in or configured one, a `***` value the saved entry does not hold is stored as `***` and means "the replaced agent's value", so a change in the config reaches it. A saved agent that leaves out `adapter` or `signal` takes those of the agent it replaces. `***` is rejected for a key neither has. The unknown-`adapter` check also runs for the config's agents and for `catalog.json` at startup.
- `docs/protocol.md`, catalog persistence paragraph: add to the limits "`cwd` at most 4096 bytes without NUL, `icon` matching `^[a-z0-9][a-z0-9:-]{0,63}$`, `adapter` matching `^[a-z0-9-]{1,32}$` and naming an adapter Conductor has, an `env` key or `envPassthrough` name at most 128 bytes and an `env` value at most 16384 bytes". Add that an entry of `catalog.json` with the ID of a built-in or configured agent replaces it but inherits what it leaves out (adapter, signal, `***` env values), while an agent in the config file that replaces a built-in replaces it whole.
- `README.md`, "Adding agents from the UI": add the same sentence about what a saved replacement inherits. In the limits paragraph, add the new limits and say that the adapter check applies to the config file too.
- `README.md`, security bullet: replace "Changing a built-in or configured agent saves a full copy of it, env values included, that replaces the original until it is deleted: a secret rotated in the config file does not reach that agent while the copy exists." with "Changing a built-in or configured agent saves only what the form changed: an env value left as it was stays the original's, so a secret rotated in the config file reaches the agent at the next start, while a value set on the Agents page is stored in `catalog.json`."

- [ ] **Step 12: Run the narrow gate and commit**

Run: `make lint && go test -race -count=1 ./internal/catalog/ ./internal/config/ ./internal/agents/ ./internal/api/ ./internal/hostagent/ && npm --prefix web test && npm --prefix web run typecheck`
Expected: PASS.

```bash
git add internal/catalog internal/agents/registry.go internal/agents/adapter_test.go internal/config internal/api internal/hostagent/agent.go web/app/utils/catalog.ts web/app/utils/catalog.test.ts web/app/composables/useSessions.ts web/app/pages/agents.vue README.md docs/protocol.md
git commit -m "catalog: bounds, sources, overrides that inherit and keep only changed env values"
```

---

### Task 3: The add-agent form

Triage items #23 to #29.

**Files:**
- Create: `web/app/utils/agentForm.ts`, `web/app/utils/agentForm.test.ts`
- Modify: `web/app/utils/argv.ts` (`slugId`), `web/app/utils/argv.test.ts`
- Modify: `web/app/components/AddAgentSlideover.vue`, `web/app/components/ArgvInput.vue`
- Modify: `web/app/composables/useSessions.ts` (`checkCommand`)

**Interfaces:**
- Consumes (Task 2): `AgentInfo.source`/`replaces` are ignored by the server on save, so the form never sends them.
- Produces (`web/app/utils/agentForm.ts`):
  ```ts
  export const MASK = '***'
  export const AGENT_ID_PATTERN = /^[a-z0-9-]{1,32}$/          // internal/catalog idPattern
  export type SignalKind = AgentSignal['kind']
  export interface EnvRow { uid: number; key: string; value: string; masked: boolean }
  export interface AgentForm { name: string; id: string; command: string[]; pendingCommand: string; description: string; env: EnvRow[]; allowArgs: boolean; signal: SignalKind; pattern: string }
  export type Field = 'name' | 'id' | 'command' | 'pattern' | 'env'
  export function formFromAgent(a: AgentInfo | undefined, uid: () => number): AgentForm
  export function commandOf(f: Pick<AgentForm, 'command' | 'pendingCommand'>): string[]
  export function formErrors(f: AgentForm): Partial<Record<Field, string>>
  export function signalOut(kind: SignalKind, pattern: string, prev?: AgentSignal): AgentSignal | undefined
  export function agentPayload(f: AgentForm, prev?: AgentInfo): AgentInput
  ```
  `ArgvInput` gains `v-model:pending` (string). `useSessions().checkCommand(program: string)` posts `{command: [program]}`.

- [ ] **Step 1: Write the failing tests**

Create `web/app/utils/agentForm.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { AgentInfo } from '~/composables/useSessions'
import { AGENT_ID_PATTERN, MASK, agentPayload, commandOf, formErrors, formFromAgent, signalOut } from './agentForm'
import { slugId } from './argv'

function counter() {
  let n = 0
  return () => n++
}

const keyed: AgentInfo = {
  id: 'keyed',
  name: 'Keyed',
  command: ['aider', '--model', 'x'],
  allowArgs: true,
  env: { REGION: MASK, API_KEY: MASK },
  envPassthrough: ['HTTP_PROXY'],
  cwd: '/srv',
  icon: 'i-lucide-sparkles',
  adapter: 'claude',
  signal: { kind: 'hook', toolEvents: true },
}

describe('formFromAgent and agentPayload', () => {
  it('round-trips an agent: stored values stay masked, passthrough names stay names', () => {
    const f = formFromAgent(keyed, counter())
    expect(f.env.map((r) => [r.key, r.masked])).toEqual([
      ['API_KEY', true],
      ['REGION', true],
      ['HTTP_PROXY', false],
    ])
    expect(agentPayload(f, keyed)).toEqual({
      id: 'keyed',
      name: 'Keyed',
      description: undefined,
      command: ['aider', '--model', 'x'],
      allowArgs: true,
      env: { API_KEY: MASK, REGION: MASK },
      envPassthrough: ['HTTP_PROXY'],
      cwd: '/srv',
      icon: 'i-lucide-sparkles',
      adapter: 'claude',
      signal: { kind: 'hook', toolEvents: true },
    })
  })
  it('starts a new agent empty, ringing the bell', () => {
    const f = formFromAgent(undefined, counter())
    expect(f).toMatchObject({ name: '', id: '', command: [], pendingCommand: '', env: [], allowArgs: true, signal: 'bell' })
    expect(agentPayload({ ...f, name: 'X', id: 'x', command: ['x'] }).signal).toBeUndefined()
  })
  it('counts what is typed in the command field and not yet an argument', () => {
    const f = { ...formFromAgent(undefined, counter()), command: ['aider'], pendingCommand: '--model "gpt 5"' }
    expect(commandOf(f)).toEqual(['aider', '--model', 'gpt 5'])
    expect(agentPayload({ ...f, name: 'A', id: 'a' }).command).toEqual(['aider', '--model', 'gpt 5'])
  })
})

describe('formErrors', () => {
  const ok = () => ({ ...formFromAgent(undefined, counter()), name: 'Aider', id: 'aider', command: ['aider'] })
  it('accepts a complete form', () => {
    expect(formErrors(ok())).toEqual({})
  })
  it('catches *** typed as a new value: the server would read it as the stored one', () => {
    const f = ok()
    f.env.push({ uid: 1, key: 'API_KEY', value: MASK, masked: false })
    expect(formErrors(f).env).toBe('API_KEY: *** stands for a stored value; type the real value')
  })
  it('refuses an unclosed quote in the command', () => {
    expect(formErrors({ ...ok(), pendingCommand: '--model "gpt' }).command).toBe('Close the quote, or remove it')
  })
  it("wants a command, a name and an ID of the server's shape", () => {
    expect(formErrors({ ...ok(), command: [] }).command).toBe('Add the command to run')
    expect(formErrors({ ...ok(), name: ' ' }).name).toBe('Give the agent a name')
    expect(formErrors({ ...ok(), id: 'Bad ID' }).id).toBe('Use lowercase letters, digits and dashes, up to 32')
    expect(formErrors({ ...ok(), id: 'a'.repeat(33) }).id).toBeDefined()
    expect(formErrors({ ...ok(), id: '' }).id).toBe('An ID is required')
  })
  it('names a variable listed twice, and a value without a name', () => {
    const f = ok()
    f.env.push({ uid: 1, key: 'A', value: 'x', masked: false }, { uid: 2, key: 'A', value: 'y', masked: false })
    expect(formErrors(f).env).toBe('A is listed twice')
    expect(formErrors({ ...ok(), env: [{ uid: 3, key: ' ', value: 'v', masked: false }] }).env).toBe('Give every variable a name')
  })
})

describe('signalOut', () => {
  it('leaves the bell out unless the agent had a signal', () => {
    expect(signalOut('bell', '')).toBeUndefined()
    expect(signalOut('bell', '', { kind: 'pattern', pattern: 'x' })).toEqual({ kind: 'bell' })
  })
  it('keeps the tool-events flag the form has no control for', () => {
    expect(signalOut('pattern', '^> $', { kind: 'hook', toolEvents: true })).toEqual({ kind: 'pattern', pattern: '^> $', toolEvents: true })
    expect(signalOut('none', '', { kind: 'hook', toolEvents: true })).toEqual({ kind: 'none', toolEvents: true })
  })
})

describe('AGENT_ID_PATTERN and slugId', () => {
  it('a suggested ID is empty or one the server takes, never ending in a dash', () => {
    for (const name of ['Claude (opus)', 'a'.repeat(31) + ' b', '--x--', 'My  Tool -- v2', '!!!', 'x'.repeat(40)]) {
      const id = slugId(name)
      expect(id === '' || AGENT_ID_PATTERN.test(id)).toBe(true)
      expect(id.endsWith('-')).toBe(false)
    }
  })
})
```

Add to the `slugId` block of `web/app/utils/argv.test.ts`:

```ts
  it('never ends in a dash when the cut falls on one', () => {
    expect(slugId('a'.repeat(31) + ' b')).toBe('a'.repeat(31))
  })
```

- [ ] **Step 2: Run them to see them fail**

Run: `npm --prefix web test -- agentForm argv`
Expected: FAIL. `./agentForm` does not exist, and `slugId('a'.repeat(31) + ' b')` ends in `-`.

- [ ] **Step 3: Implement `agentForm.ts` and fix `slugId`**

In `web/app/utils/argv.ts`, `slugId` cuts first and trims after:

```ts
/** A catalog ID (`[a-z0-9-]`, at most 32 characters, no dash at either end) suggested by a display name; empty when nothing usable is left. */
export function slugId(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 32)
    .replace(/-+$/, '')
}
```

Create `web/app/utils/agentForm.ts`:

```ts
import type { AgentInfo, AgentInput, AgentSignal } from '~/composables/useSessions'
import { hasOpenQuote, splitArgs } from './argv'

/** What GET /api/catalog shows in place of every stored env value (catalog.RedactedValue). Sent back, it keeps the stored value. */
export const MASK = '***'

/** The server's rule for an agent ID: idPattern in internal/catalog/catalog.go. */
export const AGENT_ID_PATTERN = /^[a-z0-9-]{1,32}$/

export type SignalKind = AgentSignal['kind']

/** One row of the environment editor. A `masked` row is a value stored on the server, which the form never sees. */
export interface EnvRow {
  uid: number
  key: string
  value: string
  masked: boolean
}

/** The add-agent form. `pendingCommand` is what is typed in the command field and not yet an argument. */
export interface AgentForm {
  name: string
  id: string
  command: string[]
  pendingCommand: string
  description: string
  env: EnvRow[]
  allowArgs: boolean
  signal: SignalKind
  pattern: string
}

export type Field = 'name' | 'id' | 'command' | 'pattern' | 'env'

/** The form for `a`, or an empty one. Stored env values come in masked, by name; passthrough names follow as rows without a value. */
export function formFromAgent(a: AgentInfo | undefined, uid: () => number): AgentForm {
  return {
    name: a?.name ?? '',
    id: a?.id ?? '',
    command: [...(a?.command ?? [])],
    pendingCommand: '',
    description: a?.description ?? '',
    env: [
      ...Object.keys(a?.env ?? {})
        .sort()
        .map((key) => ({ uid: uid(), key, value: '', masked: true })),
      ...(a?.envPassthrough ?? []).map((key) => ({ uid: uid(), key, value: '', masked: false })),
    ],
    allowArgs: a?.allowArgs ?? true,
    signal: a?.signal?.kind ?? 'bell',
    pattern: a?.signal?.pattern ?? '',
  }
}

/** The argv the form holds: its arguments, then what is typed and not yet one. */
export function commandOf(f: Pick<AgentForm, 'command' | 'pendingCommand'>): string[] {
  return [...f.command, ...splitArgs(f.pendingCommand)]
}

/** What is wrong with the form, by field; empty when it can be saved. */
export function formErrors(f: AgentForm): Partial<Record<Field, string>> {
  const e: Partial<Record<Field, string>> = {}
  if (!f.name.trim()) e.name = 'Give the agent a name'
  if (!AGENT_ID_PATTERN.test(f.id)) e.id = f.id ? 'Use lowercase letters, digits and dashes, up to 32' : 'An ID is required'
  if (hasOpenQuote(f.pendingCommand)) e.command = 'Close the quote, or remove it'
  else if (!commandOf(f)[0]?.trim()) e.command = 'Add the command to run'
  // Spaces count in a regex (a "> " prompt), so trim only to tell whether anything was typed.
  if (f.signal === 'pattern' && !f.pattern.trim()) e.pattern = 'Enter the pattern to look for'
  const seen = new Set<string>()
  for (const r of f.env) {
    const key = r.key.trim()
    if (!key) {
      if (r.value) e.env = 'Give every variable a name'
      continue
    }
    if (seen.has(key)) e.env = `${key} is listed twice`
    seen.add(key)
    if (!r.masked && r.value === MASK) e.env = `${key}: ${MASK} stands for a stored value; type the real value`
  }
  return e
}

/** The signal to save: the bell is what an agent without a signal gets, so it is left out unless the agent had one. The tool-events flag, which the form has no control for, stays. */
export function signalOut(kind: SignalKind, pattern: string, prev?: AgentSignal): AgentSignal | undefined {
  const toolEvents = prev?.toolEvents || undefined
  switch (kind) {
    case 'pattern':
      return { kind: 'pattern', pattern, toolEvents }
    case 'hook':
      return { kind: 'hook', toolEvents }
    case 'none':
      return { kind: 'none', toolEvents }
    default:
      return prev ? { kind: 'bell', toolEvents } : undefined
  }
}

/** The body of POST /api/catalog. A masked row sends the mask, which keeps the stored value; a row without a value is a passthrough name. What the form has no control for comes from `prev`, the agent edited. */
export function agentPayload(f: AgentForm, prev?: AgentInfo): AgentInput {
  const env: Record<string, string> = {}
  const passthrough: string[] = []
  for (const row of f.env) {
    const key = row.key.trim()
    if (!key) continue
    if (row.masked) env[key] = MASK
    else if (row.value !== '') env[key] = row.value
    else passthrough.push(key)
  }
  return {
    id: f.id,
    name: f.name.trim(),
    description: f.description.trim() || undefined,
    command: commandOf(f),
    allowArgs: f.allowArgs,
    env: Object.keys(env).length ? env : undefined,
    envPassthrough: passthrough.length ? passthrough : undefined,
    cwd: prev?.cwd,
    icon: prev?.icon,
    adapter: prev?.adapter,
    signal: signalOut(f.signal, f.pattern, prev?.signal),
  }
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `npm --prefix web test -- agentForm argv`
Expected: PASS.

- [ ] **Step 5: Use the module in the slideover, post `command[0]` only, and use the Nuxt UI radio group**

In `web/app/composables/useSessions.ts`:

```ts
    /** Whether the program resolves on the server. Only the program is sent; nothing is run. */
    checkCommand: (program: string) =>
      request<{ found: boolean; path?: string }>('/api/catalog/check', { method: 'POST', body: { command: [program] } }),
```

In `web/app/components/AddAgentSlideover.vue`, delete `EnvRow`, `MASK`, `ID_PATTERN`, `signalOut`, `payload` and the inline `errors` body, and import the module:

```ts
import { agentPayload, commandOf, formErrors, formFromAgent, type AgentForm, type Field } from '~/utils/agentForm'

let nextUid = 0
const uid = () => nextUid++
const form = reactive<AgentForm>(formFromAgent(undefined, uid))

function reset() {
  Object.assign(form, formFromAgent(props.agent, uid))
  idTouched.value = !!props.agent
  idLocked.value = !!props.agent
  attempted.value = false
  error.value = ''
  scheduleCheck()
}

const errors = computed(() => formErrors(form))
const shown = computed<Partial<Record<Field, string>>>(() => (attempted.value ? errors.value : {}))
```

The check uses the program alone:

```ts
function scheduleCheck() {
  clearTimeout(checkTimer)
  const seq = ++checkSeq
  const program = commandOf(form)[0]?.trim()
  if (!program) {
    check.value = { state: 'idle' }
    return
  }
  check.value = { state: 'pending' }
  checkTimer = setTimeout(async () => {
    try {
      const r = await api.checkCommand(program)
      if (seq === checkSeq) check.value = r.found ? { state: 'found', path: r.path ?? program } : { state: 'missing' }
    } catch {
      if (seq === checkSeq) check.value = { state: 'failed' }
    }
  }, 400)
}
watch(() => commandOf(form)[0], scheduleCheck)
```

`addEnv` pushes `{ uid: uid(), key: '', value: '', masked: false }`. `persist` saves `agentPayload(form, props.agent)`. The command field binds the pending text:

```vue
<ArgvInput v-model="form.command" v-model:pending="form.pendingCommand" placeholder="aider --model sonnet" :invalid="!!shown.command" />
```

The signal cards become a `URadioGroup`. The radio group handles arrow keys, focus and `aria-checked`:

```ts
const signalItems = computed(() => [
  {
    value: 'hook',
    label: props.agent?.adapter ? `Hook command · adapter ${props.agent.adapter}` : 'Hook command',
    description: hookAvailable.value ? 'The agent reports through its own hooks.' : 'pick an adapter (coming with Events)',
    disabled: !hookAvailable.value,
  },
  { value: 'bell', label: 'Bell / OSC 9·777', description: 'A terminal bell or notification escape. The default.' },
  { value: 'pattern', label: 'Screen pattern', description: 'A regex matched against the last screen line.' },
  { value: 'none', label: 'None', description: 'Never flagged; you watch it yourself.' },
])
```

```vue
<div class="text-sm">
  <URadioGroup
    v-model="form.signal"
    :items="signalItems"
    variant="card"
    legend="How does it tell Conductor it needs you?"
    :ui="{ fieldset: 'mt-2 grid gap-2 sm:grid-cols-2', legend: 'font-medium text-default' }"
  />
  <UFormField v-if="form.signal === 'pattern'" label="Pattern" name="pattern" hint="RE2 regular expression" :error="shown.pattern" class="mt-3">
    <UInput v-model="form.pattern" placeholder="^> $" autocapitalize="off" spellcheck="false" :ui="{ base: 'font-mono' }" class="w-full" />
    <template #help>Matched against the last line on screen once output goes quiet.</template>
  </UFormField>
</div>
```

Delete `signalCards` and the hand-rolled `role="radio"` buttons.

- [ ] **Step 6: Make `ArgvInput` keep an unclosed quote typed and use the tag input's classes**

In `web/app/components/ArgvInput.vue`, replace the script with:

```ts
<script setup lang="ts">
import theme from '#build/ui/input-tags'
import { tv } from '@nuxt/ui/utils/tv'
import { hasOpenQuote, splitArgs } from '~/utils/argv'

/**
 * Edits an argv as chips, one per element. Enter, or a space outside quotes,
 * turns the typed text into chips; Backspace on an empty field removes the
 * last one. Pasted text with spaces or quotes is split like the Launch
 * dialog's extra arguments. Nothing here is a shell: quotes only group. Text
 * with a quote still open stays typed (`pending`), for the form to say so,
 * rather than turning into a chip that holds the quote.
 */
const model = defineModel<string[]>({ default: () => [] })
/** What is typed and not yet a chip. */
const pending = defineModel<string>('pending', { default: '' })
const props = defineProps<{ placeholder?: string; invalid?: boolean }>()

const appConfig = useAppConfig()
// The ring, padding and focus outline of Nuxt UI's tag input, which this is a version of.
const ui = computed(() =>
  tv({ extend: theme, ...((appConfig.ui as Record<string, object> | undefined)?.inputTags ?? {}) })({
    variant: 'outline',
    size: 'md',
    color: props.invalid ? 'error' : 'primary',
    highlight: props.invalid,
  }),
)
const field = useTemplateRef<{ inputRef: HTMLInputElement | null }>('field')

function add(args: string[]) {
  if (args.length) model.value = [...model.value, ...args]
}

/** Turns what is typed into chips, unless a quote is still open. */
function commit() {
  if (hasOpenQuote(pending.value)) return
  const args = splitArgs(pending.value)
  pending.value = ''
  add(args)
}

function remove(i: number) {
  model.value = model.value.filter((_, at) => at !== i)
  field.value?.inputRef?.focus()
}

// A space ends an argument unless a quote is still open. Watching the text
// rather than the key also covers soft keyboards and pasted text that ends in
// a space.
watch(pending, (t) => {
  if (/\s$/.test(t) && !hasOpenQuote(t)) commit()
})

function onKeydown(e: KeyboardEvent) {
  if (e.isComposing) return
  if (e.key === 'Enter') {
    e.preventDefault()
    commit()
  } else if (e.key === 'Backspace' && pending.value === '' && model.value.length) {
    e.preventDefault()
    model.value = model.value.slice(0, -1)
  }
}

function onPaste(e: ClipboardEvent) {
  const pasted = e.clipboardData?.getData('text') ?? ''
  // One plain word goes into the field as usual; anything with spaces or quotes becomes chips.
  if (!/[\s"']/.test(pasted.trim())) return
  e.preventDefault()
  add([...splitArgs(pending.value), ...splitArgs(pasted)])
  pending.value = ''
}
</script>
```

Replace the template with this one. The root `div` takes the theme's classes in place of the copied ring classes, the chips stay as they are, and the input binds `pending`:

```vue
<template>
  <div :class="ui.root({ class: ui.base({ class: 'flex min-h-8 w-full cursor-text flex-wrap items-center gap-1.5' }) })" @click="field?.inputRef?.focus()">
    <UBadge v-for="(arg, i) in model" :key="i" color="neutral" variant="subtle" size="md" class="max-w-full font-mono">
      <span class="truncate" :class="arg === '' && 'text-muted'">{{ arg === '' ? "''" : arg }}</span>
      <template #trailing>
        <button
          type="button"
          class="-me-0.5 inline-flex flex-none rounded-xs text-dimmed transition-colors hover:text-default"
          :aria-label="`Remove ${arg === '' ? 'empty argument' : arg}`"
          @click.stop="remove(i)"
        >
          <UIcon name="i-lucide-x" class="size-3.5" />
        </button>
      </template>
    </UBadge>
    <UInput
      ref="field"
      v-model="pending"
      variant="none"
      :placeholder="model.length ? undefined : placeholder"
      :ui="{ root: 'min-w-24 flex-1', base: 'p-0 font-mono ring-0' }"
      autocapitalize="off"
      spellcheck="false"
      @keydown="onKeydown"
      @paste="onPaste"
      @blur="commit"
    />
  </div>
</template>
```

- [ ] **Step 7: Run the web gate**

Run: `npm --prefix web test && npm --prefix web run typecheck`
Expected: PASS. `nuxt typecheck` prepares `.nuxt` first, so `#build/ui/input-tags` resolves. If an editor or vitest cannot find it, run `npm --prefix web run postinstall` (`nuxt prepare`).

Check it in a browser with the recipe in Verification. In the Add agent slideover: Tab into the signal group, and the arrow keys move between Bell, Screen pattern and None (Hook is disabled for a new agent). Type `aider --model "gpt` and press Enter: the text stays in the field, and Save shows "Close the quote, or remove it" under Command. Type `***` as a new variable's value: Save shows the env error. The command field's ring matches the other inputs' and turns red on an error.

- [ ] **Step 8: Commit**

```bash
git add web/app/utils/agentForm.ts web/app/utils/agentForm.test.ts web/app/utils/argv.ts web/app/utils/argv.test.ts web/app/components/AddAgentSlideover.vue web/app/components/ArgvInput.vue web/app/composables/useSessions.ts
git commit -m "web: add-agent form logic in utils/agentForm with tests; radio group, open quotes kept, program-only check"
```

---

### Task 4: File reads skip copies of the config and catalog files

Triage item #30. The spec names the config file. The catalog file (`catalogPath`) can hold the same secrets (agents' `env`), so it gets the same rule.

**Files:**
- Modify: `internal/session/files.go` (`ResolvePath` doc, `insideAny`), `internal/session/files_test.go`
- Modify: `internal/api/files.go` (`fileDeny`), `internal/api/api_test.go`
- Modify: `README.md` (security bullet on the file viewer), `docs/protocol.md` (file reads paragraph, around line 357)

**Interfaces:**
- Consumes: nothing new.
- Produces: a deny entry may end in `*`. `"/etc/conductor/conductor.json*"` denies, in `/etc/conductor`, every file or directory whose name starts with `conductor.json`, ignoring case. The directory is compared as a file (`os.SameFile`) like the other entries. `session.ResolvePath(root, raw string, deny []string)` keeps its signature.

- [ ] **Step 1: Write the failing tests**

Add to `internal/session/files_test.go`:

```go
// An entry ending in "*" denies, in its directory, every name that starts with
// the rest of its last element, ignoring case: the copies an editor or a
// person leaves beside a config file hold its secrets too. The files beside it
// with other names still read, and an entry whose directory is gone denies
// nothing.
func TestResolvePathDeniesCopiesBesideADeniedFile(t *testing.T) {
	root := setupTree(t)
	for _, name := range []string{"conductor.json", "conductor.json.bak", "conductor.json~", "Conductor.JSON.orig", "conductor.example.json", "other.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(`{"adminToken":"secret"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "conductor.json.bak"), filepath.Join(root, "sub", "innocent.txt")); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "conductor.json")
	deny := []string{cfg, cfg + "*"}
	for _, p := range []string{"conductor.json", "conductor.json.bak", "conductor.json~", "Conductor.JSON.orig", "sub/innocent.txt", "conductor.json.swp"} {
		if r, err := ResolvePath(root, p, deny); err == nil {
			t.Errorf("%q: resolved to %s, want it denied", p, r)
		}
		if h, body := ReadPath(root, p, false, deny); h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || body != nil {
			t.Errorf("%q: %+v %q", p, h, body)
		}
	}
	for _, p := range []string{"conductor.example.json", "other.json", ".", "sub/file.go"} {
		if _, err := ResolvePath(root, p, deny); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	if _, err := ResolvePath(root, "other.json", []string{filepath.Join(root, "gone", "x.json*")}); err != nil {
		t.Fatalf("an entry in a directory that does not exist: %v", err)
	}
}
```

Add to `internal/api/api_test.go`:

```go
// The copies an editor leaves beside the config file and the catalog file
// (.bak, ~, .orig) are as secret as the files: no read reaches them, over
// HTTP or in-band. The example config beside them still reads.
func TestFileReadsNeverReachCopiesOfTheConfigOrCatalogFile(t *testing.T) {
	var configFile, catalogFile string
	e := newTestEnv(t, func(c *config.Config) {
		configFile = filepath.Join(c.DefaultCwd, "conductor.json")
		catalogFile = filepath.Join(c.DefaultCwd, "agents.json")
		c.Path, c.CatalogPath = configFile, catalogFile
	})
	copies := []string{"conductor.json.bak", "conductor.json~", "CONDUCTOR.json.orig", "agents.json.bak", "agents.json~"}
	for _, name := range append(copies, "conductor.example.json") {
		if err := os.WriteFile(filepath.Join(e.root, name), []byte(`{"adminToken": "`+adminToken+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id := e.createSession("cat")
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	view := lo["token"].(string)
	for _, p := range copies {
		if status, got, code := e.getFile(id, view, p, "raw"); status != http.StatusForbidden || code != "denied" || strings.Contains(got, adminToken) {
			t.Errorf("HTTP %q: %d %s", p, status, got)
		}
	}
	if status, _, _ := e.getFile(id, view, "conductor.example.json", "raw"); status != http.StatusOK {
		t.Fatalf("the example config: %d", status)
	}
	c := dialViewer(t, e, id, view)
	c.hello(80, 24)
	c.expectControl(proto.CtlReady)
	for i, p := range copies {
		reqID := fmt.Sprintf("c%d", i)
		c.send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: reqID, Path: p}))
		if h, b := c.expectFile(reqID); h.Kind != "error" || h.Error == nil || h.Error.Code != "denied" || len(b) != 0 {
			t.Errorf("file_get %q: %+v %q", p, h, b)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/session/ ./internal/api/ -count=1 -run 'CopiesBesideADeniedFile|CopiesOfTheConfigOrCatalogFile'`
Expected: FAIL. `conductor.json.bak` resolves, and the API serves it.

- [ ] **Step 3: Implement star entries**

In `internal/session/files.go`, the last sentence of the `ResolvePath` comment becomes "…or is one of the deny entries or inside one (the server passes its data directory, its config file and its catalog file, and a star entry for each of the two files, see insideAny)". `insideAny` becomes:

```go
// insideAny reports whether real, an absolute path with its symlinks resolved,
// is one of deny or inside one. An entry is a directory, which denies
// everything in it, or a single file, which only real itself can match.
// Entries are compared as files (os.SameFile), not by name, so neither a
// symlink to one, a hard link to a denied file, nor another spelling on a
// case-insensitive file system gets around the rule. An entry that ends in
// "*" denies, in the directory it names, everything whose name starts with the
// rest of its last element, ignoring case: "/etc/conductor/conductor.json*"
// denies conductor.json.bak and Conductor.JSON~ there, and the directory is
// compared as a file too. An entry that does not exist, or whose directory
// does not, denies nothing and is skipped.
func insideAny(real string, deny []string) bool {
	type prefix struct {
		dir  os.FileInfo
		name string // lower case
	}
	var denied []os.FileInfo
	var prefixes []prefix
	for _, d := range deny {
		if p, ok := strings.CutSuffix(d, "*"); ok {
			if fi, err := os.Stat(filepath.Dir(p)); err == nil {
				prefixes = append(prefixes, prefix{fi, strings.ToLower(filepath.Base(p))})
			}
			continue
		}
		if fi, err := os.Stat(d); err == nil {
			denied = append(denied, fi)
		}
	}
	if len(denied) == 0 && len(prefixes) == 0 {
		return false
	}
	// Every ancestor counts, above root too: a session may run inside one.
	for p := real; ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil {
			for _, d := range denied {
				if os.SameFile(fi, d) {
					return true
				}
			}
		}
		if len(prefixes) > 0 {
			if dir, err := os.Stat(filepath.Dir(p)); err == nil {
				base := strings.ToLower(filepath.Base(p))
				for _, pr := range prefixes {
					if strings.HasPrefix(base, pr.name) && os.SameFile(dir, pr.dir) {
						return true
					}
				}
			}
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}
```

A name that does not exist yet (`conductor.json.swp`) is refused as `denied`, as names inside the data directory are. Nothing about the directory's contents leaks.

In `internal/api/files.go`:

```go
// fileDeny returns what no file read of a server session may reach, even
// inside its working directory: the data directory, whose catalog.json holds
// the agents' env secrets; the config file, which holds the admin token and
// host tokens; and the catalog file (catalogPath), which can hold env secrets.
// For each of the two files there is also a star entry (see
// session.ResolvePath): beside it, every name that starts with its name,
// ignoring case, such as the conductor.json.bak or conductor.json~ an editor
// leaves. It names both the configured data directory and the one st writes
// to, should the two ever differ. A directory is denied with everything in
// it, a file on its own.
func fileDeny(cfg *config.Config, st *store.Store) []string {
	var deny []string
	if cfg.DataDir != "" {
		deny = append(deny, cfg.DataDir)
	}
	if st != nil && st.Dir() != cfg.DataDir {
		deny = append(deny, st.Dir())
	}
	for _, file := range []string{cfg.Path, cfg.CatalogPath} {
		if file != "" {
			deny = append(deny, file, file+"*")
		}
	}
	return deny
}
```

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/session/ ./internal/api/ -count=1 -race -run 'Resolve|ReadPath|FileReads|Files'`
Expected: PASS. `TestFileReadsNeverReachTheConfigOrCatalogFile` still reads `conductor.example.json`.

- [ ] **Step 5: Docs and commit**

In `README.md`, the security bullet "The file viewer of a server session never serves that directory, the config file or the catalog file" gains ", nor a file beside those two whose name starts with theirs (`conductor.json.bak`, `conductor.json~`)". Make the same addition in `docs/protocol.md` where file reads refuse the data directory (around line 357).

Run: `make lint && go test -race -count=1 ./internal/session/ ./internal/api/`

```bash
git add internal/session/files.go internal/session/files_test.go internal/api/files.go internal/api/api_test.go README.md docs/protocol.md
git commit -m "files: refuse the copies left beside the config and catalog files"
```

---

### Task 5: Sessions and events

Triage items #31 to #37.

**Files:**
- Modify: `internal/api/ws_viewer.go` (`serveHostedViewer`, `handleViewerSignal`), `internal/signal/hosted.go` (`HostDisconnected`, `SetAttentionFull`), `internal/signal/signal_test.go`
- Modify: `internal/session/local.go` (`Options.OnActivity`, `record`, `recordOwn`, `setAttention`), `internal/session/activity.go` (`oneLine`), `internal/session/attention.go` (`CleanName`)
- Create: `internal/session/record_test.go` (the Record and hook tests moved from `local_test.go`); Modify: `internal/session/local_test.go`
- Modify: `internal/api/sessions.go` (`OnActivity` wiring, `localActivity` deleted), `internal/api/events.go` (`activityEvent.State`, the `activity` comment), `internal/api/webhooks.go` (the `eventTypeOf` comment), `internal/api/events_test.go`, `internal/api/attention.go` (hosted branch), `internal/api/ws_e2e_test.go`, `internal/api/api_test.go`
- Modify: `internal/hostagent/agent.go` (`onLocalActivity`; the no-launch-route line at warn), `internal/hostagent/activity_test.go:160`, `internal/hostagent/hooks_test.go` (two `level=INFO` checks)
- Modify: `internal/crew/run_test.go` (`fakeLauncher.activity`)
- Modify: `internal/notify/notify.go` (`Send`), `internal/notify/notify_test.go`
- Modify: `web/app/utils/protocol.ts` (`ActivityEntry.state`), `web/app/utils/events.ts` (`eventTypeOf`, `attentionSettled`), `web/app/utils/events.test.ts`
- Modify: `docs/protocol.md` (the streaming paragraph and the `/api/events` row, the attention limit paragraphs, the webhook paragraph's "read then" sentence, `conductor notify`), `README.md` (`conductor notify` retry)

The SSE `activity` event is defined in `internal/api/events.go`, not `internal/proto`. Its three places are `events.go`, `protocol.ts` and `protocol.md`. The new field is bounded: it is one of `needs_input`, `working` or `done`, at most 11 bytes, and the test pins that nothing else gets through.

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  ```go
  package session
  // Options.OnActivity now receives the state the entry records:
  OnActivity func(sessionID string, e ActivityEntry, state AttentionState)
  func (s *Local) recordOwn(e ActivityEntry, state AttentionState)

  package api
  type activityEvent struct { SessionID string `json:"sessionId"`; session.ActivityEntry; State session.AttentionState `json:"state,omitempty"` }

  package notify
  const sendTimeout = 5 * time.Second
  var retryDelays = []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second}
  func (r Request) attentionWord() bool
  ```
  ```ts
  // web/app/utils/protocol.ts, ActivityEntry gains
  /** GET /api/events only: for an attention entry, the state it records. */
  state?: 'needs_input' | 'working' | 'done'
  ```

- [ ] **Step 1: Write the failing test for a departing host**

Add to `internal/api/ws_e2e_test.go`:

```go
// A host that leaves sends its viewer one error frame, host_disconnected, then
// the close. The frames it still owed come first, and the viewer's reader
// never closes the connection under the pump, whether the viewer is quiet or
// typing into the relay.
func TestAHostThatLeavesSendsItsViewerOneError(t *testing.T) {
	for _, typing := range []bool{false, true} {
		t.Run(fmt.Sprintf("typing=%v", typing), func(t *testing.T) {
			e := newTestEnv(t, nil)
			host := dialFakeHost(t, e, "hosted-agent-token")
			v := dialViewer(t, e, host.sessionID, adminToken)
			v.hello(80, 24)
			v.expectControl(proto.CtlWelcome)
			host.expect(proto.HostViewerJoin)
			relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.Simple{T: proto.SigRelay})
			v.send(relay)
			for {
				f, err := v.read()
				if err != nil {
					t.Fatalf("waiting for relay_ok: %v", err)
				}
				if tt, _ := proto.ParseHeader(f.Payload); f.Type == proto.TypeSignal && tt == proto.SigRelayOK {
					break
				}
			}
			host.c.CloseNow()
			errs := 0
			for {
				if typing {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					_ = v.c.Write(ctx, websocket.MessageBinary, proto.Encode(proto.TypeInput, []byte("x")))
					cancel()
				}
				f, err := v.read()
				if err != nil {
					if got := websocket.CloseStatus(err); got != proto.CloseSessionEnded {
						t.Fatalf("close status %d (%v), want %d", got, err, proto.CloseSessionEnded)
					}
					break
				}
				if f.Type != proto.TypeControl {
					continue
				}
				var m map[string]any
				json.Unmarshal(f.Payload, &m)
				if m["t"] == proto.CtlError {
					errs++
					if m["code"] != proto.ErrCodeHostDisconnected {
						t.Fatalf("error frame %v", m)
					}
				}
			}
			if errs != 1 {
				t.Fatalf("%d error frames, want one", errs)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api/ -run TestAHostThatLeavesSendsItsViewerOneError -count=3`
Expected: FAIL in `typing=false` with "2 error frames, want one". `HostDisconnected` queues one frame, and `wsSink.Close` writes another.

- [ ] **Step 3: One error frame, and the reader waits for the pump**

In `internal/signal/hosted.go`, `HostDisconnected` stops queuing the frame. The viewer's sink writes it as it closes (`wsSink.Close` for `ErrHostGone`):

```go
	h.mu.Unlock()
	// Each viewer is closed with ErrHostGone: Pump writes what the host still
	// owed it, then closes its sink, which tells the viewer why with one error
	// frame (host_disconnected).
	for _, v := range viewers {
		v.close(ErrHostGone)
	}
```

In `internal/api/ws_viewer.go`, `serveHostedViewer` keeps the pump's end and waits for it in place of `sink.Close(signal.ErrHostGone)`:

```go
	// Writer: drain frames queued by the host side. pumped closes when Pump
	// has closed the sink.
	pumped := make(chan struct{})
	go func() {
		defer close(pumped)
		v.Pump(sink)
	}()
```

```go
				case errors.Is(err, signal.ErrHostGone):
					hostGone(sink, pumped)
					return
```

`handleViewerSignal` takes `pumped` too (its call in the read loop becomes `s.handleViewerSignal(sink, pumped, hs, v, f.Payload)`), and its `fail` uses the same wait:

```go
func (s *Server) handleViewerSignal(sink *wsSink, pumped <-chan struct{}, hs *signal.HostedSession, v *signal.Viewer, payload []byte) bool {
	// (the header parse stays as it is)
	fail := func(err error) bool {
		if errors.Is(err, signal.ErrHostGone) {
			hostGone(sink, pumped)
			return false
		}
		return true
	}
```

Then add, next to `keepalive` in `wssink.go`:

```go
// hostGone ends a hosted viewer whose host has left. The host's disconnect
// closes the viewer (HostDisconnected), and Pump writes what the host still
// owed it and then closes the sink with one error frame. The reader waits for
// that rather than closing the sink under Pump. Should the close not come
// within writeTimeout (the host left between a check and its disconnect), the
// reader closes the sink itself.
func hostGone(sink *wsSink, pumped <-chan struct{}) {
	select {
	case <-pumped:
	case <-time.After(writeTimeout):
		sink.Close(signal.ErrHostGone)
	}
}
```

Run: `go test ./internal/api/ ./internal/signal/ -run 'HostThatLeaves|ViewerPump|Register' -count=5 -race`
Expected: PASS.

- [ ] **Step 4: Write the failing tests for the state that travels with the entry**

Add to `internal/session/local_test.go` (it moves to `record_test.go` in Step 13):

```go
// The attention state an attention entry records travels with it to
// OnActivity: the state set in the same critical section as the entry's
// stamp, whatever the session shows by the time the hook runs. Every other
// entry comes with none.
func TestOnActivityGetsTheStateTheEntryRecords(t *testing.T) {
	type call struct {
		typ, msg string
		state    AttentionState
	}
	var mu sync.Mutex
	var calls []call
	s, _ := newLocalWith(t, Options{OnActivity: func(_ string, e ActivityEntry, state AttentionState) {
		mu.Lock()
		calls = append(calls, call{e.Type, e.Message, state})
		mu.Unlock()
	}})
	s.SetAttention(AttentionNeedsInput, "Allow Bash?", SourceAPI)
	s.SetAttention(AttentionDone, "", SourceAPI)
	s.SetAttention(AttentionWorking, "compiling", SourceAPI)
	s.Record(ActivityEntry{Type: ActivityProgress, Message: "1/3"})
	mu.Lock()
	defer mu.Unlock()
	want := []call{
		{ActivityAttention, "Allow Bash?", AttentionNeedsInput},
		{ActivityAttention, "done", AttentionDone},
		{ActivityAttention, "compiling", AttentionWorking},
		{ActivityProgress, "1/3", ""},
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("OnActivity got %+v, want %+v", calls, want)
	}
}
```

(`local_test.go` imports `slices` for this test.)

Add to `internal/api/events_test.go`:

```go
// An attention entry's event carries the state it records, so a client types
// it without waiting for the session change; other entries carry none, and
// neither does an attention entry with a state that is not one of the three.
func TestEventHubActivityCarriesTheAttentionState(t *testing.T) {
	h := newEventHub()
	ch := h.subscribe()
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	h.activity("s1", session.ActivityEntry{At: at, Type: session.ActivityAttention, Message: "Allow Bash?"}, session.AttentionNeedsInput)
	h.activity("s1", session.ActivityEntry{At: at, Type: session.ActivityProgress, Message: "1/3"}, session.AttentionNeedsInput)
	h.activity("s1", session.ActivityEntry{At: at, Type: session.ActivityAttention, Message: "x"}, "bogus")
	for _, want := range []string{
		`{"sessionId":"s1","at":"2026-10-01T09:00:00Z","type":"attention","message":"Allow Bash?","state":"needs_input"}`,
		`{"sessionId":"s1","at":"2026-10-01T09:00:00Z","type":"progress","message":"1/3"}`,
		`{"sessionId":"s1","at":"2026-10-01T09:00:00Z","type":"attention","message":"x"}`,
	} {
		if got := string(<-ch); got != "event: activity\ndata: "+want+"\n\n" {
			t.Fatalf("got %q, want data %s", got, want)
		}
	}
}
```

Add to `internal/api/api_test.go`:

```go
// A server session's attention entry reaches the event stream with the state
// it records.
func TestAttentionEntriesCarryTheirStateOnTheEventStream(t *testing.T) {
	e := newTestEnv(t, nil)
	events := e.sse(t)
	id := e.createSession("cat")
	tok := e.agentToken(id)
	if resp, out := e.do("POST", "/api/sessions/"+id+"/attention", tok, map[string]any{"state": "needs_input", "message": "Allow Bash?"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("attention: %d %v", resp.StatusCode, out)
	}
	ev := e.waitEvent(t, events, isActivity("attention", id))
	if m := activityPayload(t, ev); m["state"] != "needs_input" || m["message"] != "Allow Bash?" {
		t.Fatalf("activity %v", m)
	}
}
```

Add to `internal/api/ws_e2e_test.go`:

```go
// A hosted session's attention entry carries the state its host sends with it.
func TestHostedAttentionEntriesCarryTheirStateOnTheEventStream(t *testing.T) {
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "hosted-agent-token")
	events := e.sse(t)
	msg := hostActivity(session.ActivityAttention, func(a *proto.Activity) { a.Message = "waiting" })
	msg.State = "needs_input"
	host.send(msg)
	ev := e.waitEvent(t, events, isActivity("attention", host.sessionID))
	if m := activityPayload(t, ev); m["state"] != "needs_input" {
		t.Fatalf("activity %v", m)
	}
}
```

- [ ] **Step 5: Run them to see them fail**

Run: `go test ./internal/session/ ./internal/api/ -count=1 -run 'OnActivityGetsTheState|CarriesTheAttentionState|CarryTheirState'`
Expected: build failure, because the `OnActivity` literal has three parameters. Once the signature compiles, the API tests fail on the missing `state`.

- [ ] **Step 6: Hand the state to `OnActivity` and put it on the event**

In `internal/session/local.go`:

```go
	// OnActivity is called (outside the session lock) with the session ID,
	// the stored entry and, for the attention entry of a change the session
	// applied, the attention state it records: the state set in the same
	// critical section as the entry's stamp ("" for every other entry). It
	// runs on the recording goroutine, which may be the one reading the
	// process, so it must not block. Because it runs after the lock is
	// released, calls for different entries can overlap and arrive out of
	// order: implementations must be safe for concurrent use, and must take
	// the state from here, never from Info, which may have moved on. An entry
	// the event bucket drops never reaches it; the attention entry of an
	// attention change the session applied always does.
	OnActivity func(sessionID string, e ActivityEntry, state AttentionState)
```

```go
func (s *Local) Record(e ActivityEntry) bool {
	return s.record(e, bucketed(e.Type), "")
}

// recordOwn records an entry the session makes itself without asking the
// event bucket, whatever its type, and hands OnActivity the attention state
// it records. It records the attention entry of an attention change the
// session has applied, which must not be dropped while the state it records
// is showing: a report from outside paid its token before the change
// (TrySetAttentionFull), and what the session observes itself (the bell, OSC
// notifications, the screen pattern) is held to its own pace and spends none.
func (s *Local) recordOwn(e ActivityEntry, state AttentionState) {
	s.record(e, false, state)
}

// record is Record, with limited saying whether the entry spends a token of
// the event bucket, and state what OnActivity is told the entry records.
func (s *Local) record(e ActivityEntry, limited bool, state AttentionState) bool {
	s.mu.Lock()
	if limited && !s.events.Take(time.Now()) {
		log := s.log
		s.mu.Unlock()
		countDrop(&s.dropped, log, e.Type)
		return false
	}
	// The ring write and the broadcast share one critical section, so a client
	// attaching meanwhile finds the entry in its replay or receives the
	// broadcast, never both.
	e = s.activity.Add(e)
	s.hub.Broadcast(proto.MustControl(EntryToProto(e)))
	id := s.info.ID
	s.mu.Unlock()
	if s.opts.OnActivity != nil {
		s.opts.OnActivity(id, e, state)
	}
	return true
}
```

The body is the old one with `state` passed on; nothing else in it changes.

In `setAttention`, the call becomes `s.recordOwn(ActivityEntry{At: *att.Since, Type: ActivityAttention, Message: label}, state)`.

In `internal/api/sessions.go`, delete `localActivity` and pass `OnActivity: s.events.activity`. The hub's signature already matches.

In `internal/api/events.go`:

```go
// activityEvent is the data of an `activity` event: the entry, the session it
// belongs to and, for an attention entry, the attention state it records.
type activityEvent struct {
	SessionID string `json:"sessionId"`
	session.ActivityEntry
	// State is the attention state an attention entry records: needs_input,
	// working or done. It is absent for any other entry, and for one whose
	// recorder did not say (an older host).
	State session.AttentionState `json:"state,omitempty"`
}

// eventState is what an activity event says an entry records: state, for an
// attention entry and one of the three states; nothing otherwise.
func eventState(e session.ActivityEntry, state session.AttentionState) session.AttentionState {
	if e.Type != session.ActivityAttention {
		return ""
	}
	switch state {
	case session.AttentionNeedsInput, session.AttentionWorking, session.AttentionDone:
		return state
	}
	return ""
}
```

`activity` marshals the state with the entry, and its comment no longer names `localActivity`, which is gone, or says the clients get the entry alone:

```go
// activity queues an activity entry of a session for every client, with the
// attention state an attention entry records (eventState), then hands it to
// every sink with state ("" when it is not known). It is the OnActivity hook
// of every session, server and hosted alike, so it runs on the goroutines
// that record, concurrently and out of order (each entry says when it
// happened), and it never waits: a client whose queue is past
// activityQueueLimit misses the entry and keeps its stream.
func (h *eventHub) activity(sessionID string, e session.ActivityEntry, state session.AttentionState) {
	b, err := json.Marshal(activityEvent{SessionID: sessionID, ActivityEntry: e, State: eventState(e, state)})
```

The rest of `activity` stays as it is.

In `internal/api/webhooks.go`, the comment of `eventTypeOf` says where the state comes from now (the function body does not change):

```go
// eventTypeOf is the event type the Events page gives an entry, as
// eventTypeOf in web/app/utils/events.ts does, or "" for an entry that is
// none: an attention entry is the attention state it records, a status entry
// of a process that exited on its own with a non-zero code is exit_nonzero,
// and the six event types are themselves. For an attention entry state is
// the state the entry records, handed on with it (OnActivity for a server
// session, the host's activity message for a hosted one); it is "" only for
// an older host, and then the entry names its state itself when the report
// had no message. (The browser reads the same state from the event's state
// field, and consults the session only for an entry that carries none.)
func eventTypeOf(e session.ActivityEntry, state session.AttentionState) string {
```

In `internal/hostagent/agent.go`, `onLocalActivity` takes the state it is handed and reads `Info` no more:

```go
// onLocalActivity is the local session's OnActivity hook: it queues the entry,
// with the attention state an attention entry records, for the server, whose
// admin stream shows every entry of every session. The session calls it on the
// goroutine that recorded the entry, so it never waits for the connection.
// Entries recorded while the connection is down are lost to the stream; the
// session log has them.
func (a *agent) onLocalActivity(_ string, e session.ActivityEntry, state session.AttentionState) {
	a.activity.push(e, state)
	if e.Type == session.ActivityStatus {
		a.statusQueued.Store(true)
	}
}
```

Each remaining `OnActivity` literal gets the third parameter:

- `internal/hostagent/activity_test.go:153-159`: the wrapper takes the state and passes it on, so it has the hook's type:

```go
	hook := a.onLocalActivity
	if delayStatus > 0 {
		hook = func(id string, e session.ActivityEntry, state session.AttentionState) {
			if e.Type == session.ActivityStatus {
				time.Sleep(delayStatus)
			}
			a.onLocalActivity(id, e, state)
		}
	}
```

- In `internal/session/local_test.go`, every function literal passed as `OnActivity` (`func(_ string, e ActivityEntry)`, `func(string, ActivityEntry)`, `func(id string, e ActivityEntry)`), and `(*attentionHook).hook`, gain a third parameter, `_ AttentionState`.
- In `internal/crew/run_test.go`, `fakeLauncher.activity` passes the state through:

```go
// activity is the sessions' OnActivity: the entry goes to the engine with the
// state it records, as the server's hook hands it on.
func (f *fakeLauncher) activity(sessionID string, e session.ActivityEntry, state session.AttentionState) {
	if f.beforeActivity != nil {
		f.beforeActivity(sessionID, e)
	}
	f.engine.OnActivity(sessionID, e, state)
}
```

Run: `go build ./... && go vet ./...`, then `go test -race -count=1 ./internal/session/ ./internal/api/ ./internal/hostagent/ ./internal/crew/`
Expected: PASS.

- [ ] **Step 7: Use the state on the Events page**

In `web/app/utils/protocol.ts`, `ActivityEntry` gains:

```ts
  /** GET /api/events only: for an attention entry, the state it records. Absent from a session's own replay and from an older host's entries. */
  state?: 'needs_input' | 'working' | 'done'
```

In `web/app/utils/events.ts`:

```ts
export function eventTypeOf(entry: ActivityEntry, session?: SessionInfo): EventType | null {
  switch (entry.type) {
    case 'attention': {
      // The event stream says which state the entry records.
      if (isState(entry.state)) return entry.state
      if (records(session, entry)) return session!.attention!.state as StateEvent
      if (isState(entry.message)) return entry.message
      const state = session?.attention?.state
      return isState(state) ? state : null
    }
```

```ts
export function attentionSettled(entry: ActivityEntry, session?: SessionInfo): boolean {
  return entry.type !== 'attention' || isState(entry.state) || records(session, entry) || isState(entry.message)
}
```

Update the comments above both functions to say that an entry from the stream carries its state, and that the session is consulted only for one that does not (a viewer's replay, an older host). Add to `web/app/utils/events.test.ts`:

```ts
describe('an entry that carries its state', () => {
  it('is that state, whatever the session shows, and waits for nothing', () => {
    const e = entry('attention', { message: 'Allow Bash?', state: 'needs_input' })
    expect(eventTypeOf(e, session('working', 'compiling'))).toBe('needs_input')
    expect(eventTypeOf(e)).toBe('needs_input')
    expect(attentionSettled(e, session(''))).toBe(true)
  })
})
```

Run: `npm --prefix web test -- events && npm --prefix web run typecheck`
Expected: PASS.

- [ ] **Step 8: Write the failing notify tests**

Add to `internal/notify/notify_test.go` (imports `"sync/atomic"` and `"time"`):

```go
// An attention word the server refuses with 429 (the session's bucket is
// empty) is tried again after a short wait, a few times, within the 5 s Send
// has. An event is not: a hook must not hold up its agent for one.
func TestSendRetriesARateLimitedAttentionWord(t *testing.T) {
	old := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryDelays = old })
	var calls atomic.Int32
	limited := func(first int32) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) <= first {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	srv := limited(2)
	for _, req := range []Request{{State: "needs_input"}, {Event: "done"}} {
		calls.Store(0)
		if err := Send(t.Context(), srv.URL+"/api/sessions/s/attention", "tok", req); err != nil || calls.Load() != 3 {
			t.Fatalf("%+v: %v after %d calls", req, err, calls.Load())
		}
	}
	calls.Store(0)
	if err := Send(t.Context(), srv.URL+"/api/sessions/s/attention", "tok", Request{Event: "tool_use", Tool: "Bash"}); err == nil || !strings.Contains(err.Error(), "429") || calls.Load() != 1 {
		t.Fatalf("event: %v after %d calls", err, calls.Load())
	}
	// A server that never lets up: the word is given up after the last wait.
	always := limited(1 << 30)
	calls.Store(0)
	if err := Send(t.Context(), always.URL+"/api/sessions/s/attention", "tok", Request{State: "working"}); err == nil || calls.Load() != int32(len(retryDelays)+1) {
		t.Fatalf("always limited: %v after %d calls", err, calls.Load())
	}
}

// The waits fit the budget, and a deadline that comes first ends them.
func TestSendRetriesWithinItsBudget(t *testing.T) {
	var total time.Duration
	for _, d := range retryDelays {
		total += d
	}
	if total >= sendTimeout {
		t.Fatalf("the waits add up to %v, not within %v", total, sendTimeout)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Send(ctx, srv.URL+"/a/attention", "tok", Request{State: "working"}); err == nil || !strings.Contains(err.Error(), "429") || time.Since(start) > time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}
```

Run: `go test ./internal/notify/ -count=1 -run Retr`
Expected: build failure, because `retryDelays` and `sendTimeout` are undefined.

- [ ] **Step 9: Implement the bounded retry**

In `internal/notify/notify.go`:

```go
// sendTimeout bounds a Send, its retries included: a hook waits at most this.
const sendTimeout = 5 * time.Second

// retryDelays are the waits between the attempts of an attention word the
// server answers 429: the session's bucket refills at 20 tokens a second, so a
// short wait usually finds one. They add up to well under sendTimeout, which
// still ends them.
var retryDelays = []time.Duration{100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2 * time.Second}

// attentionWord reports whether r reports an attention state, by the
// attention route or as an attention word of the events route: what a 429
// retries.
func (r Request) attentionWord() bool {
	switch r.Event {
	case "", "needs_input", "working", "done", "clear":
		return true
	}
	return false
}

// Send posts the request to url with the agent token. A request with Event
// set goes to the session's events route (url with /attention replaced by
// /events) as an event; any other goes to url as an attention update.
// Redirects are refused so a token never follows a rewrite. An attention word
// answered 429 is tried again after each of retryDelays, while the 5 s budget
// lasts; anything else is tried once.
func Send(ctx context.Context, url, token string, req Request) error {
	var payload any = req
	if req.Event != "" {
		url = eventsURL(url)
		payload = eventBody{Type: req.Event, Message: req.Message, URL: req.URL, To: req.To, Tool: req.Tool, Kind: req.Kind, Options: req.Options}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		status, msg, err := post(ctx, url, token, body)
		if err != nil {
			return err
		}
		if status < 300 {
			return nil
		}
		failed := fmt.Errorf("notify: server returned %d: %s", status, msg)
		if status != http.StatusTooManyRequests || !req.attentionWord() || attempt >= len(retryDelays) {
			return failed
		}
		select {
		case <-ctx.Done():
			return failed
		case <-time.After(retryDelays[attempt]):
		}
	}
}

// post makes one attempt: the status and, for a failure, the start of the
// reply.
func post(ctx context.Context, url, token string, body []byte) (int, string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return resp.StatusCode, strings.TrimSpace(string(msg)), nil
}
```

Run: `go test ./internal/notify/ ./internal/cli/ -count=1 -race`
Expected: PASS.

- [ ] **Step 10: Write the failing hostless-metering tests**

In `internal/signal/signal_test.go`, replace `TestOnlyForwardsToAConnectedHostAreLimited` with:

```go
// What comes through the routes (forward true) spends the session's bucket
// whether a host is connected or not: a session whose host is away cannot be
// flooded with reports while it waits for the host. The host's own reports
// come the other way and never spend one.
func TestRouteReportsAreLimitedWithOrWithoutAHost(t *testing.T) {
	hub := NewHub(session.NewRegistry(4), nil)
	hs, conn := register(t, hub)
	for i := 0; i < 5*session.EventBurst; i++ {
		if err := hs.SetAttentionFull(session.AttentionWorking, fmt.Sprint("n", i), "bell", "", nil, false); err != nil {
			t.Fatalf("the host's own report %d was limited: %v", i, err)
		}
	}
	if len(conn.Send) != 0 {
		t.Fatalf("%d messages went back to the host that sent them", len(conn.Send))
	}
	hs.HostDisconnected(conn)
	accepted := 0
	for i := 0; i < 5*session.EventBurst; i++ {
		err := hs.SetAttentionFull(session.AttentionNeedsInput, fmt.Sprint("n", i), session.SourceAPI, "", nil, true)
		if errors.Is(err, ErrRateLimited) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		accepted++
	}
	if accepted < session.EventBurst || accepted >= 5*session.EventBurst {
		t.Fatalf("accepted %d words for a session without a host, want about %d", accepted, session.EventBurst)
	}
	if got := hs.Info().Attention.Message; got != fmt.Sprint("n", accepted-1) {
		t.Fatalf("attention %q: a refused word was applied", got)
	}
	// An event for a session without a host is refused before it spends anything.
	if err := hs.ForwardActivity(session.ActivityEntry{Type: session.ActivityProgress}); !errors.Is(err, ErrHostGone) {
		t.Fatalf("event without a host: %v", err)
	}
}
```

In `internal/api/ws_e2e_test.go`, extend `TestEventsRouteTellsWhenTheHostIsAway` after its last check:

```go
	// And they are metered as for a connected host: past the burst, 429.
	limited := false
	for i := 0; i < 5*session.EventBurst && !limited; i++ {
		resp, out := e.do("POST", "/api/sessions/"+host.sessionID+"/attention", "hosted-agent-token", map[string]any{"state": "working", "message": fmt.Sprint("n", i)})
		switch {
		case resp.StatusCode == http.StatusTooManyRequests && errorCode(out) == "rate_limited":
			limited = true
		case resp.StatusCode != http.StatusOK:
			t.Fatalf("word %d: %d %v", i, resp.StatusCode, out)
		}
	}
	if !limited {
		t.Fatal("attention words for a session without a host were never limited")
	}
```

Run: `go test ./internal/signal/ ./internal/api/ -count=1 -run 'RouteReportsAreLimited|HostIsAway'`
Expected: FAIL. Every word is accepted while no host is connected.

- [ ] **Step 11: Meter route reports with or without a host**

In `internal/signal/hosted.go`, `SetAttentionFull`:

```go
// SetAttentionFull records an attention change. When forward is true (API
// origin), the report spends a token of the session's bucket, the one
// ForwardActivity spends from, whether a host is connected or not: it returns
// ErrRateLimited, having changed nothing, when there is none. A connected host
// is told, so its viewers see the change too; without one the change is held
// on the server. A change from the host (forward false) never spends one.
func (h *HostedSession) SetAttentionFull(state session.AttentionState, message, source, kind string, options []session.Option, forward bool) error {
	// (the cleaning of state, message, kind and options stays as it is)
	h.mu.Lock()
	conn := h.conn
	if forward && !h.events.Take(time.Now()) {
		h.mu.Unlock()
		return ErrRateLimited
	}
```

In `internal/api/attention.go`, the hosted branch of `reportAttention`. The `host_disconnected` answer was unreachable, since the only error is the empty bucket:

```go
	case *signal.HostedSession:
		// The only refusal is an empty bucket, host or no host.
		if err := drv.SetAttentionFull(state, message, source, kind, options, true); err != nil {
			writeSessionLimited(w)
			return false
		}
```

Run: `go test -race -count=1 ./internal/signal/ ./internal/api/`
Expected: PASS.

- [ ] **Step 12: The host logs its no-launch-route line at warn**

In `internal/hostagent/agent.go`, `injectHooks`, `opts.Log.Info("hosting without Conductor's hooks at launch: …")` becomes `opts.Log.Warn(…)` with the same message and attributes. The doc comment's "says so at info level" becomes "says so at warn level, so the line shows with the local terminal attached (`conductor host` logs at warn then)". In `internal/hostagent/hooks_test.go`, the two `strings.Contains(out, "level=INFO")` checks become `"level=WARN"`.

Run: `go test ./internal/hostagent/ -count=1 -run InjectHooks`
Expected: FAIL before the change, PASS after.

- [ ] **Step 13: `CleanName` reuses `oneLine`; split the tests; the bell test waits on a frame**

In `internal/session/attention.go`:

```go
// CleanName normalises a display name from a client: oneLine at
// proto.MaxNameLen runes (control characters dropped, surrounding space
// trimmed), and "guest" when nothing is left.
func CleanName(s string) string {
	if out := oneLine(s, proto.MaxNameLen); out != "" {
		return out
	}
	return "guest"
}
```

Pin it with a test in `internal/session/attention_test.go` (it passes before and after, since the refactor keeps the behaviour; add `strings` and `internal/proto` to that file's imports if they are missing):

```go
func TestCleanNameIsOneLineWithAGuestDefault(t *testing.T) {
	if proto.MaxNameLen != 40 {
		t.Fatalf("MaxNameLen is %d; the cases below are written for 40", proto.MaxNameLen)
	}
	for _, tc := range []struct{ in, want string }{
		{"", "guest"},
		{"  ", "guest"},
		{"\x07\x1b", "guest"},
		{"Priya", "Priya"},
		{" a\tb\nc ", "abc"},
		{"x\x7fy", "xy"},
		{strings.Repeat("é", 45), strings.Repeat("é", 40)},          // 40 runes, not 40 bytes
		{" " + strings.Repeat("é", 45), strings.Repeat("é", 40)},    // trimmed before the cut
		{strings.Repeat("a", 39) + "  b", strings.Repeat("a", 39)}, // the cut ends in a space, trimmed after
	} {
		if got := CleanName(tc.in); got != tc.want {
			t.Errorf("CleanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

Create `internal/session/record_test.go` with `package session` and move these from `local_test.go` unchanged (the signature edits of Step 6 included): `TestActivityBroadcastAndReplay`, `TestRecordRateLimitsPerSession`, `syncBuffer` and its methods, `TestRecordDropsAreNotStoredBroadcastOrReported`, `drainBucket`, `TestRecordSessionEntriesBypassTheBucket`, `attentionEntries`, `attentionHook` and its methods, `TestAttentionChangesAreRecordedWhateverTheBucketHolds`, `TestAttentionEntryIsStampedAtTheChange`, `TestTrySetAttentionFullSpendsOneTokenOrChangesNothing`, `TestOnActivityRunsAfterTheBroadcastAndOutsideTheLock`, `TestActivityFramesCarryEventFields` and the new `TestOnActivityGetsTheStateTheEntryRecords`. Move each doc comment with its function. Give `record_test.go` the imports the moved code uses, and remove from `local_test.go` the ones it no longer uses. `go vet ./internal/session/` names both.

In `local_test.go`, `TestInputDoesNotClearAPromptItsOwnOutputRaised` waits for the prompt's frame in place of polling `Info()`:

```go
func TestInputDoesNotClearAPromptItsOwnOutputRaised(t *testing.T) {
	fp := newFakeProc()
	sink := newChanSink(false)
	reacted := false // Input, and so react, runs on this goroutine only
	p := &reactingProc{fakeProc: fp}
	p.react = func() {
		if reacted {
			return
		}
		reacted = true
		fp.outW.Write([]byte("\a")) // returns once the pump has read it
		// The prompt the bell raises reaches the viewer as a frame: the
		// session has applied it before Input goes on.
		if waitControl(sink, func(m map[string]any) bool {
			return m["t"] == proto.CtlAttention && m["state"] == string(AttentionNeedsInput)
		}) == nil {
			t.Error("the bell raised no prompt")
		}
	}
	s := NewLocal(Info{ID: "sess", Cwd: t.TempDir(), Cols: 80, Rows: 24}, p, Options{})
	t.Cleanup(fp.exit)
	sub, err := s.Attach("", RoleControl, "", 80, 24, sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Input(sub, []byte("\n")); err != nil {
		t.Fatal(err)
	}
	if att := s.Info().Attention; att.State != AttentionNeedsInput || att.Source != SourceBell {
		t.Fatalf("typing cleared the prompt its own output raised: %+v", att)
	}
	// The next input does answer it.
	if err := s.Input(sub, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if att := s.Info().Attention; att.State != AttentionNone {
		t.Fatalf("the answer left %+v", att)
	}
}
```

Run: `go test ./internal/session/ -count=3 -race`
Expected: PASS. `wc -l internal/session/local_test.go` drops by about 550 lines.

- [ ] **Step 14: Docs**

- `docs/protocol.md`, streaming paragraph: `data: {"sessionId", "at", "type", "by"?, "byName"?, "message"?, "url"?, "to"?, "tool"?, "state"?}`, where `state` is, for an attention entry, the state it records (`needs_input`, `working` or `done`), absent for other entries and for an attention entry from a host that does not send one. Add the same to the `GET /api/events` row.
- `docs/protocol.md`, attention paragraph (around line 205): "on a server session and on a hosted session whose host is connected" becomes "on a server session and on a hosted session, its host connected or not"; "while no host is connected the report is applied on the server without a token" becomes "while no host is connected the report is applied on the server, and spends its token all the same". In the events paragraph (around line 309), "Only a report that goes to a connected host spends one" becomes "Every report through the routes spends one, its host connected or not".
- `docs/protocol.md`, webhooks paragraph (around line 343): "An attention entry is the attention state it records, the state its session was in when it recorded the entry: a server session's is read then, a hosted session's comes with the entry (`state` of the host's `activity` message)." becomes "An attention entry is the attention state it records, which comes with the entry: a server session hands it on as it records the entry (the state set with the entry's stamp), and a hosted session's host sends it (`state` of the host's `activity` message)."
- `docs/protocol.md` and `README.md`, `conductor notify`: an attention report (the state flags, `--event` with an attention word, or a hook mapped to one) that the server answers `429` is tried again after 0.1, 0.25, 0.5, 1 and 2 s, within the 5 s the command takes at most. An event is tried once.

- [ ] **Step 15: Run the narrow gate and commit**

Run: `make lint && go test -race -count=1 ./internal/session/ ./internal/signal/ ./internal/api/ ./internal/hostagent/ ./internal/crew/ ./internal/notify/ ./internal/cli/ && npm --prefix web test && npm --prefix web run typecheck`
Expected: PASS.

```bash
git add internal/session internal/signal internal/api internal/hostagent internal/crew/run_test.go internal/notify internal/cli web/app/utils/protocol.ts web/app/utils/events.ts web/app/utils/events.test.ts docs/protocol.md README.md
git commit -m "sessions: state travels with the entry, one error frame for a departing host, hostless metering, notify retries 429"
```

---

### Task 6: Adapters and the CLI

Triage items #38 to #48. The behaviour fixes come first, each with its test, then the moves. The moves change no behaviour, and the existing tests pin them.

**Files:**
- Create: `internal/agents/home.go` (moved from `merge.go`), `internal/notify/mappers.go` (moved from `notify.go`), `internal/hostagent/hooks.go` (moved from `agent.go`)
- Modify: `internal/agents/assets.go` (`ModeError`, `chmod` seam, `.version` record, `RecordedVersion`), `internal/agents/merge.go` (`staleCommand`, `withYAMLListItem`, `mergeHooksStep`, `run` tags its by-hand steps), `internal/agents/adapter.go` (`SnippetNeeded`, `stepError`), `internal/agents/claude.go`, `internal/agents/cursor.go`, `internal/agents/owner_unix.go`, `internal/agents/owner_other.go`
- Modify tests: `internal/agents/assets_test.go`, `merge_test.go`, `owner_test.go`, `adapter_test.go`
- Modify: `internal/cli/serve.go`, `internal/cli/hooks.go`, `internal/cli/notify.go`, `internal/cli/cli_test.go`, `internal/cli/notify_test.go`
- Modify: `internal/hostagent/agent.go`, `internal/api/integrations.go`, `internal/api/api_test.go`
- Modify: `internal/catalog/catalog_test.go` (built-in icons are bundled)
- Create: `web/app/utils/agentIcons.ts`, `web/app/utils/agentIcons.test.ts`, `web/app/utils/integrations.ts`, `web/app/utils/integrations.test.ts`
- Modify: `web/nuxt.config.ts`, `web/app/components/IntegrationCard.vue`, `CodeBlock.vue`, `LaunchSessionModal.vue`, `ShareLinksModal.vue`, `FileBrowser.vue`, `CrewMembersTable.vue`, `web/app/pages/agents.vue`, `web/app/pages/crews/[[id]].vue`, `web/app/composables/useSessions.ts` (`Integration.installable`)
- Modify: `README.md` (icons, hooks), `docs/protocol.md` (`GET /api/integrations` row, the `permissionOptions()` path), `docs/features.md` (the `permissionOptions()` path)

**Interfaces:**
- Consumes (Task 1): `agents.HostHooksDir()` three results; `serveHooksDir`. (Task 5): `injectHooks` logs its no-launch-route line at warn.
- Produces:
  ```go
  package agents
  type ModeError struct{ Errs []error }            // WriteAssets wrote everything; some modes are wrong
  var chmod = os.Chmod                              // test seam
  func RecordedVersion(hooksDir string) (string, error)
  func SnippetNeeded(err error) bool                // a step left to the user other than the skill's
  type stepError struct{ rel string; err error }    // adapter.go: a by-hand step with its file; run makes it, SnippetNeeded reads it
  func mergeHooksStep(assets map[string]string, hooksDir, asset, rel, marker string) step
  func writable(dir string) bool                    // owner_unix.go / owner_other.go

  package api
  // integration gains: Installable bool `json:"installable"`   (the adapter has a file route)

  package cli
  var hookPayloads []hookPayload                    // name, usage, mapHook; payloadFlags derives from it
  ```
  ```ts
  // web/app/utils/agentIcons.ts
  export const AGENT_ICONS: readonly string[]
  export const AGENT_ICON_FALLBACK = 'i-lucide-bot'
  export function agentIcon(icon?: string): string
  // web/app/utils/integrations.ts
  export function noInstallText(it: Pick<Integration, 'installable' | 'launchInjection'>, homeKnown: boolean): string
  // CodeBlock.vue props gain: wrap?: boolean; disabled?: boolean; copyTitle?: string; copyDescription?: string
  ```

- [ ] **Step 1: Write the failing adapter tests**

Append to `internal/agents/assets_test.go` (import `internal/version`):

```go
// A mode WriteAssets cannot set (a file that belongs to another user, say) is
// no reason to stop: every asset is still written, and the error, a
// ModeError, says which modes are wrong. A real failure is no ModeError.
func TestWriteAssetsReportsModesItCannotSetAndGoesOn(t *testing.T) {
	dir := t.TempDir()
	if err := writeAssets(t, dir, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(dir, "claude.json")
	if err := os.Chmod(asset, 0o644); err != nil {
		t.Fatal(err)
	}
	later := filepath.Join(dir, "cursor-hooks.json") // written after claude.json
	if err := os.Remove(later); err != nil {
		t.Fatal(err)
	}
	chmod = func(p string, m fs.FileMode) error {
		if p == asset || p == dir {
			return &fs.PathError{Op: "chmod", Path: p, Err: fs.ErrPermission}
		}
		return os.Chmod(p, m)
	}
	t.Cleanup(func() { chmod = os.Chmod })
	err := writeAssets(t, dir, "/opt/conductor")
	var me *ModeError
	if !errors.As(err, &me) || len(me.Errs) != 2 || !strings.Contains(err.Error(), asset) {
		t.Fatalf("WriteAssets: %v", err)
	}
	if _, err := os.Stat(later); err != nil {
		t.Fatalf("an asset after the one whose mode failed was not written: %v", err)
	}
	linked := t.TempDir()
	if err := os.Symlink(filepath.Join(t.TempDir(), "x.json"), filepath.Join(linked, "claude.json")); err != nil {
		t.Fatal(err)
	}
	if err := writeAssets(t, linked, "/opt/conductor"); err == nil || errors.As(err, &me) {
		t.Fatalf("a link is a real failure: %v", err)
	}
}

// The hooks dir records the version of the conductor that wrote it, beside
// the binary, for conductor hooks to compare with its own.
func TestWriteAssetsRecordsTheVersion(t *testing.T) {
	dir := t.TempDir()
	if err := writeAssets(t, dir, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	if v, err := RecordedVersion(dir); err != nil || v != version.Version {
		t.Fatalf("RecordedVersion: %q %v", v, err)
	}
	if _, err := RecordedVersion(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("no record: %v", err)
	}
}
```

In `internal/agents/merge_test.go`, add these cases to `TestStaleCommand`'s map:

```go
		"/opt/old/bin/conductor notify --x-hook":    true,
		"/usr/bin/python3 notify --x-hook":          false, // not conductor: not Conductor's to rewrite
		"/opt/old/conductor-dev notify --x-hook":    false,
		"'/opt/my apps/notconductor' notify --x-hook": false,
```

To `TestWithYAMLListItem`, add to `cases`:

```go
		// A byte order mark stays where it is and hides no key.
		{"﻿extensions:\n  - a.ts\n", "﻿extensions:\n  - a.ts\n  # conductor\n  - " + item + "\n"},
		{"﻿", "﻿extensions:\n  # conductor\n  - " + item + "\n"},
```

and to the by-hand list:

```go
		// A root written in flow style.
		"{extensions: [a.ts]}\n", "# mine\n{model: y}\n", "[a.ts]\n",
```

Append to `internal/agents/adapter_test.go`:

```go
// A skill file of the user's own is left to them with its own instruction;
// the settings snippet would be beside the point. Any other step left to the
// user wants the snippet.
func TestSnippetNeededOnlyForStepsOtherThanTheSkill(t *testing.T) {
	useBin(t, "/opt/conductor")
	home := t.TempDir()
	skill := filepath.Join(home, filepath.FromSlash(claudeSkill))
	if err := os.MkdirAll(filepath.Dir(skill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# my own skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := Get("claude")
	_, err := a.Install(home, t.TempDir())
	if !errors.Is(err, ErrByHand) || SnippetNeeded(err) {
		t.Fatalf("the skill alone: %v (snippet %v)", err, SnippetNeeded(err))
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere.json"), settings); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Install(home, t.TempDir()); !SnippetNeeded(err) {
		t.Fatalf("the settings left by hand: %v", err)
	}
	if SnippetNeeded(nil) || SnippetNeeded(errors.New("disk full")) {
		t.Fatal("no step left to the user wants no snippet")
	}
	d, _ := Get("dsh")
	if _, err := d.Install(home, t.TempDir()); !SnippetNeeded(err) {
		t.Fatalf("dsh: %v", err)
	}
}
```

Append to `internal/agents/owner_test.go`:

```go
// The process's own home passes when the process may write it, whoever owns
// it (a container whose HOME belongs to another uid). Another user's home
// does not, and neither does an own home the process cannot write.
func TestOwnedByAcceptsTheProcesssOwnWritableHome(t *testing.T) {
	home := t.TempDir()
	fi, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fileOwner(fi); !ok {
		t.Skip("this platform does not report who owns a file")
	}
	t.Setenv("HOME", home)
	someoneElse := os.Geteuid() + 1 // the home is ours; we write as another user
	if err := ownedBy(home, fi, someoneElse); err != nil {
		t.Fatalf("an own home it can write: %v", err)
	}
	other := t.TempDir()
	ofi, _ := os.Stat(other)
	if err := ownedBy(other, ofi, someoneElse); err == nil {
		t.Fatal("a home that is not HOME was accepted")
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(home, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(home, 0o700) })
		if err := ownedBy(home, fi, someoneElse); err == nil {
			t.Fatal("an own home it cannot write was accepted")
		}
	}
}

// Root never gets the own-home exception: under sudo, HOME may still name the
// invoking user's home, which root can always write, and what root wrote
// there would belong to root.
func TestOwnHomeExceptionNeverCoversRoot(t *testing.T) {
	home := t.TempDir()
	fi, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := fileOwner(fi); !ok || owner == 0 {
		t.Skip("needs a home that is not root's")
	}
	t.Setenv("HOME", home)
	if err := ownedBy(home, fi, 0); err == nil || !strings.Contains(err.Error(), "sudo -u") {
		t.Fatalf("root in a user's home: %v", err)
	}
}
```

Append to `internal/catalog/catalog_test.go`:

```go
// The workbench fetches no icon at runtime: every built-in agent's icon is in
// the list it bundles (web/app/utils/agentIcons.ts).
func TestBuiltInIconsAreInTheWorkbenchBundle(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "app", "utils", "agentIcons.ts"))
	if err != nil {
		t.Fatalf("the workbench's icon list: %v", err)
	}
	for _, a := range Default().List() {
		if !strings.Contains(string(b), "'"+a.Icon+"'") {
			t.Errorf("%s: icon %s is not in agentIcons.ts", a.ID, a.Icon)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/agents/ ./internal/catalog/ -count=1`
Expected: build failure, because `chmod`, `ModeError`, `RecordedVersion` and `SnippetNeeded` are undefined. Once they are stubbed, the stale-command, YAML, own-home and icon tests fail on behaviour.

- [ ] **Step 3: Implement the adapter fixes**

In `internal/agents/assets.go` (import `internal/version`):

```go
// chmod is os.Chmod: a test replaces it to make a mode fail.
var chmod = os.Chmod

// versionRecord is the file in the hooks dir that names the version of the
// conductor that wrote its assets, for conductor hooks to compare with its own.
const versionRecord = ".version"

// ModeError is what WriteAssets returns when it wrote every asset but could
// not set the mode of some of them, or of the hooks dir: a file that belongs
// to another user, say. The assets are in place and name the binary;
// conductor serve warns and starts. Errs says which.
type ModeError struct{ Errs []error }

func (e *ModeError) Error() string {
	msgs := make([]string, len(e.Errs))
	for i, err := range e.Errs {
		msgs[i] = err.Error()
	}
	return "the hook assets are written, but their modes could not all be set: " + strings.Join(msgs, "; ")
}

func (e *ModeError) Unwrap() []error { return e.Errs }

// RecordedVersion returns the version of the conductor that wrote the assets
// in hooksDir; fs.ErrNotExist when there is no record, as an older conductor
// wrote none.
func RecordedVersion(hooksDir string) (string, error) {
	p := filepath.Join(hooksDir, versionRecord)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 256 {
		return "", fmt.Errorf("%s is not a version record: not a regular file of at most 256 bytes", p)
	}
	b, err := os.ReadFile(p)
	return strings.TrimSpace(string(b)), err
}
```

`WriteAssets` collects modes it cannot set and goes on. The doc comment adds: "A mode it cannot set stops nothing: the assets are all written, and the error is a *ModeError. It also records version.Version as .version."

```go
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return err
	}
	var modes []error
	if err := chmod(hooksDir, 0o700); err != nil {
		modes = append(modes, err)
	}
	put := func(p string, data []byte) error {
		_, err := replaceFile(p, p, data, 0o600)
		var me *modeError
		if errors.As(err, &me) {
			modes = append(modes, me.err)
			return nil
		}
		return err
	}
	sets := []map[string]string{skillAssets}
	for _, a := range registry {
		sets = append(sets, a.Assets)
	}
	for _, assets := range sets {
		for _, rel := range slices.Sorted(maps.Keys(assets)) {
			content, err := render(rel, assets[rel], bin)
			if err != nil {
				return err
			}
			if err := put(filepath.Join(hooksDir, filepath.FromSlash(rel)), []byte(content)); err != nil {
				return err
			}
		}
	}
	if err := put(filepath.Join(hooksDir, binRecord), []byte(bin)); err != nil {
		return err
	}
	if err := put(filepath.Join(hooksDir, versionRecord), []byte(version.Version)); err != nil {
		return err
	}
	remember(bin)
	if len(modes) > 0 {
		return &ModeError{Errs: modes}
	}
	return nil
```

In `replaceFile` (it moves to `home.go` in Step 7), the chmod of a file whose content is right goes through the seam and is marked:

```go
// modeError is a mode replaceFile could not set on a file whose content was
// right already.
type modeError struct{ err error }

func (e *modeError) Error() string { return e.err.Error() }
func (e *modeError) Unwrap() error { return e.err }
```

```go
	if fi != nil && holds(p, data) {
		if mode != 0 && fi.Mode().Perm() != mode {
			if err := chmod(p, mode); err != nil {
				return false, &modeError{err}
			}
		}
		return false, nil
	}
```

`staleCommand` requires the program to be named `conductor`:

```go
// staleCommand reports whether s is a hook command Conductor wrote for
// marker, `<absolute path to a program named conductor> notify --x-hook` with
// the path quoted the way Conductor quotes it, naming another binary than
// ours does. A bare `conductor notify --x-hook` is the user's own, and so is
// any other program, or anything longer or shaped otherwise.
func staleCommand(s, marker, ours string) bool {
	if s == ours {
		return false
	}
	quoted, ok := strings.CutSuffix(s, " "+marker)
	if !ok {
		return false
	}
	bin, ok := shellUnquote(quoted)
	return ok && filepath.IsAbs(bin) && filepath.Base(bin) == "conductor"
}
```

`withYAMLListItem` keeps a byte order mark and refuses a flow-style root:

```go
func withYAMLListItem(content, key, marker, item, match string) (string, error) {
	if strings.Contains(content, match) {
		return content, nil
	}
	// A byte order mark stays where it is; the lines are read without it.
	bom := ""
	if rest, ok := strings.CutPrefix(content, "﻿"); ok {
		bom, content = "﻿", rest
	}
	out, err := yamlWithListItem(content, key, marker, item)
	if err != nil {
		return "", err
	}
	return bom + out, nil
}
```

`yamlWithListItem` is the previous body without its first `if`. In its first loop, the line that sets `started` for content becomes:

```go
		if s := strings.TrimSpace(t); s != "" && !strings.HasPrefix(s, "#") {
			if !rootSeen && (s[0] == '{' || s[0] == '[') {
				return "", byHand("the file's root is written in flow style (%c…); add %s to %s by hand", s[0], item, key)
			}
			rootSeen = true
			started = true
		}
```

with `rootSeen := false` declared beside `started`. Update the doc comment of `withYAMLListItem`: "…a file of more than one document, and a root written in flow style ({…} or […]). A byte order mark is kept".

`run` tags each step left to the user with its file, and `SnippetNeeded` reads the tags. `stepError` lives in `adapter.go`, beside `SnippetNeeded`, and stays there: Step 7 moves `run` to `home.go` but not `stepError`. Add to `adapter.go`:

```go
// stepError is what a step left to the user (ErrByHand) says, with the file
// it is about. Its message is the step's.
type stepError struct {
	rel string
	err error
}

func (e *stepError) Error() string { return e.err.Error() }
func (e *stepError) Unwrap() error { return e.err }
```

In `run` (`merge.go`), the by-hand branch becomes:

```go
		if errors.Is(err, ErrByHand) {
			manual = append(manual, &stepError{s.rel, err})
			continue
		}
```

And, in `adapter.go` too:

```go
// SnippetNeeded reports whether what Install left to the user (err) calls for
// the adapter's snippet. It does unless every step left is the Conductor
// skill's, whose message says what to do (conductor skill prints it); the
// snippet is for the agent's hooks.
func SnippetNeeded(err error) bool {
	return errors.Is(err, ErrByHand) && !onlySkillSteps(err)
}

func onlySkillSteps(err error) bool {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			if !onlySkillSteps(e) {
				return false
			}
		}
		return true
	}
	var se *stepError
	return errors.As(err, &se) && (se.rel == claudeSkill || se.rel == codexSkill || se.rel == agentsSkill)
}
```

The own-home rule, in `ownedBy`:

```go
// ownedBy refuses home, which fi describes, unless the user uid owns it, or
// it is the process's own home (HOME) and the process may write it, as in a
// container whose home belongs to another uid. Root never passes the second
// way: under sudo, HOME may still name the invoking user's home, which root
// can always write.
func ownedBy(home string, fi fs.FileInfo, uid int) error {
	owner, ok := fileOwner(fi)
	if !ok || owner == uid {
		return nil
	}
	if uid != 0 && ownHome(home) && writable(home) {
		return nil
	}
	who := strconv.Itoa(owner)
	if u, err := user.LookupId(who); err == nil && u.Username != "" {
		who = u.Username
	}
	return fmt.Errorf("%s belongs to %s, and what Conductor wrote there would not: run it as %s, for example sudo -u %s conductor hooks install …", home, who, who, who)
}

// ownHome reports whether dir is the process's home directory, compared as a
// file.
func ownHome(dir string) bool {
	h, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	a, errA := os.Stat(h)
	b, errB := os.Stat(dir)
	return errA == nil && errB == nil && os.SameFile(a, b)
}
```

`owner_unix.go`:

```go
// writable reports whether this process may create files in dir.
func writable(dir string) bool {
	return syscall.Access(dir, 0x2 /* W_OK */) == nil
}
```

`owner_other.go`:

```go
// writable is false where the system cannot say: the exception it allows is never needed there.
func writable(string) bool { return false }
```

- [ ] **Step 4: Bundle the icons the workbench carries**

Create `web/app/utils/agentIcons.ts`:

```ts
/**
 * The agent icons the workbench carries. It fetches no icon at runtime: the
 * client bundle holds the icons named in the app's sources and in nuxt.config,
 * which takes this list. It has the built-in catalog's icons
 * (internal/catalog/defaults.go; a Go test checks) and the example config's.
 * A catalog icon outside it would show nothing, so it shows
 * AGENT_ICON_FALLBACK instead.
 */
export const AGENT_ICONS: readonly string[] = [
  'i-lucide-sparkles',
  'i-lucide-code-xml',
  'i-lucide-rocket',
  'i-lucide-github',
  'i-lucide-mouse-pointer-2',
  'i-lucide-braces',
  'i-lucide-pi',
  'i-lucide-pi-square',
  'i-lucide-git-commit',
  'i-lucide-feather',
  'i-lucide-zap',
  'i-lucide-cpu',
  'i-lucide-terminal',
  'i-lucide-wrench',
  'i-lucide-bot',
]

/** The icon for an agent without one, or with one the workbench does not carry. */
export const AGENT_ICON_FALLBACK = 'i-lucide-bot'

/** `icon` when the workbench carries it, the generic agent icon otherwise. */
export function agentIcon(icon?: string): string {
  return icon && AGENT_ICONS.includes(icon) ? icon : AGENT_ICON_FALLBACK
}
```

Create `web/app/utils/agentIcons.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { AGENT_ICON_FALLBACK, agentIcon } from './agentIcons'

describe('agentIcon', () => {
  it('keeps an icon the workbench carries', () => {
    expect(agentIcon('i-lucide-sparkles')).toBe('i-lucide-sparkles')
    expect(agentIcon('i-lucide-wrench')).toBe('i-lucide-wrench')
  })
  it('shows the generic agent icon for one it does not carry, or none', () => {
    expect(agentIcon('i-lucide-unicorn')).toBe(AGENT_ICON_FALLBACK)
    expect(agentIcon(undefined)).toBe(AGENT_ICON_FALLBACK)
    expect(agentIcon('')).toBe(AGENT_ICON_FALLBACK)
  })
})
```

In `web/nuxt.config.ts`, take the list from that file, so the two cannot drift. Update the comment to say a catalog icon outside the list shows the generic agent icon:

```ts
import { AGENT_ICONS } from './app/utils/agentIcons'
// (the config above `icons` stays as it is)
      icons: AGENT_ICONS.map((name) => name.replace(/^i-lucide-/, 'lucide:')),
```

Use `agentIcon` wherever an agent's icon is drawn:
- `pages/agents.vue`: `<UIcon :name="agentIcon(a.icon)" …/>`
- `pages/crews/[[id]].vue`: `<UIcon :name="agentIcon(agentOf(m.agentId)!.icon)" …/>`
- `components/CrewMembersTable.vue`: `icon: agentIcon(a.icon)` in `agentItems`, and `:icon="agentIcon(agentOf(m.agentId)?.icon)"`

Run: `npm --prefix web test -- agentIcons && go test ./internal/catalog/ -run BuiltInIcons -count=1`
Expected: PASS.

- [ ] **Step 5: Write the failing CLI and API tests**

Append to `internal/cli/cli_test.go` (import `internal/version`):

```go
// The hooks dir records the version of the conductor that wrote it. conductor
// hooks says so when that is another version than its own, or unknown, and
// goes on.
func TestHooksWarnWhenTheServerIsAnotherVersion(t *testing.T) {
	clearConductorEnv(t)
	data, bin := t.TempDir(), fakeBinary(t)
	serverAssets(t, data, bin)
	code, _, stderr, err := runHooksWith(t, "install", "copilot", "--home", t.TempDir(), "--data-dir", data)
	if code != 0 || err != nil || strings.Contains(stderr, "were written by") {
		t.Fatalf("same version: exit %d %v\n%s", code, err, stderr)
	}
	record := filepath.Join(data, "hooks", ".version")
	if err := os.WriteFile(record, []byte("v0.0.1"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr, err = runHooksWith(t, "install", "copilot", "--home", t.TempDir(), "--data-dir", data)
	if code != 0 || err != nil || !strings.Contains(stderr, "conductor v0.0.1") || !strings.Contains(stderr, "conductor "+version.Version) {
		t.Fatalf("another version: exit %d %v\n%s", code, err, stderr)
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if _, _, stderr, _ = runHooksWith(t, "status", "--data-dir", data); !strings.Contains(stderr, "did not record its version") {
		t.Fatalf("no record:\n%s", stderr)
	}
}

// A skill file of the user's own is the only step left: conductor hooks says
// so and prints no settings snippet.
func TestHooksInstallByHandPrintsOnlyWhatIsNeeded(t *testing.T) {
	clearConductorEnv(t)
	home := t.TempDir()
	skill := filepath.Join(home, ".claude", "skills", "conductor", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr, err := runHooksWith(t, "install", "claude", "--home", home, "--data-dir", t.TempDir())
	if code != 1 || err != nil || !strings.Contains(stdout, "is not Conductor's skill") || strings.Contains(stdout, "notify --claude-hook") {
		t.Fatalf("exit %d %v\nstdout:\n%s\nstderr:\n%s", code, err, stdout, stderr)
	}
}
```

Append to `internal/cli/notify_test.go`:

```go
// Every flag that maps an agent's payload makes the command a hook's, which
// exits 1 and not 2 on a mistake: hookMode reads the table the flags come from.
func TestHookModeKnowsEveryPayloadFlag(t *testing.T) {
	for _, h := range hookPayloads {
		if !hookMode([]string{"--" + h.name}) {
			t.Errorf("--%s is not a payload flag", h.name)
		}
	}
	if !hookMode([]string{"--codex", "{}"}) || hookMode([]string{"--state", "done"}) {
		t.Fatal("--codex or --state misread")
	}
	if len(payloadFlags) != len(hookPayloads)+1 {
		t.Fatalf("payloadFlags %v", payloadFlags)
	}
}
```

In `internal/api/api_test.go`, `TestIntegrationsWithoutAKnownHome` adds:

```go
	if c := list["aider"]; c["installable"] != false || c["launchInjection"] != true {
		t.Fatalf("aider: %v", c)
	}
	if c := list["copilot"]; c["installable"] != true {
		t.Fatalf("copilot: %v", c)
	}
```

and `TestIntegrationsInstallByHandAndFailure` adds a skill-only case:

```go
	// Only the skill left by hand: the reply carries no snippet.
	home2 := t.TempDir()
	e.srv.home = home2
	ownSkill := filepath.Join(home2, ".claude", "skills", "conductor", "SKILL.md")
	os.MkdirAll(filepath.Dir(ownSkill), 0o700)
	os.WriteFile(ownSkill, []byte("# mine\n"), 0o600)
	resp, out = e.do("POST", "/api/integrations/claude/install", adminToken, nil)
	if apiErr, _ := out["error"].(map[string]any); resp.StatusCode != http.StatusBadRequest || apiErr["code"] != "no_file_route" || apiErr["snippet"] != nil {
		t.Fatalf("skill only: %d %v", resp.StatusCode, out)
	}
```

Insert that block after the Codex check and before `// A home that is a file…`. That block reassigns `e.srv.home`, which is fine.

Run: `go test ./internal/cli/ ./internal/api/ -count=1 -run 'AnotherVersion|PrintsOnlyWhatIsNeeded|PayloadFlag|Integrations'`
Expected: build failure for `hookPayloads`; then FAIL on behaviour.

- [ ] **Step 6: Implement the CLI and API side**

In `internal/cli/notify.go`, the table moves to package level:

```go
// hookPayload is an agent whose hooks write their payload to stdin: the flag
// that says whose it is, its usage, and the mapper.
type hookPayload struct {
	name, usage string
	mapHook     func([]byte) (notify.Request, bool)
}

// hookPayloads lists them in the order of the usage.
var hookPayloads = []hookPayload{
	{"claude-hook", "read a Claude Code hook payload from stdin and map it", notify.MapClaudeHook},
	{"codex-hook", "read a Codex hooks.json payload from stdin and map it", notify.MapCodexHook},
	{"copilot-hook", "read a GitHub Copilot CLI hook payload from stdin and map it", notify.MapCopilotHook},
	{"cursor-hook", "read a Cursor CLI hook payload from stdin and map it", notify.MapCursorHook},
	{"agy-hook", "read an Antigravity hook payload from stdin and map it", notify.MapAgyHook},
	{"goose-hook", "read a Goose hook payload from stdin and map it", notify.MapGooseHook},
}

// payloadFlags are the flags that make the command map an agent's payload:
// one per hookPayloads entry, and --codex.
var payloadFlags = func() []string {
	out := make([]string, 0, len(hookPayloads)+1)
	for _, h := range hookPayloads {
		out = append(out, h.name)
	}
	return append(out, "codex")
}()
```

In `runNotify`, the local table becomes:

```go
	type hookFlag struct {
		flag    string
		set     *bool
		mapHook func([]byte) (notify.Request, bool)
	}
	hooks := make([]hookFlag, 0, len(hookPayloads))
	for _, h := range hookPayloads {
		hooks = append(hooks, hookFlag{"--" + h.name, fs.Bool(h.name, false, h.usage), h.mapHook})
	}
```

The rest of `runNotify`, which ranges over `hooks` reading `.flag`, `.set` and `.mapHook`, stays.

In `internal/cli/hooks.go`, `runHooksInstall`'s loop over the adapters prints the snippet only when it is needed. The loop becomes:

```go
	for _, a := range list {
		if a.Install == nil {
			fmt.Fprintf(stdout, "%s: nothing to install: Conductor sets %s up when it launches it\n", a.ID, a.Name)
			if one {
				fmt.Fprintf(stdout, "Where Conductor does not launch it, set it up with:\n\n%s", a.Snippet(hooksDir))
				left = true
			}
			continue
		}
		touched, err := a.Install(dir, hooksDir)
		for _, p := range touched {
			fmt.Fprintf(stdout, "%s: wrote %s\n", a.ID, p)
		}
		switch {
		case errors.Is(err, agents.ErrByHand):
			fmt.Fprintf(stdout, "%s: %v\n", a.ID, err)
			if !one {
				// The snippet is for the agent's hooks: a skill file left by
				// hand says what to do in its own message.
				if agents.SnippetNeeded(err) {
					fmt.Fprintf(stdout, "%s: conductor hooks install %s prints the snippet\n", a.ID, a.ID)
				}
				break
			}
			if agents.SnippetNeeded(err) {
				fmt.Fprintf(stdout, "\n%s", a.Snippet(hooksDir))
			}
			left = true
		case err != nil:
			fmt.Fprintf(stderr, "%s: %v\n", a.ID, err)
			failed = append(failed, a.ID)
		case len(touched) == 0:
			fmt.Fprintf(stdout, "%s: installed already, nothing to change\n", a.ID)
		}
	}
```

Only the `ErrByHand` case changes; the lines before and after the loop (`one`, `left`, `failed` and the exit code) stay as they are.

`adoptHooksDir` warns on version skew after the binary is adopted (import `internal/version`):

```go
	// Hooks another version wrote may not be what this one would write.
	if v, err := agents.RecordedVersion(hooksDir); err != nil || v != version.Version {
		by := "conductor " + v
		if err != nil {
			by = "an older conductor, which did not record its version"
		}
		fmt.Fprintf(stderr, "conductor hooks: warning: the hooks in %s were written by %s, and this is conductor %s; install with the conductor that serves, or restart conductor serve with this one\n", hooksDir, by, version.Version)
	}
	return hooksDir, nil
```

In `internal/cli/serve.go`, a `ModeError` is a warning:

```go
	if err := agents.WriteAssets(agents.HooksDir(cfg.DataDir), exe); err != nil {
		var me *agents.ModeError
		if !errors.As(err, &me) {
			return 1, fmt.Errorf("write the hook assets to %s: %w", agents.HooksDir(cfg.DataDir), err)
		}
		log.Warn("the hook assets are written, but not all their modes are as Conductor sets them (0600 files in a 0700 directory)", "dir", agents.HooksDir(cfg.DataDir), "err", err)
	}
```

In `internal/hostagent/agent.go`, `injectHooks` treats it the same:

```go
	if err == nil {
		err = agents.WriteAssets(dir, bin)
		var me *agents.ModeError
		if errors.As(err, &me) {
			opts.Log.Warn("the hook assets are written, but not all their modes could be set", "dir", dir, "err", err)
			err = nil
		}
	}
```

In `internal/api/integrations.go`, `integration` gains `Installable bool \`json:"installable"\`` (set to `a.Install != nil`), and the by-hand reply carries the snippet only when it is needed:

```go
	case errors.Is(err, agents.ErrByHand):
		s.log.Info("integration left to install by hand", "integration", id, "changed", len(changed))
		e := installError{Code: "no_file_route", Message: err.Error(), Changed: changed}
		if agents.SnippetNeeded(err) {
			e.Snippet = snippet()
		}
		writeInstallError(w, http.StatusBadRequest, e)
```

Run: `go test -race -count=1 ./internal/cli/ ./internal/api/ ./internal/agents/ ./internal/hostagent/`
Expected: PASS.

- [ ] **Step 7: Move code into `home.go`, `mappers.go` and `hooks.go`, and share the merge step**

These are moves with no behaviour change. Move code with its doc comments, then fix the imports of both files with `go build`.

- `internal/agents/home.go`: from `merge.go`, move `homeDir`, `openHome`, `geteuid`, `CheckHome`, `ownedBy`, `ownHome`, `(*homeDir).path`, `resolve`, `read`, `write`, `errLink`, `linkByHand`, `existing`, `holds`, `modeError`, `replaceFile`, `step`, `run`, `install` and `statusOf`. `stepError` stays in `adapter.go` (Step 3). Start the file with:
  ```go
  package agents

  // A user's home as Install writes to it: files only where their path really
  // lies inside home, never through a link, 0600 in 0700 directories, each
  // replaced whole; the home must be the user's (CheckHome). A dry run writes
  // nothing and reports what a write would change, which is how Status tells
  // whether Install would change anything.
  ```
- `merge.go` keeps the format-specific merging: `copyAsset`, `copyAssetDir`, the JSON `object`, the TOML and YAML editors, `staleCommand` and its helpers, `mergeJSONHooks`, `setJSONKey` and `createJSON`. Add the shared step there:
  ```go
  // mergeHooksStep is the step that merges the hooks of the asset into the
  // JSON file rel under home, owning the entries that run marker
  // (mergeJSONHooks).
  func mergeHooksStep(assets map[string]string, hooksDir, asset, rel, marker string) step {
  	return step{rel, func(h *homeDir) (bool, error) {
  		b, err := assetFor(assets, hooksDir, asset)
  		if err != nil {
  			return false, err
  		}
  		return mergeJSONHooks(h, rel, b, marker)
  	}}
  }
  ```
  `claudeSteps` becomes `[]step{mergeHooksStep(claudeAssets, hooksDir, "claude.json", claudeSettings, claudeMarker), skillStep(hooksDir, claudeSkill)}`. `cursorSteps` becomes `[]step{mergeHooksStep(cursorAssets, hooksDir, "cursor-hooks.json", cursorHooks, cursorMarker)}`.
- `internal/notify/mappers.go`: from `notify.go`, move `permissionOptions`, `ClaudeHook`, `MapClaudeHook`, `CodexPayload`, `MapCodex`, `MapCodexHook`, `MapCopilotHook`, `MapCursorHook`, `MapAgyHook`, `MapGooseHook`, `present`, `errorText`, `firstOf` and `truncate`. Start with `// Mappers turn an agent's hook payload into a Request: an attention state, or an event.` `notify.go` keeps `Request`, `eventBody`, `FromEnv`, `eventsURL`, `Send`, `post` and `ReadAllBounded`.
- `internal/hostagent/hooks.go`: from `agent.go`, move `injectHooks` with its doc comment.

Run: `go build ./... && go vet ./... && go test -race -count=1 ./internal/agents/ ./internal/notify/ ./internal/hostagent/ ./internal/cli/`
Expected: PASS, with no test file changed by this step. `wc -l internal/agents/merge.go` is below 650.

- [ ] **Step 8: Reuse the clipboard helper and the code block, and fix the aider card**

Create `web/app/utils/integrations.ts`:

```ts
import type { Integration } from '~/composables/useSessions'

/** Why an integration card has no Install button. An adapter with no file to install into says so first, whatever the server's home. */
export function noInstallText(it: Pick<Integration, 'installable' | 'launchInjection'>, homeKnown: boolean): string {
  if (!it.installable) return it.launchInjection ? 'Nothing to install: this server wires it at launch; anywhere else, paste the snippet.' : 'Nothing to install: paste the snippet.'
  if (!homeKnown) return 'The server has no home directory to install into: paste the snippet.'
  return 'Nothing to install into on this machine: paste the snippet.'
}
```

Create `web/app/utils/integrations.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { noInstallText } from './integrations'

describe('noInstallText', () => {
  it('says there is nothing to install for an adapter without a file, home or not', () => {
    expect(noInstallText({ installable: false, launchInjection: true }, false)).toMatch(/^Nothing to install: this server wires it at launch/)
    expect(noInstallText({ installable: false, launchInjection: false }, true)).toBe('Nothing to install: paste the snippet.')
  })
  it('blames the missing home only for an adapter that has a file', () => {
    expect(noInstallText({ installable: true, launchInjection: false }, false)).toMatch(/no home directory/)
  })
})
```

`Integration` in `useSessions.ts` gains `/** The adapter has a file Conductor can install its hooks into. */ installable: boolean`. `IntegrationCard.vue` replaces its `noInstall` with `computed(() => noInstallText(props.integration, props.homeKnown))`.

`CodeBlock.vue` takes what its users need:

```ts
const props = defineProps<{
  text?: string
  commands?: string[]
  /** What the text is, for the toast after Copy. */
  name?: string
  /** Commands wrap instead of being cut short: a command to copy whole, such as one carrying a token. */
  wrap?: boolean
  /** Shown dimmed, Copy off: the command is not ready. */
  disabled?: boolean
  /** The toast after Copy; the description replaces the copied text, which must not be shown when it is a secret. */
  copyTitle?: string
  copyDescription?: string
}>()
```

Its root gains `:class="disabled && 'opacity-60'"`. A command's span uses `:class="wrap ? 'break-all' : 'truncate'"`. Each Copy button gets `:disabled="disabled"` and calls `copy(c, copyTitle ?? 'Command copied', copyDescription ?? c)` or `copy(text, copyTitle ?? 'Snippet copied', copyDescription ?? name)`.

- `LaunchSessionModal.vue`: delete `copyCommand`. The dark block becomes `<CodeBlock :commands="[command]" wrap :disabled="!localReady" copy-title="Command copied" copy-description="It carries your admin token; keep it private." />`.
- `ShareLinksModal.vue`: `const copyText = useCopy()`. Its `copy(url)` becomes `copyText(url, 'Link copied', 'The token is shown once; keep it private.')`.
- `FileBrowser.vue`: `const copy = useCopy()`. `copyPath` becomes `copy(header.value?.path || target.value?.path || '', 'Path copied')`.

Run: `grep -rn "navigator.clipboard" web/app --include='*.vue'`
Expected: no output (only `useCopy.ts` calls it).

Run: `npm --prefix web test && npm --prefix web run typecheck`
Expected: PASS.

- [ ] **Step 9: Docs and commit**

- `README.md`, catalog icons: "The workbench carries the icons it uses and fetches none at runtime, so a name outside that set shows no icon" becomes "The workbench carries the icons it uses and fetches none at runtime: the built-in agents' icons, `i-lucide-wrench` and `i-lucide-bot` (`web/app/utils/agentIcons.ts`). Any other name shows the generic agent icon."
- `README.md`, `conductor hooks`: say that it warns when the hooks dir was written by another version, that a by-hand step prints the snippet only when the snippet is what is missing, and that a home that is not yours is refused unless it is `HOME` and you may write it (never for root).
- `docs/protocol.md`, `GET /api/integrations` row: each integration also carries `installable` (the adapter has a file to install into). In the install row, `snippet` is only present when what is left by hand is the agent's hooks, not a skill file.
- `docs/protocol.md` (around line 165) and `docs/features.md` (around line 168): `permissionOptions()` moved with the mappers, so "`permissionOptions()` in `internal/notify/notify.go`" becomes "`permissionOptions()` in `internal/notify/mappers.go`" in both.

Run: `make lint && go test -race -count=1 ./... && npm --prefix web test && npm --prefix web run typecheck`

```bash
git add internal/agents internal/notify internal/hostagent internal/cli internal/api internal/catalog/catalog_test.go web README.md docs/protocol.md docs/features.md
git commit -m "adapters: modes warn, version record, conductor-only rewrites, YAML BOM and flow roots, own writable home; home.go, mappers.go, hooks.go; shared copy and icons"
```

---

### Task 7: Webhook address forms

Triage item #50.

**Files:**
- Modify: `internal/config/webhook.go` (`CheckWebhookAddr`, new `embeddedIPv4`), `internal/config/webhook_test.go`
- Modify: `README.md` ("Private addresses")

**Interfaces:**
- Consumes: nothing.
- Produces:
  ```go
  // embeddedIPv4 returns the IPv4 addresses an IPv6 address reaches through a transition mechanism, with its name
  // ("NAT64", "6to4", "Teredo", "SIIT"); via is "" when it embeds none; err for a 64:ff9b:1::/48 address in a shorter-prefix form.
  func embeddedIPv4(a netip.Addr) (via string, v4 []netip.Addr, err error)
  ```
  `CheckWebhookAddr(a netip.Addr) error` keeps its signature. An address that embeds IPv4 is judged by every IPv4 it embeds.

- [ ] **Step 1: Write the failing test cases**

Add to the map in `TestCheckWebhookAddr` (`internal/config/webhook_test.go`):

```go
		// 6to4 (2002::/16): the IPv4 address in bits 16 to 47.
		"2002:a00:1::1":   "2002:a00:1::1 reaches 10.0.0.1 through 6to4, which is a private address",
		"2002:808:808::1": "",
		// Teredo (2001::/32): the server's IPv4 address in bits 32 to 63, the
		// client's inverted in the last 32; either one counts.
		"2001:0:4136:e378:8000:63bf:f5ff:fffe": "reaches 10.0.0.1 through Teredo, which is a private address",
		"2001:0:7f00:1::f7f7:f7f7":             "reaches 127.0.0.1 through Teredo, which is a loopback address",
		"2001:0:4136:e378:8000:63bf:f7f7:f7f7": "",
		// SIIT, IPv4-translated (::ffff:0:a.b.c.d).
		"::ffff:0:a00:1":   "::ffff:0:a00:1 reaches 10.0.0.1 through SIIT, which is a private address",
		"::ffff:0:808:808": "",
		// 64:ff9b:1::/48 in the form a prefix shorter than /96 gives (u
		// octet zero, the last three bytes zero): its IPv4 address cannot be
		// told. Read as a /96 the first would reach 1.0.0.0, a public address.
		"64:ff9b:1:0:a:0:100:0": "a NAT64 prefix shorter than /96",
		"64:ff9b:1:a00:0:100::": "a NAT64 prefix shorter than /96",
```

The existing cases stay as they are: `64:ff9b:1:ffff::808:808` (allowed) and, in `TestWebhookAddressRule`, `64:ff9b:1::c0a8:101` (private) and `64:ff9b:1:abcd::6440:1` (CGNAT) are /96 forms, because their last bytes are not zero.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/config/ -run 'CheckWebhookAddr|WebhookAddressRule' -count=1`
Expected: FAIL. The 6to4, Teredo and SIIT addresses with a private IPv4 inside are accepted, and the shorter-prefix NAT64 forms are accepted as 1.0.0.0 and 10.0.1.0.

- [ ] **Step 3: Implement**

In `internal/config/webhook.go`:

```go
// Ranges of CheckWebhookAddr beside those netip names.
var (
	sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")   // RFC 6598, CGNAT; 100.100.100.200 is a cloud metadata address
	ipv4Compatible     = netip.MustParsePrefix("::/96")           // ::a.b.c.d, deprecated
	nat64WellKnown     = netip.MustParsePrefix("64:ff9b::/96")    // RFC 6052
	nat64LocalUse      = netip.MustParsePrefix("64:ff9b:1::/48")  // RFC 8215
	sixToFour          = netip.MustParsePrefix("2002::/16")       // RFC 3056
	teredo             = netip.MustParsePrefix("2001::/32")       // RFC 4380
	siit               = netip.MustParsePrefix("::ffff:0:0:0/96") // RFC 2765, IPv4-translated
)

// CheckWebhookAddr is the network rule for one address a webhook would
// connect to. Unless the webhook allows private addresses, a loopback,
// link-local, private or unique-local, shared (CGNAT) or unspecified address,
// IPv4 or IPv6, is refused, and so is an IPv4-compatible IPv6 address, so that
// a webhook reaches no service of the server's machine or its network that is
// not published. An IPv4 address written as IPv6 counts as the IPv4 one. An
// IPv6 address that carries IPv4 addresses for a transition mechanism (NAT64,
// 6to4, Teredo, SIIT) counts as each one it carries. An address of
// 64:ff9b:1::/48 is judged as a /96 NAT64 prefix; one in the form that a
// shorter prefix gives is refused, since the IPv4 address it reaches cannot be
// told. The server applies it to every address of a webhook's host when it
// validates its config, and to every address it is about to connect to,
// looked up again for each connection, so a name that resolves elsewhere by
// then (DNS rebinding) reaches no such address either.
func CheckWebhookAddr(a netip.Addr) error {
	u := a.Unmap().WithZone("")
	if u.Is6() {
		via, v4s, err := embeddedIPv4(u)
		if err != nil {
			return fmt.Errorf("%s %w; set allowPrivate to send to it", a, err)
		}
		for _, v4 := range v4s {
			if what := addrRange(v4); what != "" {
				return fmt.Errorf("%s reaches %s through %s, which is %s; set allowPrivate to send to it", a, v4, via, what)
			}
		}
		if via != "" {
			return nil
		}
	}
	if what := addrRange(u); what != "" {
		return fmt.Errorf("%s is %s; set allowPrivate to send to it", a, what)
	}
	return nil
}

// errShorterNAT64 is an address of 64:ff9b:1::/48 that a NAT64 prefix shorter
// than /96 may have made. RFC 6052 puts the IPv4 address elsewhere for those,
// and they leave bits 64 to 71 (the u octet) zero, and the bytes after the
// IPv4 address zero.
var errShorterNAT64 = errors.New("is in 64:ff9b:1::/48 in the form a NAT64 prefix shorter than /96 gives, so the IPv4 address it reaches cannot be told (only /96 prefixes are judged)")

// embeddedIPv4 returns the IPv4 addresses that a, an IPv6 address without a
// zone, reaches through a transition mechanism, and that mechanism's name:
// NAT64 (64:ff9b::/96, or a /96 in 64:ff9b:1::/48), 6to4 (2002::/16), Teredo
// (2001::/32, its server and its client) or SIIT (::ffff:0:a.b.c.d). via is ""
// for an address that embeds none.
func embeddedIPv4(a netip.Addr) (via string, v4 []netip.Addr, err error) {
	b := a.As16()
	at := func(i int) netip.Addr { return netip.AddrFrom4([4]byte(b[i : i+4])) }
	switch {
	case nat64LocalUse.Contains(a):
		if b[8] == 0 && b[13] == 0 && b[14] == 0 && b[15] == 0 {
			return "", nil, errShorterNAT64
		}
		return "NAT64", []netip.Addr{at(12)}, nil
	case nat64WellKnown.Contains(a):
		return "NAT64", []netip.Addr{at(12)}, nil
	case sixToFour.Contains(a):
		return "6to4", []netip.Addr{at(2)}, nil
	case teredo.Contains(a):
		client := netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]})
		return "Teredo", []netip.Addr{at(4), client}, nil
	case siit.Contains(a):
		return "SIIT", []netip.Addr{at(12)}, nil
	}
	return "", nil, nil
}
```

`errShorterNAT64` uses `errors.New`. `webhook.go` imports `errors` already (`Webhook.check` uses it, so Task 1's change to `parseWebhooks` keeps the import), and `fmt` and `net/netip` too: the new code needs no import.

The check refuses a few true /96 addresses as well: an IPv4 address ending in `.0.0.0` behind a /96 whose bits 64 to 71 are zero. The message says why, and `allowPrivate` sends to it.

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./internal/config/ -count=1 -race`
Expected: PASS.

- [ ] **Step 5: Docs and commit**

In `README.md`, "Private addresses": "a NAT64 address (`64:ff9b::/96`, or a /96 in `64:ff9b:1::/48`) counts as the IPv4 address it reaches" becomes "an IPv6 address that carries an IPv4 address counts as the IPv4 address it reaches: NAT64 (`64:ff9b::/96`, or a /96 in `64:ff9b:1::/48`), 6to4 (`2002::/16`), Teredo (`2001::/32`, its server and its client) and SIIT (`::ffff:0:a.b.c.d`). An address of `64:ff9b:1::/48` in the form a shorter NAT64 prefix gives is refused, since the IPv4 address it reaches cannot be told; `allowPrivate` sends to it."

```bash
git add internal/config/webhook.go internal/config/webhook_test.go README.md
git commit -m "webhooks: judge 6to4, Teredo and SIIT by their IPv4 addresses; refuse shorter NAT64 local-use forms"
```

---

### Task 8: Crews, runs and links

Triage items #53, #54, #56, #57, #58, #59, #60 and #61. The `api_test.go` split and the duplicate checks (#51) are in Task 10, and the crew-cap tests go with the cap in Task 9.

**Files:**
- Modify: `internal/crew/run.go` (`Launch`, `LaunchHeld`, `IfKept`, the `afterAdd` and `tried` hooks), `internal/crew/handoff.go` (`deliver` calls `tried`), `internal/crew/worktree.go` (`classifyRevParse`), `internal/crew/run_test.go`, `internal/crew/handoff_test.go`, `internal/crew/worktree_test.go`
- Modify: `internal/share/store.go` (`Revoke`, `RevokeRun`, `revoke`), `internal/share/store_test.go`
- Modify: `internal/api/server.go` (`track`, `Shutdown`), `internal/api/links.go` (`handleCreateRunLink`, `handleRevokeRunLink`, `handleRevokeLink`), `internal/api/runs.go` (`handleLaunchCrew`, `launchViewLink`), `internal/api/api_test.go`
- Create: `web/app/components/JoinCrewGrid.vue`
- Modify: `web/app/utils/crews.ts` (`holdViewLink` timer, `RunNameAsks`, `joinTileStatus`), `web/app/utils/crews.test.ts`, `web/app/utils/wall.ts` (`lastItemSpan`), `web/app/utils/wall.test.ts`
- Modify: `web/app/pages/crews/[[id]].vue`, `web/app/pages/join/[token].vue`, `web/app/pages/runs/[run].vue`, `web/app/layouts/default.vue`, `web/app/components/CrewMembersTable.vue` (placeholder, after the browser check)
- Modify: `docs/protocol.md` (`not_a_repo` message, run links)

**Interfaces:**
- Consumes (Task 5): `fakeLauncher.activity` passes the state.
- Produces:
  ```go
  package crew
  func (e *Engine) LaunchHeld(ctx context.Context, c Crew) (*Run, func(), error) // release is never nil; call it once
  func (e *Engine) Launch(ctx context.Context, c Crew) (*Run, error)                // LaunchHeld, released at once
  func (e *Engine) IfKept(runID string, f func()) bool                             // f under the engine's lock
  func (e *Engine) startImmediate(ctx context.Context, r *run) (*Run, error)       // the members that start at once; the existing start(ctx, r, m), one member's start, stays as it is
  // Engine test hooks: afterAdd func(runID string); tried func(member string, typed bool)
  func classifyRevParse(err error) error

  package share
  func (s *Store) Revoke(sessionID, linkID string) (found, revoked bool)
  func (s *Store) RevokeRun(runID, linkID string) (found, revoked bool)

  package api
  func (s *Server) track(f func())   // a goroutine of the server's own; Shutdown waits for it
  ```
  ```ts
  // web/app/utils/wall.ts
  export function lastItemSpan(n: number, layout: GridLayout): number
  // web/app/utils/crews.ts
  export const RUN_NAME_RETRY_MS = 5_000
  export class RunNameAsks { shouldAsk(id: string, now?: number): boolean; failed(id: string, now?: number): void }
  export function joinTileStatus(m: Pick<JoinRunMember, 'status'>, heard?: { status?: string; attention?: string }): { label: string; cls: string; dot: string }
  // components/JoinCrewGrid.vue: props members, role, transportFor, agentName; emits open(member, heard)
  ```

- [ ] **Step 1: Write the failing engine tests**

Add to `internal/crew/run_test.go`:

```go
// A stop that lands before any member's start is reserved ends the launch as
// a later stop does: ErrRunStopped, the run kept and stopped, nothing started.
func TestStopBeforeTheFirstStartEndsTheLaunch(t *testing.T) {
	e, fl := newEngine(t)
	e.afterAdd = func(runID string) {
		if err := e.Stop(context.Background(), runID); err != nil {
			t.Error(err)
		}
	}
	if _, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), manual("tests", ""))); !errors.Is(err, ErrRunStopped) {
		t.Fatalf("Launch: %v", err)
	}
	runs := e.List()
	if len(runs) != 1 || runs[0].StoppedAt == nil {
		t.Fatalf("runs %+v", runs)
	}
	if names := fl.launched(); len(names) != 0 {
		t.Fatalf("launched %v", names)
	}
	if m := memberState(t, e, runs[0].ID, "lead"); m.Status != MemberPending {
		t.Fatalf("lead %+v", m)
	}
}

// A run stays exempt from eviction until its launcher lets it go. The API
// mints the run's view link in between, and a launch at the cap meanwhile
// must not forget the run.
func TestAHeldRunIsNeverForgotten(t *testing.T) {
	e, _ := newEngine(t)
	var forgotten []string
	e.OnForget = func(runID string) { forgotten = append(forgotten, runID) }
	held, release, err := e.LaunchHeld(t.Context(), testCrew(manual("lead", "")))
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxRuns {
		if _, err := e.Launch(t.Context(), testCrew(manual("lead", ""))); err != nil {
			t.Fatalf("launch %d: %v", i, err)
		}
	}
	if _, ok := e.Get(held.ID); !ok {
		t.Fatal("the held run was forgotten")
	}
	release()
	release() // once is enough; twice changes nothing
	if _, err := e.Launch(t.Context(), testCrew(manual("lead", ""))); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Get(held.ID); ok || len(forgotten) < 2 || forgotten[len(forgotten)-1] != held.ID {
		t.Fatalf("the released run, the oldest idle one, was kept past the cap: %v", forgotten)
	}
}

// IfKept runs f under the engine's lock while the run is kept, and not at all
// otherwise: a link made in f cannot outlive a run forgotten meanwhile.
func TestIfKeptRunsUnderTheLockOfAKeptRun(t *testing.T) {
	e, _ := newEngine(t)
	run, err := e.Launch(t.Context(), testCrew(manual("lead", "")))
	if err != nil {
		t.Fatal(err)
	}
	locked := false
	if !e.IfKept(run.ID, func() { locked = !e.mu.TryLock() }) || !locked {
		t.Fatal("f did not run under the engine's lock")
	}
	if e.IfKept("nope", func() { t.Error("f ran for a run the engine does not have") }) {
		t.Fatal("IfKept reported a run the engine does not have")
	}
}
```

Add to `internal/crew/worktree_test.go`:

```go
// A rev-parse failure other than "not a git repository" says what git said:
// a repository owned by someone else, or one the server user cannot read, is
// no missing repository. Both still answer not_a_repo.
func TestClassifyRevParsePassesGitsMessageOn(t *testing.T) {
	for _, tc := range []struct {
		msg, want string
		notInRepo bool
	}{
		{"git rev-parse: fatal: not a git repository (or any of the parent directories): .git", "is not in a git repository", true},
		{"git rev-parse: fatal: detected dubious ownership in repository at '/srv/x'", "detected dubious ownership in repository at '/srv/x'", false},
		{"git rev-parse: fatal: Invalid path '/srv/x/.git': Permission denied", "Permission denied", false},
	} {
		err := classifyRevParse(errors.New(tc.msg))
		if !errors.Is(err, ErrNotRepo) || !strings.Contains(err.Error(), tc.want) || (err == errNotInRepo) != tc.notInRepo {
			t.Errorf("%q: %v", tc.msg, err)
		}
	}
}
```

In `internal/crew/handoff_test.go`, `TestHandoffTypedOrQueued` gets its two sync points. Add `"sync/atomic"` to the imports and this helper:

```go
// waitTrue waits up to 5 s for cond.
func waitTrue(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited 5 s for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

At the top of the test, after `runningPair`:

```go
	var refused atomic.Int32 // attempts to type into tests that found it waiting
	e.tried = func(member string, typed bool) {
		if member == "tests" && !typed {
			refused.Add(1)
		}
	}
```

The "Another prompt is still a prompt" block becomes:

```go
	// Another prompt is still a prompt. Sync point: the engine has tried
	// again since the change, and typed nothing.
	before := refused.Load()
	tests.SetAttention(session.AttentionNeedsInput, "Allow write?", session.SourceAPI)
	waitTrue(t, "a try after the second prompt", func() bool { return refused.Load() > before })
	if got := typed(p); len(got) != 0 {
		t.Fatalf("typed %q into a prompt", got)
	}
```

The last check becomes:

```go
	waitLogged(t, e, run.ID, "handoff delivered from lead to tests", 3, 5*time.Second)
	// Sync point: the goroutine typing for tests has ended.
	waitTrue(t, "the delivery to end", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return !e.runs[run.ID].member("tests").delivering.Load()
	})
	if got := typed(p); len(got) != 0 {
		t.Fatalf("typed %q more", got)
	}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/crew/ -count=1 -run 'StopBeforeTheFirstStart|HeldRun|IfKept|ClassifyRevParse|HandoffTypedOrQueued'`
Expected: build failure, because `afterAdd`, `LaunchHeld`, `IfKept`, `classifyRevParse` and `tried` are undefined.

- [ ] **Step 3: Implement the engine changes**

In `internal/crew/run.go`, `Engine` gains:

```go
	// afterAdd, when set, runs once Launch has kept its run and before any
	// member's start is reserved: a test stops the run there.
	afterAdd func(runID string)
	// tried, when set, is called by deliver after each attempt to type a
	// handoff, with the member and whether it was typed: tests wait on it.
	tried func(member string, typed bool)
```

`Launch` becomes `LaunchHeld`, plus a wrapper:

```go
// Launch is LaunchHeld for a caller that needs nothing more of the run once
// it has it.
func (e *Engine) Launch(ctx context.Context, c Crew) (*Run, error) {
	run, release, err := e.LaunchHeld(ctx, c)
	release()
	return run, err
}

// LaunchHeld starts a run of c, whose Cwd the caller has resolved. With
// isolation "worktree" it first checks that git is on the server's PATH
// (ErrNoGit) and that Cwd is in a git repository to make worktrees of
// (ErrNotRepo), before anything is made. It starts the sessions of the
// members that start immediately, each in a worktree of its own when the
// crew has them, and returns once they exist: each member is starting, and
// its prompt is typed once its session is ready (on the run's context, so a
// client that goes away does not stop it). A member whose prompt cannot be
// typed ends alone. When a member's session cannot be started, the ones
// started are stopped, no run is kept, and the error names the member; when
// the run is stopped meanwhile, it stays, stopped, and LaunchHeld returns
// ErrRunStopped, a stop that lands before the first member's start included.
// A crew with no members, one that runs on a host, one without a valid ID
// or, with worktrees, one whose <cwd>/.conductor or <cwd>/.conductor/worktrees
// is a symbolic link is not launched (ErrInvalid), and neither is a member
// whose directory in its worktree lies through a symbolic link.
//
// The run stays exempt from eviction until the caller calls release, which
// is never nil and must be called once, after an error too (a second call
// does nothing): the API mints the run's view link first, so that a launch at
// the cap cannot forget the run between its start and its link.
func (e *Engine) LaunchHeld(ctx context.Context, c Crew) (*Run, func(), error) {
	noop := func() {}
	if err := c.validateWithID(); err != nil {
		return nil, noop, err
	}
	if err := c.Launchable(); err != nil {
		return nil, noop, err
	}
	prefix := ""
	if c.Isolation == IsolationWorktree {
		if err := checkGit(); err != nil {
			return nil, noop, err
		}
		if !filepath.IsAbs(c.Cwd) {
			return nil, noop, invalidf("with worktrees, cwd must be an absolute path")
		}
		if err := checkWorktreesDir(c.Cwd); err != nil {
			return nil, noop, err
		}
		if err := CheckRepo(ctx, c.Cwd); err != nil {
			return nil, noop, err
		}
		p, err := repoPrefix(ctx, c.Cwd)
		if err != nil {
			return nil, noop, err
		}
		prefix = p
	}
	r := e.add(c, prefix)
	release := sync.OnceFunc(func() { e.launched(r) })
	if e.afterAdd != nil {
		e.afterAdd(r.id)
	}
	run, err := e.startImmediate(ctx, r)
	return run, release, err
}

// startImmediate starts the members of r that start immediately and returns
// r as it then is. r is launching (held) until its launcher releases it, so
// no other launch forgets it meanwhile. (Engine.start, which starts one
// member, is another function and stays as it is.)
func (e *Engine) startImmediate(ctx context.Context, r *run) (*Run, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(r.ctx, cancel)()

	e.mu.Lock()
	if r.stopping {
		// Stopped before any start was reserved: the run stays, stopped.
		e.mu.Unlock()
		return nil, ErrRunStopped
	}
	var starts []*member
	for _, m := range r.members {
		if m.def.Start.When == StartImmediately && r.reserve(m) {
			starts = append(starts, m)
		}
	}
	e.mu.Unlock()
	for i, m := range starts {
		local, err := e.launch(ctx, r, m)
		if err == nil {
			go e.finish(r, m, local) // takes over the start
			continue
		}
		if errors.Is(err, ErrRunStopped) || r.ctx.Err() != nil {
			// Stopped meanwhile: the run stays, stopped, and so does what
			// had started. The members not begun were never started.
			e.fail(r, m, ErrRunStopped)
			e.mu.Lock()
			for _, rest := range starts[i+1:] {
				rest.state.Status = MemberPending
			}
			e.mu.Unlock()
			for range starts[i:] {
				r.starts.Done()
			}
			return nil, ErrRunStopped
		}
		for range starts[i:] {
			r.starts.Done()
		}
		e.abort(r)
		return nil, fmt.Errorf("member %s: %w", quote(m.def.Name), err)
	}
	// The check stays, should something else ever forget a run.
	out, ok := e.Get(r.id)
	if !ok {
		return nil, ErrRunNotFound
	}
	return &out, nil
}
```

From the `e.mu.Lock()` that reserves the starts to the end, `startImmediate` is the old body of `Launch`; the stopping check before the reservation is new. `run.go` imports `sync` already (`run.starts`).

The old `defer e.launched(r)` goes; `release` calls `launched`. Update the `launching` field's comment: "set from add until the launcher releases the run (LaunchHeld): evict leaves the run alone meanwhile". The "The run is launching until Launch returns" comment before the final `Get` is gone (above).

```go
// IfKept runs f while the engine keeps the run with the given ID, under the
// engine's lock, and reports whether it ran. A run is forgotten under that
// lock too (OnForget), so what f makes for the run, such as a link, cannot
// outlive a forgotten run unseen. f must not call the engine or wait.
func (e *Engine) IfKept(runID string, f func()) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.runs[runID]; !ok {
		return false
	}
	f()
	return true
}
```

In `internal/crew/handoff.go`, `deliver`, right after `typed, err := l.TypeUnlessWaiting(h.text+"\r", typedBy)`:

```go
		if e.tried != nil {
			e.tried(m.def.Name, typed)
		}
```

In `internal/crew/worktree.go`:

```go
// inRepo checks that dir is in a git working tree: git -C dir rev-parse
// --show-toplevel succeeds. The error is ErrNoGit when git is not on PATH,
// and otherwise matches ErrNotRepo: "not in a git repository" when git says
// so, git's own message for any other failure (classifyRevParse).
func inRepo(ctx context.Context, dir string) error {
	if _, err := git(ctx, dir, "rev-parse", "--show-toplevel"); err != nil {
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, exec.ErrNotFound):
			return ErrNoGit
		}
		return classifyRevParse(err)
	}
	return nil
}

// classifyRevParse is the error inRepo reports for a failed rev-parse:
// errNotInRepo when git says the directory is not in a repository, and git's
// message otherwise, as for a repository owned by another user (dubious
// ownership) or one the server user cannot read. Both match ErrNotRepo.
func classifyRevParse(err error) error {
	msg := strings.TrimPrefix(err.Error(), "git rev-parse: ")
	if strings.Contains(msg, "not a git repository") {
		return errNotInRepo
	}
	return &repoError{"git cannot use the working directory's repository: " + msg}
}
```

Run: `go test -race -count=1 ./internal/crew/`
Expected: PASS, the existing `TestStopDuringLaunchKeepsTheRun` and `TestALaunchingRunIsNeverForgotten` included.

- [ ] **Step 4: Write the failing link and shutdown tests**

In `internal/share/store_test.go`, add a helper, and wrap every boolean use of `s.Revoke(…)` and `s.RevokeRun(…)` in it. These are the six conditions that change, each in place:

| Test | Before | After |
|---|---|---|
| `TestCreateResolveRevoke` | `if !s.Revoke("sess", link.ID) {` | `if !found(s.Revoke("sess", link.ID)) {` |
| `TestCreateResolveRevoke` | `if s.Revoke("other", link.ID) {` | `if found(s.Revoke("other", link.ID)) {` |
| `TestRunLinks` | `if s.Revoke("", link.ID) \|\| s.Revoke("run", link.ID) {` | `if found(s.Revoke("", link.ID)) \|\| found(s.Revoke("run", link.ID)) {` |
| `TestRunLinks` | `if s.RevokeRun("other", link.ID) {` | `if found(s.RevokeRun("other", link.ID)) {` |
| `TestRunLinks` | `if s.RevokeRun("", sl.ID) \|\| s.RevokeRun("sess", sl.ID) {` | `if found(s.RevokeRun("", sl.ID)) \|\| found(s.RevokeRun("sess", sl.ID)) {` |
| `TestRunLinks` | `if !s.RevokeRun("run", link.ID) {` | `if !found(s.RevokeRun("run", link.ID)) {` |

The five calls used as statements stay as they are, since a call statement discards both results: `s.Revoke("sess", link.ID)` in `TestCreateResolveRevoke`, `s.RevokeRun("run", link.ID)` in `TestRunLinks`, `s.RevokeRun("run", revoked.ID)` in `TestDeleteRun`, and `s.RevokeRun("run", link.ID)` and `s.Revoke("sess", sl.ID)` in `TestResolveWhileRevoking`. `grep -n 'Revoke' internal/share/store_test.go` lists them all; after the edit, every one inside a condition goes through `found`.

```go
// found is the first result of a revoke: whether the link was the caller's.
func found(ok, _ bool) bool { return ok }

// A revoke says whether it revoked the link: a second one finds it revoked
// already, and the hook fires once.
func TestRevokeSaysWhetherItRevoked(t *testing.T) {
	s := NewStore()
	link, _, _ := s.CreateRunLink("run", session.RoleView, "", 0)
	if ok, revoked := s.RevokeRun("run", link.ID); !ok || !revoked {
		t.Fatalf("first: %v %v", ok, revoked)
	}
	if ok, revoked := s.RevokeRun("run", link.ID); !ok || revoked {
		t.Fatalf("second: %v %v", ok, revoked)
	}
	sl, _, _ := s.Create("sess", session.RoleView, "", 0)
	if ok, revoked := s.Revoke("sess", sl.ID); !ok || !revoked {
		t.Fatalf("session first: %v %v", ok, revoked)
	}
	if ok, revoked := s.Revoke("sess", sl.ID); !ok || revoked {
		t.Fatalf("session second: %v %v", ok, revoked)
	}
}
```

Add to `internal/api/api_test.go`:

```go
// Revoking a link twice notes it once, in the run's log and in the session's.
func TestRevokingARevokedLinkRecordsNothing(t *testing.T) {
	e := newTestEnv(t, nil)
	e.stopEverything(t)
	runID := e.launchCrew(t, "Crew", catMember("lead", "manual"))
	_, out := e.do("POST", "/api/runs/"+runID+"/links", adminToken, map[string]any{"role": "view", "label": "pair"})
	linkID := out["link"].(map[string]any)["id"].(string)
	for range 2 {
		if resp, _ := e.do("DELETE", "/api/runs/"+runID+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("revoke: %d", resp.StatusCode)
		}
	}
	_, out = e.do("GET", "/api/runs/"+runID, adminToken, nil)
	n := 0
	for _, raw := range out["run"].(map[string]any)["log"].([]any) {
		if raw.(map[string]any)["message"] == "link revoked: pair" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the run log notes the revoke %d times", n)
	}
	id := e.createSession("cat")
	_, out = e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view", "label": "solo"})
	sl := out["link"].(map[string]any)["id"].(string)
	for range 2 {
		e.do("DELETE", "/api/sessions/"+id+"/links/"+sl, adminToken, nil)
	}
	n = 0
	for _, a := range e.local(id).Activity() {
		if a.Type == session.ActivityLink && a.Message == "link revoked: solo" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the session records the revoke %d times", n)
	}
}

// Closing the viewers of a forgotten run's links is the server's own work:
// Shutdown waits for it, or for its context.
func TestShutdownWaitsForWhatTheServerStarted(t *testing.T) {
	e := newTestEnv(t, nil)
	release := make(chan struct{})
	e.srv.track(func() { <-release })
	done := make(chan struct{})
	go func() {
		e.srv.Shutdown(context.Background())
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Shutdown returned while the server's own goroutine ran")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return")
	}
}
```

Run: `go test ./internal/share/ ./internal/api/ -count=1 -run 'RevokeSaysWhether|RevokingARevoked|ShutdownWaits'`
Expected: build failure, because `Revoke` returns one value and `track` is undefined.

- [ ] **Step 5: Implement the link fixes and the tracked goroutine**

In `internal/share/store.go`:

```go
// Revoke marks a session's link unusable and notifies OnRevoke the first
// time. found is false when the link does not belong to sessionID; revoked
// says whether this call revoked it (false when it was revoked before).
func (s *Store) Revoke(sessionID, linkID string) (found, revoked bool) {
	return s.revoke(linkID, func(l *Link) bool { return l.RunID == "" && l.SessionID == sessionID }, func() {
		if s.OnRevoke != nil {
			s.OnRevoke(sessionID, linkID)
		}
	})
}

// RevokeRun is Revoke for a run's link, notifying OnRevokeRun.
func (s *Store) RevokeRun(runID, linkID string) (found, revoked bool) {
	return s.revoke(linkID, func(l *Link) bool { return l.RunID != "" && l.RunID == runID }, func() {
		if s.OnRevokeRun != nil {
			s.OnRevokeRun(runID, linkID)
		}
	})
}

func (s *Store) revoke(linkID string, owned func(*Link) bool, notify func()) (found, revoked bool) {
	s.mu.Lock()
	link, ok := s.byID[linkID]
	if !ok || !owned(link) {
		s.mu.Unlock()
		return false, false
	}
	already := link.Revoked
	link.Revoked = true
	s.mu.Unlock()
	if !already {
		notify()
	}
	return true, !already
}
```

In `internal/api/links.go`:

```go
func (s *Server) handleCreateRunLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run")
	if _, ok := s.runs.Get(id); !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	req, ok := readLinkRequest(w, r)
	if !ok {
		return
	}
	// The link is made under the check that the run is kept: a run forgotten
	// in between takes its links (OnForget), and this one would be left over.
	var (
		link  *share.Link
		token string
		err   error
	)
	if !s.runs.IfKept(id, func() { link, token, err = s.links.CreateRunLink(id, req.Role, req.Label, req.ttl()) }) {
		writeError(w, http.StatusNotFound, "not_found", "no such run")
		return
	}
	if err != nil {
		writeLinkError(w, err, "too many links for this run")
		return
	}
	s.runs.Note(id, session.ActivityLink, "link created: "+linkLabelOr(link.Label)+" ("+string(link.Role)+")")
	s.writeLink(w, link, token)
}
```

In `handleRevokeRunLink`:

```go
	found, revoked := s.links.RevokeRun(id, linkID)
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "no such link")
		return
	}
	if revoked {
		s.runs.Note(id, session.ActivityLink, "link revoked: "+linkLabelOr(label))
	}
	w.WriteHeader(http.StatusNoContent)
```

`handleRevokeLink` does the same with `s.links.Revoke(id, linkID)` and `s.recordLink`.

In `internal/api/runs.go`, `handleLaunchCrew` holds the run until its link is minted:

```go
	run, release, err := s.runs.LaunchHeld(ctx, c)
	// Released once the reply is written: the view link is minted first, so a
	// launch at the cap meanwhile cannot forget the run.
	defer release()
	if err != nil {
		s.runError(w, "launch", c.ID, err)
		return
	}
```

`launchViewLink` creates the link under the same check (`runs.go` imports `internal/share` for `*share.Link`):

```go
func (s *Server) launchViewLink(runID string, ttl time.Duration) map[string]any {
	var (
		link  *share.Link
		token string
		err   error
	)
	if !s.runs.IfKept(runID, func() { link, token, err = s.links.CreateRunLink(runID, session.RoleView, "launch", ttl) }) {
		return nil
	}
	if err != nil {
		s.log.Warn("launch view link not created", "run", runID, "err", err)
		s.runs.Note(runID, session.ActivityError, "the view link could not be created: "+err.Error())
		return nil
	}
	s.runs.Note(runID, session.ActivityLink, "link created: "+linkLabelOr(link.Label)+" ("+string(link.Role)+")")
	return s.linkReply(link, token)
}
```

In `internal/api/server.go`, `Server` gains:

```go
	// bg counts the goroutines the server starts on its own, outside any
	// request: Shutdown waits for them. bgMu guards bgDone, which is set once
	// Shutdown waits; what starts after that is not counted.
	bg     sync.WaitGroup
	bgMu   sync.Mutex
	bgDone bool
```

```go
// track runs f on a goroutine of its own that Shutdown waits for.
func (s *Server) track(f func()) {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.bgDone {
		go f()
		return
	}
	s.bg.Go(f)
}
```

`OnForget` calls `s.track(func() { s.disconnectLinks(live) })` in place of `go s.disconnectLinks(live)`. `Shutdown` ends with:

```go
	// What the server started on its own (the viewers of a forgotten run's
	// links being closed) ends before Shutdown returns, or ctx does.
	s.bgMu.Lock()
	s.bgDone = true
	s.bgMu.Unlock()
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
```

Run: `go test -race -count=1 ./internal/share/ ./internal/api/ ./internal/hostagent/ ./internal/crew/`
Expected: PASS.

- [ ] **Step 6: Write the failing web tests**

Add to `web/app/utils/wall.test.ts` (import `lastItemSpan`):

```ts
describe('lastItemSpan', () => {
  it('fills the cells the last row leaves', () => {
    expect(lastItemSpan(5, { cols: 3, rows: 2 })).toBe(2)
    expect(lastItemSpan(6, { cols: 3, rows: 2 })).toBe(1)
    expect(lastItemSpan(3, { cols: 2, rows: 2 })).toBe(2)
    expect(lastItemSpan(1, { cols: 1, rows: 1 })).toBe(1)
  })
  it('stays within a row, whatever bestGrid picks', () => {
    for (let n = 1; n <= 13; n++) {
      const g = bestGrid(n, 1600, 900, 12, 1.4)
      const span = lastItemSpan(n, g)
      expect(span).toBeGreaterThanOrEqual(1)
      expect(span).toBeLessThanOrEqual(g.cols)
    }
  })
})
```

Add to `web/app/utils/crews.test.ts` (import `RUN_NAME_RETRY_MS`, `RunNameAsks`, `joinTileStatus`; add `vi` to the `vitest` import):

```ts
describe('holdViewLink', () => {
  it('drops a link nobody took within a minute, without waiting for a take', () => {
    vi.useFakeTimers()
    try {
      holdViewLink('r1', 'https://x/join/t', 3600, 0)
      vi.advanceTimersByTime(60_001)
      // Taken "at" 0, within the hold: only the timer can have dropped it.
      expect(takeViewLink('r1', 0)).toBeNull()
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('RunNameAsks', () => {
  it('asks once, and again after a failure once the retry time has passed', () => {
    const asks = new RunNameAsks()
    expect(asks.shouldAsk('r1', 0)).toBe(true)
    expect(asks.shouldAsk('r1', 1)).toBe(false)
    asks.failed('r1', 10)
    expect(asks.shouldAsk('r1', 10 + RUN_NAME_RETRY_MS - 1)).toBe(false)
    expect(asks.shouldAsk('r1', 10 + RUN_NAME_RETRY_MS)).toBe(true)
    expect(asks.shouldAsk('r2', 0)).toBe(true)
  })
})

describe('joinTileStatus', () => {
  it("prefers what the tile's terminal reported, and flags a prompt", () => {
    expect(joinTileStatus({ status: 'running' }, { attention: 'needs_input' })).toMatchObject({ label: 'Needs input' })
    expect(joinTileStatus({ status: 'starting' }, { status: 'running' })).toMatchObject({ label: 'Running' })
    expect(joinTileStatus({ status: 'ended' }, { attention: 'needs_input' })).toMatchObject({ label: 'ended' })
    expect(joinTileStatus({ status: 'pending' })).toMatchObject({ label: 'pending', cls: 'text-muted' })
  })
})
```

Run: `npm --prefix web test -- wall crews`
Expected: FAIL, because `lastItemSpan`, `RunNameAsks` and `joinTileStatus` do not exist, and the held link survives the minute.

- [ ] **Step 7: Implement the helpers**

In `web/app/utils/wall.ts`:

```ts
/**
 * How many cells the item placed last in a grid of `n` items fills: the cells
 * the last row leaves empty, and its own. The crew view puts its feed there,
 * so its grid has no empty cell. At least 1, at most a row.
 */
export function lastItemSpan(n: number, layout: GridLayout): number {
  if (n <= 0) return 1
  return Math.min(layout.cols, Math.max(1, layout.cols * layout.rows - n + 1))
}
```

In `web/app/utils/crews.ts`:

```ts
let heldTimer: ReturnType<typeof setTimeout> | undefined

/**
 * Hands the view link a launch returned to the crew view the launch opens, in memory only: the crew view takes it once (takeViewLink), and
 * a timer drops it once the hold is over, so a token nobody took does not stay in memory.
 */
export function holdViewLink(runId: string, url: string, ttlSeconds: number, now = Date.now()) {
  clearTimeout(heldTimer)
  const held = { runId, url, ttlSeconds, at: now }
  heldViewLink = held
  heldTimer = setTimeout(() => {
    if (heldViewLink === held) heldViewLink = null
  }, VIEW_LINK_HOLD_MS)
}

/** How long a run name that could not be read waits before it is asked for again. */
export const RUN_NAME_RETRY_MS = 5_000

/** Which run names the layout reads: each once, and again after a failure once RUN_NAME_RETRY_MS has passed. */
export class RunNameAsks {
  /** By run: when it may be asked again; Infinity while asked or read. */
  private next = new Map<string, number>()
  shouldAsk(id: string, now = Date.now()): boolean {
    const at = this.next.get(id)
    if (at !== undefined && now < at) return false
    this.next.set(id, Infinity)
    return true
  }
  failed(id: string, now = Date.now()): void {
    this.next.set(id, now + RUN_NAME_RETRY_MS)
  }
}

/** A run link's member tile: its label and classes, from what its terminal last reported (`heard`), else the run's own status. */
export function joinTileStatus(m: Pick<JoinRunMember, 'status'>, heard?: { status?: string; attention?: string }): { label: string; cls: string; dot: string } {
  const st = heard?.status ?? m.status
  if (heard?.attention === 'needs_input' && (st === 'running' || st === 'starting')) return { label: 'Needs input', cls: 'text-warning', dot: 'bg-warning' }
  if (st === 'running') return { label: 'Running', cls: 'text-success', dot: 'bg-success' }
  return { label: st.replace('_', ' '), cls: 'text-muted', dot: 'bg-neutral-400' }
}
```

(`crews.ts` imports `JoinRunMember` from `~/composables/useSessions`.)

Run: `npm --prefix web test -- wall crews`
Expected: PASS.

- [ ] **Step 8: Use them in the pages**

`web/app/pages/runs/[run].vue`:

```ts
import { bestGrid, lastItemSpan } from '~/utils/wall'
// next to the `layout` computed:
const feedSpan = computed(() => lastItemSpan(tiles.value.length + 1, layout.value))
```

```vue
            <CrewFeed :items="feed" :style="narrow ? undefined : { gridColumn: `span ${feedSpan} / span ${feedSpan}` }" />
```

`web/app/layouts/default.vue` replaces `askedNames` and its watcher:

```ts
const asks = new RunNameAsks()
let retry: ReturnType<typeof setTimeout> | undefined
async function readRunName(id: string) {
  // The crew view reads its run itself.
  if (runRoute.value || runNames.value[id] || !asks.shouldAsk(id)) return
  try {
    const r = await api.getRun(id)
    runNames.value = { ...runNames.value, [r.id]: r.name }
  } catch {
    // The crew id stands in until a later try reads it.
    asks.failed(id)
    clearTimeout(retry)
    retry = setTimeout(() => {
      if (sidebarRun.value === id) readRunName(id)
    }, RUN_NAME_RETRY_MS)
  }
}
watch(sidebarRun, (id) => {
  if (id) readRunName(id)
}, { immediate: true })
onBeforeUnmount(() => clearTimeout(retry))
```

`web/app/pages/crews/[[id]].vue` keeps a launch's link until it is shown, and reads the crews again on every visit to a crew:

```ts
/**
 * The view link a launch returned, until this page has shown it: in app state, so a launch that resolves after the page was left keeps it
 * for the next visit, and a toast offers it meanwhile. Its token is in no other state.
 */
const shownLink = useState<{ url: string; runId: string; name: string; hours: number; crewId: string } | null>('crewShownLink', () => null)
let mounted = false
```

In `launch()`, the `viewLink` branch becomes:

```ts
    if (viewLink) {
      shownLink.value = { url: viewLink.url, runId: run.id, name: c.name, hours: hours(ttl), crewId: c.id }
      if (!mounted) {
        toast.add({
          title: `${c.name} launched`,
          description: 'Its view link is shown once, on the Crews page.',
          icon: 'i-lucide-link',
          color: 'success',
          actions: [{ label: 'Show the link', onClick: () => router.push(`/crews/${encodeURIComponent(c.id)}`) }],
        })
      }
      return
    }
```

`onMounted` sets `mounted = true`, and `onBeforeUnmount` sets `mounted = false`. Add `watch(routeId, () => refresh())` next to the `admin.token` watcher, so moving between crews within the page reads them again.

Create `web/app/components/JoinCrewGrid.vue` from the run-link grid of `pages/join/[token].vue`:

```vue
<script setup lang="ts">
import type { JoinRunMember } from '~/composables/useSessions'
import type { Role } from '~/utils/protocol'
import type { TerminalTransport } from '~/utils/transport/types'
import { joinTileStatus } from '~/utils/crews'
import { agentInitials } from '~/utils/sessions'

/** The member tiles of a run link: a live, read-only terminal for each member that runs, a placeholder for the rest. Opening one is the page's to do. */
const props = defineProps<{
  members: JoinRunMember[]
  role: Role
  transportFor: (m: JoinRunMember) => () => TerminalTransport
  agentName: (agentId: string) => string
}>()
const emit = defineEmits<{ open: [member: JoinRunMember, heard: { status?: string; attention?: string } | undefined] }>()

/** What each member's tile last heard from its session, by member name. */
const heard = ref<Record<string, { status?: string; attention?: string }>>({})
function hear(name: string, patch: { status?: string; attention?: string }) {
  heard.value = { ...heard.value, [name]: { ...heard.value[name], ...patch } }
}
function open(m: JoinRunMember) {
  if (m.sessionId) emit('open', m, heard.value[m.name])
}
</script>

<template>
  <div class="grid auto-rows-[18rem] gap-3 sm:grid-cols-2 xl:grid-cols-3" data-join-tiles>
    <template v-for="m in members" :key="m.name">
      <div
        v-if="m.sessionId"
        class="flex min-h-0 cursor-pointer flex-col overflow-hidden rounded-lg border bg-elevated/40 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-primary"
        :class="heard[m.name]?.attention === 'needs_input' ? 'border-warning ring-2 ring-warning/60' : 'border-default hover:border-accented'"
        role="button"
        tabindex="0"
        :aria-label="`Open ${m.name}`"
        :data-member="m.name"
        @click="open(m)"
        @keydown.enter.prevent="open(m)"
        @keydown.space.prevent="open(m)"
      >
        <div class="flex flex-none items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs">
          <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(m.agentId) }}</span>
          <span class="flex-1 truncate text-[13px] font-semibold">{{ m.name }}</span>
          <span class="flex items-center gap-1.5 text-[11.5px]" :class="joinTileStatus(m, heard[m.name]).cls"><span class="size-[7px] rounded-full" :class="joinTileStatus(m, heard[m.name]).dot" aria-hidden="true" />{{ joinTileStatus(m, heard[m.name]).label }}</span>
        </div>
        <div class="pointer-events-none min-h-0 flex-1">
          <TerminalView
            :create-transport="props.transportFor(m)"
            read-only
            fit="scale"
            compact
            :auto-focus="false"
            @status="(s) => hear(m.name, { status: s })"
            @attention="(a) => hear(m.name, { attention: a.state })"
          />
        </div>
        <div class="flex flex-none items-center gap-2 border-t border-default px-2.5 py-1 font-mono text-[11px] text-muted">
          <span class="truncate">{{ props.agentName(m.agentId) }}</span>
          <span class="ml-auto flex-none">{{ role === 'control' ? 'open to type' : 'open' }}</span>
        </div>
      </div>
      <div v-else class="flex min-h-0 flex-col overflow-hidden rounded-lg border border-dashed border-accented" :data-member="m.name">
        <div class="flex items-center gap-2 border-b border-default px-2.5 py-1.5 text-xs">
          <span class="font-mono text-[10px] font-semibold text-muted">{{ agentInitials(m.agentId) }}</span>
          <span class="flex-1 truncate text-[13px] font-semibold">{{ m.name }}</span>
        </div>
        <div class="flex flex-1 items-center justify-center p-3 text-sm text-muted">{{ m.status === 'ended' ? 'ended' : m.status === 'starting' ? 'starting…' : 'not started yet' }}</div>
      </div>
    </template>
  </div>
</template>
```

In `pages/join/[token].vue`, replace the grid `div` with `<JoinCrewGrid :members="run.members" :role="info!.role" :transport-for="tileTransport" :agent-name="agentName" @open="openMember" />`. Delete `tileState`, `setTile` and `tileStatus`. `openMember(m: JoinRunMember, heard?: { status?: string; attention?: string })` sets `status.value = heard?.status ?? m.status`. Drop the `agentInitials` import if nothing else uses it.

Run: `npm --prefix web test && npm --prefix web run typecheck`
Expected: PASS.

- [ ] **Step 9: Check the prompt placeholder in a browser at narrow widths**

Build and start a test server (Verification, steps 1 and 2), sign in, open **Crews**, then **New crew**. With viewports 375×800, 768×1024 and 1280×800, screenshot the members table and read the prompt cell's placeholder. If the placeholder is cut short at any width, which the one-row `nowrap` textarea does whenever the cell is narrower than the sentence, shorten it in `CrewMembersTable.vue` and keep the sentence as the field's `title`:

```vue
        :placeholder="agentOf(m.agentId)?.allowArgs === false ? 'Typed into the shell (optional)' : 'Role prompt; $GOAL is the goal'"
        :title="agentOf(m.agentId)?.allowArgs === false ? 'Typed into the shell as one line once it is ready (optional)' : 'What this agent does, typed into the agent as one line. $GOAL is the crew\'s goal.'"
```

Screenshot again and record in the commit message the widths checked and what was seen.

- [ ] **Step 10: Docs and commit**

In `docs/protocol.md`, the `not_a_repo` text of the launch row gains: "a `rev-parse` failure other than 'not a git repository' (a repository owned by another user, or one that cannot be read) says what git said". In the run links paragraph, add: "revoking a revoked link answers `204` and logs nothing".

Run: `make lint && go test -race -count=1 ./internal/crew/ ./internal/share/ ./internal/api/ ./internal/hostagent/ && npm --prefix web test && npm --prefix web run typecheck`

```bash
git add internal/crew internal/share internal/api web/app docs/protocol.md
git commit -m "crews: stop before the first start, held runs, links under the run check, tracked disconnects, git's messages; web: kept launch link, retried run names, JoinCrewGrid, no empty grid cell"
```

---

### Task 9: Crew storage: one file per crew, a paged list

The spec's "Crew storage" block. It replaces `crews.json`, the 50-crew cap and its tests, and the 512 KiB bound. The crews directory is `crews/` in whichever data directory Task 1 resolves (`<dataDir>/crews/<id>.json`).

**Files:**
- Modify: `internal/store/store.go` (`Sub`, `Entry`, `List`, `Delete`, `LoadLimit`, the package and `Store` comments), `internal/store/store_test.go`
- Modify: `internal/crew/store.go` (rewritten), `internal/crew/crew.go` (`MaxEncoded`, `maxEncoded`, size measured as the file), `internal/crew/crew_test.go`
- Create: `internal/crew/migrate.go`, `internal/crew/store_test.go`
- Modify: `internal/api/crews.go`, `internal/api/server.go` (`New`), `internal/api/runs.go` (`handleLaunchCrew`), `internal/api/api_test.go`
- Modify: `internal/cli/up.go` (`runCrews` pages), `internal/cli/up_test.go`
- Modify: `web/app/composables/useSessions.ts` (`CrewSummary`, `listCrews`, `getCrew`, comments), `web/app/utils/crews.ts` (`summaryOf`), `web/app/utils/crews.test.ts`, `web/app/pages/crews/[[id]].vue`
- Modify: `docs/protocol.md` (crew rows, the persistence paragraph, the limits, the body-size sentence), `README.md` (where crews are saved, the crew limits at lines 433 to 437), `docs/architecture.md:94`, `AGENTS.md` map row for `internal/crew`

**Interfaces:**
- Consumes (Task 1): `store.Encode`, `store.DecodeStrict`. (Task 8): `Engine.LaunchHeld` in `handleLaunchCrew`.
- Produces:
  ```go
  package store
  func (s *Store) Sub(name string) (*Store, error)                       // name ^[a-z0-9-]+$, made 0700
  type Entry struct { Name string; Size int64; ModTime time.Time }
  func (s *Store) List() ([]Entry, error)                                 // documents only, by name
  func (s *Store) Delete(name string) error                               // missing is no error
  func (s *Store) LoadLimit(name string, v any, limit int64) (bool, error) // refuses a link or any file but a regular one (Lstat), as List leaves them out

  package crew
  const MaxEncoded = 1 << 20
  var maxEncoded = MaxEncoded                                             // a test lowers it
  var ErrUnreadable = errors.New("the crew's file cannot be used")
  var ErrWrite = errors.New("the crew's file could not be written")      // wraps a failed save or delete; a read failure does not
  type Summary struct { ID, Name, Cwd, Where, Isolation string; Members []MemberSummary; UpdatedAt time.Time } // json: id name cwd where isolation members updatedAt
  type MemberSummary struct { Name string `json:"name"`; AgentID string `json:"agentId"` }
  func NewStore(st *store.Store) (s *Store, problems []error, err error) // problems: unusable files and crews of crews.json not moved, each naming its files
  func (s *Store) List(offset, limit int) ([]Summary, int, error)
  func (s *Store) Get(id string) (Crew, error)                            // ErrNotFound, or wraps ErrUnreadable (a link included)
  func migrate(st, dir *store.Store) (notices []error, err error)         // migrate.go
  // Put, Create, Update, Duplicate, Delete keep their signatures; ErrTooManyCrews is gone.

  package api
  const maxCrewBody = 2 << 20                                             // a crew create or update body: twice crew.MaxEncoded
  // GET /api/crews?offset=0&limit=100 -> 200 {crews: [Summary], total}; 400 invalid_request for a bad offset or limit
  // GET /api/crews/{id} -> 200 {crew}; 404 not_found; 409 crew_unreadable; 503 store_unavailable
  ```
  ```ts
  export interface CrewSummary { id: string; name: string; cwd: string; where: 'server' | 'host'; isolation: 'none' | 'worktree'; members: Array<{ name: string; agentId: string }>; updatedAt: string }
  listCrews: (offset?: number, limit?: number) => Promise<{ crews: CrewSummary[]; total: number }>
  getCrew: (id: string) => Promise<CrewInfo>
  export function summaryOf(c: CrewInfo): CrewSummary   // utils/crews.ts
  ```

- [ ] **Step 1: Write the failing store tests**

Append to `internal/store/store_test.go` (import `"slices"`):

```go
// A sub-store is a directory of the store, made 0700; it lists its documents,
// and only them, by name, and deletes them.
func TestSubListAndDelete(t *testing.T) {
	s, _ := Open(t.TempDir())
	sub, err := s.Sub("crews")
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(sub.Dir()); err != nil || fi.Mode().Perm() != 0o700 || sub.Dir() != filepath.Join(s.Dir(), "crews") {
		t.Fatalf("sub dir %s: %v %v", sub.Dir(), fi, err)
	}
	for _, bad := range []string{"", "../x", "a/b", "X"} {
		if _, err := s.Sub(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if list, err := sub.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty: %v %v", list, err)
	}
	for _, name := range []string{"b.json", "a.json"} {
		if err := sub.Save(name, doc{N: 1}); err != nil {
			t.Fatal(err)
		}
	}
	// Not documents: a temp file, the probe, an upper-case name, a link, a directory.
	writeRaw(t, sub, ".probe-1", "")
	writeRaw(t, sub, "c.json.123.tmp", "{}")
	writeRaw(t, sub, "Upper.json", "{}")
	if err := os.Symlink(filepath.Join(sub.Dir(), "a.json"), filepath.Join(sub.Dir(), "link.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(sub.Dir(), "dir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	list, err := sub.List()
	var names []string
	for _, e := range list {
		names = append(names, e.Name)
	}
	if err != nil || !slices.Equal(names, []string{"a.json", "b.json"}) || list[0].Size == 0 || list[0].ModTime.IsZero() {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := sub.Delete("a.json"); err != nil {
		t.Fatal(err)
	}
	if err := sub.Delete("a.json"); err != nil {
		t.Fatalf("deleting a missing document: %v", err)
	}
	if err := sub.Delete("../x.json"); err == nil {
		t.Fatal("a bad name was deleted")
	}
	if list, _ := sub.List(); len(list) != 1 || list[0].Name != "b.json" {
		t.Fatalf("after delete: %+v", list)
	}
}

// LoadLimit reads a document of at most limit bytes, and refuses a larger one
// without reading past the limit.
func TestLoadLimit(t *testing.T) {
	s, _ := Open(t.TempDir())
	if err := s.Save("c.json", doc{N: 12345}); err != nil { // 17 bytes
		t.Fatal(err)
	}
	var d doc
	if ok, err := s.LoadLimit("c.json", &d, 64); !ok || err != nil || d.N != 12345 {
		t.Fatalf("within: %v %v %+v", ok, err, d)
	}
	if ok, err := s.LoadLimit("c.json", &d, 8); ok || err == nil || !strings.Contains(err.Error(), "more than 8 bytes") {
		t.Fatalf("over: %v %v", ok, err)
	}
	if ok, err := s.LoadLimit("gone.json", &d, 8); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
	writeRaw(t, s, "bad.json", `{"n":1} x`)
	if _, err := s.LoadLimit("bad.json", &d, 64); err == nil || !strings.Contains(err.Error(), "bad.json") {
		t.Fatalf("trailing data: %v", err)
	}
	// A link is refused, never read through, as List leaves links out.
	if err := os.Symlink(filepath.Join(s.Dir(), "c.json"), filepath.Join(s.Dir(), "link.json")); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.LoadLimit("link.json", &d, 64); ok || err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("a link: %v %v", ok, err)
	}
}
```

Run: `go test ./internal/store/ -count=1`
Expected: build failure, because `Sub`, `List`, `Delete` and `LoadLimit` are undefined.

- [ ] **Step 2: Implement them**

In `internal/store/store.go` (import `"time"`; `"io"` is imported already):

```go
// subPattern limits a sub-store's name to a flat, lower-case directory name.
var subPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Sub opens the directory name inside s as a store of its own, made 0700 when
// missing and refused when it cannot be written, as Open does.
func (s *Store) Sub(name string) (*Store, error) {
	if !subPattern.MatchString(name) {
		return nil, fmt.Errorf("store: bad name %q", name)
	}
	return Open(filepath.Join(s.dir, name))
}

// Entry is a document in a store, as List reports it.
type Entry struct {
	Name    string // the document's name, such as "todo-app.json"
	Size    int64
	ModTime time.Time
}

// List returns the documents in the store, sorted by name: the regular files
// whose names a document may have. Temp files, the write probe, links,
// directories and anything else are left out.
func (s *Store) List() ([]Entry, error) {
	des, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, de := range des {
		if !de.Type().IsRegular() || !namePattern.MatchString(de.Name()) {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue // removed meanwhile
		}
		out = append(out, Entry{Name: de.Name(), Size: fi.Size(), ModTime: fi.ModTime()})
	}
	return out, nil
}

// Delete removes the document called name. One that is not there is no error.
func (s *Store) Delete(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("store: bad name %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// LoadLimit is Load for a document of at most limit bytes. A larger one is an
// error that says so, and is not read past the limit. Only a regular file is
// read: a symbolic link, which List leaves out too, is refused rather than
// followed (Lstat), and so is anything else in the document's place.
func (s *Store) LoadLimit(name string, v any, limit int64) (bool, error) {
	if !namePattern.MatchString(name) {
		return false, fmt.Errorf("store: bad name %q", name)
	}
	path := filepath.Join(s.dir, name)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		what := "not a regular file"
		if fi.Mode()&os.ModeSymlink != 0 {
			what = "a symbolic link, which is not followed"
		}
		return false, fmt.Errorf("store: %s is %s", name, what)
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	// The file opened is the one checked: a link put in its place meanwhile
	// is refused too.
	if ofi, err := f.Stat(); err != nil || !os.SameFile(fi, ofi) {
		return false, fmt.Errorf("store: %s changed while it was opened", name)
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return false, err
	}
	if int64(len(b)) > limit {
		return false, fmt.Errorf("store: %s is more than %d bytes", name, limit)
	}
	if err := DecodeStrict(b, v); err != nil {
		return false, fmt.Errorf("store: parse %s: %w", name, err)
	}
	return true, nil
}
```

Update the package comment: "a directory of JSON documents, which may hold directories of its own (Sub)".

Run: `go test ./internal/store/ -count=1 -race`
Expected: PASS.

- [ ] **Step 3: Write the failing crew-store tests**

Create `internal/crew/store_test.go`:

```go
package crew

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/store"
)

// crewJSON is a crew with no members as crews.json of an older Conductor holds it.
func crewJSON(id, name string) string {
	return `{"id": "` + id + `", "name": "` + name + `", "where": "server", "isolation": "none", "members": []}`
}

// files returns the names in the crews directory of the data directory st.
func files(t *testing.T, st *store.Store) []string {
	t.Helper()
	des, err := os.ReadDir(filepath.Join(st.Dir(), "crews"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, de := range des {
		out = append(out, de.Name())
	}
	return out
}

func ids(sums []Summary) []string {
	var out []string
	for _, s := range sums {
		out = append(out, s.ID)
	}
	return out
}

// Each crew is a file of its own, crews/<id>.json, as the store writes every
// document; a delete removes it.
func TestEachCrewIsAFile(t *testing.T) {
	s, st := newStore(t)
	for _, c := range []Crew{validCrew("zeta", "Zeta"), validCrew("alpha", "alpha")} {
		if err := s.Put(c); err != nil {
			t.Fatal(err)
		}
	}
	if got := files(t, st); !slices.Equal(got, []string{"alpha.json", "zeta.json"}) {
		t.Fatalf("files %v", got)
	}
	want, _ := store.Encode(validCrew("zeta", "Zeta"))
	if b, err := os.ReadFile(filepath.Join(st.Dir(), "crews", "zeta.json")); err != nil || string(b) != string(want) {
		t.Fatalf("zeta.json: %v\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(st.Dir(), "crews.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("crews.json: %v", err)
	}
	if ok, err := s.Delete("zeta"); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if got := files(t, st); !slices.Equal(got, []string{"alpha.json"}) {
		t.Fatalf("after delete %v", got)
	}
	if ok, err := s.Delete("zeta"); ok || err != nil {
		t.Fatalf("second delete: %v %v", ok, err)
	}
	if ok, err := s.Delete("../x"); ok || err != nil {
		t.Fatalf("a bad id: %v %v", ok, err)
	}
}

// The list is read from the directory: by name ignoring case, then ID, a page
// at a time, with the total. A summary carries what the list shows and not
// the prompts. A file added, changed or removed by hand shows at the next
// listing.
func TestListPagesThroughTheDirectory(t *testing.T) {
	s, st := newStore(t)
	for i := range 7 {
		if err := s.Put(validCrew(fmt.Sprintf("c%d", i), fmt.Sprintf("Crew %d", 6-i))); err != nil {
			t.Fatal(err)
		}
	}
	page, total, err := s.List(0, 3)
	if err != nil || total != 7 || !slices.Equal(ids(page), []string{"c6", "c5", "c4"}) {
		t.Fatalf("first page %v of %d: %v", ids(page), total, err)
	}
	if page, total, _ = s.List(6, 3); total != 7 || !slices.Equal(ids(page), []string{"c0"}) {
		t.Fatalf("last page %v of %d", ids(page), total)
	}
	if page, total, _ = s.List(9, 3); total != 7 || len(page) != 0 {
		t.Fatalf("past the end %v of %d", ids(page), total)
	}
	page, _, _ = s.List(0, 1)
	want := Summary{ID: "c6", Name: "Crew 0", Cwd: "/srv/api", Where: "server", Isolation: "worktree",
		Members: []MemberSummary{{Name: "lead", AgentID: "claude"}, {Name: "tests", AgentID: "shell"}}, UpdatedAt: validCrew("", "").UpdatedAt}
	if !reflect.DeepEqual(page[0], want) {
		t.Fatalf("summary %+v, want %+v", page[0], want)
	}
	if b, _ := json.Marshal(page[0]); strings.Contains(string(b), "prompt") {
		t.Fatalf("a summary carries the prompts: %s", b)
	}
	dir := filepath.Join(st.Dir(), "crews")
	for id, name := range map[string]string{"hand": "AAA by hand", "c0": "ZZZ renamed by hand"} {
		b, _ := store.Encode(validCrew(id, name))
		if err := os.WriteFile(filepath.Join(dir, id+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(dir, "c3.json")); err != nil {
		t.Fatal(err)
	}
	page, total, _ = s.List(0, 10)
	if got := ids(page); total != 7 || got[0] != "hand" || got[6] != "c0" || slices.Contains(got, "c3") {
		t.Fatalf("after hand edits %v of %d", got, total)
	}
}

// A crew file that cannot be used is named at startup, left out of the list,
// and never overwritten: a new crew of the same name takes another ID, an
// update is refused, a delete removes it. The other crews load.
func TestACorruptCrewFileIsSkippedAndNeverOverwritten(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(st.Dir(), "crews")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	good, _ := store.Encode(validCrew("good", "Good"))
	for name, body := range map[string]string{
		"good.json":    string(good),
		"broken.json":  "{oops",
		"renamed.json": strings.Replace(string(good), `"id": "good"`, `"id": "other"`, 1),
		"huge.json":    `{"id": "huge", "name": "` + strings.Repeat("x", MaxEncoded) + `"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, problems, err := NewStore(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"broken.json", "renamed.json", "huge.json"} {
		if !slices.ContainsFunc(problems, func(p error) bool { return strings.Contains(p.Error(), filepath.Join(dir, name)) }) {
			t.Errorf("no problem names %s: %v", name, problems)
		}
	}
	if len(problems) != 3 {
		t.Fatalf("problems %v", problems)
	}
	if page, total, _ := s.List(0, 10); total != 1 || page[0].ID != "good" {
		t.Fatalf("list %v of %d", ids(page), total)
	}
	if _, err := s.Get("broken"); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("get: %v", err)
	}
	if c, err := s.Create(validCrew("", "Broken")); err != nil || c.ID != "broken-2" {
		t.Fatalf("create: %q %v", c.ID, err)
	}
	if _, err := s.Update("broken", validCrew("", "Fixed")); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("update: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "broken.json")); string(b) != "{oops" {
		t.Fatalf("broken.json was overwritten: %q", b)
	}
	if ok, err := s.Delete("broken"); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "broken.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("broken.json: %v", err)
	}
}

// crews.json of an older Conductor moves to one file per crew on the first
// start and is renamed crews.json.migrated; the next start changes nothing.
func TestMigrationSplitsCrewsJSONOnce(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "crews.json")
	legacy := `{"crews": [` + crewJSON("alpha", "Alpha") + `, ` + crewJSON("beta", "Beta") + `]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, problems, err := NewStore(st)
	if err != nil || len(problems) != 0 {
		t.Fatalf("NewStore: %v %v", problems, err)
	}
	if got := files(t, st); !slices.Equal(got, []string{"alpha.json", "beta.json"}) {
		t.Fatalf("files %v", got)
	}
	if b, err := os.ReadFile(path + ".migrated"); err != nil || string(b) != legacy {
		t.Fatalf("crews.json.migrated: %v %q", err, b)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("crews.json: %v", err)
	}
	if _, total, _ := s.List(0, 10); total != 2 {
		t.Fatalf("%d crews", total)
	}
	reopen(t, st)
	if _, err := os.Stat(path + ".migrated.2"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("migrated twice")
	}
}

// A start that stopped halfway through the move (some files written,
// crews.json still there) finishes it: every crew of crews.json, none twice,
// the crews made since kept, and an earlier crews.json.migrated kept too.
func TestMigrationFinishesAfterAnInterruptedStart(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "crews.json")
	legacy := `{"crews": [` + crewJSON("alpha", "Alpha") + `, ` + crewJSON("beta", "Beta") + `]}`
	for p, body := range map[string]string{path: legacy, path + ".migrated": "an earlier move"} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sub, err := st.Sub("crews")
	if err != nil {
		t.Fatal(err)
	}
	var alpha Crew
	if err := json.Unmarshal([]byte(crewJSON("alpha", "Alpha")), &alpha); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]Crew{"alpha.json": alpha.canonical(), "gamma.json": validCrew("gamma", "Gamma")} {
		if err := sub.Save(name, c); err != nil {
			t.Fatal(err)
		}
	}
	s := reopen(t, st)
	if got := files(t, st); !slices.Equal(got, []string{"alpha.json", "beta.json", "gamma.json"}) {
		t.Fatalf("files %v", got)
	}
	if _, total, _ := s.List(0, 10); total != 3 {
		t.Fatalf("%d crews", total)
	}
	for p, want := range map[string]string{path + ".migrated": "an earlier move", path + ".migrated.2": legacy} {
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Fatalf("%s: %v %q", p, err, b)
		}
	}
}

// crews.json that cannot be used stops startup, as it always did, naming its
// path, and nothing is moved: a store that started without it would lose it.
func TestMigrationRefusesAMalformedCrewsJSON(t *testing.T) {
	cases := []struct{ name, file, want string }{
		{"not JSON", `{oops`, "parse crews.json"},
		{"empty file", ``, "empty document"},
		{"unknown field", `{"crews": [], "bogus": true}`, `"bogus"`},
		{"invalid crew", `{"crews": [` + crewJSON("ok", "x") + `, {"id": "bad", "name": "x", "where": "server", "isolation": "none", "members": [{"name": "Lead!", "agentId": "a", "prompt": "", "start": {"when": "manual"}}]}]}`, "crews[1]"},
		{"invalid id", `{"crews": [` + crewJSON("Bad Id", "x") + `]}`, "crews[0]"},
		{"id used twice", `{"crews": [` + crewJSON("x", "x") + `, ` + crewJSON("x", "y") + `]}`, "crews[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(st.Dir(), "crews.json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			s, _, err := NewStore(st)
			if err == nil || s != nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewStore: %v, want an error naming %s and %q", err, path, tc.want)
			}
			if b, _ := os.ReadFile(path); string(b) != tc.file {
				t.Fatalf("crews.json changed: %q", b)
			}
			if entries, _ := os.ReadDir(filepath.Join(st.Dir(), "crews")); len(entries) != 0 {
				t.Fatalf("moved %v", entries)
			}
		})
	}
}

// There is no limit on the number of crews.
func TestStoreHoldsMoreThan50Crews(t *testing.T) {
	s, _ := newStore(t)
	for i := range 60 {
		if _, err := s.Create(validCrew("", fmt.Sprintf("Crew %02d", i))); err != nil {
			t.Fatalf("crew %d: %v", i, err)
		}
	}
	if _, err := s.Duplicate("crew-00"); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s.List(0, 1); total != 61 {
		t.Fatalf("%d crews", total)
	}
}

// The move never overwrites a crew file. A crew of crews.json whose file
// exists and holds something else (a crew made or edited since) is not
// moved: a notice names both files, the file stays as it was, and
// crews.json.migrated keeps the crew whole. The next start has nothing to
// say.
func TestMigrationNeverOverwritesACrewFile(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(st.Dir(), "crews.json")
	legacy := `{"crews": [` + crewJSON("alpha", "Alpha from crews.json") + `, ` + crewJSON("beta", "Beta") + `]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	sub, err := st.Sub("crews")
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Save("alpha.json", validCrew("alpha", "Alpha made since")); err != nil {
		t.Fatal(err)
	}
	alphaFile := filepath.Join(sub.Dir(), "alpha.json")
	before, err := os.ReadFile(alphaFile)
	if err != nil {
		t.Fatal(err)
	}
	s, problems, err := NewStore(st)
	if err != nil || len(problems) != 1 {
		t.Fatalf("NewStore: %v %v", problems, err)
	}
	for _, want := range []string{path, alphaFile, path + ".migrated"} {
		if !strings.Contains(problems[0].Error(), want) {
			t.Errorf("notice %q does not name %s", problems[0], want)
		}
	}
	if b, _ := os.ReadFile(alphaFile); string(b) != string(before) {
		t.Fatalf("alpha.json was overwritten:\n%s", b)
	}
	if c, err := s.Get("alpha"); err != nil || c.Name != "Alpha made since" {
		t.Fatalf("alpha: %q %v", c.Name, err)
	}
	if c, err := s.Get("beta"); err != nil || c.Name != "Beta" {
		t.Fatalf("beta: %q %v", c.Name, err)
	}
	if b, err := os.ReadFile(path + ".migrated"); err != nil || string(b) != legacy {
		t.Fatalf("crews.json.migrated: %v %q", err, b)
	}
	reopen(t, st)
}

// A crew file that is a symbolic link is never followed: Get and Update
// refuse it as unusable, the list leaves it out, a new crew does not take its
// ID, and a delete removes the link and not what it points to.
func TestALinkedCrewFileIsNotFollowed(t *testing.T) {
	s, st := newStore(t)
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	b, _ := store.Encode(validCrew("linked", "Linked"))
	if err := os.WriteFile(target, b, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(st.Dir(), "crews", "linked.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("linked"); !errors.Is(err, ErrUnreadable) || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("get: %v", err)
	}
	if _, err := s.Update("linked", validCrew("", "Changed")); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("update: %v", err)
	}
	if _, total, _ := s.List(0, 10); total != 0 {
		t.Fatalf("%d crews listed", total)
	}
	if c, err := s.Create(validCrew("", "Linked")); err != nil || c.ID != "linked-2" {
		t.Fatalf("create: %q %v", c.ID, err)
	}
	if ok, err := s.Delete("linked"); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the link: %v", err)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != string(b) {
		t.Fatalf("the file the link named: %v %q", err, got)
	}
}

// A save is listed at once, even when the file keeps its size and its time
// (a rename to a name of the same length within the clock's tick): commit
// drops the summary the list cached for the file, and Delete does too.
func TestASaveIsListedAtOnce(t *testing.T) {
	s, st := newStore(t)
	path := filepath.Join(st.Dir(), "crews", "c.json")
	if err := s.Put(validCrew("c", "Alpha")); err != nil {
		t.Fatal(err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	name := func() string {
		t.Helper()
		page, _, err := s.List(0, 1)
		if err != nil || len(page) != 1 {
			t.Fatalf("list: %v %v", page, err)
		}
		return page[0].Name
	}
	// saveAs puts the crew c under another name of five letters and gives
	// its file the first save's time back.
	saveAs := func(n string) {
		t.Helper()
		if err := s.Put(validCrew("c", n)); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, first.ModTime(), first.ModTime()); err != nil {
			t.Fatal(err)
		}
		if fi, _ := os.Stat(path); fi.Size() != first.Size() {
			t.Fatalf("sizes %d and %d", fi.Size(), first.Size())
		}
	}
	if got := name(); got != "Alpha" {
		t.Fatalf("listed %q", got)
	}
	saveAs("Bravo")
	if got := name(); got != "Bravo" {
		t.Fatalf("listed %q after the save", got)
	}
	// A file put back by hand after a delete, with the same size and time,
	// is read again: Delete dropped the summary too.
	if ok, err := s.Delete("c"); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	b, _ := store.Encode(validCrew("c", "Carol"))
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, first.ModTime(), first.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := name(); got != "Carol" {
		t.Fatalf("listed %q after a delete and a file put back by hand", got)
	}
}
```

In `internal/crew/crew_test.go`, apply these edits. `NewStore` now returns three values, `List` takes a page and returns summaries, and `Get` returns an error in place of a found flag; every test that used them changes as shown.

`newStore` keeps its two results, the crew store and the data directory's store, and opens the crew store with `reopen`. Two helpers follow it:

```go
func newStore(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return reopen(t, st), st
}

// reopen is NewStore over st, as a restart does, requiring no problem.
func reopen(t *testing.T, st *store.Store) *Store {
	t.Helper()
	s, problems, err := NewStore(st)
	if err != nil || len(problems) != 0 {
		t.Fatalf("NewStore: %v %v", problems, err)
	}
	return s
}

// all returns every crew of s in full, in list order.
func all(t *testing.T, s *Store) []Crew {
	t.Helper()
	sums, total, err := s.List(0, 500)
	if err != nil || total != len(sums) {
		t.Fatalf("list: %d of %d, %v", len(sums), total, err)
	}
	out := []Crew{}
	for _, sum := range sums {
		c, err := s.Get(sum.ID)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}
```

`encodedSize` measures the file as the store writes it, and `crewOfEncodedSize` uses `\x01`, which JSON writes as six bytes (`\u0001`) whatever the HTML escaping. Both replace the old ones whole:

```go
// encodedSize is the length of c's file, as the store writes it.
func encodedSize(t *testing.T, c Crew) int {
	t.Helper()
	b, err := store.Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}

// crewOfEncodedSize returns a valid crew whose file is exactly size bytes:
// twelve members with prompts and args of "\x01", which JSON writes as six
// bytes (\u0001), and a goal of "g" making up the rest.
func crewOfEncodedSize(t *testing.T, id string, size int) Crew {
	t.Helper()
	c := validCrew(id, "Edge")
	c.Goal = ""
	c.Members = nil
	for i := range 12 {
		c.Members = append(c.Members, Member{Name: fmt.Sprintf("m%02d", i), AgentID: "shell", Prompt: strings.Repeat("\x01", 4000), Args: []string{""}, Start: Start{When: "manual"}})
	}
	lt := (size - encodedSize(t, c) - 1000) / 6 // leave about 1000 bytes to the goal
	for i := range c.Members {
		n := lt / 12
		if i == 0 {
			n += lt % 12
		}
		c.Members[i].Args[0] = strings.Repeat("\x01", n)
	}
	c.Goal = strings.Repeat("g", size-encodedSize(t, c))
	if got := encodedSize(t, c); got != size || c.Validate() != nil {
		t.Fatalf("crew of %d bytes: %v", got, c.Validate())
	}
	return c
}
```

No valid crew reaches `MaxEncoded` (twelve members at every limit, written with six-byte escapes, come to about 900 KB), so the two tests at the bound lower it for their duration. Under `MaxEncoded`, `Validate`'s check is a guarantee that every file the store writes reads back. `TestValidateAcceptsCrewsAtTheLimits` ends, in place of its "As JSON, up to 512 KiB" lines:

```go
	// As its file, up to maxEncoded, lowered here to a size a valid crew reaches.
	old := maxEncoded
	maxEncoded = 300 << 10
	t.Cleanup(func() { maxEncoded = old })
	if err := crewOfEncodedSize(t, "edge", 300<<10).Validate(); err != nil {
		t.Fatal(err)
	}
}
```

`TestDuplicateRefusesACopyOverTheEncodedLimit` becomes:

```go
// The store checks a crew as it saves it, ID and times included, so a crew at
// the encoded limit is not copied into one past it: the store never writes a
// file a start cannot read.
func TestDuplicateRefusesACopyOverTheEncodedLimit(t *testing.T) {
	old := maxEncoded
	maxEncoded = 300 << 10
	t.Cleanup(func() { maxEncoded = old })
	s, st := newStore(t)
	edge := crewOfEncodedSize(t, "edge", 300<<10-9) // a copy is 10 bytes longer: " copy" and "-copy"
	if err := s.Put(edge); err != nil {
		t.Fatal(err)
	}
	clock(s, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)) // times as long as edge's
	if _, err := s.Duplicate("edge"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "as its file") {
		t.Fatalf("duplicate: %v", err)
	}
	if n := len(all(t, s)); n != 1 {
		t.Fatalf("%d crews", n)
	}
	reopen(t, st)
}
```

Delete `TestStoreRoundTrip` (replaced by `TestEachCrewIsAFile` and `TestListPagesThroughTheDirectory`), `TestStoreHoldsAtMost50Crews` (replaced by `TestStoreHoldsMoreThan50Crews`) and `TestNewStoreRefusesAMalformedFile` (replaced by `TestMigrationRefusesAMalformedCrewsJSON`).

`TestStoreReturnsCopies` checks the summaries the list returns as well as the crew `Get` returns:

```go
func TestStoreReturnsCopies(t *testing.T) {
	s, _ := newStore(t)
	c := validCrew("api", "API")
	if err := s.Put(c); err != nil {
		t.Fatal(err)
	}
	c.Members[0].Prompt = "changed after Put"
	c.Members[1].Args[0] = "changed after Put"
	got, err := s.Get("api")
	list, _, _ := s.List(0, 10)
	if err != nil || len(got.Members) != 2 || len(list) != 1 || len(list[0].Members) != 2 {
		t.Fatalf("stored: %v %+v %+v", err, got, list)
	}
	got.Members[0].Prompt = "changed after Get"
	got.Members[1].Args[0] = "changed after Get"
	list[0].Members[1].Name = "changed after List"
	list[0].Members = append(list[0].Members[:1], MemberSummary{Name: "extra"})
	if got, _ := s.Get("api"); !reflect.DeepEqual(got, validCrew("api", "API")) {
		t.Fatalf("the stored crew changed: %+v", got)
	}
	want := []MemberSummary{{Name: "lead", AgentID: "claude"}, {Name: "tests", AgentID: "shell"}}
	if again, _, _ := s.List(0, 10); !reflect.DeepEqual(again[0].Members, want) {
		t.Fatalf("the cached summary changed: %+v", again[0].Members)
	}
}
```

In `TestCreateDerivesUniqueIDs`, the lines from `again, err := NewStore(st)` to the end of the function become:

```go
	if n := len(all(t, reopen(t, st))); n != len(cases) {
		t.Fatalf("reloaded %d crews, want %d", n, len(cases))
	}
}
```

In `TestCreateConcurrentlyGivesDistinctIDs`, the last check becomes:

```go
	if n := len(all(t, s)); !slices.Equal(got, want) || n != writers {
		t.Fatalf("ids %v, %d crews", got, n)
	}
}
```

In `TestUpdateKeepsTheIDAndCreationTime`, the lines from `again, err := NewStore(st)` to the end become:

```go
	if list := all(t, reopen(t, st)); !reflect.DeepEqual(list, []Crew{want}) {
		t.Fatalf("stored: %+v", list)
	}
}
```

In `TestStoreRefusesInvalidCrews`, the lines from `if got, _ := s.Get("ok")` to the end become:

```go
	if got, _ := s.Get("ok"); !reflect.DeepEqual(got, validCrew("ok", "x")) || len(all(t, s)) != 1 {
		t.Fatalf("an invalid crew changed the store: %+v", all(t, s))
	}
	if list := all(t, reopen(t, st)); len(list) != 1 || list[0].ID != "ok" {
		t.Fatalf("stored: %+v", list)
	}
}
```

`TestNewStoreListsNoMembersAsEmpty` reads a crew that `crews.json` holds without members after the move:

```go
// A crew that went into crews.json without members, by hand, is moved and
// read with members [] as every saved crew is, never null, and listed so.
func TestNewStoreListsNoMembersAsEmpty(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := `{"crews": [{"id": "draft", "name": "Draft", "where": "server", "isolation": "none"}]}`
	if err := os.WriteFile(filepath.Join(st.Dir(), "crews.json"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	s := reopen(t, st)
	c, err := s.Get("draft")
	if err != nil {
		t.Fatalf("draft not loaded: %v", err)
	}
	if b, _ := json.Marshal(c); !strings.Contains(string(b), `"members":[]`) {
		t.Fatalf("read as %s", b)
	}
	if page, _, _ := s.List(0, 1); len(page) != 1 || page[0].Members == nil {
		t.Fatalf("listed as %+v", page)
	}
}
```

In `TestStoreTrimsTheName`, the lines from `if err := os.WriteFile(filepath.Join(st.Dir(), "crews.json")` to the end become (a name with spaces is trimmed whether it comes from `crews.json` or from a crew file written by hand):

```go
	if err := os.WriteFile(filepath.Join(st.Dir(), "crews.json"), []byte(`{"crews": [{"id": "hand", "name": "  Hand  ", "where": "server", "isolation": "none", "members": []}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	again := reopen(t, st)
	if got, err := again.Get("hand"); err != nil || got.Name != "Hand" {
		t.Fatalf("moved: %q %v", got.Name, err)
	}
	b, _ := store.Encode(validCrew("byhand", "  By hand  "))
	if err := os.WriteFile(filepath.Join(st.Dir(), "crews", "byhand.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := again.Get("byhand"); err != nil || got.Name != "By hand" {
		t.Fatalf("by hand: %q %v", got.Name, err)
	}
}
```

`TestFailedSaveChangesNothing` makes the crews directory read-only:

```go
// A save that fails changes nothing: the files stay as they were and the
// list with them.
func TestFailedSaveChangesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a directory of mode 0500")
	}
	s, st := newStore(t)
	if err := s.Put(validCrew("kept", "Kept")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(st.Dir(), "crews")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := s.Put(validCrew("lost", "Lost")); !errors.Is(err, ErrWrite) {
		t.Errorf("put: %v", err)
	}
	if _, err := s.Create(validCrew("", "Lost")); !errors.Is(err, ErrWrite) {
		t.Errorf("create: %v", err)
	}
	if _, err := s.Update("kept", validCrew("", "Changed")); !errors.Is(err, ErrWrite) {
		t.Errorf("update: %v", err)
	}
	if _, err := s.Duplicate("kept"); !errors.Is(err, ErrWrite) {
		t.Errorf("duplicate: %v", err)
	}
	if ok, err := s.Delete("kept"); ok || !errors.Is(err, ErrWrite) {
		t.Errorf("delete: %v %v", ok, err)
	}
	if got := all(t, s); !reflect.DeepEqual(got, []Crew{validCrew("kept", "Kept")}) {
		t.Fatalf("a failed save changed the crews: %+v", got)
	}
}
```

`TestDuplicateCopiesUnderACopyID` and the validation test that calls `st.Put` need no change: they read `Get` as `stored, _ :=` and `got, _ :=`, which compile with the error. `go vet ./internal/crew/` names any import the edits leave unused.

Run: `go test ./internal/crew/ -count=1`
Expected: build failure, because `NewStore` returns two values and `Summary`, `MemberSummary`, `ErrUnreadable`, `ErrWrite`, `MaxEncoded` and `maxEncoded` are undefined.

- [ ] **Step 4: Rewrite the crew store**

In `internal/crew/crew.go`, replace the `maxEncoded` constant and the size check:

```go
// MaxEncoded bounds a crew: its file, as the store writes it (store.Encode).
// Validate holds every crew to it, so the crew a create or an update sends is
// held to it too; the request body itself may be larger (the API takes up to
// twice this, since a client may indent the crew). It is a sanity bound, not
// a quota: the limits above keep a valid crew well under it, and there is no
// limit on the number of crews.
const MaxEncoded = 1 << 20

// maxEncoded is the bound Validate holds a crew to, MaxEncoded but for tests,
// which lower it to reach it.
var maxEncoded = MaxEncoded
```

```go
	b, err := store.Encode(c)
	if err != nil {
		return invalidf("cannot be written as JSON: %v", err)
	}
	if len(b) > maxEncoded {
		return invalidf("the crew is %d bytes as its file, more than %d", len(b), maxEncoded)
	}
```

(`crew.go` imports `internal/store` and drops `encoding/json` if nothing else uses it.) Change the package comment's "Crews persist as crews.json in the data directory" to "Crews persist one file each, crews/<id>.json in the data directory (store.go)". Update the `Limits enforced by Validate` comment: there is no crew-count limit.

Replace `internal/crew/store.go` with:

```go
package crew

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/store"
)

// crewsDir is the directory of the data directory that holds one file per
// crew, <id>.json.
const crewsDir = "crews"

// maxID bounds an ID. Derived IDs are at most 40 characters from the name
// plus a suffix; duplicates of duplicates are cut to stay within it.
const maxID = 64

// idPattern is what a crew ID looks like: a derived ID always matches it, and
// a hand-edited file is held to it. With ".json" it is a store document name.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Errors a Store returns besides validation and save errors.
var (
	ErrNotFound = errors.New("no such crew")
	// ErrUnreadable is a crew whose file cannot be used: not JSON, a field a
	// crew does not have, an invalid crew, another crew's ID, more than
	// MaxEncoded bytes, a symbolic link. The error that wraps it says why and
	// names the file, not the directory. Such a file is left alone: never
	// listed, never overwritten, and deleted only on request.
	ErrUnreadable = errors.New("the crew's file cannot be used")
	// ErrWrite wraps a save or a delete that failed: the file is as it was.
	// A failure to read the crews directory or a file does not wrap it, so
	// the API can say which of the two failed.
	ErrWrite = errors.New("the crew's file could not be written")
)

// Summary is a crew as GET /api/crews lists it: what the Crews page's list
// and conductor crews show, without the prompts.
type Summary struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Cwd       string          `json:"cwd"`
	Where     string          `json:"where"`
	Isolation string          `json:"isolation"`
	Members   []MemberSummary `json:"members"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// MemberSummary is a member as a Summary lists it.
type MemberSummary struct {
	Name    string `json:"name"`
	AgentID string `json:"agentId"`
}

func (c Crew) summary() Summary {
	members := make([]MemberSummary, 0, len(c.Members))
	for _, m := range c.Members {
		members = append(members, MemberSummary{Name: m.Name, AgentID: m.AgentID})
	}
	return Summary{ID: c.ID, Name: c.Name, Cwd: c.Cwd, Where: c.Where, Isolation: c.Isolation, Members: members, UpdatedAt: c.UpdatedAt}
}

// Store keeps the saved crews, one file each in crews/ of the data directory,
// each written whole by the store. The list is read from the directory, so a
// file added, changed or removed by hand shows at the next listing. Every
// change is checked in the form it is saved in (name trimmed, ID and times
// set), and a failed save changes nothing. Crews go in and come out as copies.
type Store struct {
	st  *store.Store // crews/
	now func() time.Time

	// mu serialises the writers, so that a derived ID is chosen and taken in
	// one step, and guards sums.
	mu sync.Mutex
	// sums caches each file's summary by document name, with the size and
	// time the file had when it was read: one whose size or time changed is
	// read again. commit and Delete drop the entry of the file they change,
	// so a save that keeps the size and the time (a rename to a name of the
	// same length within the clock's tick) is listed at once; a hand edit
	// that keeps both shows once either changes.
	sums map[string]cached
}

type cached struct {
	size int64
	mod  time.Time
	sum  Summary
	err  error // the file cannot be used: it is left out of the list
}

// NewStore opens the crews of the data directory st: crews/ in it, made 0700
// when missing, after the crews.json of an older Conductor is moved there
// (migrate). It reads every crew file once. problems lists, each naming its
// files, what was left as it was: a crew of crews.json that was not moved
// because its file exists and holds something else (crews.json.migrated
// keeps it), and a crew file that cannot be used (left out; the other crews
// load). err is fatal: a crews.json that cannot be moved, or a crews
// directory that cannot be made or read.
func NewStore(st *store.Store) (s *Store, problems []error, err error) {
	dir, err := st.Sub(crewsDir)
	if err != nil {
		return nil, nil, err
	}
	notices, err := migrate(st, dir)
	if err != nil {
		return nil, nil, err
	}
	s = &Store{st: dir, now: func() time.Time { return time.Now().UTC() }, sums: map[string]cached{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.st.List()
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", dir.Dir(), err)
	}
	problems = notices
	for _, e := range entries {
		if c := s.summaryOf(e); c.err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", filepath.Join(dir.Dir(), e.Name), c.err))
		}
	}
	return s, problems, nil
}

// summaryOf returns the cached summary of the file e, read again when its size
// or time changed. The caller holds s.mu.
func (s *Store) summaryOf(e store.Entry) cached {
	if c, ok := s.sums[e.Name]; ok && c.size == e.Size && c.mod.Equal(e.ModTime) {
		return c
	}
	c := cached{size: e.Size, mod: e.ModTime}
	if crew, err := s.read(strings.TrimSuffix(e.Name, ".json")); err != nil {
		c.err = err
	} else {
		c.sum = crew.summary()
	}
	s.sums[e.Name] = c
	return c
}

// read loads the crew with the given ID from its file and checks it as the
// store saves it: a regular file (not a link), strict JSON of at most
// MaxEncoded bytes, a valid crew, the ID its file is named for. ErrNotFound
// when there is no file, an error wrapping ErrUnreadable when it cannot be
// used. A failure of the file system itself (a *fs.PathError, which names
// the data directory) is neither: it is a read failure.
func (s *Store) read(id string) (Crew, error) {
	if !idPattern.MatchString(id) {
		return Crew{}, ErrNotFound
	}
	var c Crew
	ok, err := s.st.LoadLimit(id+".json", &c, MaxEncoded)
	var pe *fs.PathError
	switch {
	case errors.As(err, &pe):
		return Crew{}, fmt.Errorf("read %s.json: %w", id, err)
	case err != nil:
		return Crew{}, fmt.Errorf("%w: %w", ErrUnreadable, err)
	case !ok:
		return Crew{}, ErrNotFound
	}
	c = c.canonical()
	if c.ID != id {
		return Crew{}, fmt.Errorf("%w: it holds the id %s", ErrUnreadable, quote(c.ID))
	}
	if err := c.validateWithID(); err != nil {
		return Crew{}, fmt.Errorf("%w: %w", ErrUnreadable, err)
	}
	return c, nil
}

// List returns the summaries of the crews ordered by name, ignoring case,
// then by ID: those from offset on, at most limit of them, and how many there
// are in all. The directory is read each time. A file that cannot be used is
// left out and counts in no total.
func (s *Store) List(offset, limit int) ([]Summary, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.st.List()
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[string]bool, len(entries))
	all := make([]Summary, 0, len(entries))
	for _, e := range entries {
		seen[e.Name] = true
		if c := s.summaryOf(e); c.err == nil {
			all = append(all, c.sum)
		}
	}
	for name := range s.sums {
		if !seen[name] {
			delete(s.sums, name)
		}
	}
	slices.SortFunc(all, func(a, b Summary) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
	})
	total := len(all)
	from := min(max(offset, 0), total)
	to := min(from+max(limit, 0), total)
	page := slices.Clone(all[from:to])
	for i := range page {
		page[i].Members = slices.Clone(page[i].Members)
	}
	return page, total, nil
}

// Get returns the crew with the given ID, read from its file: ErrNotFound when
// there is none, an error wrapping ErrUnreadable when it cannot be used, a
// symbolic link included (store.LoadLimit does not follow one).
func (s *Store) Get(id string) (Crew, error) {
	return s.read(id)
}

// Put validates c and saves it under c.ID, adding it or replacing the crew
// with that ID. It stores c as it is, times included, only its name trimmed;
// Create, Update and Duplicate are the operations that set the ID and the
// times.
func (s *Store) Put(c Crew) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commit(c)
}

// Create saves c as a new crew. Its ID comes from its name, lower case with
// every other run of characters a dash and at most 40 of them ("crew" when
// nothing is left), then -2, -3 and so on when a file has it already, usable
// or not. CreatedAt and UpdatedAt are now. The ID and times c holds are
// ignored.
func (s *Store) Create(c Crew) (Crew, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	taken, err := s.taken()
	if err != nil {
		return Crew{}, err
	}
	c.ID = freeID(taken, cmp.Or(slug(c.Name), "crew"), "")
	c.CreatedAt = s.now()
	c.UpdatedAt = c.CreatedAt
	if err := s.commit(c); err != nil {
		return Crew{}, err
	}
	return c.canonical(), nil
}

// Update replaces the crew with the given ID by c. The ID and CreatedAt stay
// as they were, whatever c holds, and UpdatedAt is now. A file that cannot be
// used is not replaced (ErrUnreadable).
func (s *Store) Update(id string, c Crew) (Crew, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.read(id)
	if err != nil {
		return Crew{}, err
	}
	c.ID, c.CreatedAt, c.UpdatedAt = id, old.CreatedAt, s.now()
	if err := s.commit(c); err != nil {
		return Crew{}, err
	}
	return c.canonical(), nil
}

// Duplicate saves a copy of the crew with the given ID as a new crew: its ID
// is <id>-copy (then <id>-copy-2 and so on), its name "<name> copy", and its
// CreatedAt and UpdatedAt now.
func (s *Store) Duplicate(id string) (Crew, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, err := s.read(id)
	if err != nil {
		return Crew{}, err
	}
	taken, err := s.taken()
	if err != nil {
		return Crew{}, err
	}
	c := src.clone()
	c.ID = freeID(taken, id, "-copy")
	c.Name = copyName(src.Name)
	c.CreatedAt = s.now()
	c.UpdatedAt = c.CreatedAt
	if err := s.commit(c); err != nil {
		return Crew{}, err
	}
	return c.canonical(), nil
}

// Delete removes the crew with the given ID, its file whatever it holds, and
// reports whether there was one.
func (s *Store) Delete(id string) (bool, error) {
	if !idPattern.MatchString(id) {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	taken, err := s.taken()
	if err != nil {
		return false, err
	}
	if !taken[id] {
		return false, nil
	}
	if err := s.st.Delete(id + ".json"); err != nil {
		return false, fmt.Errorf("%w: delete %s.json: %w", ErrWrite, id, err)
	}
	delete(s.sums, id+".json")
	return true, nil
}

// commit checks c as it will be saved, then writes its file and drops the
// summary the list cached for it. An invalid crew is refused, and a failed
// write (ErrWrite) leaves the file as it was. The caller holds s.mu.
func (s *Store) commit(c Crew) error {
	c = c.canonical()
	if err := c.validateWithID(); err != nil {
		return err
	}
	if err := s.st.Save(c.ID+".json", c); err != nil {
		return fmt.Errorf("%w: save %s.json: %w", ErrWrite, c.ID, err)
	}
	delete(s.sums, c.ID+".json")
	return nil
}

// taken returns the IDs that have an entry in the crews directory, usable or
// not, a link included, so that no new crew takes the name of a file the
// store cannot read and Delete removes such a file. It reads the directory
// itself, since store.List leaves links out. The caller holds s.mu.
func (s *Store) taken() (map[string]bool, error) {
	des, err := os.ReadDir(s.st.Dir())
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(des))
	for _, de := range des {
		if id, ok := strings.CutSuffix(de.Name(), ".json"); ok {
			out[id] = true
		}
	}
	return out, nil
}

// freeID returns stem+tag, or stem+tag+"-2", stem+tag+"-3" and so on, the
// first that taken does not hold, with stem cut so that the ID stays within
// maxID. stem starts with a letter or digit, as slugs and IDs do.
func freeID(taken map[string]bool, stem, tag string) string {
	for n := 1; ; n++ {
		end := tag
		if n > 1 {
			end += "-" + strconv.Itoa(n)
		}
		id := strings.TrimRight(stem[:min(len(stem), maxID-len(end))], "-") + end
		if !taken[id] {
			return id
		}
	}
}
```

Keep `validateWithID`, `canonical`, `slug` (and `maxSlug`) and `copyName` from the old `store.go` below this, unchanged.

The summary cache (`sums`) stays: a listing reads again only the files whose size or time changed, which keeps a page of a large directory cheap. `commit` and `Delete` drop the entry of the file they change, so the cache never shows a save of this server late (`TestASaveIsListedAtOnce`); `List` drops the entries of files that are gone.

Create `internal/crew/migrate.go`:

```go
package crew

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/phenixrizen/conductor/internal/store"
)

// legacyFile is the file an older Conductor kept every crew in.
const legacyFile = "crews.json"

// legacyDoc is the shape of crews.json.
type legacyDoc struct {
	Crews []Crew `json:"crews"`
}

// migrate moves the crews of crews.json in the data directory st, if there is
// one, to a file each in dir, then renames crews.json to crews.json.migrated
// (or .migrated.2 and on, when that is taken), so the move happens once and
// the renamed file keeps every crew as crews.json had it. It never overwrites
// a crew file. One that holds exactly what the move would write is the first
// half of a move that stopped, and is left as it is. One that holds anything
// else (a crew made or edited since, a link) is kept too, and notices says
// so, naming both files: that crew stays in crews.json.migrated only.
// crews.json is held to what it always was: one that cannot be parsed, or
// that holds an invalid crew or an ID twice, stops startup with an error
// naming it, and nothing is moved.
func migrate(st, dir *store.Store) (notices []error, err error) {
	path := filepath.Join(st.Dir(), legacyFile)
	var doc legacyDoc
	found, err := st.Load(legacyFile, &doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !found {
		return nil, nil
	}
	seen := make(map[string]bool, len(doc.Crews))
	for i, c := range doc.Crews {
		c = c.canonical()
		if err := c.validateWithID(); err != nil {
			return nil, fmt.Errorf("%s: crews[%d]: %w", path, i, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("%s: crews[%d]: id %q is used twice", path, i, c.ID)
		}
		seen[c.ID] = true
		doc.Crews[i] = c
	}
	migrated := freeName(path + ".migrated")
	for _, c := range doc.Crews {
		file := filepath.Join(dir.Dir(), c.ID+".json")
		want, err := store.Encode(c)
		if err != nil {
			return nil, fmt.Errorf("%s: crew %s: %w", path, c.ID, err)
		}
		fi, err := os.Lstat(file)
		switch {
		case err == nil:
			if !fi.Mode().IsRegular() || !holds(file, want) {
				notices = append(notices, fmt.Errorf("%s: crew %s is not moved: %s exists and is kept; %s keeps the crew as %s had it", path, quote(c.ID), file, migrated, legacyFile))
			}
			continue
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%s: move crew %s to %s: %w", path, c.ID, dir.Dir(), err)
		}
		if err := dir.Save(c.ID+".json", c); err != nil {
			return nil, fmt.Errorf("%s: move crew %s to %s: %w", path, c.ID, dir.Dir(), err)
		}
	}
	if err := os.Rename(path, migrated); err != nil {
		return nil, fmt.Errorf("%s: the crews are moved, but the file could not be renamed: %w", path, err)
	}
	return notices, nil
}

// holds reports whether the regular file p holds exactly want.
func holds(p string, want []byte) bool {
	b, err := os.ReadFile(p)
	return err == nil && bytes.Equal(b, want)
}

// freeName returns p, or p.2, p.3 and so on, the first that does not exist.
func freeName(p string) string {
	for n := 1; ; n++ {
		q := p
		if n > 1 {
			q = p + "." + strconv.Itoa(n)
		}
		if _, err := os.Lstat(q); errors.Is(err, fs.ErrNotExist) {
			return q
		}
	}
}
```

Run: `go test -race -count=1 ./internal/crew/`
Expected: PASS.

- [ ] **Step 5: Write the failing API tests**

In `internal/api/api_test.go`, the crew helpers change, and so does what lists them:

```go
// crews returns the summaries GET /api/crews lists on one page of 500, in
// order; total must count them.
func (e *testEnv) crews() []map[string]any {
	e.t.Helper()
	resp, out := e.do("GET", "/api/crews?limit=500", adminToken, nil)
	raw, ok := out["crews"].([]any)
	if resp.StatusCode != http.StatusOK || !ok || out["total"] != float64(len(raw)) {
		e.t.Fatalf("crews: %d %v", resp.StatusCode, out)
	}
	list := []map[string]any{}
	for _, c := range raw {
		list = append(list, c.(map[string]any))
	}
	return list
}

// crew returns the crew GET /api/crews/{id} answers in full.
func (e *testEnv) crew(id string) map[string]any {
	e.t.Helper()
	resp, out := e.do("GET", "/api/crews/"+id, adminToken, nil)
	c, _ := out["crew"].(map[string]any)
	if resp.StatusCode != http.StatusOK || c == nil {
		e.t.Fatalf("crew %s: %d %v", id, resp.StatusCode, out)
	}
	return c
}

// storedCrew returns the crew with the given ID as its file in the data
// directory holds it, or nil when there is none.
func (e *testEnv) storedCrew(id string) map[string]any {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.srv.store.Dir(), "crews", id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		e.t.Fatal(err)
	}
	var c map[string]any
	if err := json.Unmarshal(b, &c); err != nil {
		e.t.Fatalf("%s.json: %v %s", id, err, b)
	}
	return c
}
```

(`api_test.go` imports `"errors"` and `"io/fs"`.) In `TestCrewsCRUD`:

- the list check becomes `if list := e.crews(); len(list) != 2 || list[0]["id"] != "api-sweep" || list[1]["id"] != "api-sweep-2" || !reflect.DeepEqual(e.crew("api-sweep"), created)`;
- `crews.json holds %v` becomes `the file holds %v`;
- "deleted crew still in crews.json" becomes "deleted crew still has a file";
- the restart check compares `live` with `e.restart().crews()` as before, and checks `!reflect.DeepEqual(e.crew("api-sweep"), updated)` in place of `live[1]`;
- in the "Nobody but the admin" table, add `{"GET", "/api/crews/api-sweep", nil}`.

Replace `TestCrewRoutesStopAt50Crews` with:

```go
// There is no limit on the number of crews: the list pages through them, by
// name, with the total, and limit and offset are checked.
func TestCrewRoutesPageThroughMoreThan50Crews(t *testing.T) {
	e := newTestEnv(t, nil)
	for i := range 55 {
		e.sendCrew("POST", "/api/crews", e.crewBody(fmt.Sprintf("Crew %02d", i)), http.StatusCreated)
	}
	e.sendCrew("POST", "/api/crews/crew-00/duplicate", nil, http.StatusCreated)
	resp, out := e.do("GET", "/api/crews?offset=50&limit=4", adminToken, nil)
	page, _ := out["crews"].([]any)
	if resp.StatusCode != http.StatusOK || out["total"] != 56.0 || len(page) != 4 || page[0].(map[string]any)["id"] != "crew-49" {
		t.Fatalf("page: %d %v", resp.StatusCode, out)
	}
	if first := page[0].(map[string]any); first["members"] == nil || first["goal"] != nil || fmt.Sprint(first["members"]) != "[map[agentId:sh name:lead] map[agentId:cat name:tests]]" {
		t.Fatalf("a summary: %v", first)
	}
	if _, out := e.do("GET", "/api/crews", adminToken, nil); len(out["crews"].([]any)) != 56 || out["total"] != 56.0 {
		t.Fatalf("the default page of 100: %v", out["total"])
	}
	for _, q := range []string{"limit=0", "limit=501", "limit=x", "offset=-1", "offset=1.5"} {
		resp, out := e.do("GET", "/api/crews?"+q, adminToken, nil)
		wantAPIError(t, q, resp, out, http.StatusBadRequest, "invalid_request", "")
	}
}

// A crew file that cannot be used is left out of the list; reading or
// updating it answers 409 crew_unreadable with why, without the data
// directory's path; deleting it works.
func TestCrewRoutesAnswerForACorruptFile(t *testing.T) {
	e := newTestEnv(t, nil)
	e.sendCrew("POST", "/api/crews", e.crewBody("Good"), http.StatusCreated)
	if err := os.WriteFile(filepath.Join(e.srv.store.Dir(), "crews", "broken.json"), []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ids := e.crewIDs(); !slices.Equal(ids, []string{"good"}) {
		t.Fatalf("ids %v", ids)
	}
	for _, r := range []struct {
		method, path string
		body         any
	}{{"GET", "/api/crews/broken", nil}, {"PUT", "/api/crews/broken", e.crewBody("Fixed")}, {"POST", "/api/crews/broken/duplicate", nil}, {"POST", "/api/crews/broken/launch", nil}} {
		resp, out := e.do(r.method, r.path, adminToken, r.body)
		wantAPIError(t, r.method+" "+r.path, resp, out, http.StatusConflict, "crew_unreadable", "broken.json")
		if strings.Contains(fmt.Sprint(out), e.srv.store.Dir()) {
			t.Errorf("%s %s names the data directory: %v", r.method, r.path, out)
		}
	}
	if resp, _ := e.do("DELETE", "/api/crews/broken", adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	resp, out := e.do("GET", "/api/crews/nope", adminToken, nil)
	wantAPIError(t, "unknown", resp, out, http.StatusNotFound, "not_found", "")
}
```

`TestCrewFailedSaveChangesNothing` makes the crews directory read-only in place of removing the data directory:

```go
func TestCrewFailedSaveChangesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a directory of mode 0500")
	}
	e := newTestEnv(t, nil)
	e.sendCrew("POST", "/api/crews", e.crewBody("Kept"), http.StatusCreated)
	before, kept := e.crews(), e.crew("kept")
	dir := filepath.Join(e.srv.store.Dir(), "crews")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/crews", e.crewBody("Lost")},
		{"PUT", "/api/crews/kept", e.crewBody("Changed")},
		{"POST", "/api/crews/kept/duplicate", nil},
		{"DELETE", "/api/crews/kept", nil},
	} {
		resp, out := e.do(r.method, r.path, adminToken, r.body)
		wantAPIError(t, r.method+" "+r.path, resp, out, http.StatusInternalServerError, "store_failed", "could not save the crews")
		if strings.Contains(fmt.Sprint(out), e.srv.store.Dir()) {
			t.Errorf("%s %s names the data directory: %v", r.method, r.path, out)
		}
	}
	if after := e.crews(); !reflect.DeepEqual(after, before) || !reflect.DeepEqual(e.crew("kept"), kept) {
		t.Fatalf("a failed save changed the crews:\n before %v\n after  %v", before, after)
	}
}
```

`TestNewRefusesAMalformedCrewsFile` stays as it is: a malformed `crews.json` still stops `New`. Its message keeps naming the path and `"bogus"`. In `TestCrewRoutesNeedAStore`, add `{"GET", "/api/crews/crew", nil}` to the table (503), and change the listing check to `ro.crews()` returning nothing with `total` 0.

Add the startup log test:

```go
// A corrupt crew file names itself in the startup log, at error level, and
// the server starts with the other crews.
func TestNewLogsACorruptCrewFile(t *testing.T) {
	e := newTestEnv(t, nil)
	e.sendCrew("POST", "/api/crews", e.crewBody("Good"), http.StatusCreated)
	broken := filepath.Join(e.srv.store.Dir(), "crews", "broken.json")
	if err := os.WriteFile(broken, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	srv, err := New(e.srv.cfg, e.srv.base, slog.New(slog.NewTextHandler(&logs, nil)), nil, e.srv.store)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), broken) {
		t.Fatalf("log:\n%s", logs.String())
	}
	if ids := e.serve(srv).crewIDs(); !slices.Equal(ids, []string{"good"}) {
		t.Fatalf("ids %v", ids)
	}
}
```

Two existing tests assumed the list of full crews and the 50-crew cap. Replace both whole. `TestCrewSaveRejectsInvalidCrews` compares the full crew (`e.crew`), not a summary, with the reply, and its body case is now past 2 MiB:

```go
func TestCrewSaveRejectsInvalidCrews(t *testing.T) {
	e := newTestEnv(t, nil)
	kept := e.sendCrew("POST", "/api/crews", e.crewBody("Kept"), http.StatusCreated)
	with := func(change func(b map[string]any)) map[string]any {
		b := e.crewBody("Crew")
		change(b)
		return b
	}
	cases := []struct {
		name string
		body any
		code string
		msg  string // part of the message
	}{
		{"member name Lead!", with(func(b map[string]any) { crewMember(b, 0)["name"] = "Lead!" }), "invalid_crew", "must match"},
		{"unknown agent", with(func(b map[string]any) { crewMember(b, 1)["agentId"] = "nope" }), "invalid_crew", `"nope"`},
		{"after a missing member", with(func(b map[string]any) {
			crewMember(b, 1)["start"] = map[string]any{"when": "after", "member": "ghost"}
		}), "invalid_crew", `"ghost"`},
		{"13 members", with(func(b map[string]any) {
			for i := 2; i < 13; i++ {
				b["members"] = append(b["members"].([]any), map[string]any{"name": fmt.Sprintf("m%d", i), "agentId": "cat", "prompt": "", "start": map[string]any{"when": "manual"}})
			}
		}), "invalid_crew", "at most 12"},
		{"where elsewhere", with(func(b map[string]any) { b["where"] = "cloud" }), "invalid_crew", `"cloud"`},
		{"args over 8 KiB in all", with(func(b map[string]any) {
			crewMember(b, 0)["args"] = []any{strings.Repeat("a", 4096), strings.Repeat("b", 4096), "c"}
		}), "invalid_crew", "8192 bytes"},
		{"members starting after each other", with(func(b map[string]any) {
			crewMember(b, 0)["start"] = map[string]any{"when": "after", "member": "tests"}
		}), "invalid_crew", "cycle"},
		{"member name git refuses", with(func(b map[string]any) {
			crewMember(b, 1)["name"] = "tests.lock"
		}), "invalid_crew", "git"},
		{"name with a control character", with(func(b map[string]any) { b["name"] = "API\x1b[2Jsweep" }), "invalid_crew", "control"},
		{"id sent by the client", with(func(b map[string]any) { b["id"] = "mine" }), "invalid_request", `"id"`},
		{"createdAt sent by the client", with(func(b map[string]any) { b["createdAt"] = "2026-09-29T10:00:00Z" }), "invalid_request", `"createdAt"`},
		{"unknown member field", with(func(b map[string]any) { crewMember(b, 0)["model"] = "x" }), "invalid_request", `"model"`},
		{"no body", nil, "invalid_request", ""},
		{"body over 2 MiB", with(func(b map[string]any) { b["goal"] = strings.Repeat("g", maxCrewBody) }), "invalid_request", "too large"},
	}
	for _, tc := range cases {
		for _, path := range []string{"POST /api/crews", "PUT /api/crews/kept"} {
			method, route, _ := strings.Cut(path, " ")
			resp, out := e.do(method, route, adminToken, tc.body)
			wantAPIError(t, tc.name+" ("+path+")", resp, out, http.StatusBadRequest, tc.code, tc.msg)
		}
	}
	if list := e.crews(); len(list) != 1 || list[0]["id"] != "kept" || !reflect.DeepEqual(e.crew("kept"), kept) {
		t.Fatalf("a refused save changed the crews: %v", list)
	}
}
```

`TestCrewErrorsQuoteShortAndComeInOrder` keeps its order checks and ends with a valid crew created, where it expected `409 too_many_crews` at 50 crews:

```go
// A value quoted in an error comes back cut short, however long it was sent.
// A crew that breaks a rule is refused for that before its agents are looked
// up and before its id is; an agent the catalog does not have is 400 whatever
// else holds. No crew limit comes after them: a valid crew is created.
func TestCrewErrorsQuoteShortAndComeInOrder(t *testing.T) {
	e := newTestEnv(t, nil)
	e.sendCrew("POST", "/api/crews", e.crewBody("Kept"), http.StatusCreated)
	message := func(out map[string]any) string {
		apiErr, _ := out["error"].(map[string]any)
		msg, _ := apiErr["message"].(string)
		return msg
	}
	huge := e.crewBody("Huge")
	crewMember(huge, 1)["agentId"] = strings.Repeat("a", 900<<10)
	invalid := e.crewBody("Invalid")
	crewMember(invalid, 0)["name"] = "Lead!"
	unknown := e.crewBody("Unknown")
	crewMember(unknown, 1)["agentId"] = "nope"
	both := e.crewBody("Both")
	crewMember(both, 0)["name"] = "Lead!"
	crewMember(both, 1)["agentId"] = "nope"
	for _, path := range []string{"POST /api/crews", "PUT /api/crews/kept", "PUT /api/crews/missing"} {
		method, route, _ := strings.Cut(path, " ")
		resp, out := e.do(method, route, adminToken, huge)
		wantAPIError(t, "a 900 KiB agentId ("+path+")", resp, out, http.StatusBadRequest, "invalid_crew", "agentId")
		if msg := message(out); len(msg) > 300 {
			t.Errorf("%s: a message of %d bytes", path, len(msg))
		}
		resp, out = e.do(method, route, adminToken, both)
		wantAPIError(t, "a rule broken and an unknown agent ("+path+")", resp, out, http.StatusBadRequest, "invalid_crew", "must match")
		resp, out = e.do(method, route, adminToken, invalid)
		wantAPIError(t, "a rule broken ("+path+")", resp, out, http.StatusBadRequest, "invalid_crew", "must match")
		resp, out = e.do(method, route, adminToken, unknown)
		wantAPIError(t, "an unknown agent ("+path+")", resp, out, http.StatusBadRequest, "invalid_crew", `"nope"`)
	}
	e.sendCrew("POST", "/api/crews", e.crewBody("Valid"), http.StatusCreated)
}
```

In `TestCrewRoutesTakeTheLargestCrews`, the comment's "whose bodies may reach 1 MiB where others stop at 64 KiB" becomes "whose bodies may reach 2 MiB (`maxCrewBody`) where others stop at 64 KiB"; its code stays.

Add the tests for the body bound and for a crews directory the server cannot read:

```go
// A crew near the bound on its file goes through when a client sends it
// indented: the body may be up to 2 MiB (maxCrewBody), twice the bound, and
// the crew itself is held to crew.MaxEncoded as its file.
func TestCrewRoutesTakeAnIndentedBodyNearTheBound(t *testing.T) {
	e := newTestEnv(t, nil)
	ctl := func(n int) string { return strings.Repeat("\x01", n) } // six bytes each as JSON
	body := e.crewBody("Indented")
	var members []any
	for i := range 12 {
		members = append(members, map[string]any{
			"name": fmt.Sprintf("m%02d", i), "agentId": "sh", "prompt": ctl(4000),
			"args":  []any{ctl(4096), ctl(4096)},
			"start": map[string]any{"when": "manual"},
		})
	}
	body["members"] = members
	body["goal"] = ctl(2000)
	compact, _ := json.Marshal(body)
	raw, err := json.MarshalIndent(body, "", strings.Repeat(" ", 2048))
	if err != nil {
		t.Fatal(err)
	}
	if len(compact) < 800<<10 || len(raw) <= 1<<20 || len(raw) >= maxCrewBody {
		t.Fatalf("compact %d bytes, indented %d", len(compact), len(raw))
	}
	// e.do would marshal the body again, compact: send the bytes as they are.
	req, _ := http.NewRequest("POST", e.http.URL+"/api/crews", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the indented crew: %d", resp.StatusCode)
	}
	fi, err := os.Stat(filepath.Join(e.srv.store.Dir(), "crews", "indented.json"))
	if err != nil || fi.Size() > crew.MaxEncoded {
		t.Fatalf("its file: %v %v", fi, err)
	}
}

// A crews directory the server cannot read is a read failure and is answered
// as one: the list, and a create, which reads the directory for a free id,
// say "could not read the crews", without the data directory's path.
func TestCrewRoutesSayWhenTheyCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory of mode 0300")
	}
	e := newTestEnv(t, nil)
	dir := filepath.Join(e.srv.store.Dir(), "crews")
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	resp, out := e.do("GET", "/api/crews", adminToken, nil)
	wantAPIError(t, "list", resp, out, http.StatusInternalServerError, "store_failed", "could not read the crews")
	resp, out = e.do("POST", "/api/crews", adminToken, e.crewBody("New"))
	wantAPIError(t, "create", resp, out, http.StatusInternalServerError, "store_failed", "could not read the crews")
	if strings.Contains(fmt.Sprint(out), e.srv.store.Dir()) {
		t.Errorf("the reply names the data directory: %v", out)
	}
}
```

(`api_test.go` imports `"bytes"` and `"encoding/json"` already; add `github.com/phenixrizen/conductor/internal/crew` for `crew.MaxEncoded`.) `TestCrewFailedSaveChangesNothing` above already expects "could not save the crews" from every write.

Run: `go test ./internal/api/ -count=1 -run 'Crew|NewLogs'`
Expected: build failure on `crew.NewStore`'s results; then FAIL on the new routes, the 2 MiB body and the read failures.

- [ ] **Step 6: Implement the routes**

In `internal/api/server.go`, `New`:

```go
	var crews *crew.Store
	if st != nil {
		var problems []error
		if crews, problems, err = crew.NewStore(st); err != nil {
			return nil, err
		}
		// Each problem names its files and says why: a crew file that
		// cannot be used (fix or delete it), or a crew of crews.json that
		// was not moved over a file of the same id.
		for _, p := range problems {
			log.Error("crew left out", "err", p)
		}
	}
```

Update `Server.crews`'s comment ("one file each in crews/ of the data directory") and `New`'s ("…or crews.json that cannot be moved, is an error…; a crew file that cannot be used, and a crew of crews.json not moved over an existing file, are logged and left out").

`internal/api/crews.go` (add `"fmt"`, `"net/url"` and `"strconv"` to the imports):

```go
// maxCrewBody bounds the body of a crew create or update: 2 MiB, twice
// crew.MaxEncoded. A client may indent a crew, or escape more of it than the
// server does, so a crew near the bound on its file still fits; Validate holds
// the crew itself to crew.MaxEncoded as its file.
const maxCrewBody = 2 << 20

// The crew list's page: limit defaults to 100 and takes 1 to 500.
const (
	defaultCrewPage = 100
	maxCrewPage     = 500
)

// handleListCrews lists one page of the saved crews' summaries, ordered by
// name: GET /api/crews?offset=0&limit=100. total counts every crew. Without a
// data directory there are none.
func (s *Server) handleListCrews(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := pageParams(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	list, total := []crew.Summary{}, 0
	if s.crews != nil {
		page, n, err := s.crews.List(offset, limit)
		if err != nil {
			s.log.Error("crews list failed", "err", err)
			writeError(w, http.StatusInternalServerError, "store_failed", "could not read the crews")
			return
		}
		list, total = page, n
	}
	writeJSON(w, http.StatusOK, map[string]any{"crews": list, "total": total})
}

// pageParams reads offset (a whole number from 0, default 0) and limit (1 to
// maxCrewPage, default defaultCrewPage).
func pageParams(q url.Values) (offset, limit int, err error) {
	offset, limit = 0, defaultCrewPage
	if v := q.Get("offset"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			offset = n
		} else {
			return 0, 0, errors.New("offset must be a whole number from 0")
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 1 && n <= maxCrewPage {
			limit = n
		} else {
			return 0, 0, fmt.Errorf("limit must be a whole number from 1 to %d", maxCrewPage)
		}
	}
	return offset, limit, nil
}

// handleGetCrew answers one crew in full, for the editor.
func (s *Server) handleGetCrew(w http.ResponseWriter, r *http.Request) {
	if s.crews == nil {
		writeError(w, http.StatusServiceUnavailable, "store_unavailable", "no data directory is configured")
		return
	}
	id := r.PathValue("id")
	c, err := s.crews.Get(id)
	if err != nil {
		s.crewStoreError(w, id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"crew": c})
}
```

`handleDuplicateCrew` reads the source with `src, err := s.crews.Get(id)` and calls `s.crewStoreError(w, id, err)` on error, in place of the `ok` check. `crewStoreError`:

```go
// crewStoreError answers an error from the crew store. A failure of the
// store itself is logged with its cause, which names the data directory; the
// reply does not, and says whether a read or a write failed.
func (s *Server) crewStoreError(w http.ResponseWriter, id string, err error) {
	switch {
	case errors.Is(err, crew.ErrUnreadable):
		// Says what is wrong with the file, which it names, without the
		// data directory's path.
		writeError(w, http.StatusConflict, "crew_unreadable", err.Error())
	case errors.Is(err, crew.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid_crew", err.Error())
	case errors.Is(err, crew.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such crew")
	case errors.Is(err, crew.ErrWrite):
		s.log.Error("crew save failed", "crew", id, "err", err)
		writeError(w, http.StatusInternalServerError, "store_failed", "could not save the crews")
	default:
		// The crews directory, or a crew's file, could not be read.
		s.log.Error("crew read failed", "crew", id, "err", err)
		writeError(w, http.StatusInternalServerError, "store_failed", "could not read the crews")
	}
}
```

`readCrew`'s comment drops the crew limit: "So a crew that breaks a rule is refused for it (400) before the store looks at the crew limit (409) or at the id of an update (404)" becomes "So a crew that breaks a rule is refused for it (400) before the store looks at the id of an update (404)".

`ErrUnreadable` comes first, since its errors may wrap `ErrInvalid` too. The store's errors name the file (`broken.json`), never the directory. Delete the `ErrTooManyCrews` case. In `server.go`, add the route `mux.HandleFunc("GET /api/crews/{id}", s.requireAdmin(s.handleGetCrew))` after `GET /api/crews`. In `runs.go`, `handleLaunchCrew` reads the crew with:

```go
	c, err := s.crews.Get(r.PathValue("id"))
	if err != nil {
		s.crewStoreError(w, r.PathValue("id"), err)
		return
	}
```

Run: `go test -race -count=1 ./internal/api/`
Expected: PASS.

- [ ] **Step 7: `conductor crews` pages through the list**

In `internal/cli/up.go`:

```go
// crewsPage is how many crews conductor crews asks for at a time.
const crewsPage = 500
```

In `runCrews`, everything from `// As in runUp, unknown fields are ignored on purpose.` to the end of the function becomes:

```go
	// As in runUp, unknown fields are ignored on purpose. A reply without
	// total (an older server) is one page.
	type crewLine struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Members []struct {
			Name string `json:"name"`
		} `json:"members"`
	}
	var all []crewLine
	for offset := 0; ; {
		var reply struct {
			Crews []crewLine `json:"crews"`
			Total int        `json:"total"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/crews?offset=%d&limit=%d", offset, crewsPage), &reply); err != nil {
			return 1, err
		}
		all = append(all, reply.Crews...)
		offset += len(reply.Crews)
		if len(reply.Crews) == 0 || offset >= reply.Total {
			break
		}
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
```

In `internal/cli/up_test.go`, `TestCrewsList` expects the query `offset=0&limit=500`: `request{http.MethodGet, "/api/crews", "offset=0&limit=500", "Bearer " + secretToken}`. Add:

```go
// conductor crews asks for page after page until it has the total.
func TestCrewsPagesThroughTheList(t *testing.T) {
	clearConductorEnv(t)
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		var body strings.Builder
		body.WriteString(`{"total":501,"crews":[`)
		n := 500
		if r.URL.Query().Get("offset") == "500" {
			n = 1
		}
		for i := range n {
			if i > 0 {
				body.WriteString(",")
			}
			fmt.Fprintf(&body, `{"id":"c-%s-%d","name":"C","members":[]}`, r.URL.Query().Get("offset"), i)
		}
		body.WriteString("]}")
		w.Write([]byte(body.String()))
	}))
	t.Cleanup(srv.Close)
	code, stdout, stderr, err := crews(t, "--server", srv.URL, "--token", secretToken)
	if code != 0 || err != nil || strings.Count(stdout, "\n") != 501 || !strings.Contains(stdout, "c-500-0\t") {
		t.Fatalf("exit %d %v\nstderr:\n%s", code, err, stderr)
	}
	if !slices.Equal(queries, []string{"offset=0&limit=500", "offset=500&limit=500"}) {
		t.Fatalf("queries %v", queries)
	}
}
```

(`up_test.go` imports `"fmt"` and `"slices"`.)

Run: `go test ./internal/cli/ -count=1 -run Crews`
Expected: PASS.

- [ ] **Step 8: The Crews page pages, and reads the selected crew in full**

In `web/app/composables/useSessions.ts`:

```ts
/** A crew as GET /api/crews lists it: what the list shows, without the prompts. GET /api/crews/{id} answers it in full. */
export interface CrewSummary {
  id: string
  name: string
  cwd: string
  where: 'server' | 'host'
  isolation: 'none' | 'worktree'
  members: Array<{ name: string; agentId: string }>
  updatedAt: string
}
```

```ts
    /** One page of the saved crews' summaries, ordered by name, and how many there are in all. `limit` is 1 to 500. */
    listCrews: (offset = 0, limit = 100) =>
      request<{ crews: CrewSummary[]; total: number }>('/api/crews', { query: { offset: String(offset), limit: String(limit) } }).then((r) => ({ crews: r.crews ?? [], total: r.total ?? 0 })),
    /** One crew in full, for the editor. 404 for an unknown id; 409 `crew_unreadable` for a file the server cannot use, with why. */
    getCrew: (id: string) => request<{ crew: CrewInfo }>(`/api/crews/${encodeURIComponent(id)}`).then((r) => r.crew),
```

In the `CrewInfo` comment on `members`, "The whole crew is at most 512 KiB as JSON." becomes "The whole crew is at most 1 MiB as its file." In the `saveCrew` comment, "so a listed CrewInfo can be passed as it is" becomes "so a CrewInfo read with getCrew can be passed as it is", and "409 `too_many_crews` past 50" becomes "409 `crew_unreadable` for a crew whose file the server cannot use".

In `web/app/utils/crews.ts`:

```ts
/** The summary GET /api/crews lists for `c`: what the list shows after a save, until the list is read again. */
export function summaryOf(c: CrewInfo): CrewSummary {
  return { id: c.id, name: c.name, cwd: c.cwd, where: c.where, isolation: c.isolation, members: c.members.map((m) => ({ name: m.name, agentId: m.agentId })), updatedAt: c.updatedAt }
}
```

and in `web/app/utils/crews.test.ts` (import `summaryOf`):

```ts
describe('summaryOf', () => {
  it('keeps what the list shows and drops the prompts', () => {
    const s = summaryOf(info)
    expect(s).toEqual({ id: info.id, name: info.name, cwd: info.cwd, where: info.where, isolation: info.isolation, members: info.members.map((m) => ({ name: m.name, agentId: m.agentId })), updatedAt: info.updatedAt })
    expect(JSON.stringify(s)).not.toContain('prompt')
  })
})
```

In `web/app/pages/crews/[[id]].vue`, the list holds summaries a page at a time, and the editor reads its crew in full:

```ts
import type { AgentInfo, CrewInfo, CrewSummary, RunInfo } from '~/composables/useSessions'
import { crewKey, defaultCrew, holdViewLink, runActive, summaryOf, toCrewInput, toDraft, type DraftCrew } from '~/utils/crews'

/** Crews the list shows at a time. */
const PAGE_SIZE = 100

/** The page of the list on screen, as GET /api/crews lists it. */
const crews = useState<CrewSummary[]>('crews', () => [])
const total = useState<number>('crewsTotal', () => 0)
const page = useState<number>('crewsPage', () => 1)
/** Crews read in full, by id: the one in the editor, and any whose draft is open. */
const full = useState<Record<string, CrewInfo>>('crewsFull', () => ({}))
/** Why the selected crew's file cannot be used, when the server says so. */
const unreadable = ref('')
/** The id of the crew being read in full: the editor shows a loading state meanwhile, never "No crew has the id". */
const crewLoading = ref<string>()

const saved = computed(() => (selectedKey.value ? full.value[selectedKey.value] : undefined))

async function refresh() {
  if (!admin.hasToken.value) {
    admin.needsToken.value = true
    return
  }
  loading.value = true
  try {
    const [list, a, r] = await Promise.all([api.listCrews((page.value - 1) * PAGE_SIZE, PAGE_SIZE), api.catalog(), api.listRuns()])
    // A page past the end (crews deleted elsewhere) moves back to the last one, and its watcher reads it.
    const last = Math.max(1, Math.ceil(list.total / PAGE_SIZE))
    if (page.value > last) {
      page.value = last
      return
    }
    crews.value = list.crews
    total.value = list.total
    agents.value = a
    runs.value = r
    error.value = ''
    loaded.value = true
    await loadSelected()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

/** Reads the crew on screen in full. A draft nobody edited follows it as saved now: another admin, or conductor crews, may have changed it. */
async function loadSelected() {
  const id = routeId.value
  unreadable.value = ''
  if (!id) return
  crewLoading.value = id
  try {
    const fresh = await api.getCrew(id)
    const before = full.value[id]
    const d = drafts.value[id]
    if (d && before && crewKey(d) === crewKey(before)) drafts.value = { ...drafts.value, [id]: toDraft(fresh) }
    full.value = { ...full.value, [id]: fresh }
  } catch (e) {
    const next = { ...full.value }
    delete next[id]
    full.value = next
    // An answer for a crew no longer on screen says nothing about the one that is.
    if (routeId.value !== id) return
    if (e instanceof ApiError && e.code === 'crew_unreadable') unreadable.value = e.message
    else if (!(e instanceof ApiError && e.status === 404)) error.value = (e as Error).message
  } finally {
    if (crewLoading.value === id) crewLoading.value = undefined
  }
}
watch(page, refresh)
```

Replace Task 8's `watch(routeId, () => refresh())` with `watch(routeId, loadSelected)`: moving to another crew reads that crew, and the page of the list stays.

The draft for the crew on screen is seeded from the crew read in full (`saved`, which reads `full`), never from a summary, which has no prompts. The watcher that makes it watches `full` as well, so it seeds the draft once `getCrew` answers:

```ts
// A draft for the crew on screen, seeded from the crew read in full once it is read; /crews moves to the first crew of the page.
watch(
  [selectedKey, crews, full, loaded],
  () => {
    if (!loaded.value) return
    if (selectedKey.value === undefined) {
      const first = crews.value[0]
      if (first) router.replace(`/crews/${encodeURIComponent(first.id)}`)
      return
    }
    if (selectedKey.value !== NEW && !drafts.value[selectedKey.value] && saved.value) {
      drafts.value = { ...drafts.value, [selectedKey.value]: toDraft(saved.value) }
    }
  },
  { immediate: true },
)
```

`isDirty` compares a draft with the crew read in full:

```ts
function isDirty(key: string): boolean {
  const d = drafts.value[key]
  if (!d) return false
  if (key === NEW) return true
  // A draft is seeded from `full`, so the crew is there; without it (a new token cleared it) the draft counts as changed.
  const c = full.value[key]
  return !c || crewKey(d) !== crewKey(c)
}
```

The list shows the page's summaries, or a crew's draft when one is open; `meta` takes either:

```ts
/** The list's meta in two parts: the working directory, which may be cut short, and the rest, which may not. */
function meta(c: DraftCrew | CrewSummary, id?: string): { cwd: string; rest: string } {
  const last = id ? runsOf(id)[0] : undefined
  return {
    cwd: shortCwd(c.cwd) || 'server default',
    rest: `${c.isolation === 'worktree' ? 'worktrees' : 'shared cwd'} · last run ${last ? relativeTime(last.startedAt, now.value) : 'never'}`,
  }
}

/** The list: a new draft first, then the page's crews, each as its draft shows it when one is open. */
const list = computed(() => {
  const out: Array<{ key: string; crew: DraftCrew | CrewSummary; to: string }> = []
  const fresh = drafts.value[NEW]
  if (fresh) out.push({ key: NEW, crew: fresh, to: '/crews' })
  for (const c of crews.value) out.push({ key: c.id, crew: drafts.value[c.id] ?? c, to: `/crews/${encodeURIComponent(c.id)}` })
  return out
})
```

In `save()`, the two lines after `const c = await api.saveCrew(toCrewInput(d), d.id)` that update `crews` become:

```ts
    full.value = { ...full.value, [c.id]: c }
    if (crews.value.some((x) => x.id === c.id)) crews.value = crews.value.map((x) => (x.id === c.id ? summaryOf(c) : x))
    else crews.value = [...crews.value, summaryOf(c)].sort((a, b) => a.name.localeCompare(b.name))
    // Only a new crew adds to the count: one opened by its link from another page was counted already.
    if (key === NEW) total.value += 1
```

`duplicate()` and `confirmDelete()` become:

```ts
async function duplicate() {
  const c = saved.value
  if (!c) return
  try {
    const copy = await api.duplicateCrew(c.id)
    full.value = { ...full.value, [copy.id]: copy }
    crews.value = [...crews.value, summaryOf(copy)].sort((a, b) => a.name.localeCompare(b.name))
    total.value += 1
    toast.add({ title: 'Crew duplicated', description: isDirty(c.id) ? `${copy.name}, from what ${c.name} has saved` : copy.name, icon: 'i-lucide-copy', color: 'success' })
    router.push(`/crews/${encodeURIComponent(copy.id)}`)
  } catch (e) {
    fail('Duplicate failed', e)
  }
}

async function confirmDelete() {
  // A crew whose file cannot be used has no `saved`: it is deleted by the id on screen.
  const id = saved.value?.id ?? routeId.value
  if (!id) return
  const name = saved.value?.name ?? id
  // Only a crew the server could read counts in `total`.
  const counted = !!saved.value
  deleting.value = true
  try {
    await api.deleteCrew(id)
    crews.value = crews.value.filter((x) => x.id !== id)
    if (counted) total.value = Math.max(0, total.value - 1)
    const nextFull = { ...full.value }
    delete nextFull[id]
    full.value = nextFull
    const next = { ...drafts.value }
    delete next[id]
    drafts.value = next
    unreadable.value = ''
    deleteOpen.value = false
    toast.add({ title: 'Crew deleted', description: name, icon: 'i-lucide-trash-2', color: 'neutral' })
    router.replace('/crews')
  } catch (e) {
    fail('Delete failed', e)
  } finally {
    deleting.value = false
  }
}
```

The token watcher also forgets what was read under the old token, and goes back to the first page:

```ts
watch(
  () => admin.token.value,
  () => {
    // Another token may be another server: nothing typed or read under the old one stays.
    drafts.value = {}
    crews.value = []
    full.value = {}
    total.value = 0
    if (page.value !== 1) page.value = 1 // its watcher reads the list
    else refresh()
  },
)
```

In the template, the delete modal's title is `` :title="`Delete ${saved?.name ?? routeId ?? 'crew'}?`" ``. Under the list, after the "No crews yet." line:

```vue
          <UPagination v-if="total > PAGE_SIZE" v-model:page="page" :total="total" :items-per-page="PAGE_SIZE" size="xs" class="mt-2 self-center" />
```

The editor section's branches become these, in this order. While the crew on screen is read, the page says so, and "No crew has the id" shows only once the server has answered that there is none:

```vue
          <CrewEditor
            v-if="draft"
            :key="selectedKey"
            v-model="draft"
            :agents="agents"
            :dirty="isDirty(selectedKey!)"
            :saving="saving"
            :launching="launching"
            @save="onSave"
            @discard="discard"
            @duplicate="duplicate"
            @launch="launch"
            @delete="deleteOpen = true"
          />
          <div v-else-if="unreadable" class="flex flex-col items-start gap-3" data-crew-unreadable>
            <UAlert color="error" variant="subtle" icon="i-lucide-file-warning" :title="`The file of ${routeId} cannot be used`" :description="unreadable" />
            <UButton label="Delete it" icon="i-lucide-trash-2" color="error" variant="soft" @click="deleteOpen = true" />
          </div>
          <div v-else-if="routeId && (crewLoading === routeId || !loaded)" class="flex items-center gap-2 text-sm text-muted" data-crew-loading>
            <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Loading the crew…
          </div>
          <div v-else-if="loaded && routeId && !saved" class="flex flex-col items-start gap-3 text-sm text-muted">
            <p>No crew has the id <code>{{ routeId }}</code>.</p>
            <UButton label="All crews" icon="i-lucide-arrow-left" color="neutral" variant="soft" to="/crews" />
          </div>
          <UEmpty
            v-else-if="loaded && !list.length"
            icon="i-lucide-users"
            title="No crews yet"
            description="A crew is a saved team of agents: each with a role prompt, its own git worktree and a start condition. Launch it here or with conductor up <crew>."
            :actions="[{ label: 'New crew', icon: 'i-lucide-plus', onClick: newCrew }]"
          />
          <div v-else-if="loading" class="flex items-center gap-2 text-sm text-muted"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" /> Loading crews…</div>
```

Run: `npm --prefix web test && npm --prefix web run typecheck`
Expected: PASS.

- [ ] **Step 9: Docs**

- `docs/protocol.md`, the `GET /api/crews` row becomes: "`{crews, total}`: a page of the saved crews' summaries ordered by name (ignoring case), then id, each `{id, name, cwd, where, isolation, members: [{name, agentId}], updatedAt}`; `?offset=` from 0 (default 0), `?limit=` 1 to 500 (default 100), `400 invalid_request` otherwise; `total` counts every crew; `{crews: [], total: 0}` without a data directory". Add a row `GET /api/crews/{id}` | admin | "one crew in full, `{crew}`, shaped as before; `404` when unknown; `409 crew_unreadable` when its file cannot be used, the message saying why; `503 store_unavailable`". In the create row, drop `409 too_many_crews`. In the duplicate row, "over 512 KiB" becomes "over 1 MiB" and `409 too_many_crews` goes. In the update, duplicate and launch rows, add `409 crew_unreadable`. In the body-size sentence (around line 375), "1 MiB on the crew routes" becomes "2 MiB on the crew routes". A failure of the store is `500 store_failed`, "could not save the crews" for a write and "could not read the crews" for a read.
- `docs/protocol.md`, the persistence paragraph: "The crew routes keep each crew in a file of its own, `crews/<id>.json` in the data directory, written whole (a temp file renamed into place). The list is read from that directory, so a file added, changed or removed by hand shows at the next listing. A file that cannot be used, a symbolic link included (it is not followed), is named in the server's log at startup and left out of the list; it is never overwritten (a new crew of the same name takes the next id), and `DELETE` removes it. A `crews.json` from an earlier version is split into these files at the first start and renamed `crews.json.migrated`, which keeps every crew; the move never overwrites a crew file, and a crew whose file exists with something else in it is not moved, which the log says naming both files. A `crews.json` that cannot be parsed stops startup, as before." In the limits, "at most 50 crews;" goes, "The whole crew… at most 512 KiB of JSON" becomes "A crew's file, as the server writes it with its `id` and times, is at most 1 MiB; a create or update body may be up to 2 MiB, so a crew sent indented still fits", and "the 50-crew limit (`409`)" goes from the order of checks.
- `README.md:346`: "Crews are saved as `crews.json` in the data directory" becomes "Crews are saved one file each, `crews/<id>.json` in the data directory (a `crews.json` from an earlier version is split up at the first start)".
- `README.md`, crew limits (around lines 433 to 437): "- 50 crews; 12 members a crew, and a run takes no more than that." becomes "- No limit on the number of crews (the Crews page and `conductor crews` page through them); 12 members a crew, and a run takes no more than that." "- 32 extra arguments a member, 8 KiB in all; 512 KiB for a whole crew as saved; request bodies of 1 MiB on the crew routes." becomes "- 32 extra arguments a member, 8 KiB in all; 1 MiB for a whole crew as its file; request bodies of 2 MiB on the crew routes."
- `internal/store/store.go:22`, the `Store` comment: "Store is a directory of JSON documents such as catalog.json or crews.json." becomes "Store is a directory of JSON documents such as catalog.json. It may hold directories of its own, each a Store (Sub), such as crews/ with one document per crew."
- `docs/architecture.md:94`: "saved crews in `dataDir/crews.json`" becomes "saved crews, one file each in `dataDir/crews/`".
- `AGENTS.md` map, `internal/crew` row: "persistence in `crews.json`" becomes "persistence in `crews/<id>.json`".

- [ ] **Step 10: Run the gate and commit**

Run: `make lint && go test -race -count=1 ./... && npm --prefix web test && npm --prefix web run typecheck`
Expected: PASS.

```bash
git add internal/store internal/crew internal/api internal/cli web/app docs/protocol.md docs/architecture.md README.md AGENTS.md
git commit -m "crews: one file per crew, paged summaries and GET /api/crews/{id}, no crew cap, crews.json migrated once"
```

---

### Task 10: Docs and test hygiene

Triage items #49 and #51, and the spec's "Docs" items that no earlier task covered. What an override inherits is in Task 2. The crew-cap checks were deleted with the cap in Task 9.

**Files:**
- Create: `internal/api/crews_test.go`
- Modify: `internal/api/api_test.go` (the crew and run tests leave it)
- Modify: `internal/crew/crew_test.go` (the duplicate-names case)
- Modify: `README.md` (Webhooks: what the Events page shows), `docs/protocol.md` (the two causes of a missing hosted attention state), `docs/features.md` (Round 3: plan 1 closed), `AGENTS.md` ("Adding things": where a route's test goes)

**Interfaces:**
- Consumes: the tests and helpers of Tasks 1 to 9, as they are after Task 9.
- Produces: no code. `crews_test.go` holds the crew and run tests of `internal/api`.

- [ ] **Step 1: Tighten the duplicate-names case**

In `internal/crew/crew_test.go`, `TestValidateRejectsBadMembers`, the case becomes:

```go
		{"duplicate member names", func(c *Crew) { c.Members[1] = manual("lead") }, `member "lead": the name is used twice`},
```

Run: `go test ./internal/crew/ -run TestValidateRejectsBadMembers -count=1`
Expected: PASS. The message already says it; the case now pins it.

- [ ] **Step 2: Deduplicate the 503 checks**

In `internal/api/api_test.go`, delete the tail of `TestCrewLaunchRefusals` that starts at `// Without a data directory there are no crews to launch.`. Add the launch route to the table of `TestCrewRoutesNeedAStore` instead:

```go
		{"POST", "/api/crews/crew/launch", nil},
```

so that table holds every crew route but the list: create, update, delete, duplicate, get (Task 9) and launch.

Run: `go test ./internal/api/ -run 'TestCrewLaunchRefusals|TestCrewRoutesNeedAStore' -count=1`
Expected: PASS.

- [ ] **Step 3: Move the crew and run tests into `crews_test.go`**

Before the move, record the test count: `go test ./internal/api/ -list '.*' | grep -c '^Test'`.

Create `internal/api/crews_test.go` with `package api`, and move from `api_test.go`, each with its doc comment, exactly these functions and nothing else (other tests that Tasks 1 to 9 added to `api_test.go`, such as `TestCatalogReadersDoNotWaitForAnEdit` and `TestShutdownWaitsForWhatTheServerStarted`, stay where they are, wherever in the file they were added):

- Helpers: `crewBody`, `crewMember`, `crews`, `crew`, `crewIDs`, `sendCrew`, `storedCrew`, `wantAPIError`, `gitRepo`, `runCrewBody`, `stopEverything`, `runMember`, `catMember`, `launchCrew`, `waitRunning`, `local`, `inputsBy`, `outputUntil` (a method of `*wsClient`) and `wantRunLog`.
- Tests: `TestCrewsCRUD`, `TestCrewSaveRejectsInvalidCrews`, `TestCrewErrorsQuoteShortAndComeInOrder`, `TestCrewRoutesTakeTheLargestCrews`, `TestCrewRoutesCheckTheAgents`, `TestCrewRoutesNeedAStore`, `TestCrewFailedSaveChangesNothing`, `TestNewRefusesAMalformedCrewsFile`, `TestDevCORSAllowsPut` (the crews' `PUT`), `TestCrewRunLifecycle`, `TestCrewLaunchViewLink`, `TestCrewLaunchRefusals`, `TestCrewLaunchWithoutGit`, `TestPlainSessionHasNoCrewVariables`, `TestCrewHandoffReachesTheOtherMember`, `TestBroadcastSkipsWaitingMembers`, `TestBroadcastReasonsAndLimits`, `TestRunLinkGrantsViewOnEveryMember`, `TestRunLinksGoWithTheirForgottenRun` and `TestRunLinkJoinSessionAndFiles`.
- Tests that Tasks 8 and 9 added: `TestRevokingARevokedLinkRecordsNothing`, `TestCrewRoutesPageThroughMoreThan50Crews`, `TestCrewRoutesAnswerForACorruptFile`, `TestNewLogsACorruptCrewFile`, `TestCrewRoutesTakeAnIndentedBodyNearTheBound` and `TestCrewRoutesSayWhenTheyCannotRead`.

Give `crews_test.go` the imports its code uses, and remove from `api_test.go` those it no longer uses; `go vet ./internal/api/` names both.

Check that every name moved, and moved once:

```bash
cd internal/api
for name in crewBody crewMember crews crew crewIDs sendCrew storedCrew wantAPIError gitRepo runCrewBody \
  stopEverything runMember catMember launchCrew waitRunning local inputsBy outputUntil wantRunLog \
  TestCrewsCRUD TestCrewSaveRejectsInvalidCrews TestCrewErrorsQuoteShortAndComeInOrder TestCrewRoutesTakeTheLargestCrews \
  TestCrewRoutesCheckTheAgents TestCrewRoutesNeedAStore TestCrewFailedSaveChangesNothing TestNewRefusesAMalformedCrewsFile \
  TestDevCORSAllowsPut TestCrewRunLifecycle TestCrewLaunchViewLink TestCrewLaunchRefusals TestCrewLaunchWithoutGit \
  TestPlainSessionHasNoCrewVariables TestCrewHandoffReachesTheOtherMember TestBroadcastSkipsWaitingMembers \
  TestBroadcastReasonsAndLimits TestRunLinkGrantsViewOnEveryMember TestRunLinksGoWithTheirForgottenRun \
  TestRunLinkJoinSessionAndFiles TestRevokingARevokedLinkRecordsNothing TestCrewRoutesPageThroughMoreThan50Crews \
  TestCrewRoutesAnswerForACorruptFile TestNewLogsACorruptCrewFile TestCrewRoutesTakeAnIndentedBodyNearTheBound \
  TestCrewRoutesSayWhenTheyCannotRead; do
  grep -qE "^func (\([^)]*\) )?$name\(" api_test.go && echo "still in api_test.go: $name"
  [ "$(grep -cE "^func (\([^)]*\) )?$name\(" crews_test.go)" = 1 ] || echo "not once in crews_test.go: $name"
done
cd ../..
```

Expected: no output.

Run: `go vet ./internal/api/ && go test -race -count=1 ./internal/api/ && go test ./internal/api/ -list '.*' | grep -c '^Test'`
Expected: PASS, and the same test count as before the move: no test was lost or doubled.

- [ ] **Step 4: The doc wording**

- `README.md`, Webhooks: "The Events page and `GET /api/integrations` show a webhook's URL without its user info, query string and fragment, and never its secret, but with its path: for a service that puts its credential in the path (Slack, Discord), admins see it there." becomes "The Events page shows each webhook by its host. `GET /api/integrations` returns its URL without its user info, query string and fragment, and never its secret, but with its path: for a service that puts its credential in the path (Slack, Discord), an admin reading that route sees it."
- `docs/protocol.md`, Webhooks (around line 344): "An entry that comes without one, from an older host, is the state it names as its message…" becomes "An entry that comes without one is the state it names as its message, if it names one (as it does for a report without a message), and otherwise only an `attention` entry. That happens for two reasons: the host is older and sends no state, or it sent a state the server does not take (anything but `needs_input`, `working` or `done`), which the server drops."
- `AGENTS.md`, "Adding things", the API route rule: "a test in `api_test.go`" becomes "a test in `internal/api` (`api_test.go` or the route family's `*_test.go`)", since the crew and run routes now have `crews_test.go`.
- `docs/features.md`, Round 3: after the heading "Deferred items to close (plan 1 of round 3)", add a line: "Closed by `docs/superpowers/plans/2026-10-01-round3-deferred.md`." Under "Open verification (round 3)", add "The crew-prompt placeholder at narrow widths (plan 1, Task 8)" only if Task 8's browser check could not run.

- [ ] **Step 5: Run the full gate and commit**

Run: `make lint && go test -race -count=1 ./... && npm --prefix web run typecheck && npm --prefix web test && make web-build && make build-go && python3 scripts/brand_assets.py --check`
Expected: every command succeeds.

```bash
git add internal/api internal/crew/crew_test.go README.md docs/protocol.md docs/features.md AGENTS.md
git commit -m "tests: crew and run tests in crews_test.go, one 503 table, duplicate names pinned; docs: webhook host, two causes"
```

---

## Verification

1. Run the full gate from `AGENTS.md`: `make lint`, `go test -race -count=1 ./...`, `npm --prefix web run typecheck`, `npm --prefix web test`, `make web-build`, `make build-go`, `python3 scripts/brand_assets.py --check`. A check that cannot run is reported as a limitation, not a pass.
2. Start a test server with a home of its own: `HOME=$(mktemp -d) bin/conductor serve --config conductor.example.json --listen 127.0.0.1:8099`. The admin token is `dev-admin-token-change-me`. The log names `dataDir=$HOME/.conductor`. For the headless browser, use `playwright-core@1.44.0` from a scratchpad directory with `~/.cache/ms-playwright/chromium-1117/chrome-linux/chrome` and `--no-sandbox`. Seed `localStorage` with `conductor.adminToken` through `addInitScript`. Stop the server by its pid.
3. Upgrade path: stop it, `mkdir ./conductor.d`, then put a `crews.json` with two crews in `./conductor.d` and start again with a new `HOME` that holds only `~/.conductor/hooks` (`mkdir -p $HOME/.conductor/hooks`, as `conductor host` leaves it). The log has one `level=WARN` line naming `./conductor.d` and `~/.conductor`. `./conductor.d/crews/` holds two files, and `crews.json.migrated` exists. **Crews** lists both, and a second start moves nothing.
4. Create 120 crews (a loop over `POST /api/crews`). The Crews page shows a pager with two pages. Delete one on page two and the page stays. `conductor crews` prints 120 lines.
5. Write `{oops` into `crews/broken.json` and restart. The log names the file at error level. `/crews/broken` shows the "cannot be used" alert with Delete, and Delete removes it.
6. Agents page: edit the built-in `claude` (change only the description). `catalog.json` holds no env values, only `***` markers if it had env. The card's button says "Revert", and Revert says "Change removed". Hide `shell`: its button says "Hide", and it moves to Hidden.
7. Add agent: type `aider --model "gpt` and press Enter. The text stays in the field, and Save shows the quote error. The arrow keys move between the signal cards.
8. Events page: a `conductor notify --state needs_input` from a session shows as `needs_input` at once (the SSE event carries `state`).

---

## Self-review

Run against the spec on 2026-10-01:

- **Coverage.** Every bullet under "Deferred items to close (plan 1 of round 3)", "Data directory default" and "Crew storage" maps to a task: strict JSON, the store and the lock to Task 1; catalog validation, inheritance, env and `source` to Task 2; the add-agent form to Task 3; file reads to Task 4; sessions and events to Task 5; adapters and the CLI to Task 6; the webhook address rule to Task 7; crews, runs and links to Task 8 (the `api_test.go` split, the duplicate checks and the duplicate-names assertion are Task 10); the Docs bullets to Task 2 (what an override inherits) and Task 10 (Events page host, two causes); crew storage to Task 9; the data directory default to Task 1. "Accepted as documented limitations (not changed): hosted crews" needs no task.
- **Placeholders.** Each step carries its code or its exact edit. Where a step moves code unchanged (Tasks 5, 6 and 10), it names every function that moves and gives the command that proves nothing was lost.
- **Type consistency.** `catalogEditMu` (Task 1) is used by Task 2. `store.DecodeStrict` (Task 1) is used by `config.Load`, `parseWebhooks`, `catalog.ReadFile` and `store.LoadLimit` (Task 9). `holdsServerData` (Task 1) is the one test of `~/.conductor` that `ResolveDataDir`, and so `serveHooksDir`, apply. `startImmediate(ctx, r)` (Task 8) is new; the existing `Engine.start(ctx, r, m)` keeps its name and signature. `crew.ErrWrite` and `crew.ErrUnreadable` (Task 9) are what `crewStoreError` tells apart, and `migrate` returns `(notices, err)` to `NewStore`, whose `problems` carry the notices. `maxCrewBody` is 2 MiB in the Values line, Task 9's code and its tests. `ResolveDataDir` returns `(notice, err)` in Tasks 1 and 6. `HostHooksDir` returns `(dir, legacy, err)` in Tasks 1 and 6. `OnActivity` has three parameters in Tasks 5 and 8. `LaunchHeld` (Task 8) is used by Task 9's `handleLaunchCrew`. `Revoke` and `RevokeRun` return `(found, revoked)` in Task 8. `crew.Store.Get` returns `(Crew, error)` from Task 9 on, while Task 8's code still uses the old `(Crew, bool)` and Task 9 changes it. `store.Encode` (Task 1) is used by Task 9.
- **Review Focus.** Each of the five has its test in its owning task: `TestServeKeepsAnOldDataDirectoryWithANotice` and `TestResolveDataDirDefaults` (Task 1), `TestMigrationFinishesAfterAnInterruptedStart` (Task 9), `TestACorruptCrewFileIsSkippedAndNeverOverwritten` and `TestCrewRoutesAnswerForACorruptFile` (Task 9), `TestCatalogOverrideFollowsTheBaseEnv` (Task 2), and `TestOwnHomeExceptionNeverCoversRoot` (Task 6).
