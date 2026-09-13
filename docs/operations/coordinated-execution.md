# Coordinated execution setup and inspection

**Implemented:** shared plan admission and a trusted local Temporal/Docker runtime.
An accepted authorization alone does not prove that a producer started, completed
or passed verification. Inspect separate task receipts and observed execution.

Apply the ordered migrations through 008, configure the documented OIDC API, and explicitly set
`CONDUCTOR_COORDINATION=1` on `conductord`. Local actor mode rejects this capability.
Use the trusted `conductor-admin` provisioning command to add `executionGrants`:

```json
{"executionGrants":[{"repositoryId":"application","principalId":"engineer","canExecute":true,"canPublish":false}]}
```

The principal must already have workspace membership and repository read/author
grants. Provision execution permission for every repository in a plan. False values
revoke the corresponding capability without deleting historical attribution.
GET `/api/v1/execution-capabilities` reports the selected repository's effective
human execution and publication capabilities; it cannot grant access elsewhere.

Provision the public execution profile separately from its worker credential file.
The operator command validates the actual adapter contract and computes its digest:

```json
{"executionProfiles":[{"workspaceId":"engineering","id":"synthetic-check","image":"sha256:REPLACE_WITH_64_HEX_IMAGE_DIGEST","profile":{"adapter":"command/v1","command":["/bin/true"]},"enabled":true}]}
```

GET `/api/v1/execution-profiles` lists up to 100 enabled profiles with an explicit
truncation flag. Inspect each task's `profileDigest` and immutable `image` before
authorization. The plan must also carry each repository's `fullSourceDigest`, matching
the inspected whole-source graph. Missing pins permit a proposal for discussion,
but cannot authorize execution. Updating or disabling an operator profile invalidates
future admission under its old pin. Never put credentials in public profile fields
or command arguments; the executor's private credential catalog stays outside this API.

POST `/api/v1/coordination-runs` accepts one `Idempotency-Key` and the strict
`CoordinationPlan` documented in [OpenAPI](../../api/openapi.yaml). Inspect that plan's
exact digest before POST `/{id}/authorization` with `{"digest":"..."}`. Cancellation
uses POST `/{id}/cancellation` with the same inspected digest. Commands do not refresh
the plan or retry automatically. Reuse an identical proposal/key only to reconcile
an uncertain create response. An edit requires a new proposal and authorization.

GET `/api/v1/coordination-runs?limit=20` provides bounded shared discovery; GET
`/{id}` inspects a run. Readers need access to every included repository. A forbidden
or unauthenticated response requires discarding private inspection. A stale package
approval or conflicting path returns a conflict; it is not an execution failure.

```bash
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  go test -race ./internal/store -run Coordination -count=1
go test -race ./internal/domain ./internal/coordinationworkflow ./internal/contextworkflow
```

Database tests own isolated schemas and preserve development data. The actual
coordinator acceptance uses signed synthetic OIDC, whole-source Git bundles, the
native CodeGraph image, isolated Docker producers and independent checks. It runs
two producers in parallel and a descendant reading both repositories, retains
cumulative patches, checks metadata-only Temporal history, and restarts an owned
Temporal process with its completed history intact. Separate paths exercise an
intentionally orphaned Docker producer and revocation during actual production.
An additional process test kills and restarts the compiled executor during actual
production: the lost attempt remains unresolved, its producer never starts again,
and its write reservations remain retained.
These tests do not claim paid Codex/Claude execution or live identity compatibility.

Build the [pinned worker image](coding-workers.md), then provision its exact public
profile and image as above. The executor's separate private catalog repeats that
public configuration and adds only its bounded model-credential reference:

```json
{"profiles":[{"workspaceId":"engineering","id":"synthetic-check","image":"sha256:REPLACE_WITH_64_HEX_IMAGE_DIGEST","profile":{"adapter":"command/v1","command":["/bin/true"]},"allowProviderNetwork":false}]}
```

Store the catalog as a private regular file with mode 0600; symlinks and unknown
configuration fields are rejected. Native adapters require an explicit operator `model`, `allowProviderNetwork`
true and an absolute `credentialFile` referring to a private provider API key file.
Use the exact Codex/Claude profile fields and limitations in the
[coding worker guide](coding-workers.md). Never put publication, deployment or
Conductor bearer credentials in either execution catalog.

```bash
go build -o /tmp/conductor-executor ./cmd/conductor-executor
export DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable'
export CONDUCTOR_TEMPORAL_MODE=local
export CONDUCTOR_TEMPORAL_ADDRESS=127.0.0.1:7233
export CONDUCTOR_TEMPORAL_NAMESPACE=conductor-local
export CONDUCTOR_EXECUTION_PROFILES_FILE=/absolute/private/execution-profiles.json
/tmp/conductor-executor
```

The executor supports a literal loopback Temporal address, the documented retained
namespace, and the local Docker daemon. `CONDUCTOR_DOCKER_BINARY` optionally selects
its executable. It checks schema readiness without applying migrations. Public
profile/image pins must equal the private operator catalog before work starts;
changing either requires a newly reviewed plan. Source bundles, prompt text,
commands, patch payloads and credentials never enter workflow history or worker logs.

Every activity records its attempt before starting a producer. Redelivery recovers
a committed receipt, or reconciles the existing attempt after its database deadline;
it does not start another producer. Exact Docker cleanup is confirmed independently.
A running activity rechecks current authority every second and cancels if that
check fails. The receipt commit rechecks authority atomically; revoked output is
not retained as a publishable artifact.

Cancellation is asynchronous. A task becomes stopped only after cleanup evidence;
write reservations require a terminal Temporal observation and all terminal task
receipts. Lost outcomes stay `unresolved` even after resource cleanup, preserving
reservations for operator investigation. Logs contain fixed operational labels.
Inspect the shared run and its receipt facts rather than retrying with a new key to
hide uncertainty. A zero-check analysis task can finish successfully, but does not
establish a verified implementation or satisfy publication verification requirements.

Run the actual local acceptance with existing immutable images:

```bash
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
CONDUCTOR_TEST_EXECUTION=1 CONDUCTOR_TEST_CODEGRAPH=1 CONDUCTOR_TEST_TEMPORAL=1 \
CONDUCTOR_TEST_PROCESS_RESTART=1 \
CONDUCTOR_TEST_WORKER_IMAGE="$CONDUCTOR_WORKER_IMAGE" \
CONDUCTOR_CODEGRAPH_IMAGE="$CONDUCTOR_CODEGRAPH_IMAGE" \
CONDUCTOR_TEMPORAL_CLI=/absolute/path/to/temporal \
  go test -race ./tests/acceptance -run TestCoordinated -count=1
```

The Temporal CLI profile is 1.8.3. Tests create their own process, SQLite history,
PostgreSQL schema and disposable Docker resources; they do not restart development
services. Absent opt-ins are explicit skips. Missing dependencies after opt-in are
failures. The recovered completed workflow is tested across a real Temporal restart. The
separate executor crash test requires `CONDUCTOR_TEST_PROCESS_RESTART=1`; it kills
the actual worker process, restarts it with the same private catalog and database,
and proves that the lost producer is reconciled without a replacement.

Retained task output can be inspected before publication through
`GET /api/v1/coordination-runs/{id}/artifact` with the exact `runDigest`, `taskId`,
and `artifactDigest` query pins. This returns failed checks and design reports
as well as successful patches. It grants no approval or publication authority;
current read permission for every repository in the run remains necessary.
See [terminal controls](release-terminal.md) for CLI and TUI inspection.
