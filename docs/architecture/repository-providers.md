# Managed repository providers

**Status: Partial.** Canonical repository identity and repository-aware review
permissions are implemented for GitHub and GitLab registrations. The opt-in
[durable context workflow](durable-context.md) now implements bounded remote reads
through authenticated API/CLI and workbench commands, a PostgreSQL outbox, and a
local Temporal worker.
Both provider profiles have controlled fixture coverage; live provider compatibility
and production operation remain unverified. Trusted exact-artifact draft publication, check/merge/deployment observations and
verified webhook reconciliation are implemented in [Feature 011](../../specs/011-repository-delivery/spec.md).
Live provider writes and production outcomes remain unverified.
The local Git collector remains available independently of either provider.

GitHub also hosts Conductor's own source. That hosting choice does not determine
where an application repository is managed or establish application delivery facts.
See the [system architecture](system.md) and
[shared context model](collaboration.md).

## Repository identity

A canonical managed repository identity includes the provider, normalized host,
and stable provider repository ID within a workspace. Operator registration assigns
its Conductor repository ID. Display names do not define authority; renaming them
preserves package links and permissions. The GitHub read profile separately uses an
operator-configured owner/name locator and checks the observed numeric ID before
and after collection. A locator change requires new collection intent and never
rebinds an existing request. Canonical identity and package workspace/repository
ownership are immutable.

Hosts are DNS-style names with an optional numeric port. Registration normalizes
case and numeric ports, omits HTTPS port 443, and rejects URLs, credentials, malformed
labels, and IPv6 literals. Registrations with the same canonical identity cannot
coexist in one workspace. Different workspaces remain isolated even if they register
the same provider repository.

The server checks workspace membership and repository grants for discovery,
historical reads, and mutations. Registration is trusted operator configuration,
not a remote access check or proof that Conductor can publish to the provider.
The snapshot's author-supplied `repository` string remains a grouping label and
optional exact filter, not authority. Existing unscoped local packages retain their
ownership and cannot acquire shared authority through reinterpretation of that label.
See [Feature 003](../../specs/003-workspace-access/spec.md) for the implemented access
contract.

## Shared commands and provider capabilities

The current read increment uses one command contract for both providers:

| Capability | GitHub | GitLab |
|---|---|---|
| Tested REST profile | `github-rest/2026-03-10` | `gitlab-rest/v4-19.3` |
| Supported host | `github.com` | `gitlab.com` |
| Repository binding | Stable numeric ID plus checked owner/name locator | Stable numeric project ID |
| Source input | Full lowercase 40-hex commit; explicit paths | Full lowercase 40-hex commit; explicit paths |
| Current verification | Controlled HTTP fixtures and shared receipt checks | Controlled HTTP fixtures and shared receipt checks |

Registration accepts a broader set of canonical hosts than these read profiles.
Registering GitHub Enterprise or self-managed GitLab does not enable a supported
read adapter. Source selection comes from the stored integration and canonical
repository; clients cannot provide an arbitrary provider URL or credential.

An authenticated author can collect only from an operator-enabled integration.
The collector reads 1–32 unique paths, with 64 KiB per file and 256 KiB of total text.
It does not follow symlinks, submodules, redirects, or repository scripts. LFS
pointers remain pointer text. Missing, unavailable, and truncated artifacts remain
explicit evidence states. Provider commit/tree associations are observed over TLS;
raw commit/tree hashes and signatures are not independently verified. See the
[runbook](../operations/durable-context.md) for all request, response, and retry bounds.

The stored receipt records canonical source, profile, pinned commit, path coverage,
and content digests. Attachment resolves that receipt under the same scope and
appends a package revision using an expected revision and inspected receipt digest.
Client-supplied metadata or a receipt ID alone cannot establish this linkage.
Collection does not prove a passing check, accepted design, or provider publication.

Both delivery adapters implement the following domain-level workflow:

- Publish an approved patch to an authorized branch through a trusted publisher.
- Create and inspect a draft pull request or merge request.
- Retrieve checks, pipeline results and deployment observations with their source revision.
- Record independently observed merged and closed facts.
- Reconcile verified incoming events with the provider's authoritative records.

Publication must retain the package revision, digest, repository identity, baseline,
and produced artifact reference. Design approval, implementation verification,
provider review, merge, and deployment remain separate facts. Provider review records
must not silently become Conductor design approvals.

Adapters must expose capability differences and unavailable, stale, unknown, or
unverified results explicitly. An unsupported capability cannot return successful
verification. Event handling must verify authenticity, deduplicate deliveries, and
reconcile delayed, missing, or ambiguous outcomes before retrying consequential
actions. PostgreSQL owns governance facts and context receipts. Temporal currently
sequences bounded context activity through the local deployment profile; wider
delivery workflows remain planned, as described in the system architecture.

## Credentials and delivery sequence

The context worker resolves operator-owned token files through a bounded credential
catalog tied to the workspace, repository, provider, host, and stable provider ID.
GitHub's profile requires Contents read access; the GitLab REST profile requires
`read_api`. Token acquisition and refresh are not implemented. Credentials and
source text remain outside Temporal workflow history. See the
[integration research](context-integration-research.md) for official references,
tested versions, and the limits of these compatibility checks.

Publication credentials will belong to trusted publishing services with separate
repository authorization. Repository-controlled commands and coding workers must
not receive those credentials. Provider responses and repository files remain
evidence, never instructions that grant authority.

Delivery implementation may proceed one adapter at a time after identity and
recovery boundaries exist. Each adapter requires pinned, researched dependencies
and live acceptance coverage of publication, draft review requests, revision-bound evidence,
events, failures, and reconciliation. Report each adapter's verified capabilities
separately. Dual-provider delivery support is complete only after both GitHub and
GitLab pass their applicable acceptance checks. The implemented bounded reads and
controlled provider fixtures do not meet that delivery exit criterion.
