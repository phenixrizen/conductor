# Managed repository providers

**Status: Partial.** Canonical repository identity and repository-aware review
permissions are implemented for GitHub and GitLab registrations. Remote discovery,
verification, and delivery adapters remain planned.
The current local Git collector reads committed objects independently of either
provider; it does not establish remote integration support.

GitHub also hosts Conductor's own source. That hosting choice does not determine
where an application repository is managed or establish application delivery facts.
See the [system architecture](system.md) and
[shared context model](collaboration.md).

## Repository identity

A canonical managed repository identity includes the provider, normalized host,
and stable provider repository ID within a workspace. Operator registration assigns
its Conductor repository ID. An owner/name or namespace/path is display metadata;
renaming it preserves package links and permissions. Canonical identity and package
workspace/repository ownership are immutable.

Hosts are DNS-style names with an optional numeric port. Registration normalizes
case and numeric ports, omits HTTPS port 443, and rejects URLs, credentials, malformed
labels, and IPv6 literals. Registrations with the same canonical identity cannot
coexist in one workspace. Different workspaces remain isolated even if they register
the same provider repository.

The server checks workspace membership and repository grants for discovery,
historical reads, and mutations. Registration is trusted operator configuration,
not a remote access check or proof that Conductor can publish to the provider.
The snapshot's author-supplied `repository` string remains a grouping label and
optional exact filter, not authority. Existing local snapshots retain unscoped
ownership and cannot acquire shared authority through reinterpretation of that label.
See [Feature 003](../../specs/003-workspace-access/spec.md) for the implemented access
contract.

## Shared commands and provider capabilities

Both adapters must implement the same domain-level workflow:

- Collect context pinned to an immutable repository commit.
- Publish an approved patch to an authorized branch through a trusted publisher.
- Create and inspect a draft pull request or merge request.
- Retrieve checks, pipeline results, and review records with their source revision.
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
actions. PostgreSQL owns governance facts; future Temporal workflows own execution
sequencing, as described in the system architecture.

## Credentials and delivery sequence

Credentials belong to provider-specific server configuration and trusted publishing
services, with repository-scoped authorization. Repository-controlled commands and
coding workers must not receive publication credentials. Provider responses and
repository files remain evidence, never instructions that grant authority.

Implementation may proceed one adapter at a time after identity and recovery
boundaries exist. Each adapter requires pinned, researched dependencies and live
acceptance coverage of publication, draft review requests, revision-bound evidence,
events, failures, and reconciliation. Report each adapter's verified capabilities
separately. Dual-provider support is complete only after both GitHub and GitLab
pass their applicable acceptance checks; the local collector alone does not meet
that exit criterion.
