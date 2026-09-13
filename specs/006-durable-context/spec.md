# Feature 006: durable shared repository context

**Status: Partial.** API/CLI requests, shared receipts, explicit attachment,
PostgreSQL outbox, Temporal sequencing and both bounded provider read profiles are
implemented. The collection permission model was selected on 2026-09-13: repository
authors may collect after operator enablement. Initial Temporal deployment is local
only; browser/TUI collection controls and live provider compatibility remain
unverified or later work. ADR 0003 remains Proposed. See the
[operations guide](../../docs/operations/durable-context.md) for supported bounds.

## Outcome

An engineer or agent selects a managed repository, an exact commit, and a small
set of files. Conductor collects those files in the background and keeps one
shared result that survives process restarts. Another authorized collaborator can
inspect the request, its progress, collected text, and any gaps without recovering
the original agent conversation.

This is context gathering before design or implementation. It runs no repository
commands, generates no patch, and grants no package approval. It is the first
bounded workflow used to prove durable orchestration before coding workers.

## Authorization decision

The selected policy allows an active human or agent with repository author
permission to request collection **only after an operator enables a read
integration for that canonical repository**. This extends author permission to
bounded context collection; it does not authorize coding or publication. There is
no separate collection grant in this increment. This policy choice does not accept
ADR 0003 or describe the integration as implemented.

Read permission governs access to stored collection records
and text. Only the recorded requester with the currently required collection
permission may request cancellation through the initial client workflow. Operator
revocation provides the administrative stop boundary. Design approval is neither
required to gather context nor inferred from a collected result.

## Acceptance criteria

1. An authenticated command selects an existing canonical workspace/repository,
   one full commit object ID, 1–32 unique explicit relative file paths, and a
   bounded idempotency key. The provider profile must explicitly support the Git
   object format. Reject branch names, abbreviated IDs, caller-supplied URLs,
   credentials, local filesystem paths, directory traversal, and shell commands.
2. In one PostgreSQL transaction, verify current principal, membership, collection
   permission, and operator-enabled integration; record immutable input, requester,
   canonical repository identity, integration configuration version, audit event,
   and dispatch outbox entry. The API returns the accepted record only after commit.
   Integration identity never comes from a content label or token role claim.
3. Scope the idempotency key to requester and canonical repository. Canonicalize
   validated paths in a documented order before input hashing and collection, so
   total-byte limits have deterministic results. A repeated key with identical
   input returns the original record; changed input conflicts. Concurrent retries
   create one request and one start intent. No automatic new request follows an
   uncertain response. Compare retries against the original bound integration
   profile rather than substituting current configuration. Secret rotation may
   repair access only under that unchanged canonical binding; a changed provider,
   host, repository identity, or collector profile requires a new explicit request.
4. The outbox bridges the committed request to a deterministic Temporal workflow
   ID. Reconcile ambiguous starts before retrying. Temporal owns activity order,
   timeouts, retry policy, and cancellation; database dispatch leases manage only
   transport delivery. Store a workflow run identity once established. Do not
   silently start a replacement after the original history becomes unavailable.
   Retain request/idempotency/start identities beyond workflow-history retention;
   a completed database receipt prevents redispatch even after history expires.
5. Workflow payloads contain collection IDs and bounded receipt metadata. Trusted
   activities load inputs from PostgreSQL and credentials from operator-controlled
   configuration. Provider tokens, source text, response bodies, and client access
   tokens must not enter workflow history, ordinary logs, or command arguments.
6. Before each bounded provider operation, recheck the recorded requester's active
   identity, membership, collection permission, cancellation intent, and integration
   configuration. A trusted
   worker acts for that recorded requester without retaining or minting a bearer
   token. Reject credentials whose provider, host, or stable repository ID differs
   from the immutable request. Read-only credentials cannot authorize publication.
7. Resolve paths through the selected commit's tree, verify canonical repository
   identity and object IDs, and collect only regular UTF-8 text files. Never follow
   symlinks, submodules, LFS download links, provider redirects, or repository
   scripts. Bound HTTP duration, response bytes, tree traversal, pagination,
   request count, and retry count; incomplete tree lookup is unavailable evidence,
   not proof that a path is missing.
8. Retain existing content bounds: at most 64 KiB per file and 256 KiB total text.
   Each requested path has a `collected`, `missing`, `unavailable`, or `truncated`
   result with a bounded reason. Retain complete text only, with its blob ID and
   SHA-256 digest. An oversized file keeps no partial text. Unsupported Git object
   formats or provider capabilities are explicit failures, never substitutes.
9. Persist one immutable scoped receipt and snapshot with input digest, canonical
   source, collector/profile version, timestamps, per-path coverage, and content
   digest. A retry checks for an existing receipt before collecting again and
   returns its reference after verifying request identity. If concurrent activity
   attempts finish, the transaction returns the first committed receipt; it never
   replaces it with a later observation. Reject any attempt to bind that receipt
   to different request inputs. Keep attempt timestamps outside receipt identity,
   and never select a newer commit on retry. A committed receipt remains the
   authoritative completion for a trusted activity retry after later revocation
   or cancellation; public inspection still requires current read permission.
10. Recheck authorization and cancellation in the transaction that accepts the
    result. Do not hold permission locks across network I/O. Revocation prevents
    later provider operations and result publication once observed; it cannot undo
    an HTTP request already sent. Workers cancel in-flight HTTP where supported
    and discard text when final authorization fails.
11. A cancellation request is a durable audited intent delivered to Temporal. The
    interface distinguishes cancellation requested from confirmed cancellation.
    Request, cancellation, and result transactions serialize on the same collection
    record. A committed cancellation intent prevents later provider authorization
    and receipt publication; an already authorized request may still finish.
    Only observed Temporal cancellation confirms the
    execution stopped. Reconcile cancellation racing with dispatch/start as well.
    A result committed first remains historical fact; a cancellation committed
    first prevents later result publication. An unavailable workflow service is
    reported as unknown progress, not completed, failed, or cancelled by guess.
12. Keep request facts, execution observations, and artifact coverage separate.
    Accepted means persisted intent; workflow completion can still contain missing
    or unavailable paths. A terminal outcome establishes no passing checks, merge,
    deployment, or production result. Show observation time and unavailable or
    stale progress explicitly after reconnect.
13. Authorized clients list and inspect collection records through bounded keyset
    pagination, with repository permission filtering before the page limit. One
    client's collection becomes visible to other authorized clients. Revocation
    also protects historical text and receipt reads; knowing an ID is insufficient.
14. An author may explicitly attach an inspected receipt to a package in the same
    workspace and Conductor repository ID using the expected package revision and
    receipt digest. Matching external provider identity across workspaces is
    insufficient; those registrations remain isolated.
    The service reads the immutable stored receipt, preserves unknown package
    fields, and creates an ordinary new revision with transactional audit. No
    collection silently edits a package or carries approval onto that new revision.
15. Introduce a versioned remote snapshot/receipt representation without rewriting
    existing `repositoryContext` version 1 documents or their package digests.
    Remote collection must not identify itself as `conductor-git/v1`. An embedded
    client-supplied receipt ID is not authenticated provenance; trusted linkage
    requires a server lookup under the same scope and digest checks. Compare the
    full version 2 snapshot JSON, including any extensions, to the stored receipt;
    unverified extra fields cannot inherit its provenance. Preserve unknown outer
    package fields and historical version 1 extensions, including new-field aliases.
16. Deliver one complete API/CLI request → inspect → cancel or result → explicit
    attachment workflow. Enforce configured finite admission and concurrency limits
    without evicting active work. Publish exact defaults, deadlines, and recovery
    controls with the implementation and prove them in acceptance tests.

## Verification and limits

Tests must exercise actual PostgreSQL and an owned Temporal server, including API,
worker, workflow-server, and dispatcher restarts; duplicate delivery; ambiguous
start/completion; permission revocation; cancellation races; scope isolation;
provider errors and redirects; partial evidence; and stale attachment conflicts.
Include missing workflow history after lost start acknowledgment, receipt commit
followed by lost activity acknowledgment, and forged or cross-workspace receipt
links through attachment and ordinary create/revise commands.
SDK test environments and controlled provider fixtures are complementary tests, not
proof of real provider compatibility or durable process recovery.

Each provider profile must distinguish transient failures
eligible for bounded Temporal activity retry from terminal path gaps. Do not commit
a final `unavailable` receipt and then retry collection to improve it. A later
collection is a new explicit request with its own identity; earlier observations
remain recoverable.

The current implementation requires workspace and canonical repository scope on
all collection commands. It admits at most 20 active/unresolved requests per
repository; pending cancellation retains its slot. Per-process activity concurrency
is two. Namespace history retention must be at least 24 hours; an unknown start
has a one-hour reconciliation horizon. Known missing history, changed runtime
binding and receipt mismatch become explicit sticky unresolved observations.
Manual workflow-history deletion inside the horizon cannot be distinguished from
a lost start that never arrived. Do not delete/reset retained executions to recover
dispatch. Administrative repair of unresolved records is not implemented.

GitHub and GitLab read profiles may land separately. Report their verified
capabilities separately and retain explicit unavailable behavior for the other
provider. Live provider checks use synthetic repositories and read-only credentials;
missing access is a limitation, not a pass. Publication, check ingestion, webhook
reconciliation, assistant execution, and Linear/Jira synchronization remain later
work. The workspace still selects exactly one tracker.

See the [delivery plan](plan.md), [architecture contract](../../docs/architecture/durable-context.md),
and [proposed decision](../../docs/adr/0003-durable-context-workflow.md).
