# MCP integration research

Research date: 2026-09-13. These notes describe the actual dependency selected for
[Feature 008](../../specs/008-mcp/spec.md), not certification for any MCP host.

## Pinned dependency and protocol

The official [Go SDK repository](https://github.com/modelcontextprotocol/go-sdk)
identifies `github.com/modelcontextprotocol/go-sdk` as the maintained SDK. The
[stable v1.7.0 release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
is pinned, including its Go module checksums. The upstream tagged source commit is
`bc72835f62eb94d0fb484439f886b6885b075f36`. Its
[module file](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/go.mod)
requires Go 1.25.0. Conductor records that minimum and tests with toolchain
`go1.26.8`.

The upstream compatibility table and tagged implementation support MCP protocol
2026-07-28 and earlier negotiated revisions. Conductor's actual protocol acceptance
uses the official v1.7.0 client and a compiled stdio server. This demonstrates that
pair's negotiated protocol path; it does not establish vendor-host compatibility
or every older revision independently.

The [current MCP specification](https://modelcontextprotocol.io/specification/2026-07-28)
defines tools, resources and per-request protocol metadata. The bridge uses these
standard features and emits JSON tool schemas. It does not implement optional
sampling, roots, logging, subscriptions, elicitation or task extensions. Long-lived
Conductor context execution remains in its existing Temporal workflow and is
inspected through persisted request/receipt tools.

## Source inspection and selected controls

The SDK's tagged
[transport implementation](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/transport.go)
and [JSON-RPC transport API](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/jsonrpc/jsonrpc.go)
were inspected. Stable v1.7.0 stdio reads do not impose a frame-size bound. Conductor
therefore implements the SDK's public transport interface with bounded newline
framing, pre-decode nesting/duplicate-key validation and closeable streams.
The [v1.8.0-pre.2 release notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0-pre.2)
also describe forthcoming upstream framing/JSON hardening; Conductor uses a stable
release with its own explicitly tested bounds rather than a prerelease dependency.

The tagged [server](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/server.go)
and [tool](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/tool.go)
source distinguish protocol errors from execution errors and require low-level
handlers to validate inputs. Conductor resolves each published schema with the
SDK's `github.com/google/jsonschema-go` v0.4.3 dependency, validates exact command
fields, and emits `isError` for API or command-input failures. Source text is
escaped JSON and never enters tool descriptions or executable host commands.

The [stdio transport specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
reserves stdout for protocol messages. The bridge disables SDK request logging and
uses fixed, bounded errors. The [HTTP authorization specification](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)
has additional protected-resource and OAuth requirements. No public HTTP MCP
listener or shared-token proxy is claimed; the local stdio bridge calls the
existing authenticated Conductor API using an explicitly selected token file.

## Evidence boundary

Protocol unit tests cover strict schemas, fixed scope, no hidden mutation refresh,
uncertain outcomes, safe resource output, cancellation, revocation, framing and
file bounds. Actual stdio acceptance adds the production HTTP authorization and
PostgreSQL command path with signed synthetic credentials. Neither suite proves
an external identity vendor or an unrelated MCP host, and neither authorizes merge,
deployment or acceptance of an ADR.
