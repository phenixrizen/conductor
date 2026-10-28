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

The twelve articles are the Program's (section 2.1); their text is there,
not here. By number, what this item does to each. Articles 1 to 4 become
public as `CONTRIBUTING.md`'s open-core rules, in the wording of its
section 6 below.

1. Served: `CONTRIBUTING.md` states it as open-core rule 1, the PR
   template makes every PR tick it, and nothing in this item adds a gate
   of that kind.
2. Served: rule 2 and the template's second box; the build itself is
   untouched.
3. Served: rule 3 and the third box; `CONTRIBUTING.md` says paid work is
   built outside this repository.
4. Served: rule 4 and the fourth box; none of those code paths is touched.
5. Not touched: this item adds no import; the lint rule is item C1's.
6. Not touched: the `dco` job runs `python3` on the runner, no build and
   no tag.
7. Not touched: no interface exists yet; `CONTRIBUTING.md` gets a "Rules
   for code" section where item C7 adds its one line.
8. Not touched: no provider, nothing hosted, nothing to test.
9. Not touched: no Go change, no store.
10. Served: `AGENTS.md` stays the contract and gains the sign-off as one
    bullet of its own; `CONTRIBUTING.md` keeps it in one section and the
    PR template in one line.
11. Served: the new files say Conductor, Yardmaster, Brakeman and
    Switchman as `AGENTS.md` defines them, call a worktree a worktree,
    and `TRADEMARKS.md` covers the Conductor name.
12. Not touched: no process, check or attempt is started or changed by
    documents, a workflow job and a script that reads `git log` on a CI
    runner (the Boxcar table below).

## Why and what happens

Conductor is open source under Apache-2.0 and is about to take
contributions from outside RockSolid Labs while the paid product grows in
private code beside it. The repository has `LICENSE`, a `NOTICE` that calls
the Conductor marks "the project's marks" (NOTICE:10-11), an `AGENTS.md`
written for the owner's own agents that says "the owner" five times
(lines 40, 64, 67, 79, 85), a PR template with no checklist
(`.github/pull_request_template.md`, nine lines), and no `CONTRIBUTING.md`,
`SECURITY.md`, `TRADEMARKS.md` or DCO. Of the 1,196 non-merge commits on
`main`, 22 carry a `Signed-off-by` line, all of them the `docs:` and
`workflow:` commits of 2026-10-17 (373df2eb among them, signed off by its
author); none of the twenty most recent has one, and no merge commit does.
CI's one required status check is `required-checks` (ruleset
25591653 on `main`; `ci.yml:257-264`), green when every job in its
`needs` list passed.

After this item:

- A contributor finds `CONTRIBUTING.md`: `AGENTS.md` for how an issue is
  worked, and the fork path for a contributor without write access,
  `docs/checks.md` for the checks, the sign-off on every commit with the
  fix for a branch that lacks it, the four open-core rules, where paid
  work goes instead, the PR checklist, and who the maintainers are.
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

Two pull requests; by file. Nothing here touches a Go package, the web
app, the desktop shell, the protocol, a store or a schema, so there is no
migration and no new frame; no dependency is added, so `make notices` has
nothing to regenerate. Names: the application is Conductor; Yardmaster is
its agent harness; a worktree is a worktree.

### `CONTRIBUTING.md` (new, repository root; PR-1; section 9 extended by PR-2)

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
   comment answered, fixed or declined with why. Never two open issues at
   once. The `ready` label is the go-ahead for everyone. With write access
   (the maintainers and the agents they run), `AGENTS.md`'s bullets apply
   as written: assign yourself, cut the branch on origin with
   `gh issue develop` (the branch on origin is the claim), keep the Project
   board current. Without it, which is every contributor outside RockSolid
   Labs at first: say on the issue that you are taking it, and a
   maintainer assigns it to you (the assignment is the claim) and moves it
   on the board for you; fork the repository, cut `issue-N` from `main`
   (your fork's, brought up to date with this repository's), and open the
   PR from the fork, where CI runs as on any other PR, the `dco` job
   included (a first-time contributor's first run may wait for a
   maintainer's approval, as GitHub has it); handing off, say so in a PR
   comment and a maintainer unassigns you.
4. **Sign-off.** Every commit is signed off: `git commit -s` adds
   `Signed-off-by: Your Name <you@example.org>`, and by adding it you
   certify the Developer Certificate of Origin, whose text is in `DCO` at
   the root (in short: you wrote the change or have the right to submit it
   under this repository's licence, and you know the contribution and your
   sign-off are public and kept). The name and address must be the commit
   author's, as `git config user.name` and `user.email` give them. There
   is no contributor licence agreement. CI's `dco` job fails a pull request
   with a commit that lacks a matching sign-off, naming the commit. The
   fix for a branch of your own commits with no merge commit on it:
   `git rebase --signoff origin/main`, then `git push --force-with-lease`;
   for the last commit alone, `git commit --amend -s --no-edit` (a merge
   at the tip too: amending keeps its parents and its tree). The base is
   named on purpose: a bare `git rebase --signoff` rebases onto the
   branch's own upstream, which already holds the commits, and signs
   nothing. Who runs the fix matters: `git rebase --signoff` and
   `git commit -s` sign as the person running them (`user.name`,
   `user.email`), whatever the commit's author, so on someone else's
   commit they add a sign-off that is not the author's and the check
   still fails. A commit by someone else is theirs to sign: the author
   runs the fix on the branch and pushes it, or writes the line,
   `Signed-off-by: Name <address>` as their commits name them, on the
   pull request, and whoever repairs the branch adds it to that commit's
   message (`git rebase -i origin/main`, `reword`). Check before pushing:
   `python3 scripts/dco_check.py --base origin/main --head HEAD`
   (`make preflight` runs it first). A merge commit on the branch is a
   commit of the pull request and is checked like any other
   (`git merge --signoff` signs one; GitHub's "Update branch" button makes
   one nobody signed), so rebase instead of merging. A branch that has a
   merge commit anyway is repaired without losing what the merge carried:
   a rebase drops the merge and replays the commits of both sides, and
   what the merge held of its own goes with it; a conflict it resolved
   comes back during the replay and stops the rebase to be resolved
   again, an edit made in the merge itself is dropped without a word
   (`--rebase-merges` is no help: it makes the merge again without that
   edit and without a sign-off). So: note the tip,
   `before=$(git rev-parse HEAD)`; rebase in place,
   `git rebase --signoff $(git merge-base origin/main HEAD)`; compare,
   `git diff --stat $before HEAD`, which prints nothing when nothing was
   lost; otherwise `git restore --source=$before --staged --worktree :/`
   and `git commit -s` put back what the diff showed as a commit of its
   own, and the diff is empty. Then the check above, and the push with
   `--force-with-lease`. An agent's commits are
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
9. **Licence.** Contributions are under the Apache License 2.0
   (`LICENSE`), no copyright assignment. PR-2 extends the section to
   "Licence, marks, security": the marks are `TRADEMARKS.md`'s; a
   vulnerability is `SECURITY.md`'s and never an issue. PR-1 names
   neither file, since neither exists until PR-2 merges.

About 190 lines. The company is "RockSolid Labs"; the entity
"RockSolid Labs, Inc."

### `DCO` (new, repository root; PR-1)

The Developer Certificate of Origin, version 1.1, verbatim from
developercertificate.org, including its own notice (verbatim copies are
permitted, changes are not). It is not under Apache-2.0 and says so itself;
`scripts/notices.py` reads Go modules and npm packages, never root files,
so `THIRD_PARTY_NOTICES` is unchanged.

### `AGENTS.md` (edited; PR-1, one pointer by PR-2)

- A new bullet in the top list, after the dependency bullet: every commit
  is signed off (`git commit -s`; the Developer Certificate of Origin in
  `DCO`; the sign-off's name and address the commit author's); CI's `dco`
  job fails a pull request with a commit that lacks it;
  `git rebase --signoff origin/main` fixes a branch of your own commits;
  `CONTRIBUTING.md` says how, and how a merge commit or another author's
  commit is repaired. `CLAUDE.md` imports `AGENTS.md`, so every agent
  reads it.
- "The owner" becomes the role, at its five sites: line 40, "The owner has
  said this twice" becomes "The maintainers have said this twice";
  line 64, "The owner merges" becomes "A maintainer merges"; line 67,
  "Work the owner asks for" becomes "Work a maintainer asks for";
  line 79, "the owner decides when" becomes "a maintainer decides when";
  line 85, "until the owner decides on disclosure" becomes "until a
  maintainer decides on disclosure"; PR-2 appends "(`SECURITY.md`)" to
  that bullet once the file exists.
- Two sentences in the "Working an issue" introduction. The first defines
  the role: the maintainers are the owners of the `rocksolidlabs` GitHub
  organization (`CONTRIBUTING.md`, Who decides); they merge and decide.
  The second says whose the assignment, branch-on-origin and Project
  board bullets are: the maintainers' and the agents they run, who have
  write access; a contributor without it follows `CONTRIBUTING.md`'s fork
  path, and a maintainer assigns the issue and moves the board for them.
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

### `.github/ISSUE_TEMPLATE/task.md` (edited; PR-2)

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
6. **A fork.** Rename it and drop the marks. Where the images are:
   `web/public/brand/` (the SVGs); `web/public/sponsor/` and
   `desktop/static/sponsor/` (the sponsor PNGs); `desktop/build/`, the
   committed rasters the desktop packages carry (`desktop/electron-builder.yml`):
   `icon.png`, `icon-512.png` and `tray.png`, rendered from the app icon
   by `scripts/brand_assets.py --desktop-icons`, and the Windows
   installer's `installerSidebar.bmp` and `installerHeader.bmp`, rendered
   from `scripts/installer-art.html` by `scripts/installer-art.mjs`. Where
   the name and the credit are written in: `web/app/utils/about.ts` (the
   credit and the sponsor's name and site),
   `web/app/components/AboutCard.vue`, `web/app/pages/join/[token].vue`,
   `web/app/layouts/default.vue` (the sidebar's name),
   `desktop/src/splash.ts` and `desktop/static/splash.html`,
   `internal/cli/root.go` (`Credit`), `internal/api/switchyard_page.go`
   (the header's marks, the footer's badge), `desktop/electron-builder.yml`
   (`productName`, `copyright`, the invite protocol's name),
   `web/nuxt.config.ts` (the title), `README.md`'s footer,
   `docs/design/brand.md`. That is where to start, not the whole of it:
   the name is in page titles, messages and documentation too, and
   `git grep -w Conductor` finds every site; what a renamed build then
   shows is the fork's to check.
7. **The spelling.** "Conductor"; "RockSolid Labs"; "RockSolid Labs,
   Inc." for the legal entity; the site rocksolidlabs.io; the GitHub
   organization `rocksolidlabs`.
8. **Questions.** conductor@rocksolidlabs.io.

About 100 lines. `docs/design/brand.md` keeps the rule that the junction is
artwork and never a status light (`AGENTS.md`); `TRADEMARKS.md` is about
who may use the marks, not how the project draws them. The file says
nothing about the railroad names of Conductor's parts (Yardmaster,
Brakeman, Switchman, the Switchyard, the Yard, the Roundhouse): neither
claimed nor disclaimed (B8).

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
   candidates get no fixes. The desktop app updates itself on macOS, on
   Windows and as an AppImage (`desktop/src/updater.ts`, `updateChannel`);
   a deb or rpm install only points at the release page, and the
   `conductor` binary, the packages and the image are updated by whoever
   installed them: install the latest release.
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

### `scripts/dco_check.py` (new; PR-1)

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
- `problems(commits) -> list[str]`: for every commit, merge commits
  included, unless one of its sign-offs has the author's name (trimmed,
  exact) and the author's address (compared without regard to case), one
  line: the first twelve characters of the sha, the subject (a merge
  commit marked `(merge)`), the author, and either `no Signed-off-by` or
  the sign-offs found with `(not the author)`. A merge on the branch is a
  commit of the pull request and may carry a conflict's resolution, so it
  is judged like any other; the DCO GitHub App skips merges, this check
  does not. The committer is not consulted: a maintainer's rebase keeps
  the author's sign-off. No bot exemption.
- `read(repo, base, head) -> list[Commit]`: one
  `git -C repo log --reverse --format=%x1e%H%x1f%P%x1f%an%x1f%ae%x1f%s%x1f%B base..head`,
  read as UTF-8 whatever the locale (the lesson of
  `release_draft_test.py`'s `Gh` test), split on the record and unit
  separators; a record that does not split into six parts (a message
  holding those bytes) fails the read. The script never passes a shell
  string.
- `main(argv)`: exit 0 and `N commits signed off by their authors` (or
  `no commits between base and head`); exit 1 with one line per problem and
  a last line that counts and names the fix: when no commit in the range
  is a merge, `K of N commits are not signed off by their author. Fix:
  git rebase --signoff <base> && git push --force-with-lease, run by each
  commit's author (CONTRIBUTING.md, Sign-off).`, the rebase that signs a
  linear branch; when one is, `K of N commits are not signed off by their
  author. The branch has a merge commit: CONTRIBUTING.md, Sign-off, says
  how to sign it without losing what the merge carried.`, because a plain
  rebase drops a merge with the edit it may hold of its own; exit 2 when
  git fails or a record does not parse, with git's message. Never
  silently green.

About 120 lines.

### `scripts/dco_check_test.py` (new; PR-1)

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
- `test_a_merge_commit_is_judged_like_any_other` (an unsigned
  two-parent commit is named, marked as a merge; one signed off by its
  author passes).
- `test_an_empty_range_passes`.
- `test_the_report_counts_the_commits_and_names_the_fix` (the rebase line
  for a range without a merge; the merge line when the range holds one).
- `test_a_range_is_read_from_a_repository_as_git_logs_it`: a repository
  made in a temporary directory with `git init -q`: a base commit; on
  `main` one commit signed off and one not; then a `--no-ff` merge of a
  side branch holding one signed-off commit, the merge itself unsigned
  and carrying nothing of its own.
  `main([... "--base", <base sha>, "--head", "HEAD"])` returns 1 naming
  the unsigned commit and the merge, nothing else. Then the fix the
  script prints for a linear branch, `git rebase --signoff <base sha>`,
  is run in that repository, and the same base and `HEAD` return 0 with
  three commits counted: the rebase replays the three non-merge commits,
  adds the sign-off where it was missing and drops the merge; and the
  tree is the same before and after (`git rev-parse HEAD^{tree}` read
  before the rebase and after it), the proof that this merge carried
  nothing. (`git commit --amend -s` would not do here: it rewrites
  `HEAD` only, and the unsigned commit is below the merge.) The git
  calls carry `-c user.name`, `-c user.email`, `-c commit.gpgsign=false`,
  `-c init.defaultBranch=main` and run with `GIT_CONFIG_GLOBAL=/dev/null`
  and `GIT_CONFIG_NOSYSTEM=1`, so a developer's global configuration (a
  signing key, a commit template, a hooks path) cannot make the test fail
  or pass for its own reasons; the two tests below are made the same way.
- `test_a_merge_that_carried_its_own_edit_is_repaired_without_losing_it`:
  the same repository, but the merge is made with `--no-commit`, a file
  in neither parent is added before it is committed, and it is the only
  unsigned commit. `main()` returns 1 naming the merge. The tip and its
  tree are recorded; `git rebase --signoff <base sha>` returns 0 for the
  check with three commits counted, but the tree is not the recorded
  one and `git diff --stat <tip before> HEAD` names the file: the loss
  the script's merge line and `CONTRIBUTING.md` warn of, which a count
  of commits and sign-offs cannot see. Then the recipe:
  `git restore --source=<tip before> --staged --worktree :/` and
  `git commit -s`; the tree equals the recorded one again and the check
  returns 0 with four commits counted. (Observed with git 2.50.1 while
  planning: the plain rebase drops the file; `--rebase-merges` makes the
  merge again without the file and without a sign-off; amending a merge
  at the tip keeps its parents and its tree; `git restore --source`
  removes a file the source lacks, which `git checkout <rev> -- .` would
  not, so the restored tree is exact.)
- `test_the_fix_signs_as_the_person_running_it_so_each_author_signs_their_own`:
  a repository with a base commit and, on `main`, one commit by Alice
  signed off and one by Bob without a sign-off (the identity set per
  commit with `-c user.name` and `-c user.email`). `main()` returns 1
  naming Bob's commit. `git rebase --signoff <base sha>` run as Alice
  returns 1 still: Bob's commit now carries Alice's sign-off, named
  `(not the author)`, since git signs as the committer and keeps the
  author. Run as Bob, it returns 0 with two commits counted: Bob's
  commit carries his own line beside Alice's, and Alice's commit keeps
  hers. This is why `CONTRIBUTING.md` says the author runs the fix.
- `test_a_record_that_does_not_parse_fails_the_read` (exit 2).

### `.github/workflows/ci.yml` (edited; PR-1)

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

### `scripts/preflight.py` and `scripts/preflight_test.py` (edited; PR-1)

`LINUX_STEPS` gains, first, `{"name": "dco", "covers": "dco", "argv":
["python3", "scripts/dco_check.py", "--base", "origin/main", "--head",
"HEAD"]}`: it takes a second, and the rest of the run takes an hour, so an
unsigned commit is the first line of the receipt. Without the step the
existing `test_every_ci_job_has_a_linux_step` (`preflight_test.py:99`)
fails on the new job: that test is why the step cannot be forgotten.
`preflight_test.py` gains `test_the_dco_check_runs_first_against_origin_main`
(the first Linux step's name and argv). The Mac and Windows rows do not
get it: what a commit carries does not depend on the machine.

### `docs/checks.md` (edited; PR-1)

The code block gains
`python3 scripts/dco_check.py --base origin/main --head HEAD  # every commit of the branch signed off by its author (CI's dco job)`,
and the scripts-suite line's "(notices.py)" becomes the four scripts.

### `docs/repository-map.md` (edited; PR-1, PR-2)

One row in the table, added by PR-1 and extended by PR-2:
`CONTRIBUTING.md`, `DCO`, `TRADEMARKS.md`, `SECURITY.md`, `NOTICE`,
`scripts/dco_check.py`: how to contribute (the sign-off, the open-core
rules, the checklist, the maintainers), the certificate's text, the marks
and their owner, how to report a vulnerability, and the check CI's `dco`
job runs.

### `docs/features.md` (edited; PR-2, the last)

One entry, where the newest entries go (before the entry for the move,
#380, 2026-10-27), recording what shipped and what was observed: the
three runs of the acceptance check with their URLs, the decisions below,
and the date.

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
- The invariant: every commit in that set, merge commits included,
  carries a `Signed-off-by` whose name is the author's and whose address
  is the author's, compared without regard to case. The committer is not
  consulted. No author is exempt, bots included; the first bot adopted
  brings its exemption in its own PR. A merge in the set is the
  contributor's own (GitHub's merge commits are on `main`, outside it);
  a branch rebased as `CONTRIBUTING.md` says has none.
- The workflow that runs for a pull request is the one at the PR's merge
  commit, so the PR that adds the job is checked by it, and so is every
  PR opened or pushed after PR-1 merges. A PR open before that is not,
  until it runs again: `ci.yml` runs on `pull_request`'s default events
  (`opened`, `synchronize`, `reopened`; `ci.yml:6`), PR-1's merge is a
  push to `main` that fires none of them, and the ruleset does not
  require a branch to be up to date before it merges
  (`strict_required_status_checks_policy: false` on ruleset 25591653),
  so such a PR keeps the green `required-checks` of a run that had no
  `dco` job and could merge unsigned. The cutover closes that gap: right
  after PR-1 merges and before any other PR does, a maintainer lists the
  open PRs (`gh pr list --state open`) and closes and reopens each
  (`gh pr close N && gh pr reopen N`); `reopened` makes a new merge
  commit against the current `main`, whose `ci.yml` has the job, and the
  new run's `required-checks` needs it. A re-run of the old run would
  not do: a re-run keeps the original event's commit and with it the old
  `ci.yml`. A PR that no longer merges cleanly gets its run when its
  branch is updated, which it needs before merging anyway. History is
  never re-checked: the commits on `main` before the job carry no
  sign-off and never will; the rule is not retroactive.
- The words and the check land in one merge (PR-1): nobody meets the
  check before the rule is written, `AGENTS.md` never states a check that
  does not run, and the template's sign-off line and the `dco` job meet a
  contributor on the same PR. PR-1 names no file PR-2 adds.
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
- A rename rehearsal (building a fork under another name and checking
  what it shows): the fork's work; `TRADEMARKS.md` says where to look.
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
  (`git ls-files` for each; it passes on each PR as cut, since no PR
  names a file the other adds); `DCO` diffed against developercertificate.org
  with no difference; `git grep -i 'rock solid'` and
  `git grep -i 'rocksolid labs' | grep -v 'RockSolid Labs'` find nothing;
  `wc -l NOTICE` is 12; the line-number sites in `AGENTS.md` and `NOTICE`
  named above are the ones edited.
- **`.github` templates** (PR-1, PR-2): no runner; read twice; the next PR
  opened shows the template and ticks it (PR-2's body is the first).
- **`.github/workflows/ci.yml`** (PR-1): no runner; read twice; the scripts
  suite (`preflight_test.py`'s job-coverage test fails when a job has no
  preflight step); the PR's own CI run shows the `dco` job green and
  `required-checks` listing it among its needs; the first throwaway PR
  (below) shows it red with the commit named, then green; after the
  merge, the cutover's reopened runs show it among `required-checks`'
  needs on every PR open at the merge, red on the second throwaway with
  its commit named.
- **Scripts** (PR-1: `scripts/dco_check.py`, `scripts/preflight.py` and
  the two tests): `python3 -m unittest discover -s scripts -p '*_test.py'`
  before every push, with every test named above; CI's `go` job runs the
  same suite (`ci.yml:78`); `python3 scripts/preflight.py --only dco` once,
  to see the step run here (a partial receipt, as expected).
- **Go, web, desktop**: not affected by any PR; reported as not run.
- **By hand, what no test reaches**: the acceptance check (below), the
  cutover among it; the private-vulnerability-reporting setting read back
  before PR-2 merges;
  the mailbox conductor@rocksolidlabs.io confirmed by a maintainer.

## Boxcar constraints

The eight constraints are the Program's (section 2.5); their text is
there, not here. By number, none is touched:

1. Nothing of Yardmaster's is started or changed: no attempt, run or
   process; the `dco` job is CI tooling on a runner, like `notices.py`
   and `brand_assets.py`.
2. No session code is touched.
3. No task file, worktree or `os.Root` is touched.
4. No hook, MCP or token path is touched.
5. `dco_check.py` is not a task check; it runs `git` through `subprocess`
   with an argv list on the CI runner, as the other scripts in `scripts/`
   do.
6. No build tag, capability or UI is touched.
7. No ledger row is touched.
8. Neither `internal/api` nor `internal/hostagent` is touched.

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

Two pull requests, in this order, each cut from `main` on its own
`issue-N` branch once the plan is cut into issues, each green on its own,
each passing the path check as cut (no PR names a file the other adds),
each one sitting. They share the repository root, `.github/` and `docs/`,
so the second waits for the first.

### PR-1: Contributing under the DCO: `CONTRIBUTING.md`, the `DCO` text, the maintainer role and the fork path in `AGENTS.md`, the PR template, `scripts/dco_check.py` and its tests, the `dco` job behind `required-checks`, the preflight step, `docs/checks.md`

- Contains: `CONTRIBUTING.md` (sections 1 to 8, and section 9's licence
  sentence) and `DCO` (new); `AGENTS.md` (the sign-off bullet, the role's
  five sites, the two sentences defining the role and the fork path, the
  checklist in the draft-PR bullet); `.github/pull_request_template.md`
  (the checklist and the sign-off line); `scripts/dco_check.py` and
  `scripts/dco_check_test.py` (new); `scripts/preflight.py` and
  `scripts/preflight_test.py`; `.github/workflows/ci.yml` (the job and the
  `needs` list); `docs/checks.md`; `docs/repository-map.md` (the row, with
  the files this PR adds).
- Touches: the repository root (`CONTRIBUTING.md`, `DCO`, `AGENTS.md`),
  `.github` (the PR template, the workflow), `scripts`, `docs`
  (`checks.md`, `repository-map.md`).
- Tests: the scripts suite with the thirteen tests named above and the
  preflight test; `python3 scripts/preflight.py --only dco`; `ci.yml` read
  twice; the PR's own CI run, every commit signed, the green case on a
  real PR; the two throwaway PRs of the acceptance check, the first cut
  from this branch with its two run URLs in the PR body, the second cut
  from `main` and left open for the cutover; by hand, the path check, the
  `DCO` diff, the spelling grep.
- Lands with: the cutover (Events, orderings and invariants), run by a
  maintainer right after the merge and before any other, or by the
  implementer with a maintainer's go-ahead; the acceptance's fourth
  observation is its proof.
- Depends on: nothing. It names no file PR-2 adds: `TRADEMARKS.md` and
  `SECURITY.md` are PR-2's words. Parallel with: none.
- About 650 changed lines, most of them the script and its tests; one
  sitting.

### PR-2: The marks and security: `TRADEMARKS.md`, `SECURITY.md`, `NOTICE`'s owner line, the brand guide's pointer, the vulnerability contact link, the pointers in `CONTRIBUTING.md`, `AGENTS.md` and the issue template, the `docs/features.md` entry

- Contains: `TRADEMARKS.md` and `SECURITY.md` (new); `NOTICE` lines 8-12;
  `docs/design/brand.md` (one sentence);
  `.github/ISSUE_TEMPLATE/config.yml` (new); `CONTRIBUTING.md` (section 9
  extended with the marks and security sentences); `AGENTS.md` (the
  `SECURITY.md` pointer in the vulnerabilities bullet);
  `.github/ISSUE_TEMPLATE/task.md` (the pointer); `docs/repository-map.md`
  (the row extended); `docs/features.md` (the entry for the whole item,
  with the acceptance check's three run URLs).
- Touches: the repository root (`TRADEMARKS.md`, `SECURITY.md`, `NOTICE`,
  `CONTRIBUTING.md`, `AGENTS.md`), `.github` (`ISSUE_TEMPLATE`), `docs`
  (`design/brand.md`, `repository-map.md`, `features.md`).
- Tests: none apply; by hand, `wc -l NOTICE`, the path check, the
  spelling grep, the private-vulnerability-reporting setting read back
  (`enabled: true`) and the advisory link opened as a non-member once the
  setting is on. Its own body is the first to show PR-1's template.
- Depends on: PR-1 (the files its pointers name, the repository-map row it
  extends, the template its body ticks). Parallel with: none.
- Needs a maintainer before it merges: the setting on, the mailbox
  confirmed, the wording of `TRADEMARKS.md` and `SECURITY.md` approved
  (the review approval is that).
- About 220 changed lines.

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
  doubled; the App skips merge commits, which the job does not (B3), and
  that difference comes with the choice.
- **B1, the named role.** "Maintainer": an owner of the `rocksolidlabs`
  GitHub organization. Two accounts hold it today, and the org's `Owners`
  team (secret) mirrors them; no team is created and no setting changes.
  Overturn by naming a team in `CONTRIBUTING.md`'s "Who decides".
- **B2, where the certificate's text lives.** `DCO` at the root, verbatim
  1.1, so what a sign-off certifies is in the repository and readable
  offline; `CONTRIBUTING.md` paraphrases and points.
- **B3, what a sign-off must match.** The commit author's name (exact,
  trimmed) and address (case ignored); merge commits included, since a
  merge on the branch is a commit of the pull request and the requirement
  exempts none (the DCO GitHub App's exemption of merges is the App's, not
  the requirement's); the committer not consulted; no bot exemption until
  a bot is adopted. The sign-off is the author's own line: the fix is run
  by the author, or the author's line, given on the pull request, is
  added to the commit by hand; nobody signs for someone else.
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
  junction mark and logos, and the RockSolid Labs marks, and says nothing
  about the railroad names of Conductor's parts (Yardmaster, Brakeman,
  Switchman, the Switchyard, the Yard, the Roundhouse): neither claimed
  nor disclaimed. Silence is the default because a published disclaimer
  is relied on and no later status-log row takes it back, and the names
  may yet name products or services (the Switchyard already names the
  hosted service RockSolid Labs runs); claiming or disclaiming any of
  them is a decision the owner takes before PR-2, not a default of this
  plan.
- **B9, the README.** Left to stream A, which the Program already charges
  with its badges and links; its footer stays true through `NOTICE`.
- **B10, not retroactive.** No commit on `main` is rewritten or re-signed.
- **B11, two PRs in order.** PR-1 (the words and the check together, so
  nothing documented is unenforced between merges), the cutover, then
  PR-2; none in parallel.
- **B12, the acceptance check's force-push.** The first throwaway branch
  of the acceptance check is rewritten once, with `--force-with-lease`,
  because the fix for a missing sign-off is a rebase; the second is
  closed as it is; nothing else is ever force-pushed, and both branches
  are deleted by a maintainer afterwards.
- **B13, the fork path.** A contributor without write access forks,
  claims an issue by a comment, is assigned by a maintainer (the
  assignment is the claim) who moves the board for them, and opens the PR
  from the fork; `AGENTS.md`'s assignment, branch-on-origin and board
  bullets are the maintainers' and their agents'. No team, label or
  setting is added for it.
- **B14, the cutover.** The gap for PRs open at PR-1's merge is closed
  by a procedure, not a setting: a maintainer closes and reopens every
  open PR right after the merge and before any other (Events, orderings
  and invariants), and the acceptance's fourth observation proves it on
  a throwaway opened before the merge. The alternative is the ruleset's
  "require branches to be up to date before merging"
  (`strict_required_status_checks_policy`), which makes a stale result
  worthless for good; it is the owner's setting, outside this item, and
  it changes every merge: each PR would be brought up to date first, by a
  rebase, since the button's merge commit is one nobody signed. The owner
  may choose it with a status-log row; the cutover is then not needed
  and the fourth observation still holds.

## Acceptance

The acceptance boundary is three observations; the cutover adds a
fourth that the first needs.

1. **An unsigned commit fails CI.** From PR-1's branch, the implementer
   cuts a throwaway branch `issue-N-unsigned`, makes one commit without
   `-s` (a line in a scratch file), pushes it and opens a draft PR: the
   `dco` job fails with the commit named and the fix in its log, and
   `required-checks` is red. Then the fix as the script prints it, with
   its base: `git rebase --signoff origin/main` on that branch (the PR's
   base, the range CI checks; a bare `git rebase --signoff` would rebase
   onto the branch's upstream, `origin/issue-N-unsigned`, which already
   holds the commit, and sign nothing), the same range checked locally,
   `python3 scripts/dco_check.py --base origin/main --head HEAD`
   returning 0, then `git push --force-with-lease`: `dco` and
   `required-checks` green. Both run URLs go into PR-1's body and the
   `docs/features.md` entry (PR-2); the throwaway PR is closed and a
   maintainer deletes its branch. PR-1's own run, every commit signed, is
   the green case on a real PR.
2. **The marks have a named owner.** `TRADEMARKS.md` and `NOTICE` both say
   RockSolid Labs, Inc., and a maintainer has approved the wording (the
   review approval on PR-2).
3. **The PR template carries the four rules.** The first PR opened after
   PR-1 merges (PR-2) shows the checklist with the four rule boxes and the
   sign-off line in its body.
4. **A PR open before the job merged runs it before it merges.** Before
   PR-1 merges, the implementer cuts a second throwaway branch
   `issue-N-before` from `main`, which has no `dco` job, makes one commit
   without `-s`, pushes it and opens a draft PR: its run is green with no
   `dco` job in it, the gap the cutover closes. Right after PR-1 merges,
   the cutover runs (Events, orderings and invariants): every open PR
   closed and reopened, the throwaway among them. Its new run shows `dco`
   red with the commit named and `required-checks` red; that run's URL
   goes into the `docs/features.md` entry. Every other PR open at the
   merge shows a new run with `dco` among `required-checks`' needs before
   it merges. Then the throwaway PR is closed as it is and a maintainer
   deletes its branch.

Done means all four observed after the two PRs merge, never a ticked
list. Needs the owner: enabling private vulnerability reporting on the
repository before PR-2 merges; confirming the mailbox; approving the
trademark and security wording; the two merges, with the cutover right
after the first (or the go-ahead for the implementer to run it); deleting
the two throwaway branches; the status-log rows for O11 and O13 in the
Program; and, outside this item, the repository's description and topics
and the `Owners` team's visibility if wanted.
