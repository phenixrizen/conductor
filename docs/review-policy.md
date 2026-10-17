# Review policy


Every pull request gets a Codex adversarial review before it is opened: a
second model questioning the design, not only the lines. It comes from the
Codex plugin for Claude Code (`openai/codex-plugin-cc`; `/plugin marketplace
add openai/codex-plugin-cc`, `/plugin install codex@openai-codex`,
`/codex:setup`, a Codex login). A person types
`/codex:adversarial-review --base origin/main <focus>`; an agent cannot
invoke that command and runs the same review through the plugin's script,
which `scripts/codex_review.py` finds (the installation Claude Code's
registry names for this repository, else the user's; never the newest
cached copy, and an ambiguous registry is refused):

```bash
python3 scripts/work_issue.py review phenixrizen/conductor#N --session SESSION --phase code --focus "<risks>"
```

- **The plan is reviewed before the code** when a change touches a state
  machine (a session's, a link's, a turn's), a protocol, concurrency or a
  security behaviour. The branch's first commit is
  `docs/plans/<branch>.md`: the events that can interact and every order
  they can come in, the invariants that must hold in all of them (say,
  "nothing is written to a viewer once its link goes"), what is out of
  scope, and the tests that will pin each ordering. Codex reviews the plan
  alone (`codex_review.py "review this plan: <focus>"`, the branch holding
  only the plan's commits) before any code is written; its findings change
  the plan, and the revision is reviewed once more. A plan still blocked
  after its second review is not revised again on the agent's own: the
  agent reports it, and the owner decides. Only then are the tests for the
  orderings written,
  from the plan, and then the code that passes them. A design that has to
  change once code exists goes back to the plan: the plan is revised and
  reviewed before the code follows it. The pull request's body names the
  plan's last reviewed commit apart from the code's. The plan stays as the
  change's design record, written as the pull request is (neutral for a
  security fix). A small fix (one cause, one place, no new state) needs no
  plan.
- `--base` reviews the commits `origin/main...HEAD`, not the working tree:
  commit everything first. The pull request's body names the commit
  reviewed; commits after it (the review's own fixes included) get another
  review before the merge, within the rounds below.
- **The review is of what will merge.** The script fetches its base and
  refuses a branch that is behind it: rebase onto the current
  `origin/main` before each review, so the review sees the change on top
  of everything merged since it began, not on the main it started from. A
  branch stacked on another (see Parallel work) names its parent with
  `--base`; once the parent merges it is rebased onto main and reviewed
  again before it merges.
- **Review rounds are bounded.** Every round costs; the limits (the owner,
  2026-10-14) are a budget, not a target. A docs or process change gets
  one round. Code gets **three**: when the third review still finds
  something, stop and sort its findings before going on. A finding in the
  current stream of work (the change introduces it, exposes it or needs
  it fixed; see the Task contract in AGENTS.md) is fixed before the change
  merges, where One change per pull request puts it: on the branch when the change introduces it or it
  is in code the change touches, in a pull request of its own that merges
  first when it is in code the change does not otherwise touch. One that
  is independent of the change is split off (an issue, and the owner's
  call), not fixed on the branch. The pull request's body records the
  checkpoint and how each finding was sorted. **Five** rounds is the stop:
  a change still finding its own defects then goes back to its plan, the
  agent reports where it stands, and the owner says whether more is spent
  on it. When the last round a change may have still finds something, the
  agent fixes that round's findings, commits them, and reports them as
  unreviewed in the pull request's body; the owner says whether one more
  round is spent on them or the pull request merges as it is. An agent
  never passes a limit on its own.
- The focus names the risks the change touches: races and ownership,
  link scopes and revocation, credentials in logs, what the switchyard can
  see, recovery after a restart, per-connection bounds.
- Its findings are claims, not facts. Each is reproduced (a test, or a run
  with `CONDUCTOR_RENDEZVOUS=0` and an isolated home) or traced in the code
  before it is fixed; one the code refutes is answered with the lines that
  refute it.
- The pull request's body lists the findings and what became of each. One
  outside the change's scope becomes an issue (see the Task contract in AGENTS.md), never dropped silently, and the owner says whether it is fixed
  now, in a pull request of its own, or waits.
- A security finding (a way to gain access, read what one should not, or
  deny service) never goes into this public repository, a pull request, a
  commit message, an issue or the project board until the owner explicitly authorizes disclosure: it goes to the affected repository's GitHub Draft Security Advisories. The
  fix's pull request describes the change, not the attack.
- When the review cannot run (no login, no network), the pull request says
  so: a limitation, not a pass.


The workflow helper invokes `scripts/codex_review.py` and records structured results outside the commit tree. Use it for issue work so counts and receipts survive resume. See `docs/issue-workflow.md` for exceptions and recovery.
