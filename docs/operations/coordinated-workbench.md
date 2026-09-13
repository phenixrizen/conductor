# Review and authorize shared agent work

Conductor keeps task plans and their evidence in the workspace database. Agents
can propose plans through MCP; people can inspect the same plans in the browser,
confirm exact execution authority, and inspect later observations and receipts.
These interfaces use the shared API and never execute repository commands locally.

## Browser controls

Sign in, select the workspace and repository, then open **Agent work → Coordinated execution**.
Use **Refresh runs and access** to load one bounded page, current selected-repository
execution capability and the operator's enabled profile catalog. A missing or
truncated profile does not establish runnable capacity. The server checks every
included repository independently when committing a decision.

Inspect a listed run or enter its exact run ID. Review its plan digest, graph/source
pins, package revisions, task dependencies, configured profiles, writable paths and
declared verification commands. A task with no declared checks cannot establish
passing implementation verification. Profile configuration contains no credentials.

An author can import an explicitly selected JSON plan file, up to 1 MiB, or paste a
version 1 plan matching [OpenAPI](../../api/openapi.yaml). Duplicate JSON fields,
unknown plan fields and incomplete previews are rejected. Preview the structured
plan before recording it. Recording a proposal does not run it. A lost creation
acknowledgment offers **Retry exact plan proposal**, preserving the complete input
and idempotency key without fetching replacement facts.

A human with execution permission may select **Authorize inspected plan**, inspect
the confirmation's run ID/digest, and confirm. Current independent design approval,
whole-source/profile pins and permissions must still hold on every repository.
Authorization cannot approve a design, publish a patch, or prove a running worker.
A conflict or uncertain response disables further execution commands until an
explicit **Refresh inspected run**. Authorization never refreshes any source,
package, profile or plan inside the confirmed command.

**Request run cancellation** records intent against the displayed plan digest.
The trusted worker must still confirm termination and cleanup; reservations remain
held while either is unresolved. Run observations show their timestamp and age;
missing or stale observations remain unknown, and display aging does not poll the
API. Refresh explicitly to inspect newer facts. **Inspect task artifact** shows
retained patches, reports and checks here, including failed or read-only tasks.
Publication eligibility and human authorization belong to the separate **Delivery**
workflow.

Changing workspace/repository clears private inspection and confirmations. Access
denial also clears imported drafts and capability information. Read-only users see
shared plans without execution controls. Browser login is restricted to humans;
agents use authenticated API/MCP access and cannot grant execution authorization.

## MCP workflow

With the API's coordination capability enabled, the existing fixed-scope
[MCP bridge](mcp.md) exposes:

| Tool | Behavior |
|---|---|
| `conductor_execution_profiles` | Inspect enabled public profiles and exact configuration/image pins |
| `conductor_execution_capabilities` | Inspect current selected-repository capability facts |
| `conductor_propose_run` | Record a strict, bounded plan under one explicit idempotency key |
| `conductor_list_runs` | Discover one bounded page of shared plans |
| `conductor_get_run` | Inspect exact plan, human authorization and retained observations/receipts |
| `conductor_get_task_artifact` | Read complete bounded output using the inspected run, task and artifact digests |

The scope-bound resources `/runs` and `/runs/{id}` expose the corresponding shared
records. No MCP tool authorizes execution or cancellation. A perspective such as
architect or QC supplies task context and never grants permissions. Missing runtime
configuration returns an unavailable error; the bridge does not invent success.

## Acceptance

```bash
npm --prefix apps/web ci
npm --prefix apps/web run build
go test -race ./internal/mcpserver
CONDUCTOR_TEST_BROWSER=1 go test -race ./tests/acceptance -run TestBrowserCoordinatedRuns -count=1
```

Set `CONDUCTOR_TEST_DATABASE_URL` and `CONDUCTOR_BROWSER_PYTHON` as described in the
[browser runbook](browser-sign-in.md). Acceptance uses the real MCP SDK client,
signed API/browser identities, isolated PostgreSQL, retained real Git bundles and
actual Chromium. It covers shared agent proposals, stale design rejection, strict
file preview, exact lost-response retries, uncertain authorization recovery,
cancellation, permission limits and desktop/mobile layout. It deliberately does
not fabricate task execution; the [executor runbook](coordinated-execution.md)
records the separate Docker/Temporal/process evidence.

## Inspect task reports and patches

After inspecting a run, choose **Inspect task artifact** beside a retained receipt.
The browser requests exactly that run, task and artifact digest. It checks every
displayed patch and producer/check output against its byte digest and shows the
complete bounded retained data. Failed tasks and read-only design reports remain
inspectable even with no patches, failed cleanup or no independent checks. Those
states stay explicit and do not grant publication authority. Source denial clears
both the run and cached artifact. Scope changes cancel pending reads.

This uses the same artifact display as publication review, whose separate
eligibility and exact human authorization rules remain unchanged. Only the dedicated
artifact GET endpoints allow a 17 MiB response; ordinary browser reads remain at
4 MiB. Controlled signed-session PostgreSQL/Chromium acceptance verifies failed
reports, altered-output rejection, source denial and desktop/mobile layout.
