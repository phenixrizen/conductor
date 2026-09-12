# Feature 001: Durable work-package review

**Status:** In progress

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

## Initial content

The package captures intent, design, context, scope, tasks, verification, and
authority as structured JSON. Unknown fields are retained exactly, allowing later
schema evolution without lossy UI round trips.

## Non-goals

Agent execution, production authentication, Temporal, GitHub/GitLab delivery,
Linear, CodeGraph, Groundcover, artifact storage, and automatic merge are not part
of this slice.

## Implementation status

The domain rules, PostgreSQL command store, HTTP endpoints, Go client, initial CLI,
and web inspector are implemented. HTTP-level tests cover the complete review and
invalidation path. Go and frontend dependency locks are committed and the frontend
build is verified. Live PostgreSQL tests cover concurrent edits, approval
invalidation, audit rollback, missing packages, and connection-pool reopen
durability. Full API/database restart persistence was checked manually; an
automated process-restart suite and the Bubble Tea TUI remain before the
Milestone 1 exit is met.
