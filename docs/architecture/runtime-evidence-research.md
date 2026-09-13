# Groundcover integration research

**Status: Inspected and fixture-tested on 2026-09-13.** Live account compatibility
remains unverified. No telemetry ingestion, resource management or deployment API
is implemented by this integration.

| Item | Pin and implemented boundary |
|---|---|
| Official source | `groundcover-com/groundcover-sdk-go` v1.424.0, commit `7c4ff02883078abf9266bd7602093e424206a61c` |
| REST profile | `groundcover-rest/2026-09-13-sdk1.424.0` |
| API host | `https://api.groundcover.com` only |
| Authentication | API-key bearer token and explicit `X-Backend-Id` |
| Metrics | POST `/api/metrics/query-range`, bounded raw selectors preserving exact correlation labels |
| Logs and traces | POST `/api/logs/v2/search` and `/api/traces/v2/search`, nonstreaming gcQL with exact filters and explicit limit |
| Go toolchain tested | Repository Go 1.26.8 toolchain; SDK is a schema reference rather than a linked retrying client |

The [official API-key documentation][keys] distinguishes API keys from ingestion
keys and binds keys to service-account policies. Operators must provision a read-only
service account limited to the intended backend/data. The [API examples][examples]
specify the canonical host and backend header. This profile supports that canonical
host; self-hosted endpoints need a separately reviewed transport profile.

The [metrics documentation][metrics] defines the custom range response as
`velocities`, each containing labels and timestamp/value pairs, with an echoed
query. Conductor uses this documented endpoint instead of guessing the payload of
the SDK's older `/api/metrics/query`. Only raw allowlisted metric selectors are
constructed. Rate, percentile and arbitrary MetricsQL expressions are not implied.
Returned query-grid timestamps do not establish raw scrape freshness.

The [pinned SDK log][logs] and [trace][traces] clients confirm the v2 endpoints;
their request types include start/end, query, sources and nonstreaming selection.
Their successful payload types are untyped. SDK examples and tests demonstrate
array responses and the [log download example][download] demonstrates timestamp,
content and `string_attributes`. Conductor accepts bounded rows with RFC3339
`timestamp` and configured direct or `string_attributes` correlation keys. Unknown
shapes fail closed; no complete trace graph or universal field compatibility is
claimed. The direct REST adapter avoids SDK retries, response-body logging and
caller-configurable endpoint/header overrides.

The [gcQL filtering reference][filters] documents case-insensitive defaults,
case-sensitive `:=` and the repeated-field implicit OR rule. Conductor constructs
case-sensitive terms with explicit AND and bounded literal values. The
[pipeline reference][pipeline] documents sorting and limits. Queries fetch 51 rows
to retain at most 50 and make truncation visible. Record filters are checked again
against returned fields before private source is retained.

Tests use controlled TLS/HTTP provider responses, a real owned PostgreSQL instance,
a real MCP SDK client, signed API identity, and a real owned Temporal CLI process.
The Temporal protocol test uses a controlled receipt activity; provider decoding
and database transactions have separate acceptance tests. These checks do not
establish live Groundcover account, retention, tier or instrumentation compatibility.

[keys]: https://docs.groundcover.com/use-groundcover/remote-access-and-apis/api-keys
[examples]: https://docs.groundcover.com/use-groundcover/remote-access-and-apis/api-examples
[metrics]: https://docs.groundcover.com/use-groundcover/remote-access-and-apis/api-examples/query-metrics
[logs]: https://github.com/groundcover-com/groundcover-sdk-go/blob/7c4ff02883078abf9266bd7602093e424206a61c/pkg/client/logs/logs_client.go
[traces]: https://github.com/groundcover-com/groundcover-sdk-go/blob/7c4ff02883078abf9266bd7602093e424206a61c/pkg/client/traces/traces_client.go
[download]: https://github.com/groundcover-com/groundcover-sdk-go/blob/7c4ff02883078abf9266bd7602093e424206a61c/examples/logs/download-to-file/main.go
[filters]: https://docs.groundcover.com/use-groundcover/querying-your-groundcover-data/groundcover-query-language/filters
[pipeline]: https://docs.groundcover.com/use-groundcover/querying-your-groundcover-data/groundcover-query-language/pipeline-operations
