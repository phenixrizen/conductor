# Local development and operations

## Prerequisites

- Go 1.24 or the version declared in `go.mod`.
- Docker with Compose for the local PostgreSQL dependency.
- Node.js and npm for the optional web workbench.

Dependencies must be installed from reviewed lockfiles. If a lockfile is absent,
treat the affected surface as incomplete; do not invent checksums.

## Start PostgreSQL and the API

```bash
docker compose -f deploy/local/compose.yaml up -d
export DATABASE_URL='postgres://conductor:conductor@localhost:5432/conductor?sslmode=disable'
go run ./cmd/conductord
```

The Compose volume mounts `migrations/` into PostgreSQL's initialization directory.
Initialization scripts run only when the database volume is first created. Until a
migration command is added, apply later migrations deliberately rather than
assuming a restarted existing volume was upgraded.

Configuration:

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | Required | PostgreSQL connection URL |
| `CONDUCTOR_ADDR` | `:8080` | API listen address |
| `CONDUCTOR_URL` | `http://localhost:8080` | CLI API base URL |

## Exercise the review flow

Create structured content:

```bash
cat > /tmp/package.json <<'JSON'
{
  "intent": {
    "request": "Reject a duplicate synthetic replay",
    "expectedBehavior": "A repeated replay key has one logical effect",
    "nonGoals": ["Change event ordering semantics"]
  },
  "scope": {
    "repositories": [{"id": "synthetic-claims", "baseline": "0123456789abcdef"}],
    "allowedPaths": ["internal/replay/**"]
  },
  "verification": {
    "requiredChecks": ["go test ./internal/replay/..."]
  }
}
JSON

go run ./cmd/conductor create --actor developer --file /tmp/package.json
```

Copy the returned ID, revision, and digest. Then submit and inspect revision 1:

```bash
go run ./cmd/conductor submit --actor developer --revision 1 CHG-...
go run ./cmd/conductor show --actor reviewer CHG-...
```

Approve only the revision and digest just inspected:

```bash
go run ./cmd/conductor approve \
  --actor reviewer \
  --revision 1 \
  --digest '<64-character digest>' \
  CHG-...
```

Edit `/tmp/package.json` to change the intended behavior, then revise using the
expected current revision. The current schema rejects duplicate content within a
change, so submitting the unchanged file is not a valid edit:

```bash
go run ./cmd/conductor revise \
  --actor developer \
  --revision 1 \
  --file /tmp/package.json \
  CHG-...
```

The response for revision 2 must report `approved: false`. Reusing the revision 1
approval command must return a `409 revision_conflict` rather than silently
approving revision 2.

## HTTP error contract

Errors contain stable machine-readable fields:

```json
{
  "error": {
    "code": "revision_conflict",
    "message": "approval does not match current revision",
    "correlationId": "..."
  }
}
```

Retain the correlation ID when reporting a failure. `400` means malformed or
invalid input, `401` means the local identity is missing or invalid, `404` means the
change was not found, `409` means inspected state is stale, and `422` means the
content is current but approval policy rejected the action.

## Checks

```bash
go test ./...
go test -race ./...
go vet ./...
npm --prefix apps/web ci
npm --prefix apps/web run typecheck
npm --prefix apps/web run build
```

Run the PostgreSQL integration tests against an explicitly selected test database:

```bash
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@localhost:5432/conductor?sslmode=disable' \
  go test -race ./internal/store -count=1 -v
```

Each test creates an isolated schema, applies the ordered migrations, and drops
its schema afterward. The database user needs permission to create schemas.
Without this variable the live tests report a skip. The suite covers lifecycle,
concurrent edits, audit rollback, missing packages, and connection-pool reopen
durability; pool reopen does not restart PostgreSQL.

A missing dependency, database, or scanner is **not** a passing check. Report the
check as unavailable and preserve the reason.

## Reset and troubleshooting

```bash
docker compose -f deploy/local/compose.yaml logs postgres
docker compose -f deploy/local/compose.yaml down
```

Adding `--volumes` destroys local review history and should be intentional:

```bash
docker compose -f deploy/local/compose.yaml down --volumes
```

Common failures:

| Symptom | Meaning/action |
|---|---|
| `DATABASE_URL is required` | Export the connection URL before starting `conductord`. |
| `revision_conflict` | Re-inspect the latest revision; do not retry approval blindly. |
| `approval_rejected` | Confirm the revision is submitted and the reviewer is independent. |
| Missing `go.sum` or npm lock | Generate and review it in a dependency-enabled environment. |
| Existing DB lacks a new table | Initialization SQL does not migrate existing volumes; apply the migration deliberately. |
