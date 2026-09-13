# Durable context delivery plan

**Status: Implemented bounded workflow; provider/deployment qualification is partial.**
The API/CLI and browser/terminal workbenches, receipt and authorization model,
both bounded provider read adapters, and trusted Temporal worker are present.
Collection authorization was selected on 2026-09-13: author permission plus an
operator-enabled repository read integration. Workers support explicit local or
verified TLS/mTLS transport. The later whole-source work records one live GitHub
read; live GitLab compatibility and production deployment remain unverified.
ADR 0003 remains Proposed.

## Independently reviewable increments

1. **Contract and policy:** review the request, authority, canonical input, receipt,
   cancellation, revocation, and evidence contracts in the [specification](spec.md).
   Apply the selected author-plus-operator permission without changing design approval.
2. **Researched integration profiles:** inspect official source and documentation,
   pin a Temporal SDK/server test tool and each provider API profile, and record
   real capabilities. Choose finite output, tree, request, concurrency, and retry
   bounds. Test those choices before claiming compatibility.
   [Initial research](../../docs/architecture/context-integration-research.md)
   records pinned versions and protocol limits, with actual Temporal process tests
   distinguished from provider fixtures and unverified live compatibility.
3. **Request and persistence:** implement versioned domain commands, access checks,
   immutable request/receipt facts, ordered migrations, idempotency constraints,
   and transactional audit/outbox. Test concurrency, rollback, revocation, legacy
   snapshot preservation, and scope filtering against real PostgreSQL. Do not ship
   an apparently successful collection endpoint without a working dispatcher and
   supported worker configuration.
4. **One durable read workflow:** implement the dispatcher, reconciliation, Temporal
   workflow, trusted activity boundary, and first provider read adapter together.
   Start with exact commits and explicit paths. Keep source outside workflow
   history, preserve gaps, and verify real process recovery using owned resources.
5. **Shared inspection and attachment:** expose only implemented HTTP contracts,
   add Go client/CLI request and inspection, cancellation, and explicit attachment.
   Demonstrate two clients recovering the same facts after restart and rejecting
   stale package edits. Keep UI status vocabulary aligned with observed evidence.
6. **Second provider and interface expansion:** exercise the same acceptance
   contract for the other provider, then bring collection into the browser and
   terminal. Publish capability and deployment limits before adding assistant work.

Keep focused commits and the explicit dependency order of the authorized release
stack. Proposed documentation must not be copied into OpenAPI as an implemented
route. No shipped migration is rewritten and no runtime directory is added solely
to mirror the target diagram.

## Completion evidence

Run the repository Go, race, vet, API, documentation, frontend, and live database
checks appropriate to each change. Add an opt-in owned Temporal process acceptance
with persistent storage and explicit missing-dependency failures. It must never
restart a user-configured service or delete development data. Keep browser and
real PTY checks for any changed interface paths.

Publish the exact tested dependency/API profiles, commands, result limits, and
unavailable live checks in an operations runbook. An API acknowledgment proves
only persisted intent. A fetched file proves only collected source. A workflow
passing its test does not approve this ADR or authorize assistant execution.

## Implemented scope and remaining exit criteria

The API/CLI path covers request, bounded shared inspection, cancellation intent,
immutable receipt and explicit revision-checked attachment. Migration 004 adds
request/audit/outbox facts, immutable runtime bindings and execution observations.
The worker runs the pinned Temporal workflow against an explicitly configured
local service or the [shared TLS/mTLS profile](../015-temporal-tls/plan.md). Current
provider profiles are GitHub.com REST 2026-03-10 and GitLab.com REST v4/19.3. Source
and credentials stay out of workflow history.

The [runbook](../../docs/operations/durable-context.md) records finite admission,
HTTP/output/retry limits, startup configuration and unresolved-recovery boundaries.
Controlled provider tests are not live provider certification. Both workbenches
support shared collection discovery, exact-input requests, receipt inspection,
requester cancellation and explicit revision-bound attachment. Browser version 2
rendering checks complete snapshots against explicitly inspected scoped receipts;
the terminal retains escaped source and complete JSON. Actual Chromium and signed
PTY suites exercise these commands with isolated PostgreSQL and receipt fixtures.
Shared Temporal TLS/mTLS and [release operations](../016-operations/plan.md) now
have owned protocol and recovery acceptance. A real production deployment and
administrative repair of unresolved handoffs remain outside the verified scope.

## Later implementation and remaining qualification

The [CodeGraph research](../../docs/architecture/codegraph-integration-research.md)
records one actual GitHub whole-source read of this repository at an exact commit.
It does not certify arbitrary GitHub installations or live GitLab credentials.
Broader provider qualification, production deployment and administrative recovery
of unresolved handoffs still require their own evidence.

[Features 009–010](../009-coordinated-execution/plan.md) implement independently
authorized coding attempts, isolated patch production and actual verification.
[Feature 011](../011-repository-delivery/plan.md) adds a separately credentialed
publisher for draft GitHub PRs and GitLab MRs; both paths pass controlled real HTTP
acceptance. [Feature 012](../012-work-tracking/plan.md) implements one Linear/Jira
tracker per workspace. Live publication and tracker tenants remain unverified;
none of these records supplies merge or deployment authority.

## Current release delivery instruction

The user authorized stacked PRs on 2026-09-13 for the full release. Follow its
required gates and explicit dependency order in the
[full release contract](../../docs/full-release.md).
