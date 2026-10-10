# Round 15: copy and paste, attributions, invites into the open window, and three checks

Planned 2026-10-10 from the owner's pick of the todo (`docs/tasks-todo.md`):
items 1–5 and 7 of the list as it stood. One pull request each from main,
tests at both ends; the checks are recorded in `docs/features.md` when done.

## 1. Copy and paste, as Windows Terminal has them

- Keys, in a pure `utils/terminalClipboard.ts` with vitest:
  - Ctrl+Shift+C and Ctrl+Insert copy the selection (Ctrl+Shift+C is
    Chrome's element picker otherwise: the page takes it).
  - Ctrl+Shift+V and Shift+Insert paste. They are left to the browser's own
    paste, which reaches xterm's text area and so keeps bracketed paste,
    and works where `navigator.clipboard` cannot read (plain-HTTP origins).
  - Ctrl+C with a selection copies it and clears it, without the interrupt;
    with none it stays the interrupt.
  - On macOS the ⌘ keys stay as they are (xterm and the browser do them).
- Right-click: with a selection it copies and clears it, without one it
  pastes (Windows Terminal). Shift+right-click opens the terminal's menu:
  Copy, Paste, Select all, and "Right-click pastes" to turn the behaviour
  off (then right-click opens the menu). Kept per browser.
- A view-only connection copies and never pastes: no paste key, no paste in
  the menu, right-click only copies.
- Copy writes with `navigator.clipboard.writeText`, falling back to a
  hidden text area and `execCommand('copy')` within the key's gesture.
- Middle-click pastes the X selection on Linux by the browser's own
  behaviour; nothing to build, a check on the by-hand list.
- Tests: vitest for the key and click policy; Playwright with clipboard
  permissions: a word the stub printed, double-clicked and copied with
  Ctrl+Shift+C, read from the clipboard; Ctrl+C on a selection sends no
  interrupt; a clipboard text pasted with Ctrl+Shift+V and with right-click
  reaches the stub; a view link pastes nothing.

## 2. Mouse, checked with the real agents

A by-hand check in the installed app: Codex's TUI (which asks for mouse
reporting) and Claude Code, clicks, drags, the wheel, Shift+drag selecting.
Recorded in `docs/features.md`; a fix only if an agent's mouse mode is wanting.

## 3. Codex's hook trust, how long it lasts

A live check with the real Codex (0.161): where it keeps the trust given to
Conductor's hooks at "Hooks need review", and whether a second launch asks
again. Never an Enter into a startup screen in a probe. The finding goes in
the todo or, when it calls for one, a change to Codex's questions.

## 4. The sidebar, watched in use

The five-task script (find the agent asking you something; stop a run; share
one member; open yesterday's run; tell a crew from a run) written out for the
owner to run with two coworkers, and a first walkthrough of the same tasks
in the installed app, its findings recorded. The watching is the owner's.

## 5. Dependency attributions

`THIRD_PARTY_NOTICES` generated from the Go modules (`go.mod`) and the npm
packages shipped (`web/`, `desktop/`), bundled with the binaries, the
packages and the switchyard's pages, linked from Settings → the app's
License row; a CI check that it is current.

## 7. An invite pushed to the open window

An invite (`conductor://…`) the app receives while its window is open goes
to the page over a bridge event instead of a full page load, once the page
has loaded (a `did-finish-load` gate, or the page's ack); a desktop test.
