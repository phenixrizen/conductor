# Refined implementation plan

The broad first milestone is split into independently verifiable increments. This
avoids building three divergent state machines in the API, TUI, and web client.

1. **Normative core:** define canonical package content, revision/digest semantics,
   lifecycle commands, independent approval, typed conflicts, and audit events.
2. **Transactional persistence:** add append-only revisions and approvals in
   PostgreSQL. Lock each change during mutation and commit the revision and audit
   event together. Treat status as a projection derived from current revision and
   approvals rather than a freely writable field.
3. **One command boundary:** expose create, revise, submit, inspect, and approve via
   HTTP. Derive identity from local authentication middleware and require explicit
   optimistic-concurrency inputs.
4. **Common clients:** use the Go client for CLI/TUI operations. The React client
   submits the revision and digest currently rendered on screen.
5. **Acceptance proof:** exercise stale approval, self-approval, concurrent edits,
   invalidation, restart durability, and client-visible conflicts against Postgres.

### Refinements to the proposed product plan

- Approval is effective only when it targets the current revision; no mutable
  `approved` flag exists to forget to invalidate.
- Revision content is canonical JSON, not a server-generated Markdown rendering.
- Submission is recorded per revision, keeping package review state independent
  from future execution runs.
- The first slice has one Go module and one web package. Feature 006 introduces
  Temporal and outbox delivery for the first bounded remote context operation;
  review itself remains a PostgreSQL command workflow.
- Shared access requires verified identity and repository authorization. Feature
  003 supplies these for API/CLI review; the local identity header confers no shared
  rights. Feature 004 adds browser login; Feature 005 adds authenticated terminal
  review using the same permission boundary.
- The UI is an inspector and command surface, not an alternate source of truth.

## Next increment

The terminal review workflow and automated API/PostgreSQL process-restart
acceptance now complete the local Milestone 1 scope described in the specification.
The terminal, CLI, and browser all use the same version-checked command path.

[Feature 003](../003-workspace-access/spec.md) now implements authenticated API/CLI
review with verified identity, workspace membership, immutable canonical repository
ownership, and permissions covering discovery, historical reads, and every mutation.
Real PostgreSQL acceptance covers collaboration, isolation, revocation, and retained
legacy data. Identity tests use a synthetic issuer and establish no vendor-specific
compatibility claim.

Feature 004 adds browser OIDC sign-in using those same service commands and
repository capabilities, with durable sessions and cleared inspection on access
changes. [Feature 005](../005-authenticated-terminal/spec.md) adds authenticated
terminal review with a fixed identity and scope, capability discovery, and explicit
recovery. The local revision-pinned context collector remains available independently
of those login flows.

[Feature 006](../006-durable-context/spec.md) now has an implementation for that
first remote operation: author-requested context collection from an operator-enabled
GitHub or GitLab repository, immutable shared receipts, and explicit attachment to
an inspected package revision. The API/CLI request boundary, PostgreSQL outbox, and
local Temporal worker preserve review authority separately from execution progress.
Collection controls are opt-in; the web's structured version 2 context display and
interactive collection controls remain unimplemented. Targeted signed-issuer and
PostgreSQL checks exercise scope, revocation, receipt integrity, and attachment.
Controlled provider fixtures do not prove live provider compatibility. Full runtime
acceptance and operational limits are tracked in the
[durable context plan](../006-durable-context/plan.md) and
[runbook](../../docs/operations/durable-context.md).

Managed repository delivery must support both GitHub and GitLab through the same
domain workflow. The current read adapters fetch explicit paths at a pinned commit;
repository discovery, publication, draft PR/MR creation, and delivery reconciliation
remain planned. See the
[repository provider plan](../../docs/architecture/repository-providers.md).
Each workspace will select either Linear or Jira as its single work tracker, with
explicit field/status ownership when synchronizing Conductor and linked repository
work. See the [work-tracking plan](../../docs/architecture/work-tracking.md).

### Proposed sequencing refinement

The [AI-DLC inspiration and plan review](../../docs/architecture/aidlc-plan-review.md)
proposes Milestone 1 closure, authenticated review with history and pinned
context/evidence, then one durable execution workflow. Authenticated review is now
implemented across the API, CLI, browser, and terminal. The first bounded context
workflow has an implementation; the wider execution sequence remains a proposal.
It also reviews human perspectives, stage contracts, verification, recovery, and
knowledge reuse alongside the planned Spec Kit and ADRKit integrations. These are
proposals for architectural review; they do not grant approval or enable execution.
