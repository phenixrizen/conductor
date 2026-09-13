# Feature 016: Operate and recover the shared platform

**Status: Implementation produced; verification is recorded in [the plan](plan.md).**

Operators can build a committed Linux release, run its separate authenticated
services, inspect source-free diagnostics, apply reviewed migrations, and recover
PostgreSQL without erasing existing data or silently replacing Temporal execution.
This feature grants no package approval, merge, publication or deployment authority.

## Required behavior

- Build only an exact committed tree using the pinned Go and Node toolchains and
  existing lockfiles. Include executable binaries, browser assets, migrations,
  documentation, deployment examples and checksums. Fix archive ordering and
  timestamps so two builds of the same tree and toolchains compare byte for byte.
  Refuse to overwrite an existing output. Building is not installing or deploying.
- Keep API, context collector, coding executor, publisher, repository webhook,
  tracker and runtime collector under separate operating-system accounts with
  separate credential directories. Only services that launch isolated Docker
  workloads receive Docker access. Repository-controlled processes receive no
  Conductor, PostgreSQL, publication or production credentials.
- Use the existing explicit OIDC and verified remote Temporal TLS profiles.
  Examples must identify operator-owned TLS, identity, database and provider setup
  and cannot claim a synthetic fixture qualifies an external account.
- Apply migration SQL in order under a database advisory lock. Commit schema and
  exact file checksums atomically, reject gaps and changed recorded files, and
  preserve an append-only ledger. An existing untracked schema requires a declared
  operator baseline; retain its provenance as `operator_baseline`, not `executed`.
- Backup PostgreSQL through trusted PostgreSQL 17 `pg_dump`, custom archive format,
  without credentials in command arguments or provider/source environment leakage.
  Publish a new owner-only archive and checksum manifest. A failed dump must not
  publish an archive. The checksum establishes consistency, not signer identity.
- Restore only an explicitly selected trusted archive whose length/checksum match,
  into an empty quarantined database. Use a private verified archive snapshot and
  one `pg_restore` transaction. Never drop existing schemas or data. Operator
  controls keep the target disconnected from services throughout recovery.
- PostgreSQL and Temporal backups are independent authorities. Never reset run IDs,
  dispatch bindings, receipts, leases or uncertainty to make a restore appear
  current. Restored historical approvals bind only their retained revision.
- An optional separate listener accepts literal loopback addresses only. `/healthz`
  reports process liveness; `/readyz` reports database/release-schema availability;
  `/metrics` reports request status classes and fixed outbox aggregate categories.
  These routes must not bypass authentication on the public API. Database failures
  produce 503 for readiness/metrics while liveness stays independent. Worker
  liveness, provider availability and successful execution cannot be inferred from
  an API-ready response or an empty queue.
- CI uses immutable action commits, read-only repository permission, no persisted
  checkout credential, bounded job durations and explicit real protocol opt-ins.
  Tests may create owned synthetic resources; no external publication credentials
  or production deployment actions belong in this workflow.

## Verification

Use unit tests for migration/file/transport bounds, real PostgreSQL for checksum
and transaction behavior, an owned PostgreSQL container with actual archive tools
for restore, and API clients to compare exact restored history and approval facts.
Run full normal/race/vet, web and contract checks. Exercise real Temporal, browser,
PTY and isolated-worker acceptance in their corresponding CI jobs. A configured
workflow is distinct from an observed hosted CI run. No deployment or live provider
qualification may be inferred from packaging or service-unit syntax validation.
