# ADR 0001: Immutable work-package revisions

- Status: Proposed
- Date: 2026-09-11

## Decision

Append immutable canonical-JSON revisions. Compute SHA-256 in the application and
store it with a uniqueness constraint. An approval references `(change_id,
revision, digest)`; it is effective only when that tuple is current.

## Consequences

Historical approvals remain auditable and invalidation requires no destructive
update. Storage grows append-only. Canonicalization and schema-version migrations
must remain deterministic.
