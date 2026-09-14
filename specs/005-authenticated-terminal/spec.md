# Feature 005: authenticated terminal review

**Status:** Implemented with signed-token, live PostgreSQL, and real PTY acceptance.
Uses the existing authenticated API and
CLI credentials; no new provider integration or API capability is introduced.

## Outcome

An engineer or agent can review the same authorized workspace and repository from
the Bubble Tea workbench. The terminal derives its identity and available controls
from the server, while preserving exact revision approval and bounded file imports.
It also exposes the shared collection and receipt commands from
[Feature 006](../006-durable-context/spec.md) within that fixed scope.

## Acceptance criteria

1. Start authenticated `tui` with the existing token file or environment credential
   and explicit canonical workspace/repository selection. Reject a local actor
   combined with a token. Do not acquire tokens or refresh them inside the session.
2. Fix the API endpoint, credential, workspace, and repository for the running
   workbench. Resolve the principal and readable repository capabilities from
   bounded `session` and `repositories` responses before showing shared packages.
   Never derive authority from token role claims, labels, or a terminal actor flag.
3. Validate discovered principal, membership, repository ownership, capabilities,
   and bounds before enabling review. If truncated discovery omits the explicitly
   selected scope, say access could not be established from incomplete discovery;
   do not present that omission as a definitive permission denial.
4. Keep the initial principal ID and human/agent kind fixed. A later identity
   mismatch requires a new terminal session. Replacing the token file after startup
   cannot change the credential or the actor shown in an existing session.
5. Author capability enables creation, revision, and submission for humans or
   agents. Approval additionally requires a human, the approve capability, an
   independent reviewer, and the current submitted revision. Client controls
   reflect stored capabilities; the service enforces the actual command policy.
6. Support the [guided Change editor](../019-guided-change-authoring/spec.md) and
   preserve bounded explicit JSON-file selection as an advanced import, complete
   replacement preview, unknown content fields, text escaping, cancellation, and small-screen safeguards.
   The UI never runs repository text as a command, editor, or script.
7. Confirmation captures the displayed revision and digest before dispatch. No
   identity, permission-discovery, or package read occurs inside a mutation. A
   stale or uncertain package write blocks further package mutations until explicit
   inspection. Collection creation has the explicit same-key recovery path below.
8. Every 401/403 response invalidates access, including reads and malformed error
   bodies. Clear package content, listings, drafts, confirmation, and capabilities;
   ignore late results and require explicit `r` to re-establish access. The client
   retains a bounded typed HTTP status without leaking credentials or raw bodies.
9. Explicit refresh clears the previous inspection, reads current access, then
   loads the selected package or listing. Permission revocation must be enforced
   by the server on every request even before the interface refreshes capabilities.
   Received package/list ownership must match the selected canonical scope.
10. Exercise the compiled CLI in a real PTY against signed-token authentication and
    PostgreSQL: collaboration, exact approval, stale conflicts, agent restrictions,
    revocation, token-file stability, and terminal output without credentials.
    Preserve the existing local terminal and API workflows.
11. Keep collection controls in a separate authenticated view. Read access permits
    bounded collection discovery and receipt inspection; author permission permits
    requests only after operator enablement. Package approval keys cannot trigger
    commands from the collection view, and a local actor cannot enter it.
12. Preview one explicitly selected regular UTF-8 JSON request file, bounded to
    64 KiB, with exactly `commit`, `paths`, and `idempotencyKey`. Reject unknown,
    duplicate, case-aliased, or null fields. Validate the full commit and explicit
    paths using the domain rules, sort paths before preview, and capture immutable
    input and key before confirmation. Never execute a file or repository text.
13. Show request acceptance, timestamped execution observations, cancellation
    intent, and receipt coverage separately. Age execution freshness after 30
    seconds without polling or replacing inspected source. Missing, unavailable,
    truncated, stale, and unknown evidence must remain explicit.
14. Permit cancellation only for the recorded requester with current author
    permission and no committed receipt or existing cancellation request. Attach
    an inspected receipt from any authorized collaborator only to a separately
    inspected package in the selected scope. Confirmation captures the expected
    package revision, package digest, receipt ID, and receipt digest without a read.
    Validate the returned replacement content and show its new unapproved revision.
15. Preserve collection input and key after an uncertain creation response. Only
    explicit confirmation may retry that exact request; neither refresh nor the
    HTTP transport silently repeats the command. A stale or uncertain attachment
    requires package reinspection; uncertain cancellation requires collection
    reinspection. Authentication failures clear collection drafts, keys, receipts,
    listings, and confirmations along with package state. Recovery preserves the
    original credential and distinguishes collection IDs from package IDs.
16. Exercise shared collection paging and evidence, attachment conflicts and exact
    requests, lost-response recovery, cancellation intent, permission revocation,
    and reader/agent controls with the compiled CLI in real PTYs. Stored receipt
    fixtures exercise these interfaces without claiming provider or Temporal
    execution; those have separate Feature 006 acceptance paths.

## Boundaries

The workbench inspects current packages; the browser and CLI continue to provide
history and comparison where documented. Bounded discovery has no continuation
after 100 workspaces/repositories. Collection pages contain at most 20 entries,
with at most 1,000 visited page cursors retained per browsing session. CLI
interactive login, token refresh, coding execution, repository publication, and
work-tracker synchronization remain separate work. The terminal reuses the existing
Feature 006 collection API; its controls add no migration or provider behavior.

## Full-release workbench

The authenticated terminal also exposes graph, coordinated execution, delivery and
tracker views through the shared client. Its explicit request files retain the
complete input and idempotency key. Exact human authorizations do not refresh
inspection, missing profiles block execution controls, and publication requires
inspected artifact identity and patch/check content. Every view clears private
state on access failure; fixed identity, bounded pagination, escaped text and
explicit uncertain-response recovery apply throughout. See the
[release terminal runbook](../../docs/operations/release-terminal.md).
