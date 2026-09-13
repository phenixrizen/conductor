# Feature 016 implementation and verification

**Status: Implementation produced; final checks in progress.**

| Area | Implementation | Verification |
|---|---|---|
| Release | Exact committed source, fixed Go/Node toolchains, deterministic archive and SHA256 manifest | Two-build comparison pending |
| Operator migrations | Ordered SQL, checksum ledger, explicit legacy baseline, one transaction and advisory lock | Real PostgreSQL application, idempotency, changed-checksum refusal and failed-SQL rollback pass |
| Database backup/restore | PostgreSQL 17 custom archive, private connection file and verified snapshot, exclusive publication, empty-target single transaction restore | Owned PostgreSQL 17.11 tools and exact API history/approval/audit comparison pass |
| Diagnostics | Separate optional loopback liveness/readiness/aggregate metrics | HTTP authority/redaction tests pass; live database queue/failure checks pending |
| Deployment examples | Separate service accounts and credentials, systemd template, nginx HTTPS example, remote Temporal TLS | Syntax and packaged binary checks pending; host services are not deployed |
| CI | Pinned actions, full shared protocol and isolated worker jobs, reproducible archive | Local commands under verification; hosted GitHub run not observed yet |
| Full regression | Go normal/race/vet, web type/build, contracts, docs and restart checks | Pending final integration |

See [the specification](spec.md) and [operator runbook](../../docs/operations/release.md).
