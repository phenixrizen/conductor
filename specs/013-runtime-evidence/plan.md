# Feature 013 implementation plan

**Status: Implementation verified within the fixture and protocol bounds below.** Depends on shared coordination and publication
facts. Migration 010 adds runtime bindings, requests, receipts, audit and dispatch.
Migration 011 preserves exact receipt JSON and rejects unverifiable historical
representations without rewriting their digests.

| Work | Evidence |
|---|---|
| Official Groundcover research | REST docs and SDK v1.424.0 source inspected and pinned |
| Exact scoped provider adapter | Real TLS/HTTP fixture tests pass scope, timestamp, correlation, bounds, redirect and revocation paths |
| Approved criterion evaluation | Exact thresholds, series count, complete query grids, deployment window and staleness tested |
| Shared transactions | Live PostgreSQL tests pass source isolation, revocation, idempotency, configuration changes and audit rollback |
| Exact receipt retention | Live PostgreSQL and signed typed-client tests retain multiple nested source records, key order and large finite JSON numbers; legacy mismatches fail closed after authorization |
| Real API and MCP | Signed identity, actual MCP SDK calls and shared-client acceptance pass |
| Temporal | Owned CLI 1.8.3 / server 1.31.2 protocol, binding and immutable receipt recovery tests pass |
| Operator and worker | Bounded operator configuration/credentials and runnable Temporal worker with explicit local or shared TLS/mTLS transport implemented |
| Regression checks | Full Go tests and race-enabled tests against the owned PostgreSQL instance, plus `go vet ./...`, pass |
| Process durability | Owned API process and PostgreSQL postmaster restart acceptance passes; this does not test a live Groundcover account |
| Contract and documentation | OpenAPI validation, local Markdown links, balanced fences and `git diff --check` pass |
| Browser | Signed-session Chromium/API/PostgreSQL acceptance passes exact request previews and retry, met/not_met/not_verified policy displays, stale windows, escaped telemetry and scope denial; desktop/mobile screenshots inspected |
| CLI and terminal | Actual compiled CLI and signed PostgreSQL PTYs pass strict offline preview, identical retries after dropped acknowledgments, source inspection, scope denial, historical outcomes/window aging and fixed-token recovery |
| Live Groundcover account | Unverified: no live account or scoped API key supplied |
| Broad production outcome | Not verified; selected telemetry criteria do not imply full requirement satisfaction |

See [the specification](spec.md), [research](../../docs/architecture/runtime-evidence-research.md)
and [operations](../../docs/operations/runtime-evidence.md).
