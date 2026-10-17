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

## Task contract

GitHub Issues are the authoritative backlog; the Project is its work view:
Backlog → Ready → In progress → In review → Done.
Use `.claude/skills/work-issue/SKILL.md` for ordinary development (Claude:
`/work-issue phenixrizen/conductor#N`); other agents use the same Python helper.
A direct request for an issue selects it even when it is not Ready. Otherwise
work only the queue the owner authorized. Capture directly requested new work
as a scoped issue before implementation; do not ask again for selection.

- Every independently deliverable task has an issue; reuse its existing issue.
  Keep necessary fixes and regression tests in the current task and PR. Create
  or reuse another issue for a demonstrated independent actionable defect or
  owner-selected follow-up. Optional suggestions remain review dispositions;
  unverified claims do not automatically become backlog items. Ask before
  doing unrelated work or expanding scope.
- Keep one coherent outcome and its verification together. Split independently
  useful, risky or revertible work. Parallel work considers shared contracts
  and state as well as files. Retargeted stacks need integration evidence.
- `Part of #N` is partial work; `Fixes #N` is finishing work. In review needs a
  finishing PR; Done needs that fix on main. An open PR or intermediate merge
  is not Done. Duplicates name their original; not-planned closures record the
  owner's decision and reason. Neither claims a delivered implementation.
- Use the helper's persistent session claim and checkpoint; never silently
  take over active work. Locks coordinate linked worktrees in one clone only.
- Follow `docs/review-policy.md` for planning, review and spending. Readiness
  requires acceptance criteria, applicable independent review, resolved blockers,
  affected-surface verification and current integration evidence. Stop when ready;
  submit and merge when already authorized. Drafts can precede readiness.
  Explicitly owner-scoped deliverables have bounded allowances; session/branch
  changes never reset history or spending. Optional suggestions need no extra round.
- Security findings stay in the affected repository's GitHub Draft Security Advisories until the owner explicitly authorizes disclosure,
  never public issues, cards or checkpoints. A neutral, owner-authorized
  exception supports issue-free private fixes; see `docs/issue-workflow.md`.

Before implementation, establish the selected task worktree and load its current
contract. In Claude use `/memory` to verify that worktree's instructions; preserve
unrelated checkouts. Historical plans/logs retain technical decisions, while this
contract and `docs/review-policy.md` govern current process.

Procedures and handoff: `.claude/skills/work-issue/SKILL.md` and
`docs/issue-workflow.md`. Task requirements stay in the issue and linked plan;
execution metadata stays outside the source tree. Do not auto-import these
references or recreate a todo file.

## Undisclosed security findings

GitHub Draft Repository Security Advisories in this affected repository are
mandatory and authoritative. Use `/security-finding` to search existing drafts
before recording a credible finding, update matches, and preserve claims,
confidence and verification. Adversarial-review findings follow the same path.
Use `/work-security phenixrizen/conductor GHSA-xxxx-yyyy-zzzz` for owner-authorized
remediation. No security fix starts without the owner's required authorization.

Never put undisclosed details in public issues, cards, branches, commits, PRs,
docs, release notes or accessible logs, even after tests pass. Use a temporary
private security fork for revealing patches. Publication, CVE requests and
moving changes into public history need the owner's explicit disclosure decision.
If advisory access fails, stop that operation and report only the access problem;
never fall back to public metadata. Do not retire an original private artifact
until migration is verified and the owner approves retirement. See
`.claude/skills/security-finding/SKILL.md` and `docs/security-workflow.md` on demand.
