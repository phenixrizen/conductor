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

Add production identity/repository authorization, the durable workflow outbox, and
one revision-pinned repository context adapter before enabling agent execution.

### Proposed sequencing refinement

The [AI-DLC inspiration and plan review](../../docs/architecture/aidlc-plan-review.md)
proposes splitting this increment into Milestone 1 closure, authenticated review
with history and pinned context/evidence, then one durable execution workflow.
It also reviews human perspectives, stage contracts, verification, recovery, and
knowledge reuse alongside the planned Spec Kit and ADRKit integrations. These are
proposals for architectural review; they do not grant approval or enable execution.
