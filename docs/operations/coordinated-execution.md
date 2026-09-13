# Coordinated execution setup and inspection

**Partial:** these commands persist and inspect shared plans and execution intent.
The trusted execution worker and full runtime acceptance are being connected in
the following stack entries. An accepted authorization alone does not prove that
a producer started, completed or passed verification.

Apply migrations through 006, configure the documented OIDC API, and explicitly set
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

Database tests own isolated schemas and preserve development data. Temporal SDK
tests prove sequence behavior, not process restart or external model compatibility.
Those runtime checks remain required before reporting the full execution workflow
verified. See the [coding worker guide](coding-workers.md) for actual container
verification, source bounds, provider credential isolation and remaining paid-model
acceptance requirements.
