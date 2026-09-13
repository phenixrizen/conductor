# Feature 008: authenticated MCP access to shared engineering context

**Status: Implemented for stdio; verification recorded in the delivery plan.**

## Outcome

A coding agent uses Model Context Protocol tools and resources to inspect the same
Conductor work packages, retained decisions, history and context receipts as human
collaborators. It may author drafts and request context through the shared API.
Conductor remains the authority for identity, permissions, immutable revision
history and consequential commands.

The bridge runs as `conductor-mcp`, launched by an MCP host over standard input and
output. The API can be shared and remote; the bridge is local to its host process.
A dedicated agent principal is recommended. No public MCP HTTP listener is enabled.

## Contract

1. Require an explicit API URL, bounded token file, workspace ID and canonical
   repository ID at process launch. HTTPS is required except for the shared
   client's existing loopback development exception. Read the credential once.
   Tool arguments, resource URIs and protocol metadata cannot change identity,
   scope, API URL or token. No local actor fallback exists.
2. Derive principal kind and capabilities from API discovery. Guard protocol-only
   tool/resource discovery through fresh session and repository discovery; missing
   or truncated discovery cannot invent access. Every data read and mutation also
   reaches the existing scoped API authorization boundary. Do not cache package
   content, receipts or permissions in the bridge.
3. Expose implemented tools for access discovery, package discovery/current read,
   historical revision/history/audit read, draft creation/revision/submission,
   context discovery/read/request/cancellation and explicit receipt attachment.
   Expose graph creation from 1–16 inspected receipts, graph listing/inspection,
   and bounded search/traversal. The selected repository anchors the graph; source
   IDs do not change session scope, and every source requires current API access.
   Do not expose approval, arbitrary HTTP, shell execution, credential management,
   merge, deployment, provider publication or unimplemented success tools.
4. Consequential commands carry the caller's inspected revision and, for receipt
   attachment, collection identity and digest. Send the exact command without any
   refresh inside it. The API transaction authorizes and version-checks the write.
   Agents cannot gain approval authority through MCP, including when configured
   with a human credential: the bridge exposes no approval command at all.
5. An uncertain command returns a tool error describing the possibility of a
   committed result. Never automatically retry. Collection retries use an explicit
   original key with the same normalized input; changed input remains an API
   conflict. Other stale or uncertain writes require explicit renewed inspection.
   Cancellation intent does not establish that execution stopped.
6. Tool schemas reject unknown or case-aliased command fields. Preserve arbitrary
   package content extension fields. Bound input frames before SDK decoding,
   reject duplicate JSON keys and excessive nesting, enforce per-operation
   deadlines and cap concurrent operations. Propagate MCP cancellation to HTTP.
7. Resource URIs use the configured workspace/repository prefix. They identify
   Conductor records, never arbitrary locations to fetch. Return complete escaped
   JSON with a notice that source and imported text are untrusted data. Retain
   missing evidence and pagination/truncation flags. Historical approval and
   collected source cannot be presented as passing current verification.
8. Standard output contains only JSON-RPC. Do not log requests, source text, access
   tokens or backend error bodies. Return stable bounded error categories. For
   MCP versions supporting caching, access-controlled results are private and
   immediately stale. Revocation prevents subsequent fresh reads and discovery;
   the bridge cannot retract content already delivered to a host.
9. Exercise an actual compiled stdio server with the official MCP client, signed
   tokens, migrated PostgreSQL and the real Conductor API. Prove shared data,
   immutable history, server-owned agent attribution, author commands, request
   idempotency, cancellation intent, receipt attachment, stale revision rejection,
   scope isolation, fixed credentials and revocation. Receipt fixtures are not
   provider-execution or production compatibility proof.

## Transport boundary

The supported transport is stdio. A host authorizes launching the bridge and holds
its credential file. Public HTTP MCP would require a separate OAuth resource and
end-user authorization design; sharing a process credential across HTTP clients
would not satisfy this contract. MCP prompts, sampling, host filesystem roots,
subscriptions and background polling are not required or exposed by this bridge.

See the [plan](plan.md), [runbook](../../docs/operations/mcp.md), and
[upstream research](../../docs/architecture/mcp-integration-research.md).
