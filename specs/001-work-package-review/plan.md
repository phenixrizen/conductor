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
  from separately authorized execution runs.
- The first slice has one Go module and one web package. Feature 006 introduces
  Temporal and outbox delivery for the first bounded remote context operation;
  review itself remains a PostgreSQL command workflow.
- Shared access requires verified identity and repository authorization. Feature
  003 supplies these for API/CLI review; the local identity header confers no shared
  rights. Feature 004 adds browser login; Feature 005 adds authenticated terminal
  review using the same permission boundary.
- The UI is an inspector and command surface, not an alternate source of truth.

## Implemented follow-through

The terminal review workflow and automated API/PostgreSQL process-restart
acceptance now complete the local Milestone 1 scope described in the specification.
The terminal, CLI, and browser all use the same version-checked command path.

[Feature 019](../019-guided-change-authoring/spec.md) extends that path with readable
Change/Design authoring in the browser and terminal, preserving legacy structured
content and separating a saved draft from its review request.

[Feature 003](../003-workspace-access/spec.md) now implements authenticated API/CLI
review with verified identity, workspace membership, immutable canonical repository
ownership, and permissions covering discovery, historical reads, and every mutation.
Real PostgreSQL acceptance covers collaboration, isolation, revocation, and retained
legacy data. The original identity tests use a synthetic issuer; later
[Keycloak qualification](../017-provider-qualification/plan.md) exercises a pinned
native browser-login profile without broadening the API access-token contract.

Feature 004 adds browser OIDC sign-in using those same service commands and
repository capabilities, with durable sessions and cleared inspection on access
changes. [Feature 005](../005-authenticated-terminal/spec.md) adds authenticated
terminal review with a fixed identity and scope, capability discovery, and explicit
recovery. The local revision-pinned context collector remains available independently
of those login flows.

[Feature 006](../006-durable-context/spec.md) implements author-requested context
collection from operator-enabled GitHub or GitLab repositories, immutable shared
receipts, and explicit attachment to an inspected package revision. The API, CLI,
browser and terminal share this command boundary. PostgreSQL outbox delivery and
Temporal preserve review authority separately from execution progress. Workers
support explicit local or [TLS/mTLS transport](../015-temporal-tls/spec.md).
The [durable context plan](../006-durable-context/plan.md) and
[runbook](../../docs/operations/durable-context.md) distinguish controlled provider
fixtures, the narrow live GitHub source-read result, and unverified deployment.

[Features 007–010](../009-coordinated-execution/plan.md) add shared repository graphs,
MCP proposals, exact human execution authorization, isolated coding workers and
retained verification evidence. [Feature 011](../011-repository-delivery/plan.md)
implements separately authorized GitHub draft PR and GitLab draft MR publication,
plus reconciliation and provider observations. Canonical repository registrations
remain operator-managed; automatic discovery of a provider account is unsupported.
Both publication adapters have controlled HTTP acceptance; live external writes
remain unverified. Each workspace selects one Linear or Jira tracker, with existing
ticket links and synchronization implemented in
[Feature 012](../012-work-tracking/plan.md). Ticket state cannot approve or verify work.

### Proposed sequencing refinement

The [AI-DLC inspiration and plan review](../../docs/architecture/aidlc-plan-review.md)
proposes Milestone 1 closure, authenticated review with history and pinned
context/evidence, then one durable execution workflow. Authenticated review is now
implemented across the API, CLI, browser, and terminal. The first bounded context
workflow and the later coordinated execution features are now implemented. The
original AI-DLC review remains Proposed. Its recommendations cover human
perspectives, stage contracts, verification, recovery and knowledge reuse;
[Feature 014](../014-design-tools/plan.md) now provides bounded native Spec Kit and
ADRKit commands in an isolated image. These tools and perspective selectors cannot
grant design approval or execution authority.

The complete platform is the current release target; follow the
[full release contract](../../docs/full-release.md) and its stacked review order.

[Feature 020](../020-native-design-assistance/spec.md) extends guided authoring with
shared native-assistant section suggestions, a restricted assistance MCP profile,
and explicit requester application as an ordinary unapproved revision. It also
adopts the Switch visual specification across the web and ASCII terminal header.
Provider accounts remain native; hosted inference and broad source selection are
not implemented by this increment.
