# Feature 001: Durable work-package review

**Status:** Implemented for local development; shared deployment remains blocked
on authentication and authorization.

## Goal

Provide the smallest complete governance loop: create a package, persist an
immutable revision, submit it, inspect it, approve the exact inspected content,
then edit it and observe that the earlier approval is no longer effective.

## Actors

- **Author:** creates and revises a package and requests review.
- **Reviewer:** independently inspects and approves a submitted revision.

Development authentication uses explicit local identities. The server derives the
approval actor from the authenticated request; approval payloads never contain an
actor.

## Rules and acceptance criteria

1. Each change has a stable ID and monotonically increasing revision number.
2. Revision content is immutable and has a deterministic SHA-256 digest over its
   canonical JSON representation.
3. A mutation supplies the expected latest revision; a mismatch is a conflict.
4. Only the latest submitted revision can be approved.
5. Approval supplies both the inspected revision and digest.
6. An author cannot approve their own revision.
7. Creating a subsequent revision leaves the approval record auditable but makes
   it ineffective for the current revision.
8. Clients show conflicts rather than silently approving refreshed content.
9. Package state and its audit event are committed in one database transaction.

## Terminal review acceptance

The Bubble Tea interface uses the shared Go API client and the same domain commands
as the web and CLI. It must support one complete local-development review loop:

1. Create a package from an explicitly selected JSON file, discover shared packages,
   and inspect the current package's complete content, revision, and digest.
2. Submit the displayed current revision. An independent reviewer can confirm an
   approval bound to the displayed revision and digest without a refresh request
   inside that action.
3. Revise from an explicitly selected JSON file using the displayed expected
   revision. Unknown structured fields in that file survive the command.
4. A conflict or uncertain mutation outcome disables further mutations until the
   user explicitly inspects the latest revision. Never retry an approval silently.
5. Keep any historical inspection read-only. Historical approvals are records, not
   approval for the current package.
6. Bound file input and HTTP operations, support cancellation and scrolling, and
   render untrusted terminal controls as inert text. The selected local actor is
   development identity only and does not establish workspace permissions.

## Process-restart acceptance

An opt-in automated suite must run the compiled API as an operating-system process
against its own temporary PostgreSQL container and persistent volume. Record review
content, exact digests, submission, independent approval, subsequent revision, and
audit events before restarting the API and PostgreSQL processes. After restarting,
retrieve those facts through fresh API clients and verify they are unchanged.
Stale approval must still fail, and a new valid command must still succeed.

The suite owns and cleans up only resources it creates. It must never restart an
externally configured test database or the developer's existing database. Missing
opt-in reports a skip; missing dependencies after opt-in report a failure.

## Initial content

The package captures intent, design, context, scope, tasks, verification, and
authority as structured JSON. Unknown fields are retained exactly, allowing later
schema evolution without lossy UI round trips.

## Non-goals

Agent execution, production authentication, Temporal, GitHub/GitLab delivery,
Linear/Jira synchronization, CodeGraph, Groundcover, artifact storage, and automatic
merge are not part of this slice.

## Implementation status

The domain rules, PostgreSQL command store, HTTP endpoints, Go client, CLI,
Bubble Tea terminal workbench, and web inspector are implemented. HTTP-level tests
cover the complete review and invalidation path. Go and frontend dependency locks
are committed. Live PostgreSQL tests cover concurrent edits, approval invalidation,
audit rollback, missing packages, and connection-pool reopen durability.

The opt-in process-restart suite also verifies actual API and PostgreSQL process
restarts, with exact historical approval, audit, and shared-discovery retention.
Real PTY acceptance exercises terminal file import, submission, stale approval
without an implicit refresh, renewed inspection, independent approval, and revision
invalidation through the API and PostgreSQL. See the
[terminal guide](../../docs/operations/terminal-review.md) and
[local development guide](../../docs/operations/local-development.md) to reproduce
these checks. These complete the local Milestone 1 workflow; they do not establish
production identity, workspace isolation, or external integration readiness.
