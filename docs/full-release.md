# Full release completion contract

**Status: Implementation produced; review and merge pending.** The release target
is the complete coordinated engineering platform. This contract records the user's
direction on 2026-09-13, the implemented capabilities and their qualification limits.
A review-only or single-agent pilot does not satisfy the target. Local verification,
hosted CI, merge, deployment and production outcomes are separate facts; this
contract grants none of those permissions.

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

| Gate | Implemented behavior and verification | Remaining qualification |
|---|---|---|
| Shared review | Immutable revisions, exact independent approval, historical attribution and server-owned workspace/repository isolation; signed API, Chromium, PTY and live PostgreSQL tests | Release changes awaiting merge |
| Shared source | Local and GitHub/GitLab pinned collection, selected artifacts, whole-repository bundles, receipts, cancellation and exact attachment; real Git/HTTP/Temporal recovery; read-only live GitHub acquisition exercised | Live GitLab account qualification |
| Repository relationships | Immutable shared graph spanning all authorized sources; dependency, symbol, impact and exact source reads through API/MCP/browser/terminal; grant-revocation tests | Extraction coverage remains explicit and bounded |
| CodeGraph | Selected upstream 1.6.0, source `dfccdf62547fcd76d343344d823a0e1998d3a89f`, native Rust kernel 0.1.0/ABI 2; actual isolated extraction and cross-repository dependency gate pass | No claim of complete semantic understanding |
| MCP | Actual SDK stdio clients discover/read/author scoped work, graph/run/artifact/delivery/tracker/runtime context; exact inputs and agent authority enforced | Host configuration documented separately |
| Coordinated agents | Immutable DAG plans, source/profile/image pins, exact human execution authority, concurrency, write claims, cumulative patches, cancellation and restart reconciliation; actual compiled executor/Temporal/Docker gate passes | Paid model inference unverified |
| Coding adapters | Pinned Codex/Claude producers, isolated source/check containers and restricted credential gateway; native CLI startup/protocol and offline command execution tested | Live paid inference unverified |
| Verification | Exact approved criterion links survive admission, independent Docker checks, receipt retention, publication and MCP/browser/terminal review; derived supported/not_verified coverage preserves historical artifact digests; full release gate verifies linked and unlinked criteria | Evidence proves its declared checks only |
| Delivery | Trusted GitHub/GitLab draft publication and check/merge/deployment observations; actual Git result trees and HTTP adapters reconcile lost writes without duplicate drafts | Live SaaS write accounts and application deployment unverified |
| Work tracking | Linear/Jira adapters, one selected per workspace, exact package/publication-receipt links, field ownership, conflict resolution and lost-write recovery; both provider choices pass complete acceptance | Existing tickets only; live SaaS write qualification unverified |
| Specifications and decisions | Spec Kit 1.0.6 and ADRKit CLI 0.13.0 native commands in an immutable isolated image; actual scaffold/template/prerequisite and Proposed ADR/lint/applicability/graph verification | Broad agentic prompt/extension compatibility is not claimed |
| Runtime context | Groundcover REST profile pinned to official SDK schema 1.424.0; exact service/environment/commit/window correlation, approved criteria, complete-grid evaluation, shared retention and recovery | Live Groundcover account and overall production outcome unverified |
| Complete interfaces | Browser/CLI/TUI/MCP review, graph/source, coordination, artifact, delivery, tracker and runtime paths tested; accessible workflow navigation preserves uncertain inputs across tabs; signed Chromium, real Keycloak and authenticated PTY acceptance pass | Release changes awaiting merge |
| Operations | Verified remote Temporal TLS/mTLS; actual Keycloak 26.7.3 HTTPS browser qualification; checked migration ledger, private diagnostics, actual PostgreSQL backup/restore, process recovery and identical release archives | [Current hosted CI](https://github.com/phenixrizen/conductor/actions/workflows/verify.yml); hosted Temporal and Conductor deployment unverified |
| Complete acceptance | Both Linear/Jira variants pass two-source native graph → signed MCP → three-task Docker DAG → retained Temporal restart → exact GitHub/GitLab drafts → tracker reconciliation → correlated runtime criteria; related-source revocation tested | Controlled provider fixtures establish protocol behavior, not live SaaS or paid inference |

See [complete acceptance](operations/full-release-acceptance.md),
[provider sign-in qualification](operations/keycloak-qualification.md),
[release operations](operations/release.md), and each feature plan for reproducible
commands and the exact distinction between implemented, verified and external
qualification. New ADRs remain Proposed. Nothing in this matrix grants deployment
or merges the review stack.

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

### Published PR dependencies

Review and merge in this order. Each PR targets the immediately preceding branch;
this keeps its own diff reviewable. Do not skip prerequisites.

| Order | PR | Capability |
|---|---|---|
| 1 | [#15](https://github.com/phenixrizen/conductor/pull/15) | Authenticated shared MCP |
| 2 | [#16](https://github.com/phenixrizen/conductor/pull/16) | Shared repository graph and native CodeGraph |
| 3 | [#17](https://github.com/phenixrizen/conductor/pull/17) | Isolated coding workers and independent checks |
| 4 | [#18](https://github.com/phenixrizen/conductor/pull/18) | Whole-repository source and graph workbench |
| 5 | [#19](https://github.com/phenixrizen/conductor/pull/19) | Durable coordinated agents and exact execution authority |
| 6 | [#20](https://github.com/phenixrizen/conductor/pull/20) | Trusted GitHub/GitLab publication and browser review |
| 7 | [#21](https://github.com/phenixrizen/conductor/pull/21) | Exact related-repository source inspection |
| 8 | [#22](https://github.com/phenixrizen/conductor/pull/22) | Linear/Jira workspace synchronization and browser review |
| 9 | [#23](https://github.com/phenixrizen/conductor/pull/23) | Complete authenticated release CLI/TUI |
| 10 | [#24](https://github.com/phenixrizen/conductor/pull/24) | Complete retained task reports and patch inspection |
| 11 | [#25](https://github.com/phenixrizen/conductor/pull/25) | Native Spec Kit and ADRKit commands |
| 12 | [#26](https://github.com/phenixrizen/conductor/pull/26) | Scoped runtime evidence across all clients |
| 13 | [#27](https://github.com/phenixrizen/conductor/pull/27) | Shared verified Temporal TLS/mTLS |
| 14 | [#28](https://github.com/phenixrizen/conductor/pull/28) | Reproducible releases, database recovery and private diagnostics |
| 15 | [#29](https://github.com/phenixrizen/conductor/pull/29) | Actual Keycloak HTTPS sign-in qualification |
| 16 | [#30](https://github.com/phenixrizen/conductor/pull/30) | Complete cross-repository acceptance and safe activity retries |
| 17 | [#31](https://github.com/phenixrizen/conductor/pull/31) | Accessible browser workflow navigation and exact retry retention |
| 18 | [#32](https://github.com/phenixrizen/conductor/pull/32) | Attempt deadlines, streaming failure and publication receipt recovery |
| 19 | [#33](https://github.com/phenixrizen/conductor/pull/33) | Exact approved coding criteria and retained verification coverage |
| 20 | [Release guide branch](https://github.com/phenixrizen/conductor/tree/codex/full-release-guide-review-20260913) | Final README, architecture, setup and verification reconciliation |

The final documentation PR targets #33. Review the complete stack in this order,
including the guide. CI status belongs to each current PR head and is visible on
GitHub; earlier canceled or failed attempts are not substituted for current checks.

## Completion evidence

Run the applicable [repository checks](../AGENTS.md) for each behavioral change,
including live PostgreSQL, actual MCP clients, isolated worker execution, Chromium,
PTYs and owned process-restart acceptance. Live adapter tests use synthetic
repositories/accounts and scoped credentials. Record unavailable prerequisites
explicitly and continue independent work; never mark the complete release verified
while a required gate lacks its evidence. Design approval, produced implementation,
verified implementation, merged code, deployment and production outcome stay
separate throughout this contract.

### Combined local verification

The complete implementation at `1279584` passed both full Go runs with an explicit
isolated PostgreSQL database: **1,348 tests passed in each normal/race run**, across
37 tested packages. Fifty optional/helper tests were explicitly skipped in those
general runs; separately enabled acceptance provides the live boundaries below.
`go vet`, all three OpenAPI contracts, every tracked Go file's formatting, web
`npm ci`/typecheck/build, documentation links and source hygiene passed.

| Boundary | Observed qualification |
|---|---|
| Isolated workers and native tools | 390 tests/subtests across 11 protocol packages passed with actual Docker, native CodeGraph, Spec Kit/ADRKit and verified Temporal/TLS processes; live-account GitHub and the parent-only crash helper remain explicit skips |
| Complete shared workflow | Both Linear/Jira variants passed with actual cross-repository Git, native graph, signed MCP, dependent Docker checks, retained Temporal restart, exact provider publication receipts and runtime observations |
| Database and process recovery | Owned PostgreSQL backup/restore, API/database restart, durable source recovery and retained tracker/runtime receipts passed; existing development databases were preserved |
| Browser | Actual signed Chromium workflows and native Keycloak HTTPS sign-in passed; exact criterion support rejects substituted revisions, invented checks and contradictory evidence; desktop/mobile screenshots inspected |
| Terminal | All five authenticated/local PTY paths passed, including source collection, graph/agent/delivery/tracker workflows and runtime evidence; exact credential, scope, revision and request checks remain enforced |

The final test portability corrections preserve the acceptance assertions: browser
fixture names avoid Python standard-library shadowing, and terminal refresh checks
wait for completed reads before inspecting a fresh frame. A recovery fixture now receives its intended 150-second budget at construction;
a child timeout could not extend the previous 60-second parent. Its unchanged
crash/cleanup/no-repeat assertions passed twice after the correction. Local
concurrent Docker network creation interrupted one Jira browser startup before
sign-in; both tracker browser variants then passed independently. Current GitHub
Actions results provide the hosted evidence.

These checks use synthetic source, identities and controlled provider responses.
Actual paid model inference, live SaaS writes, a hosted Temporal service, Conductor
deployment and production outcomes remain unverified. Those limits are not passing
results and do not authorize external operations.
