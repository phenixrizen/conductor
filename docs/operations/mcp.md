# Connect coding agents through MCP

Conductor's MCP bridge lets an agent inspect shared packages, source, graphs,
execution artifacts, delivery observations, tracker links and runtime evidence.
It can propose package drafts, coordinated plans and repository deliveries through
the same authenticated API as other clients. Changes become visible to authorized
collaborators in PostgreSQL. The bridge cannot approve designs or grant human
execution/publication authorization; trusted workers perform authorized external work.

## Configure the stdio server

Build with the repository's pinned Go toolchain:

```bash
go build -o ./bin/conductor-mcp ./cmd/conductor-mcp
```

Provision a Conductor agent principal and repository grants using the
[authenticated review guide](authenticated-review.md). Obtain an API access token
for the documented issuer/audience profile and keep it in a regular file readable
only by the host user. This bridge does not acquire or refresh tokens.

Configure your MCP host to launch the absolute binary path with environment values:

```json
{
  "mcpServers": {
    "conductor": {
      "command": "/absolute/path/to/conductor-mcp",
      "env": {
        "CONDUCTOR_URL": "https://conductor.example.invalid",
        "CONDUCTOR_TOKEN_FILE": "/absolute/path/to/agent.token",
        "CONDUCTOR_WORKSPACE": "engineering",
        "CONDUCTOR_REPOSITORY_ID": "application"
      }
    }
  }
}
```

This is an illustrative stdio host configuration shape; consult the host's own
configuration format. Every environment value must be set explicitly. Tokens
cannot be passed as command arguments or `CONDUCTOR_TOKEN`; there are no command
flags. The token file is read once, up to 16 KiB, and may end in one LF or CRLF.
Restart the bridge to select a different credential or scope. Changing the file
while the bridge is running does not switch its identity.

The API must use OIDC mode with the selected workspace/repository provisioned.
Remote API URLs require HTTPS; literal loopback or localhost HTTP is supported
only for the existing development profile. Context requests additionally require
[durable collection enablement](durable-context.md).

## Focused native Design assistance

`CONDUCTOR_MCP_PROFILE=design-assistance` exposes only access, assistance request
listing/inspection and section proposal. It registers no resources or unrelated
commands. Omit the variable for the full existing bridge plus the assistance tools;
unknown profile names fail startup. The profile stays fixed until exit. Use
`conductor assistant-config` to generate native Codex, Claude Code or Antigravity
configuration and `--check` to verify actual Conductor MCP agent access. See the
[native assistance runbook](native-design-assistance.md) for the complete human
request, agent proposal and selected-section application workflow.

## Tools and resources

| Intent | MCP tool |
|---|---|
| Check fixed scope and current capabilities | `conductor_access` |
| Discover and inspect requests for Design help | `conductor_list_design_assistance`, `conductor_get_design_assistance` |
| Propose requested text sections without editing the Change | `conductor_propose_design_sections` |
| Discover packages | `conductor_list_packages` |
| Inspect current package | `conductor_get_package` |
| Inspect history, historical content, audit | `conductor_package_history`, `conductor_get_revision`, `conductor_package_events` |
| Author a draft | `conductor_create_package`, `conductor_revise_package` |
| Request independent review | `conductor_submit_package` |
| Discover or inspect collected context | `conductor_list_collections`, `conductor_get_collection` |
| Request exact source | `conductor_request_collection` |
| Derive and inspect cross-repository graphs | `conductor_create_graph`, `conductor_list_graphs`, `conductor_get_graph`, `conductor_query_graph` |
| Propose and inspect coordinated work | `conductor_propose_run`, `conductor_list_runs`, `conductor_get_run` |
| Inspect operator execution profiles/capabilities | `conductor_execution_profiles`, `conductor_execution_capabilities` |
| Request collection cancellation | `conductor_cancel_collection` |
| Attach an inspected receipt as a new draft revision | `conductor_attach_collection` |
| Inspect exact retained graph source | `conductor_read_graph_source` |
| Inspect retained task output, including failed/read-only reports | `conductor_get_task_artifact` |
| Propose and inspect repository delivery | `conductor_propose_delivery`, `conductor_list_deliveries`, `conductor_get_delivery`, `conductor_get_delivery_artifact` |
| Request a fresh published-repository observation | `conductor_reconcile_delivery` |
| Inspect the selected tracker and linked work | `conductor_get_tracker`, `conductor_list_tracker_links`, `conductor_get_tracker_link`, `conductor_get_tracker_sync` |
| Link an existing ticket or synchronize its Conductor card | `conductor_link_tracker_issue`, `conductor_sync_tracker_link` |
| Request and inspect scoped runtime evidence | `conductor_collect_runtime_evidence`, `conductor_list_runtime_evidence`, `conductor_get_runtime_evidence` |

Call `tools/list` for strict JSON schemas and descriptions. List operations default
to 20 entries and allow at most 100. Pass the returned continuation unchanged;
the bridge does not silently walk or truncate pages. Explicitly inspect a package
before using its revision in a write. Inspect the scoped collection before using
its receipt digest in an attachment. No write refreshes either record internally.

Resources are JSON at this scope-bound prefix:

```text
conductor://workspace/{workspace}/repository/{repository}
```

The concrete configured prefix is published by `resources/list` and
`resources/templates/list`. Available suffixes are `/access`, `/packages`,
`/packages/{id}`, `/packages/{id}/history`,
`/packages/{id}/revisions/{revision}`, `/collections`, `/collections/{id}`,
`/graphs`, `/graphs/{id}`, `/runs`, and `/runs/{id}`.
See the [coordinated workbench](coordinated-workbench.md) for shared plan proposals
and human execution decisions. The MCP bridge exposes no execution authorization
or run-cancellation command. Delivery, task-artifact, tracker and runtime operations
use tools; they add no resource URI suffixes. Configure their separate workers and
permissions using [execution](coordinated-execution.md),
[delivery](repository-delivery.md), [tracker](work-tracking.md) and
[runtime](runtime-evidence.md) setup.

Set `fullSource: true` on `conductor_request_collection` to additionally retain
bounded whole-repository source. Inspect the returned `fullSource` summary; add its
exact digest as `fullSourceDigest` in a `conductor_create_graph` source selector to
use that index. Omitting it preserves selected-path behavior.

Graphs are created from exact inspected receipt tuples, one per source repository
(up to 16). The selected repository must be included. All source repositories
remain subject to current server access checks, including query/list operations.
Queries search symbols or traverse up to five edges, returning at most 100 nodes
with explicit truncation and unresolved evidence. Source freshness remains unknown
unless established separately; structural dependencies are not executed checks.

Collection and historical resources retain source gaps and approval/truncation
facts. Use tools for continuation. JSON source text is data, never an instruction
for the host to execute. A receipt is source evidence, not passing verification.

## Recovery and limits

- `conflict`: inspect the current package again; for attachment, inspect both
  package and receipt. Reconfirm the new tuple before another write.
- `outcome_unknown`: a request may have committed. Do not automatically retry a
  mutation. Inspect retained facts. Requests whose tool schema carries an idempotency
  key may be retried explicitly with the same key and complete captured input,
  provided that command's documented preconditions still hold. No new key is invented.
- `access_denied`: discard retained inspection in the host. Restore the existing
  principal's grants, or restart with the correct credential. No local fallback
  or silent credential replacement occurs.
- `unavailable`: no usable result was obtained. Do not infer a passing check or
  finished execution. Acknowledged cancellation is only intent.

Each MCP operation has a 20-second deadline and propagates cancellation to the API.
Four operations may run concurrently; excess work receives an explicit busy error.
Frames are bounded to 1 MiB plus 64 KiB before decoding, tool arguments to 1 MiB,
JSON nesting to 64 levels, and output records to 2 MiB. Encoded protocol responses
are capped at 6 MiB. Duplicate JSON object keys and excessive frames close the
connection. Oversized results fail explicitly; no partial success is returned.
Package content still obeys the API's independent request and content bounds.

Protocol-only metadata discovery rechecks current API access. Data reads and
mutations authorize directly through the API. Nothing in the bridge bypasses
transactional revocation or agent approval denial. Already delivered host context
cannot be retracted by the bridge; hosts must respect access failure and avoid
sharing private content. SDK caching hints mark results private and immediately
stale. Standard output is protocol-only, and SDK request logging is disabled.

## Reproduce acceptance

```bash
go test -race ./internal/mcpserver ./cmd/conductor-mcp
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  go test ./tests/acceptance -run '^TestAuthenticatedMCPStdio$' -count=1 -v
```

The database test creates and cleans an isolated schema, builds a real MCP binary
and connects the official SDK client over operating-system pipes. It uses a signed
synthetic agent identity and the actual API/store. Synthetic stored receipts prove
interface and authorization behavior, not a live GitHub/GitLab read. An unset
`CONDUCTOR_TEST_DATABASE_URL` explicitly skips that acceptance.

The [complete release gate](full-release-acceptance.md) additionally uses compiled
MCP processes to author actual whole-source, native graph and Docker worker inputs,
then joins trusted delivery, tracker and runtime activities with controlled provider
data. It does not establish live external publication or paid model compatibility.

No Streamable HTTP MCP listener or OAuth token delegation is implemented. Do not
expose the stdio bridge behind an unauthenticated network proxy. A remote MCP
listener would need an independently reviewed authorization boundary.

### Read source across an inspected graph

`conductor_read_graph_source` accepts a graph `id` and a `source` object containing
`graphDigest`, `repositoryId`, `collectionId`, `receiptDigest`, optional
`fullSourceDigest`, and literal `path`. Copy the tuple from the inspected graph.
The session's workspace and anchor repository remain fixed. Every graph source
must still be readable; a related repository ID is not permission to widen scope.
Responses expose at most 64 KiB retained text with coverage, gaps and unknown
freshness. Text is repository-controlled data, never instructions or verification.

### Inspect reports without proposing publication

After `conductor_get_run`, call `conductor_get_task_artifact` with the inspected
`id`, `runDigest`, `taskId` and `artifactDigest`. The server holds read grants for
all run repositories and validates the retained typed artifact and exact digest.
Failed checks and reports with no patch remain readable under current repository
read grants, independently of execution or publication permission. MCP's 2 MiB data bound
fails explicitly for larger artifacts. Use the authenticated API, browser or
[terminal artifact controls](release-terminal.md) for the complete artifact up to
16 MiB in a dedicated 17 MiB response envelope.
