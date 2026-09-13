# MCP delivery plan

1. Inspect the official MCP specification and Go SDK source. Pin a stable version
   and compatible tested Go toolchain; retain dependency checksums.
2. Implement a stdio bridge with fixed authenticated API scope, a narrow client
   interface, strict tool schemas, bounded framing, deadlines and cancellation.
3. Expose working shared package/context operations and scoped JSON resources.
   Keep exact mutation inputs and approval authority unchanged.
4. Prove protocol behavior with the official SDK client, including malformed
   frames, injected arguments, no hidden mutation refresh, safe source output,
   cancellation and fresh authorization after revocation.
5. Run the compiled server through actual stdio against signed-token API and
   isolated PostgreSQL fixtures. Exercise shared records, author commands,
   historical approval retention, receipt coverage, cancellation, idempotency,
   credential immutability, scope isolation and revocation.
6. Integrate implemented cross-repository graph and coordinated-work commands
   through the same authenticated client boundary as those APIs become available.
   Do not advertise a placeholder tool for an unavailable capability.

## Verification status

The initial stdio bridge passes `go test ./...` and `go test -race ./...` with
`CONDUCTOR_TEST_DATABASE_URL` set (18 packages), plus `go vet ./...`,
`go mod verify` and `go mod tidy -diff`. `TestAuthenticatedMCPStdio` builds the
actual bridge and passes with signed tokens and isolated PostgreSQL. Browser,
terminal, Temporal and process-restart opt-ins were not enabled in this bridge-only
run; those skips establish no new acceptance evidence. Markdown links and fences
validate. The graph follow-up exposes four working shared API commands and scoped graph
resources. MCP tests verify exact receipt tuples, retained keys, normalized source
order, query bounds and scope injection denial. Integrated graph authority is
covered by Feature 007 and the combined MCP/database acceptance. The latter
passes with race detection against a real compiled stdio process: two repository
receipts produce one idempotent graph, bounded queries retain provenance, and
revoking one source hides graph reads/query/listing while anchor package reads
remain available. The graph API prerequisite must precede the MCP graph commit. The whole-source
follow-up preserves explicit collection intent and inspected bundle digests through
the same strict tool schemas; SDK-client tests verify the exact forwarded inputs.

This branch forms one ordered PR in the full-release review stack requested by the
user. Implemented and tested behavior does not grant merge, deployment or ADR
approval authority.

The coordinator follow-up adds strict plan/profile discovery and proposal tools,
with no human execution-authority tools. SDK-client tests cover preserved pins,
optional empty arrays, empty command arguments, unchanged retry keys, nested scope
injection denial and absent authorization tools. Signed shared MCP-to-browser
acceptance proves an agent proposal is visible to a human and cannot self-authorize.

Exact related graph-source reads are verified through real MCP SDK/stdio, signed
API and PostgreSQL, including revoked related-source access.
