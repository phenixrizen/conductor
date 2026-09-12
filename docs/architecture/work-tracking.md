# Work tracking and synchronization

**Status: Planned requirements.** Conductor must support Linear or Jira as the work
tracking system. Neither integration is implemented. Each workspace selects exactly
one tracker, giving people one ticketing system synchronized with Conductor and
linked repository work. Linear-to-Jira mirroring is outside this scope.

## Separate authorities, connected records

The selected tracker owns ticket priority, assignment, and planning workflow.
Conductor owns immutable package revisions, design approvals, and retained evidence.
GitHub or GitLab owns its pull/merge requests, checks, pipelines, and merge facts.
These records describe related work without becoming interchangeable authorities.
See [managed repository providers](repository-providers.md) and the
[system architecture](system.md).

A ticket marked done does not establish that implementation was verified, merged,
deployed, or successful in production. A ticket status change cannot approve a
package, waive evidence, or authorize publication. Conversely, one merged request
does not complete a ticket covering unfinished packages or other repositories.

## Relationships and field ownership

One ticket may link to multiple work packages, repositories, and pull/merge requests.
Store these relationships explicitly, retaining the relevant package revisions and
provider records. Do not infer completion from the presence of a link or require a
one-ticket-to-one-branch model.

External identity must include the tracker, host or installation, workspace/project
scope where applicable, and stable provider record ID. Human-readable ticket keys,
titles, and URLs are display attributes, not sufficient identity. Renames or moves
must not silently detach historical relationships.

Before enabling synchronization, configure which authority owns each synchronized
field and the permitted write direction. For example, tracker-owned assignment may
be displayed in Conductor, while revision and approval summaries are projections of
Conductor facts. An edit to a projected summary cannot rewrite its source facts.
Competing edits require a visible conflict and an explicit resolution; unrestricted
bidirectional last-writer-wins behavior is prohibited.

Status mappings must be configured and reviewed for the selected workspace. Do not
assume vendor-default status names or equate similarly named states. Unmapped states
remain explicit and block dependent transitions. Every configured status needs a
mapping or an intentional no-synchronization outcome. A proposed ticket transition
must identify the facts and policy supporting it; ambiguous mappings must not trigger
automatic completion.

## Durable synchronization

Database changes and outgoing synchronization intents must commit together in a
durable outbox. Incoming events require authenticated ingestion, a durable inbox,
deduplication, and reconciliation with authoritative records. Future Temporal
workflows own retry and execution sequencing; PostgreSQL owns governance facts and
durable synchronization records.

Operations need stable identifiers, source provenance, and recorded outcomes. Handle
duplicate, delayed, out-of-order, and missing events without assuming vendor delivery
ordering. Prevent feedback loops by tracking projected writes and their source
versions. Reconcile uncertain external results before retrying; do not claim global
exactly-once synchronization.

Show pending, synchronized, unavailable, stale, and conflicting relationships with
their last confirmed state. Network failure or rejected credentials must not look
like successful synchronization. Preserve failures and resolutions in the audit
record without exposing credentials.

## Implementation boundary

Each adapter requires separately researched authentication, scoped credentials,
capabilities, and tested dependencies. The server must enforce workspace membership
and tracker/repository permissions for discovery, linking, and synchronization.
Test relationship integrity, status mapping, conflicts, retries, loop prevention,
and recovery against each supported tracker before declaring its synchronization
verified. The [shared context model](collaboration.md) remains the current
local-development foundation; tracker synchronization is a later integration
increment.
