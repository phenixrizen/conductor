# Background repository context

**Status: Partial.** The API, CLI, browser/terminal controls, PostgreSQL outbox,
Temporal worker, and bounded GitHub/GitLab read adapters are present. This profile uses a trusted local Temporal
development server. Remote Temporal authentication and production deployment
validation remain later work. Provider fixtures exercise the pinned protocols; live GitHub/GitLab compatibility has not
been verified with repository-limited read credentials.

An authorized engineer or agent requests files from one exact commit. The service
saves that request, collects in the background, and exposes the same result to
other repository readers. Collecting does not edit a package. An author explicitly
attaches an inspected receipt to create a new revision; its design needs review.

## Prepare the services

First configure [authenticated review](authenticated-review.md). Collection requires
OIDC and both workspace and canonical repository selection on every command. Local
actor headers cannot use it. Existing repository author permission allows collection
only after operator enablement; read permission allows inspecting retained text.

Apply ordered migrations 001–004 to an empty database. For a database already on
migration 003, apply **only** the new migration, once, through its trusted operator
connection:

```bash
psql "$DATABASE_URL" --set ON_ERROR_STOP=1 --single-transaction \
  --file migrations/004_context_collections.sql
```

For the standard Docker development database, pipe the migration to support Snap
checkouts outside the home directory:

```bash
docker exec -i conductor-local-postgres-1 psql -U conductor -d conductor \
  --set ON_ERROR_STOP=1 --single-transaction < migrations/004_context_collections.sql
```

Retain existing data and apply missing earlier migrations first. The local database
startup script initializes empty databases with all migrations and reports missing
upgrades; it does not silently upgrade an existing schema.

Install the official [Temporal CLI 1.8.3 release](https://github.com/temporalio/cli/releases/tag/v1.8.3)
and verify its published archive checksum before extracting it. The tested Linux
amd64 archive SHA-256 is
`6f0afac1e9ddea71f480c43a49f5db5167a244c21db923707f069a79bcabdfea`.
`temporal --version` must report CLI 1.8.3 and server 1.31.2. The release binary avoids
requiring Go 1.26.4 to build the CLI; Conductor uses Go 1.24.

Run the persistent local workflow server in its own terminal:

```bash
./scripts/start-local-temporal.sh
```

The script binds `127.0.0.1:7233`, creates namespace `conductor`, and keeps SQLite
history under `$XDG_STATE_HOME/conductor/temporal` or
`$HOME/.local/state/conductor/temporal`. `CONDUCTOR_TEMPORAL_STATE_DIR` selects another
owned directory. Keep that directory across restarts. This is development storage,
not a production availability or security profile.

## Enable one repository

Register the repository with its real stable numeric provider ID using the existing
operator access configuration. The following synthetic example adds a read
integration for an already registered GitHub repository:

```json
{
  "contextIntegrations": [{
    "workspaceId": "workspace-example",
    "repositoryId": "repo-example",
    "profile": "github-rest/2026-03-10",
    "locator": "example/synthetic-project",
    "credentialId": "example-read",
    "enabled": true
  }]
}
```

Save it as an operator-controlled file, then apply it:

```bash
go run ./cmd/conductor-admin apply --operator local-operator --file integration.json
```

For GitLab.com use profile `gitlab-rest/v4-19.3` and an empty `locator`. The managed
repository record supplies the canonical provider, host, and project ID. This first
profile supports only `github.com` and `gitlab.com`. GitHub owner/name locates the
repository; numeric ID checks before and after reading establish the observed
identity. Rename/rebinding conflicts require operator reconciliation and a new
request. No client URL or profile can override this binding.

Give the worker a repository-limited **read** credential. GitHub's Git-object reads
require Contents read access. The GitLab project/commit/tree/blob REST profile needs
`read_api`; a `read_repository` scope alone is not claimed sufficient. Provider token
creation, automatic refresh and account provisioning are not implemented. See the
[researched permission boundaries](../architecture/context-integration-research.md).

Keep credentials outside the checkout. A worker catalog contains references, not
tokens; every token path must be absolute:

```json
{
  "credentials": [{
    "id": "example-read",
    "workspaceId": "workspace-example",
    "repositoryId": "repo-example",
    "provider": "github",
    "host": "github.com",
    "providerId": "12345",
    "tokenFile": "/home/example/.config/conductor/provider-read-token"
  }]
}
```

Replace the synthetic IDs and path with the registered values. Protect the catalog
and token files for the worker operator, for example with mode `0600`. Tokens must
be nonempty printable ASCII, at most 16 KiB, with at most one final LF. Catalogs are
bounded to 1 MiB and 100 distinct credential references. A credential must match
the workspace, Conductor repository, provider, host, and stable provider ID exactly.
The worker rereads its selected token file for each fresh activity attempt; token
rotation can repair an unchanged binding. Restart the worker to change its catalog.

Start the worker with the same PostgreSQL dataset as the API:

```bash
export CONDUCTOR_TEMPORAL_MODE=local
export CONDUCTOR_TEMPORAL_ADDRESS=127.0.0.1:7233
export CONDUCTOR_TEMPORAL_NAMESPACE=conductor
export CONDUCTOR_CONTEXT_CREDENTIALS_FILE=/home/example/.config/conductor/worker.json
go run ./cmd/conductor-worker
```

The worker requires a literal loopback Temporal address and an explicit namespace.
It checks the actual cluster/namespace identity and at least 24 hours of history
retention. Its task queue is fixed at `conductor-collection-v1`. Remote Temporal,
TLS/mTLS, hosted-service credentials, and untrusted shared-host operation are outside
this deployment profile. Do not expose the local Temporal port to untrusted clients.

Start or restart the configured OIDC API with `CONDUCTOR_CONTEXT_COLLECTIONS=1`.
Unset or `0` keeps collection commands unavailable; other values and enabled local
mode fail startup. A successful collection response proves committed intent even
when the worker is temporarily offline. It does not claim provider availability.

## Request, inspect, and attach

Use the existing HTTPS API URL and API token-file environment settings. All flags precede the
positional ID. Supply a **full lowercase 40-hex commit ID** and 1–32 unique explicit
relative paths; branches and abbreviated IDs are rejected. Paths are sorted before
hashing and collection. Choose and retain an idempotency key for this request:

```bash
export CONDUCTOR_URL=https://conductor.example.test
export CONDUCTOR_TOKEN_FILE=/home/example/.config/conductor/access-token
go run ./cmd/conductor context-collect \
  --workspace workspace-example --repository-id repo-example \
  --commit 1111111111111111111111111111111111111111 \
  --path specs/example/spec.md --path docs/adr/example.md \
  --idempotency-key example-context-1
```

The synthetic commit is illustrative; select a real inspected commit in your
configured repository. Repeat the same key and input after an uncertain API result
to recover the original request. Changed input conflicts; changing key creates new
work. Keys are scoped to requester and canonical repository, 1–128 printable ASCII
characters without whitespace or commas. CLI commands never retry automatically.

Use the same identity/scope flags with these commands:

| Command | Result |
|---|---|
| `context-collections --limit 20` | A bounded page of shared requests; pass returned `nextBefore` as `--page` |
| `context-collection COLLECTION_ID` | Immutable request, optional receipt and last execution observation |
| `context-cancel COLLECTION_ID` | Request cancellation; only its current authorized requester may do this |
| `context-attach --collection-id COLLECTION_ID --digest RECEIPT_DIGEST --expected-revision 3 CHANGE_ID` | Attach the inspected stored snapshot as a new package revision |

Inspect the complete receipt, coverage, package revision, and digest before attaching.
Attachment never refreshes or recollects. A stale revision, wrong digest, or repeated
historical package digest returns a conflict. Inspect again before a new command.
Unknown fields outside the snapshot survive attachment. Version 2 remote snapshots
must match the entire stored receipt JSON under the same workspace/repository;
injected extra snapshot fields are rejected instead of receiving trusted provenance.
Version 1 local snapshots and their unknown extensions retain their original meaning.

## Read the result honestly

`receipt` is the immutable source result. Each path is `collected`, `missing`,
`unavailable`, or `truncated`. Complete text has its Git blob ID and SHA-256 digest.
The collector retains no partial file. LFS pointers remain pointer text; symlinks,
submodules, redirects, binary/control bytes, and repository scripts are not followed.
Provider commit/tree associations are observed over TLS; raw commit/tree hashes and
signatures are not independently verified. Collection is not a passing check.

`execution` is a timestamped observation, with namespace, workflow ID, run ID,
state and `current`. It is absent before any observation. `current` becomes false
after 30 seconds and is always false for `unavailable` or `unresolved`. A lost start
acknowledgment can be `unavailable` with an empty run ID. A receipt can remain valid
while execution progress is unavailable. Workflow completion can still contain gaps.
`cancelRequestedAt` records intent; only observed `cancelled` confirms the execution
stopped. Revocation prevents later provider operations and final publication once
observed, but cannot undo a request already sent or a receipt already committed.

## Browser and terminal controls

Sign in and select the canonical workspace/repository before using **Shared context
collections** in the browser. **Refresh collections** loads one page of 20 shared
requests; **Load more collections** replaces it with the next page. Inspect a
request to see source identity, receipt digest, per-path coverage and the last
execution observation. Use **Request repository context** to enter the exact
commit, literal paths and retained idempotency key. After a lost acknowledgment,
**Retry same request** sends that unchanged input and key; it does not start new work.

Inspect the current package and receipt, then choose **Attach receipt to inspected
revision**. Confirmation displays the package revision/digest and receipt identity.
It sends no refresh. If the result is stale or uncertain, use the displayed recovery
buttons to inspect both captured records before another attachment or approval.
An attachment creates a new draft and retains earlier approval only in history.
**Request cancellation** separately confirms intent from the current requester.

In the authenticated terminal, **g** switches between packages and collections.
**c** imports and previews a strict request JSON file (at most 64 KiB) containing
`commit`, `paths` and `idempotencyKey`; **s** confirms or explicitly retries it.
**Enter/o** inspects, **n/p** changes pages, **x** confirms cancellation, and **t**
confirms attachment to the retained current package. **r** recovers access and
refreshes with the same credential, clearing the package target. Before attaching
after recovery, switch back to packages, inspect the current revision, then return
to collections and inspect the receipt. Ordinary approval requires renewed package
inspection. See the [terminal guide](terminal-review.md) for request examples.

Both workbenches age observations locally without polling; refresh explicitly for
later progress. Reader access allows inspection without author controls. Browser
scope/account changes and either interface's access failure discard inspected
source, drafts and confirmation. Local mode cannot collect remotely.

The browser renders version 2 coverage while retaining complete JSON. A JSON
receipt reference alone is not trusted linkage: **Inspect linked collection** reads
the stored receipt in the selected scope and compares the complete snapshot.
The terminal displays escaped source and JSON. Existing version 1 extension fields
remain uninterpreted. Neither a linked receipt nor completed execution establishes
passing verification. See the [browser guide](browser-sign-in.md) for session setup.

## Bounds and recovery

| Boundary | Current limit |
|---|---|
| Admission | 20 unresolved/active requests per repository; pending cancellation still counts until observed terminal or receipted |
| Worker concurrency | 2 activities and 2 workflow tasks per worker process |
| Text | 64 KiB per file; 256 KiB total, allocated in sorted path order |
| Provider reads | 128 HTTP requests; 128 KiB per response; 2 MiB total response bytes; 32 KiB response headers |
| Tree enumeration | 1,000 entries per directory; incomplete trees are unavailable evidence |
| HTTP | 10 seconds per operation, fresh HTTP/1 connection, no redirect/proxy/cookie state or hidden connection retry |
| Temporal activity | 2 minutes per attempt, 10 minutes total scheduling, at most 3 attempts, 5-second heartbeat timeout |
| Temporal workflow | 12 minutes, no workflow retry/reset/continue-as-new support |
| Retry | Temporal controls backoff; provider retry-after over 30 seconds ends this attempt sequence instead of retrying too soon |
| Dispatch | 45-second fenced lease; 5-second retry eligibility; bounded polling and observation batches |
| Unknown start | Reconcile for at most 1 hour; actual namespace retention must be at least 24 hours |

Restarting the API, dispatcher, worker, or the same persisted Temporal server can
recover committed work. A lost activity acknowledgment returns the first committed
receipt without fetching again, even after later revocation. A known run whose
history is missing becomes `unresolved`; a completed receipt prevents redispatch
after history retention. Changing cluster/namespace identity cannot redirect an
old request. A running worker detects target replacement and stops dispatching;
restart it only after checking the intended service and retained history.

Retain workflow history during unresolved dispatch. Do not manually delete/reset
executions or recreate namespaces to recover them: deletion after a lost start
acknowledgment inside the one-hour horizon cannot be distinguished from a start
that never reached Temporal. Deterministic workflow IDs do not provide globally
exactly-once network calls. Stop the worker and investigate a known history loss.
`unresolved` is sticky and consumes admission capacity; automatic operator repair
or administrative clearance is not implemented. Do not edit database observations
to manufacture a terminal result or delete review history to free capacity.

Disable an integration through a new audited operator config with `enabled:false`
to prevent new requests and later authorized reads/publication. This does not erase
old receipts or grant a confirmed Temporal cancellation. New collection configuration
changes require an explicit new request; they never rebind an existing one.

## Acceptance

Run the full [contributor checks](../../AGENTS.md). For the additional owned Temporal
and PostgreSQL paths:

```bash
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
CONDUCTOR_TEST_TEMPORAL=1 \
go test -race ./internal/contextworkflow ./internal/store ./internal/collectionworker ./tests/acceptance
```

`CONDUCTOR_TEMPORAL_CLI` can select the verified binary. Missing opt-in is a skip;
missing dependencies after opting in fail. Tests own temporary PostgreSQL schemas,
Temporal SQLite storage and subprocesses. They never restart the database selected
by that URL or delete development data. The separate process-restart opt-in in the
[local guide](local-development.md) owns its own PostgreSQL container and volume.

Keep real process-recovery evidence distinct from SDK unit tests, controlled provider
fixtures, and synthetic OIDC issuer tests. These checks do not certify a real identity
vendor, live repository provider compatibility, or production operation.

The 2026-09-13 verification ran full Go and race suites with live PostgreSQL,
browser/PTY acceptance and an owned API/PostgreSQL process restart. Separate actual
Temporal tests covered persistent server restart, duplicate retained runs, worker
death after receipt commit, and server replacement behind the same address. The
combined collection acceptance restarted signed API, dispatcher and worker helper
processes using the production components, deliberately lost start/activity
acknowledgments, and recovered one run and one receipt. After author revocation,
receipt recovery made no additional provider reads. Cancellation, final permission
checks, shared inspection, attachment/history, and source/token exclusion from
decoded history and logs were exercised too.

Those helper processes inject exact crash boundaries and use a controlled GitHub
HTTP fixture. They do not certify deployed worker configuration or a live vendor;
the worker executable has separate configuration, file-boundary and log tests.
The PostgreSQL process remained running in the combined Temporal test; its restart
was proved separately with the owned Docker acceptance.

Collection workbench acceptance additionally uses actual Chromium and a real PTY
with signed identity and isolated PostgreSQL. It covers bounded shared pagination,
receipt gaps, exact stale/successful attachment, lost create acknowledgment with
one explicit same-key retry, cancellation intent and revoked/read-only access.
Browser checks cover lost attachment acknowledgment, display aging without polling,
late scope responses, receipt linkage, stalled denial bodies and mobile layout.
These tests seed immutable receipts; provider reads and Temporal execution are
verified by the separate suites above. Build the web app, then run:

```bash
CONDUCTOR_TEST_BROWSER=1 go test -race ./tests/acceptance -run Browser
CONDUCTOR_TEST_TERMINAL=1 go test -race ./tests/acceptance -run AuthenticatedTerminal
```

Both commands require `CONDUCTOR_TEST_DATABASE_URL`; the browser command also needs
`CONDUCTOR_BROWSER_PYTHON` with the pinned Playwright dependency and Chromium.
