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
  a "next up" line, and a "jumped here" note when follow mode moved.
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
- **Runs (orchestration).** Mockup screen 1c. A run is a spec
  (`conductor.run.yaml`) of stages: plan, parallel build steps in per-step
  worktrees, verify, and a human merge gate with named approvers. Every step
  is an ordinary session that can be opened, shared or taken over. The run
  page shows the stage graph, a Spec / Events / Artifacts inspector, and
  `on_needs_input: queue` routes step prompts into the wall queue. Presence
  and the activity log above are prerequisites for gates and approvers.
