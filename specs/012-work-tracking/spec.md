# One work tracker per workspace

**Status: Implemented backend, provider adapters and MCP commands; fixture and
owned-runtime acceptance verified.** Live Linear/Jira tenant compatibility and
interface coverage are separately tracked release gates. Existing tickets are
linked and synchronized; ticket creation and planning-field writes are unsupported.

Each workspace selects exactly one Linear organization/team or Jira Cloud site/
project. A trusted operator provisions that binding, versioned status-ID mappings,
credential references and explicit tracker read/sync/resolve grants. A workspace
with retained links cannot silently replace its provider, host, organization,
project/team or Conductor origin. Omitted grants remain unchanged; explicit false
values revoke access. Credentials are not returned by the authenticated API.

Tracker title, description, assignment, priority and status are authoritative
planning data imported through fresh provider reads. Conductor owns a separate
Linear attachment or Jira remote issue link, with an immutable package/publication
projection. No tracker state can approve a package, establish a check result, prove
merge/deployment, or complete a ticket spanning unfinished repositories.

An authorized author links a stable existing issue ID to 1–16 exact package
revisions, including the selected repository. Optional publication references bind
exact immutable provider receipts. Current tracker read permission and every
included repository grant protect reads and pagination. Author permission on all
repositories plus a tracker sync grant protect link creation and sync requests.

Creation atomically stores immutable relationships, audit and an initial refresh
outbox intent. Subsequent keyed requests capture the link digest, operation and
inspected projection digest. A fresh observation of an absent card is required to
publish a new card. Competing edits remain conflicts. Restoring a conflicting card
requires explicit human resolve permission and the inspected conflicting digest.
The API never refreshes a baseline inside confirmation.

Temporal executes opaque synchronization references. Activities check current
principal, membership, tracker grants, every repository grant and configuration
version before each provider exchange and at source-bearing receipt commit. Signed
webhooks atomically enter a deduplicated inbox and enqueue authoritative refreshes;
webhook fields cannot replace a provider read. Incoming events never write cards,
preventing projection echoes from generating another outgoing mutation.

Before a provider mutation, persist one immutable write attempt. A retry reconciles
the stable attachment URL or remote-link global ID. If it finds the intended card,
retain synchronization; otherwise preserve uncertainty without retransmission.
Missing or mismatched retained Temporal history never starts replacement execution.
A committed activity receipt wins a retry even after later permission revocation;
public reads still require current access.

Expose pending/dispatched/unresolved transport observations separately from
refreshed/synchronized/conflict/unknown/unavailable activity facts. Status IDs need
an explicit mapping or intentional no-synchronization entry; unmapped values remain
visible. Observations older than five minutes are stale, and a recent observation
never proves that no newer provider change exists. A delayed response older than
already observed provider data is retained as an explicit stale conflict.

Both external APIs lack an atomic compare-and-swap for these dedicated cards.
Conductor reads before writing and verifies afterward; an edit in the intervening
provider race window may not be detectable. This limit never permits overwriting
tracker-owned planning fields or carrying approval onto changed source.
