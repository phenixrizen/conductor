# Feature 011: Trusted repository publication

**Status: Implementation produced; verification recorded in the plan.**

Conductor turns a persisted, verified coding artifact into a draft GitHub pull
request or GitLab merge request after a person authorizes the exact proposal.
All developers and agents with current access inspect the same proposal, source
pins, human authorization, provider receipt and subsequent observations.

## Authority

1. An author proposes a publication using a coordination run ID, immutable task
   ID and artifact digest, an operator-allowed base branch, title and description.
   Source bytes, actor identity, credentials and provider URLs are not request fields.
2. The selected repository must belong to the run. Every source repository remains
   subject to current read access. The run's human execution authority must remain
   active, the run must not be cancelled, and its approved package revisions,
   graph and source receipts must still match.
3. The task receipt must represent success. Producer evidence binds the exact input.
   Every configured check must pass with exact argv and repository identity, an
   untruncated result, output digest and the digest of the complete cumulative patch
   set. At least one configured check must cover the target repository.
4. A proposal binds the target provider identity, integration version, base commit,
   base and result trees, patch digest, generated branch and presentation content.
   Its digest never changes. Editing requires another proposal; no approval follows it.
5. Only a current human with author permission and the separate `can_publish`
   grant may authorize the inspected proposal digest. An agent cannot authorize,
   including when an operator accidentally grants that capability. Admission,
   authorization, audit and outbox commit atomically. No source is fetched in this
   transaction or as part of confirmation.

## Trusted execution

Temporal owns sequencing through `conductor.publish.v1`. History contains only
opaque operation and binding IDs and the committed receipt digest. PostgreSQL
retains immutable proposal, authorization and receipts, append-only observations,
audit events, verified webhook identities, and fenced dispatch intents.

The publisher retrieves the original retained Git bundle and applies the cumulative
patch in an isolated bare Git object database. It verifies the base and resulting
Git tree, patch digest, declared paths, modes and size bounds. Git receives no
provider credentials, source checkout, network permission or repository commands.
The adapter separately receives its operator-controlled publication credential.

Every provider request and the receipt commit recheck the original human authority,
all source permissions, cancellation, approved pins and integration binding. A
permission change can stop subsequent work; it cannot undo a provider request
already admitted. Such partial writes and uncertain acknowledgments remain explicit.
A previously committed immutable operation receipt wins a retry after revocation,
without granting a public read or authorizing more provider calls.

## Provider contract

Both profiles support only the public hosts `github.com` and `gitlab.com`. Canonical
numeric repository/project IDs are checked independently of display names. Each
publication gets `conductor/publication/<server-generated-id>`; existing refs are
inspected and never overwritten or force-pushed. Provider commit parent, exact
result tree, marker and base branch are checked before draft review creation.

GitHub uses the Git data API and create-ref, then a draft pull request. GitLab uses
an atomic `start_sha` commit to a new branch with `force: false`, then a draft merge
request. GitLab's returned root tree is reconstructed from all root entries before
review creation, detecting content transformations such as LFS processing. A tree
mismatch can leave a branch but cannot produce a successful Conductor receipt.

A retry first reconciles the deterministic branch and unique review marker. It must
not create a second PR/MR after a lost response. Redirects and HTTP command replay
are disabled. Known missing Temporal history, changed runtime identity or receipt
mismatch becomes unresolved and never starts replacement execution.

## Evidence and events

The immutable first provider receipt remains distinct from the newest timestamped
observation. Draft/open/closed/merged state, exact-head checks and pipeline results
are provider observations, not Conductor design approval. Empty, skipped, neutral,
truncated, unavailable and pending checks cannot imply passing. A merge requires
provider merge facts and a valid merge commit. No automatic merge occurs.

An authenticated provider webhook is a reconciliation hint only. GitHub HMAC-SHA256
and the documented GitLab secret-token profile bind the payload to an operator
selected repository. Event IDs and payload digests deduplicate atomically with a
reconciliation outbox. Events never assign domain lifecycle state. Changed payload
reuse, invalid authentication, wrong repository or malformed commit is rejected.

Deployment reads preserve exact published-head or provider-merge commit, environment,
provider status, profile and timestamps. Partial provider histories remain explicit;
GitHub and GitLab deployment requests are never created by Conductor. Production
outcomes remain `not_observed` until an independent observability adapter supplies
evidence. Live provider writes or deployments are not claimed from fixtures.

## Interfaces

Authenticated API and shared Go client provide proposal, inspection, bounded summary
discovery, exact-digest authorization and explicit reconciliation. A dedicated artifact endpoint
returns the complete persisted implementation and check output for human inspection
before authorization, bounded to 16 MiB plus its envelope. MCP exposes
proposal, inspection, discovery and reconciliation; it has no authorization tool.
The publisher and webhook listener are separate trusted processes. Operator config
is a database command, not an HTTP administration endpoint.

## Browser review

The browser lists bounded pages of 20 shared proposals and previews explicit
run/task/artifact, base branch and presentation inputs before creation. An uncertain
creation preserves the complete input and key for an explicit retry.

Human publication controls require server-discovered publication access and a
complete retained artifact matching the inspected proposal. The browser verifies
all displayed patch and producer/check output byte digests, shows every retained
patch and result as escaped text, and requires explicit review acknowledgment.
Missing checks, failed/unexecuted/truncated results or unknown cleanup block the
control. Server authorization still checks every source and exact approval.
Confirmation sends only the displayed proposal digest, with no read inside it.
An uncertain authorization clears artifact inspection and requires renewal.

Provider observations retain timestamps, exact commits, gaps and deployment
provenance separately from the first immutable receipt. Only a proposal with a
retained provider observation offers an explicit provider refresh. An uncertain
refresh retains the exact digest/key. Scope changes and access denial clear private
patches, previews and confirmations. Provider publication, merge, deployment and
production verification remain separate facts.
