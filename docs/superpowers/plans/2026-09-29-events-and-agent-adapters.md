# Events and Agent Adapters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the per-session attention signal into a general event stream (mockup 2d), wire Conductor's hooks into every supported agent with launch-time injection or one-click install, add a screen-pattern detector for agents without hooks, and grow the built-in catalog to thirteen agents.

**Architecture:** Events reuse the activity log that already lives in `internal/session.Local` (ring of 200, `activity` control frame, replay on attach). New entry types (`progress`, `artifact`, `handoff`, `tool_use`, `tool_denied`, `error`) arrive through a new agent-token route `POST /api/sessions/{id}/events` and are fanned out to admins as `activity` events on the existing `/api/events` SSE stream; hosts forward theirs over the control connection. A new `internal/agents` package holds one adapter per supported agent: how to inject hooks at launch (argv/env), how to install them into the agent's config on demand, how to map its hook payload to events (used by `conductor notify --<agent>-hook`), and the generated assets written into `dataDir/hooks/`. A pattern detector in `Local` watches the last screen line for catalog entries with `signal.kind = pattern`.

**Tech Stack:** Go stdlib (`regexp`, `os/exec.LookPath`, `net/http`), Nuxt UI, vitest. No new dependencies.

**Spec:** `docs/features.md` § "Round 2" (Decisions → Hook wiring, Events, Supported agents; Agent adapter matrix; Open verification). Depends on Plan A (`2026-09-29-data-dir-and-catalog-editing.md`): `cfg.DataDir`, `store.Store`, `catalog.Agent.Adapter/Signal/EnvPassthrough`.

## Global Constraints

- Protocol changes touch `internal/proto`, `web/app/utils/protocol.ts` and `docs/protocol.md` together; every new field gets a limit and a test.
- Event limits: `message` ≤ 500 (existing `MaxAttentionMessage`), `url` ≤ 2048, `to` ≤ 40 runes, `tool` ≤ 100; at most 20 events/s per session (token bucket), excess dropped and counted.
- Hook commands invoked by agents are always the plain `conductor notify …` argv; adapters never build shell strings from user input. Generated assets contain only fixed text plus the absolute path of the `conductor` binary (`os.Executable()`).
- Conductor edits an agent's own config only through `Install`, which is idempotent (a Conductor-marked block or its own file) and only runs when an admin clicks or runs `conductor hooks install`.
- Adapters are marked verified in `docs/features.md` only after the manual check in the matrix's last column.
- The pattern detector strips ANSI, waits 500 ms of silence, and never fires on a session already in `needs_input`.

## Review Focus

1. **A hook floods events** (tool events on a busy agent). Expected: the ring keeps the newest 200, the SSE client is not evicted, and the per-session bucket drops the excess with a debug log. Pinned in Task 1 (rate-limit test) and Task 2 (SSE stays connected under 1000 events).
2. **An `artifact` URL that is `javascript:` or a local path.** Expected: stored as text, rendered as a link only for `http(s)://`; anything else is plain text. Pinned in Task 6 (`linkableUrl` test).
3. **A hosted session's host runs an old binary** without the `activity` host message. Expected: the server ignores unknown host messages (already `default: log`), events from that host simply do not appear. Pinned in Task 2 (host message test with an unknown `t`).
4. **The agent binary is missing at launch** after injection adds flags. Expected: the existing `start_failed` error, not a crash in the adapter. Pinned in Task 4 (inject on a missing binary returns argv unchanged plus flags; launch fails cleanly).
5. **A pattern that matches the agent's normal prompt line while it is still working** (spinner rows). Expected: silence gate of 500 ms and no re-fire while `needs_input`; a chatty TUI is documented as unsupported for pattern mode. Pinned in Task 3.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/session/activity.go` | new entry types and fields, `CleanEntry`, rate bucket |
| `internal/session/local.go` | `Record` uses the bucket; `OnActivity` option; `LastOutputAt`; pattern hook |
| `internal/session/pattern.go` (new, +test) | ANSI stripper, last-line tracker, `PatternWatcher` |
| `internal/proto/control.go`, `hostmsg.go` | `Activity` fields; host `activity` message |
| `internal/api/events.go` | `eventHub.activity(sessionID, entry)` → SSE `activity` |
| `internal/api/attention.go` | `POST /api/sessions/{id}/events` |
| `internal/api/integrations.go` (new) | `GET /api/integrations`, `POST /api/integrations/{id}/install` |
| `internal/api/sessions.go` | launch injection, pattern wiring |
| `internal/agents/*.go` (new) | `Adapter` interface, registry, one file per agent, assets, install/merge helpers, tests |
| `internal/notify/notify.go` | `Request.Event/URL/To/Tool`, per-agent hook mappers |
| `internal/cli/notify.go`, `hooks.go` (new), `host.go` | `--event …`, `--<agent>-hook`, `conductor hooks install|status`, `--agent`/`--signal-pattern` |
| `internal/hostagent/agent.go` | forwards activity; injection for hosted launches |
| `internal/catalog/defaults.go` | thirteen built-ins with adapters and signals |
| `web/app/utils/protocol.ts`, `composables/useEvents.ts` (new), `pages/events.vue` (new), `components/IntegrationCard.vue`, `RoutingMatrix.vue`, `EventFeed.vue` (new), `layouts/default.vue`, `composables/useAttention.ts`, `components/SessionInspector.vue` | Events page, routing, feed |
| `docs/protocol.md`, `README.md`, `docs/features.md` | docs |

---

### Task 1: Event model in the session log

**Files:**
- Modify: `internal/session/activity.go`, `internal/session/local.go`, `internal/session/activity_test.go`, `internal/session/local_test.go`, `internal/proto/control.go`, `web/app/utils/protocol.ts`, `docs/protocol.md`

**Interfaces:**
```go
// activity.go additions
const (
	ActivityProgress   = "progress"
	ActivityArtifact   = "artifact"
	ActivityHandoff    = "handoff"
	ActivityToolUse    = "tool_use"
	ActivityToolDenied = "tool_denied"
	ActivityError      = "error"
)
const (MaxEventURL = 2048; MaxEventTo = 40; MaxEventTool = 100; EventRatePerSecond = 20; EventBurst = 40)
type ActivityEntry struct { …existing…; URL string `json:"url,omitempty"`; To string `json:"to,omitempty"`; Tool string `json:"tool,omitempty"` }
func ValidEventType(t string) bool            // the six above plus attention/input/join/leave/link/status
func CleanEntry(e ActivityEntry) ActivityEntry // trims/caps every field, strips control chars
// Local
type Options struct { …; OnActivity func(sessionID string, e ActivityEntry) } // called outside the lock after each Record
func (s *Local) Record(e ActivityEntry) bool   // false when dropped by the rate bucket
func (s *Local) Dropped() uint64
// proto.Activity gains URL, To, Tool (json url,to,tool omitempty)
```

- [ ] **Step 1: Failing tests**

```go
// activity_test.go
func TestCleanEntryCapsFields(t *testing.T) {
	e := CleanEntry(ActivityEntry{Type: "artifact", URL: strings.Repeat("u", 3000), To: strings.Repeat("t", 100), Tool: strings.Repeat("x", 200), Message: "a\x1bb"})
	if len(e.URL) != MaxEventURL || len([]rune(e.To)) != MaxEventTo || len(e.Tool) != MaxEventTool || e.Message != "ab" {
		t.Fatalf("%+v", e)
	}
	if ValidEventType("bogus") || !ValidEventType("handoff") || !ValidEventType("join") {
		t.Fatal("ValidEventType")
	}
}
// local_test.go
func TestRecordRateLimitsPerSession(t *testing.T) {
	var got int
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, OnActivity: func(string, ActivityEntry) { got++ }})
	accepted := 0
	for i := 0; i < 200; i++ {
		if s.Record(ActivityEntry{Type: ActivityProgress, Message: "x"}) {
			accepted++
		}
	}
	if accepted > EventBurst || accepted < EventBurst/2 || s.Dropped() != uint64(200-accepted) || got != accepted {
		t.Fatalf("accepted %d dropped %d hooks %d", accepted, s.Dropped(), got)
	}
	time.Sleep(1100 * time.Millisecond)
	if !s.Record(ActivityEntry{Type: ActivityProgress}) {
		t.Fatal("bucket did not refill")
	}
}
```
`newLocalWith(t, opts)` is a small helper like `newLocal` that takes `Options`. Run → FAIL.

- [ ] **Step 2: Implement.** Token bucket: `tokens float64`, `last time.Time`, guarded by `s.mu`; refill `EventRatePerSecond` per second up to `EventBurst`. `Record` returns false and increments `dropped` when empty. `CleanEntry` uses `CleanMessage` for Message/Tool, trims URL to bytes, To to runes. `activityMessage` passes the three new fields. `OnActivity` called after the broadcast, outside the lock. `protocol.ts`: extend `ActivityEntry.type` union and add `url?`, `to?`, `tool?`. `docs/protocol.md`: extend the `activity` row and add an "Events" subsection listing the six types with their meaning and limits.

- [ ] **Step 3: Run** `go test -race ./internal/session/ ./internal/proto/` → PASS; typecheck. Commit `events: six new activity types with limits and a per-session rate bucket`.

---

### Task 2: Events route, notify flags, host forwarding, admin SSE

**Files:**
- Modify: `internal/api/attention.go`, `internal/api/events.go`, `internal/api/server.go`, `internal/api/ws_host.go`, `internal/api/api_test.go`, `internal/api/ws_e2e_test.go`, `internal/proto/hostmsg.go`, `internal/signal/hosted.go`, `internal/hostagent/agent.go`, `internal/hostagent/agent_test.go`, `internal/notify/notify.go`, `internal/notify/notify_test.go`, `internal/cli/notify.go`, `web/app/composables/useSessions.ts`, `docs/protocol.md`

**Interfaces:**
- `POST /api/sessions/{id}/events` (agent token or admin): body
  `{"type": "progress|artifact|handoff|tool_use|tool_denied|error|needs_input|working|done|clear", "message"?, "url"?, "to"?, "tool"?, "kind"?, "options"?}` → 202 `{"accepted": true}`; attention types route to `SetAttentionFull` (so `/attention` and `/events` stay equivalent for those); unknown type → 400 `invalid_type`; 429 `rate_limited` when the bucket drops it.
- `notify.Request` gains `Event string json:"event,omitempty"`, `URL`, `To`, `Tool`; `notify.Send` posts to `…/events` when `Event != ""`, else to `…/attention` (compatibility). CLI: `--event <type> [--message] [--url] [--to] [--tool]`.
- Host → server message `activity{sessionId, entry}` (`proto.HostActivityMsg{T:"activity", SessionID, Entry proto.Activity}`, ≤ 8 KiB); `HostedSession.HostActivity(entry)` calls `hub.OnActivity`.
- `eventHub.activity(sessionID string, e session.ActivityEntry)` writes `event: activity\ndata: {"sessionId": …, …entry}` to every SSE client; server wires `Options.OnActivity` for Local sessions and `signal.Hub.OnActivity` for hosted.
- Client: `useSessions().sendEvent(id, body)`; `useAttention` handles `activity` SSE blocks by calling `useEvents().push(sessionId, entry)` (Task 6 defines `useEvents`; until then keep a no-op stub in this task).

- [ ] **Step 1: Failing tests**

```go
// api_test.go
func TestEventsRouteRecordsAndStreams(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	// subscribe to SSE (reuse the helper pattern from TestAttentionViaAgentTokenBellAndEvents)
	events := e.sse(t) // returns chan string of "<event> <data>" lines; add this helper
	resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "artifact", "message": "PR opened", "url": "https://github.com/x/y/pull/1"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	e.waitEvent(t, events, func(ev string) bool { return strings.HasPrefix(ev, "activity ") && strings.Contains(ev, `"type":"artifact"`) && strings.Contains(ev, id) })
	if resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "bogus"}); resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "invalid_type" {
		t.Fatalf("bogus: %d %v", resp.StatusCode, out)
	}
	// attention types still update attention
	e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "needs_input", "message": "?"})
	_, got := e.do("GET", "/api/sessions/"+id, adminToken, nil)
	if got["session"].(map[string]any)["attention"].(map[string]any)["state"] != "needs_input" {
		t.Fatal("needs_input via /events did not apply")
	}
}

func TestEventsFloodKeepsSSEClient(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	events := e.sse(t)
	for i := 0; i < 1000; i++ {
		e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "progress", "message": "n"})
	}
	// The client must still receive a later event.
	e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "error", "message": "last"})
	e.waitEvent(t, events, func(ev string) bool { return strings.Contains(ev, `"message":"last"`) })
}
// notify_test.go
func TestRequestEventTargetsEventsRoute(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; w.WriteHeader(202) }))
	defer srv.Close()
	if err := Send(context.Background(), srv.URL+"/api/sessions/s1/attention", "tok", Request{Event: "progress", Message: "1/7"}); err != nil {
		t.Fatal(err)
	}
	if path != "/api/sessions/s1/events" {
		t.Fatalf("path %q", path)
	}
}
```
Run → FAIL.

- [ ] **Step 2: Implement.** Handler in `attention.go` (`handleEvent`), sharing the auth logic with `handleAttention` (extract `s.attentionPrincipal(r, d) (source string, ok bool)`). For event types: `local.Record(session.CleanEntry(ActivityEntry{Type, Message, URL, To, Tool, ByName: "agent"}))` (Local) or forward to the host via a new server→host `record{entry}`? No: hosted sessions receive API-originated events the same way attention does today (`HostAttentionMsg` forwarding). Add server→host `activity` too (same struct) and have the host `local.Record` it. Events SSE fan-out as specified; `readLimit` unchanged. `Send` rewrites the URL suffix `/attention` → `/events` when `Event != ""`. Host message handling in `ws_host.go` + `hostagent.onLocalActivity` (new `Options.OnActivity` in the host's Local → `a.send(HostActivityMsg)`).

- [ ] **Step 3: Run** `go test -race ./internal/...` → PASS. Docs: protocol.md host rows (`activity{sessionId, entry}` both directions), HTTP `POST /api/sessions/{id}/events`, SSE `activity` event. Commit `events: agent-token route, notify --event, host forwarding and admin SSE feed`.

---

### Task 3: Screen-pattern detector

**Files:**
- Create: `internal/session/pattern.go`, `internal/session/pattern_test.go`
- Modify: `internal/session/local.go` (feed chunks; `Options.Pattern *regexp.Regexp`; `SourcePattern = "pattern"`), `internal/api/sessions.go` (compile from `agent.EffectiveSignal()`), `internal/cli/host.go` + `internal/hostagent/agent.go` (`--signal-pattern` flag → `Options.Pattern`), `docs/protocol.md` (source `pattern`)

**Interfaces:**
```go
// StripANSI removes CSI, OSC (BEL or ST terminated) and 2-byte ESC sequences.
func StripANSI(b []byte) []byte
// LineTracker keeps the text of the last screen line as a stream arrives.
type LineTracker struct{ /* line []byte, cap 4096 */ }
func (t *LineTracker) Write(chunk []byte) // handles \n (new line), \r (rewrite from column 0), \b (backspace)
func (t *LineTracker) Last() string
// PatternWatcher fires once per quiet period when the last line matches.
type PatternWatcher struct{ /* re, quiet, timer, tracker, fire func(line string) */ }
func NewPatternWatcher(re *regexp.Regexp, quiet time.Duration, fire func(line string)) *PatternWatcher
func (w *PatternWatcher) Feed(chunk []byte) // resets the quiet timer
func (w *PatternWatcher) Stop()
```
`Local.pump` calls `w.Feed(chunk)` after `scanOutput`; `fire` calls `SetAttentionFull(AttentionNeedsInput, "prompt: "+line, SourcePattern, KindPrompt, nil)` only when the current state is not already `needs_input`.

- [ ] **Step 1: Failing tests**

```go
func TestStripANSIAndLineTracker(t *testing.T) {
	var lt LineTracker
	lt.Write([]byte("\x1b[32mhello\x1b[0m world\n\x1b]0;title\x07> "))
	if got := lt.Last(); got != "> " {
		t.Fatalf("last %q", got)
	}
	lt.Write([]byte("\rcodex› "))
	if got := lt.Last(); got != "codex› " {
		t.Fatalf("after CR %q", got)
	}
	lt.Write([]byte("\b\bx"))
	if got := lt.Last(); got != "codexx" {
		t.Fatalf("after BS %q", got)
	}
	lt.Write(bytes.Repeat([]byte("a"), 10000))
	if len(lt.Last()) > 4096 {
		t.Fatal("line not capped")
	}
}

func TestPatternWatcherFiresAfterQuiet(t *testing.T) {
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`^> $`), 50*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	w.Feed([]byte("working…\n"))
	w.Feed([]byte("> "))
	select {
	case l := <-fired:
		if l != "> " {
			t.Fatalf("fired with %q", l)
		}
	case <-time.After(time.Second):
		t.Fatal("did not fire")
	}
	// Continuous output (spinner) never goes quiet: no second fire.
	for i := 0; i < 10; i++ {
		w.Feed([]byte("\r| working"))
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case l := <-fired:
		t.Fatalf("fired during spinner: %q", l)
	case <-time.After(120 * time.Millisecond):
	}
}

func TestLocalPatternSetsNeedsInputOnce(t *testing.T) {
	var changes []Attention
	var mu sync.Mutex
	p := newFakeProc()
	s := NewLocal(Info{ID: "p", Cwd: t.TempDir(), Cols: 80, Rows: 24}, p, Options{Pattern: regexp.MustCompile(`\(Y\)es/\(N\)o`), OnChange: func(i Info) { mu.Lock(); changes = append(changes, i.Attention); mu.Unlock() }})
	t.Cleanup(p.exit)
	p.outW.Write([]byte("Add file.go to the chat? (Y)es/(N)o "))
	time.Sleep(700 * time.Millisecond)
	p.outW.Write([]byte("still (Y)es/(N)o "))
	time.Sleep(700 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for _, a := range changes {
		if a.State == AttentionNeedsInput && a.Source == SourcePattern {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("needs_input from pattern fired %d times, want 1: %+v", n, changes)
	}
}
```
Run → FAIL. **Step 2:** implement (`StripANSI` state machine: ESC `[` … final byte 0x40–0x7E; ESC `]` … BEL or ESC `\`; ESC + one byte otherwise). Watcher uses `time.AfterFunc`; `Feed` strips, writes the tracker, resets the timer; fire checks `re.MatchString(tracker.Last())`. **Step 3:** wire `Options.Pattern` in `handleCreateSession` from `agent.EffectiveSignal()` when `Kind == "pattern"`, and `--signal-pattern` on the host (validated with `regexp.Compile`, ≤ 200 bytes). The launch dialog's host command includes `--signal-pattern '<re>'` when the agent has one (client change in `hostCommand.ts`: new optional `pattern` field; test). Run → PASS. Commit `attention: screen-pattern detector for agents without hooks`.

---

### Task 4: Adapters package, assets, launch injection, hook mappers

**Files:**
- Create: `internal/agents/adapter.go`, `registry.go`, `assets.go`, `merge.go` (JSON/TOML/YAML-append helpers), one file per agent: `claude.go`, `codex.go`, `agy.go`, `copilot.go`, `cursor.go`, `opencode.go`, `pi.go`, `omp.go`, `aider.go`, `goose.go`, `amp.go`, `dsh.go`; tests `adapter_test.go`, `assets_test.go`, `merge_test.go`, `mappers_test.go`
- Modify: `internal/notify/notify.go` (mappers live here so `conductor notify` needs no import cycle: `MapCopilotHook`, `MapCursorHook`, `MapAgyHook`, `MapGooseHook`, `MapCodexHook`), `internal/cli/notify.go` (`--copilot-hook`, `--cursor-hook`, `--agy-hook`, `--goose-hook`, `--codex-hook`), `internal/api/sessions.go` (inject), `internal/hostagent/agent.go` + `internal/cli/host.go` (`--agent <id>` injects on the host), `internal/cli/serve.go` (write assets at start)

**Interfaces:**
```go
package agents

// Adapter knows how one agent reports to Conductor.
type Adapter struct {
	ID, Name string
	// Assets written under hooksDir at startup: relative path → content. {{BIN}} is
	// replaced by the absolute conductor binary path.
	Assets map[string]string
	// Inject returns extra argv and env for a launch, given the hooks dir. Empty
	// when the agent has no launch-time route.
	Inject func(hooksDir string, sig catalog.Signal) (argv []string, env map[string]string)
	// Install merges Conductor's hooks into the agent's own config under home.
	// Idempotent. Returns the files it touched. nil when the agent has no file route.
	Install func(home, hooksDir string) ([]string, error)
	// Status reports whether the install exists under home.
	Status func(home string) (installed bool, where string)
	// Snippet is what a host user pastes when Install is not available to them.
	Snippet func(hooksDir string) string
	// Events documents what this adapter can report (for the Events page).
	Events []string // e.g. "needs_input", "done", "tool_use"
}
func Get(id string) (Adapter, bool)
func All() []Adapter                                     // stable order
func WriteAssets(hooksDir, bin string) error             // writes every adapter's assets, 0600/0700
func InjectFor(agentID string, hooksDir string, sig catalog.Signal) ([]string, map[string]string)
```

Per-adapter specifics. Wherever a hook list below abbreviates an event with `[…]`, that event gets exactly the same single entry as the first one shown for that adapter (one `command` hook running the adapter's `conductor notify --<agent>-hook` argv, no matcher). Assets are complete files; the plan fixes their content here so an implementer does not guess:

- **claude** — asset `claude.json` = `{"hooks":{"Notification":[{"hooks":[{"type":"command","command":"{{BIN}} notify --claude-hook"}]}],"Stop":[…],"UserPromptSubmit":[…],"PermissionRequest":[…],"PermissionDenied":[…]}}`; asset `claude-tools.json` adds `PostToolUse`, `PostToolUseFailure`, `SubagentStop`. `Inject` → argv `["--settings", hooksDir+"/claude.json"]` (or `claude-tools.json` when `sig.ToolEvents`). `Install` merges those hook arrays into `~/.claude/settings.json` (parse, append entries whose command contains `notify --claude-hook` only if absent). `Status` reads the same file. Mapper: extend `MapClaudeHook`: `PermissionDenied` → `Request{Event:"tool_denied", Tool: tool_name}`, `PostToolUse` → `{Event:"tool_use", Tool: tool_name}`, `PostToolUseFailure` → `{Event:"error", Message: error, Tool}`, `SubagentStop` → `{Event:"progress", Message:"subagent finished"}`.
- **codex** — `Inject` → argv `["-c", `notify=["{{BIN}}","notify","--codex"]`, "-c", `tui.notification_method="bel"`]`; asset `codex-hooks.json` (PreToolUse/PostToolUse/Stop/PermissionRequest command `{{BIN}} notify --codex-hook`, gated on `features.hooks`); `Install` appends a marked block to `~/.codex/config.toml` (`# >>> conductor` … `# <<< conductor` with `notify = [...]`) and writes `~/.codex/hooks.json` if absent (never overwrite an existing one; return a "merge by hand" note). Mapper `MapCodexHook`: `Stop` → done, `PostToolUse` → tool_use, `PermissionRequest` → needs_input kind permission (no options until verified).
- **agy** — no inject; asset `agy-hooks.json` (`{"conductor":{"enabled":true,"Stop":[{"matcher":"*","hooks":[{"type":"command","command":"{{BIN}} notify --agy-hook"}]}],"PostToolUse":[…]}}`); `Install` writes `~/.gemini/config/hooks.json` if absent, else merges the `conductor` top-level key. Mapper: `Stop` → done (`terminationReason` in message), `PostToolUse` → tool_use (`toolCall.name`); the payload has no `hook_event_name`, so the CLI flag decides and the mapper switches on the presence of `toolCall` vs `terminationReason`.
- **copilot** — asset `copilot.json` (`{"version":1,"hooks":{"notification":[{"type":"command","bash":"{{BIN}} notify --copilot-hook"}],"agentStop":[…],"userPromptSubmitted":[…],"postToolUse":[…],"errorOccurred":[…]}}`); `Install` copies it to `~/.copilot/hooks/conductor.json` (own file). Mapper: `notification_type == "permission_prompt"` → needs_input kind permission (no options), other `notification` → needs_input kind prompt, `agentStop` → done, `userPromptSubmitted` → working, `postToolUse` → tool_use (`toolName`), `errorOccurred` → error. Copilot passes camelCase and no `hook_event_name` for most events: the mapper switches on which fields are present (`toolResult`, `stopReason`, `prompt`, `error`, `notification_type`).
- **cursor** — asset `cursor-hooks.json` (`{"version":1,"hooks":{"stop":[{"command":"{{BIN}} notify --cursor-hook"}],"postToolUse":[…],"afterFileEdit":[…]}}`); `Install` merges into `~/.cursor/hooks.json` (add entries whose command contains `notify --cursor-hook` if absent). Mapper: `hook_event_name` present: `stop` → done, `postToolUse` → tool_use, `afterFileEdit` → tool_use (`Tool: "edit " + file_path`). Signal default `pattern` with `^› $` (verify the CLI prompt glyph).
- **opencode** — asset `opencode/plugins/conductor.ts` (exports a plugin whose `event` handler switches on `event.type`: `permission.asked` → `$`{{BIN}} notify --state needs_input --message …` with kind prompt, `session.idle` → `--state done`, `tool.execute.after` → `--event tool_use --tool …`, `session.error` → `--event error`). `Inject` → env `OPENCODE_CONFIG_DIR=<hooksDir>/opencode` **only after the "additive" verification passes**; until then `Inject` is nil and `Install` copies the plugin to `~/.config/opencode/plugins/conductor.ts`.
- **pi** — asset `pi-conductor.ts` (`export default function (pi) { pi.on("agent_end", …spawnSync("{{BIN}}", ["notify","--state","done"])…); pi.on("tool_result", … --event tool_use …) }` using `node:child_process`); `Inject` → argv `["--extension", hooksDir+"/pi-conductor.ts"]`; `Install` copies to `~/.pi/agent/extensions/conductor.ts`.
- **omp** — same asset as pi under `omp-conductor.ts`; no inject; `Install` copies to `~/.omp/agent/extensions/conductor.ts` and appends its path to the `extensions:` list in `~/.omp/agent/config.yml` (line-based append under a `# conductor` marker; create the file if absent).
- **aider** — `Inject` → env `AIDER_NOTIFICATIONS=true`, `AIDER_NOTIFICATIONS_COMMAND={{BIN}} notify --state needs_input --message "aider is waiting"`; no assets; signal default `pattern` for `\(Y\)es/\(N\)o` confirmations.
- **goose** — asset `goose/hooks/hooks.json` (Stop, PostToolUse → `{{BIN}} notify --goose-hook`); `Install` copies the directory to `~/.agents/plugins/conductor/`. Mapper: `event == "Stop"` → done, `PostToolUse` → tool_use.
- **amp** — asset `amp/conductor/index.ts` (plugin registering `agent.end` → `--state done`, `tool.result` → `--event tool_use`); `Install` copies to `~/.config/amp/plugins/conductor/`.
- **dsh** — asset `dsh-conductor.js` (plugin subscribing to `session completed/failed`, `question-asked`, `permission-requested` → notify); `Install` returns an error "install by hand: developer preview" with the snippet; marked experimental in `Events`.

Launch injection in `handleCreateSession`: after `argv` is built, `extra, env := agents.InjectFor(agent.Adapter, s.hooksDir, agent.EffectiveSignal())`; `argv = append(argv, extra...)`; env merged into the `set` map passed to `pty.BuildEnv`; per-agent `EnvPassthrough` appended to `extraAllow`. Skipped when `agent.EffectiveSignal().Kind != "hook"`. On the host: `conductor host --agent claude -- claude` does the same with the host's hooks dir (`$XDG_STATE_HOME/conductor/hooks` or `~/.conductor/hooks`, assets written on first run).

- [ ] **Step 1: Failing tests** (representative; every adapter gets the same three)

```go
func TestWriteAssetsAndInjectClaude(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAssets(dir, "/opt/conductor"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "claude.json"))
	if err != nil || !strings.Contains(string(b), `"/opt/conductor notify --claude-hook"`) || strings.Contains(string(b), "{{BIN}}") {
		t.Fatalf("asset: %v %s", err, b)
	}
	argv, env := InjectFor("claude", dir, catalog.Signal{Kind: "hook"})
	if len(argv) != 2 || argv[0] != "--settings" || argv[1] != filepath.Join(dir, "claude.json") || len(env) != 0 {
		t.Fatalf("inject: %v %v", argv, env)
	}
	argv, _ = InjectFor("claude", dir, catalog.Signal{Kind: "hook", ToolEvents: true})
	if argv[1] != filepath.Join(dir, "claude-tools.json") {
		t.Fatalf("tool events: %v", argv)
	}
	if argv, env := InjectFor("claude", dir, catalog.Signal{Kind: "bell"}); argv != nil || env != nil {
		t.Fatal("bell signal must not inject")
	}
}

func TestInstallClaudeIsIdempotentAndPreservesUserHooks(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o700)
	os.WriteFile(settings, []byte(`{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`), 0o600)
	a, _ := Get("claude")
	for i := 0; i < 2; i++ {
		if _, err := a.Install(home, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(settings)
	var doc map[string]any
	json.Unmarshal(b, &doc)
	if doc["model"] != "opus" {
		t.Fatal("user settings lost")
	}
	stop := doc["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 { // user's entry + one conductor entry, not two
		t.Fatalf("Stop hooks: %d", len(stop))
	}
	if ok, where := a.Status(home); !ok || where != settings {
		t.Fatalf("status %v %q", ok, where)
	}
}

func TestInjectUnknownAdapterIsNoop(t *testing.T) {
	if argv, env := InjectFor("", t.TempDir(), catalog.Signal{Kind: "hook"}); argv != nil || env != nil {
		t.Fatal("no adapter must inject nothing")
	}
}
// notify/mappers_test.go
func TestMapCopilotHook(t *testing.T) {
	r, ok := MapCopilotHook([]byte(`{"sessionId":"s","timestamp":1,"cwd":"/x","hook_event_name":"Notification","message":"Allow bash?","notification_type":"permission_prompt"}`))
	if !ok || r.State != "needs_input" || r.Kind != "permission" {
		t.Fatalf("%+v", r)
	}
	r, ok = MapCopilotHook([]byte(`{"sessionId":"s","timestamp":1,"cwd":"/x","toolName":"bash","toolArgs":{},"toolResult":{"resultType":"success","textResultForLlm":"ok"}}`))
	if !ok || r.Event != "tool_use" || r.Tool != "bash" {
		t.Fatalf("%+v", r)
	}
	if _, ok := MapCopilotHook([]byte(`{"sessionId":"s"}`)); ok {
		t.Fatal("empty payload must not map")
	}
}
```
Equivalent tests for `MapCursorHook` (`stop` → done; `afterFileEdit` → tool_use), `MapAgyHook` (`terminationReason` → done; `toolCall.name` → tool_use), `MapGooseHook`, `MapCodexHook`, and for each adapter's `Install` on an empty home (creates the file) and on an existing file (merges). Run → FAIL.

- [ ] **Step 2: Implement** `merge.go` helpers first: `mergeJSONHooks(path string, key string, entries []any, marker string) (changed bool, err error)` (parse or start `{}`, ensure `hooks[key]` array, append entries whose serialised form contains `marker` only when no existing entry contains it, write back with the original indentation of 2), `appendMarkedBlock(path, begin, end, body string)` for TOML/YAML, `copyFile(src, dst)`. Then adapters and mappers. `serve.go`: `agents.WriteAssets(filepath.Join(cfg.DataDir, "hooks"), exe)` where `exe, _ := os.Executable()`.

- [ ] **Step 3: Run** `go test -race ./internal/agents/ ./internal/notify/ ./internal/api/` → PASS. Commit `agents: adapter registry with launch injection, on-demand install and hook mappers`.

---

### Task 5: Built-in catalog, integrations API, `conductor hooks`, the skill

**Files:**
- Modify: `internal/catalog/defaults.go`, `internal/catalog/catalog_test.go`
- Create: `internal/api/integrations.go`, `internal/cli/hooks.go`, `internal/agents/skill.go` (the SKILL.md text as a Go string, written as asset `skills/conductor/SKILL.md`)
- Modify: `internal/cli/root.go`, `internal/api/server.go`, `internal/api/api_test.go`, `web/app/composables/useSessions.ts`, `README.md`

**Interfaces:**
- Defaults (id → command → adapter → signal): `claude`/`claude`/claude/hook · `codex`/`codex`/codex/hook · `agy`/`agy`/agy/bell · `copilot`/`copilot`/copilot/hook · `cursor`/`cursor-agent`/cursor/pattern `^› $` · `opencode`/`opencode`/opencode/hook · `pi`/`pi`/pi/hook · `omp`/`omp`/omp/hook · `aider`/`aider`/aider/hook · `goose`/`goose`/goose/bell · `amp`/`amp`/amp/hook · `dsh`/`dsh`/dsh/none · `shell`/`/bin/bash -l`/—/bell. All `AllowArgs: true` except shell. Icons from lucide as in the mockup (`i-lucide-sparkles`, `i-lucide-code-xml`, `i-lucide-rocket`, `i-lucide-github`, `i-lucide-mouse-pointer-2`, `i-lucide-braces`, `i-lucide-pi`, `i-lucide-pi-square`, `i-lucide-git-commit`, `i-lucide-feather`, `i-lucide-zap`, `i-lucide-cpu`, `i-lucide-terminal`).
- `GET /api/integrations` (admin) → `{"integrations":[{"id","name","events":[…],"launchInjection":bool,"installed":bool,"where":"…","snippet":"…","experimental":bool}]}` evaluated against the server user's home.
- `POST /api/integrations/{id}/install` (admin) → 200 `{"changed":[…]}` or 400 `no_file_route` / 500 `install_failed`. Only touches the server user's home.
- `conductor hooks install <adapter>|all [--home DIR] [--data-dir DIR]` and `conductor hooks status`; exit 0 on success, prints touched files.
- `conductor skill` prints the SKILL.md; `Install` for claude/codex/pi/goose also copies it to their skill dirs (`~/.claude/skills/conductor/SKILL.md`, `~/.codex/skills/conductor/SKILL.md`, `~/.agents/skills/conductor/SKILL.md`).
- SKILL.md content (verbatim in `skill.go`): frontmatter `name: conductor`, `description: Report progress, artifacts, blockers and handoffs to Conductor while working in a Conductor session`; body: when to run `conductor notify --event progress --message "4/7 handlers"`, `--event artifact --url <url>`, `--event handoff --to <member> --message "…"`, `--state needs_input --message "…"` for decisions the agent cannot make, and the rule "never call notify outside a Conductor session; the command exits silently there".

- [ ] **Step 1: Failing tests** — `catalog_test.go`: `TestDefaultsHaveAdaptersAndValidSignals` (every default with `Adapter != ""` resolves in `agents.Get`; every signal validates; ids unique; `shell` has no adapter). `api_test.go`: `TestIntegrationsListAndInstall` (list has 12 entries with adapters; `POST /api/integrations/copilot/install` with `HOME` pointed at `t.TempDir()` via `e.srv.home` override creates `.copilot/hooks/conductor.json`; second call reports `changed: []`; `dsh` returns 400 `no_file_route`). CLI: `TestHooksInstallCLI` in `internal/cli/cli_test.go` (new) runs `runHooks(ctx, []string{"install","copilot","--home",dir,"--data-dir",dir2})` and checks the file.
- [ ] **Step 2: Implement.** **Step 3:** README: replace the hand-written hook snippets section with "Events and hooks" describing launch injection, `conductor hooks install`, the Events page and the skill; keep the manual snippets in a collapsed "by hand" list. Commit `agents: thirteen built-ins, integrations API, conductor hooks and the Conductor skill`.

---

### Task 6: Events page, routing and feed in the workbench

**Files:**
- Create: `web/app/composables/useEvents.ts`, `web/app/pages/events.vue`, `web/app/components/IntegrationCard.vue`, `web/app/components/RoutingMatrix.vue`, `web/app/components/EventFeed.vue`, `web/app/utils/events.ts` (+test)
- Modify: `web/app/layouts/default.vue` (nav entry Events with `New` tag for one release), `web/app/composables/useAttention.ts` (SSE `activity` → `useEvents().push`; consult routes before notification/chime/wall jump), `web/app/components/SessionInspector.vue` (icons and link rendering for the new types), `web/app/components/WallQueue.vue` (no change), `web/app/pages/wall.vue` (jump honours routes), `web/app/composables/useShortcuts.ts` (`g-e`, `alt_e`)

**Interfaces:**
```ts
// utils/events.ts
export type EventType = 'needs_input' | 'done' | 'working' | 'tool_denied' | 'progress' | 'artifact' | 'handoff' | 'error' | 'exit_nonzero' | 'tool_use'
export interface RouteRow { badge: boolean; browser: boolean; wall: boolean; feed: boolean }
export const DEFAULT_ROUTES: Record<EventType, RouteRow>   // mirrors the mockup matrix: needs_input all on; done badge+feed; working feed; tool_denied badge+browser+feed; progress feed; artifact browser+feed; handoff wall+feed; exit_nonzero badge+browser+feed; tool_use feed
export function eventTypeOf(entry: ActivityEntry, session?: SessionInfo): EventType | null  // attention entries map by state; status entries with a non-zero exit → exit_nonzero
export function linkableUrl(url?: string): string | null   // only http(s) URLs ≤ 2048
// composables/useEvents.ts
export function useEvents(): { entries: ComputedRef<Array<ActivityEntry & { sessionId: string }>>; push(sessionId: string, e: ActivityEntry): void; routes: Ref<Record<EventType, RouteRow>>; setRoute(t: EventType, k: keyof RouteRow, v: boolean): void }
```
Routes persist in localStorage `conductor.events.routes`. The feed ring keeps 500 entries across sessions.

- [ ] **Step 1: Failing tests** `events.test.ts`: `DEFAULT_ROUTES` has every type; `eventTypeOf` maps `{type:'attention', message}` + session state `needs_input` → `needs_input`, `{type:'status', message:'exited (exit 1)'}` → `exit_nonzero`, `{type:'status', message:'exited (exit 0)'}` → `null`; `linkableUrl('javascript:alert(1)')` → `null`, `linkableUrl('https://x')` → `'https://x'`, a 3000-char URL → `null`.
- [ ] **Step 2: Implement** utils and composable. **Step 3:** page: left column integration cards (`GET /api/integrations`: avatar, name, status badge "n hooks installed" / "injected at launch" / "not configured on <host>" / "experimental", snippet code block with Copy, "Install on this machine" → `POST …/install` with toast of touched files; the skill card with the three commands and "Install skill"), right column `RoutingMatrix` (checkbox grid; webhook column shown disabled with tooltip "configure webhooks in conductor.json" until Task 7) and `EventFeed` (live lines `time type session · message`, artifact rows link when `linkableUrl`). `useAttention.react` consults `routes.needs_input.browser` before notifying and `routes.<type>.browser` for artifact/tool_denied/error/exit_nonzero; wall follow/jump consults `routes.needs_input.wall` and `routes.handoff.wall`. **Step 4:** typecheck, headless browser check (Events page renders, install button creates a file in a temp `HOME`), commit `web: events page with integrations, routing and live feed`.

---

### Task 7: Webhooks (server-side routing target)

**Files:**
- Modify: `internal/config/config.go` (`Webhooks []Webhook{URL, Events []string, Secret string, AllowPrivate bool}`), `internal/api/webhooks.go` (new), `internal/api/webhooks_test.go`, `README.md`, `web/app/components/RoutingMatrix.vue` (webhook column reflects configured events, read-only)

**Interfaces:** `webhookSender` subscribes to `eventHub` activity; for each entry whose type is listed, POSTs `{"sessionId","session":{id,name,agentId},"entry":{…}}` with `Content-Type: application/json`, `X-Conductor-Event: <type>`, `X-Conductor-Signature: sha256=<hex HMAC of body>`; queue 256 per webhook (drop oldest), 5 s timeout, no redirects, resolves the host and refuses loopback/link-local/private ranges unless `AllowPrivate`.

- [ ] **Step 1: Failing test** with `httptest.Server` receiving one POST with a valid signature for a `progress` event and none for `tool_use`; a webhook to `http://127.0.0.1:1` without `allowPrivate` is refused at config validation. **Step 2:** implement. **Step 3:** docs; commit `events: signed webhooks per event type`.

---

## Verification

1. Full gate: `make lint && go test -race -count=1 ./... && npm --prefix web run typecheck && npm --prefix web test && make web-build && make build-go`.
2. `bin/conductor serve --config conductor.example.json`: `conductor.d/hooks/` contains the assets with the absolute binary path; launch `claude` from the workbench → `ps` shows `claude --settings …/hooks/claude.json`; trigger a permission prompt → Yes/Always/No buttons; run `conductor notify --event artifact --url https://example.com/pr/1` inside the session → the Events feed and the session Activity tab show a linked artifact.
3. Launch `aider` → the process env has `AIDER_NOTIFICATIONS_COMMAND`; finish a turn → needs-input badge.
4. Events page → Copilot → Install on this machine → `~/.copilot/hooks/conductor.json` exists; second click reports nothing changed.
5. Add a catalog entry with `pattern ^> $` running `sh -c 'printf "> "; read x'` → needs-input after half a second; typing clears it; a spinner loop never triggers it.
6. Each matrix "verify" cell is exercised against the real CLI and the outcome recorded in `docs/features.md`.
