# Durable context delivery plan

**Status: Proposed delivery plan.** Collection authorization was selected on
2026-09-13: author permission plus an operator-enabled repository read integration.
The contract does not itself implement a command or external workflow.

## Independently reviewable increments

1. **Contract and policy:** review the request, authority, canonical input, receipt,
   cancellation, revocation, and evidence contracts in the [specification](spec.md).
   Apply the selected author-plus-operator permission without changing design approval.
2. **Researched integration profiles:** inspect official source and documentation,
   pin a Temporal SDK/server test tool and each provider API profile, and record
   real capabilities. Choose finite output, tree, request, concurrency, and retry
   bounds. Test those choices before claiming compatibility.
   [Initial research](../../docs/architecture/context-integration-research.md)
   records candidate versions and protocol limits; none is runtime-tested yet.
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

Each working increment gets focused commits and a new PR based on its current
prerequisite branch. Proposed documentation must not be copied into OpenAPI as an
implemented route. No existing migration is rewritten and no runtime directory is
added solely to mirror the target diagram.

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

## Following work

Use the proven orchestration and context boundaries to specify an independently
authorized coding attempt: immutable approved inputs, isolated patch-only worker,
executed verification evidence, and a separately credentialed publisher. Deliver
draft GitHub PRs and GitLab MRs before claiming dual-provider delivery support.
Keep merge/deployment authorization and one Linear/Jira tracker per workspace as
separate contracts.
