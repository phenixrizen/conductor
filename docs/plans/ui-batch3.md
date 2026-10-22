# The third UI batch: five issues from the owner's rc.14 screens, one PR (2026-10-22)

The owner's word: the five new issues as one stream of work, started now.
One branch (`issue-328`), one commit per issue with `Fixes`, a Codex
round after each and two on the whole, the three-machine preflight, then
the PR for the owner to merge, then a release candidate. The order puts
the small bar and row changes first, the two design passes next, and the
palette sweep last, so it tests the final bar, rows and tab menu.

## 1. #329: the Files and Chat buttons off the session page's bar at `xl`

The inspector rail (#301) has Explorer and Chat with the chat's unread
count on the rail; at `xl` the bar's **Files** and **Chat** say the same
twice. Both buttons get `xl:hidden`; below `xl`, where the rail is not on
the page, they stay as they are (the phone's chat sheet opens from the
header, `chat.spec.ts`). The `?tab=` query, the palette's rows and the
chords are unchanged. `inspector-rail.spec.ts` asserts the bar has
neither button at 1600 px; `chat.spec.ts`'s phone case stands. The
README's session page paragraph and `docs/features.md`.

## 2. #331: an exited row's arrow and its `…` line up

`SidebarSessionRow.vue` puts the resume/relaunch arrow inline at the
row's edge (`ResumeButton`, `relative z-10 mr-1.5`) and the `…` in the
hover bar (`[data-row-actions]`, `absolute top-1.5`, its own padding,
ring and background): different heights and insets. For an exited row
the two sit in one trailing group inside the row's first line, the same
size (`xs`), the same centre and right inset, the `…` fading in on hover
with its width reserved so nothing shifts; the arrow keeps showing
always. Running rows keep their hover bar (Share, Stop, More). A spec in
`sidebar-actions.spec.ts`: an exited row hovered and not, the arrow's
and the `…`'s boxes sharing a vertical centre within a pixel and the
same right edge as the other exited row's arrow.

## 3. #328: the Keyboard shortcuts modal, a design pass

`ShortcutsModal.vue` (#312's three columns). What changes, in Nuxt UI's
grammar (`docs/design/brand.md`):

- Width with the window: `sm:max-w-3xl` becomes `sm:max-w-3xl xl:max-w-5xl`,
  the key columns `12rem` each at `xl`; the body scrolls only when the
  rows do not fit (the dialog's own body already scrolls).
- The description shortened to one line that never runs under the close
  button (the header keeps `pe-14`, the description gets it too).
- The key cells lose their tints (`bg-elevated/60`, `/30`): plain caps on
  the row, both columns laid out the same way (centered in cells of equal
  width), every cap `size="md"`, modifiers and letters alike.
- A column with no chord shows nothing (an empty cell with `aria-label`
  "none"), not a dot.
- The column names once, in the header row, sticky over the scroll; the
  per-group repeats go.
- Enter shows as the word (`UKbd value="enter"` draws `↵`; a `label`
  shows "Enter"); "then" between a chord's caps at the caps' text size.

`chords.spec.ts` opens the modal already: it asserts at 1600 px that the
terminal cells of a group share one left edge and the outside cells one
right edge, no cell holds a lone dot, and the column names appear once.

## 4. #330: close tabs to the left, to the right, others, all

`utils/editorTabs.ts` gains pure helpers with vitest: `tabsToClose(state,
id, 'others' | 'right' | 'left' | 'all')` returning the ids in strip
order, and the active tab moving to a neighbour as VS Code does when the
active one goes (`closeTab` does this for one). `EditorArea.vue` gets
`requestCloseIds(ids)` (today's `requestCloseAll` generalised: unsaved
among them ask through `closing.ids`, else close now), a `UContextMenu`
on each tab (right-click; Shift+F10 on the focused tab) with Close, Close
others, Close to the right, Close to the left, Close all, and the same
five as → actions on the palette's tab rows beside the Close there. The
Yard's and the join page's editors get it with the area. `tabs.spec.ts`:
three files open, the menu on the middle one, Close to the right leaves
two with the middle active; Close others leaves one; Close all with one
unsaved file asks and Don't save closes them all. `docs/features.md`;
the README's editor paragraph in one clause.

## 5. #332: every palette row does what it says

First the defect: on the session page `showView('chat')` hands to
`showChat()`, the header button's toggle, so **Open Chat** from the
palette (and a session row's Open Chat on this page) collapses an open
chat. `showView` shows: the inspector on the view, or the sheet below
`xl`, and nothing when it is shown already; the header buttons keep
their toggles. `showFiles` is the header's alone.

Then the sweep, one spec per page group in `palette-rows.spec.ts` (the
existing `palette*.spec.ts` keep what they cover; the sweep adds what
they do not), each row run from Enter and from a click with its visible
effect asserted:

- The session page: Share (the dialog), Copy a view link (the clipboard),
  Stop (asks, then the status), the three marks (the attention from the
  API), Fit (when shown), Fold (the area folded, then back), the views
  (the inspector on each, the chat sheet below `xl`), the tab rows (the
  tab activated) and #330's close actions, #321's editor rows.
- The run page: the broadcast field, the view rows, the chat, the member
  rows.
- The Yard: the chips (the filter), Expand (the focused tile), Back to
  the grid. The Roundhouse: the rotation rows. Crews: Launch, Load the
  examples (once). Events: the hook rows exist and are disabled when
  installed (no install is run). Settings: Save asks before a restart.
- The session rows' → actions: Share, Copy a view link, Copy path, Copy
  branch, Copy the agent's session id, Open Files/Chat/Activity/People
  (that page on the view, this page's own inspector), Type a line,
  Resume/Relaunch on an exited row, Stop.
- The verbs, one each: `stop`, `share … view`, `answer … 2`, `reply …`,
  `broadcast …`, `run … as …`, `resume`, `open path:line`, `mark …
  working`, `go yard`, `say …`, `launch claude in <dir>`.
- The switches: the sidebar's folds, the colour mode (the `html` class),
  fullscreen (the toggle's own state; the browser's fullscreen is not
  Playwright's to see).
- The prefixes `>` `#` `:` `@` `!` `?` each show their groups.

A row that cannot act is disabled with its words, never acting the other
way; a row named for showing something never hides it. What the sweep
finds is fixed in the same commit, each fix named in the PR body.

## Checks and docs

Per commit: `make web-typecheck`, `npm --prefix web test`, `npm --prefix
web run typecheck:e2e`, `make web-build build-go` (the e2e server embeds
the web build), the specs the commit touches. For the batch: the
three-machine preflight at the head. `docs/features.md` gets an entry
per issue; the README where it describes the bar, the editor and the
modal.
