# Milestone 1: durable work-package review

## Scope and status

The current vertical slice supports creating an immutable package revision,
submitting the current revision, inspecting it, approving its exact revision and
digest, and appending a revision that makes the prior approval ineffective.

The Go CLI, Bubble Tea terminal workbench, HTTP API, domain service, PostgreSQL
schema/store, and React inspector implement the local review workflow, with
committed dependency locks. Live PostgreSQL tests cover concurrent edits,
transactional audit rollback, missing packages, and connection-pool reopen.
An opt-in acceptance suite separately restarts the actual API and PostgreSQL
processes and verifies preserved content, approvals, history, audit, and discovery.
Real PTY acceptance exercises the terminal through that same API/store path.
[Feature 003](../../specs/003-workspace-access/spec.md) adds authenticated workspace
and repository review through the API and noninteractive CLI. Browser and terminal
workbench review remain local-only; authenticated browser sign-in is the next
interface increment.

## Domain model

```mermaid
erDiagram
    CHANGE ||--|{ WORK_PACKAGE_REVISION : contains
    WORK_PACKAGE_REVISION ||--o{ APPROVAL : receives
    CHANGE ||--o{ AUDIT_EVENT : records

    CHANGE {
        text id PK
        text workspace_id FK
        text repository_id FK
        timestamptz created_at
    }
    WORK_PACKAGE_REVISION {
        text change_id PK,FK
        bigint revision PK
        integer schema_version
        text digest
        jsonb content
        text author
        timestamptz created_at
        timestamptz submitted_at
    }
    APPROVAL {
        text change_id PK,FK
        bigint revision PK,FK
        text digest FK
        text reviewer PK
        timestamptz created_at
    }
    AUDIT_EVENT {
        bigint sequence PK
        text change_id FK
        text event_type
        text actor
        bigint revision
        jsonb data
        timestamptz created_at
    }
```

A work package is the latest immutable revision plus an **effective** approval, if
one exists for the same `(change_id, revision, digest)`. Historical approval rows
are retained; the application never flips a freely writable `approved` column.
Feature 003 adds immutable workspace/repository ownership outside revision content.
Legacy local packages retain null ownership; migrating them does not change content,
digests, authors, or approval records. The [shared context model](collaboration.md)
describes permissions and the separation between local and authenticated data.

## Review lifecycle

```mermaid
stateDiagram-v2
    [*] --> Draft: create revision 1
    Draft --> Submitted: submit current revision
    Submitted --> Approved: independent reviewer approves exact revision + digest
    Draft --> Draft: revise with expected revision
    Submitted --> Draft: revise / prior submission is historical
    Approved --> Draft: revise / approval becomes ineffective
    Approved --> Approved: inspect current approved revision

    note right of Submitted
      Stale revision or digest: reject with conflict
      Same author as revision: reject approval
    end note
```

`Approved` is derived, not assigned. Appending a revision transitions the current
view to `Draft` while leaving all historical evidence intact.

## Exact-revision approval sequence

```mermaid
sequenceDiagram
    autonumber
    participant Reviewer
    participant Client as Web / CLI / TUI client
    participant API
    participant Service as Domain service
    participant DB as PostgreSQL

    Reviewer->>Client: Inspect revision N and digest D
    Client->>API: POST approval {revision: N, digest: D}
    API->>API: Establish request identity and selected scope
    API->>Service: Approve(change, identity, scope, N, D)
    Service->>DB: Begin transaction, resolve principal and permissions, lock change
    DB-->>Service: Current immutable revision
    Service->>Service: Validate submitted, current, digest, independent reviewer
    alt exact content is still current
        Service->>DB: Insert approval and audit event
        Service->>DB: Commit
        API-->>Client: 201 approved
    else package changed or review is invalid
        Service->>DB: Roll back
        API-->>Client: 409 conflict or 422 rejected
    end
```

The client must send what was displayed. It must not fetch a newer revision inside
an approval action. A `409` requires the reviewer to inspect the new revision.

## Edit and invalidation sequence

```mermaid
sequenceDiagram
    participant Author
    participant API
    participant DB as PostgreSQL
    participant Reviewer

    Author->>API: Revise change with expected revision N
    API->>DB: Lock change and read latest revision
    alt latest revision is N
        API->>DB: Append revision N+1 and audit event atomically
        API-->>Author: 201 revision N+1, approved=false
    else another mutation won
        API-->>Author: 409 revision_conflict
    end
    Reviewer->>API: Approve previously inspected N/D
    API-->>Reviewer: 409 revision_conflict
```

PostgreSQL advisory transaction locks serialize commands for one change. Optimistic
revision checks still define client-visible concurrency semantics. Commands for
unrelated changes remain independent.

## Command and endpoint map

| Intent | CLI | HTTP | Required concurrency input |
|---|---|---|---|
| Create | `conductor create` | `POST /api/v1/changes` | None |
| Inspect | `conductor show` | `GET /api/v1/changes/{id}` | None |
| Revise | `conductor revise` | `POST /api/v1/changes/{id}/revisions` | Expected revision |
| Submit | `conductor submit` | `POST /api/v1/changes/{id}/review-requests` | Inspected revision |
| Approve | `conductor approve` | `POST /api/v1/changes/{id}/approvals` | Inspected revision and digest |

The exact payload contract is maintained in `api/openapi.yaml`.
The [terminal workbench](../operations/terminal-review.md) invokes these same Go
client commands with the revision displayed before confirmation. Conflicts and
uncertain mutation outcomes block further writes until explicit inspection.

## Invariants

1. Revision numbers increase monotonically within a change.
2. Revision content is append-only and recoverable.
3. Digest is SHA-256 of Go's deterministic JSON encoding of the structured content.
4. Revision and audit event are committed in one transaction.
5. Only the current submitted revision can be approved.
6. A revision author cannot approve that revision.
7. Approval identity comes from the request context, never the approval payload.
8. An approval is effective only for its referenced current revision and digest.
9. Unknown content fields survive because content is stored as structured JSON.
10. Missing evidence or unavailable integrations cannot be represented as passing.
11. Authenticated ownership is immutable; content labels do not change access.
12. Agent principals cannot approve, even with a configured approval grant.
13. Permission checks and consequential review writes share one transaction.

## Known limitations

- Local actor headers are development identity only and cannot access workspace
  packages. Browser and TUI review still use this explicit loopback mode.
- Authenticated API/CLI access supports the documented signed access-token profile.
  Synthetic issuer tests do not establish compatibility with a real identity vendor;
  interactive login and browser/TUI authentication remain pending.
- No Temporal workflow, assistant, GitHub/GitLab delivery, Linear/Jira, or runtime adapter
  runs. Local Git context collection is available as described in Feature 002.
- Shared discovery, historical revision inspection, and audit-query endpoints are
  available; see [Feature 002](../../specs/002-context-history/spec.md).
- Process-restart acceptance proves persistence across completed commands and
  process restarts. It does not prove power-loss durability, high availability,
  point-in-time recovery, or recovery of future external workflows.
- The schema currently rejects duplicate content within a change; unchanged edits
  and exact content reverts need an explicit domain policy and error contract.
- The terminal workbench supports current-package review. Historical inspection
  remains available in the CLI and web; revision comparison is available in the web.
