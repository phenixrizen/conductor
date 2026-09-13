# Publish verified work to GitHub or GitLab

**Status: Implemented with controlled provider fixtures, live PostgreSQL, signed
API acceptance and an owned Temporal publication workflow. Live provider writes,
external webhook setup and production deployment have not been performed.**

A developer or agent proposes an exact persisted coding artifact. A person inspects
its patches and check output, then authorizes that proposal. A separate trusted
publisher creates a new branch and draft PR/MR. Developers share the same proposal,
receipt and later check, merge and deployment observations through Conductor.

## Prerequisites and configuration

Use Go 1.25 or later (the repository pins its tested toolchain), Git, PostgreSQL,
OIDC workspace access, the context/coding integrations and retained full-source
bundles. Apply migrations through 008 in numeric order. Run the verified local
Temporal CLI 1.8.3 / server 1.31.2 with persistent history and a namespace retaining
at least one day, or configure the shared [remote TLS/mTLS profile](temporal-tls.md).
Hosted-account compatibility and production deployment remain unverified.

Enable the authenticated API with `CONDUCTOR_DELIVERIES=1`. Local actor mode cannot
enable publication. The separate `executionGrants` section of `conductor-admin`
configuration gives a human `canPublish: true` for the selected repository. They
also need current author/read access. The original run's human execution grants,
all repository read grants and package approvals must remain valid.

Register the provider integration through an operator-controlled JSON file:

```json
{
  "integrations": [{
    "workspaceId": "team",
    "repositoryId": "application",
    "profile": "github-delivery/2026-03-10",
    "locator": "synthetic/application",
    "credentialId": "application-publication",
    "baseBranches": ["main"],
    "enabled": true
  }]
}
```

For GitLab, use `gitlab-delivery/v4-19.3` and an empty `locator`; the registered
numeric project ID identifies the repository. Only `github.com` and `gitlab.com`
are supported. Each operator change increments the integration version and
invalidates older proposals, including disable/re-enable.

```bash
go run ./cmd/conductor-publisher configure \
  --file /absolute/operator/delivery.json --operator local-operator --check
go run ./cmd/conductor-publisher configure \
  --file /absolute/operator/delivery.json --operator local-operator
```

The first command validates syntax without touching PostgreSQL. The second uses
`DATABASE_URL` and commits configuration plus its audit. Neither command contacts
a provider or grants design approval.

Use a separate publication credential catalog. Do not share it with coding workers
or repository commands. Its format uses the same bounded, canonical credential-file
references as the [context worker](durable-context.md):

```json
{
  "credentials": [{
    "id": "application-publication",
    "workspaceId": "team",
    "repositoryId": "application",
    "provider": "github",
    "host": "github.com",
    "providerId": "123456",
    "tokenFile": "/absolute/operator/application-publication-token"
  }]
}
```

Use a repository-limited provider credential with the required Git data/PR or
commit/MR capabilities and check/deployment read access. The numeric provider ID
must equal Conductor's managed repository registration. Token files are reread for
each fresh activity attempt; secret values are not command arguments, workflow
payloads or source-process environment variables.

```bash
export CONDUCTOR_TEMPORAL_MODE=local
export CONDUCTOR_TEMPORAL_ADDRESS=127.0.0.1:7233
export CONDUCTOR_TEMPORAL_NAMESPACE=conductor
export CONDUCTOR_PUBLICATION_CREDENTIALS_FILE=/absolute/operator/publication-credentials.json
go run ./cmd/conductor-publisher
```

## Review and publication

Use the authenticated API/shared client or **Delivery → Repository deliveries** in the browser.
All requests select a fixed workspace and canonical target repository. Authoring
calls accept no actor, provider URL or credential.

1. `POST /api/v1/repository-deliveries` with `Idempotency-Key` and
   `runId`, `taskId`, `artifactDigest`, `baseBranch`, `title`, `description`.
2. Inspect `GET /api/v1/repository-deliveries/{id}` and
   `GET /api/v1/repository-deliveries/{id}/artifact`. The latter returns the complete
   persisted `execution.Result`, including base64-encoded patch bytes, producer and
   independent check output. Match its `deliveryDigest` and `artifactDigest` to the
   inspected proposal. Treat every patch, log and description as untrusted data.
3. A human sends the captured proposal `digest` to
   `POST /api/v1/repository-deliveries/{id}/authorizations`. This queues work; it is
   not confirmation of provider publication. Never refresh inside confirmation.
4. Inspect the shared receipt and timestamped observations. An immutable first
   receipt is separate from the newest provider observation and Temporal progress.
5. To refresh provider facts explicitly, send the inspected `digest` with a new
   captured `Idempotency-Key` to `POST .../{id}/reconciliations`.

The shared client methods are `CreateDelivery`, `GetDelivery`,
`GetDeliveryArtifact`, `ListDeliveries`, `AuthorizeDelivery` and `ReconcileDelivery`.
Artifact inspection alone permits a 17 MiB response envelope; ordinary client
responses retain their 2 MiB limit. MCP exposes proposal and inspection tools,
including exact artifact reads within its existing 2 MiB data limit, but no human
authorization tool. Use the authenticated artifact API for a larger complete result.

No automatic merge or deployment occurs. Deployment observations describe exact
provider records for the published head or independently observed merge commit.
They preserve environment and status provenance; `productionOutcome` remains
`not_observed`. Missing, unexecuted, truncated and unavailable evidence cannot pass.

## Webhook reconciliation

Run a separate loopback listener behind an operator-managed HTTPS reverse proxy.
The [listener contract](../../api/webhooks.openapi.yaml) is separate from the
authenticated control-plane API.
Use a webhook secret of 32–256 bytes, distinct from all publication/API tokens:

```json
{
  "webhooks": [{
    "workspaceId": "team",
    "repositoryId": "application",
    "provider": "github",
    "host": "github.com",
    "providerId": "123456",
    "secretFile": "/absolute/operator/application-webhook-secret"
  }]
}
```

```bash
export CONDUCTOR_WEBHOOK_ADDRESS=127.0.0.1:8091
export CONDUCTOR_WEBHOOKS_FILE=/absolute/operator/webhooks.json
go run ./cmd/conductor-webhooks
```

Forward only `/webhooks/github/application` or `/webhooks/gitlab/application` to
this listener. GitHub events supported are ping, pull request, check run, commit
status, push and deployment status. GitLab events supported are merge request,
pipeline, push and deployment. Configure JSON payloads and GitHub HMAC-SHA256 or the
GitLab secret-token profile. GitHub's authenticated ping acknowledges transport only;
it does not assert a configured provider integration or completed publication.

The receiver permits eight concurrent requests and 1 MiB payloads. Signed/token
validated event IDs and digests deduplicate in PostgreSQL. Reconciliation fetches
provider state under the original human authority. Deployment/push hints may fan
out to at most 100 published proposals in the repository to discover a newly merged
commit; overflow fails explicitly, preserving retryability. Other events match an
exact known head/merge commit. Event bodies and secrets are not retained.

## Bounds and recovery

The publisher accepts a retained bundle up to 32 MiB, an 8 MiB patch, at most 128
changed regular files, 8 MiB per resulting file and 32 MiB total changed content.
Only regular 0644/0755 files are supported. Offline Git gets 45 seconds. Provider
invocations allow 320 requests, 2 MiB per response and 64 MiB total responses, with
fixed-host TLS and per-request timeouts. The Temporal activity allows three bounded
attempts; the workflow's deadline is 30 minutes.

GitHub deployment coverage is at most 20 records per exact head/merge commit and
100 retained status records per deployment. GitLab scans 100 newest deployments
and reads matching records. Coverage gaps and partial histories remain explicit.

If a request loses acknowledgment, retry the same captured input and key explicitly.
Adapters inspect their deterministic branch and unique marker before creating
anything. They never overwrite an existing branch. A changed base or result tree,
permission revocation, cancelled run or stale approval stops further work. A partial
provider branch can remain; inspect it before making a fresh proposal.

A changed Temporal cluster, known missing history or inconsistent receipt becomes
unresolved. Do not reset database observations or start replacement workflows to
hide uncertainty. A previously committed operation receipt remains recoverable after
revocation without granting fresh source/provider access or public inspection.

## Verification

```bash
CONDUCTOR_TEST_DATABASE_URL=postgres://conductor:conductor@127.0.0.1:55432/conductor?sslmode=disable \
  go test -race ./internal/delivery ./internal/deliveryworker ./internal/store ./tests/acceptance
CONDUCTOR_TEST_TEMPORAL=1 CONDUCTOR_TEMPORAL_CLI=/absolute/path/to/temporal \
  go test -race ./internal/deliveryworker -run TestPublicationRuntimeLive -count=1
```

Use the configured owned test database URL; the shown port is this session's isolated
acceptance instance, not a requirement for normal development. Tests own schemas and
Temporal processes, never restart the selected database, and clearly distinguish
real Git/HTTP/Temporal protocol checks from controlled provider data and synthetic
receipt fixtures. Run the repository's full Go/race/vet and OpenAPI/doc checks before
merging changes. Live repository writes require an explicitly authorized test target.

## Browser controls

Sign in, select a workspace/repository, open **Delivery** and choose **Refresh deliveries and access**.
Inspect a shared proposal, or open the proposal form and copy the exact run ID,
retained task ID and artifact digest from coordinated execution. Preview the inputs
before recording. A lost creation response offers an exact retry with the same key.

Choose **Inspect complete implementation artifact**. Inspect the displayed patch
for each repository, producer output and independent checks, then acknowledge the
review. Only a human with publication permission can confirm the displayed proposal
digest. No automatic read occurs inside confirmation. A lost decision response
clears the artifact and requires renewed inspection; it never silently authorizes
newer content. Large artifacts use their dedicated 17 MiB response envelope.

After the trusted publisher retains a provider observation, request an explicit
provider refresh and inspect again later. A lost refresh response offers the same
captured key and digest for retry. Old timestamps, absent checks and incomplete
deployment coverage remain visible. Provider facts cannot establish a production
outcome. The workbench never merges or deploys.

Build the web app and run actual signed browser acceptance with the database and
Python environment documented in [browser setup](browser-sign-in.md):

```bash
CONDUCTOR_TEST_BROWSER=1 go test -race ./tests/acceptance -run TestBrowserRepositoryDeliveries -count=1
```

This uses real browser/API/PostgreSQL paths and explicitly synthetic retained worker
and provider evidence. It verifies larger complete patches, tampering rejection,
escaped source, exact retries, authorization recovery, read-only controls and denied
source clearing. Separate Docker and provider HTTP suites verify execution/adapters.

A transient database error during publication is an uncertain result. The bounded
Temporal retry rechecks the exact retained operation receipt before loading source,
reading credentials or contacting GitHub/GitLab. This recovers a receipt whose
commit acknowledgment was lost without republishing it. Typed permission, binding,
configuration and artifact failures remain terminal; database error details never
enter workflow history. SDK and owned Temporal tests exercise this recovery path.
