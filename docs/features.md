# Features

Scope notes for the workbench redesign. The source mockup is the
claude.ai/design project "Conductor Mockups" (screens 1a workbench, 1b wall,
1c runs, 1d launch, 1e share, 1f join, 1g carousel). This file records what is
being built, the decisions taken along the way, and what each round left
for a person to verify. The bugs known and what is deliberately
deferred are listed once, in [tasks-todo.md](tasks-todo.md); the "Deferred"
lists under the rounds below are the history of when each item was set
aside.

## Delivered (2026-09-28)

Everything under Planned below shipped on the `workbench-redesign` branch.
The list is kept as the description of what exists; see `docs/protocol.md`
for the wire details.

## Delivered (2026-09-30)

- **Crews** (2a Crews, 2b Crew running). Saved teams of up to 12 agents with
  role prompts, start conditions and optional per-member git worktrees, run
  from the Crews page or `conductor up`; the run page has tiles, handoffs,
  broadcast, run links and stop. See the README (Crews) and
  `docs/protocol.md` (Crew runs); hosted crews and worktree cleanup are below
  under Future features.

## Delivered (2026-10-01)

- **Round 3**: the deferred items of rounds 1 and 2 and the crew storage of
  plan 1; then the sidebar rail, example crews, the working-directory picker
  with the git check, agent availability, shell completion and fullscreen on
  every page. See the README (Sidebar and keyboard shortcuts, Crews, Agent
  catalog, Shell completion) and `docs/protocol.md` (`GET /api/paths`,
  `GET /api/git/check`, `POST /api/crews/examples`, `available` and `site` on
  `GET /api/catalog`). The round 3 decisions below describe what exists.

## Planned

### Client only (no protocol change)

- **Status colours.** Needs-input is amber, running is green, idle and exited
  are grey. Terracotta stays on the brand mark only, per
  [design/brand.md](design/brand.md). JetBrains Mono for code and terminal
  chrome.
- **Workbench** (1a): the sidebar becomes the session list grouped into Needs
  you, Running and Exited, with a filter box. The selected session fills the
  main pane. A right-hand inspector has People, Files and Activity tabs; Files
  replaces the current slideover. The Sessions table page is removed.
- **Wall** (1b): header chips All / Needs you / Running; a queue panel that
  lists every session waiting for input with its attention message and a
  reply box that sends text plus Enter over a control connection.
- **Carousel** (1g): film strip of all sessions with a rotation progress bar,
  a "next up" line, and a "jumped here" note when follow mode moved. Follow
  mode holds for two intervals (at least 20 s), not forever, so a stray
  prompt cannot freeze the rotation.
- **Launch** (1d): "Runs on: Server / My machine". The latter shows the exact
  `conductor host` command, including the host token, and waits for a hosted
  session with that name to appear.
- **Share** (1e) and **Join** (1f): restyle. Join adds a display-name field.

### Needs protocol or server work

Each item touches `internal/proto`, `web/app/utils/protocol.ts` and
`docs/protocol.md` together.

- **Presence.** Display name per connection, a viewer roster (id, name, role,
  link label, since) in the viewers message, and a per-subscriber last-input
  time so "typing" is derived server side. Names are labels, not
  authentication; the UI says so.
- **Structured attention.** Optional `kind` and `options` on attention reports
  so quick-reply buttons render only when the prompt shape is known
  (Claude Code permission requests). Otherwise the UI shows a plain reply box.
  Buttons disable the moment the attention state changes.
- **Activity log.** Bounded per-session log of attention transitions, inputs
  during needs-input, link changes and joins. Feeds the Activity tab and the
  wall's Answered list.
- **Session metadata.** Git branch (read at launch for server sessions,
  reported by the host for hosted ones), host OS user, per-link viewer count,
  client-measured transport round-trip time.

## Round 2: crews, adding agents, events and hooks (planned 2026-09-29)

Source: claude.ai/design project "Conductor Mockups v2" (`Conductor Mockups
v2.dc.html`, screens 2a Crews, 2b Crew running, 2c Add agent, 2d Events).
Plans: `docs/superpowers/plans/2026-09-29-data-dir-and-catalog-editing.md`,
`2026-09-29-events-and-agent-adapters.md`, `2026-09-29-crews.md`, executed in
that order.

### Decisions

- **Persistence.** A writable data directory (`dataDir`, env
  `CONDUCTOR_DATA_DIR`, default `~/.conductor` since round 3, `conductor.d`
  next to the config file before) holds `catalog.json` (agents added or hidden
  from the UI), `crews/` (one file per crew), generated hook assets under
  `hooks/` and the Conductor skill. The config file stays read-only; the data
  directory overlays it.
- **Hook wiring.** Inject at launch wherever the agent's CLI allows it
  (Claude Code `--settings`, Codex `-c`, pi `--extension`, aider env vars,
  OpenCode `OPENCODE_CONFIG_DIR`); everything else gets an explicit
  "Install on this machine" that merges a Conductor block into the agent's own
  config, plus copyable snippets and `conductor hooks install <agent>` for
  hosts. Conductor never edits an agent's files without a click or a command.
- **Events are a superset of attention.** The per-session activity log
  already broadcasts join, leave, attention, input, link and status entries.
  Round 2 adds `progress`, `artifact`, `handoff`, `tool_use`, `tool_denied`
  and `error`, a `POST /api/sessions/{id}/events` route the agent token may
  call, `conductor notify --event`, and an admin SSE feed of every entry.
  Attention state (`needs_input`, `working`, `done`) is unchanged and still
  derived from the same reports.
- **Supported agents.** Events and one-click install exist only for agents
  with an adapter (table below). Any other catalog entry still works with
  bell/OSC, the new screen-pattern detector, or `conductor notify` called by
  hand. Gemini CLI is not added: it is superseded by Antigravity (`agy`).
- **Crews** (2a and 2b) shipped in full on 2026-09-30; see Delivered above.

### Agent adapter matrix

| Agent | Command | Waiting-for-input signal | Turn done | Tool events | Wiring | Verify before shipping | Yolo recipe (what it turns off) | Trust prompt | Session recipe (id; resume) |
|---|---|---|---|---|---|---|---|---|---|
| Claude Code | `claude` | `Notification`/`PermissionRequest` hooks (with options); `idle_prompt` (a minute at rest) is done, not a question (round 4) | `Stop` | `PostToolUse`, `PermissionDenied`, `SubagentStop` | launch: `--settings <hooks/claude.json>` | — | `--dangerously-skip-permissions`, `skipDangerousModePermissionPrompt` in the `--settings` file (every permission prompt; `.git`, `.claude` writable; deny rules hold; the trust question stays) — live, 2.1.287 | `Is this a project you created or one you trust?` — live; no per-launch trust: trust the repository once (its worktrees follow) | set `--session-id {uuid}`, then hook `session_id` (latest); `--resume {id}` — live |
| Codex CLI | `codex` | the bell (`tui.notification_method="bel"`) and the `PermissionRequest` hook; the end of a turn is done, not a question (round 4) | `notify` (agent-turn-complete) → done; the hidden title thread's turn is ignored | experimental `hooks.json` (`features.hooks`), not on Windows | launch: `-c notify=[…]`; file: `~/.codex/config.toml` + `~/.codex/hooks.json` | `-c` array syntax; PermissionRequest hook shape | `--dangerously-bypass-approvals-and-sandbox` (every approval and the sandbox) and per launch `-c projects={"<repo>"={trust_level="trusted"}}` — live, 0.159.0 | `Trust this folder?` (Enter accepts and saves it) — live | not settable; notify `thread-id` (lowest: the title thread reports too); `resume {id} -c tui.resume_cwd="session"`, same directory — live |
| Antigravity | `agy` | bell (no notification event) | `Stop` | `PreToolUse`/`PostToolUse` (`toolCall.name`) | file: `~/.gemini/config/hooks.json` (global) or `.agents/hooks.json` (project) | exact global path | `--dangerously-skip-permissions` (tool permission requests; its trust dialog stays, trusted per exact path) — help | — | not settable; hook `conversationId` (installed hooks only); `--conversation {id}`, same directory — live (`-p`) |
| Copilot CLI | `copilot` | `notification` (`permission_prompt`) | `agentStop` | `postToolUse`, `errorOccurred` | file: `~/.copilot/hooks/conductor.json` (own file, no merge) | permission prompt key bindings for options | `--yolo`, `COPILOT_ALLOW_ALL=true` (every tool, path and URL; the env var also skips its trust question; deny rules win) — live, 1.0.59 | — (the recipe skips it) | set `--session-id {uuid}`, hook `sessionId`; `--session-id {id}` — live (the hook's `sessionId` not live) |
| Cursor CLI | `cursor-agent` | bell / pattern (CLI fires no waiting event) | `stop` | `postToolUse`, `afterFileEdit` | file: `~/.cursor/hooks.json` (merge) | CLI binary name; which events the CLI fires today | `--yolo --trust` (command and MCP approvals; `--trust` skips workspace trust) — docs | — (the recipe skips it) | not settable; hook `conversation_id` (installed hooks only); `--resume {id}`, same directory — docs |
| OpenCode | `opencode` | plugin `permission.asked`, `session.idle` | `session.idle` | `tool.execute.after`, `session.error` | launch: `OPENCODE_CONFIG_DIR=<hooks/opencode>` if additive, else file: `~/.config/opencode/plugins/conductor.ts` | whether `OPENCODE_CONFIG_DIR` adds to or replaces the default dir | `--auto` (every permission not explicitly denied) — docs | — | none this round (plugin capture deferred): Relaunch |
| pi | `pi` | `agent_end` | `agent_end` | `tool_call`/`tool_result` | launch: `--extension <hooks/pi-conductor.ts>` | — | none: pi has no permission system | — | set `--session-id {uuid}`; `--session-id {id}`, same directory — source |
| oh-my-pi | `omp` | as pi | as pi | as pi | file: `extensions:` entry in `~/.omp/agent/config.yml` | pi extension API compatibility (issue #2166) | `--yolo` (approvals unless a deny policy matches; its default already) — docs | — | none this round (plugin capture deferred): Relaunch |
| aider | `aider` | `--notifications-command` | same | none | launch: `AIDER_NOTIFICATIONS=true`, `AIDER_NOTIFICATIONS_COMMAND` | confirmations (`(Y)es/(N)o`) need the pattern detector | `--yes-always` (every confirmation, shell commands included) — docs | — | none this round (its handle is a chat-history file): Relaunch |
| Goose | `goose` | bell / pattern (no waiting event yet) | `Stop` | `PostToolUse` | file: `~/.agents/plugins/conductor/hooks/hooks.json` | — | `GOOSE_MODE=auto` (tool approvals; its default already) — docs | — | set `session --name cdr-{uuid}`; `session --resume --name {id}`, same directory — source |
| Amp | `amp` | `agent.end` (in-process plugin) | `agent.end` | `tool.call`/`tool.result` | file: `~/.config/amp/plugins/conductor/` | plugin API surface | `--dangerously-allow-all` (every tool approval; the flag in the rebuilt CLI unconfirmed) — docs | — | none this round (plugin capture deferred): Relaunch |
| DeepSeek Harness | `dsh` (with the TUI plugin) | plugin events `question-asked`, `permission-requested` | `session completed/failed` | none | file: dsh plugin | developer preview; plugin API changes | `DSH_PERMISSION_MODE=danger-full-access` (its sandbox and every approval) — source | — | none (unverified): Relaunch |
| Shell / anything | any argv | bell / OSC / pattern | exit | none | — | — | none | — | none: Relaunch |

### Open verification (round 2)

- Each "Verify before shipping" cell above is a manual check against the
  real CLI before that adapter is marked supported on the Events page.
- The screen-pattern detector strips ANSI sequences and matches the last
  line after 500 ms of silence; TUIs that redraw continuously (spinners) may
  never go quiet. Ship it as opt-in per catalog entry.
- **Crews: Claude Code's workspace-trust dialog.** A member in a fresh
  worktree starts in a directory Claude Code has not seen, so it may open with
  its "trust this folder" dialog. The run engine types the role prompt once the
  session is ready (the agent waits for input, or its output has gone quiet) and
  cannot tell that dialog from the prompt, so the dialog may take the typed
  prompt. Launch a crew of `claude` members with worktrees in a repository
  Claude Code has not trusted and see whether the prompts land.
  Answered in round 4: the dialog appears even with the bypass flag, and the
  typed Enter answered it (Claude Code chose "No, exit"); the run now holds
  the prompt while the question shows (Crew runs in `docs/protocol.md`), and a
  worktree of a trusted repository asks nothing.
- **Crews: long one-line role prompts.** A role prompt is typed as one line,
  its line breaks and tabs made spaces as a handoff's or a broadcast's are,
  with a carriage return at the end, so no agent takes the second line of a
  prompt as a further message. What is left to check is whether each agent TUI
  that matters takes a long single line (a paragraph of a few thousand
  characters) as one message, rather than truncating it or treating a fast
  burst of input as a paste; bracketed paste stays the deferred fix for agents
  that need it.
  Answered in round 4: Codex took every one-write prompt as a paste (a
  newline, not Enter) and Claude Code any over 800 bytes; prompts, handoffs,
  broadcasts and replies now go in as a bracketed paste and a separate Enter.
- **Crews: end to end.** In a git checkout under the allowed roots, create the
  crew `api-sweep` with worktree isolation and the members `lead` (`claude`,
  immediately), `core` (`claude`, immediately) and `tests` (`shell`, after
  `core`), and launch it.
  - `git worktree list` shows the new worktrees; the lead and core terminals
    received their prompts; when core first reports done, the tests session
    starts.
  - From core's terminal run `conductor notify --event handoff --to tests
    --message "/v1/users ready"`: the tests terminal shows `Handoff from core:
    /v1/users ready`.
  - Tick two tiles and broadcast a line: it lands in both. Flag one member as
    needing input (`conductor notify --state needs_input` in its terminal) and
    broadcast again: it is skipped and named in the toast.
  - **Share crew**, open the link in a private window: every member tile is
    there, read-only.
  - `conductor up api-sweep` from a shell with `CONDUCTOR_WORKBENCH_TOKEN` set
    prints the run URL.

## Open verification

- **Claude Code permission keys.** Quick-reply buttons send the digits `1`,
  `2`, `3` with no Enter, assuming Claude Code's permission dialog confirms
  on the number key and offers three choices. This has not been checked
  against a live Claude Code prompt from this repository's checks (no agent
  runs in CI). First real use: trigger a permission prompt, press **Yes** in
  the reply bar, and confirm Claude proceeds. If it only highlights, change
  `permissionOptions()` in `internal/notify/mappers.go` to send `"1\r"` etc.

## Decisions

- **Theme is system wide.** One dark or light setting for the whole app. The
  mockup's dark wall next to a light workbench is not a per-screen theme; the
  wall follows the global mode.
- **No join preview.** The mockup showed a snippet of terminal output on the
  join page before the guest steps in. Removed: a share URL fetched by a link
  unfurler or crawler would expose session content without anyone choosing to
  join. The join page shows only the session name, agent, host and the role the
  link grants. Content appears only after the guest joins.
- **No Runs nav item** until there is something behind it.

## Future features

Collected here so they are not lost. None of these block the planned work.

- **Keyboard turn-taking.** A per-session mode (anyone with control, or take
  turns) with a single keyboard holder. Input from non-holders is rejected with
  an error; a control message takes or releases the keyboard; the People tab
  shows who holds it. Lives in `internal/session.Local` so server and host
  behave the same.
- **Hosted crews.** Members that run on `conductor host` (a developer's
  machine) instead of the server. The editor's **My machine** option is
  disabled and the server refuses to launch a crew saved with `where: host`.
  It needs the server to start sessions on a host, make worktrees there and
  type into a hosted session (prompts, handoffs, broadcast); none of that
  exists yet.
- **Automatic worktree cleanup.** Conductor never deletes a worktree or a
  branch, whether the run stops or the crew is deleted; they are removed by
  hand (`git worktree remove`, `git branch -d`). Removing a stopped run's
  worktrees and merged branches, after a confirmation, is future work.
- **Runs (orchestration).** Mockup screen 1c. A run is a spec
  (`conductor.run.yaml`) of stages: plan, parallel build steps in per-step
  worktrees, verify, and a human merge gate with named approvers. Every step
  is an ordinary session that can be opened, shared or taken over. The run
  page shows the stage graph, a Spec / Events / Artifacts inspector, and
  `on_needs_input: queue` routes step prompts into the wall queue. Presence
  and the activity log above are prerequisites for gates and approvers.

## Round 3: polish, examples and completion (planned 2026-10-01)

Decided with the user on 2026-10-01. Two plans, run in order: first close every
open deferred item from rounds 1 and 2, then the features below.

### Decisions

- **Sidebar rail.** Hiding the sidebar collapses it to a narrow icon rail
  instead of removing it. The rail keeps every function: the mark (artwork),
  a Launch button, the filter as a search icon that expands the sidebar, the
  nav entries as icons with tooltips, and the session list as agent avatars
  with the attention dot and the crew grouping (a thin label when a run is
  active). Clicking an avatar opens the session; the rail has an expand
  button; `meta+B` / Alt+B toggles rail and full width. The choice is
  persisted per browser as before. Narrow screens keep the slideover.
- **Example crews.** `conductor serve --examples` (env `CONDUCTOR_EXAMPLES` set
  to `1` or `true`) seeds example crews once: ids `example-todo-app`,
  `example-test-fixer`, `example-docs-writer`, `example-dependency-upgrade`; a
  crew whose id exists is left alone. Members use the `claude` and `codex`
  built-ins with real role prompts and start conditions (the todo app: a lead
  that plans, two builders after the lead, a tester after the second builder,
  since a start condition names one member; every member that starts after
  another begins by merging that member's branch,
  `crew/$CONDUCTOR_RUN/<member>`, because a worktree starts from `HEAD`); `cwd`
  is the server's default working directory; isolation `worktree`. The empty
  Crews page offers "Load the examples", which calls `POST /api/crews/examples`
  (admin, same seeding, same once-only rule). The examples are data, not
  built-ins: editing or deleting them is normal.
- **Working-directory autocomplete.** `GET /api/paths?prefix=<path>&limit=50`
  (admin) lists child directories of the longest existing directory in the
  prefix, confined to `allowedRoots` through the same resolution as
  `resolveCwd` (symlinks resolved, nothing outside a root), at most 50
  entries, hidden directories only when the typed segment starts with a dot,
  each entry with `git: {repo, commits}` so the picker shows which
  directories a worktree crew can use. The crew editor and the Launch dialog
  use one `DirInput` combobox fed by it (debounced, keyboard navigable).
  Enter takes an entry only once the highlight was moved (the arrows, or the
  pointer over an entry) since the text last changed: the list highlights its
  first entry by itself, and Enter on a complete path must keep it rather
  than take its first child. A prefix outside the roots is refused with a
  message that names the allowed roots, which the picker shows as it is. A
  listing cut short by its 5 s deadline, entries left unmarked, says
  `truncated`.
- **Git check.** `GET /api/git/check?cwd=<path>` (admin) answers
  `{inRepo, toplevel?, hasCommit, message}` with the same rules the launch
  uses (`git rev-parse` as argv, under the allowed roots). The crew editor
  shows the state beside the working directory and explains why Launch with
  worktree isolation would be refused; the launch handler's 409 stays as the
  authority.
- **CLI completion.** `conductor completion zsh|bash` prints a completion
  script: subcommands, flags and their values statically; crew ids for
  `conductor up` dynamically through `conductor crews --ids` (ids only, one
  per line, exit 0 and silent when the server is unreachable or the token is
  missing), using `CONDUCTOR_SERVER` and `CONDUCTOR_WORKBENCH_TOKEN` from the
  environment. `conductor completion install` appends one marked `source`
  line to `~/.zshrc` or `~/.bashrc` (idempotent; refuses a file, a link or a
  directory not owned by the user). No new dependency.
- **Fullscreen everywhere.** The document-fullscreen toggle that the wall and
  the carousel have (`useFullscreenToggle`, the `F` key, Alt+F in a terminal)
  moves to the layout: one button in every page header and one shortcut
  registration, so the session page, the crew views, the join page, Events,
  Agents and Crews all toggle it; the wall and carousel keep their buttons.
  `F` is ignored while an input or textarea has focus, as the other letter
  shortcuts already are, and while a select (a combobox or listbox, whose
  typeahead takes letters) has focus.
- **Agent availability.** `GET /api/catalog` reports `available` per agent (whether `command[0]` resolves on the server through the same check `POST /api/catalog/check` uses, re-evaluated per request and cached for 30 s; any program of that name on the server's PATH counts, so a namesake such as the Go migration tool `goose` makes the Goose agent show as installed, and a relative path such as `./agent.sh` counts as installed because it resolves in the session's directory, which the check reports as `unknown: "relative"`, resolved at launch; a lookup that does not answer within 2 s, or before the client goes away, counts as installed too and its answer is kept when it lands, so a `PATH` entry on a stalled mount cannot hold up the listing, the Launch dialog or a launch, whose spawn still fails if the program is missing) and `site` (the agent's website, a field on built-ins and an optional field on saved agents). The Agents page greys out an agent that is not installed and says "Not installed on <host>", and links to the site of any agent that has one; the Launch dialog's server tab lists only available agents and, when none is, says so with a link to the Agents page; the "My machine" tab lists every agent because availability there is the host's. The crew editor's agent select marks unavailable agents the same way and the launch route refuses a crew whose member agent is not installed with `invalid_crew` naming it.
- **Deferred items.** Every open item from the rounds 1 and 2 ledgers is
  closed in the first plan, whatever its kind. A triage on 2026-10-01 found
  five already closed and two obsolete; the 55 open ones are listed below
  with the decision taken where one was needed.

### Open verification (round 3)

- The example crews' role prompts in a live run against Claude Code and Codex:
  whether each `after` member's merge of its predecessor's branch works for
  each agent, and whether the todo app's tester finds core's branch when it
  waits for it. The git sequence was run by hand; no agent was.
- The completion scripts in a real zsh and bash session. They were driven
  through a pseudo-terminal with `zsh -f -i` and `bash --norc -i` (plain
  `compinit`), not `zsh -ic` or `bash -ic` with a user's own rc files or a
  framework such as oh-my-zsh, and `completion install` was run against scratch
  files, never a real `~/.zshrc`. bash 3.2 (the one macOS ships) is untested
  and may not support `source <(...)`.
- The built-in agents' `site` URLs (twelve, in `internal/catalog/defaults.go`),
  each opened in a browser by hand: they ship as best known and a test checks
  their shape only. On 2026-10-02 three were found moved and fixed (codex,
  goose, dsh, now pinned by the test); the other nine are still to open.
- Real fullscreen entry with the `F` key and the header button is unverified:
  headless Chromium does not grant it, so the headless checks emulated the
  Fullscreen API and counted the requests.
- The picker, the rail and the Launch dialog's server tab on a real phone (the
  slideover) and at 1024 px; they were only looked at in headless Chromium.
- The sidebar's width across a Nuxt UI upgrade. Nuxt UI 4.11.2 has no size
  model or event for `UDashboardSidebar`, so the width kept in localStorage
  (`conductor.sidebar.size`) is read from the `--width` style on the sidebar's
  root after a drag or a double-click, and the saved width starts it through
  `defaultSize`; the headless check of this passed at 4.11.2 only.
- Wording: the crew members table says "After X idle" where the engine and the
  example prompts say "reports done". It is the same condition; pick one.

### Deferred items to close (plan 1 of round 3)

Closed by `docs/superpowers/plans/2026-10-01-round3-deferred.md`.

Strict JSON and the store:
- `config.Load` and `catalog.ReadFile` reject trailing data after the first JSON value, like the store and webhooks do; the store comment then tells the truth.
- `store.Save` writes with `SetEscapeHTML(false)` so patterns and snippets stay hand-editable.
- The catalog lock is not held across the fsync or the response: an editor lock for writers, the catalog lock only for the snapshot swap.
- Store tests: a chmod 0500 write-probe case (skipped as root), a forced rename failure for the kept-previous-document test, a concurrent reader in the concurrent-save test; the end-to-end test stops assigning the server's catalog without the lock.

Catalog validation and API shape:
- `validate` bounds `cwd`, `icon` and `adapter` (shape `^[a-z0-9-]{1,32}$`), env key and value lengths and `envPassthrough` name length, and checks the adapter id against the registry for config files too.
- `Upsert` stores a clone; signal kinds become exported constants; `Clone` gets a value receiver.
- A saved override that omits `adapter` or `signal` inherits them from the built-in it replaces.
- An override stores only the env keys whose value the editor changed; unchanged keys follow the base agent, so a secret rotated in the config reaches the agent (closes the "pinned env" item).
- Catalog entries carry `source` (`built-in`, `config`, `saved`) so the Agents page says Hidden or Removed truthfully.
- Tests: launching an overridden built-in, hiding down to an empty catalog, deleting an overlay-only agent.

The add-agent form:
- The form logic (masked-env round trip, `signalOut`, errors, the id pattern from the server's rule) moves to `utils/agentForm.ts` with vitest; a typed `***` on a new row is caught in the form; the command check posts `command[0]` only; an unclosed quote is refused instead of chipped; `slugId` never ends in a dash; the signal cards use the Nuxt UI radio group with arrow keys; `ArgvInput` reuses the input ring classes.

File reads:
- The file-read deny list also covers, in the config file's directory (and beside its symlink-resolved target), every name that contains the config's base name, ignoring case (`.bak`, `~`, `.swp` and `#…#` editor copies); the catalog file gets the same rule.

Sessions and events:
- The viewer reader stops closing the sink under `Pump` on a host disconnect, and a departing host's viewer gets one error frame.
- The attention state travels with the entry from `Record` to `OnActivity` (closes the read-after-the-fact window), and the SSE `activity` event carries `state` for attention entries so the Events page stops inferring hosted state.
- `conductor notify` retries a 429 for attention words (bounded backoff within its 5 s budget).
- A hosted session with no host connected meters route reports like a connected one.
- The host's "no launch route" line is logged at warn so it shows with the local terminal attached.
- `oneLine`/`dropControl` reuse `CleanName`'s stripping; the Record and hook tests move to their own file; the bell test waits on a frame instead of polling.

Adapters and the CLI:
- `WriteAssets` warns instead of refusing to start on a chmod failure of an existing asset; `staleCommand` rewrites only commands whose program is named `conductor`; `withYAMLListItem` handles a BOM and refuses a flow-style root; the hooks dir records the version with the binary and the CLI warns on skew; the by-hand note prints only the step that needs the hand; the ownership check accepts the process's own home when it is writable; `payloadFlags` derives from the hooks table; the aider card says "nothing to install" under an unknown home.
- `merge.go` splits out `home.go`, the mappers move to `notify/mappers.go`, `injectHooks` to `hostagent/hooks.go`, the claude/cursor merge closures share a helper; the remaining clipboard calls and dark code blocks use the shared helper and component.
- A catalog icon missing from the client bundle renders a generic agent icon, not nothing.

Docs:
- README: the Events page shows the webhook host, not the path; protocol.md: a missing hosted attention state has two causes; README and protocol.md: what an override inherits.

Webhook address rule:
- 6to4, Teredo and SIIT forms are judged by their embedded IPv4 address; a `64:ff9b:1::/48` prefix of another length is refused with a clear message.

Crews, runs and links:
- `Launch` returns `ErrRunStopped` when a stop lands before the first reserve; the run being launched stays exempt from eviction until its view link is minted; the link creation and the run lookup happen under one check; the forget-time disconnect goroutine is tracked by `Shutdown`; revoking an already-revoked link records nothing.
- `inRepo` reports `rev-parse` failures other than "not a repository" (dubious ownership, permissions) with git's message.
- The crews page keeps a launch's shown-once link until it is shown, refreshes crews on every visit, retries a failed run-name fetch, and checks the holder's expiry when storing; the join page gets a `JoinCrewGrid` component; the run view's grid has no empty cell beside the feed; the long prompt placeholder is checked in a browser at narrow widths.
- Tests: a sync point in the handoff "nothing typed" check; `api_test.go` split by route family (crews and runs into `crews_test.go`), the duplicate 503 and crew-cap checks deduplicated, the duplicate-names test asserting "twice".

Data directory default (decided 2026-10-01):
- When neither `dataDir` nor `CONDUCTOR_DATA_DIR` is set, the data directory is `~/.conductor` (the home of the user running `conductor serve`), never next to the config file or in the working directory. For backward compatibility, when `~/.conductor` holds no server data (`catalog.json`, `crews/` or `crews.json`; a `hooks/` directory alone does not count) and the old default (`<config dir>/conductor.d` or `./conductor.d`) does, the server keeps using the old directory and logs a notice naming both paths and how to move. The old directory must be a real directory (not a symbolic link) owned by the server's user, since its `catalog.json` chooses the commands agents run; anything else under that name, when it would be kept, stops the server with an error naming it, its owner and `dataDir`/`CONDUCTOR_DATA_DIR`. The host's hooks directory moves to `~/.conductor/hooks` the same way (old `~/.local/state/conductor/hooks` kept when present). The Docker image keeps its explicit `CONDUCTOR_DATA_DIR`. README, the upgrade note and `conductor.example.json` follow.

Crew storage (replaces the single `crews.json` and the 50-crew cap):
- One file per crew at `<dataDir>/crews/<id>.json`, written atomically by the store, which gains `List` and `Delete`; a corrupt file names itself in the startup error and the other crews still load.
- `GET /api/crews?offset=0&limit=100` reads the directory and answers `{crews: [summaries], total}` (limit at most 500); `GET /api/crews/{id}` returns one crew in full for the editor; the Crews page pages through the list.
- No cap on the number of crews; the per-crew size cap stays as a sanity bound on one file and its request body, at 1 MiB.
- An existing `crews.json` is split into per-crew files on the first start and renamed `crews.json.migrated`.

Accepted as documented limitations (not changed): hosted crews stay future work.

## Round 4: crews in view, prompts that run, interactive tiles, yolo, e2e (planned 2026-10-01)

Asked by the user after running round 3 on 2026-10-01; decided the same day.
One plan, `docs/round4-plan.md`, built on the research in `docs/round4/`
(the prompt investigation, the terminal fill, the yolo flags, session
resume, and Codex's review of this section).

### Decisions

- **Runs on the Crews page.** Each crew in the list shows its runs (newest
  first, from `GET /api/runs`): the run's name and age, a status (running,
  needs input with a count, stopped, finished), the members as avatars with
  their status (pending, starting, running, needs input, ended), and a link
  to the crew view; the crew's badge keeps "Running" when any run is active.
  Runs stay in memory on the server as today (no persistence; the engine
  keeps at least the active runs and evicts idle ones past 100). A run's
  state is: running while a member is starting or running; needs input when
  a running member's session needs it; stopped after a stop; finished when
  every member ended and none is pending (a member's `done` report keeps it
  running — a done agent is idle, not gone); a member's error shows beside
  the state. Run changes that no session change carries (a member going
  from starting to running or failing to start, an all-manual launch, a stop
  of a sessionless run, eviction, a log entry) reach the browser through the
  existing `/api/events` stream as a bounded `run` event (`{id}` to re-read,
  or `{id, removed: true}`), coalesced on the client; the one live store
  keeps the runs, every page reads them, and an older reply never replaces a
  newer one. No polling anywhere, including the crew view's 10 s re-read,
  which goes.
- **The sidebar groups sessions by crew run everywhere.** The full sidebar
  does what the rail already does: inside each section (needs you, running,
  exited) the sessions with no run come first, then one group per run with a
  header naming the run (linking to the crew view) and its member sessions.
  The run-only variant on a member's session page and on the crew view stays.
  One grouping helper in `web/app/utils/sidebar.ts` serves both.
- **A typed prompt runs.** When the engine types a member's role prompt, a
  handoff or a broadcast into Claude Code or Codex, the agent runs it without
  a person pressing Enter. The investigation against the real CLIs (2026-10-01)
  found the cause: the text and its carriage return went out in one write, so
  each TUI read the return as part of a paste — Codex on every prompt (its
  paste-burst detector), Claude Code for text over about 800 characters or
  typed before its input is up (startup pauses of up to 0.95 s beat the
  one-second-quiet check). The fix verified end to end on both: the text as a
  bracketed paste, then the carriage return as a separate write 250 ms later;
  for Claude Code, no typing between its screens and one more Enter if the
  member has not reported working within 3 s. The mechanism is one serialised
  submission routine in
  `internal/session` used by every automatic typing path (the prompt,
  handoffs, broadcasts; a person's own keystrokes stay raw): it writes the
  text and the Enter as the agent needs them (bracketed paste and a separate
  carriage return after a short pause are the leading candidates — Codex's
  TUI turns an Enter inside a fast burst into a newline), holds no session
  or engine lock across the pause, rechecks that no prompt appeared in
  between, is cancellable, never re-sends a prompt after an uncertain
  result, and marks the member prompted when the Enter is written so a
  `done` from before it never starts a dependant. It is verified live
  against both agents on this machine before the plan closes, with an
  agent's response as the proof (not the echo): the initial prompt, a
  handoff and a broadcast, short and long, in a fresh worktree. A trust
  dialog is never typed into: in a never-trusted repository the typed Enter
  answered it (Claude Code chose "No, exit"; Codex chose "Trust and continue"
  and saved the trust), so the member's output is watched for each agent's
  dialog text, the prompt is held, the member shows "needs you" with the
  dialog's words, and typing resumes after a person answers; a worktree of a
  trusted repository shows no dialog, so the README says to trust the
  repository once (Codex can be trusted per launch through its config
  override when yolo is on; Claude Code has no safe equivalent). The web's
  reply boxes (the quick reply, the broadcast bar) go through the same
  routine so a person's reply runs too. A terminal's automatic report
  (xterm answering Codex's cursor-position query) no longer counts as input
  that clears a member's needs-input state. Whatever first-run or workspace-trust dialog
  an agent shows in a fresh worktree is handled so that the prompt is not lost;
  if that needs a per-agent setting (a pre-trusted directory, a flag), the
  adapter carries it and the README says so.
- **Tiles are interactive, and they fill their width.** On the wall and on
  the crew view, a member's tile is a live terminal a person can type into in
  place: clicking the tile focuses its terminal, keys go to the session, and
  the full view is still one action away (a button on the tile or a
  double-click). The investigation found why tiles leave 16–37 % of their
  width blank: a tile renders the PTY's grid scaled to fit (the PTY's aspect
  ratio, not the tile's), and its hello carries xterm's default 80×24, which
  the server applies for an admin viewer, so opening the wall resets every
  session to 80×24. Only more columns can fill a tile, so a tile fits its own
  pane and sizes the PTY the way the full view does: the PTY follows the
  latest viewer that attached or resized, tile or full view (latest controller
  wins, unchanged), a tile re-asserts its size when it becomes visible again
  (after the full view closes), and two viewers open at once alternate only
  when one attaches or resizes, never continuously. The scaled rendering
  stays for view-only links (the join page's run tiles when the link grants
  view), and a scaled viewer's hello carries no size — `cols: 0, rows: 0`
  means "take the PTY's size" (a protocol change in `internal/proto`,
  `web/app/utils/protocol.ts` and `docs/protocol.md`, with its limit and a
  test), so no viewer resets a session by accident again. Plain-key
  shortcuts pause while a tile's terminal has focus, as in the full view; the
  Alt chords still pass through. A headless check measures the unused width
  on the wall and the crew view at two viewport sizes (under one cell) and
  that a session seeded at a large size is sized by the tile that last
  fitted it, never reset to the 80×24 default; the hello rule is: `(0, 0)`
  means "follow the current size", anything else is two values in 1–500 as
  today, zero stays invalid in a resize message, and an older client's
  nonzero hello keeps its meaning. Hello sizing never grants control: the
  role still comes from the token on both transports.
- **A global yolo flag.** `yolo: true` in the config, `CONDUCTOR_YOLO=1` and
  `conductor serve --yolo` make every launched agent skip its permission
  prompts: each built-in carries its recipe (arguments appended to the
  command and environment set at launch, as researched per agent), a saved
  agent can edit it, and an agent with no recipe is launched as before with a
  notice. The server default can be overridden per launch (the Launch dialog
  and `POST /api/sessions` take `yolo`) and per crew (the crew editor; the
  members follow it). A session shows a "yolo" badge in its header, the wall
  tile and the sidebar when it runs that way. The recipe goes through the
  single launch path in `createLocalSession` as argv and env (argv arrays,
  never shell text; the recipe's env goes through the same allowlisted
  environment as the agent's own env, never the privileged inject map),
  bounded like the catalog's other fields, documented per agent in the
  adapter matrix with what each flag really disables (Codex's bypass also
  drops its sandbox). Inheritance: a launch or crew with no `yolo` follows
  the server default, `false` disables, `true` enables; a crew's effective
  choice is fixed when the run is created so members started later follow
  it; a saved agent that omits the recipe inherits its base's, an explicit
  empty recipe disables it, an explicit recipe replaces it whole. The badge
  means "Conductor applied this agent's recipe", not that no dialog can ever
  appear; an agent without a recipe gets a notice and no badge. Hosted
  sessions (`conductor host`) are out of this round's scope.
- **Playwright tests for the example crews.** An in-repo suite
  (`web/e2e/`, `@playwright/test` pinned to the version matching the cached
  Chromium, a `make test-e2e` target, a CI job that installs Chromium) starts
  a built server with its own data directory and a test config whose catalog
  overrides the `claude` and `codex` ids with a stub agent script: the stub
  prints a banner, reads the prompt line, echoes it, runs the merge the
  example prompts ask for (recognising only the expected `crew/<run>/<member>`
  targets built from validated ids, run as argv, never evaluating prompt
  text), commits a file on its branch, reports done
  through `"${CONDUCTOR_BIN:-conductor}" notify`, and answers handoffs the
  same way. The tests load the examples, launch the todo app in a scratch
  repository, and assert on the crew view, the Crews page (the run under its
  crew with member statuses), the sidebar grouping, the wall (typing into a
  tile reaches the session), and the feed (prompt typed and run, handoffs,
  starts in order, the merges succeeding), plus the other three examples
  launching. One live test, skipped unless `CONDUCTOR_E2E_LIVE=1` and the real
  `claude`/`codex` are on the PATH, launches a one-member crew per agent with
  a tiny prompt and asserts the agent answered without a person pressing
  Enter.
- **An ended session needs nothing.** When a session's process exits or is
  killed, its attention state is cleared at the same moment its status
  becomes exited or stopped (the needs-you count, the badges, the dots and
  the sidebar section follow; the activity log keeps the prompt's history).
  Today the state lingers, so a killed agent keeps saying "needs input".
- **Agent sessions are named and resumable.** Each built-in agent carries a
  session recipe alongside its yolo recipe: how Conductor sets the agent's
  own session identity at launch where the CLI allows it (Claude Code
  `--session-id`, Goose's named sessions, …), where to capture it when it
  cannot be set (the hook payloads Conductor already receives, or the
  agent's own session store), and the arguments that resume that identity.
  The identity is stored on the Conductor session (`agentSession`, in the
  session info and on the stream) and shown in the header. An exited or
  stopped session offers **Resume** (its header, the wall tile, the sidebar's
  exited section, and a crew member on the crew view): a new Conductor
  session with the same agent, name, working directory, arguments, yolo
  choice and crew membership (a member keeps its branch and worktree),
  launched with the agent's resume arguments for the stored identity; an
  agent without a recipe is relaunched plainly and the UI says so. Ids are
  validated per agent and passed as argv, never shell text; the per-agent
  facts come from the research report and are marked verified or from docs
  in the adapter matrix.
- **Broadcast goes to everyone by default.** On the crew view the broadcast
  bar sends to the members ticked beside their tiles; the selection started
  empty and was reset to empty on every re-read of the run, so the button
  always read "Send to 0". The selection now defaults to every member with a
  live session, a member that starts later is selected when it appears, an
  untick survives re-reads and run events, and the button's count is the
  number of selected members the server will type into (those waiting on a
  prompt are counted separately as "N waiting skipped", since the server
  skips them). The run view's reload never clears the selection.

### Open verification (round 4)

Round 5 gave each of these a test tier (`docs/round5-plan.md`, Phase B).
What runs where:

**In CI, against the stub agent (`make test-e2e`, web/e2e):**

- The interactive tiles (item 1): `tiles.spec.ts` measures every tile on the
  crew view and the wall (less than a cell beside its grid), types into a
  focused wall tile without leaving the wall, and sends the Alt chords to
  the page. `webgl-probe.spec.ts` records that Chromium 1117 headless has no
  WebGL2 whatever flags it is given (`--use-gl=angle` with
  `--use-angle=swiftshader`, `--enable-unsafe-swiftshader`, `--use-gl=egl`
  were tried), so the tiles are measured with the DOM renderer and
  `data-renderer` says which; the WebGL look in a real browser stays by
  hand.
- The trust hold (item 2): `trust.spec.ts`, with the `codex-untrusted` stub
  drawing "Trust this folder?": held with the question on the API and the
  Crews page, arrows keep it, Enter in the terminal answers it and the stub
  saves the trust as Codex does; with yolo the override makes the question
  not appear and nothing is saved; a broadcast into a held member waits.
- Resume (item 3): `resume.spec.ts`: a Claude-shaped session resumed with
  `--resume <id>` and its transcript, one without a turn relaunched afresh,
  a crew member resumed in its kept worktree and branch without a second
  prompt, a Codex-shaped session resumed by its captured thread, a gone
  working directory refused, and (item 8) an ended member of a stopped run
  resumed in place, and Resume run as a new run.
- Codex's hidden title thread (item 6): `title-thread.spec.ts`, with the
  stub reporting the title turn first: no false needs input, the user's
  thread captured, one done.
- The identity probe: `identity.spec.ts` (the stubs named by their version
  output, the goose impostor named and refused).

**Nightly, on the network and against freshly installed CLIs
(`.github/workflows/nightly.yml`):**

- The built-in sites (item 7): `make test-network`, each site answering
  within its own domain; passed on 2026-10-03 from this machine.
- The recipe flags (items 4 and 5, as far as a help text tells): `make
  test-recipes`, each CLI's `--version` matched by its adapter's probe and
  every flag of its yolo, session and injection recipes found in its help
  (or its subcommand's); the version and help outputs are uploaded so an
  unverified probe pattern can be closed with real text. On 2026-10-03 this
  machine identified Claude Code 2.1.288, Codex 0.159.0, Antigravity 1.2.14
  and Copilot 1.0.91 with every flag in their help, and named the goose on
  its PATH as the Go migrations tool.

**Live, with test accounts (`make test-live`, `.github/workflows/live.yml`,
the `live` environment holding `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`):**

- `live.spec.ts`, for Claude Code and Codex: a typed prompt answered
  without a person pressing Enter (Codex's title thread raising no false
  needs input); the trust question held in a repository the agent has not
  seen and answered by Enter in its terminal (the live check of the
  `trustPrompt` words and of what Enter selects); yolo (Codex asks nothing
  and saves nothing; Claude Code still asks); a conversation resumed after
  its session ended, which still knows the word it was told. Round 4's own
  live run (`PASS live4`, the spec 2 of 2) passed on 2026-10-02 against
  Claude Code 2.1.287 and Codex 0.159.0.

**Still by hand:**

- The WebGL renderer's look in a real browser (headless Chromium has none).
- Antigravity's and DeepSeek Harness's recipes and trust dialogs (no
  automatable sign-in), Copilot's hook `sessionId`, and the recipes of the
  CLIs the nightly job cannot install.
- Codex's "Hooks need review" dialog on a machine where Conductor's
  `~/.codex/hooks.json` is not yet trusted: trust the hooks once in Codex
  before a crew run.
- The live tier on 2026-10-03 from this machine: Claude Code 2.1.288 passed
  the typed prompt and the yolo check; its trust question highlights "No,
  exit" first, so the trusting answer is Down then Enter (the spec presses
  that; Codex's is Enter alone). A resumed Claude Code member answered the
  word it was told (PINEAPPLE) once the broadcast waited for it to be ready.
  On 2026-10-04, after `codex login`, all four Codex rows passed against
  Codex 0.159.0, and the four Claude Code rows against 2.1.289 once the
  trust row waited for the run log's note (its first run read the log a
  moment before the engine wrote "solo asks").

### Deferred (round 4)

- Capturing the agent's session id through the plugin agents (OpenCode,
  oh-my-pi, pi's mid-process changes, Amp, DeepSeek Harness): their plugins
  would pass it to `conductor notify`; until then they relaunch.
- A resume record that outlives `exitedRetention` (a bounded store in the data
  directory), so that a session can be resumed after it leaves the list.
- Yolo and Resume for hosted sessions (`conductor host`).
- `conductor up --resume <run>` (the API has `POST /api/runs/{run}/resume`
  since round 5).
- aider's chat-history file as its session handle.

## Round 13: what round 12 set aside, built (started 2026-10-09)

The owner asked why five Files items had been moved to the todo instead of
built, and decided each (`docs/round13-plan.md`): Touched one row per file
with its count; files seen by git for every agent, labelled `git`; live
payload captures for the agents whose hooks name no files; a swap file
opened read-only with a banner; local echo always on in insert mode. While
planning, a bug: the `goose` on the owner's machine is the Go migrations
tool, and Conductor offered it as Goose.

- **G0, a program that only shares an agent's name is not the agent.** The
  identity probe already named the migrations tool an impostor, but the
  catalog still listed Goose as available, so the Launch dialog offered it
  and the Agents page showed it installed with a red "Not Goose". Now a
  known impostor makes the agent `available: false`: it leaves the Launch
  dialog, the Agents and Settings pages show it greyed as "Not installed",
  the tooltip naming the program that is there and what it printed, and
  `POST /api/sessions` refuses it (`not_the_agent`, from the probe's cached
  answer, so a launch never waits on a probe). `probe: false` on the agent
  launches it anyway. A verified probe that matches nothing (Claude Code
  printing its version some new way) is not treated so: the agent stays
  offered with its warning, since hiding it would make a real agent vanish
  after an update. Tests: the catalog and the launch refusal against a
  scripted impostor, a Claude entry printing something new still offered
  and noted, vitest for the badge's words, Playwright `identity.spec.ts`
  (the Goose stub greyed with the tooltip, absent from the Launch dialog,
  its launch refused).

## Round 12: the Files tab as four sections, and a Monaco editor beside the terminal (started 2026-10-08)

From the design screens 4a–4g and 5a–5c in "Conductor UI mockups" (the
owner designed them on 2026-10-08 from a brief written here). The plan with
its decisions and layers is `docs/round12-plan.md`: Monaco as the editor,
editing for controllers under a `fileEdit` setting, go-git for the git
features with the CLI wrapper kept where go-git is slow, file events from
the hooks first, layers merged one at a time (never a stack), and Vim keys
waiting on monaco-neovim-wasm's missing license.

### Decisions

- **F1, the Explorer (4a).** The Files tab opens on the working directory
  as a tree (`utils/fileTree.ts`: the root, folders listed on their first
  expansion through the same `file` request a directory took before, files
  with their size, folders first and names by code point without regard to
  case so every browser shows one order); the breadcrumb runs from the top
  of the path; the box above the tree is a filter over what is loaded (a
  folder that holds a match opens so the match shows) or, for a typed path
  (a slash in it, a leading dot or tilde, or `:line` at its end) and Enter,
  opens that file at that line, made absolute against the working
  directory; the hint that paths in the terminal are clickable is one line
  at the foot. A file still opens in the pane (the editor area is F2); a
  crumb or Back returns to the tree, opened down to that folder, fetching
  what it has not seen. The box moved out of the inspector into the pane,
  so the Yard's slide-over has it too. A root listing refused because the
  connection is not open yet is tried again a few times. No server change.
- **F2a, the editor area on the session page (4b, 4c, 4f).** Monaco
  0.57.0 (`utils/monaco.ts`: the editor, its workers as Vite worker files,
  and the two brand themes, in a chunk loaded with the first file;
  `CodeEditor.vue` one file at a time, read-only until F6, the line asked
  for shown in the middle and tinted for a moment). `EditorArea.vue` holds
  the tabs (`utils/editorTabs.ts`: one per file or URL, the next or the
  previous taking a closed one's place, Ctrl+Tab round the end, the fold,
  the split kept in `conductor.editor.split`), the header (the breadcrumb
  from the working directory, Ln/Col, copy path, open raw) and the states
  4f draws: an image on a checker with its size, a binary file with Open
  raw, a URL in the sandboxed frame with New tab, Refresh and Copy, a read
  refused with the words for view-only and for viewing off, a hosted
  session's machine away. The session page puts the area above the
  terminal, split by a bar dragged with pointer capture, the terminal
  keeping its minimum height; T and Alt+T fold it (`EDITOR_SHORTCUTS` in
  the modal, Alt+T handed back by a terminal); the Files pane opens files
  into it (`external`, the `open` emit) and stays the tree; "Preview in
  pane" for a URL the agent printed opens a URL tab. The symbol crumb the
  design shows ("ListUsers") waits for a language server Monaco does not
  have for Go; the Yard, the guest page and the phone are F2b.
- **F2b, the Yard, the guest page and the phone (4g).** The column a
  terminal shares with the editor is one component (`EditorColumn.vue`:
  the area, the split, the terminal in its slot, the reply bar in `bar`),
  and the files a page has open one composable (`useEditorTabs`: the tabs,
  how a path or a URL opens, the fold with T and Alt+T). The Yard's focused
  tile and a guest's join page use both, with the Files pane beside the
  editor in place of the slide-over (`FileViewer.vue` is gone); a
  view-only guest gets the pane marked read only and the editor read-only;
  the join reply carries the session's working directory so the guest's
  tree can root there (reads stay the session's to allow). On a phone the
  editor takes the column and the terminal folds to a bar at the foot that
  says what the agent is doing (the attention's words, else the agent) and
  brings the terminal back on a tap, the editor folding to its strip; the
  terminal stays mounted, so nothing reconnects. In the Yard, Esc folds the
  editor first and leaves the focus second. Found on the way: below lg the
  dashboard panels were at least a screen tall (the theme's `min-h-svh`)
  and ran under the bottom bar, clipping whatever sat at a panel's foot;
  the app config caps them there (`dashboardPanel.slots.root:
  max-lg:min-h-0`), so a phone's terminal bar and reply bar end above the
  bottom bar. And the Files pane's retry for a connection not open yet
  compared a raw root with its reactive proxy and never fired; the root is
  reactive from the start now, and a page whose terminal connects after
  the pane is retried too.
- **F3, Changes (4d).** The git reads ride the file request: `file_get`
  with `op: status` (against `base`, HEAD when empty) answers a FILE frame
  of kind `status` (the tree's top as its path, the branch, the base's id,
  the changes with their status letter and lines, the totals, truncated at
  500 or at the frame's header bound), `op: show` a FILE frame of kind
  `show` with the path's content at a revision (1 MiB at most); both under
  the session's file policy and deny list, so a hosted session's status
  comes from the developer's machine through the same channel.
  `internal/gitcli` runs the git binary (argv, the C locale; the crews'
  helper moved there): status by `git status --porcelain=v1 -z`, the lines
  by `git diff --numstat --no-renames`, an untracked file's lines counted
  from the file (a binary one as none), a revision's file by `git show`.
  go-git is kept for the commits (F5): its status hashes the working tree,
  which the owner ruled out. The Files pane gains its section switch
  (Explorer, Changes with its count); the Changes list with M, A, D and
  the lines; the footer "Refreshed as the agent works · 8s ago" with the
  totals; a refresh every five seconds while the section shows and once
  when the pane opens, for the Explorer's marks; "Not a git repository"
  with the words. A change opens as a `diff` tab (`DiffEditor.vue`,
  Monaco's diff editor, side by side or inline, read-only; the base's
  version through `show`, none for an added file, the working directory's
  through a read, none for a deleted one), the header saying what it is
  against, the status and the lines, and Open file. A crew member's
  changes against the run's base wait for the base to reach the page.
- **F4, Touched (4e).** A seventh event type, `file`: `op` (read, edit,
  write, delete) and `path` (≤ 1024 bytes) with `tool`, through the same
  activity ring, replay and frames as the rest (`proto.Activity` carries
  both), posted like any event (the API refuses one without its op and
  path). The hook mappers yield the files a tool call touched beside the
  call's own event (`notify.Request.Files`; Claude Code's input
  `file_path` or `notebook_path` by tool, Codex's `apply_patch` by the
  files its patch names, Cursor's `afterFileEdit`), and `Send` posts each
  as its own event; `conductor notify --event file --op … --path …` and
  the MCP `report` tool take it for agents without hooks, which the skill
  says. A repeat of the newest file entry (the same path and op within
  3 s) moves its time instead of adding a line (`CoalesceFile`), so a file
  saved forty times is one. The Files pane gains Touched (newest first,
  the op's icon, the folder and name, "Edit · codex · 08:33:12", a row
  opening the file) and dots on touched files in the Explorer, fed by the
  inspector's activity (the Yard's and the guest's panes have no activity
  yet, so no Touched there); the Activity tab and the Events page word it
  ("edited internal/api/users.go · Edit"), routed to the feed alone by
  default. The activity replay stays at 50 entries, so a long session's
  Touched starts from what the page saw; the hook bucket (20 a second)
  still bounds a burst. Verified against the real Claude Code (2.1.294)
  on 2026-10-08: its base settings had no PostToolUse hook at all (tool
  events are the catalog's opt-in `toolEvents`), so nothing reported a
  file; the base settings (`claude.json`, `claude-yolo.json`, the
  installed `~/.claude/settings.json`) now hook PostToolUse for the file
  tools alone (`matcher` Read|Edit|MultiEdit|NotebookEdit|Write) with
  `notify --claude-hook --files`, which posts the files and no tool call,
  and a tools launch keeps its every-call hook (the same Edit reported
  twice coalesces, the call's own event between them stepped over). The
  live tier (`live.spec.ts`, "reports the files it reads and writes
  through its hooks") checks the real agent's read and write land as
  file events; Codex's passes only where its `features.hooks` is on with
  Conductor's hooks.json, which this machine does not have.
- **By hand, F1–F4 on Windows (2026-10-08).** A dev server from main on
  the WSL box, opened in Chrome on the Windows host and driven through a
  desktop-control MCP (UI Automation, real mouse and keyboard, window
  screenshots), with the real Claude Code 2.1.295 in the trusted scratch
  repository, bypass permissions on. Seen and screenshotted: the Explorer
  tree with sizes and the breadcrumb; "internal/api/users.go:12" and
  Enter opening the file in Monaco at Ln 12 with Go highlighting, the
  breadcrumbs and the minimap; a second file as a second tab; the fold
  strip "2 files open · users.go README.md" and unfold; the split bar
  dragged 150 px (the editor from 495 to 644 px, the split kept); the
  tab's × closing it and the editor folding away. A typed prompt had the
  agent read and edit README.md: Changes listed it as M +1 −0 within
  seconds ("Refreshed as the agent works · 4s ago"), the Explorer marked
  it M with a touched dot, the diff opened side by side against HEAD and
  inline, Open file raised the file's tab; Touched listed "README.md
  Edit · agent · 16:29:33" above its Read, newest first; the Activity tab
  and the Events feed worded both, and the session's sidebar row carried
  no badge. Two notes: the filter box matches only what the tree has
  loaded (a "users" typed before `internal` was expanded found nothing;
  in the todo); Ctrl+Tab is a reserved shortcut in Chrome, so the tab
  cycle cannot be driven from a browser tab (it works in the desktop app
  and in the Playwright spec). The phone layout and the light theme were
  not part of this pass.
- **F5, Commits (4e).** Two more git reads ride the file request:
  `file_get` with `op: log` answers kind `log` (the branch, the commits on
  HEAD newest first with their subject, short id, author, committer time
  and first parent, at most 200), from the session's start (`since`, the
  session's `createdAt`, compared at whole seconds since git keeps no
  more) or, with `base`, after the base's merge base with HEAD (a crew
  member's run base, whatever the commit times); `op: commit` answers
  kind `commit` (the commit with its body, at most 4 KiB, and its files
  against the first parent with their lines, renames found with their old
  path in `from`, a file over 1 MiB listed without lines, at most 500).
  Both read with go-git (`internal/gitrepo`, v5.19.3, Apache 2.0; chosen
  by the owner for the Go git features): 200 commits logged in 37 ms and
  a 45-file merge's stats in 79 ms on this repository, a crew's linked
  worktree opened through its common directory; status and show stay with
  the git binary, which is faster for them. The Files pane gains Commits
  beside Changes with its count, "2 commits on main since 08:31", a
  commit opening in place to its body and files, a file opening as a
  diff tab "a1b2c3d users.go", "a1b2c3d vs its parent 9e8d7c6" (the
  parent's side read at the parent, a rename's at its old path, nothing
  for an added file or a root commit), refreshed every 10 s while it
  shows, and "Commits made here. Nothing is pushed from Conductor." The
  Yard's focused tile and the guest page open commits the same way. Tests:
  go-git against scratch repositories (since a time, after a base in a
  linked worktree with an old-dated commit, a root commit, modified,
  added, deleted, renamed and binary files, the totals, an unknown
  revision), the session's reads through the file policy (the deny list,
  not a repository, a flag as a revision), vitest for the rows, the words
  and a tab per commit, Playwright `commits.spec.ts`.
- **F6, Editing (4b, 4f).** A controller edits a file in Monaco and saves
  it with Save or Ctrl+S, on the session page, the Yard's focused tile and
  the guest page alike (one composable, `useEditorBridge`, now gives all
  three the save and the Neovim keymap). A save travels as FILE_WRITE
  frames (`0x08`, new): parts of at most 32 KiB in order under one request,
  the FILE frame's layout the other way, bounded at 1 MiB, one save at a
  time per connection, answered with a FILE frame of kind `written` or
  `error`. The owner checks the file against the read's sha256 (a read
  whole now carries `sha256` and `mtime`) and refuses with
  `changed_on_disk`, naming the newest file event that changed it ("Changed
  on disk since you opened it · 08:34:10 · codex (Edit). Saving would
  overwrite that."), unless forced; it writes atomically with the file's
  mode kept and records a `file` event `write` with tool `editor`, so
  Touched and Changes follow. The client: an unsaved tab's dot, Save
  (disabled until something changes) and "Saved", Compare (the disk and
  yours side by side), Reload and Save anyway, a prompt before an unsaved
  tab closes ("Save notes.txt?", Save, Don't save, Cancel), unsaved edits
  kept across switching tabs, the keymap switch held while a tab is
  unsaved. Policy: the control role on a session whose `fileEdit` is
  `control` (the setting F8 added); a truncated or binary file and a view
  guest stay read-only; the switchyard drops FILE_WRITE from view-role
  connections. Found on the way and fixed: Monaco 0.57 types through
  Chromium's EditContext element, a plain div the page's shortcut guard
  does not know, so single-key shortcuts fired while the cursor was in the
  editor (`g` then `a` opened Agents, `n` the Launch dialog), read-only
  included; both editors use Monaco's textarea now (`editContext: false`).
  Tests: the frame's round trip and bounds, the session's save (parts,
  mode, Activity, the conflict with who changed it, forced, deleted since,
  every refusal, editing off), a save over the viewer WebSocket and a view
  link refused, the relay dropping saves and Neovim for view viewers,
  vitest for the parts, the frame, the saved versions and the words,
  Playwright `saving.spec.ts` (type, dot, Ctrl+S, Touched; the agent's
  edit caught, Compare, Reload, Save anyway; the close prompt and Don't
  save; a view guest's Read only).
- **F7, Comment on a line (5a, 5b, 5c).** A selection in the editor shows
  a bar under its last line: Comment, Ask the agent (a controller's) and
  Copy (`path:from-to` and the lines); Ctrl+Shift+M and Ctrl+Shift+A open
  the composer from the keyboard, on the selection or the cursor's line.
  The composer, anchored there, shows the quote and takes the words (Enter
  sends, Esc closes); the post rides the chat with a new optional `quote`
  `{path, from, to, lines, cut}` (the path from the working directory, at
  most 12 lines of 200 bytes, control characters out; its last lines left
  out when a full text of quotes beside it would pass the 8 KiB frame, a
  worst case tested), kept with the message and in a run's record. Asked
  of the agent, the message is typed as the location, the lines as `> `
  lines and the words, at most 4096 bytes (`AgentText`), on a session and
  into a run's member alike. In the thread a quote card shows the location
  and the numbered lines; on a page with an editor (provided by the page,
  so every chat container shows it) Open goes to the range with it tinted
  for a moment, and the card reads the file (once per 15 s per path) to say
  "Lines moved since · now 5–6" (the nearest place the lines are now) or
  "Lines changed since". The session page brings the Chat tab forward on a
  comment (the sheet below xl); the guest page posts to its chat too (a
  view guest comments, a controller asks). Tests: the quote's bounds, the
  frame's worst case, the agent's text and its typing (Go), vitest for the
  quote, the location words and the relocation, Playwright
  `comments.spec.ts` (select, Comment, the card; Ctrl+Shift+A, the marker,
  the location typed into the agent; the lines moved on disk, the note,
  Open at the new range).
- **Round 12's remainder (2026-10-09).** The Explorer's filter reaches
  folders not opened yet: a fifth file operation, `op: find`, walks the
  working directory on the session's machine (at most 20,000 entries,
  3 s, 200 matches, `.git` and `node_modules` left out, the deny list
  checked on every folder and match) for files whose name holds the
  words, and the pane lists them under the tree, "In folders not opened
  yet", a row opening the file. Touched shows on the Yard's focused tile
  and the guest page too (their terminal's activity). Under the Neovim
  keymap the cursor sits right on lines with multibyte characters
  (Neovim's byte columns made UTF-16 ones, `byteColToUtf16`), and a
  bug found on the way: after each key the bridge asked for the cursor
  with an eval, which cancelled a command waiting for its next key, so
  `gg`, `f<char>` and the like sent a key at a time (as a browser sends
  them) did nothing and could hang the bridge; it now asks
  `nvim_get_mode` first and skips the eval while Neovim waits. Set aside,
  with their reasons, in the todo: a local echo for insert mode, input
  methods and dead keys under the Neovim keymap, the swap-file prompt,
  Touched before the replay's 50 entries, files from more agents. Tests:
  the find (names below, any case, the skips, the deny list, the bound),
  `gg` then `ft` a key at a time against the real Neovim, vitest for the
  found rows and the byte columns, Playwright `files-more.spec.ts` (the
  find past an unopened folder, node_modules left out, the file opened;
  Touched on the Yard's tile) and `vim.spec.ts` (the cursor past an é).
- **F8, Neovim in the editor (the real one, on the session's machine).**
  Chosen on 2026-10-08 over monaco-neovim-wasm (no license: public code
  without one grants viewing and forking on GitHub, not redistribution)
  and over monaco-vim: the editor's keys stay Monaco's until the **Keys**
  button in the tab strip is pressed, which is kept per browser
  (`conductor.editor.keymap`) and is off by default, so nobody who never
  asked for Vim keys meets one. With Neovim chosen, a file opened on the
  session page is held by `nvim --embed` on the machine that runs the
  session (the person's own config and plugins), started by the official
  Go client (`github.com/neovim/go-client`, Apache 2.0) with a UI attached
  for the mode, the command line and the messages, the buffer followed
  through `nvim_buf_attach`, the cursor through an autocmd. The wire is
  `nvim_open`, `nvim_input` (≤ 256 bytes, a per-connection bucket),
  `nvim_close` and `nvim_event` (the buffer's `lines` cut to the control
  bound, a line past 4 KiB cut and marked; `cursor`, `mode`, `cmdline`,
  `message`, `written`, `closed`, `error`), on the terminal's connection
  as file reads are, on the server and in `conductor host`; the switchyard
  drops the three from view-role connections. The policy is the new
  `fileEdit` setting (`control`, the default; `off`; `CONDUCTOR_FILE_EDIT`,
  `conductor host --file-edit`) on top of the control role and `fileView`,
  reported in the welcome as `fileEdit` and `nvim` (installed there), at
  most 2 editors per connection and 8 per session, closed with the
  connection. In Monaco the keys are intercepted and sent in Neovim
  notation (`utils/nvimKeys.ts`), the model follows the `lines` events
  (`utils/nvimLines.ts`), the cursor style follows the mode, a visual
  selection shows as Monaco's, a status line under the tabs shows
  `-- INSERT --`, the command line as typed and the messages; `:w` writes
  on that machine and lands in Activity as the person's `file` event
  `write` with tool `nvim`, so Changes and Touched follow; `:q` closes the
  tab; Ctrl+W goes to Neovim in that keymap. Where `nvim` is missing or
  the connection may not edit, the button's note says so and the keys
  stay Monaco's. Verified against Neovim 0.10.4 on this machine: the Go
  bridge (`internal/nvim`), the policy and bounds through the session
  (`internal/session/nvim_test.go`), the viewer WebSocket end to end
  (`TestNvimOverTheViewerWebSocket`: opened, the buffer, `dd` as a
  deletion, `:w` on disk, a view link refused with `nvim_unavailable`),
  22 vitest cases for the keys and the edits, and Playwright
  `vim.spec.ts` (off by default; the button; `dd`; insert mode with its
  words; `:w` on the command line, written on disk, the message, the
  Touched row with tool `nvim`; `:q` closing the tab; the choice kept
  across a reload and turned off again). CI installs Neovim 0.10.4 from
  the pinned release for the go and e2e jobs; without it the Go tests
  skip with a message and the spec is skipped. Left for later (the todo):
  the Yard's focused tile and the guest page, a local echo in insert mode,
  byte columns with multibyte text, a Settings row.
- **By hand, v0.7.0-rc.4 in the installed desktop app on Windows
  (2026-10-08).** rc.4 was cut from main (F1–F4), its installer put on the
  Windows host and run through the same desktop-control MCP: the welcome
  page with the lockup in the header and "Conductor 0.7.0-rc.4", the
  "Choose Installation Options" page naming the per-machine install it
  would upgrade, the elevation prompt (the person's click), the finish
  page with the lockup on forest and the sponsor badge in the sidebar.
  The app launched, put the rc.4 server in the WSL distribution and ran
  it; its page was driven through Electron's own debugging port
  (`--remote-debugging-port`, for one launch) with real keys and mouse
  for what a person types and hovers, screenshots of the window for what
  shows. Passed, each with a screenshot: **3b** two loose sessions (one
  asking), a run of three (one asking, one exited, folded "Exited · 1"
  with the count), the hosted session from this box with the laptop tag
  "lan-box", the Yard's count; **3c** hover (Share, Stop, More), the
  in-row stop confirm ("Stop migrate-db? … Cancel / Stop") and its
  Cancel, the run header's Share run, Stop run, Open run; **3c keys**
  `/` to the filter, ↓ to the first row, J and K over rows and the run
  block, Enter opening the row, X asking and Esc cancelling, S opening
  "Share lan-shell", R from a member opening its run; **3d** Ctrl+B to
  the rail with the counts, the run capsule holding its members, the
  laptop tile, "+2" exited, the tooltip "users api · run started 17:46 ·
  lead running · core running"; **3f** nothing dropped remains. **The
  trust question (rc.3)**: Claude Code 2.1.295 in an unseen folder shows
  the two answers on the reply bar, the row, the Yard count and the
  chat; "Yes" from the row and "Yes" from the chat each let it continue
  (no exit); "To agent" while it asks is refused with "the agent asks
  whether to trust the folder: answer with the question's choices, or in
  its terminal". **Chat 2a, 2b, 2g, 2h** with a second browser as Jane:
  names, roles and times; "To agent" typing `echo chat-to-agent-ok` into
  Claude Code with the marker "Sent to agent by nater"; the closed tab's
  count rising on her message and clearing on open; the pills on the
  session row and the run header while on the home page; "This session
  ended. The chat is read-only…" after a stop. **2e** the run's Chat
  beside Share, the scope menu "Chat only · everyone here reads it",
  "Also send to lead · waiting on a prompt: skipped" (disabled), "Also
  send to core · typed into its terminal", "Also send to review · ended";
  `echo run-chat-ok` typed into core with "Sent to core by nater"; a
  member's question in the run chat on its name. **2d** through the real
  switchyard (switchyard.rslabs.net, still rc.2): the view link's join
  card ("You'll be view only: you can watch and open files", the sponsor
  badge), joined as Guest: the terminal over "WebRTC direct", "hosted on
  naterdev-win", the chat with "You are view only: what you write reaches
  the people here, not the agent." and no agent actions. **The newline
  chords** Shift+Enter and Ctrl+Enter each added a line to a Claude Code
  prompt and Enter submitted the three lines. **G then Y** opened the
  Yard with the focus outside the terminal. **The Files tab in the app**:
  an image on the checker with "64 × 48" and Open raw, a binary file
  ("Binary file; nothing to show · 2.0 KiB. Open raw downloads it."), a
  link the shell printed clicked in the terminal and previewed in the
  pane (example.com in the sandboxed frame with New tab, Refresh, Copy),
  the Yard's focused tile opening go.mod in Monaco from its Files pane.
  Not verifiable by this harness: the Alt chords (the control server's
  key events carry no scan code, so `e.code` is empty and both xterm's
  pass-through and the shortcut matcher, which key on `e.code` under Alt,
  ignore them; a synthetic Alt+Y navigates, and a real keyboard sets the
  code) and the uninstaller's sidebar. Seen and not explained: an
  "error" badge on a shell member (lead at launch, review after a stop
  with exit 0) with no error line in the Events feed; in the todo. Two
  harness lessons: Enter or Esc sent to a focused terminal while Claude
  Code asks the trust question answers it ("No, exit" / cancel) and the
  session ends, so a keyboard pass must leave such a page first; a
  session row below the sidebar's fold cannot be hovered until scrolled
  into view.

## The trust question's answers as choices (2026-10-08)

The owner, testing rc.2: "the trust question kills the session if I answer
in the chat". Claude Code's trust dialog highlights "No, exit" (the live
test knew: its trusting answer is Down then Enter), and every typed path in
Conductor ends with Enter (the chat's "To agent", the reply boxes), while a
trust question carried no choices, so typing anything there picked "No,
exit" and Claude Code left. Now:

- **The catalog's `trustAnswers`** (`catalog.Answer`: a label and the keys
  that pick it, the trusting answer first, at most 6, inherited by a saved
  override like `trustPrompt`): Claude Code "Yes, I trust this folder" =
  Down then Enter and "No, exit" = Enter; Codex "Yes, trust this folder" =
  Enter and "No, continue without trusting" = Down then Enter. The session
  takes them as `Options.TrustAnswers`, and `fireTrust` puts them on the
  attention as its `options` (cleaned and bounded as any prompt's), so the
  reply bar, the sidebar's row, the Yard's card and the chat's question row
  show the two buttons and type the right keys, raw.
- **Typed text is refused while the question shows**: `Submit` by a person
  returns `ErrTrustQuestion` (`TrustQuestionWords`); the viewer and the host
  answer `submit`, `chat` to the agent and `chat_send` with `not_sent` and
  those words, which the chat row and the reply box show. Conductor's own
  typing (a crew's prompt, a handoff, a broadcast) already waited.
- Found on the way: Codex 0.159's own "update available" dialog (Enter
  continues with the update) is not detected as needing input, so a session
  sits on it silently; in `docs/tasks-todo.md`. `conductor host` watches no
  trust question yet, so a hosted session's stays the terminal's to answer.

Tests: the catalog's defaults and validation; the session's question
carrying its answers, Enter-by-text refused and the choice's keys answering;
the Codex stub's trust dialog in `trust.spec.ts` (the buttons on the page and
in the chat, "To agent" refused with the words, the chat's "Yes" trusting).

## After round 11: a newline in the prompt, the page chords, the installer's art and the sponsor badge (2026-10-07)

Four small things the owner asked for after the stack merged, in one pull
request from main (not stacked, each its own commit):

- **Shift+Enter and Ctrl+Enter are a newline in the agent's prompt.** The
  browser terminal sent a plain carriage return for Enter whatever the
  modifier, so both submitted (the bug the owner found on 2026-10-06, now
  off the todo list). `TerminalView`'s key handler takes the chord before
  xterm does and sends ESC CR (`NEWLINE_IN_PROMPT`, `utils/terminalKeys.ts`),
  what Claude Code's own `/terminal-setup` teaches VS Code and iTerm2 to
  send for Shift+Enter and what a terminal sends for Option+Enter: Claude
  Code reads it as a newline, Codex as Alt+Enter, a newline too. Plain
  Enter, Alt+Enter and Meta+Enter are untouched. vitest for the chord;
  Playwright types two chords and an Enter into the stub and finds the ESC
  at the end of each line in its transcript. Codex's reading is the live
  tier's to confirm (`make test-live`); the by-hand list names it.
- **The page chords go by each page's initial**: G then Y the Yard (was W),
  G then R the Roundhouse (was C), G then C Crews (was R); A and E as
  before; the Alt twins in a terminal follow (Alt+Y, Alt+R, Alt+C), and
  Alt+W reaches the agent again. vitest on the rows and the passthrough
  set; Playwright presses the three chords.
- **The installer's own artwork.** electron-builder's stock bitmaps (a blue
  laptop and a box) gave way to ours: `scripts/installer-art.mjs` renders
  `scripts/installer-art.html` headless at 1:1 and writes the 24-bit BMPs
  NSIS reads into `desktop/build/`: the welcome and finish pages' sidebar
  (164 × 314, the lockup on forest with an orange rule, the sponsor kit's
  badge "Sponsored by" + the RockSolid Labs logo at its foot) and the other
  pages' header (150 × 57, the lockup on the dialog's white). The
  uninstaller takes the same sidebar. A desktop vitest reads the bitmaps'
  headers (size, 24-bit, uncompressed) and the config naming them. The
  installer itself is a Windows runner's build: checked by eye on the
  next release candidate.
- **The sponsor badge on the switchyard's pages.** The footer's text line
  gave way to the kit's badge (1b) at the lower right: "Sponsored by" and
  the logo, linking to rocksolidlabs.io, on the landing page and the 404
  page (`switchyard_page.go`; the Go test and the switchyard e2e check it
  and that the logo is served beside the app). A switchyard's own join
  page shows the same badge under the card (light and dark logos), never
  inside the workbench.

## Round 11: the sidebar made simpler, and chat beside the terminal (2026-10-06)

From the design hand-off "Conductor UI.dc.html" (the 2-series is chat, the
3-series the sidebar; the 1-series is the app as built and stays), approved
by the owner on 2026-10-06. The plan: two tracks of pull requests, the
sidebar S1–S7 and chat C1–C5, each matched to its screen ids.

### Decisions

- **The sidebar lists sessions and runs** (3a, S1). A run is one block,
  kept whole, placed where its most urgent member is, its members by urgency
  then the crew's order, an exited member inside with Resume; the crew is
  not listed; a machine is a tag on a hosted session's row, not a heading;
  this server's own loose sessions say `server`, members say nothing (they
  run here); the path leaves the row for its tooltip (3f). Shared with you
  sits below your own sessions. Exited folds to one line. The model is
  `sidebarModel` in `web/app/utils/sidebar.ts`; the rail draws from it too.
- **Sections fold from their headers** (3c, S1), remembered per browser in
  `conductor.sidebar.folds`; a folded header keeps its count and up to six
  status squares; Exited starts folded; Needs you opens again by itself for a
  new prompt (`newlyNeedingInput`, the notifications' own rule), the first
  list a page sees priming the comparison so a reload reopens nothing.
- **No scoping on a run page** (3f, S1): the full list shows with the open
  run's block marked, its section unfolded and scrolled into view; the Crew
  box and the laptop headings are gone.
- The counts: Needs you is how many sessions need you, members included;
  Running is that section's live sessions; Exited is that section's.
- **Actions on a row** (3c, S2): Share, Stop and More appear on hover in
  place of the dot, outside the row's link (a link holds no button); a run
  header offers Share run, Stop run and Open run; a member has its own Share
  and Stop. Stop asks in the row, with the design's words
  (`stopQuestion`). A right-click or a touch long press (Reka's context
  menu) opens the same list as More (`rowMenuItems` in
  `utils/sidebarActions.ts`): Open, Open the run, Share…, Show in the Yard
  (`/yard?focus=<id>`), Stop…; a run header Open run, Share run, Stop run.
  One Share dialog serves every row, mounted on its target a tick before it
  opens so that it mints the link.
- **Answer in the row** (3b, 3c, S3): `rowPrompt` (`utils/sidebarActions.ts`)
  turns a row's prompt into choices (numbered 1..9, `UKbd`) or a reply
  field, and marks a hosted session whose host is away, where the controls
  wait disabled with "The host is away". `SidebarPrompt` sits below the
  row's link (a link holds no button). The answer takes the Yard's path,
  `useQuickReply` (a short-lived control connection, the relay for a hosted
  session), and its busy set, so the Yard's card and the row agree; the
  stream clears the prompt and the row moves to Running by itself. The
  field stops its keydown events, so the window's digit listener and, from
  S4, the list's keys never see what is typed.
- **The list's keys** (3c, S4): one `keydown` handler on `[data-session-list]`
  in `SessionSidebar.vue`, live while a row has the focus (`focused`, a row
  id, set by `focusin`; the filter's ↓ puts the first row there). ↑ ↓ or J K
  move by `moveFocus`, Enter opens, 1–9 answer the focused row's prompt
  through S3's path (digits are consumed even without a prompt), R opens a
  member's run, S shares, X asks in the row with the question's Stop button
  focused (Enter stops, Escape cancels), Escape leaves the list. Every key the
  list owns calls `preventDefault` and `stopPropagation`, so the Yard's J K
  and `QuickReplyBar`'s window digit listener (which also returns for a
  target inside `[data-session-list]`) never see it; a field in a row keeps
  its keys, Escape excepted. The stop question is one model
  (`confirmingId`): a hover Stop and the X key set the same one, and the
  row components take `confirming` as a `defineModel`. Focus is by row id,
  so a row that moves sections after an answer keeps it. `SIDEBAR_SHORTCUTS`
  ("The sidebar") lists the keys after Everywhere in the shortcuts modal;
  the `/` row moved into it. Links stay tabbable in the usual way: no roving
  tabindex, Tab walks the rows.
- **The header's menus and a foot of pages** (3b, S5): `SidebarHeaderMenus`
  beside the name holds Alerts (the two switches of `useAttentionSettings`)
  and your menu behind your initials, an amber chip while no workbench
  token is set (`accountItems` in `utils/sidebarActions.ts`: Your name…,
  Keyboard shortcuts, Toggle theme, Workbench token…, Forget token). The
  foot holds the pages alone; the six-icon `[data-sidebar-tools]` row is
  gone. On the rail the same two stack in the foot above the expand button.
  Your name is a small dialog (`[data-name-dialog]`, its input still labelled
  "Your name"); `WorkbenchTokenGate` stays the layout's, opened by the menu.
- **The rail** (3d, S6): `railModel` (`utils/sidebar.ts`) draws the rail
  from the list's model, the links shared with you and the Events page's
  marks: `RailShape`s in the list's order, a square a session, a capsule a
  run holding its members' squares (`SidebarRailSquare`), the shared
  squares with a globe, exited runs dashed, loose exited sessions folded
  into `+N`; two counts on top (`counts.needs`, and how many sessions carry
  a mark). The corners: the state top right (the amber one following the
  Events page's Badge route as the rows' dot does), new events bottom right
  (one per session today: the Events page keeps one badge a session; C4
  folds the unread chat into the same number), the origin tile bottom left.
  Every shape's tooltip names it in words (`railRunLabel`: "users api · run
  started 08:31 · review needs you · core running · lead exited"). A capsule
  is a `div` with the play icon a link to the run and each member its own
  link (a link holds no link). `railGroups` and its types are gone.
- **The phone** (3e, S7): the list is the home screen, `pages/sessions/index.vue`
  (`SessionSidebar page`, `[data-session-list="page"]`), where `/` sends a
  phone (at lg and up the route does what the home does: the first session
  worth looking at); the layout renders the sidebar's own list only at lg and
  up (`useMedia`, the same composable as C2's), every page navbar has no
  hamburger (`:toggle="false"`), and `BottomBar` sits at the foot below lg:
  Sessions, Yard (the count), Crews, Events, More (a `UDrawer` with
  Roundhouse, Agents, Settings on the desktop app, `AlertSwitches` and
  `accountItems` as buttons; `NameDialog` and `AlertSwitches` are split out
  of `SidebarHeaderMenus` for it). The session, run and workbench join pages
  get **Back** to the list below lg (`[data-back-to-list]`). A prompt in the
  page's list takes full-width 44 px buttons (`SidebarPrompt big`). A touch
  long press (500 ms, `pointerType` touch) on a row or a run's header opens
  `SidebarRowSheet` (`UDrawer`, `rowMenuItems` as 44 px buttons, Cancel);
  the context menu is off for a coarse pointer and the row's callout
  suppressed; Stop asks again in the row. `viewport-fit=cover` lets the bar's
  padding read the safe area, and the dashboard group leaves it room below
  lg. No swipe actions.
- **Chat beside the terminal** (2a, 2b, 2h, C1). One chat per session,
  over the connection the terminal takes (`chat`, `chat_send`,
  `chat_history`; `welcome.chat` gates a client), so it works wherever a
  link works and the switchyard never reads it. Every viewer may post; a
  controller's "To agent" and "Send to agent" type a message into the
  agent through `Submit` and leave a `sent_to_agent` marker. The session
  keeps 200 (`session.chatRing`), replays the newest that fit 128 KiB, and
  takes no post once ended. Chat lines are not activity entries: their own
  ring, frames and hook (`Options.OnChat`), no bucket shared with hook
  reports, no 500-byte cut, nothing in the run log or the webhooks. Chat
  frames are encoded without HTML escaping (`proto.MustControlRaw`) so a
  2 KiB message fits the 8 KiB control frame. Join and leave lines coalesce
  by name. Unread is the browser's own count, in memory for now; the
  sidebar's pills come with C4. A per-connection bucket of 10 a second,
  burst 20, is new: viewer connections had no message bound before.
- **Chat on a phone and on a guest's page** (2c, 2d, C2). No server
  change. Where the inspector is not rendered (below `xl`) the session
  page's header gets a Chat button with the unread count that opens the
  chat as a sheet: `UDrawer` from the bottom on a phone (snap points two
  thirds and full, no overlay, not modal, so the terminal stays live and
  usable behind it), `USlideover` from the right in between. The join page
  wires the same thread over the guest's own connection (so a switchyard
  guest has it too): a panel beside the terminal at `md` and up, which the
  header's button folds away, the sheet below; a view-only guest reads the
  2d note and sees no agent actions. A thread counts as open only while it
  is in front of the person (`useMedia` says whether the inspector is
  rendered), so a phone counts what the hidden inspector's tab would have
  swallowed. Nothing in a hello or welcome changed.
- **Run chat** (2e, 2f, C3). One thread per run across its members:
  `session.ChatRoom`, made by the engine's run, which every member session
  is made with (`Options.RunChat`) and joins at launch. Read and posted
  over any member's connection with scope `run`, so a run link's guest and
  a run published through a switchyard have it without the switchyard or
  a run socket knowing (option A of the plan; option B, a run socket, was
  rejected for that). A member's new viewer is replayed the run's chat
  after the session's; the people on any member make the roster
  (`chat_roster`, one row per name with the member they look at, bounded
  at 32 so a full list of the longest names fits a control frame; `count`
  says how many); join and leave lines are counted once across the
  members. The run page opens one quiet connection (`hello.chatOnly`) to a
  live member and moves it when that member ends: no scrollback or output,
  not a viewer of the session, no line in its chat, on the run's roster
  (`useRunChat`). A controller's `chat_send` with scope `run` types a kept
  message into the member it names as a broadcast does, nothing while it
  waits on a prompt, answered `not_sent{needs_input|not_running|unknown|
  no_enter}` (the broadcast's reasons, now `session.NotSent*`); the
  composer's menu (`scopeItems`) disables such a member with the broadcast
  bar's words before anything is sent. The run's chat goes into its record
  (`chat` on `GET /api/runs/{run}` and the record; lists strip it; 500
  worst-case messages stay under the 4 MiB record read) and stays open
  while the run is kept; a resumed run starts a new one. The `send` emit of
  the chat components now names where a message goes ('', `agent`, a
  member).
- **Unread elsewhere** (2g, C4). The admin stream carries `chat` events
  (`eventHub.chat`, from `Options.OnChat` and `Engine.OnRunChat`, as
  droppable as `activity`, reaching no sink), so the browser counts the
  chats it has no page on. `utils/chatUnread.ts` keeps the counts per
  thread in `conductor.chat.unread` with the thread's last opening and the
  last 64 message ids, so the same message from the connection and the
  stream counts once, nothing older than the opening counts, a reload
  recounts nothing seen, and another tab's writes are read back
  (`storage`). `ChatUnreadPill` sits on every session row and run header
  (neutral, never a status colour); the rail's bottom-right number is the
  Events badge plus the unread chat, a capsule's its members' plus the run's
  own, and the tooltip names both ("alone · running · done · 3 unread in
  chat"). A hosted session's chat stays on the host until C5.
- **Hosted chats on the stream** (2g for `conductor host`, C5). A host sends
  each message its session keeps as `chat{sessionId, message}` on its
  control connection (the local session's `OnChat` hook queues it on the
  same bounded forwarder as activity, one goroutine sends in order); the
  server's `HostedSession.HostChat` cleans it (the connection's session
  whatever the host named, the known kinds only, scope `session`, bounded
  ids, cleaned names, the text cleaned and cut to the bound, `to` only as
  `agent`) and the hub's `OnChat` hands it to `eventHub.chat`. A server
  publishing its sessions to a switchyard sends their chat up the same way
  (`Published.OnChat`), so the switchyard's stream shows it as a hosted
  session's. `HostedSession` stays a pass-through for the chat itself: the
  host keeps it and serves its viewers. Found by the chat tests on the way:
  a session's end revoked its links and closed their viewers as "link
  revoked" (4403), racing the status frame, so a guest on a view link could
  miss that the session ended; `DisconnectLink` now closes them as the
  session's end (4410) once it has ended, which the join page already takes
  as ended.
- **The agents' questions in the chat** (C6, the owner's fast follow-up:
  "can we have the agents send questions to the chat as well when input is
  needed?"). A session going `needs_input` keeps the attention's message
  and choices in its chat as a `question` from the agent (`By.Role`
  `agent`, `Options` the attention's, at most six; `askInChat`), and the
  answer, whoever's input cleared the prompt, as a `system` line with
  `event: answered` and `ref` the question's (`answeredInChat`; "Conductor"
  when nothing named the person). A run member's question and its answer
  go into the run's chat too, on the member (`tellRun`). The thread draws a
  question amber-tinted, its choices as buttons for a controller while it
  stands (`answeredQuestions` in `utils/chat.ts`), typing the choice's
  input as the quick-reply bar does: into the session on its page and a
  guest's, into the member from a run's chat (`useQuickReply`, which now
  reaches a link's server too, for the guest on a run link). A question
  counts as unread like any message; the answered line never does. A
  question of the longest message with six of the longest choices fits the
  control frame (`TestAQuestionFrameFits`).

### Verified (round 11)

- S7: `phone.spec.ts` at 390×844 with touch: `/` lands on the list page with
  the filter on top and no sidebar list or hamburger; the bar's five targets
  are 44 px and the Yard's count shows; a prompt's buttons are full width and
  44 px and one answers; a row opens its page and Back returns; a long press
  opens the sheet with Open, Share…, Show in the Yard, Stop…, Cancel, Stop
  asking again in the row, and a run's header's with Open run, Share run,
  Stop run…; More lists the pages, the alerts and your menu. By hand on a
  phone (the keyboard on a reply field, the safe area): pending.
- S6: `sidebar.spec.ts` collapses the sidebar and reads the two counts, a
  capsule holding two members with the amber play icon while review asks
  and its tooltip in words, the news corner on a session given a `done`
  attention, the machine tile on the hosted stub, the stopped run's capsule
  and members dashed, and `+N` opening the full sidebar on Exited; vitest
  covers `railModel` (the order, the counts, the capsule's members and
  label, the folded exited, every tooltip).
- S5: `sidebar.spec.ts` finds no `[data-sidebar-tools]`, opens Alerts from
  the header, lists the account menu's five items, sets a name through the
  dialog (the button then says whose menu it is), and reads the foot as the
  five pages; `theme.spec.ts` toggles the theme from the menu; vitest covers
  `accountItems`.
- S4: `sidebar.spec.ts` drives the keys: `/` then ↓ focuses the first row,
  J K move, Escape leaves; with a page open on one asking session and the
  other's row focused, `1` answers the row's session alone (the transcripts
  and the page's attention say so) and the row keeps the focus as it moves to
  Running; X asks and Escape takes it back, S opens the share dialog, Enter
  opens the row, R from an exited member opens its run. vitest covers the
  shortcuts group.
- S3: a choice clicked in the row (`choices.spec.ts`) and a line typed in
  the row's field (`sidebar.spec.ts`) reach the stub's transcript, clear the
  prompt and move the row to Running without leaving the page; `rowPrompt`
  in vitest, the host-away state among it.
- S2: the hover actions, the in-row stop question, the context menu and the
  run header's actions are Playwright-covered in `sidebar.spec.ts`
  (share from a row, Show in the Yard, a member stopped from its row, a run
  stopped from its header landing whole in Exited, a loose session stopped).
  By hand: the long press on a phone, pending.
- S1 rendered headless at 1440, dark and light, against 3b: the mix of two
  loose sessions (one asking), a run of three (one asking, one exited), a
  hosted session with its machine, Exited folded "· 4" with squares; on a
  run page the block is marked. By hand on the installed app: pending.
- C1 rendered headless at 1440 against 2a (dark and light, the hover
  action) and 2b (the closed tab's count). By hand: pending.
- C2 rendered headless at 390 against 2c (the header's count, the sheet
  open over the terminal, dark and light) and at 1440 against 2d (the bare
  guest page with the panel and the note). By hand on a phone (the keyboard
  pushing the composer up, the drag to full height): pending.
- C5: Go tests for the cleaning in `signal` (every kind, a bad role, no
  id, a bad time, the cut), for the fake host's message reaching the stream
  and a bogus kind costing nothing (`ws_e2e_test.go`), and for a real host
  whose relay viewer's post reaches the stream (`hostagent`); Playwright
  `chat.spec.ts` scenario 7 (a line said in a hosted session counts on
  another person's sidebar row).
- C4: Go tests for the `chat` event's shape and its reach (no sink) and
  for the stream carrying a session's and a run's chat; vitest for the
  store (counted once, never a system line or one's own, nothing older than
  the opening, bounded ids, the read and write) and the rail's unread;
  Playwright `chat.spec.ts` scenario 6 (two messages count on another
  person's row and rail corner, survive a reload, clear on opening and stay
  clear; a run's header counts the run's own chat).
- C3: Go tests for the room (fan-out across members, the replay order, the
  roster, joins coalesced, quiet connections, sends with every refusal),
  the roster's bound (proto) and the run chat over WebSockets with a run
  link, the record and the lists (`crews_test.go`); Playwright
  `chat.spec.ts` scenario 5 (two people on the run page, the composer's
  menu with review skipped and lead typed into, a guest on the run link
  with the view-only note, the run stopped). Rendered headless at 1440
  against 2e and 2f. By hand: a run published through the switchyard from
  the LAN box with a phone on its link, pending.

## Round 10: the startup splash, the sponsor credit, a folder button on every "Runs in" (2026-10-05)

Asked on 2026-10-05, with the round 9 checks: "whenever we show 'runs in' we
should have the same folder button to the right"; "the settings page we
should use the whole width"; a startup banner while the app starts, with the
logo and the build; a copyright; and "sponsored by RockSolid Labs" following
the RockSolid Labs Sponsor Kit (claude.ai/design).

### Decisions

- **The folder button is part of the directory field**, so the crew editor's
  "Runs in" and the launch dialog's working directory both have it. Its
  picker browses the allowed roots only (scope `roots`, no Home), opening on
  the field's text or, when empty, the server's default directory.
- **Settings uses the whole width**: two columns of cards from `xl`, the
  save row under them.
- **The splash** is a small frameless window shown as soon as Electron is
  ready, until the workbench window shows (or 20 s): the Conductor mark, the
  version, platform and Electron, what the app is doing (checking WSL,
  preparing the server, starting it, opening the workbench), the copyright,
  and the kit's "Sponsored by" badge. It is dark, the switchyard pages'
  palette, with no preload; its link opens in the system browser.
- **The credit, in the kit's words** ("Sponsored and maintained by RockSolid
  Labs", linking to rocksolidlabs.io), only where the kit puts it: the last
  row of Settings → The app (desktop and browser), the switchyard pages'
  footer, under the card of a guest's join page on a switchyard, the last
  line of `conductor version` and `--help`, the README, and the splash. Never
  in the workspace, a log or run output. The logos are RockSolid Labs' own,
  bundled (`web/public/sponsor`, `desktop/static/sponsor`), never fetched.
- **The copyright and the license.** "© 2026 the Conductor Authors and
  RockSolid Labs, Inc." (the owner's wording, 2026-10-06), on the splash, in
  Settings → The app and the README; the repository is under the Apache
  License 2.0 (`LICENSE`, with a `NOTICE` naming the holder and keeping the
  RockSolid Labs and Conductor marks out of the grant), named in Settings →
  The app, the switchyard pages' footer and the README.
- `GET /api/whoami` gains `version`, for The app card in a browser.
- **Tests at both ends, as a rule** (AGENTS.md, 2026-10-06): every feature
  ships with a Go test and a UI test (vitest for logic, Playwright for what a
  person sees); the by-hand list is only for what no harness reaches. With
  it, the browser test of a crew run link through the switchyard, deferred
  in round 9: a second server publishing to the spec's switchyard, Share on
  its run page saying "Works from anywhere", the link listing the members on
  this workbench, a member's terminal through the relay, and a revoke at
  home ending the link there (`web/e2e/switchyard.spec.ts`).
- **What is owed lives in one place**, `docs/tasks-todo.md`: the bugs known
  and the features set aside, by area, each item with the round that found
  or deferred it; the rounds' own "Deferred" lists stay as history.
- **Zoom in the desktop app** (reported on Windows: zoom in did nothing).
  Electron's zoom roles bind Ctrl+Plus, which needs Shift, and on Windows and
  Linux the page sees a key before the menu, where the terminal takes Ctrl+-
  as a control character. The app now takes Ctrl+= / Ctrl++ / numpad +,
  Ctrl+- and Ctrl+0 (Cmd on macOS) before the page sees them, and keeps the
  level in its settings, since the server's port, and with it Chromium's
  per-origin zoom, changes at every start.
- **A stopped run is recorded as stopped.** Stopping a run ends its members'
  sessions first, and the last one ending recorded the run as finished on a
  goroutine racing the stop's own record: about one stop in twenty kept
  "finished" (the CI flake in `TestRunRecordIsWrittenWhenARunEnds`, seen
  at 100 runs under race). A run being stopped is now recorded only by its
  stop, and the record saves keep the order their snapshots were taken in.

### Verified (round 10)

- 2026-10-06, by the owner on the installed v0.6.0-rc.6: the splash, the
  folder buttons and the full-width Settings page, the credit on the
  switchyard pages and in `conductor version`, and zoom (Ctrl+=, Ctrl+-,
  Ctrl+0, the View menu, the level kept) all passed.

## Round 9: the Events page, the switchyard dark, remote sessions in the sidebar, the WSL picker, and the leftovers of rounds 7 and 8 (2026-10-04)

Asked on 2026-10-04: the Events page from its design hand-off; the
switchyard pages dark as their design has them; "the settings in the
windows app opens the windows folders which makes zero sense"; remote
sessions in the sidebar "with an icon above similar to the crew icon", so an
invite no longer takes the app over; the Wall named the Yard and the
Carousel the Roundhouse; and the deferred items of rounds 7 and 8 the owner
picked.

### Decisions

- **The Events page** has three tabs: Feed (default: events by minute, a crew
  member named `<crew> / <member>`, Answer / Open run / Open on the row; beside
  it the last hour as bars, where events go, who reports), Routing (the ten
  events grouped by how loud they are, destinations as switches) and
  Integrations (a table, the snippet of an agent nothing wires open below).
  The bars are divs; with the Wall's donut gone too, `nuxt-charts` has no
  user and is dropped.
- **The switchyard's pages are dark always**, whatever the system prefers
  (the owner: "the switchyard UI should be the dark mode theme"); no toggle.
- **The Yard and the Roundhouse.** `/yard` and `/roundhouse`; `/wall` and
  `/carousel` redirect, a focus query kept; the keys stay G W and G C.
- **Remote sessions in the sidebar.** Hosted sessions sit under their machine
  (a laptop and its name) after this server's own and the crew runs. A link
  joined from the workbench is kept in the browser (`conductor.joined`, at most
  20, pinned to its server) and listed at the top under "Shared with you" with
  a globe; opening it joins at once, Leave keeps it, × forgets it, and it is
  looked at again every minute. The join page sits beside the sidebar when the
  page has a workbench token (the desktop app, an owner's browser), chosen
  once so a live terminal never remounts; a guest's page is unchanged and
  keeps nothing.
- **The folder picker browses the server.** `paths.browse: any`
  (`CONDUCTOR_PATHS_BROWSE`, which the desktop app sets) lets
  `GET /api/paths?scope=any` list outside the roots; Settings' picker is a
  workbench modal over it, so on Windows it lists the distribution's folders.
  The Electron dialog and the Windows path conversion go.
- **A run's own name** (`Run.label`, given at launch, `conductor up --name`,
  kept by a resume) and **filters on the Runs tab** (state chips, a time
  select, kept in the browser).
- **Render fixes:** handoff edges on an arc of their own, the Yard's
  attention as a slim stacked bar, the phone run header's counts on a strip.
- **The upgrade notice:** a settings file from before the notices existed
  owes the publishing notice, shown once as a toast; a fresh install owes none.
- **The switchyard:** the open hosts of one address share one relay bound;
  a host registers under an instance and a local id, which fix its session's
  id, so a switchyard restart gives it back and `registered.links` says which
  links survive; the switchyard keeps the links it mints for such hosts in
  `links/` (a join while the host is away answers `503 host_offline`); and a
  crew run's link is minted there (`link_run`, kept current by
  `link_run_update`).

### Verified (round 9)

- 2026-10-05, by the owner on the installed v0.6.0-rc.4 and the deployed
  switchyard: all nine by-hand checks passed. The upgrade notice, the
  Settings picker inside WSL, an invite beside the sidebar under "Shared with
  you", a `conductor host` session under its machine, a run link from a phone
  across a switchyard restart, the dark switchyard pages, the Events page and
  the Yard / Roundhouse names, the View / Control switch on a Share link, and
  a session that ends taking its links (no revoke error). The one follow-up
  (a folder button beside every "Runs in", Settings using the whole width)
  is round 10.

### Deferred

- An e2e test of a run link through the e2e switchyard (the Go loopback test
  covers the protocol and both servers).
- Pushing an invite to the open window over a bridge event instead of a
  full load; the join route answering a browser origin named in Settings;
  link tokens in the OS keychain; a page behind a machine's sidebar header.
- A forgotten run revoking its links at the switchyard (they expire, or the
  orphan sweep takes them after seven days).

## Round 8: saved crews and runs told apart (2026-10-04)

From the design hand-off "Crews Redesign": one page held both the saved crews
and their runs, a narrow list where each crew carried a "Running" badge and
its runs were unnamed rows ("3", "33s…"), beside an editor that filled the
screen. Nothing said which was the plan and which the thing running.

### Decisions

- **Two nouns, said every time.** A crew is a saved plan; a run is one
  launch of it. "Crew view" is the run page; "Launch 5 agents" is "Launch
  run"; "Stop all" is "Stop"; "Resume run" is "Resume as new run"; "Share
  crew" is "Share". A run is named by its crew and its start ("users api ·
  run started 08:31") with its short id beside it, never by its id alone.
- **Status belongs to runs.** A saved crew never says Running or Ready: its
  row says "1 live run" when one exists, leading to it, and its page shows a
  banner for each live run ("started 08:31 · 3 running · review needs you ·
  docs waits for Start now · Open run"). "Draft changes" became an
  unsaved-changes bar at the bottom of the crew's page that names what
  changed and, while a run is live, says the run keeps the version it
  launched with.
- **One Crews page, two sections.** `/crews` is the home: "Running now"
  (one card per live run: badge, the members as chips in the crew's shape
  with a status dot each, a line per member that asks with Answer beside it,
  up time, Stop, Open run) above "Saved crews" (a table: name and goal, the
  shape in miniature with words such as "5 · a chain of 3, 1 by hand, 1
  alone", where it runs, the last runs as bars, Edit, Launch run).
  `/crews/<id>` is one saved crew with Setup and Runs tabs; `/crews/new` the
  draft of a crew not saved yet (the server never derives the id `new`).
  The summary `GET /api/crews` lists carries the goal now.
- **Start rules in words.** "At launch", "After lead is done", "When you
  press Start", each with an icon, in the table's Starts column, on a graph
  node's last line and in the sentence beside the Members heading ("lead →
  core → tests start in a chain; docs waits for you; review starts at
  once"). Graph edges read "when done"; a member started by hand is drawn
  dashed; the column gap grew to 96 px so the label fits. "Runs on" (Server
  / My machine) left the editor: hosted crews come later, and the field
  said nothing until then. The switches of a launch (open the run page, a
  view link, yolo) sit behind the line under "Each run".
- **Stopped is not failed.** A member a stop cut off reads "not started"
  (never started, or `the run is stopped` as its error) or "stopped", in
  grey; red is kept for an error of the member's own, and a run with one
  is "failed" whether it was then stopped or not. The Runs tab's rows carry a
  note: who asks a question, what failed ("tests: exited 1"), how many a
  stop cut off ("3 never started"), who waits for Start now, else what
  changed ("+412 −58 on 5 branches").
- **The chart moved** out of the 240 px column into the Runs tab, drawn as
  plain bars (the last twelve runs; the median, how many needed an answer,
  how many failed) with an amber edge where someone had to answer; the home's
  table draws the same bars beside each crew. `nuxt-charts` no longer draws
  it (the Events page and the Wall still use it).
- **Leaving a share.** The join page's headers have Leave: the terminals go
  (their connections close with them), the card comes back with the name
  kept, and the link still joins. Asked on 2026-10-04: "in a shared view I
  only see the one screen, no way to leave the share".

### Open verification (round 8)

- By eye on the installed app: the Crews home with a run going and one that
  asks a question; a crew's page under a live run; the graph's "when done"
  labels at the new gap; the Runs tab with a failed run (a member whose
  program exits non-zero before its prompt).

### Deferred

- Hosted crews ("Runs on: my machine"), and with them the field.
- A run's name of its own (today the crew's name and the start time).
- Filtering the Runs tab (by state, by date) once a crew has hundreds.

## Round 7: the switchyard as the way sharing works, dark always, the Windows app seeing WSL (2026-10-04)

Asked after installing the first release candidate on Windows: "I want the
conductor app to start in dark mode. I want switchyard to be the default for
sharing! I want conductor to just work when I click share an agent. I want
the names as examples in the UI removed. Also what the hell does 'Runs on'";
and from the Windows app: Codex "not installed" though it is in WSL, and
Settings showing Windows paths.

### Decisions

- **Open hosts.** A switchyard with `switchyard.openHosts` registers a host
  that presents no token, under per-address limits: `openHostSessions` live
  sessions (4), `openHostRegistrationsPerMinute` (6), `openHostRelayKBps`
  (128). A wrong token is still refused; a host token marks a trusted machine
  outside the limits. The relay bound is per connection, so one address may
  relay `openHostSessions` times the bound; a quota per address is deferred.
  The public switchyard runs with open hosts on, `maxSessions` 500 and
  `maxViewersPerSession` 8 (hosted sessions take the viewer cap from the
  config now; the hub hard-coded 32).
- **The public switchyard by default.** Every server publishes to
  `https://switchyard.rslabs.net` (`config.DefaultRendezvousServer`) unless
  `rendezvous.server` names another, `rendezvous.enabled: false`
  (`CONDUCTOR_RENDEZVOUS=0`) turns it off, or the server is a switchyard. The
  token is optional. The desktop app's Settings has the switch, on by default,
  and keeps the four switchyard settings across restarts (they were dropped
  on read). Every test harness that launches sessions sets
  `CONDUCTOR_RENDEZVOUS=0` (AGENTS.md).
- **Links minted at the switchyard are the sharing machine's.** It keeps a
  record of each (dropped when the publication ends, or when a switchyard
  restart re-registered the session and lost them), lists them with the local
  ones (`remote: true`) and revokes them over the host protocol
  (`link_revoke` / `link_revoked`, `error{not_found}`).
- **One-click Share.** Opening the dialog makes a view link for two hours,
  copies it and says where it reaches: from anywhere through the switchyard
  that minted it; or why the session is not there (the server waits up to
  ten seconds for a publication still being made, and a link made meanwhile
  carries `rendezvous: {server, error}`); or what a local link reaches.
  Opening it again shows the link already made (a host may ask a switchyard
  for five links a minute). View is the one-click role: a link that lands on
  the clipboard must not hand out typing rights by accident; control is one
  click more. Crew links are made here, not at the switchyard.
- **Dark always.** The workbench opens dark whatever the OS prefers, under a
  storage key of its own; the theme button switches to light and that is
  kept.
- **No invented names** in placeholders; the launch dialog asks where the
  agent runs only when the workbench is served from another machine.
- **The Windows app and WSL** (E1, E2 of the plan): the server in WSL gets
  the person's own shell's PATH, imported through `$SHELL -ilc` as the native
  app does, so nvm's and npm's bins count; a program found under `/mnt/` is
  named and not used; the settings on Windows are WSL paths, picked inside
  the distribution.

### Open verification (round 7)

- The public switchyard with open hosts on: a `conductor host` with no token
  from a second machine registered and showed in the operator card
  (2026-10-04). Still to see: the desktop app on this PC with nothing
  configured, listed there; Share → one click → the link on a phone on mobile
  data saying "Works from anywhere"; a revoke from the dialog disconnecting
  the phone.
- The Windows installer after E1 and E2: Codex identified inside the
  distribution, `/home/<user>` paths in Settings, a session launched there.

### Deferred

- Run (crew) links through a switchyard.
- A relay quota per address rather than per connection.
- The switchyard persisting its links across restarts.
- A one-time notice in the desktop app on the upgrade that turns publishing on.

## Round 6: switchyard, invites the app opens itself, and ICE from WSL without mirrored networking (2026-10-03)

Asked the same day Round 5 landed. `docs/round6-plan.md` is the plan.

### Decisions

- **The person configures no Windows networking, ever.** Not mirrored
  networking (it changes WSL for Docker and every other tool), not
  `netsh interface portproxy`, not a `.wslconfig` edit: whatever must cross
  Hyper-V's NAT, the desktop app forwards itself. The desktop app on Windows runs a UDP
  forwarder (a NAT in user space) in front of the server in WSL, which puts
  every WebRTC connection on one UDP port (7877) and advertises the Windows
  LAN address; ICE then crosses one NAT, the router's. The installer adds
  the firewall rule when it can; Settings offers it through one elevated
  `netsh` with fixed arguments.
- **Switchyard is Conductor in a mode.** `conductor switchyard` keeps the
  signaling hub, the host route, the links, the join route, the events
  stream and the workbench, answers `403 switchyard` on everything that
  launches, and relays only when `switchyard.relay` is on. Its load is
  signaling and the relayed terminals; one small machine serves many.
- **An invite is a share link the app opens itself.** Every link reply
  carries `conductor://<host>/join/<token>`; the app registers the scheme
  and opens its own join page with `?server=`, which fetches the link from
  the server named (loopback origins answered across origins, the session
  WebSocket accepted from them) and connects there. A machine that publishes
  to a switchyard mints its links there over the host connection (`link`,
  `link_created`), so the URL and the invite are the switchyard's.
- **Round 5 stands.** A reachable server with a certificate is still the
  path for a machine with a public address; switchyard is the path for the
  rest.
- **Paste invites need no server at all.** `/paste` gathers a viewer's ICE
  candidates without trickle into a blob; the session's side answers with
  one (`POST /api/sessions/{id}/paste`, the Share dialog); the data channel
  then runs machine to machine. Most home-to-home pairs connect; carrier
  NAT and office networks do not, and there is no relay, so they take the
  switchyard. A public switchyard may bound what a host relays
  (`switchyard.relayKBps`).

- The switchyard serves no workbench (2026-10-04, from the design "Switchyard
  Pages"): `/` is a server-rendered landing page (what the server is, how a
  link looks, how a machine publishes to it, the status card: version,
  uptime, TLS and its renewal, the relay, the public address, the invite
  form; the operator's figures behind the workbench token), every workbench
  path is a 404 page pointing at the Conductor on one's own computer, and the
  app is served only for `/join/<token>` and `/paste`. The join page names
  the switchyard it was shared through. Both pages are Go templates with the
  brand tokens inlined, so they stand without the app built.
- Restarting the switchyard (2026-10-04, seen live): the publishing server
  retried resuming its old hosted session forever, refused with `4404` each
  time, until it was restarted. A host answered `4404` on a resume now
  registers afresh under a new id (tested against a fake switchyard that
  forgets); links minted before the restart are gone, since the switchyard
  keeps hosted sessions and links in memory. Persisting them is deferred.

### Open verification (round 6)

- Two desktop apps on two home networks through a switchyard on a VPS, one
  of them from WSL in NAT mode: the invite opens the app, the terminal
  connects direct (the transport badge says WebRTC) and falls back to the
  relay when a router refuses. The e2e suite proves the relay path on one
  machine; the direct path needs two networks.
- The installer's firewall rule on a per-user install (no administrator):
  the rule is left to Settings, which must add it with one prompt.
- The `conductor:` scheme registered by each installer (NSIS, dmg, deb,
  rpm, AppImage) and an invite clicked in a browser reaching a running app.

- 2026-10-04, a switchyard on a VPS (a Lightsail nano in Ohio, Ubuntu 24.04,
  a systemd service with a root-only env file, `reach.mode: manual`, the TLS
  listener on 443, Let's Encrypt's IP certificate issued in seven seconds):
  a server on a Linux machine on the home LAN published a shell session to
  it (`CONDUCTOR_RENDEZVOUS_*`, reach off, no TLS of its own); the link was
  minted at the switchyard (`remote: true`), the switchyard listed the
  session as hosted, and a browser on another machine (inside WSL, behind
  Hyper-V's NAT and the same UniFi router) joined it over **WebRTC direct**
  (69 ms) with typing reaching the shell. Both machines share one router,
  so a viewer on another network (a phone on mobile data) is still the
  proof that the path crosses two NATs; the relay fallback is not exercised
  yet. Later the same day the switchyard moved to a domain name
  (`tls.acme.domains`, `publicUrl` on the name, reach off): Let's Encrypt's
  ninety-day certificate came in seven seconds, and the publishing server,
  still dialing the address, failed its TLS handshake ("bad certificate")
  until its `rendezvous.server` was repointed at the name, which is the
  expected shape: a certificate for a name does not cover the address.

### Deferred

- The app forwards the TLS listener into WSL and maps the router from
  Windows (UPnP, PCP, NAT-PMP from the Windows side), so the public-address
  path of round 5 works from WSL with nothing configured by hand. Until
  then the phone test of a public link is done from a machine that is not
  WSL.
- Switchyard rosters and invite lifetimes beyond the share store's: who is
  online, invites that name a person.
- A relay quota per host on a switchyard, for a public one.

## Round 5: sharing without configuration, a test tier, agent identity, desktop, the skill and the crew graph (planned 2026-10-03)

Asked by the user after running round 4 on 2026-10-03; decided the same day.
One plan, `docs/round5-plan.md`; the design of the crew graph and the charts
is handed to Claude Design through `docs/design/crews-graph-brief.md` on the
branch `design/crews-graph`.

### Decisions

- **STUN and ICE connect the terminal channel, not the link.** STUN gives a
  browser or a `conductor host` its public address and port so ICE can form a
  direct UDP path once the join page has loaded from the server; it carries
  no data and makes no HTTP page reachable. What makes a link open from
  outside is a reachable server: a forwarded port, a public address or a
  proxy. Round 5 makes the first automatic.
- **Reach is automatic everywhere, for the TLS port only.** `reach.mode` is
  `auto` by default for `conductor serve` and the desktop app: the server
  asks STUN for its public address and maps external 443 to its TLS listener
  through UPnP IGD, then PCP, then NAT-PMP, all written against the RFCs with
  the standard library and tested against loopback fakes; it renews the lease,
  re-checks the address and unmaps on shutdown. The plain listener is never
  mapped: plain http never leaves the network. `off` turns it off, `manual`
  only discovers the address for a port the person forwarded. `GET /api/reach`
  reports the state and the Share dialog says what a link reaches.
- **No public link until the certificate is ready.** While the server has no
  certificate for the public address, links keep the address the request came
  through and the Share dialog says why. Precedence of a link's base: an
  explicit non-local `publicUrl`; the discovered `https://<ip>` while mapped
  and the certificate is ready; the request's address with forwarded headers
  honoured; `publicUrl`. The notify URL agents get stays local.
- **TLS through lego, ACME only.** `tls.acme` obtains a certificate from
  Let's Encrypt through `go-acme/lego` as a library: for the STUN-discovered IP
  address (an IP-address certificate, `shortlived` profile, six days, renewed
  at two thirds of its life, `tls-alpn-01` on the mapped 443) or for configured
  domains (`dns-01` through lego's providers, `http-01` on a mapped 80, or
  `tls-alpn-01`). `tls.certFile`/`keyFile` serve a pair of the person's own.
  Nothing self-signed, no fingerprint pinning, no redirect from plain http and
  no HSTS (the plain listener serves the desktop window and local hosts). The
  TLS listener is its own (`:8443` by default, mapped from 443). The
  certificate manager serves nothing before the first issuance, so a handshake
  fails cleanly rather than trusting a made-up certificate. Tested against
  Let's Encrypt's Pebble in CI.
- **Mapped is not verified.** The server's self-check through the public URL
  reports "ok" or "unverified", never "unreachable": a router that does not
  hairpin refuses the server's own request while a phone on mobile data gets
  through. A person on another network is the proof, and the open
  verification below asks for it.
- **Local sessions publish to a rendezvous.** A server with `rendezvous`
  configured publishes its sessions to a public Conductor through the host
  protocol, one session per host connection as today; links for them are
  minted on the rendezvous. TURN credential minting (coturn's static secret)
  is optional and last.
- **Every by-hand check gets a tier.** CI runs the stub-agent suites; a
  weekly nightly runs the built-in sites on the network and the recipe flags
  against freshly installed CLIs without accounts; a live tier with test
  accounts (Claude Code and Codex first, keys in a reviewed GitHub
  environment, cheapest models, one-word prompts) runs on a schedule or by
  hand. The stub agent grows an identity, a transcript per session id, a
  trust dialog and Codex- and Claude-shaped reports so the trust hold, resume,
  the title thread, the tiles and the keyboard are tested in CI. Antigravity
  and DeepSeek Harness stay by hand (no automatable sign-in).
- **Agents are identified by their version output.** Each adapter carries a
  probe (arguments and a pattern); the server runs it on the resolved program
  with a timeout and bounds, caches the result and reports `identity` on the
  catalog. The Agents page shows the name and version, names an impostor
  (`goose` on the PATH that is the Go migrations tool), and a crew with a
  misidentified member is refused at launch; a pending or failed probe refuses
  nothing. A saved agent without an adapter is not probed.
- **A stopped run resumes both ways.** Resume on an ended member of a stopped
  run reopens the run in place once its stop has completed; "Resume run"
  creates a new run of the crew in which every member with a resumable agent
  session continues its conversation in its kept worktree and branch, and the
  others start afresh under their start rules.
- **Desktop: Electron, with Windows through WSL2.** A `desktop/` shell packages
  the Go server (macOS dmg and zip, Linux deb, rpm and AppImage) and talks to it
  through a one-line JSON handshake on stdout and a stdin that ends the server
  when the shell dies; the workbench token lives in memory for the shell's run.
  Windows gets no native server: the installer bundles the Linux binary and runs
  it inside the user's WSL2 distribution, with a first-run screen that explains
  `wsl --install`. (Round 6 settled how WSL reaches out: the app forwards,
  the person configures no Windows networking.)
- **The skill's commands are also MCP tools.** `conductor mcp` serves
  report, set_state, ask, form_crew, add_member, run_status and link on
  stdio (JSON-RPC 2.0, one message a line); a server's launch registers it
  with Claude Code (`--mcp-config`) and Codex (`-c mcp_servers.conductor.…`),
  both verified against the CLIs on this machine; `agents.mcp` turns it off;
  `conductor host` does not register it yet.
- **The skill reaches every agent, and an agent can form a crew around its
  own session.** The skill is installed at launch for every adapter with a
  skills directory (a file carrying the `conductor:skill` marker, never the
  person's own), and every session gets `CONDUCTOR_SKILL`. A session's agent
  token gains a scoped grant: form a crew around its own session (its cwd and
  yolo, never more; the session becomes the first member), add a member to its
  run, read its run, mint a view-only link to itself; two crews per session
  per hour. An agent never holds the workbench token. `conductor crew create|add|
  status|link` wrap the routes; `--open` shows a toast with the run in the
  workbench rather than navigating anyone. A needs-input report may carry
  choices that viewers answer with one click.
- **A crew graph and charts, with run records.** The run page and the crew
  editor get a graph (Vue Flow): solid edges for "after X idle", dashed edges
  for handoffs as they happen, one parent per node enforced while editing. A
  run timeline draws each member's bar with its waits and handoffs. Runs that
  end are recorded in `dataDir/runs/` (never terminal output) so the Crews
  page charts a crew's last runs (Nuxt Charts); the Events page charts activity
  per minute; the Wall shows sessions by attention state. No chart with fewer
  than two points.

### Open verification (round 5)

- The crew graph, the run timeline and the charts against a design: there
  was no hand-off (the brief on `design/crews-graph` was never run through
  the design tool), so the owner updates the mockups by hand from the built
  pages. A rendered review on 2026-10-04 (every page at 1440 and 390, both
  themes) found: an "after" edge and a handoff edge between the same members
  share one path, so the count badge covers the label; the Crews card chart
  is cramped in the sidebar column; a stopped run lists never-started members
  as errors; the Events chart is empty on every page load (feed only); the
  Wall donut reads as a spinner; the phone run header truncates its title.
- The skills directories: Cursor, Copilot, OpenCode, oh-my-pi and Amp read
  `~/.agents/skills` and the Antigravity CLI `~/.gemini/antigravity-cli/skills`
  by their vendors' docs (read 2026-10-03); DeepSeek Harness reads
  `~/.agents/skills` by third-party guides only. Each agent, launched from
  Conductor, should list the `conductor` skill (`/skills` or its equivalent).
- A link `https://<public ip>/join/<token>` opened from a phone on mobile
  data: the padlock, the join page, the terminal live. The server's self-check
  cannot prove it. 2026-10-04, from a Linux machine on a home LAN (not WSL),
  `CONDUCTOR_TLS_ACME=1` with reach auto and no email: STUN found the public
  address, no gateway mapped the port (below), so no certificate was ordered,
  the TLS listener refused handshakes with "no certificate yet" and links
  stayed on the request address, as designed. The LAN path passed: a control
  link to a shell session, opened from another machine, joined over WebSocket
  and typing reached the shell. Open until a router maps or forwards 443.
- Router models beyond the loopback fakes: which answered UPnP IGD, PCP or
  NAT-PMP, and which refused (list them here with the firmware).
  - UniFi OS gateway (Ubiquiti), 2026-10-04: refused all three. No SSDP
    answer from an Internet Gateway Device (a raw M-SEARCH from the same
    machine saw only a Roku and a media device), PCP and NAT-PMP refused on
    5351. UPnP is off by default on UniFi and it has no NAT-PMP; turning
    UPnP on, or a manual forward with `reach.mode: manual`, is the way.
- IP-address certificates from the real Let's Encrypt: the rate limits and
  the renewal every few days over a week. Pebble stands in for it in CI
  (`make test-pebble`): an IP identifier over `tls-alpn-01` with a renewal,
  and a name over `tls-alpn-01` and `http-01`, all validated by Pebble
  against the listener, pass on 2026-10-03. 2026-10-04: issued for real on
  a VPS running `conductor switchyard` with `reach.mode: manual` and the
  TLS listener on 443: the order, the `tls-alpn-01` validation and the
  six-day certificate (renewal at two thirds) took seven seconds, with no
  account email. The first order went to the host's IPv6 address, which
  STUN over a plain "udp" dial returned on a dual-stack host; the reach
  lookups are IPv4 only now (`udp4`), and the second order was for the
  static IPv4 address, which `https://<ip>/api/health` verifies with the
  system trust store. The renewal over a week is still to be watched.
- The TLS listener offers HTTP/1.1 only. Whether the WebSocket routes work
  over HTTP/2 (RFC 8441 extended CONNECT in Go's server and the browsers)
  decides whether `h2` can be offered later.
- The first nightly run: every **verify** row of the plan closed with the real
  CLI's help and version output.
- The Windows installer on a machine with WSL2: the first-run screen without
  WSL, the server in the distribution, the agents found, a link from Settings.
  2026-10-04: the release workflow ran for the first time on the tag
  `v0.6.0-rc.1` and, after four rounds of runner-only fixes (Playwright's
  dependencies on Ubuntu 24.04, POSIX-only desktop tests on Windows, no
  `make` on the Windows runner, git 2.55's reading of info/exclude, empty
  signing secrets read as certificate paths, the package version from the
  tag), built every package on the three runners into a draft prerelease:
  dmg and zip for macOS, deb, rpm and AppImage for Linux, the NSIS
  installer for Windows, all unsigned. The installer's walk-through on this
  machine is still to do.
### Deferred (round 5)

- A readiness wait for a resumed member: it is `running` the moment its
  session exists, while its agent takes a few seconds to show its prompt and
  no hook reports that, so a handoff or broadcast typed into it meanwhile
  can be lost (the live spec waits ten seconds). The start path's readiness
  wait (`awaitReady`) could hold the member at `starting` until then.

- A multi-session host protocol (one `conductor host` serving several
  sessions); round 5 publishes one session per connection.
- TURN credential minting, unless a network needs it.
- `conductor mcp`, the same actions as an MCP server for agents without a
  skills directory.
- Signing and notarisation of the desktop builds until the secrets exist; the
  builds ship unsigned.
- Resuming a run from its record after a server restart (the record holds the
  agent session ids and worktrees it needs).
