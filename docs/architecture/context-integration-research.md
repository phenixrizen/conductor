# Context integration research

**Status: Implemented profiles with bounded verification.** Reviewed official
source on 2026-09-12 and exercised the implementation on 2026-09-13. The pinned SDK
and actual Temporal development server have process-recovery and replay coverage.
GitHub/GitLab adapters have controlled HTTP/TLS fixtures, without live provider
credential verification. These findings do not establish production compatibility
or accept ADR 0003. See the [workflow](durable-context.md) and
[operations guide](../operations/durable-context.md).

## Pinned profiles

| Component | Inspected pin | Implementation implication |
|---|---|---|
| Temporal Go SDK | `v1.44.1`, API module `v1.62.12`; declares Go 1.24.0 | Pinned in Conductor's module; SDK, replay and actual local-server tests |
| Temporal development CLI | `v1.8.3`, embedding server `v1.31.2`; declares Go 1.26.4 | Verified Linux amd64 release binary for owned acceptance; not built with Conductor's Go 1.24 toolchain |
| GitHub | REST `2026-03-10`; OpenAPI source `cca5c0021436293e6ec6a689b9e2f6794080d003` | Explicit version header; initially test `github.com` through `api.github.com` |
| GitLab | REST `/api/v4`; release source `v19.3.2-ee`, commit `601afd607baae60e14e83e16fb9dea03cf8c7032` | `/v4` is not a frozen server release; initially test GitLab.com and report the observed profile |

Sources: [SDK module](https://github.com/temporalio/sdk-go/blob/v1.44.1/go.mod),
[CLI module](https://github.com/temporalio/cli/blob/v1.8.3/go.mod),
[GitHub API versions](https://docs.github.com/en/rest/about-the-rest-api/api-versions),
[GitHub OpenAPI snapshot](https://github.com/github/rest-api-description/blob/cca5c0021436293e6ec6a689b9e2f6794080d003/descriptions/api.github.com/api.github.com.json),
[GitLab release source](https://gitlab.com/gitlab-org/gitlab/-/tree/601afd607baae60e14e83e16fb9dea03cf8c7032).

## Read adapters

Use direct bounded HTTP/JSON reads for this slice. The implemented initial profiles
support full 40-hex commit IDs; SHA-256 repositories need separate support. Preserve
Conductor's explicit-path and text bounds. Self-managed GitHub/GitLab needs an
operator-configured HTTPS origin, egress policy, and separately tested release
profile; accepting a registration host is not sufficient integration configuration.

GitHub's documented Git-object routes use `/repos/{owner}/{repo}`. Add an
operator-controlled locator separate from canonical numeric identity. Validate the
repository lookup's numeric ID before collection and again before accepting its
result. Reject redirects and locator mismatches; a rename requires reconciliation.
These checks detect ordinary changes but are not an atomic remote identity
transaction. Do not depend on undocumented numeric-ID route aliases.
[Repository lookup](https://docs.github.com/en/rest/repos/repos#get-a-repository),
[commit objects](https://docs.github.com/en/rest/git/commits#get-a-commit-object).

Inspect nonrecursive trees and explicit Git modes before reading blobs by object
ID. Avoid the Contents API's symlink-target behavior and submodule compatibility
representation. Truncated metadata is unavailable evidence, not a missing file.
[Tree contract](https://docs.github.com/en/rest/git/trees#get-a-tree),
[Contents limitations](https://docs.github.com/en/rest/repos/contents#get-repository-content),
[blob reads](https://docs.github.com/en/rest/git/blobs#get-a-blob).

GitLab documents numeric project IDs. Validate project identity and the exact
commit, then inspect bounded nonrecursive tree pages at that commit and retrieve
regular blobs by ID. Its pinned tree finder resolves `ref` to a commit before
listing; entries include mode/path/type but do not expose a raw root tree object.
Validate pagination scope and repeated cursors. Do not rely on file executable
metadata alone to distinguish symlinks or submodules.
[Project lookup](https://docs.gitlab.com/api/projects/#retrieve-a-project),
[repository endpoints](https://docs.gitlab.com/api/repositories/),
[pinned tree finder](https://gitlab.com/gitlab-org/gitlab/-/blob/601afd607baae60e14e83e16fb9dea03cf8c7032/app/finders/repositories/tree_finder.rb),
[pinned tree fields](https://gitlab.com/gitlab-org/gitlab/-/blob/601afd607baae60e14e83e16fb9dea03cf8c7032/lib/api/entities/tree_object.rb).

Recompute each retained Git blob ID from its exact bytes and record SHA-256 too.
This verifies blob bytes against the selected object. The REST profile still
relies on provider-observed commit/path associations over TLS; it does not verify
raw commit/tree hashes, commit signatures, branch freshness, or passing checks.
Never resolve HEAD after a failed pinned read or follow LFS/download links.
[Git object format](https://git-scm.com/book/en/v2/Git-Internals-Git-Objects).

Use operator-supplied repository-limited read credentials. GitHub's blob endpoint
documents Contents read permission. GitLab's full project/commit/tree profile needs
`read_api`; do not infer that `read_repository` covers every REST operation. Token
creation, installation-token refresh, and provider account provisioning are outside
this slice. Resolve tier/role availability during actual provider setup.
[GitHub permissions](https://docs.github.com/en/rest/git/blobs#get-a-blob),
[GitLab token scopes](https://docs.gitlab.com/security/tokens/access_token_scopes/).

## Temporal handoff and recovery

Set explicit conflict `FAIL`, reuse `REJECT_DUPLICATE`, and
`WorkflowExecutionErrorWhenAlreadyStarted: true`. The SDK default can turn an
already-started response into a run handle; a handle alone is not an existence
check. Reconcile actual execution type, request binding, namespace, and run identity.
Outbox acknowledgments need lease fencing; a lease cannot prevent duplicate network
calls by a dispatcher whose lease expired.
[Pinned client options](https://github.com/temporalio/sdk-go/blob/v1.44.1/internal/client.go),
[pinned start implementation](https://github.com/temporalio/sdk-go/blob/v1.44.1/internal/internal_workflow_client.go).

Closed-workflow duplicate rejection depends on retained history. Keep Conductor's
request/result identities longer and stop automatic redispatch when an uncertain
execution is beyond the configured reconciliation horizon. Never infer permanent
exactly-once behavior from a stable workflow ID.
[Workflow identity and retention](https://docs.temporal.io/workflow-execution/workflowid-runid).

The implemented workflow calls one `conductor.collect-and-persist.v1` activity using an
opaque request reference. It first returns any existing receipt, otherwise reads
remote data, persists the bounded result, and returns only its reference. Explicit
finite activity deadlines/retry limits and cooperative cancellation are required.
Sanitize errors, heartbeat details, headers, and other payloads as well as normal
results; default failure conversion can retain readable diagnostics.
[Activity options](https://github.com/temporalio/sdk-go/blob/v1.44.1/internal/activity.go),
[Go cancellation](https://docs.temporal.io/develop/go/workflows/cancellation),
[failure conversion](https://docs.temporal.io/failure-converter).

## Verification boundaries

The opt-in tests own a real development-server subprocess with a persistent `--db-filename`,
loopback listener, and temporary resources. Its default in-memory mode cannot prove
server restart recovery. The development server does not establish production
availability or security. Check the release binary and cleanup even when readiness
fails; use SDK unit/replay tests in addition to real process acceptance.
[CLI server options](https://docs.temporal.io/cli/command-reference/server),
[pinned development storage](https://github.com/temporalio/cli/blob/v1.8.3/internal/devserver/server.go),
[SDK testing guide](https://docs.temporal.io/develop/go/best-practices/testing-suite).

Provider fixtures exercise identity mismatches, literal paths, pagination,
symlinks/submodules, binary/LFS pointer text, output limits, redirects, rate limits,
and cancellation. Live read checks on synthetic GitHub and GitLab repositories
remain required for compatibility claims. Actual Temporal acceptance decodes
history payloads and inspects logs to check source/token canaries are absent, and
exercises lost acknowledgments, duplicate delivery and process restart.
Signed API/live PostgreSQL paths additionally cover permission revocation and shared scope.

The namespace binding uses the server's actual cluster ID, registered namespace ID,
normalized address and queue, with at least 24 hours of retention. A development
server rebuilt behind the same address changes that identity; the bound runtime
refuses dispatch. This does not detect manual deletion of an individual execution
inside the one-hour unknown-start horizon, and does not claim globally exactly-once
provider calls.

Ordinary 5xx/408 responses without Retry-After use bounded Temporal backoff. Explicit
provider delays are preserved; delays above 30 seconds stop the retry sequence
rather than violating the hint. Missing rate-limit metadata uses a conservative
one-minute delay, which likewise ends this sequence. Provider collection uses fresh
HTTP/1 connections to prevent hidden client transport retries outside authorization.
