# Round 13 plan: the five Files items round 12 left undone

Asked on 2026-10-09; decided the same day.

**Status (2026-10-09): built.** G0 (#68), G1 (#69), G2a (#70), G2b (#71),
G2c (#72), G3 (#73), G4 (#74) and G5 merged or in review the same day.
Left open: Goose's files (its real CLI is not on the owner's machine),
whether Codex's hook trust lasts past a session, the Pebble test's port
race, and the by-hand checks (a real input method and dead keys in the
Windows app, typing over the switchyard from the LAN box and a phone). Round 12 (`docs/round12-plan.md`) closed with five
items written into `docs/tasks-todo.md` instead of built. This round builds
them. Each layer is its own PR from main, merged before the next; never a
stack. Each ships with a Go test, a vitest test and a Playwright spec.

## G0. A program that only shares an agent's name is not offered as that agent

**Found while planning (2026-10-09).** `goose` on this machine is the Go
database migration tool (`~/go/bin/goose`, "goose version:v3.5.3"), not
Block's agent. The identity probe already names it an impostor, but the
catalog still reports the agent `available`, so the Launch dialog offers
Goose and the Agents page shows it as installed with a red "Not Goose"
badge. The owner: Conductor should not surface it as an agent at all.

**Build.**
- The catalog routes report `available: false` for an agent whose identity
  is misidentified (a known impostor, or a verified probe that matched
  nothing), with `notAgent` holding what the program said. A pending probe
  leaves `available` as the lookup says, and the next listing corrects it.
- The Launch dialog then leaves it out (it offers only available agents);
  the crew editor marks it as it marks an uninstalled agent; the Agents page
  shows it greyed as "Not installed", the tooltip saying which program is
  there instead ("goose on this machine is another program: it printed
  goose version:v3.5.3").
- `POST /api/sessions` refuses to launch it (`not_the_agent`), as a crew run
  already refuses it.

**Tests.** Go: the catalog list and one agent with an impostor probe say not
available; the launch is refused. Vitest: the Agents page words for an
impostor. Playwright: an impostor stub (`STUB_IDENTITY=impostor`) is absent
from the Launch dialog and greyed on the Agents page.

## G1. Touched keeps the whole session, not the last 50 entries

**Today.** Touched is computed in the browser from the activity entries the
page has seen: the replay gives the last 50 (`ActivityReplay`), and the ring
keeps 200 (`MaxActivity`) shared with every other event. A long session's
early files never show, and a busy one pushes file events out of the ring.

**Build.**
- `session.Local` keeps a touched index beside the activity ring: one entry
  per path with the latest op, the ops seen, a count, the tool, the agent and
  the first and last time. Bounded at 2,000 paths; the least recently
  touched goes first. It is filled in `record()` from `file` entries, so the
  server, `conductor host` and a published session all have it (they share
  `Local`).
- A sixth file operation, `op: touched` on `file_get`: kind `touched`,
  `touched[]` newest first, cut to `MaxFileHeader` (64 KiB) with `truncated`
  set. It rides the file policy like the other reads, so a guest gets it
  where a guest may read files, through the switchyard too.
- The Files pane asks for it when Touched first shows and merges live `file`
  entries on top. The Yard tile and the guest page get it the same way.
- Rows become one per file: the latest op's icon, "Edit · claude · 08:33 ·
  4 times". The Explorer's dots read the same list.
- Protocol: `internal/proto`, `protocol.ts`, `docs/protocol.md` together.

**Tests.** Go: the index (bound, eviction order, ops merged), the op through
the viewer WebSocket and the host relay. Vitest: merging the reply with live
entries. Playwright: a stub reports 60 file events; a page opened afterwards
lists all 60 files.

## G2. Files from every agent

**Today.** Claude Code names its files (Read, Edit, Write). Codex names them
only in `apply_patch`; its shell commands name none. The Copilot, Goose and
agy mappers read the tool's name and nothing else, because their payloads'
argument fields were never seen from a real run. Copilot 1.0.91 and agy
1.2.14 are installed on this machine, so those two can be checked now; the
`goose` here is the migration tool (G0), so Goose waits for its real CLI.

**Build, in two PRs.**

*G2a, no live run needed:*
- **Files seen by git, for any agent.** After a `tool_use` event that named
  no files, and at the end of a turn, `Local` runs the same `git status` the
  Changes section runs (debounced 1.5 s, skipped outside a work tree) and
  compares it with the last snapshot (status, size, mtime per path). New or
  changed paths become `file` entries with op write, edit or delete and the
  tool `git`, so the row says how it was learned. A path a hook already
  reported in that window is not added twice, and an editor save is not
  counted as the agent's. This covers shell writes by every agent,
  Claude's Bash included. Reads cannot be seen this way.

*G2b, from real payloads (captured 2026-10-09):* Codex 0.161, Copilot
1.0.91 and agy 1.2.14 each ran once in a scratch repository with a hook
that saved every payload, from a throwaway config home holding a copy of
its login (deleted afterwards). The payloads, redacted, are the fixtures in
`internal/notify/testdata/`. What they showed:

- **Codex's apply_patch files were never reported.** The patch arrives in
  `tool_input.command`; the mapper read `input` or `patch`. Its shell tool
  is `Bash` with `tool_input.command` a string (the old test assumed
  `shell` with an argv).
- **Codex runs hooks only once they are trusted.** `codex exec` skips
  untrusted hooks silently, and the interactive Codex opens on a "Hooks
  need review" question (1. Review hooks, 2. Trust all and continue, 3.
  Continue without trusting) whenever its hooks are new or changed, which
  Conductor's install makes them. Enter there picks Review hooks: a crew
  member's prompt typed at launch would land in the review screen, the
  same class of bug as the trust question fixed in round 12. A digit only
  moves the highlight; Enter confirms. Where Codex keeps the trust was not
  found (not in `config.toml`, not in its databases), and one trust in a
  throwaway home did not make `codex exec` run the hooks afterwards, so
  whether the answer lasts past one session is open. This is its own
  layer, G2c, below.
- **Copilot's arguments are an object** (`toolArgs`), not a JSON string:
  `view` and `edit` carry `path`, `create` too, `bash` a `command` string.
- **agy never ran Conductor's hooks.** agy reads
  `~/.gemini/config/hooks.json`, but a `Stop` entry must be a plain command
  hook, not a matcher group; Conductor's file puts Stop in a matcher group,
  agy rejects the whole "conductor" hook ("command hook must specify
  'command'", in its log). The tool events keep the matcher form. Payloads:
  `toolCall.name` and `toolCall.args`: `view_file` (`AbsolutePath`) reads,
  `write_to_file` (`TargetFile`) writes, `replace_file_content`,
  `multi_replace_file_content` and `edit_file` (`TargetFile`) edit,
  `delete_file` deletes, `run_command` (`CommandLine`, `Cwd`) runs a shell
  command.
- **Shell commands, for Codex, Copilot and agy alike:** parse the command
  conservatively: split on `|`, `&&`, `;`; skip anything with `$(` or
  backticks; `cat`, `head`, `tail`, `sed -n`, `nl`, `bat`, `less` name
  reads; `> file`, `>> file` and `tee file` name writes. Keep only paths
  that exist as regular files under the working directory (notify runs on
  the session's machine and can check); at most 32 per call.
- Goose waits for its real CLI (G0).

**Tests.** Go: the git snapshot diff (write, edit, delete, a hook's path not
doubled, a non-repository skipped), the shell parser table, each mapper
against its fixture. Vitest: the `git` tool's words on a row. Playwright: a
stub that writes a file through its shell with no file report shows it in
Touched as seen by git.

## G2c. Codex's "Hooks need review" question

**Found by the G2b capture (2026-10-09).** See above. Codex draws the
question at startup whenever its hooks are new or changed; Conductor knows
one startup question per agent (`trustPrompt` with its `trustAnswers`), and
Codex's is the folder trust question.

**Build.**
- The catalog takes more than one startup question per agent: a list of
  `{prompt, answers}`, the existing `trustPrompt`/`trustAnswers` its first
  entry, so saved agents keep working. Codex's built-in entry gains "Hooks
  need review" with the answers Trust all and continue (`2` then Enter) and
  Continue without trusting (`3` then Enter).
- The session watches the screen for each, as it does the trust question:
  attention `needs_input` of kind `trust` with that question's answers as
  choices, typed text and Submit refused while it shows, a crew member's
  prompt held until it is answered.
- Verify live, in a throwaway Codex home, whether trusting lasts past one
  session; the Agents page's Codex status says so either way.

**Tests.** Go: two questions on one agent, each answer, the refusal while
the second shows, the catalog's old fields still read. Vitest: the choices.
Playwright: a stub (`STUB_HOOKS_REVIEW=1`) that draws Codex's question;
the choices show, typing is refused, Trust all and continue lets the
prompt through.

## G3. Neovim: a swap file opens read-only with a choice, not a refusal

**Today.** `:edit` on a file with a swap file (another Vim has it open, or
one crashed) fails, and the tab shows Neovim's error. Inside the editor,
`:e other` hits the same wall.

**Build.**
- Before the first `:edit`, a `SwapExists` autocmd sets `v:swapchoice` to
  `o` (open read-only) and notifies the bridge with `swapinfo(v:swapname)`:
  the process id, whether it still runs, the user, whether it was modified.
- A new `nvim_event` kind `swap` carries that. The editor shows a banner:
  "Another Vim (process 4121, still running) has this file open. Opened
  read-only." with **Edit anyway**, and when that process is gone
  **Recover** and **Delete the swap file**.
- A new client message `nvim_swap{id, choice}` with a fixed set of choices
  mapped to fixed commands on the server (`:set noreadonly`, `:recover`,
  deleting that one swap file), never a command string from the browser.
- A Neovim `confirm` message (`:confirm q`, a write over a changed file)
  shows as a card with its choices as buttons that send the choice's key.

**Tests.** Go: open with a stale swap file and with a live one (a second
`nvim --embed` holding the file), each choice. Vitest: the banner's words
from the swap facts; parsing a confirm message's choices. Playwright: a
swap file planted beside a file, open under the Neovim keymap, the banner,
Edit anyway, a save.

## G4. Neovim: text that arrives without a key press

**Today.** Under the Neovim keymap the editor is read-only and only key
presses go to Neovim. An input method (Japanese, Chinese, Korean), a dead
key (é on US-International) and dictation produce text input with no key,
and the read-only text area does not even start an input method. Those
characters are lost.

**Build.**
- Under the Neovim keymap, keep the model read-only but the text area
  writable (Monaco's `domReadOnly: false`), so input methods start.
- Take the text area's `compositionend` and `beforeinput` (`insertText`
  with no key press handled) and send their text as keys, `<` as `<lt>`.
  Monaco's read-only message is turned off for this keymap.
- While a composition is in progress, its keys are not sent one by one.

**Tests.** Vitest: text to Neovim notation. Playwright: `keyboard.insertText`
of "café" lands in Neovim's buffer; a composition through Chromium's
`Input.imeSetComposition` then commit lands as its final text only. By
hand: a real input method and a dead-key layout in the Windows app.

## G5. Neovim: insert-mode typing shows at once

**Today.** Every character round-trips to the session's machine before it
shows. On a slow link (the switchyard, a phone) typing feels behind.

**Why it was set aside, and how this design answers it.** A naive local
echo cannot tell which of Neovim's line updates already include a key, so
predicted text jumps back and forth. The fix is a sequence number that
Neovim's side acknowledges in order with its line updates, as mosh does.

**Build.**
- `nvim_input` gains `seq`. After the keys, when Neovim is not waiting for
  a next key, the bridge asks Neovim to `rpcnotify` an `ack` with that
  number. The ack travels the same channel, after the line updates the keys
  caused, and the bridge forwards both in that order.
- The browser predicts only in insert mode, only plain printable
  characters. It keeps Neovim's confirmed text and lays the
  unacknowledged characters over it at the cursor. A line update replaces
  the confirmed text; an ack drops the predictions it covers.
- When an ack shows a prediction was wrong (an autopair, an abbreviation, a
  mapping like `jk`), Neovim's text wins once, and prediction stops until
  the next Escape. A prediction older than 100 ms is underlined, so a slow
  link shows what is not confirmed yet.

**Tests.** Go: the ack arrives after the line updates for its keys, and
never while a key is pending. Vitest: the prediction model (apply, ack, a
wrong guess, an update on another line, Backspace not predicted).
Playwright: the e2e server's Neovim config adds a 300 ms sleep on
`InsertCharPre`; typed text shows before Neovim answers and ends exactly as
Neovim has it; an autopair mapping corrects once. By hand: typing over the
switchyard from the LAN box and a phone.

## Order and decisions

Order: G0, G1, G2a, G2b, G2c, G3, G4, G5. G0 is a small fix that changes what
G2b can capture. G1 comes before G2 because G2's new file events land in
it. G5 is last because it is the largest and touches the protocol.

**Decisions (the owner, 2026-10-09):**
1. Touched rows: one per file with a count.
2. Files seen by git: added for every agent, labelled `git`.
3. The live captures in G2b: run them, in throwaway config homes. The
   `goose` here is not the agent (G0), so Goose's capture waits until the
   real Goose CLI is installed; Copilot, agy and Codex run now.
4. A swap file: open read-only with the banner.
5. Local echo: always on in insert mode, no switch.
