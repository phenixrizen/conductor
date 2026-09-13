# Durable repository context

**Status: Implemented bounded workflow; provider/deployment qualification is partial.**
The API/CLI and browser/terminal controls, shared immutable receipts, explicit
attachment, bounded GitHub/GitLab reads and Temporal workflow are implemented with
local or [verified TLS transport](temporal-tls.md). Local Git snapshot behavior is
retained. The [whole-source research](codegraph-integration-research.md) records one
actual GitHub read at an exact commit. Broader provider compatibility, live GitLab
credentials and production deployment remain unverified. ADR 0003 remains Proposed.
The [feature specification](../../specs/006-durable-context/spec.md) records the
selected permission: repository authors may collect after an operator enables
the repository read integration.
The [integration research](context-integration-research.md) records inspected
Temporal and provider source versions, exercised protocol boundaries and live-service limits.
The [operations guide](../operations/durable-context.md) gives runnable setup,
finite limits and acceptance commands.

## Why collection comes first

A team needs recoverable source context before approving a design or asking an
agent to implement it. Reading selected files from one commit provides a useful
first workflow without running repository code. It can prove recovery, credentials,
scope, and evidence handling before adding coding and publication capabilities.

The user supplies an existing repository selection, an exact commit, and explicit
paths. The operator supplies the enabled integration and repository-scoped read
credential. The result stays in the shared service, so another authorized engineer
can inspect it even after the first client's process or conversation ends.

## Ownership and flow

```mermaid
sequenceDiagram
    participant Client as Engineer or agent
    participant API as Authenticated domain commands
    participant DB as PostgreSQL
    participant Dispatch as Outbox dispatcher
    participant Temporal as Temporal workflow
    participant Activity as Trusted collection activity
    participant Provider as Configured GitHub or GitLab
    Client->>API: Repository, full commit, paths, idempotency key
    API->>DB: Authorize and commit request, audit, start intent
    API-->>Client: Persisted request ID
    Dispatch->>DB: Claim lease and persist actual runtime identity
    Dispatch->>Temporal: Start deterministic workflow ID
    Dispatch->>DB: Record observed workflow/run identity
    Temporal->>Activity: Collection ID and opaque request binding
    Activity->>DB: Load immutable input and recheck access
    Activity->>Provider: Bounded read at exact commit
    Provider-->>Activity: Tree/blob evidence or explicit gap
    Activity->>DB: Recheck access; commit immutable receipt
    Activity-->>Temporal: Receipt ID and digest only
    Client->>API: Inspect shared collection
    API->>DB: Authorize and read receipt/progress observations
    API-->>Client: Text, gaps, and observed progress
    Client->>API: Explicit attachment with package revision and receipt digest
    API->>DB: Version-checked revision plus audit
```

| Record or behavior | Authority | Meaning |
|---|---|---|
| Principal, membership, collection permission, enabled integration | PostgreSQL and trusted operator configuration | Who can request/read work and which remote identity may be used |
| Immutable collection request and audit | PostgreSQL | Accepted intent and exact requested source |
| Dispatch lease and delivery attempts | PostgreSQL outbox | Handoff delivery only; not execution sequencing |
| Activity order, retries, timeouts, cancellation | Temporal | Durable execution coordination |
| Immutable receipt and source text | PostgreSQL | What the activity collected, with bounds and provenance |
| Progress observations | Derived read model | Last observed workflow fact with time; may be stale or unavailable |
| Package revision and approval | Existing PostgreSQL domain model | Separately inspected engineering intent |

Temporal receives IDs and bounded metadata. Activities keep tokens and source text
outside workflow history and return only durable receipt references. Trusted worker
credentials permit its narrow service operations; repository text cannot issue
commands or obtain those credentials. No permission lock spans a provider request.

## Recovery is observable

A committed request survives a failed HTTP response. Retrying the same idempotency
key returns that request; changing input conflicts. A dispatcher may die before or
after a workflow starts, so it reconciles the deterministic workflow ID and records
the actual run identity before acknowledging delivery. It never guesses that an
unavailable workflow has not started or silently replaces lost history.
Request, idempotency, and start identities outlive Temporal history retention. An
existing receipt prevents redispatch after history expires. Credential rotation
may repair access under the original binding; it cannot change the requested
provider identity or collector profile.

Activities may execute again after an uncertain result. A retry first retrieves
any committed receipt for the immutable request and returns that reference without
recollecting. Provider reads remain tied to the original commit. If concurrent
attempts finish, receipt persistence returns the first committed result and never
overwrites it with later timestamps or changed availability observations. A receipt
cannot be rebound to different request inputs. Bounded attempt observations stay
separate from immutable output identity.
The workflow cannot revise a package to make its progress look complete. PostgreSQL
retains durable facts; Temporal determines sequencing. Finite workflow-history
retention cannot support a claim of globally exactly-once execution.

Cancellation is requested and then observed. Each provider operation checks current
access and cancellation first; no check can unsend a request already in flight.
Cancellation and result transactions lock the same collection record. A committed
cancel intent fences new receipt publication, while Temporal supplies confirmation
that execution stopped. A receipt committed first remains authoritative even if
its acknowledgment was lost before later cancellation or revocation. Public read
permission protects historical source. A failed final permission check discards
newly fetched text.

The first dispatch records the actual Temporal cluster and namespace identity,
address and queue as an immutable binding. Each execution RPC checks that target;
a new server behind the same address cannot silently replace retained history.
A known missing run becomes unresolved; an unknown start is reconciled for at most
one hour against a namespace with at least 24 hours of retention. Manual deletion
inside that window is indistinguishable from a start that never arrived. Retain
history and investigate known deletion instead of resetting/recreating executions.
Unresolved observations are sticky; administrative repair is not implemented.

## Provenance and coverage

The existing `conductor-git/v1` snapshot remains unchanged. Version 2 uses
`conductor-remote/v1` and identifies its provider, canonical repository, exact commit,
collector/profile version, request/receipt identity, and content digest explicitly.
Clients cannot authenticate provenance merely by writing a familiar receipt ID in
JSON; the service resolves a matching immutable record under current scope and
compares the entire snapshot JSON. Unverified version 2 extensions are rejected;
unknown outer package fields and version 1 extensions remain intact.
Attachment requires the same workspace and Conductor repository ID, even when
another workspace has registered the same external repository.

Complete text retains both its Git blob ID and SHA-256 digest. Missing paths,
unsupported modes, binary text, provider failures, and collection limits remain
visible. A paginated or truncated tree is not evidence that a file does not exist.
No symlink, submodule, LFS target, download redirect, or embedded command is followed.

A completed workflow means its defined collection procedure ended. Coverage may
still be incomplete. Collected specifications and ADRs are source artifacts; their
text cannot grant approval, establish passing verification, or authorize execution.
An explicit version-checked attachment creates a new package revision and therefore
requires its own review.

## Delivery limits

Browser and authenticated terminal collection controls follow the API/CLI commands.
One bounded page of shared requests leads to explicit receipt inspection and
confirmation of cancellation or attachment. Lost request acknowledgments retain
the exact input and key for an explicit retry. An attachment conflict or unknown
outcome blocks reattachment until inspection is renewed; confirmation never fetches.
Manual refresh reads later observations, while a local display clock marks old
progress stale without making HTTP requests.

The browser renders version 2 source coverage and compares the complete snapshot
against an explicitly inspected scoped receipt before displaying trusted linkage.
The terminal shows escaped source and full JSON. Version 1 extensions retain their
original meaning. Both interfaces clear collection state on access failure, and
the browser discards it on account or scope changes. Real Chromium and PTY
acceptance use stored receipt fixtures, separate from actual workflow recovery.

Both GitHub and GitLab are required, with separately reported read profiles and
live test evidence. [Feature 011](../../specs/011-repository-delivery/plan.md) now
implements separately authorized draft publication, check/merge/deployment
observations and webhook reconciliation through controlled real HTTP adapters.
Those operations do not acquire authority from collection or imply live external
publication qualification. See the [provider boundaries](repository-providers.md).
See the [delivery plan](../../specs/006-durable-context/plan.md) for implementation
and process-restart acceptance boundaries.
