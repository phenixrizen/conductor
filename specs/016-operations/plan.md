# Feature 016 implementation and verification

**Status: Implementation verified within the local protocol and fixture bounds below.**

| Area | Implementation | Verification |
|---|---|---|
| Release | Exact committed source, fixed Go/Node toolchains, deterministic archive and SHA256 manifest | Two builds of commit `eb6e2b4` compare byte for byte; archive SHA256 `a61680e47341c9cea4b3035949924dcf597ef463d904ca2abc3c2f031ce097a3` |
| Operator migrations | Ordered SQL, checksum ledger, explicit legacy baseline, one transaction and advisory lock | Real PostgreSQL application, idempotency, changed-checksum refusal and failed-SQL rollback pass |
| Local startup | `make run` uses the checked operator on its selected loopback Compose database; explicit `db-migrate` handles inspected legacy baselines | Owned real Make/Compose/API regression covers fresh initialization, tracked upgrade, original migration-001 refusal/recovery, preserved data and ledger, separate database selection, and persistent stop/start |
| Database backup/restore | PostgreSQL 17 custom archive, private connection file and verified snapshot, exclusive publication, empty-target single transaction restore | Owned PostgreSQL 17.11 tools and exact API history/approval/audit comparison pass |
| Diagnostics | Separate optional loopback liveness/readiness/aggregate metrics | HTTP authority/redaction and live database queue/closed-connection checks pass |
| Deployment examples | Separate service accounts and credentials, systemd template, nginx HTTPS example, remote Temporal TLS | Packaged executable/service-unit verification and nginx 1.30.4 TLS configuration check pass; host services are not deployed |
| CI | Pinned actions, full shared protocol and isolated worker jobs, reproducible archive | Actionlint 1.7.7, workflow YAML and pinned Temporal CLI installer pass locally; hosted GitHub run not observed yet |
| Full regression | Go normal/race/vet, web type/build, contracts, docs and restart checks | Full Go normal/race/live PostgreSQL/vet, web install/typecheck/build, three OpenAPI contracts, documentation links and owned API/PostgreSQL restart pass |

See [the specification](spec.md) and [operator runbook](../../docs/operations/release.md).
The [local development guide](../../docs/operations/local-development.md) covers
checked startup and one-time legacy recovery. The local startup regression runs
under `CONDUCTOR_TEST_PROCESS_RESTART=1` and owns its projects, ports and volumes.
Later successful hosted CI and merged release evidence are recorded in the
[full release contract](../../docs/full-release.md).
