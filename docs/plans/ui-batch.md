# The UI batch: seven issues, one PR

The owner, 2026-10-21: plan the UI improvements as multiple issues in a
single PR, without #318 (a collaborator's). This is the plan, in the shape
of the batches before it (streams 1 and 2): one branch, one commit per
issue, `Fixes` each, the owner merges. #304 (a cross-session Touched
index) stays out: it touches a route and gets its own plan when it is
taken up, as its brief says.

## Order, and why

| # | Issue | Why here |
|---|---|---|
| 1 | #314 Collapsed sidebar: the open session told apart, same-agent squares differ | Smallest; sets the selection idiom (a 2 px primary bar on the rail's outer edge) that #301's rail must share. |
| 2 | #301 Inspector rail | The inspector's views become one rail; Touched folds into Activity; the git polling moves into a composable. #309 and #302 build on the result. |
| 3 | #309 The editor and the terminal side by side | "With or after #301": the two must not fight over the page's widths; the rail's 44 px is known by then. |
| 4 | #310 Roundhouse: rearrange the strip, thumbnails | Independent; any time. |
| 5 | #302 The command palette (PR 1) | The largest; after #301 so its Open Files / Chat / Activity / People rows select rail items, and after #314 so the rail's search button retarget lands on the finished rail. |
| 6 | #303 The palette's reach (PR 2) | On #302's plan, as its brief says. |
| 7 | #312 Validate every shortcut on macOS, Linux and Windows | Last: it covers every key the batch adds, the palette's included. |

Branch `issue-314` (the first in order), worktree
`.claude/worktrees/issue-314`. The plan is the first commit; each issue is
one commit with its tests; a Codex round after each issue's commit, on
that issue; two rounds on the whole batch at the end; the three-machine
preflight at the end and after any rebase; one push, then the PR ready.

## What each commit does

### 1. #314 — `utils/sidebar.ts`, `SidebarRailSquare.vue`, `SidebarRail.vue`, `SessionAvatar.vue`

- The current square gets the selection idiom: a 2 px primary bar down
  the rail's edge beside the square (8 px out, in the rail's margin, clear
  of the corner badges), the elevated background it has, `aria-current`
  as today. The state dot (top right) and the news count (bottom right)
  stay where they are, so the mark is another shape in another place.
- Two squares never look the same: a pure `railGlyphs(shapes)` in
  `utils/sidebar.ts` gives each square (exited run members included) its
  two letters: the agent's initials unless another square shares them,
  then the first two letters of the session's folder name
  (`basename(cwd)`, upper case), and then, across every square whatever
  its agent (Codex's round: Claude Code and Codex both in `conductor`
  read `CO`), a one-character ordinal in the square's top left (the free
  corner: the state dot is top right, the news count bottom right, the
  machine or share tile bottom left) by the sidebar's order when the
  letters still match. The tooltip stays the full label. vitest
  for the rule, Playwright: two sessions of one agent, collapse, the open
  one's bar and the two squares' letters differ, light and dark.

### 2. #301 — `InspectorRail.vue`, `utils/inspector.ts`, `useRepoStatus.ts`, `useTouched.ts`, `SessionInspector.vue`, `FileBrowser.vue`, the three pages

- `InspectorRail.vue`: `items[{id, label, icon, count?}]`,
  `v-model:view`, `v-model:open`; 44 px, icon buttons 36×36 with
  `UTooltip side: 'left'` naming the view and its count; neutral pills
  capped at `99+`; the active item elevated with a 2 px primary bar on
  the outer edge (the same idiom as #314); clicking the active item
  collapses the panel; the toggle at the rail's foot replaces the
  header's `panel-right` button; `conductor.inspector` keeps open/closed.
  The pure parts (the item list, the pill text, the tooltip text) in
  `utils/inspector.ts` with vitest.
- `SessionInspector.vue` renders the rail with People, Explorer, Changes,
  Commits, Activity, Chat (Chat when `chat.capable`); each view's head
  row becomes the two-line title (bold name, the mono sub-line the view
  computes, the refresh button kept). The header's Files and Chat buttons
  select rail items with their toggle-if-shown behaviour.
- `FileBrowser.vue` loses its section row and takes `section` as a
  controlled model (`explorer | changes | commits`). Its git polling moves
  into `useRepoStatus(request, cwd, base)` owned by the inspector: 5 s
  (status) / 10 s (log) while that view shows, 30 s otherwise, paused
  while the document is hidden, the per-connection bounds kept. This is
  the one behaviour change beyond layout, named in the PR. The Commits
  count fills from the first poll (today it is empty until Commits is
  opened).
- Touched folds into Activity as the chip row All · Files · Tools:
  `useTouched(request, cwd, activity)` carries `loadTouched` out of
  FileBrowser; Files shows the touched rows with its count on the chip,
  Tools filters to `tool_use` / `tool_denied`; the Touched foot note moves
  under the Files filter.
- The Yard's focused aside and the join page's Files aside use the rail
  with the three Files items and drop their copy of the old row.
- Below `xl` nothing changes. Test hooks `data-rail-item`,
  `data-rail-count`, `data-rail-open`; every spec that clicks
  `data-files-section`, `data-chat-tab` or the header toggle is updated.
  No protocol change. Docs: 4h in `docs/features.md`.
- Out of scope, noted for the owner: below `xl` the header's Files button
  toggles an inspector that is not rendered (a gap on main today).

### 3. #309 — `EditorColumn.vue`, `utils/editorTabs.ts`, `EditorArea.vue`, `main.css`

- A `layout` of `stacked | side` kept per browser under
  `conductor.editor.layout`, the split ratio kept per layout
  (`conductor.editor.split` as today, `conductor.editor.split.side` for
  side by side), both through `readSplit`/`writeSplit`-style pure
  functions with clamping, vitest beside them.
- Side by side is the editor on the left and the terminal on the right;
  the bar between them is draggable either way (`cursor-col-resize`), a
  real `role="separator"` with `aria-orientation`, focusable, the arrow
  keys moving it by 2 % a press. The terminal keeps a minimum width side
  by side (320 px, about forty columns at the default font) as it keeps
  a minimum height stacked; its size reaches the agent as any resize
  does (the sizer rules are untouched: `TerminalView` fits its box).
- The switch is a button in the editor's tab strip
  (`data-editor-layout-switch`, a tooltip naming the other layout), shown
  only at `lg` and up; below `lg` the column is stacked whatever is kept;
  the phone stays as it is. The default stays stacked until the person
  switches (least surprise; the brief allows a width-based default, not
  taken). The column says which layout shows (`data-editor-layout`).
- Built: the terminal's minimum width is the CSS of the column (its
  side holds `min-width`), and the split kept side by side is the one
  that shows once the terminal keeps its width (`editorWidth`), so a
  reload brings back what the person saw, and the arrow keys move from
  there. A switch moves no DOM node of the terminal (the terminal's side
  is `display: contents` stacked), so it is not remounted.
- Codex's round: the column's own width (a ResizeObserver), not the
  viewport's, decides whether both panes fit (`fitsSide`: 128 + 320 px
  and the gap); a column beside wide panels (a join page's Files and
  Chat at 1024 px) stays stacked and offers no switch. The reply bar's
  form shrinks with the terminal's pane.
- Playwright: a drag in each direction, the switch, the split surviving
  a reload, the terminal's minimum width.

### 4. #310 — `pages/roundhouse.vue`, `CarouselStrip.vue`, `utils/carousel.ts`

- No new dependency: the strip's chips are HTML5 `draggable` with
  `dragstart` / `dragover` / `drop`, and by keyboard a chip picked up
  with Space, moved with the arrows, dropped with Space or Enter, Esc
  cancelling (`aria-grabbed`, a live region announcing the move).
- The order is kept with the Roundhouse's settings
  (`conductor.carousel.settings`, a new `order: string[]` of session ids)
  through a pure `orderSessions(active, kept)` in `utils/carousel.ts`: a
  session the order does not know goes at the end, one that ended leaves
  it; stepping, autoplay and "Next up" follow the kept order; follow mode
  still jumps to a waiting session first and goes on in the kept order.
- A button in the strip's corner switches chips and thumbnails
  (`strip: 'chips' | 'thumbs'` kept with the order). A thumbnail is a
  scaled, read-only `TerminalView fit="scale"` (the join grid's view-only
  rendering; `tile` would size the session to the thumbnail) for the
  current slide and its neighbours, which are connected anyway; any
  other session shows the idle mark with its initials and state, never a
  connection: the Roundhouse's cost stays the current slide's and its
  neighbours'. (A "last frame" for a disconnected session would need a
  snapshot kept at disconnect; not built, said here.)
- Built: a chip is a `div[role=button]` (a `button` cannot hold the
  thumbnail's terminal), so Space is the pick-up and drop key without
  the button's own click, Enter selects; a chip's keys stop at the chip
  while it is picked up, so the page's arrows do not step the carousel
  under a move. The order is written back whenever the active list
  changes, the first list included, so what the strip shows is what is
  kept and a session started afterwards goes at the end. The low-window
  terminal-height spec addresses the chips by `[data-strip-chip]`.
- The second whole-batch round: a thumbnail is the slide's own screen as
  text (`TerminalView.snapshot()`, read every second), not a view of its
  own, which doubled the connections and took a viewer slot.
- Codex's round: a thumbnail's subtree is `inert` and a read-only
  `TerminalView` sends nothing on its chords; the picked chip is tracked
  by its session id (keyboard and drag) and let go when its session
  ends, and Escape keeps the focus on it; the drop marker shows the
  boundary in the list as shown, apart from the index the move uses.
- vitest for the kept order and the switch; Playwright for a drag in the
  strip surviving a reload, the keyboard move, the thumbnails toggle.

### 5. #302 — the command palette: `docs/plans/command-palette.md`

Its plan, as the brief asks, is the document beside this one. In short:
`CommandPalette.vue` on `UCommandPalette` inside a `UModal`,
`usePalette()` with `registerPage`, `utils/palette.ts` for the pure parts
(prefix, verbs, ranking, frecency, grouping, match highlight), the
sidebar's actions lifted into shared descriptors that both the sidebar
and the palette render, the keys, every group and prefix of PR 1, the
page groups, the README and `?` modal rows.

### 6. #303 — the palette's reach

`?tab=` / `?view=` / `?filter=`, `useLaunchModal.show({...})` and the
`launch` verb, Type a line (`I`, `say`), Answer all, Stop every session
that is done, Clear the new-event badges, `]` / `[` and the pill, `G S`,
`S` / `X` on the session page. Its section is in the palette plan.

### 7. #312 — `utils/chords.ts`, `useShortcuts.ts`, `ShortcutsModal.vue`, the pages' `defineShortcuts`

- One per-system chord table, `utils/chords.ts`: for each action the
  chord on macOS, Linux and Windows, in the desktop app and in a browser
  tab; the modal and the palette show the chord of the system the page
  runs on (as the sidebar's `meta` / `ctrl` text does). vitest for the
  table.
- Replacements decided now, to be confirmed by the pass: the terminal's
  leave-focus chord becomes `Alt+Q` everywhere (a free Alt letter), with
  `Alt+Esc` kept where the system leaves it; the sidebar's `Meta+B`
  becomes `Ctrl+B` on Linux and Windows in the desktop app and in Chrome
  (Firefox takes it for its bookmarks sidebar: recorded as the exception,
  `Alt+B` inside a terminal unchanged); `Ctrl+Tab`, `Ctrl+Shift+Tab` and
  `Ctrl+W` in the editor are registered by the desktop app only while
  the editor has focus, never taken from a terminal; the terminal's copy
  and paste show `Cmd+C` / `Cmd+V` on macOS.
- The pass itself, recorded as a table in the PR body (chord, system,
  surface, reached the app or not, what took it otherwise): the Linux
  box and the Windows host in the desktop app and Chrome, driven here
  where that is possible; the Mac's browser rows need a hand at the
  machine (see the questions).
- Built (2026-10-22): `utils/chords.ts` as above, with `rowsFor`
  applied in `useShortcutsModal` so the modal and the palette's `?` rows
  show the system's chords, `SYSTEM_NOTES` under the modal's table.
  `Alt+Q` leaves the terminal on the Yard and the Roundhouse; `Alt+Esc`
  stays bound (the system that takes it never delivers it). `meta+B` is
  kept: it is ⌘B on a Mac and Ctrl+B elsewhere already; Win+B and Super+B
  were never ours. The desktop app's menu binds no Ctrl+W/Ctrl+Tab (its
  View menu: reload, zoom, fullscreen; Settings on ⌘,/Ctrl+,), so the
  editor keeps them in the app, as `EditorArea.vue` handles them; a
  browser tab keeps them, said in the notes. Codex's round: Electron's
  `windowMenu` role did bind Close to Ctrl+W on Windows and Linux (⌘W on
  a Mac), so the Window menu is built by hand without Close; Alt+Q is
  handed back by a terminal only on the Yard and the Roundhouse
  (`usePageAltCodes`), a session page's terminal keeping it; the Mac's
  clipboard rows say `command`, which UKbd shows as ⌘ on an iPhone's or
  iPad's user agent too. The table below is the
  pass as run here: Linux Chromium rows are the specs' (every row green
  in the batch's runs); the Windows and macOS rows, in the desktop app
  and in a browser, are NOT RUN in this PR and need the owner's hand at
  those machines (the Mac rows were asked for in the plan's questions).

| Chord | Linux · Chromium (browser) | Windows | macOS |
| --- | --- | --- | --- |
| Ctrl+K / ⌘K outside a terminal → palette | reached (palette-keys.spec) | not run | not run |
| Ctrl+K inside a terminal → the shell's | reached the shell, palette closed (palette-keys.spec) | not run | not run (⌘K from a terminal: the layout listener, untested here) |
| Alt+/ from a terminal → palette | reached (palette-keys.spec) | not run | not run |
| / outside a terminal → palette | reached (palette.spec) | not run | not run |
| Alt+S from a terminal → palette | bound as before (sidebar's chord); not asserted | not run | not run |
| Ctrl+B / ⌘B outside, Alt+B inside → sidebar | reached (chords.spec) | not run (Firefox: its bookmarks sidebar, noted) | not run |
| G then Y/R/C/A/E, Alt+letters → pages | reached (shortcuts.spec) | not run | not run (Option+letter typed glyphs are taken first by the page) |
| ] and [ outside, Alt+] / Alt+[ inside → the pill | ] [ reached (palette-reach.spec); Alt twins not asserted | not run | not run |
| S and X on a session page, Alt+X inside | reached (palette-reach.spec) | not run | not run |
| Alt+Q from a terminal → leave the terminal (Yard, Roundhouse) | reached (chords.spec) | not run (Alt+Esc known taken by Windows) | not run |
| Alt+Esc from a terminal → leave the terminal | reached (chords.spec) | taken by the system (not delivered) | not run |
| Ctrl+Tab, Ctrl+Shift+Tab, Ctrl+W in the editor | the browser's tabs (reserved by Chromium); the editor's strip stands in | not run (the app's Window menu bound Close to Ctrl+W through Electron's `windowMenu` role; Codex's round replaced the role with Minimize and Zoom, so the editor keeps it there: to be checked in the app) | not run (⌘W: the same) |
| Ctrl+Shift+C / V in a terminal; ⌘C / ⌘V on a Mac | reached (TerminalView's own handler; terminal specs) | not run | not run |
| F outside a terminal, Alt+F inside → fullscreen | reached (shortcuts.spec) | not run | not run |
| G S / Alt+, → Settings (desktop app only) | n/a in a browser | not run | not run |

## Checks

Per commit: `npm --prefix web run typecheck`, `npm --prefix web test`,
`make web-build build-go`, the Playwright specs the commit touches or
adds. For the batch: `make lint`, the whole Playwright suite, the
three-machine preflight at the batch's head; every receipt named in the
PR. No Go change is expected; a Go change that turns out to be needed
stops and asks, as #302's brief says.

## Decisions taken here (the briefs left them to the plan)

- #314's rule for squares that would match: folder letters, then an
  ordinal (above).
- #309: editor left, terminal right; the switch in the tab strip; the
  default stays stacked.
- #310: HTML5 drag, no library; thumbnails only for the connected slides.
- #312: `Alt+Q` for leaving a terminal's focus; `Ctrl+B` for the sidebar
  off macOS.
- The palette's decisions are in its plan.

## Questions for the owner

1. The "Conductor Command Palette" canvas link is still the placeholder
   in #302. The palette is built from the brief's behaviour and the brand
   either way; the canvas gives the look. Send it when you have it.
2. #312's by-hand rows on the Mac (Chrome, Firefox, Safari and the
   desktop app): I can drive the Linux box and the Windows host from here,
   and run commands on the Mac over SSH, but not its screen. Those rows
   can be yours, or the pass can note them as not run.
3. Codex rounds: one per issue commit plus two on the whole batch is nine
   in all, past the standing stop of five per PR. Say which you want.
4. #291 (clone an agent with its configuration) was next in stream 3; it
   waits behind this batch unless you say otherwise.

## Whole-batch Codex rounds

Round 1 (seven findings, all taken): no key of the palette reaches the
page's handlers (its root stops every keydown but Tab; Escape in the
input closes it itself), so a digit on a row without a prompt answers
nothing on the page behind; a tab closed from the palette goes through
the editor area's own close and its unsaved-changes guard
(`EditorArea.requestClose`, exposed through `EditorColumn`); the reply
and answer verbs are bound to the question their row first showed in
the open and say so when it moved on; the Alt codes a page takes are
counted, since Nuxt sets the next page up before the last is unmounted;
`say` reads an error frame on the open connection as a refusal and
never sends a line past the owner's 4096 bytes; Open Files / Chat /
Activity / People open the page's own inspector on its session and
`?tab=` elsewhere; the Roundhouse's waiting list follows the strip's
order and follow mode cycles from the current slide.

Round 2 (four findings, all taken): `share <run|name> control|view` carries
the role into the Share dialog, which makes this opening's link with it
and reuses a held link only of that role; the reply and answer verbs
are bound by the session alone, whatever the line becomes as it is
typed; a line typed into a session is reported as sent, not as typed
(the owner's `submit` has no answer: a `submit_ok` would be the
owner's to add, a protocol change not made here), a refusal within the
moment still shown; the Roundhouse's thumbnails are the slides' own
screens as text, no connection of their own.
