# Build, operate and recover Conductor

**Status: Implemented operator tooling; verification is recorded in
[feature 016](../../specs/016-operations/plan.md). No host deployment is performed by
these commands or their tests.**

Conductor keeps shared engineering records in PostgreSQL and durable execution
history in Temporal. Deploy the API and browser behind one HTTPS origin, then run
separate trusted services for source collection, coding, publication, repository
webhooks, tracker synchronization and runtime evidence. The browser, terminal and
MCP bridge all use the same authenticated API.

## Build a committed release

The tested build platform is Linux x86_64, Go toolchain 1.26.8 and Node.js 22.14.0.
Git, Bash, GNU tar, gzip, npm and sha256sum are also required. Registry/network access
is needed for dependencies; versions and integrity come from checked-in lockfiles.

```bash
scripts/build-release.sh /absolute/new/conductor-linux-amd64.tar.gz
```

The script builds `HEAD`, so review and commit changes first. It never includes
untracked files, environment files, local tokens or dirty working-tree changes.
The archive contains all Go commands, browser assets, ordered migrations, docs,
deployment examples, module/dependency lists, the source commit and SHA256 checksums.
Extract into a new operator-owned directory and run `sha256sum --check SHA256SUMS`
from its `conductor` directory before choosing it as the installed release. Run
`python3 scripts/check-release.py /absolute/extracted/conductor` from the source
checkout to verify checksums, the packaged systemd command and nginx TLS syntax
using owned temporary fixtures; Docker, OpenSSL and systemd-analyze are required. The
checksums detect changed bytes; a trusted release distribution/signature policy
remains the operator's responsibility. No license is selected by this packaging.

Run the build twice and compare the two archives to verify reproducibility with
these toolchains. Worker container images have their own inspected immutable IDs;
build them from the exact source using the existing
[coding](coding-workers.md), [native design tools](design-tools.md) and
[CodeGraph](repository-graph.md) instructions. Never substitute a moving tag for a
reviewed execution profile's image digest.

## Configure independent services

The supplied [systemd template](../../deploy/release/systemd/conductor@.service)
uses `/opt/conductor/current/bin/%i` and `/etc/conductor/%i.env`. Provision a separate
OS account and group matching each instance name, with a private credential
directory that only that account and the trusted operator can read:

| Instance | Purpose | Credentials and special host access |
|---|---|---|
| `conductord` | Shared authenticated API | Database, OIDC browser client secret; no provider token |
| `conductor-worker` | Source collection and native CodeGraph | Database, read-provider catalog, Temporal TLS, Docker |
| `conductor-executor` | Coordinated isolated producers/checks | Database, reviewed profiles and model gateway credentials, Temporal TLS, Docker |
| `conductor-publisher` | Authorized GitHub/GitLab publication | Database, separate publication catalog, Temporal TLS; no Docker needed |
| `conductor-webhooks` | Signed repository event inbox | Database and separate webhook secrets |
| `conductor-tracker-worker` | Selected Linear/Jira synchronization and webhook | Database, tracker-only catalog and webhook secrets, Temporal TLS |
| `conductor-runtime-worker` | Scoped Groundcover evidence | Database, read-only runtime catalog, Temporal TLS |

Install the optional [Docker drop-in](../../deploy/release/systemd/conductor-docker.conf)
only for the context collector and executor. Docker daemon membership grants host
administration authority; use dedicated worker hosts where that authority must be
separated from the API and publication host. Container sandbox flags do not remove
that host-level responsibility. Do not mount service credential directories into
repository workloads. The template's `NoNewPrivileges`, private temporary files,
read-only system filesystem and separate state directory protect the trusted
process; they are not a replacement for the execution runner's checks.

Copy the [API environment example](../../deploy/release/conductord.env.example)
and [worker settings](../../deploy/release/worker.env.example), retaining only each
service's own settings. Environment files may contain the database credential and
must be protected; provider/model secrets remain in the referenced private files.
Database roles must have the table/sequence rights their trusted service uses;
this release does not provision database roles, TLS certificates, DNS or firewall
rules. Use a separate operator role for schema changes and recovery. Network
clients require HTTPS; remote database archive operations require `sslmode=verify-full`.

Use the [remote Temporal TLS guide](temporal-tls.md) for the exact CA/certificate
profile and file permissions. Copy certificates into regular private files; the
loader rejects symlink components, including projected secret symlinks. Restart
workers for certificate rotation while preserving endpoint, namespace and recorded
cluster bindings. The local Temporal CLI remains a development profile.

The [nginx example](../../deploy/release/nginx.conf.example) serves built assets and
proxies `/api/` to loopback. Replace the synthetic hostname and certificates and run
`nginx -t` before enabling your proxy. Preserve the exact browser origin and cookie
headers described in [browser setup](browser-sign-in.md). Forward only documented
repository webhook routes to port 8091 and tracker routes to a separately configured
port such as 8092; these listeners otherwise have colliding defaults. Never proxy
the operations listener to the public origin. Proxy installation/certificate
qualification and a real identity-provider registration are separate operator work.

Provision workspace/repository grants and each integration using their existing
operator commands before starting consequential workers. Start the configured
systemd instances only after inspecting those files and the release. Services can
be restarted independently; API shutdown drains already authorized commands, and
worker recovery retains the original Temporal run and immutable database receipts.
No script automatically enables a service, creates a provider branch, merges code
or deploys repository work.

## Apply or upgrade the database

Keep a verified backup before changes and stop affected services during schema
upgrades. Select a connection using the protected `DATABASE_URL` environment.

```bash
/opt/conductor/current/bin/conductor-db migrate \
  --directory /opt/conductor/current/migrations --operator declared-operator
```

An empty database applies every migration in one transaction. The tool serializes
other instances through an advisory lock and records the file name/checksum for
each completed migration. A later run checks every recorded file before applying
new ones. Migration 011 makes this ledger append-only; no automatic downgrade,
checksum replacement or partial acceptance of failed SQL is provided. A commit
acknowledgment failure requires inspection of the ledger before another attempt.

For an existing installation that applied migrations manually, inspect its actual
schema and the original SQL first, then supply the exact already-applied count,
for example `--baseline 10`. That is a declared operator assertion. Its ledger rows
say `operator_baseline`; they do not claim the new tool witnessed historical SQL
execution. A baseline cannot replace existing recorded history. Do not guess a
baseline from a table name or use it to skip unapplied changes.

## Backup and restore

Install trusted PostgreSQL 17 `pg_dump` and `pg_restore` on the operator host. The
owned acceptance uses PostgreSQL 17.11 from the pinned image in
[restart acceptance](../../tests/acceptance/restart_test.go). Backup and restore
commands have a 30-minute deadline; size and disk capacity must fit that window.
Archives contain source, review history and authorization records: keep them on
protected storage and apply your normal encryption, retention and access policy.

```bash
# DATABASE_URL selects the inspected source database.
conductor-db backup --output /absolute/protected/new-backup.dump
# Creates new-backup.dump and new-backup.dump.json with owner-only permissions.
```

The custom archive is a PostgreSQL-consistent snapshot. Credentials go through a
private temporary libpq service file; subprocess arguments and output omit the
connection URL. An existing output is never overwritten. Copy the archive and
manifest together; the manifest contains only format version, bytes, digest, tool
version and creation time. This is a logical database backup, not a backup of
PostgreSQL roles, TLS keys, provider credentials, worker images or Temporal.

Create a fresh database from `template0` and quarantine it from all services. Set
`DATABASE_URL` to that new target and restore only a trusted archive:

```bash
conductor-db restore --input /absolute/protected/new-backup.dump
```

Restoration verifies the manifest and reads a private snapshot of the exact archive
bytes. Temporary disk space at least the archive size is required. It refuses
nonempty databases and uses a single transaction with `--no-owner --no-acl`; the
restoring role owns restored objects, so reapply your reviewed database-role grants
before service access. It never runs `DROP`, `--clean` or an implicit replacement.
A trusted database archive can contain SQL/functions: checksum validation does not
make an untrusted archive safe. Keep the target quarantined while verifying it.

Capture and compare immutable revisions/digests, approval history, audit events,
source bundles, runtime bindings, outbox identities and receipt digests as relevant
to your installation. The owned automated restore test compares exact package,
approval/history/audit facts and migration records through real API processes, plus
scoped collection requests, uncertain outbox leases and Temporal target bindings; it
does not claim to restore an external Temporal cluster or provider account.

Back up Temporal using the deployment's supported persistent-store procedure and
retention policy, separately from PostgreSQL. For a coordinated recovery, quiesce
API writes, webhook ingestion and workers, then record the PostgreSQL snapshot and
Temporal recovery points together. An independently older database snapshot may
omit provider writes or workflow receipts; an older Temporal snapshot may omit
known execution. Keep services stopped until those differences are reconciled
against retained workflow history and provider state. Never reset dispatch target,
namespace, run IDs, receipts or unresolved observations to force a restart. Missing
known history must remain unresolved. Losing acknowledged source/approval facts
requires recovery of those records, not relabeling newer content as approved.

## Health, diagnostics and capacity

Set `CONDUCTOR_OPERATIONS_ADDR=127.0.0.1:9090` to enable a separate operator listener.
No operations route is added to the authenticated API listener.

| Route | Meaning |
|---|---|
| `GET /healthz` | The API process can answer requests; no database or provider success assertion |
| `GET /readyz` | PostgreSQL and required release outbox schemas are available; 503 otherwise |
| `GET /metrics` | Prometheus text with status-class request totals, aggregate duration, in-flight requests, uptime, database readiness, pending/unresolved outbox counts and oldest pending age |

Metrics use five fixed workflow categories. They never include request paths,
query values, actors, workspace/repository IDs, credentials, source or provider
response text. At most two diagnostic reads run concurrently, each with a two-second
deadline. The listener has bounded headers and timeouts. PostgreSQL failure leaves
liveness independent and makes readiness/metrics return 503; errors are generic.
Readiness does not assert that a worker is connected, an identity vendor is healthy,
a provider is reachable, or an execution passed. Monitor Temporal task-queue
pollers and service process state separately, using its operator tooling.

Alert on unavailable readiness, growing pending age/counts, unresolved observations,
process restart loops and depleted database/storage capacity. Select thresholds
from the deployment's operating objectives; the product invents no production
success criteria. Context oldest age uses its scheduled `available_at`, which may
advance on retry; other workflow categories use request creation time. These
aggregate queries are intended for bounded deployments; inspect query load before
increasing scrape frequency or dataset size. API body/header limits and worker
activity concurrency remain those documented in each integration runbook.

## Continuous verification

[The GitHub workflow](../../.github/workflows/verify.yml) pins action commits and
has separate shared-protocol and isolated-worker jobs. It runs Go normal/race/vet,
live PostgreSQL, actual Temporal, browser, PTY, restart/backup, native graph and
Docker coding/design paths, validates web/contracts/docs and compares two release
archives. No job carries provider or production credentials. A configured workflow
is not an observed hosted CI result; record that result on the reviewed PR.

The focused owned recovery command is:

```bash
CONDUCTOR_TEST_BACKUP=1 go test -race ./tests/acceptance -run TestOwnedBackupRestore -count=1 -v
```

Docker access is required. Tests own their database containers/volumes and never
restart or overwrite the database selected by `CONDUCTOR_TEST_DATABASE_URL`.
Use `sg docker -c '…'` if a fresh supplementary Docker group is needed in a Snap
installation. Missing dependencies after opting in are failures, not passing skips.
