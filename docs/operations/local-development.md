# Local development and operations

## Prerequisites

- Go 1.25 or later, using the tested Go 1.26.8 toolchain declared in `go.mod`.
- Git and Make.
- Docker with Compose for the local PostgreSQL dependency.
- Node.js and npm for the optional web workbench.

Dependencies must be installed from reviewed lockfiles. If a lockfile is absent,
treat the affected surface as incomplete; do not invent checksums.

## Start PostgreSQL and the API

From the checkout, run this in your first terminal:

```bash
make run
```

This starts the persistent local PostgreSQL container and runs the API in the
foreground on `127.0.0.1:8080` with explicit local authentication. `make dev` is an
alias. Ctrl+C stops the API; PostgreSQL remains running. Use `make db-stop` when
you also want to stop the database without removing its volume.

In a second terminal, connect to the running instance:

```bash
make tui
```

The terminal uses local actor `developer`; use `make tui ACTOR=reviewer` for a
separate review session. Press `q` to exit. The TUI target connects only: it does
not start an API or database. Start local sessions without authenticated token or
workspace settings. For shared identity and release views, follow the
[authenticated terminal setup](terminal-review.md#authenticated-workspace-review).

The `db-up` target reuses `scripts/start-local-db.sh`, which pipes Compose
configuration into Docker so Docker Snap can work with a checkout under `/mnt`.
PostgreSQL data lives in a named volume. The script then runs the trusted
`conductor-db migrate` operator on the Go host. It initializes an empty database
with a checksum ledger, validates recorded checksums, and applies pending
migrations in one transaction. The API starts only after migration succeeds.
Before upgrading an existing installation, stop its API/workers and retain a
verified backup as described in the [release migration procedure](release.md#apply-or-upgrade-the-database).

### Recover an older local database

Earlier versions applied SQL without recording migration history. Startup refuses
to guess how far those databases were migrated; a table's presence alone cannot
establish that a complete migration ran. If startup reports an **existing untracked
database**, back it up and compare its complete schema with the original ordered
migration files. Then record only the verified already-applied count:

```bash
# Select the local database just inspected; use its port if different from 5432.
# Replace both placeholders after inspecting the schema and verifying a backup.
DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  make db-migrate BASELINE='<verified-count>' OPERATOR='<operator-label>'
make run
```

The explicit URL prevents an unrelated inherited `DATABASE_URL` from receiving
the local database's baseline. Match the port printed by the failed startup and
retain any custom project/port settings for `make run`.
`db-migrate` always uses `DATABASE_URL`; without an override, Make defaults to the
selected local PostgreSQL port.
It records legacy entries as `operator_baseline` and applies all remaining
migrations atomically. Subsequent `make run` calls validate that ledger; do not
supply the baseline again. `OPERATOR` is an audit label, not authenticated identity.
Never reset the volume or replace recorded checksums to get past a migration error.
A failed or uncertain migration requires inspecting its error and ledger before
retrying; the helper does not automatically replay the command.

If Docker group membership is not active in the current shell, start a new login
session or use an absolute checkout path:

```bash
sg docker -c 'make -C /absolute/path/to/conductor run'
```

Replace the path with your checkout. This also avoids Docker Snap's working
directory restriction for paths outside your home directory.

## Make targets and configuration

`make` and `make help` print the available commands. Long-running commands stay in
the foreground so each terminal has a clear owner to stop with Ctrl+C.

| Target | Behavior |
|---|---|
| `run`, `dev` | Start local PostgreSQL, then run the API |
| `serve` | Run only the API using the selected database and authentication settings |
| `tui` | Connect the terminal workbench to an already running API |
| `db-up` | Start local PostgreSQL and apply checked migrations |
| `db-migrate` | Apply migrations to `DATABASE_URL`; accept an explicitly inspected legacy `BASELINE` |
| `db-stop` | Stop PostgreSQL and retain its volume |
| `db-logs` | Follow PostgreSQL logs |
| `web-install` | Install frontend dependencies with `npm ci` |
| `web-dev` | Install dependencies and start the Vite browser workbench |
| `temporal` | Run the existing local Temporal profile with persistent history |
| `test`, `check`, `web` | Run the existing Go or frontend checks |

Environment variables and Make command-line settings can override defaults:

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | `postgres://conductor:conductor@127.0.0.1:$(CONDUCTOR_POSTGRES_PORT)/conductor?sslmode=disable` | Make's local PostgreSQL connection; required when invoking the API directly |
| `CONDUCTOR_LOCAL_PROJECT` | `conductor-local` | Compose project and persistent volume namespace |
| `CONDUCTOR_POSTGRES_PORT` | `5432` | Local PostgreSQL host port; integer 1–65535, always bound to `127.0.0.1` |
| `BASELINE`, `OPERATOR` | `0`, `local-development` | Explicit migration count and operator audit label for `db-migrate` only |
| `CONDUCTOR_ADDR` | `127.0.0.1:8080` | API listen address; local mode requires literal loopback |
| `CONDUCTOR_AUTH_MODE` | `local` | Make's default authentication mode; use `oidc` with the required provider settings for shared work |
| `CONDUCTOR_OIDC_ISSUER` | Required in OIDC mode | Exact HTTPS identity issuer |
| `CONDUCTOR_OIDC_AUDIENCE` | Required in OIDC mode | API access-token audience |
| `CONDUCTOR_URL` | `http://$(CONDUCTOR_ADDR)` | Make's terminal API URL |
| `CONDUCTOR_API_URL` | `$(CONDUCTOR_URL)` | Vite's API proxy target |
| `ACTOR` | `developer` | Local TUI identity; omitted when a token setting is present |
| `VIEW`, `FILE`, `CHANGE` | Empty | Optional TUI starting view, JSON file, and change or record ID |

For a different local API port, use the same setting in both terminals:

```bash
make run CONDUCTOR_ADDR=127.0.0.1:8081
# In the second terminal:
make tui CONDUCTOR_ADDR=127.0.0.1:8081
```

Use `make serve` for an existing database or an already configured OIDC environment;
it performs no database startup or migration. Set `DATABASE_URL` and the applicable
identity settings before invoking it. Inherited OIDC or integration settings are
still validated by the API; local mode cannot enable authenticated features.
`make run` starts and migrates its Compose database even if you override
`DATABASE_URL`; prefer `serve` for a separately managed database. To run an isolated
local instance, set a distinct `CONDUCTOR_LOCAL_PROJECT`, `CONDUCTOR_POSTGRES_PORT`
and `CONDUCTOR_ADDR`, and retain those settings for later startup, migration and
shutdown. Startup always migrates its selected loopback Compose database; it does
not use an unrelated inherited `DATABASE_URL`.

`make temporal` requires the verified CLI 1.8.3 / server 1.31.2 profile. It starts
Temporal only, not collection or coding workers. See the
[background context guide](durable-context.md) for worker configuration and
`CONDUCTOR_TEMPORAL_CLI` / `CONDUCTOR_TEMPORAL_STATE_DIR` overrides.

## Start the local browser

With the local API running, start Vite in another terminal:

```bash
make web-dev
```

Open the loopback URL printed by Vite, normally `http://127.0.0.1:5173`. Its `/api`
proxy defaults to the API selected by `CONDUCTOR_URL`; set `CONDUCTOR_API_URL` to
override it explicitly. Local mode reviews unscoped development
packages. Shared workflow tabs require the authenticated setup below.

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
invalid input, `401` means identity is missing or invalid, `403` means workspace or
repository capability is denied, `404` means the change is missing or inaccessible,
`409` means inspected state is stale, and `422` means the
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

Run the separate process-restart suite with working Docker access:

```bash
CONDUCTOR_TEST_PROCESS_RESTART=1 \
  go test -race ./tests/acceptance -run TestProcessRestartDurability -count=1 -v
```

It starts the compiled API as a separate process and provisions a unique temporary
PostgreSQL container and named volume. It checks API-only restart, then restarts
both API and PostgreSQL and checks exact package content, historical approvals,
audit records, stale-request rejection, and new writes through fresh clients.
The test removes only its own resources. It does not use or restart the database
in `CONDUCTOR_TEST_DATABASE_URL` or your persistent development database.

The suite skips unless explicitly enabled. Once enabled, unavailable Docker,
an unavailable pinned PostgreSQL image, or failed readiness is a test failure.
For changes to local startup and migrations, also run the real Make/Compose gate:

```bash
CONDUCTOR_TEST_PROCESS_RESTART=1 \
  go test -race ./tests/acceptance -run TestLocalMakeMigrationStartup -count=1 -v
```

It owns separate projects, ports and volumes for fresh startup, tracked upgrades,
and migration-001 legacy recovery. It checks exact history and migration records,
refusal without an inspected baseline, database targeting, and actual PostgreSQL
stop/start with retained data. The existing development database is never selected.

If the current shell lacks its newly assigned Docker group, use:

```bash
sg docker -c 'CONDUCTOR_TEST_PROCESS_RESTART=1 go test -race ./tests/acceptance -run TestProcessRestartDurability -count=1 -v'
```

For interactive terminal acceptance, see the
[terminal review guide](terminal-review.md). The browser and terminal share the
same domain commands; neither may refresh inside an approval action.

A missing dependency, database, or scanner is **not** a passing check. Report the
check as unavailable and preserve the reason.

## Reset and troubleshooting

Inspect or stop the existing local database without deleting its data:

```bash
make db-logs
make db-stop
```

None of these Make targets removes the database volume. An explicit Compose reset
with `--volumes` destroys local review history and should be intentional:

```bash
docker compose --project-name conductor-local --file - down --volumes < deploy/local/compose.yaml
```

Common failures:

| Symptom | Meaning/action |
|---|---|
| `DATABASE_URL is required` | Export the connection URL before starting `conductord`. |
| `revision_conflict` | Re-inspect the latest revision; do not retry approval blindly. |
| `approval_rejected` | Confirm the revision is submitted and the reviewer is independent. |
| Missing `go.sum` or npm lock | Generate and review it in a dependency-enabled environment. |
| Existing untracked DB / old missing-table message | Back up and inspect the complete legacy schema, then use `make db-migrate BASELINE=...`; startup never guesses a baseline. |
| Migration ledger differs from release | Restore the inspected migration files; never replace ledger checksums or bypass the error. |
| Local TUI reports mixed actor/token or missing scope | Unset token and scope variables for local review, or use the authenticated setup; even an empty token variable counts as configured. |
| API port is already in use | Reuse the running API with `make tui`, or select another `CONDUCTOR_ADDR` consistently. |

Shared browser deployments use [configurable OIDC sign-in](browser-sign-in.md),
protected server sessions, and a fixed HTTPS origin. The local Vite workflow above
continues to use explicit development identity and unscoped local data.
