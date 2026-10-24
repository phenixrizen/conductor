# Conductor repository contract

Conductor runs coding agents in PTYs and shares browser terminals. One Go module, one Nuxt app, one binary. Read `docs/repository-map.md` for architecture and extension points; `docs/dependencies.md` before changing dependencies. These are on-demand references, not startup imports.

- Test every affected surface. Server/CLI changes get Go race tests; UI behavior gets vitest and Playwright. Changes spanning both require both. Python tooling runs the scripts suite. See `docs/checks.md`; report unavailable checks accurately.
- Protocol changes touch three places together: `internal/proto`,
  `web/app/utils/protocol.ts` and `docs/protocol.md`. Every new frame or message
  gets a size limit and a test.
- Stdlib first: `net/http` mux with method patterns, `log/slog`, `encoding/json`
  with `DisallowUnknownFields`. New Go dependencies need a reason in the PR.
- A dependency added, removed or upgraded (go.mod, `web/` or `desktop/`
  packages) regenerates `THIRD_PARTY_NOTICES` with `make notices` (after
  `npm ci` in `desktop/`), committed with it; CI checks it is current. A
  package that ships without its licence text gets the upstream text in
  `scripts/notices/`.
- Commands are argv arrays. Never build a shell string from user input.
- Compare tokens with `share.Equal`; store only hashes; never log query strings.
- Server session working directories go through `resolveCwd` (a launch
  through `openCwd`, which opens the folder it checks); file reads and saves
  go through `session.ResolvePath` and then the session's `session.Folder`
  (an `os.Root`), never a path opened again. Do not bypass either.
- Keep per-connection bounds (read limits, queues, in-flight file requests).
- UI components come from Nuxt UI; use the brand palette in `web/app/app.config.ts`
  and `docs/design/brand.md`. The junction mark is artwork, never a status light.
- Do not commit `internal/web/dist` contents (only `.gitkeep`), `web/.nuxt` or
  `web/.output`. Commit `web/package-lock.json`.
- **Windows and WSL: the person configures no networking, ever.** Never ask
  for `netsh interface portproxy`, `networkingMode=mirrored` in
  `.wslconfig`, a `.wslconfig` edit, or any other manual step to expose the
  server inside WSL. Whatever must cross Hyper-V's NAT, the desktop app
  forwards itself (today UDP for ICE, `desktop/src/udp-forwarder.ts`;
  forwarding the TLS listener and mapping the router from Windows are the
  app's job too, not yet done). Docs, setup screens, Settings text, test
  instructions and by-hand checklists follow this; a check that would need
  such a step on WSL is done from a machine that is not WSL, or waits for the
  app. The owner has said this twice; do not raise it again.

## Working an issue

GitHub Issues are the backlog; the Project board is a view of them. One
issue, one branch, one pull request, and CI is the only gate.

- Take the issue you were given, or else the top unassigned issue labelled
  `ready`. Assign yourself. Never hold two open issues at once.
- Work on the branch `issue-N`, cut from `main` and linked to the issue
  (`gh issue develop N --name issue-N`), in a worktree of its own. The
  branch on origin is the claim: one that already exists is someone's work,
  so ask before touching it.
- Open a draft PR at the first commit. `Fixes #N` finishes the issue;
  `Part of #N` leaves it open. Say in the PR body which agent or person made
  it, with a session link when there is one.
- Keep the Project board current: an issue goes on it when it is filed
  (Backlog, or Ready), moves to In progress when its branch is cut and to
  In review when its PR is pushed; Done follows the merge by itself. New
  issues do not land on the board on their own.
- Before every push run the checks for each affected surface
  (`docs/checks.md`). Report a check that did not run as not run, never as
  passed. Mark the PR ready when it is complete and green.
- Answer every review comment: fix it, or reply why not, then resolve the
  thread. The owner merges. Merged is done, and GitHub closes the issue.
- Blocked or handing off: push, leave one PR comment saying what is left,
  and unassign yourself.
- Work the owner asks for directly gets an issue first (search for an
  existing one). A product defect found on the way gets its own issue,
  unless this change caused it or cannot ship without the fix.
- A test that fails in the PR's own checks is fixed in that PR and named in
  its body under its own heading; no issue. A flake seen once that blocks
  nothing goes as one line into the single standing flaky-tests issue
  (#281), fixed when someone is in that file anyway; no issue per flake.
- Vulnerabilities go in a GitHub Draft Security Advisory, never in an
  issue, PR, commit message or doc, until the owner decides on disclosure.
