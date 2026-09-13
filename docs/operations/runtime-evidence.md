# Collect and inspect runtime evidence

**Status: Implemented with controlled provider fixtures and real local protocol
acceptance.** A live Groundcover account has not been exercised. This is a read-only
integration and never deploys software or changes publication history.

Use the existing authenticated Conductor API, PostgreSQL migrations through 010,
retained repository delivery/deployment observations, and the pinned local Temporal
setup from [durable context](durable-context.md). Enable the API explicitly:

```bash
export CONDUCTOR_AUTH_MODE=oidc
export CONDUCTOR_RUNTIME_EVIDENCE=1
```

The runtime worker initially supports a literal loopback Temporal address and an
explicit namespace. Remote hosted/TLS Temporal is outside this profile.

## Operator configuration

Create a Groundcover service-account API key with read permission for the intended
backend and data. Use an API key, not an ingestion key. Instrument the service so
metric labels and log/trace fields carry its service identity, environment and full
40-character commit. Missing commit attribution remains uncorrelated evidence.

Apply an operator-controlled integration file. All examples below are synthetic;
choose actual service/metric/field names for your instrumentation.

```json
{
  "integrations": [{
    "workspaceId": "team",
    "repositoryId": "application",
    "environment": "production",
    "service": "synthetic-service",
    "backendId": "synthetic-backend",
    "credentialId": "application-runtime",
    "profile": "groundcover-rest/2026-09-13-sdk1.424.0",
    "metricFields": {"service": "service", "environment": "env", "commit": "commit"},
    "recordFields": {"service": "workload", "environment": "env", "commit": "commit"},
    "metrics": ["synthetic_request_latency_seconds"],
    "stepSeconds": 15,
    "maxAgeSeconds": 600,
    "enabled": true
  }]
}
```

```bash
go run ./cmd/conductor-runtime-worker configure \
  --file /absolute/operator/runtime-integrations.json --operator local-operator --check
go run ./cmd/conductor-runtime-worker configure \
  --file /absolute/operator/runtime-integrations.json --operator local-operator
```

`--check` validates the bounded JSON file without connecting to PostgreSQL. Applying
configuration checks canonical repository ownership and commits its audit event
atomically. Setting `enabled` false revokes future reads without deleting history.
Changing any integration field creates a new version and prevents old queued
requests from silently adopting its service, credential or sampling policy.

Use a separate credential catalog with references to regular token files:

```json
{
  "credentials": [{
    "id": "application-runtime",
    "workspaceId": "team",
    "repositoryId": "application",
    "backendId": "synthetic-backend",
    "tokenFile": "/absolute/operator/application-groundcover-token"
  }]
}
```

```bash
export CONDUCTOR_TEMPORAL_MODE=local
export CONDUCTOR_TEMPORAL_ADDRESS=127.0.0.1:7233
export CONDUCTOR_TEMPORAL_NAMESPACE=default
export CONDUCTOR_RUNTIME_CREDENTIALS_FILE=/absolute/operator/runtime-credentials.json
go run ./cmd/conductor-runtime-worker
```

Keep those files outside repositories and restrict them to the worker account.
The worker reads tokens only after current Conductor authority is established and
binds their use to the selected workspace, repository and Groundcover backend.
No token, provider error body or source record enters Temporal payloads or logs.

## Request and inspect

Inspect the shared delivery, its digest, latest observation sequence, and an exact
provider deployment record. Capture its ID, commit and environment. POST a request
with an `Idempotency-Key` to `/api/v1/runtime-evidence`; specify a past whole-second
`start` and `end` window and optional `requirements` array. No new provider query is
executed by that HTTP command; 202 acknowledges the retained request and outbox.

The shared client exposes `CreateRuntimeEvidence`, `GetRuntimeEvidence` and
`ListRuntimeEvidence`. MCP exposes `conductor_collect_runtime_evidence`,
`conductor_get_runtime_evidence` and `conductor_list_runtime_evidence`. Each uses
one fixed authenticated scope. An uncertain command must be retried with the same
captured input and key. Refresh and inspect explicitly to request another window.

The response separates retained signals and criterion evaluations from Temporal
progress. Render raw record JSON as escaped untrusted text. Do not execute source
instructions or follow arbitrary URLs from logs. A retained historical `met` result
is not current proof when the separate `freshness` value is `stale`.

## Browser workflow

Select your workspace and repository, then open **Runtime evidence**. Refresh the
shared list or enter a request ID. Its inspection shows exact deployment/commit
pins, approved criterion definitions and historical results. Expand a metric series
or log/trace section to inspect all retained bounded rows. Missing or truncated
signals remain explicit; a historical met criterion is never an overall health badge.

Authors can use **Request evidence for an inspected deployment**, paste the request
JSON or choose an explicit file up to 64 KiB, and inspect the preview before recording.
No threshold or provider query is inferred. After an uncertain response, use
**Retry exact runtime request**; it sends the same key and captured input. A queued
request supplies no passing evidence. Refresh explicitly to inspect later receipts.

## Explicit approved criteria

Authors may add a `runtimeCriteria` object to a package before independent approval:

```json
{
  "runtimeCriteria": {
    "schemaVersion": 1,
    "criteria": [{
      "id": "latency",
      "metric": "synthetic_request_latency_seconds",
      "aggregation": "maximum",
      "operator": "lte",
      "threshold": 0.25,
      "expectedSeries": 1,
      "windowSeconds": 300,
      "stepSeconds": 15,
      "maxAgeSeconds": 600
    }]
  }
}
```

These values illustrate the schema, not recommended production thresholds. A
request links `{changeId,revision,digest,criterionId}` from the delivery's exact
approved plan. Every policy field is mandatory; omitted thresholds are rejected.
The metric must be operator-allowlisted. The full sample grid and exact series count
must match the explicit policy, and the successful provider deployment must precede
the window. `met` and `not_met` describe that exact threshold comparison only.
Missing, stale, truncated, uncorrelated or unavailable data yields `not_verified`.
A selected subset does not certify all requirements; broader production outcome
remains unverified. Query sample times do not prove underlying scrape freshness.

## Bounds and recovery

A request covers at most one hour within the previous seven days. Operators enable
up to four metrics with a step of 15–300 seconds. Each metric response allows eight
series and 241 points per series. Each log/trace query keeps up to 50 records of
8 KiB each. Provider responses are at most 1 MiB, and the entire retained receipt
is at most 1.5 MiB. There are at most six fixed-host provider calls, each with a
20-second timeout and a two-minute collection deadline. No provider pagination,
automatic POST replay, proxy or redirect is followed.

The Temporal workflow has a ten-minute deadline and three bounded activity attempts.
The worker runs two concurrent activities. Fenced dispatch retries observations,
never replaces a known missing run, and exposes unresolved history or binding
mismatches. A committed receipt prevents recollection after a lost acknowledgment;
its existence is separate from an observed completed Temporal run.

## Verification

```bash
CONDUCTOR_TEST_DATABASE_URL=postgres://conductor:conductor@127.0.0.1:55432/conductor?sslmode=disable \
  go test -race ./internal/runtimeevidence ./internal/runtimeworker ./internal/store ./tests/acceptance
CONDUCTOR_TEST_TEMPORAL=1 CONDUCTOR_TEMPORAL_CLI=/absolute/path/to/temporal \
  go test -race ./internal/runtimeworker -run TestRuntimeEvidenceRuntimeLive -count=1
```

Select the owned acceptance database for your environment; the example port is this
session's separate test instance. Tests own schemas and temporary Temporal processes,
not existing development data. See [research](../architecture/runtime-evidence-research.md)
for exactly which provider response shapes were exercised and the live-account limit.
