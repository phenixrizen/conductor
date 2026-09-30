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
  `CONDUCTOR_DATA_DIR`, default `conductor.d` next to the config file) holds
  `catalog.json` (agents added or hidden from the UI), `crews.json`, generated
  hook assets under `hooks/` and the Conductor skill. The config file stays
  read-only; the data directory overlays it.
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
- **Crews: multi-line role prompts.** A role prompt is typed with its line
  breaks as written, and a carriage return at the end; a handoff or a broadcast
  is made one line first, a prompt is not. An agent TUI may submit at the first
  newline and take the rest as further input. Try a three-line prompt in each
  agent that matters; bracketed paste is the deferred fix.
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
