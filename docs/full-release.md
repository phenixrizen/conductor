# Full release completion contract

**Status: In progress.** The release target is the complete coordinated engineering
platform. A review-only or single-agent pilot does not satisfy this target. This
contract records the user's direction on 2026-09-13 and the work still requiring
implementation and verification. It does not accept an ADR, merge a PR, deploy a
service, or grant application execution permissions.

## Required outcome

Multiple engineers and agents use the same authenticated workspace to discover
relationships across repositories, retrieve source and governing decisions, plan
dependent work, coordinate bounded coding attempts, inspect actual verification,
and publish reviewed changes to GitHub or GitLab. Each workspace selects one
tracker, Linear or Jira, synchronized with those shared records. Interrupted work
is recoverable without reconstructing a private agent conversation.

CodeGraph means the user-selected upstream
[colbymchenry/codegraph](https://github.com/colbymchenry/codegraph), including its
documented native Rust extraction where available. Other projects with the same
name are not substitutes. Each dependency must be pinned and tested before its
capabilities are described as implemented.

## Release gates

Every gate is required. Implementation and verification are recorded separately;
source fixtures do not establish live service compatibility.

| Gate | Required behavior | Current evidence |
|---|---|---|
| Shared review | Immutable revisions, independent exact approval, historical attribution, authenticated workspace/repository isolation | Implemented and tested through PR 14 |
| Shared source | Bounded pinned local and provider collection, receipts, cancellation and explicit attachment across interfaces | Implemented; live provider compatibility remains to verify |
| Repository relationships | Shared immutable graph spanning authorized repositories; dependency, symbol and impact queries; coverage and freshness limits visible | In progress |
| CodeGraph | Pinned selected upstream indexes controlled source and contributes provenance-bound graph evidence; native and fallback capabilities reported accurately | In progress |
| MCP | Real MCP protocol lets agents discover/read/author permitted Conductor work and query shared graph/run context; scope cannot be widened through tool arguments | In progress |
| Coordinated agents | Versioned task dependencies, explicit human execution authorization, concurrent independent tasks, overlap protection, assignments, cancellation and restart recovery | In progress |
| Coding adapters | Codex and Claude Code produce bounded patches in isolated workers without publication or production credentials | In progress |
| Verification | Executed commands and outputs bind to exact source/artifacts and acceptance criteria; missing, failed and unexecuted checks remain distinct | In progress |
| Delivery | Trusted publisher supports GitHub draft PRs and GitLab draft MRs, exact artifact authorization, checks/events and ambiguous-result reconciliation | Planned |
| Work tracking | Linear and Jira adapters, one chosen per workspace, explicit field ownership, duplicate/out-of-order recovery and visible sync conflicts | Planned |
| Specifications and decisions | Tested Spec Kit and ADRKit artifact/command capabilities; governing decisions and requirement/evidence links available to agents without inherited approval | Planned |
| Runtime context | Groundcover integration and cross-repository runtime evidence remain scoped, source-bound and separate from deployment authorization | Planned |
| Complete interfaces | Browser/CLI/TUI cover work authoring, graph inspection, coordination, verification, recovery and delivery/tracker state with useful role perspectives | Partial |
| Operations | Supported authenticated deployment, real identity/provider validation, remote Temporal configuration, backups/restore, observability, bounded capacity, CI and security acceptance | Partial |
| Complete acceptance | Multiple engineers and coordinated agents change related synthetic repositories, retain evidence, publish both provider review requests, synchronize the selected tracker and recover from failures | Planned |

## Stacked review order

The user explicitly authorized stacked PRs on 2026-09-13. Each PR targets its
immediate prerequisite branch, lists that dependency, and shows its own change
relative to that base. The first PR targets current `main`. Keep commits focused;
do not rewrite or force-push reviewed branches. After an earlier PR merges,
reconcile the remaining stack and retarget the next PR to `main`, verifying that
its diff still contains only the intended work. Publishing the stack is not
authorization to merge it.

The intended dependency sequence is repository knowledge → MCP → coordinated
execution and workers → verification and delivery → workflow/tracker integrations
→ complete interfaces and operational acceptance. Independent implementation may
run in parallel in separate worktrees. Actual PR links and merge dependencies are
recorded here as the tested changes become reviewable.

## Completion evidence

Run the applicable [repository checks](../AGENTS.md) for each behavioral change,
including live PostgreSQL, actual MCP clients, isolated worker execution, Chromium,
PTYs and owned process-restart acceptance. Live adapter tests use synthetic
repositories/accounts and scoped credentials. Record unavailable prerequisites
explicitly and continue independent work; never mark the complete release verified
while a required gate lacks its evidence. Design approval, produced implementation,
verified implementation, merged code, deployment and production outcome stay
separate throughout this contract.
