# The command palette (#302, PR 1) and its reach (#303, PR 2)

The owner's brief is #302, filed as given; this is the plan it asks for
first, in plain terms, with the decisions the brief leaves to it written
down. Where the brief and this plan differ, the brief wins; where the
canvas "Conductor Command Palette" and the brief differ, the brief wins.
The canvas link is still to come from the owner: the palette is built from
the brief's behaviour and the brand (`docs/design/brand.md`); the canvas
gives the look once it is here.

Both PRs are commits of the UI batch (`docs/plans/ui-batch.md`), built
after the inspector rail (#301), so the palette's Open Files / Chat /
Activity / People rows select rail items.

## What exists to build on (read on 2026-10-21, main at 3495fb7)

- `@nuxt/ui` 4.11.2 ships `UCommandPalette` (`fuse.js` 7.5 and
  `@vueuse/integrations`' `useFuse` behind it; items take `kbds`,
  `children`, `onSelect`, `placeholder`; a group takes `ignoreFilter`)
  and `UDashboardSearch` (a modal around it, default shortcut `meta_k`).
  Nothing in the app uses either yet. The palette uses `UCommandPalette`
  inside a `UModal` of its own (680 px, top-anchored), not
  `UDashboardSearch`: the brief's keys, groups and prefixes need the
  input, the list focus and the row actions under the app's control.
- The sidebar's actions: `utils/sidebarActions.ts` (`rowMenuItems`,
  `stopQuestion`, `rowPrompt`, `sessionLive`, `accountItems`), already
  Nuxt UI menu items with `label`, `icon`, `kbds`, `onSelect`; the list's
  keys in `SessionSidebar.vue` (`onKey`: J K arrows ↵ R S X Esc, digits
  1–9 through `choiceKey`); `useQuickReply` for choices and lines.
- The shortcuts: `useShortcuts.ts` (`GLOBAL_SHORTCUTS` and the other
  groups the `?` modal lists, `ALT_PASSTHROUGH_CODES`,
  `useShortcutsModal().registerPage`), `layouts/default.vue`'s
  `defineShortcuts` (`meta_b`, `?`, `n`, `/`, `g-…`, the Alt twins with
  `usingInput: true`), `TerminalView.vue` handing the Alt chords back to
  the page.
- The stores the groups read: `useAttention` (sessions, `needsInput`,
  runs), `useEvents` (the last 500 events, marks, routes),
  `useChatUnread`, `useJoined`, `useEditorTabs`, `useDesktop`,
  `useDesktopUpdate`; `useSessions` for the API; `utils/sessions.ts`
  (`filterSessions`, `sessionMeta`, `agentInitials`); `utils/attention.ts`
  (`needingInput`, `waitsForTerminal`, `answersInTerminal`);
  `utils/events.ts` (`linkableUrl`).

## The pieces

- `web/app/utils/palette.ts`, pure, vitest beside it:
  `parsePrefix(query)` (`>` `#` `:` `@` `!` `?`, the rest a search),
  `parseVerb(query, names)` (the twelve verbs; a verb only when its first
  word is one and its second names exactly one session, run or crew by
  the filter's match; a name that matches one of each kind at once is a
  search unless the verb takes one kind), `rank(groups, query, page,
  recent)` (Needs you first, the page's group next, then the rows that
  match with the match highlighted, frecency within a group, then the
  sidebar's order), `frecency` over `localStorage`
  `conductor.palette.recent` (row key → count and last use, at most 200
  entries, every read and write in try/catch, a corrupt value ignored),
  `groupRows(...)` building each group from the stores' values.
- `web/app/utils/actions.ts`: the one action system. An `Action` is
  `{ id, label, icon?, kbds?, kind: 'session' | 'run' | 'shared' |
  'account' | 'page' | 'global', target?, ask?: string, run() }`.
  `sidebarActions.ts` keeps its exported names (`rowMenuItems`,
  `accountItems`, `stopQuestion`, `rowPrompt`, `sessionLive`) as views
  over the descriptors, so the sidebar's rows change nothing in
  behaviour and the palette renders the same descriptors; a descriptor
  with `ask` asks in the row before it runs (Stop, Restart server).
- `web/app/composables/usePalette.ts`: `open`, `close`, `toggle`, the
  query and mode, `registerPage(group)` (registered in a page's setup,
  unregistered on unmount, as `useShortcutsModal().registerPage` does),
  the crews and catalog lists loaded after the first paint and cached for
  the session (the catalog as the Launch dialog keeps it; the crews
  refreshed on a `run` event).
- `web/app/components/CommandPalette.vue`: the modal, the input with its
  mode chip, the groups, the rows in the sidebar's grammar (agent initials
  chip with the status dot, name, `agent · where · how long`, the prompt's
  message on the second line; the choices as numbered buttons in a
  Needs-you row; a reply field for a free-text question), the row actions
  revealed by `→` and folded by `←`, the keys of principle 3. Mounted in
  `layouts/default.vue`.
- `web/app/components/NeedsYouPill.vue` (PR 2): the pill `]` / `[` show
  for four seconds at the top of the viewport.

## Opening it, and the keys (PR 1)

- `meta_k` in the layout's `defineShortcuts` (⌘K on macOS, Ctrl+K
  elsewhere), outside a terminal; `alt_/` with `usingInput: true` inside
  one, `Slash` added to `ALT_PASSTHROUGH_CODES`. `Ctrl+K` inside a
  terminal keeps reaching the shell: a Playwright assertion types it into
  the stub and checks the palette stayed closed and the terminal got the
  key.
- ⌘K inside a terminal on macOS: `TerminalView.vue`'s key handler lets
  `isMac && e.metaKey && !e.ctrlKey && !e.altKey && e.code === 'KeyK'`
  through, beside the Alt chords; checked in Chromium that the Linux
  `Ctrl+K` path is untouched.
- `/`, `Alt+S` and the rail's search button open the palette; the
  sidebar's filter box stays a pure list filter, ↓ from it still enters
  the list. The README's sentences about `/` change.
- `⌘P` opens in `:` mode and `⌘⇧P` in `>` mode only if `defineShortcuts`
  takes them without cost; no Alt twins (`Alt+P` is the Roundhouse's
  pause, and the editor's preview).
- Inside the palette: `↑ ↓`; `↓` from the input enters the list; in the
  list `J K ↵ 1–9 R S ⇧S X I → ← ⌘↵ ⇧↵ ⌫ Esc` as the brief lists them;
  digits answer only while the list has focus; Esc from the list returns
  to the input, a second Esc closes.
- New global keys in `GLOBAL_SHORTCUTS` so the `?` modal lists them: PR 2's
  `]` / `[` (`Alt+]` / `Alt+[`, `BracketRight` and `BracketLeft` added),
  `G S` Settings in the desktop app (`Alt+,`), `S` / `X` on a session page
  (`Alt+X` in its terminal). The per-system chords go through #312's table.

## Groups with an empty query (PR 1)

Needs you (oldest question first, the choices inline, a reply field for a
free-text question, the held and host-away states as the sidebar shows
them; ↵ with nothing typed opens the first) · Running · Exited (folded,
Resume or Relaunch by `agentSession.resumable`) · Runs (the sidebar's run
headers) · Shared with you (Open, Leave, Forget) · Artifacts (`artifact`
entries with a `linkableUrl`) · Go to (Yard with the needs-you count,
Roundhouse, Crews, Agents, Events, Events · Routing, Events ·
Integrations, Settings on desktop, Paste invite, each with its chord as a
kbd hint) · Actions (the rest of `>`).

A row's actions (`→`): `rowMenuItems` for its kind, plus Copy a view link
now (`createLink({role: 'view'})` then copy; the toast with the
`rendezvous` note the Share dialog gives), Copy path, Copy branch, Copy
the agent's session id, Open Files / Chat / Activity / People (the rail
item; PR 2 gives the page `?tab=`), Type a line into it (`I`, PR 2),
Launch another like this (PR 2).

## Prefixes (PR 1 unless marked)

- `>` actions: the page's group first, then every action: Launch an agent
  (`N`); Browser notifications and Chime as switches; the routing switches
  of `needs_input`; Install hooks for each adapter not installed; Copy a
  view link to <session>; Copy the `conductor host` command; Make a paste
  invite and Answer a paste invite; Revoke link <label> on <session>; in
  the desktop app Install update <version> when one is ready, Open in
  browser, Server log, Restart server (asks, naming how many sessions
  end), Settings; Your name, Toggle theme, Workbench token, Forget token,
  Keyboard shortcuts; Collapse or expand the sidebar, Fullscreen, Fold or
  unfold each sidebar section. PR 2: Launch <agent> here, Launch another
  like <session>, Stop every session that is done, Clear the new-event
  badges.
- `#` runs and crews: live runs with Open, Broadcast… (the line inline,
  "Send to N" and who is skipped), Add agent…, Share run, Stop run…;
  ended runs with Resume as new run; members waiting for Start now; the
  saved crews with Launch run on ↵ and, under `→`, Launch named… (an
  inline name field, 60 characters), Edit, Duplicate, the crew's runs;
  then New crew and Load the example crews. A crew that runs on another
  machine says so and does not launch.
- `:` files: `path` or `path:line` opens the file in the session that has
  it, as the Files box does; then the page's open tabs (↵ switches; `→`
  closes, copies the path, opens raw), Touched lately across all sessions
  from the feed's `file` entries (each with its session, tool, time and
  the git mark when the page knows it), this session's Changes (↵ opens
  the diff) and Commits, then Artifacts. The cross-session index is #304.
- `@` people: the viewers on this page's session and its links (Revoke);
  Your name….
- `!` events: the feed, newest first, with the one thing to do that the
  Events page offers (Answer, Open run, Open).
- `?` the shortcuts: `useShortcutsModal().groups` as rows, read-only.

## Verbs (PR 1; `say` and `launch … in` PR 2)

`stop`, `share [view|control]`, `answer <n>`, `reply <text>`, `say
<text>` (PR 2), `broadcast <run> <text>`, `launch <agent> [in <dir>]`
(PR 2), `run <crew> [as <name>]`, `resume`, `open <path>[:line]`, `mark
waiting|working|clear`, `go <page>`. A verb is a row like any other,
nothing runs before ↵, the destructive ones ask in the row. The twelve
and no more.

## The page's own group (PR 1)

`usePalette().registerPage(group)`: the first group after Needs you and
the first under `>`.

- Session page: Share… (`S`) with Copy a view link now (`⇧S`), Stop…
  (`X`), Mark as needs input / working / Clear signal, Fit to my window
  (`takeSize`, exposed on the terminal ref as `focus` is, only when the
  sizer chip would show), Fold the editor (`T`), Open Files / Chat /
  Activity / People, the open tabs. **Decision:** the editor's own rows
  (Keys → Neovim, Save, Close file, Ask the agent about the selection)
  are left for a later issue: `EditorArea.vue` exposes none of them on
  its ref today and giving it a public surface for four actions is a
  restructuring the brief says not to do here.
- Run page: Broadcast… (inline, "Send to N", the skipped named), Add an
  agent…, View Grid / Graph / Timeline (PR 2 adds `?view=`), Share run
  (`S`), Open the run's chat with its unread count, Stop run… (`X`) or
  Resume as new run, the members as rows with the one asking first, Start
  now for a by-hand member, Resume for an exited one.
- Yard: All / Needs you / Running, Expand the focused tile, Back to the
  grid. Roundhouse: Pause or resume rotation, Follow routed events, the
  interval. Crews: New crew, Load the examples, Launch <crew>. Events:
  Feed / Routing / Integrations, Refresh, Install hooks for <adapter>.
  Agents: Add agent, Edit <agent>, Launch <agent>. Settings: Save, Restart
  server, Open in browser, Server log.

## PR 2 (#303)

- `?tab=files|chat|activity|people` on the session page (selecting the
  rail item), `?view=` on the run page, `?filter=` on the Yard.
- `useLaunchModal.show({agentId, cwd, args, yolo})`; Launch <agent> here,
  Launch another like this, the `launch` verb.
- Type a line into <session> (`I`, `say`) over `submit`, the same
  connection discipline as `useQuickReply` (open, send, close); refused
  with the broadcast's words when the session waits on a prompt.
- Answer all: the same question (message and choices) asked by several
  live sessions, one `send` each, the toast counting them.
- Stop every session that is done (asks with their names); Clear the
  new-event badges (`clearMark` each).
- `]` / `[` and the pill: a cursor through `needingInput` in its order,
  from the page's session when it is one; the pill at the top of the
  viewport for four seconds with the agent chip, the name, the question,
  "n of m" and the choices as numbered buttons; `1–9` answer over
  `useQuickReply`, `↵` opens, `Esc` dismisses; shown only when no terminal
  has focus, or from the Alt twins, which focus the pill; a component of
  its own, not a toast.
- `G S` Settings (desktop, `Alt+,`); `S` and `X` on a session page.

## Tests

- vitest: `utils/palette.test.ts` (`parsePrefix`; `parseVerb` with an
  ambiguous name a search, a name matching one session, run and crew at
  once a search unless the verb takes one kind; ranking: Needs you before
  everything, the page group next, frecency within a group, a corrupt
  `localStorage` value ignored; grouping with runs; match highlighting),
  `utils/actions.test.ts` (the sidebar's menu items come out of the
  descriptors unchanged), `useShortcuts.test.ts` for the new rows and
  codes.
- Playwright, `e2e/palette.spec.ts` and `e2e/palette-keys.spec.ts` with
  the stub agent: ⌘K / Ctrl+K opens and Esc closes; `Alt+/` opens from a
  focused terminal and the terminal did not get the key; `Ctrl+K` in a
  focused terminal reaches the shell and the palette stays closed; a
  session in `needs_input` (as `answered.spec.ts` gets it there) is the
  first row and `↓ 1` answers it, the row acknowledging who answered;
  `> stop <name>` asks, Esc cancels, ↵ stops; `#` launches a saved crew
  and Launch named passes the label; `:path:line` opens the editor at the
  line; `/` opens the palette and the sidebar's filter box still filters;
  the page group is first on a session page and absent on the Crews
  page. PR 2: `]` shows the pill and `1` answers without navigating;
  `?tab=chat` opens the chat.

## Docs

README "Sidebar and keyboard shortcuts": a paragraph on the palette in
the README's voice (the keys, the prefixes, the verbs, what `/` now
does); `docs/features.md` entries for PR 1 and PR 2; nothing in
`docs/protocol.md`; `docs/repository-map.md` unchanged (no new
directory).

## Not in scope, and the decisions already made

As the brief lists them: no renaming or kicking, no Yard tile resizing,
no crew editing from the palette, no auto-answering, no Dispatcher or
summaries, no new dependency. The palette replaces the sidebar filter as
what `/` opens; `I` is "type a line"; 680 px, top-anchored, dark default;
digits answer only while the list (or the pill) has focus; the crews and
catalog lists load after the first paint and are cached; nothing tagged
"new route" is built here.

## Ask before you act

Nothing in this plan touches `internal/`, a route, `docs/protocol.md`, a
dependency, the Windows and WSL rule, or how `useQuickReply` closes its
connection. A key that collides with a plain key the terminal needs is
#312's pass to catch; the only new plain keys outside a terminal are
`]`, `[`, `G S`, `S` and `X` on a session page.

## Built (2026-10-21), and the decisions taken on the way

- **Not `UCommandPalette`.** It keeps the focus in its input and moves a
  highlight; the list never has the focus, so principle 3 (digits answer
  only while a row has the focus) could not hold on it. The palette is a
  `UModal` with its own input, groups and rows (`role="listbox"`,
  rows `role="option"` with `tabindex="-1"` that take the real focus), on
  Nuxt UI's input, kbd, badge and button. Nothing else in the brief
  needed the component.
- The one action system is `utils/actions.ts`; `sidebarActions.ts` keeps
  its names as views over it; the palette renders the same descriptors
  under `→` and runs them by their `kbds`. A row's own `ask` and a
  descriptor's `ask` both ask in the row; `restart` names how many
  sessions end.
- A row that takes a line has a `field`: Enter opens it on the broadcast
  row (`fieldOnEnter`), the action `field` opens it on a crew row (Enter
  launches; "Launch named…" is under `→`, key `L`).
- `:path` and the `open` verb open in the page's editor through the
  `openFile` the page registers with its group (`useEditorTabs` is one
  per page, not shared: the session page's and the Yard's focus), the
  typed path resolved against the page's `cwd` as the Files box does;
  elsewhere the row is disabled and says "Open a session page first". The feed's touched rows of another
  session open that session's page; #303's `?open=`/`?tab=` can carry
  the file. Open Files / Chat / Activity / People from another page open
  the session page alone for the same reason.
- Shared with you: Open and Forget. There is no Leave: a joined link is
  forgotten, not left.
- `⌘P` / `⌘⇧P` are not taken: Ctrl+P is the browser's print on Linux and
  Windows, so the alias would cost what the brief said it must not.
- The Install hooks rows need the integrations list, which no store
  holds: it is read when the palette opens (one request, cached for the
  open), beside the two lists the brief allows. Said here; the brief's
  principle 1 is bent for those rows alone.
- The crews list is read again when the palette opens and it is a minute
  old, or after a crew is duplicated or the examples loaded from the
  palette; the `run` event the brief names is the `RunInfo` the attention
  store already carries, so a run started elsewhere shows under `#` at
  once (Runs reads the store), and a crew saved on another page shows
  within the minute.
- `I` (type a line) is #303's; the key does nothing in PR 1.
- The Exited group folds to one row with the count; Enter on it unfolds
  the exited sessions for the open; a query lists them straight away.
- The `?` modal lists the palette's keys as `PALETTE_SHORTCUTS`; the
  sidebar's `/` row says what `/` does now.
- The verb row under `>` reads as under no prefix (`> stop docs`), so the
  brief's test passes as written.
- Codex's round: a reply drafted in a Needs-you row is bound to the
  question it was begun for (`bindDraft`), and drafts go with the close;
  the Settings page's Save asks first when it restarts the server, naming
  how many sessions end; a button inside a row (a choice, an action,
  Confirm, Send) keeps its own Enter and Space; a click back into the
  input leaves no row focused, and a row re-rendered under the focus gets
  it back only while the focus was in the list; ⌘K on a Mac goes through
  the layout's document listener (defineShortcuts pauses a plain chord
  while xterm's text area has the focus), `meta_k` registered off the Mac
  alone, so Ctrl+K in a terminal stays the shell's; the Exited expander
  keeps the palette open (`keepOpen`); sessions are partitioned by their
  own state whatever section their run sits in.

## PR 2 built (2026-10-22), and its decisions

- `?tab=`, `?view=` and `?filter=` are read, never written back: a link
  names a view; what the person clicks afterwards stays out of the URL.
  `?tab=files` means the Explorer (`changes` and `commits` are taken
  too); below `xl` `?tab=chat` opens the chat sheet.
- `]` and `[`: `useNeedsCursor` (one cursor, in `useState`) and
  `NeedsYouPill.vue` mounted in the default layout; the pill's keys are a
  document listener (capture) that leaves a text field or a terminal
  alone unless the pill itself has the focus, which the Alt twins give
  it; the pill goes with its question (answered elsewhere too) and on
  its own after four seconds. `BracketRight`, `BracketLeft` and `Comma`
  go through the layout's chord listener (defineShortcuts names none).
- Type a line: `useQuickReply().say` over `submit` on a short control
  connection, closed 400 ms after the line is written (the owner's Enter
  follows at 250 ms and goes on without the client); the palette refuses
  a session waiting on a prompt, or not running, with the broadcast's
  words before connecting. `I` opens the session row's field; the `say`
  verb reads as a row.
- Answer all: `sameQuestions` groups by message and choices across live
  sessions (a free-text question is never grouped); the row sits first
  in Needs you with the choices as its buttons; each choice sends to
  every session, one `send` each naming that session's question id.
- Launch <agent> here needs a page with a directory (a session page, the
  Yard's focus, the Roundhouse); Launch another like this takes the
  session's agent, directory, yolo and the arguments after the agent's
  own command (none when the command does not start with it).
- `X` on a session page opens the palette on its Stop row asking
  (`usePalette().askRow`), so the question is asked once, in one place;
  `S` opens the Share dialog; both only while the session runs. The
  session page's `?` group is `SESSION_SHORTCUTS` (S, X, then the
  editor's rows).
- `G S` and `Alt+,` do nothing outside the desktop app (Settings is its
  page).
- Stop every session that is done takes `attention.state === 'done'` on
  a running session; Clear the new-event badges keeps the palette open.
- Codex's round on PR 2: Launch another like this clones the agent, the
  directory and yolo, never the arguments (a session's command carries
  the flags a launch generates, not what was typed); the pill is bound
  to the question it showed and goes when the session asks another, its
  keys stand down while the palette, a dialog, a list, a button, a field
  or a terminal has the focus (a button keeps its Enter), and the focus
  the Alt twins took goes back when the pill goes; `say` looks at the
  page's latest word once the connection is up and reads the close for
  a refusal (the owner's `submit` has no guard of its own: a protocol
  change, not made here, named for the owner); the launch prefill is
  applied once per opening and an earlier opening's catalog read sets
  nothing; Alt+Shift+anything stays the terminal's; the S that completes
  G S is the chord's, not the session page's.
- The whole-batch rounds: no key of the palette reaches the page's
  handlers (its root stops every keydown but Tab); a tab closed from the
  palette goes through the editor's own close; the reply and answer
  verbs are bound to the question first shown, by the session alone; a
  line typed into a session is reported as sent, not as typed, since
  nothing confirms it landed (a `submit_ok` would be the owner's to add);
  `share … control|view` carries the role into the Share dialog; Open
  Files / Chat / Activity / People open the page's own inspector on its
  session and `?tab=` elsewhere.
