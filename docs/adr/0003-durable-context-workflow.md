# ADR 0003: First durable workflow collects pinned repository context

- Status: Proposed
- Date: 2026-09-12

## Context

Authenticated review now has shared immutable package history and local Git context
across the API, CLI, browser, and terminal. Remote context and workflow sequencing
remain planned. Introducing a coding worker first would combine provider access,
durability, execution isolation, and publication before recovery is proven.

## Proposed decision

Implement one bounded remote context workflow before assistant execution. Start
from a canonical repository, full commit ID, and explicit paths. Persist request,
authorization, audit, outbox, and immutable receipts in PostgreSQL. Use Temporal as
the sole execution sequencer, with trusted activities handling external reads and
idempotent result persistence. Keep credentials and source text out of workflow
history. Attach results only through explicit version-checked package authoring.

The [feature contract](../../specs/006-durable-context/spec.md) specifies proposed
recovery, cancellation, revocation, and coverage behavior. The product permission
choice was selected on 2026-09-13: existing author permission plus an
operator-enabled repository read integration. Recording that choice does not
accept this proposed architectural decision or grant permissions to any principal.

## Consequences

The first workflow has a useful shared result and can prove process recovery
without executing repository code. Context precedes design approval. PostgreSQL
review facts and Temporal sequencing have one owner each, with observable handoff
and reconciliation. Provider collection supports GitHub and GitLab through explicit
tested profiles; remote reads do not establish delivery support.

This adds a workflow service and trusted worker to operate. Network calls and
activity execution may repeat; deterministic IDs and receipt constraints do not
promise globally exactly-once behavior. Cancellation and revocation cannot undo
an already sent provider request. The snapshot representation must evolve without
rewriting historical version 1 package content or its digest.

Accepting this decision would not approve any package, authorize a coding attempt,
or grant merge or deployment permission. Implementation and verification remain
separate from architectural acceptance.
