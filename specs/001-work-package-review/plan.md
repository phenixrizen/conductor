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
- The first slice has one Go module and one web package. Temporal and outbox delivery
  begin only when an external workflow operation exists in Milestone 2.
- Production identity and repository authorization are explicit exit blockers for
  shared deployment, not implied by the local identity header.
- The UI is an inspector and command surface, not an alternate source of truth.

## Next increment

Close Milestone 1 with the terminal review workflow and automated API/PostgreSQL
process-restart acceptance described in the specification. Keep these as separate
implementation commits, then validate them together against real PostgreSQL.

The following increment is authenticated workspace and repository-aware review:
server-verified identity, workspace membership, canonical repository identity, and
permissions covering discovery, historical reads, and every mutation. Prove both
isolation between workspaces/repositories and collaboration within them before
shared deployment. The local revision-pinned context collector is already available.

Introduce the durable workflow outbox when the first external workflow operation
exists. Execution remains disabled until identity, authorization, context evidence,
and durable recovery meet their exit criteria.
Managed repository delivery must support both GitHub and GitLab through the same
domain workflow; remote discovery and publication adapters remain planned. See the
[repository provider plan](../../docs/architecture/repository-providers.md).
Each workspace will select either Linear or Jira as its single work tracker, with
explicit field/status ownership when synchronizing Conductor and linked repository
work. See the [work-tracking plan](../../docs/architecture/work-tracking.md).

### Proposed sequencing refinement

The [AI-DLC inspiration and plan review](../../docs/architecture/aidlc-plan-review.md)
proposes splitting this increment into Milestone 1 closure, authenticated review
with history and pinned context/evidence, then one durable execution workflow.
It also reviews human perspectives, stage contracts, verification, recovery, and
knowledge reuse alongside the planned Spec Kit and ADRKit integrations. These are
proposals for architectural review; they do not grant approval or enable execution.
