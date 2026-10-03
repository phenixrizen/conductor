# Features

Scope notes for the workbench redesign. The source mockup is the
claude.ai/design project "Conductor Mockups" (screens 1a workbench, 1b wall,
1c runs, 1d launch, 1e share, 1f join, 1g carousel). This file records what is
being built, the decisions taken along the way, and what is deliberately
deferred. Update it when scope changes.

## Delivered (2026-09-28)

Everything under Planned below shipped on the `workbench-redesign` branch.
The list is kept as the description of what exists; see `docs/protocol.md`
for the wire details.

## Delivered (2026-09-30)

- **Crews** (2a Crews, 2b Crew running). Saved teams of up to 12 agents with
  role prompts, start conditions and optional per-member git worktrees, run
  from the Crews page or `conductor up`; the crew view has tiles, handoffs,
  broadcast, run links and stop-all. See the README (Crews) and
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
  - `conductor up api-sweep` from a shell with `CONDUCTOR_ADMIN_TOKEN` set
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
  missing), using `CONDUCTOR_SERVER` and `CONDUCTOR_ADMIN_TOKEN` from the
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
  that; Codex's is Enter alone). Codex's rows failed because its login had
  expired (`Error: account/read failed … unauthorized (401)` at start): a
  `codex login` and a rerun are due. A resumed Claude Code member answered
  the word it was told (PINEAPPLE) once the broadcast waited for it to be
  ready.

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
  when the shell dies; the admin token lives in memory for the shell's run.
  Windows gets no native server: the installer bundles the Linux binary and runs
  it inside the user's WSL2 distribution, with a first-run screen that explains
  `wsl --install`; reach inside WSL2 needs mirrored networking and the app says
  so.
- **The skill reaches every agent, and an agent can form a crew around its
  own session.** The skill is installed at launch for every adapter with a
  skills directory (a file carrying the `conductor:skill` marker, never the
  person's own), and every session gets `CONDUCTOR_SKILL`. A session's agent
  token gains a scoped grant: form a crew around its own session (its cwd and
  yolo, never more; the session becomes the first member), add a member to its
  run, read its run, mint a view-only link to itself; two crews per session
  per hour. An agent never holds the admin token. `conductor crew create|add|
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

- A link `https://<public ip>/join/<token>` opened from a phone on mobile
  data: the padlock, the join page, the terminal live. The server's self-check
  cannot prove it.
- Router models beyond the loopback fakes: which answered UPnP IGD, PCP or
  NAT-PMP, and which refused (list them here with the firmware).
- IP-address certificates from the real Let's Encrypt: the rate limits and
  the renewal every few days over a week. Pebble stands in for it in CI
  (`make test-pebble`): an IP identifier over `tls-alpn-01` with a renewal,
  and a name over `tls-alpn-01` and `http-01`, all validated by Pebble
  against the listener, pass on 2026-10-03.
- The TLS listener offers HTTP/1.1 only. Whether the WebSocket routes work
  over HTTP/2 (RFC 8441 extended CONNECT in Go's server and the browsers)
  decides whether `h2` can be offered later.
- The first nightly run: every **verify** row of the plan closed with the real
  CLI's help and version output.
- The Windows installer on a machine with WSL2: the first-run screen without
  WSL, the server in the distribution, the agents found, a link from Settings.
- The crew graph, timeline and charts compared against the design hand-off at
  1440 and 390 in both themes.

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
