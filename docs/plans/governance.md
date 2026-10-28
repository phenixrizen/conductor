# Governance: contributing under the DCO, the marks' owner, security reporting and the open-core PR checklist (Program item B)

Status: proposed 2026-10-28, for the owner's review; the owner sets it to
Planned. Main at b6d017df (PR #381, the move to rocksolidlabs/conductor).
Every fact below about the repository, the org and CI was read on
2026-10-28 from main and from GitHub's API; the line numbers are main's.

This item changes no product behaviour. A person using Conductor sees
nothing new. The people who see it are a contributor opening the
repository for the first time, a fork deciding what it may call itself, a
researcher who found a vulnerability, and the maintainers, whose rules
stop saying "the owner" and start naming a role.

## Articles it serves

The twelve articles are the open-core rules of the Program (section 2.1);
they are restated here in the repository's own words where this item
touches them.

1. No paid code and no licence or plan check in this repository; the only
   check is whether a provider is registered. Served: `CONTRIBUTING.md`
   states it as open-core rule 1, the PR template makes every PR tick it,
   and nothing in this item adds a gate of that kind.
2. The open-source build is fully useful on its own. Served: rule 2 and
   the template's second box; the build itself is untouched.
3. A paid feature is gated by a hosted dependency it needs, never by a
   flag a fork could flip. Served: rule 3 and the third box;
   `CONTRIBUTING.md` says paid work is built outside this repository.
4. Stop, inspect, revoke, export and required approvals stay free and never
   degrade. Served: rule 4 and the fourth box; none of those code paths is
   touched.
5. Dependencies point one way and a lint rule proves it. Not touched: the
   lint rule is item C1's; this item adds no import.
6. No feature build tag in the open-source repository. Not touched: the
   `dco` job runs `python3` on the runner, no build at all.
7. The free default of every interface is what main does today, and
   open-source code never calls `EntitlementSource` to gate a free feature.
   Not touched: no interface exists yet; `CONTRIBUTING.md` gets a "Rules
   for code" section where item C7 adds its one line.
8. A provider whose hosted service is unreachable answers as the free
   default. Not touched: no provider, nothing hosted, nothing to test.
9. Release binaries stay `CGO_ENABLED=0`; a local store is
   `modernc.org/sqlite`. Not touched: no Go changes.
10. The repository contract (`AGENTS.md`) holds in all three repositories,
    the private ones dropping the DCO. Served: `AGENTS.md` stays the
    contract and gains the sign-off as one bullet a private copy leaves
    out; `CONTRIBUTING.md` keeps the sign-off in one section and the PR
    template in one line, for the same reason.
11. The railroad names keep their meaning and "workspace" is reserved.
    Served: the new files say Conductor, Yardmaster, Brakeman and Switchman
    as `AGENTS.md` defines them, call a worktree a worktree, and
    `TRADEMARKS.md` covers the Conductor name.
12. Execution stays behind one seam. Not touched: no process, check or
    attempt is started or changed by documents, a workflow job and a script
    that reads `git log` on a CI runner (the Boxcar table below).

## Why and what happens

Conductor is open source under Apache-2.0 and is about to take
contributions from outside RockSolid Labs while the paid product grows in
private code beside it. The repository has `LICENSE`, a `NOTICE` that calls
the Conductor marks "the project's marks" (NOTICE:10-11), an `AGENTS.md`
written for the owner's own agents that says "the owner" five times
(lines 40, 64, 67, 79, 85), a PR template with no checklist
(`.github/pull_request_template.md`, nine lines), and no `CONTRIBUTING.md`,
`SECURITY.md`, `TRADEMARKS.md` or DCO. No commit on main carries a
`Signed-off-by` line: none of the twenty most recent non-merge commits has
one. CI's one required status check is `required-checks` (ruleset
25591653 on `main`; `ci.yml:257-264`), green when every job in its
`needs` list passed.

After this item:

- A contributor finds `CONTRIBUTING.md`: `AGENTS.md` for how an issue is
  worked, `docs/checks.md` for the checks, the sign-off on every commit
  with the fix for a branch that lacks it, the four open-core rules, where
  paid work goes instead, the PR checklist, and who the maintainers are.
- A pull request with a commit whose author has not signed it off fails
  CI: the `dco` job names the commit and the fix in its log, and
  `required-checks`, which needs it, is red, so the PR cannot merge. A
  signed-off branch is green. `make preflight` says the same thing first,
  in seconds, before the hour the rest takes.
- `TRADEMARKS.md` says that the Conductor name, the junction mark and the
  logos, and the RockSolid Labs name and logos, belong to RockSolid Labs,
  Inc.; what a fork may do without asking (redistribute unmodified builds
  with the marks, say "built on Conductor" or "a fork of Conductor") and
  what it may not (ship a modified build or a derived product named or
  branded Conductor, put the logos on anything that is not this project,
  imply endorsement); and whom to ask. `NOTICE` names the same owner and
  points at it.
- `SECURITY.md` tells an outsider to report a vulnerability through
  GitHub's private vulnerability reporting, which opens a draft security
  advisory only the maintainers see, and never an issue; and states the
  rule everyone working in the repository already follows (`AGENTS.md`:
  a Draft Security Advisory, never an issue, PR, commit message or doc).
- The PR template carries a sign-off line and a five-point checklist whose
  first four points are the open-core rules.
- `AGENTS.md` speaks of maintainers, a role of the GitHub organization,
  instead of "the owner".
- The company is spelled one way everywhere: "RockSolid Labs", and
  "RockSolid Labs, Inc." for the legal entity.

## The changes

Three pull requests; by file. Nothing here touches a Go package, the web
app, the desktop shell, the protocol, a store or a schema, so there is no
migration and no new frame; no dependency is added, so `make notices` has
nothing to regenerate. Names: the application is Conductor; Yardmaster is
its agent harness; a worktree is a worktree.

### `CONTRIBUTING.md` (new, repository root; PR-1)

Sections, in this order, in plain words:

1. **Before you start.** One line on what Conductor is (coding agents in
   real terminals, driven from the browser, shared by link; Yardmaster runs
   crews of them as managed runs). The contract is `AGENTS.md`, read whole:
   its "Working an issue" section is the process. `docs/repository-map.md`
   for where things are, `docs/checks.md` for the checks, `docs/plans/` for
   a change larger than one PR (a plan is reviewed before its code).
2. **Who decides.** The maintainers are the owners of the `rocksolidlabs`
   GitHub organization (today two accounts; the org's `Owners` team lists
   them). A maintainer merges, approves a plan, decides when a defect
   outside a stream is worked, and decides a vulnerability's disclosure.
   The `ready` label on an issue is the maintainers' go-ahead. Everything
   else is any contributor's. `AGENTS.md` says "a maintainer" wherever it
   means this role.
3. **Issues and pull requests.** One issue, one branch `issue-N` cut from
   `main`, one PR; a draft PR at the first commit; `Fixes #N` or
   `Part of #N`; the PR body as the template lays it out, with who made it
   (person or agent, with a session link when there is one); every review
   comment answered, fixed or declined with why. A branch on origin is a
   claim. Never two open issues at once.
4. **Sign-off.** Every commit is signed off: `git commit -s` adds
   `Signed-off-by: Your Name <you@example.org>`, and by adding it you
   certify the Developer Certificate of Origin, whose text is in `DCO` at
   the root (in short: you wrote the change or have the right to submit it
   under this repository's licence, and you know the contribution and your
   sign-off are public and kept). The name and address must be the commit
   author's, as `git config user.name` and `user.email` give them. There
   is no contributor licence agreement. CI's `dco` job fails a pull request
   with a commit that lacks a matching sign-off, naming the commit. The
   fix for a branch: `git rebase --signoff origin/main`, then
   `git push --force-with-lease`; for the last commit alone,
   `git commit --amend -s --no-edit`. Check before pushing:
   `python3 scripts/dco_check.py --base origin/main --head HEAD`
   (`make preflight` runs it first). A merge commit needs no sign-off, and
   a branch is rebased rather than merged anyway. An agent's commits are
   the commits of the person running it: they carry that person's
   sign-off; the agent's attribution lines (`Co-Authored-By`, a session
   link) are something else and do not replace it.
5. **Checks before a push.** `docs/checks.md`, by affected surface; the
   PR's Verification section lists what ran and what did not ("not run",
   never "passed"); CI is the only gate and a maintainer merges.
6. **The open-core rules.** Conductor is open core: this repository is the
   whole free product, built and useful on its own; RockSolid Labs builds
   paid features outside it, as providers registered through extension
   points and as hosted services. The four rules, one sentence each (the
   wording the PR template's first four boxes cite as "rule 1" to
   "rule 4"):
   1. no paid code, licence check or plan check in this repository; the
      only gate it knows is whether a provider is registered;
   2. the open-source build stays fully useful on its own: nothing free is
      removed or degraded;
   3. nothing paid hides behind a flag: a paid feature needs its hosted
      service, through a registered provider;
   4. stop, inspect, revoke, export and required approvals are free and
      never degrade.
   A PR that adds a licence check, a plan check, a flag for something paid,
   a paid feature's code, or that removes or weakens a free safety control,
   is declined whatever else it does. Where paid work goes instead: not
   here; RockSolid Labs' private code and services, reached through the
   registered providers; write to conductor@rocksolidlabs.io to talk about
   it. The private repositories are not named.
7. **The pull request checklist.** The five points, each with what it
   means in practice, and the sign-off line (the template, below).
8. **Rules for code.** Pointers only, `AGENTS.md` has them: argv arrays,
   `share.Equal`, `resolveCwd` and `os.Root`, per-connection bounds; a
   protocol change touches `internal/proto`, `web/app/utils/protocol.ts`
   and `docs/protocol.md` together, with a size limit and a test; a new
   dependency needs its reason in the PR and `make notices`; every shipped
   feature gets its `docs/features.md` entry. Item C7 adds its line here.
9. **Licence, marks, security.** Contributions are under the Apache
   License 2.0 (`LICENSE`), no copyright assignment; the marks are
   `TRADEMARKS.md`'s; a vulnerability is `SECURITY.md`'s and never an
   issue.

About 150 lines. The company is "RockSolid Labs"; the entity
"RockSolid Labs, Inc."

### `DCO` (new, repository root; PR-1)

The Developer Certificate of Origin, version 1.1, verbatim from
developercertificate.org, including its own notice (verbatim copies are
permitted, changes are not). It is not under Apache-2.0 and says so itself;
`scripts/notices.py` reads Go modules and npm packages, never root files,
so `THIRD_PARTY_NOTICES` is unchanged.

### `AGENTS.md` (edited; PR-1)

- A new bullet in the top list, after the dependency bullet: every commit
  is signed off (`git commit -s`; the Developer Certificate of Origin in
  `DCO`; the sign-off's name and address the commit author's); CI's `dco`
  job fails a pull request with a commit that lacks it;
  `git rebase --signoff` fixes a branch; `CONTRIBUTING.md` says how.
  `CLAUDE.md` imports `AGENTS.md`, so every agent reads it.
- "The owner" becomes the role, at its five sites: line 40, "The owner has
  said this twice" becomes "The maintainers have said this twice";
  line 64, "The owner merges" becomes "A maintainer merges"; line 67,
  "Work the owner asks for" becomes "Work a maintainer asks for";
  line 79, "the owner decides when" becomes "a maintainer decides when";
  line 85, "until the owner decides on disclosure" becomes "until a
  maintainer decides on disclosure (`SECURITY.md`)".
- One sentence in the "Working an issue" introduction defines the role:
  the maintainers are the owners of the `rocksolidlabs` GitHub
  organization (`CONTRIBUTING.md`, Who decides); they merge and decide.
- The draft-PR bullet (lines 53-55) adds: tick the template's checklist.
- Nothing else moves. Other files say "the owner" in other senses or as
  history and stay: `docs/dependencies.md:17` (a dated choice),
  `README.md:302` (a session's owner), the plans and round documents.

### `.github/pull_request_template.md` (edited; PR-1)

Between Verification and the "Made by" comment:

```markdown
## Checklist

- [ ] No paid code, licence check or plan check: the only gate this repository knows is whether a provider is registered (CONTRIBUTING.md, open-core rule 1)
- [ ] The open-source build stays fully useful on its own: nothing free is removed or degraded (rule 2)
- [ ] Nothing paid hides behind a flag: a paid feature needs its hosted service, through a registered provider (rule 3)
- [ ] Stop, inspect, revoke, export and required approvals are untouched, or still free and whole (rule 4)
- [ ] The checks of every affected surface ran and are listed under Verification, one not run named as such; a new dependency has its reason and `make notices` ran (docs/checks.md)

Every commit is signed off by its author (`git commit -s`; CONTRIBUTING.md, Sign-off): the `dco` check proves it.
```

The first line, the two headings and the comments stay as they are.

### `.github/ISSUE_TEMPLATE/task.md` (edited; PR-1)

Its closing comment gains "(`SECURITY.md` says how)" after "Vulnerabilities
go in a Draft Security Advisory, never here."

### `TRADEMARKS.md` (new, repository root; PR-2)

Sections:

1. **The marks.** The name "Conductor" as the name of this software; the
   junction mark (the Switch: a C of two parallel routes with a separate,
   right-leaning terracotta junction; `docs/design/brand.md`) and the
   lockups, the app icon and the favicon that carry it, the seven SVGs in
   `web/public/brand/`; the RockSolid Labs name and logos in
   `web/public/sponsor/` and `desktop/static/sponsor/` (three PNGs each).
2. **The owner.** RockSolid Labs, Inc. (rocksolidlabs.io), which sponsors
   and maintains Conductor, owns all of them.
3. **What the licence does not grant.** The Apache License 2.0 covers the
   code; its section 6 grants no right to the marks. The image files are
   in the repository so the unmodified software builds and shows them;
   that is the whole of what their presence allows.
4. **Allowed without asking.** Redistributing unmodified builds of this
   repository with the marks as they are (packages, images, a mirror);
   plain references in words that do not imply endorsement: "built on
   Conductor", "a fork of Conductor", "works with Conductor"; the name to
   identify this software in talks, articles, package lists and
   compatibility notes; screenshots of the unmodified software.
5. **Not allowed without written permission.** A modified build, or a
   product derived from this repository, named or branded Conductor, or a
   name confusingly close to it; the junction mark or the logos on
   anything that is not this project (a product, a site, a store listing,
   a domain name, a company name); anything implying that RockSolid Labs
   made, endorses or sponsors something it did not; the RockSolid Labs
   logos anywhere except as the unmodified software shows them.
6. **A fork.** Rename it and drop the marks. Where they are compiled in:
   `web/public/brand/`, `web/public/sponsor/`, `desktop/static/sponsor/`,
   `web/app/utils/about.ts` (the credit and the sponsor's name and site),
   `web/app/components/AboutCard.vue`, `web/app/pages/join/[token].vue`,
   `desktop/src/splash.ts` and `desktop/static/splash.html`,
   `internal/cli/root.go` (`Credit`), `internal/api/switchyard_page.go`
   (the header's marks, the footer's badge), `desktop/electron-builder.yml`
   (`productName`, `copyright`), `web/nuxt.config.ts` (the title),
   `README.md`'s footer, `docs/design/brand.md`. The railroad names of
   Conductor's parts (Yardmaster, Brakeman, Switchman, the Switchyard, the
   Yard, the Roundhouse) are ordinary words used descriptively and are not
   claimed as marks; a fork may keep them.
7. **The spelling.** "Conductor"; "RockSolid Labs"; "RockSolid Labs,
   Inc." for the legal entity; the site rocksolidlabs.io; the GitHub
   organization `rocksolidlabs`.
8. **Questions.** conductor@rocksolidlabs.io.

About 90 lines. `docs/design/brand.md` keeps the rule that the junction is
artwork and never a status light (`AGENTS.md`); `TRADEMARKS.md` is about
who may use the marks, not how the project draws them.

### `NOTICE` (edited, lines 8-12; PR-2)

Lines 1-7 stay. Lines 8-12 become one paragraph of five lines that says:
the Conductor name, the junction mark and the logos (`web/public/brand`,
`docs/design/brand.md`), and the RockSolid Labs name and logos
(`web/public/sponsor`, `desktop/static/sponsor`), are trademarks of
RockSolid Labs, Inc.; the Apache License grants no right to use them (its
section 6); `TRADEMARKS.md` in the source repository
(github.com/rocksolidlabs/conductor) says what is allowed without asking
and what is not. The file stays twelve lines; the Dockerfile (line 31) and
`desktop/electron-builder.yml` (lines 22-23) copy it unchanged, and a
`NOTICE` read inside a package still points somewhere.

### `SECURITY.md` (new, repository root; PR-2)

1. **Reporting.** Through GitHub's private vulnerability reporting on this
   repository (Security, "Report a vulnerability":
   github.com/rocksolidlabs/conductor/security/advisories/new), which
   opens a draft security advisory that only the maintainers and the
   reporter see. Never an issue, a pull request, a discussion or a public
   comment. If GitHub cannot be used: conductor@rocksolidlabs.io with
   "security" in the subject.
2. **What to include.** The version (`conductor version`, or the app's
   About card), the platform, what an attacker needs (a share link, a
   view role, a hosted session, the switchyard's relay, a local account)
   and what they get, and steps that reproduce it.
3. **What happens.** An acknowledgement within seven days; the work in the
   draft advisory (GitHub's private fork of the advisory for the fix when
   one is needed); the fix lands as an ordinary pull request that names the
   change and not the vulnerability; the advisory is published when the
   fix has shipped, with credit if the reporter wants it; the maintainers
   decide the timing. No bounty.
4. **Scope.** The `conductor` binary in all its modes (`serve`, `host`,
   `switchyard`), the web app, the desktop app, the published packages and
   image, and the public switchyard at switchyard.rslabs.net that RockSolid
   Labs runs. Out of scope: the coding agents Conductor launches (report
   to their vendors) and what the README's "Security model and limits"
   documents as by design.
5. **Supported versions.** The latest release and `main`. Older release
   candidates get no fixes; the desktop app updates itself.
6. **Everyone working in this repository.** A vulnerability found while
   working, by a person or an agent, goes into a draft security advisory
   and never into an issue, a pull request, a commit message or a doc,
   until a maintainer decides on disclosure (`AGENTS.md`).

About 60 lines.

### `.github/ISSUE_TEMPLATE/config.yml` (new; PR-2)

`blank_issues_enabled: true` and one `contact_links` entry, "Report a
vulnerability", to the advisory URL above, so the issue chooser offers the
private channel before the form.

### `docs/design/brand.md` (edited; PR-2)

One sentence under "Assets and source of truth": the Conductor name and
these marks, and the RockSolid Labs marks, belong to RockSolid Labs, Inc.;
`TRADEMARKS.md` says what may be done with them. Line 215's "does not
establish trademark clearance" stays true of that guide.

### `scripts/dco_check.py` (new; PR-3)

Standard library only, the SPDX and copyright lines `scripts/preflight.py`
carries, a docstring with the usage:

```
python3 scripts/dco_check.py --base <rev> --head <rev> [--repo <dir>]
```

- `signoffs(message) -> list[(name, address)]`: every line whose key is
  `Signed-off-by:` (the key compared without regard to case, leading
  whitespace allowed) parsed as `Name <address>`; a line without angle
  brackets is no sign-off.
- `Commit`: sha, parents, author name, author address, subject, message.
- `problems(commits) -> list[str]`: for every commit with fewer than two
  parents, unless one of its sign-offs has the author's name (trimmed,
  exact) and the author's address (compared without regard to case), one
  line: the first twelve characters of the sha, the subject, the author,
  and either `no Signed-off-by` or the sign-offs found with `(not the
  author)`. Merge commits are skipped, as the DCO GitHub App skips them.
  The committer is not consulted: a maintainer's rebase keeps the author's
  sign-off. No bot exemption.
- `read(repo, base, head) -> list[Commit]`: one
  `git -C repo log --reverse --format=%x1e%H%x1f%P%x1f%an%x1f%ae%x1f%s%x1f%B base..head`,
  read as UTF-8 whatever the locale (the lesson of
  `release_draft_test.py`'s `Gh` test), split on the record and unit
  separators; a record that does not split into six parts (a message
  holding those bytes) fails the read. The script never passes a shell
  string.
- `main(argv)`: exit 0 and `N commits signed off by their authors` (or
  `no commits between base and head`); exit 1 with one line per problem and
  a last line, `K of N commits are not signed off by their author. Fix:
  git rebase --signoff <base> && git push --force-with-lease
  (CONTRIBUTING.md, Sign-off).`; exit 2 when git fails or a record does not
  parse, with git's message. Never silently green.

About 120 lines.

### `scripts/dco_check_test.py` (new; PR-3)

`python3 -m unittest discover -s scripts -p '*_test.py'` finds it. The
tests, each failing without its part:

- `test_a_signoff_line_is_parsed_with_its_name_and_address` (the key in
  any case, leading spaces, a line in the middle of the body, two lines).
- `test_a_line_without_an_address_is_no_signoff`.
- `test_a_commit_signed_off_by_its_author_passes`.
- `test_a_commit_with_no_signoff_is_named_with_its_author_and_subject`.
- `test_another_persons_signoff_does_not_count` (a co-author's, a
  reviewer's).
- `test_the_address_is_compared_without_regard_to_case_and_the_name_exactly`.
- `test_a_merge_commit_is_skipped`.
- `test_an_empty_range_passes`.
- `test_the_report_counts_the_commits_and_names_the_fix`.
- `test_a_range_is_read_from_a_repository_as_git_logs_it`: a repository
  made in a temporary directory with `git init -q`, three commits (one
  signed off, one not, one a `--no-ff` merge of a branch), `main([...])`
  returning 1 and naming the unsigned one only; then the unsigned commit
  amended with `-s` and the same range returning 0. The git calls carry
  `-c user.name`, `-c user.email`, `-c commit.gpgsign=false`,
  `-c init.defaultBranch=main` and run with `GIT_CONFIG_GLOBAL=/dev/null`
  and `GIT_CONFIG_NOSYSTEM=1`, so a developer's global configuration (a
  signing key, a commit template, a hooks path) cannot make the test fail
  or pass for its own reasons.
- `test_a_record_that_does_not_parse_fails_the_read` (exit 2).

### `.github/workflows/ci.yml` (edited; PR-3)

A job before `required-checks`, and `dco` appended to that job's `needs`
list (line 259):

```yaml
  dco:
    # Every commit of a pull request is signed off by its author
    # (CONTRIBUTING.md, Sign-off). The job runs on every event and only
    # its check step is conditional: required-checks reads a skipped job
    # as a failure, and a push to main carries GitHub's merge commits,
    # which nobody signs.
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - if: github.event_name == 'pull_request'
        env:
          BASE: ${{ github.event.pull_request.base.sha }}
          HEAD: ${{ github.event.pull_request.head.sha }}
        run: python3 scripts/dco_check.py --base "$BASE" --head "$HEAD"
```

The shas reach the script through the environment, as `release.yml` passes
its version. `python3` is on the runner, as the `go` job already relies on
(`ci.yml:77-78`). No permission beyond `contents: read` is needed.

### `scripts/preflight.py` and `scripts/preflight_test.py` (edited; PR-3)

`LINUX_STEPS` gains, first, `{"name": "dco", "covers": "dco", "argv":
["python3", "scripts/dco_check.py", "--base", "origin/main", "--head",
"HEAD"]}`: it takes a second, and the rest of the run takes an hour, so an
unsigned commit is the first line of the receipt. Without the step the
existing `test_every_ci_job_has_a_linux_step` (`preflight_test.py:99`)
fails on the new job: that test is why the step cannot be forgotten.
`preflight_test.py` gains `test_the_dco_check_runs_first_against_origin_main`
(the first Linux step's name and argv). The Mac and Windows rows do not
get it: what a commit carries does not depend on the machine.

### `docs/checks.md` (edited; PR-3)

The code block gains
`python3 scripts/dco_check.py --base origin/main --head HEAD  # every commit of the branch signed off by its author (CI's dco job)`,
and the scripts-suite line's "(notices.py)" becomes the four scripts.

### `docs/repository-map.md` (edited; PR-1, PR-2, PR-3)

One row in the table, added by PR-1 and extended by the two after it:
`CONTRIBUTING.md`, `DCO`, `TRADEMARKS.md`, `SECURITY.md`, `NOTICE`,
`scripts/dco_check.py`: how to contribute (the sign-off, the open-core
rules, the checklist, the maintainers), the certificate's text, the marks
and their owner, how to report a vulnerability, and the check CI's `dco`
job runs.

### `docs/features.md` (edited; PR-3, the last)

One entry, where the newest entries go (before the entry for the move,
#380, 2026-10-27), recording what shipped and what was observed: the two
runs of the acceptance check with their URLs, the decisions below, and the
date.

### Not changed

`README.md` (stream A's: the badges and the links to `CONTRIBUTING.md` and
`TRADEMARKS.md`; its footer's "see NOTICE for the marks" stays true,
since `NOTICE` points on), the `Makefile`, the Dockerfile, the desktop
packaging, the web app and its tests (the About card states the licence
and the copyright, no mark ownership), Go code (the switchyard page's
footer says Apache-2.0 and shows the credit, nothing about the marks), the
ruleset, `THIRD_PARTY_NOTICES`.

## Events, orderings and invariants

No state machine, store, schema or protocol is touched: this item is
documents, one workflow job and one script. What does have an order is CI.

- The `dco` job runs on every `ci.yml` event (`pull_request`, `push` to
  `main`, `workflow_call` from `release.yml`); only its check step is
  conditional on `pull_request`. The job is never skipped, because
  `required-checks` (`if: always()`, `ci.yml:258`) counts a need whose
  result is `skipped` as a failure (`ci.yml:263`), and a push to `main`
  carries merge commits GitHub made, which have no sign-off by design.
- The set checked is `git rev-list base..head` with `base` the pull
  request's `base.sha` and `head` its `head.sha` from the event: exactly
  the commits GitHub lists on the PR, whatever `main` did since. The
  checkout fetches the whole history (`fetch-depth: 0`), so both are
  present; a PR from a fork is the same, since its head is a parent of the
  merge ref the checkout takes.
- The invariant: every commit in that set with at most one parent carries
  a `Signed-off-by` whose name is the author's and whose address is the
  author's, compared without regard to case. Merge commits are exempt.
  The committer is not consulted. No author is exempt, bots included;
  the first bot adopted brings its exemption in its own PR.
- The workflow that runs for a pull request is the one at the PR's merge
  commit, so the PR that adds the job is checked by it, and so is every PR
  after. History is never re-checked: the commits on `main` before the
  job carry no sign-off and never will; the rule is not retroactive.
- Order of the PRs: the words (PR-1) before the check (PR-3), so nobody
  meets the check before the rule is written, and the template's sign-off
  line tells a contributor before CI does.
- The script fails closed: exit 1 names every offending commit and the
  fix; exit 2 is a read that failed (git, or a record that did not parse);
  there is no path to exit 0 without every commit read and judged.
- `make preflight` runs the step first; `--only dco` gives a partial
  receipt as any `--only` does (`preflight.py:188-189`).
- `NOTICE` keeps twelve lines; the two sites that copy it do not change.
- `SECURITY.md`'s channel depends on a repository setting, private
  vulnerability reporting, which is off today
  (`GET /repos/rocksolidlabs/conductor/private-vulnerability-reporting`
  answers `enabled: false`). A maintainer turns it on before PR-2 merges;
  until then the advisory link leads to a page a reporter cannot use. The
  implementer checks the setting with that same call and says so in the
  PR body.

## Out of scope

- `README.md`'s CI and licence badges and its links to `CONTRIBUTING.md`
  and `TRADEMARKS.md`: stream A's, once this item lands.
- The repository's GitHub description and topics (empty today): assigned
  to no item; a setting a maintainer edits by hand.
- `CODEOWNERS`, a code of conduct, a `MAINTAINERS` file, a CLA: not asked
  for; the owner may add them later.
- Making the org's `Owners` team visible (it is secret today) or creating a
  `maintainers` team: org settings; the role is defined in words.
- A `make` target for the check: `docs/checks.md` and preflight carry the
  command.
- Shipping `TRADEMARKS.md` inside the desktop packages and the image:
  `NOTICE`, which they carry, points at it by repository.
- The `EntitlementSource` line (item C7) and the import lint (item C1):
  the seams' work; `CONTRIBUTING.md` leaves them their section.
- The DCO GitHub App: not installed (the check runs on main's head are
  GitHub Actions' alone) and not adopted; see O13 below.
- The private repositories' contract (`AGENTS.md` minus the DCO): their
  scaffolds' work (the Program, section 2.3).
- Re-signing history; rewriting any commit on `main`.
- A test or lint for the company's spelling: the PR's verification greps
  for it instead.
- The six historical docs that say phenixrizen, and the doc drift named
  for stream A (`docs/repository-map.md`'s schema version, the three
  "AGENTS.md, Reviews" citations).
- Org security settings (secret scanning, push protection, Dependabot):
  stream A's verification.

## Tests per surface

Per `docs/checks.md`: a change to the Python scripts runs their suite and
needs no unrelated Go, UI, desktop, network or hardware tests; a docs
change has no check; a workflow change has no local runner.

- **Documents** (`CONTRIBUTING.md`, `DCO`, `TRADEMARKS.md`, `SECURITY.md`,
  `NOTICE`, `AGENTS.md`, `docs/design/brand.md`, `docs/repository-map.md`,
  `docs/checks.md`, `docs/features.md`): no check applies. By hand, in
  each PR's Verification: every path a new file names exists
  (`git ls-files` for each); `DCO` diffed against developercertificate.org
  with no difference; `git grep -i 'rock solid'` and
  `git grep -i 'rocksolid labs' | grep -v 'RockSolid Labs'` find nothing;
  `wc -l NOTICE` is 12; the line-number sites in `AGENTS.md` and `NOTICE`
  named above are the ones edited.
- **`.github` templates** (PR-1, PR-2): no runner; read twice; the next PR
  opened shows the template and ticks it (PR-2's body is the first).
- **`.github/workflows/ci.yml`** (PR-3): no runner; read twice; the scripts
  suite (`preflight_test.py`'s job-coverage test fails when a job has no
  preflight step); the PR's own CI run shows the `dco` job green and
  `required-checks` listing it among its needs; the throwaway PR (below)
  shows it red with the commit named, then green.
- **Scripts** (PR-3: `scripts/dco_check.py`, `scripts/preflight.py` and
  the two tests): `python3 -m unittest discover -s scripts -p '*_test.py'`
  before every push, with every test named above; CI's `go` job runs the
  same suite (`ci.yml:78`); `python3 scripts/preflight.py --only dco` once,
  to see the step run here (a partial receipt, as expected).
- **Go, web, desktop**: not affected by any PR; reported as not run.
- **By hand, what no test reaches**: the acceptance check (below); the
  private-vulnerability-reporting setting read back before PR-2 merges;
  the mailbox conductor@rocksolidlabs.io confirmed by a maintainer.

## Boxcar constraints

The eight constraints of the Program's section 2.5, each not touched:

1. An attempt starts only through `crew.Launcher`: no attempt, run or
   process of Yardmaster's is started or changed; the `dco` job is CI
   tooling on a runner, like `notices.py` and `brand_assets.py`.
2. `session.Local` drives a `session.Process`; no code reaches past it:
   no session code is touched.
3. An attempt's context is files relative to its worktree: no task file,
   worktree or `os.Root` is touched.
4. The agent reaches the server over the network: no hook, MCP or token
   path is touched.
5. Checks go through `internal/proc` and carry where they ran: this is
   about Yardmaster's task checks; `dco_check.py` is not one, and it runs
   `git` through `subprocess` with an argv list on the CI runner, as the
   other scripts in `scripts/` do.
6. Platform splits stay platform tags and the UI shows capabilities, never
   a plan: no tag, capability or UI is touched.
7. Evidence rows gain `where` in C10 and nothing assumes a native process:
   no ledger row is touched.
8. `hostagent` is a launch site as much as `api`: neither is touched.

## Open-core checklist

For the item as a whole, the four rules it writes down:

- [x] Rule 1 (article 1): this item adds no paid code and no licence or
  plan check; it adds the words that forbid them and the box every PR
  ticks.
- [x] Rule 2 (article 2): the open-source build is untouched and the
  documents say it stays whole.
- [x] Rule 3 (article 3): nothing is gated by anything; the documents say
  a paid feature is gated only by its hosted service.
- [x] Rule 4 (article 4): stop, inspect, revoke, export and required
  approvals are not touched; `SECURITY.md` and the checklist keep them
  named as what never degrades.

## The PRs

Three pull requests, in this order, each cut from `main` on its own
`issue-N` branch once the plan is cut into issues, each green on its own,
each one sitting. They share the repository root, `.github/` and `docs/`,
so none runs in parallel with another.

### PR-1: Contributing under the DCO: `CONTRIBUTING.md`, the `DCO` text, the maintainer role in `AGENTS.md`, the PR and issue templates

- Contains: `CONTRIBUTING.md` and `DCO` (new); `AGENTS.md` (the sign-off
  bullet, the role's five sites and its definition, the checklist in the
  draft-PR bullet); `.github/pull_request_template.md` (the checklist and
  the sign-off line); `.github/ISSUE_TEMPLATE/task.md` (the pointer);
  `docs/repository-map.md` (the row, with the files this PR adds).
- Touches: the repository root (`CONTRIBUTING.md`, `DCO`, `AGENTS.md`),
  `.github` (the two templates), `docs` (`repository-map.md`).
- Tests: none apply (documents and templates); by hand, the path check,
  the `DCO` diff, the spelling grep. Every commit signed off (the brief's
  rule, before CI enforces it).
- Depends on: nothing. Parallel with: none.
- About 250 changed lines.

### PR-2: The marks and security: `TRADEMARKS.md`, `SECURITY.md`, `NOTICE`'s owner line, the brand guide's pointer, the vulnerability contact link

- Contains: `TRADEMARKS.md` and `SECURITY.md` (new); `NOTICE` lines 8-12;
  `docs/design/brand.md` (one sentence);
  `.github/ISSUE_TEMPLATE/config.yml` (new); `docs/repository-map.md` (the
  row extended).
- Touches: the repository root (`TRADEMARKS.md`, `SECURITY.md`, `NOTICE`),
  `.github` (`ISSUE_TEMPLATE/config.yml`), `docs` (`design/brand.md`,
  `repository-map.md`).
- Tests: none apply; by hand, `wc -l NOTICE`, the path check, the
  spelling grep, the private-vulnerability-reporting setting read back
  (`enabled: true`) and the advisory link opened as a non-member once the
  setting is on.
- Depends on: PR-1 (`CONTRIBUTING.md` links to both new files; the
  repository-map row it extends). Parallel with: none.
- Needs a maintainer before it merges: the setting on, the mailbox
  confirmed, the wording of `TRADEMARKS.md` and `SECURITY.md` approved
  (the review approval is that).
- About 200 changed lines.

### PR-3: The dco check: `scripts/dco_check.py` and its tests, the `dco` job behind `required-checks`, the preflight step, `docs/checks.md`, the `docs/features.md` entry

- Contains: `scripts/dco_check.py` and `scripts/dco_check_test.py` (new);
  `scripts/preflight.py` and `scripts/preflight_test.py`;
  `.github/workflows/ci.yml` (the job and the `needs` list);
  `docs/checks.md`; `docs/repository-map.md` (the row's last file);
  `docs/features.md` (the entry for the whole item).
- Touches: `scripts`, `.github/workflows`, `docs`.
- Tests: the scripts suite with the eleven tests named above and the
  preflight test; `python3 scripts/preflight.py --only dco`; `ci.yml` read
  twice; the PR's own CI run; the throwaway PR of the acceptance check,
  cut from this branch, its two run URLs in the PR body.
- Depends on: PR-1 (the words the job's comment and the script's last line
  cite). Parallel with: none.
- About 360 changed lines.

## Owner decisions

Dated 2026-10-28. O11 and O13 are the open decisions the Program leaves to
this plan (section 3.2); the rest are defaults the owner can overturn with
a status-log row (the convention of section 3.1).

- **O11, one spelling of the company name.** Default: "RockSolid Labs",
  and "RockSolid Labs, Inc." for the legal entity, as `LICENSE`, `NOTICE`,
  the copyright lines and the credit already have it; the site
  rocksolidlabs.io and the organization `rocksolidlabs` as names of those
  things. A grep of the repository on 2026-10-28 found no other spelling
  ("Rock Solid" with a space appears nowhere), so no file changes for it;
  `TRADEMARKS.md` states the rule so the next file follows it. The owner
  records the answer in the status log.
- **O13, the DCO check.** Default: the `dco` job inside `ci.yml`, in the
  `needs` of `required-checks`. CI stays the only gate; nothing is
  installed or trusted on the org; the same script runs on a laptop and in
  preflight; the ruleset is untouched (its required context is already
  `required-checks`). The DCO GitHub App is not installed today. If the
  owner prefers the App as a second required check, the ruleset gains its
  context (an action only the owner takes) and the job is dropped, not
  doubled.
- **B1, the named role.** "Maintainer": an owner of the `rocksolidlabs`
  GitHub organization. Two accounts hold it today, and the org's `Owners`
  team (secret) mirrors them; no team is created and no setting changes.
  Overturn by naming a team in `CONTRIBUTING.md`'s "Who decides".
- **B2, where the certificate's text lives.** `DCO` at the root, verbatim
  1.1, so what a sign-off certifies is in the repository and readable
  offline; `CONTRIBUTING.md` paraphrases and points.
- **B3, what a sign-off must match.** The commit author's name (exact,
  trimmed) and address (case ignored); merge commits exempt; the committer
  not consulted; no bot exemption until a bot is adopted.
- **B4, the fifth point of the checklist.** The four open-core rules, then
  the checks-per-surface rule with the dependency rule (what `AGENTS.md`
  already requires of every PR and the template's Verification section
  already asks for). The sign-off is a line of its own, not a box.
- **B5, the contact address.** conductor@rocksolidlabs.io for the marks
  and as the security fallback: it is already the Linux package's
  maintainer address (`desktop/electron-builder.yml:56`). The owner
  confirms the mailbox or names another before PR-2 merges.
- **B6, the security channel.** GitHub's private vulnerability reporting
  first (the setting a maintainer turns on), the mailbox second; an
  acknowledgement within seven days as the stated expectation; no bounty.
- **B7, private repositories unnamed.** Public files say paid work is
  built by RockSolid Labs outside this repository and name no private
  repository.
- **B8, the component names.** `TRADEMARKS.md` claims Conductor, the
  junction mark and logos, and the RockSolid Labs marks; the railroad
  names of Conductor's parts are described as ordinary words a fork may
  keep.
- **B9, the README.** Left to stream A, which the Program already charges
  with its badges and links; its footer stays true through `NOTICE`.
- **B10, not retroactive.** No commit on `main` is rewritten or re-signed.
- **B11, three PRs in order.** PR-1, PR-2, PR-3; none in parallel.
- **B12, the acceptance check's force-push.** The throwaway branch of the
  acceptance check is rewritten once, with `--force-with-lease`, because
  the fix for a missing sign-off is a rebase; nothing else is ever
  force-pushed, and the branch is deleted by a maintainer afterwards.

## Acceptance

The acceptance boundary is three observations.

1. **An unsigned commit fails CI.** From PR-3's branch, the implementer
   cuts a throwaway branch `issue-N-unsigned`, makes one commit without
   `-s` (a line in a scratch file), pushes it and opens a draft PR: the
   `dco` job fails with the commit named and the fix in its log, and
   `required-checks` is red. Then `git rebase --signoff` on that branch,
   `git push --force-with-lease`: `dco` and `required-checks` green. Both
   run URLs go into PR-3's body and the `docs/features.md` entry; the
   throwaway PR is closed and a maintainer deletes its branch. A
   maintainer may repeat it from `main` after the merge; the first
   observation is enough for Done. PR-3's own run, every commit signed, is
   the green case on a real PR.
2. **The marks have a named owner.** `TRADEMARKS.md` and `NOTICE` both say
   RockSolid Labs, Inc., and a maintainer has approved the wording (the
   review approval on PR-2).
3. **The PR template carries the four rules.** The first PR opened after
   PR-1 merges (PR-2) shows the checklist with the four rule boxes and the
   sign-off line in its body.

Done means all three observed after the three PRs merge, never a ticked
list. Needs the owner: enabling private vulnerability reporting on the
repository before PR-2 merges; confirming the mailbox; approving the
trademark and security wording; the three merges; deleting the throwaway
branch; the status-log rows for O11 and O13 in the Program; and, outside
this item, the repository's description and topics and the `Owners` team's
visibility if wanted.
