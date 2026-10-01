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
  from the UI), `crews.json`, generated hook assets under `hooks/` and the
  Conductor skill. The config file stays read-only; the data directory
  overlays it.
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

| Agent | Command | Waiting-for-input signal | Turn done | Tool events | Wiring | Verify before shipping |
|---|---|---|---|---|---|---|
| Claude Code | `claude` | `Notification`/`PermissionRequest` hooks (with options) | `Stop` | `PostToolUse`, `PermissionDenied`, `SubagentStop` | launch: `--settings <hooks/claude.json>` | — |
| Codex CLI | `codex` | `notify` (agent-turn-complete) + `tui.notification_method="bel"` | same | experimental `hooks.json` (`features.hooks`), not on Windows | launch: `-c notify=[…]`; file: `~/.codex/config.toml` + `~/.codex/hooks.json` | `-c` array syntax; PermissionRequest hook shape |
| Antigravity | `agy` | bell (no notification event) | `Stop` | `PreToolUse`/`PostToolUse` (`toolCall.name`) | file: `~/.gemini/config/hooks.json` (global) or `.agents/hooks.json` (project) | exact global path |
| Copilot CLI | `copilot` | `notification` (`permission_prompt`) | `agentStop` | `postToolUse`, `errorOccurred` | file: `~/.copilot/hooks/conductor.json` (own file, no merge) | permission prompt key bindings for options |
| Cursor CLI | `cursor-agent` | bell / pattern (CLI fires no waiting event) | `stop` | `postToolUse`, `afterFileEdit` | file: `~/.cursor/hooks.json` (merge) | CLI binary name; which events the CLI fires today |
| OpenCode | `opencode` | plugin `permission.asked`, `session.idle` | `session.idle` | `tool.execute.after`, `session.error` | launch: `OPENCODE_CONFIG_DIR=<hooks/opencode>` if additive, else file: `~/.config/opencode/plugins/conductor.ts` | whether `OPENCODE_CONFIG_DIR` adds to or replaces the default dir |
| pi | `pi` | `agent_end` | `agent_end` | `tool_call`/`tool_result` | launch: `--extension <hooks/pi-conductor.ts>` | — |
| oh-my-pi | `omp` | as pi | as pi | as pi | file: `extensions:` entry in `~/.omp/agent/config.yml` | pi extension API compatibility (issue #2166) |
| aider | `aider` | `--notifications-command` | same | none | launch: `AIDER_NOTIFICATIONS=true`, `AIDER_NOTIFICATIONS_COMMAND` | confirmations (`(Y)es/(N)o`) need the pattern detector |
| Goose | `goose` | bell / pattern (no waiting event yet) | `Stop` | `PostToolUse` | file: `~/.agents/plugins/conductor/hooks/hooks.json` | — |
| Amp | `amp` | `agent.end` (in-process plugin) | `agent.end` | `tool.call`/`tool.result` | file: `~/.config/amp/plugins/conductor/` | plugin API surface |
| DeepSeek Harness | `dsh` (with the TUI plugin) | plugin events `question-asked`, `permission-requested` | `session completed/failed` | none | file: dsh plugin | developer preview; plugin API changes |
| Shell / anything | any argv | bell / OSC / pattern | exit | none | — | — |

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
- **Crews: long one-line role prompts.** A role prompt is typed as one line,
  its line breaks and tabs made spaces as a handoff's or a broadcast's are,
  with a carriage return at the end, so no agent takes the second line of a
  prompt as a further message. What is left to check is whether each agent TUI
  that matters takes a long single line (a paragraph of a few thousand
  characters) as one message, rather than truncating it or treating a fast
  burst of input as a paste; bracketed paste stays the deferred fix for agents
  that need it.
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
  `permissionOptions()` in `internal/notify/notify.go` to send `"1\r"` etc.

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
- **Example crews.** `conductor serve --examples` (env `CONDUCTOR_EXAMPLES=1`)
  seeds example crews once: ids `example-todo-app`, `example-test-fixer`,
  `example-docs-writer`, `example-dependency-upgrade`; a crew whose id exists
  is left alone. Members use the `claude` and `codex` built-ins with real
  role prompts and start conditions (the todo app: a lead that plans, two
  builders after the lead, a tester after the builders); `cwd` is the
  server's default working directory; isolation `worktree`. The empty Crews
  page offers "Load the examples", which calls `POST /api/crews/examples`
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
- **Git check.** `GET /api/git/check?cwd=<path>` (admin) answers
  `{inRepo, toplevel, hasCommit, message}` with the same rules the launch
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
  line to `~/.zshrc` or `~/.bashrc` (idempotent; refuses a file not owned by
  the user). No new dependency.
- **Fullscreen everywhere.** The document-fullscreen toggle that the wall and
  the carousel have (`useFullscreenToggle`, the `F` key, Alt+F in a terminal)
  moves to the layout: one button in every page header and one shortcut
  registration, so the session page, the crew views, the join page, Events,
  Agents and Crews all toggle it; the wall and carousel keep their buttons.
  `F` is ignored while an input or textarea has focus, as the other letter
  shortcuts already are.
- **Agent availability.** `GET /api/catalog` reports `available` per agent (whether `command[0]` resolves on the server through the same check `POST /api/catalog/check` uses, re-evaluated per request and cached for 30 s) and `site` (the agent's website, a field on built-ins and an optional field on saved agents). The Agents page greys out an agent that is not installed, says "Not installed on <host>" and links to its site; the Launch dialog's server tab lists only available agents and, when none is, says so with a link to the Agents page; the "My machine" tab lists every agent because availability there is the host's. The crew editor's agent select marks unavailable agents the same way and the launch route refuses a crew whose member agent is not installed with `invalid_crew` naming it.
- **Deferred items.** Every open item from the rounds 1 and 2 ledgers is
  closed in the first plan, whatever its kind. A triage on 2026-10-01 found
  five already closed and two obsolete; the 55 open ones are listed below
  with the decision taken where one was needed.

### Open verification (round 3)

- The example crews' role prompts against a live Claude Code and Codex.
- The completion scripts in a real zsh and bash session.

### Deferred items to close (plan 1 of round 3)

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
- When neither `dataDir` nor `CONDUCTOR_DATA_DIR` is set, the data directory is `~/.conductor` (the home of the user running `conductor serve`), never next to the config file or in the working directory. For backward compatibility, when `~/.conductor` holds no server data (`catalog.json`, `crews/` or `crews.json`; a `hooks/` directory alone does not count) and the old default (`<config dir>/conductor.d` or `./conductor.d`) does, the server keeps using the old directory and logs a notice naming both paths and how to move. The host's hooks directory moves to `~/.conductor/hooks` the same way (old `~/.local/state/conductor/hooks` kept when present). The Docker image keeps its explicit `CONDUCTOR_DATA_DIR`. README, the upgrade note and `conductor.example.json` follow.

Crew storage (replaces the single `crews.json` and the 50-crew cap):
- One file per crew at `<dataDir>/crews/<id>.json`, written atomically by the store, which gains `List` and `Delete`; a corrupt file names itself in the startup error and the other crews still load.
- `GET /api/crews?offset=0&limit=100` reads the directory and answers `{crews: [summaries], total}` (limit at most 500); `GET /api/crews/{id}` returns one crew in full for the editor; the Crews page pages through the list.
- No cap on the number of crews; the per-crew size cap stays as a sanity bound on one file and its request body, at 1 MiB.
- An existing `crews.json` is split into per-crew files on the first start and renamed `crews.json.migrated`.

Accepted as documented limitations (not changed): hosted crews stay future work.
