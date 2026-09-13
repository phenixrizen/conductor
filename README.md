# Conductor

**Engineering intent, orchestrated.**

Conductor is a shared workspace for engineers to plan software changes, review
exact versions of a design, and keep the supporting repository context and review
history available to their team. It is being built to coordinate AI coding agents
under human architectural authority.

The [full release contract](docs/full-release.md) tracks the required coordinated
platform: cross-repository relationships, CodeGraph, MCP, coding agents, verification,
GitHub/GitLab delivery, and workspace-selected Linear/Jira synchronization. The
current capabilities below are working parts of that release, not a complete release.

A **work package** describes a proposed change: its intent, design, scope, tasks,
context, and verification requirements. Every edit creates an immutable revision.
An independent reviewer approves the exact revision and digest they inspected.
Editing the package preserves that approval in history while making it ineffective
for the new revision.

## What works today

- Create, revise, submit, inspect, and approve packages through the HTTP API and Go CLI.
- Share work within authenticated workspaces, with repository permissions for
  reading, authoring, and independent human approval.
- Browse related packages and history that your workspace and repository grants allow.
- Connect coding agents through an authenticated MCP stdio bridge to the same
  packages, history, context requests, and draft commands used by other clients.
- Build and query shared graphs across repositories in the browser, API and MCP,
  with inspected source references, dependency relationships and visible coverage gaps. An optional
  pinned CodeGraph Rust parser extracts symbols during background collection.
- Propose shared dependent task plans through MCP or browser JSON import; inspect
  exact source, design and profile pins before separate human execution decisions.
- Sign in to the browser through a configured OpenID Connect provider and select shared work.
- Import, submit, and review shared packages in the interactive terminal workbench.
- Inspect historical revisions, approvals, and audit events; compare content in the web workbench.
- Capture selected specification, ADR, and other text files from one Git commit,
  with their original content, source IDs, and explicit collection gaps.
- Collect selected files from exact GitHub/GitLab commits in the background through
  the API, CLI, browser or authenticated terminal. Share the saved results, inspect
  missing files, request cancellation, and attach context as a new package draft.
- Retain bounded whole-repository Git source at an exact commit for graph and
  coding work, while keeping review artifacts and source coverage explicit.
- Check whether a local repository ref still matches the captured commit.
- Review through architect, QC, developer, or product perspectives. These tailor
  questions and do not grant permissions.
- Keep revisions and audit records in PostgreSQL, with conflict checks when clients
  edit or approve content that another client has changed.

Developers and agent clients using the **same API and database share the same saved
context**, subject to their workspace and repository permissions. Work belongs to
the service, not an individual browser or conversation. Agent identities can read
and author permitted work; they cannot grant design approval. Background context requests have shared execution observations. Live presence and
coding-agent execution are not implemented yet.

Authenticated review supports the browser, API, CLI, and terminal workbench. An operator
configures the OpenID Connect issuer and provisions access. The browser signs people
in and keeps its session on the server; the CLI and terminal read an API access token
from a selected file. A terminal session keeps one identity, workspace, and repository.
Explicit local mode remains available for development and cannot access authenticated
workspace packages. See the
[browser sign-in guide](docs/operations/browser-sign-in.md) and
[authenticated setup](docs/operations/authenticated-review.md). Signed synthetic tests
exercise the protocol; compatibility with a particular identity provider has not
yet been certified.

Isolated coding-worker and independent verification components are implemented;
shared execution plans, separate human authorization and write reservations are
available through the API. A trusted worker runs related tasks in isolated Docker
containers, carries earlier changes into dependent tasks, and records independent
checks through durable Temporal workflows; see
[execution setup](docs/operations/coordinated-execution.md). GitHub/GitLab publication and tracker
integrations remain planned. Spec Kit and ADRKit files can be captured as native text; their command/API
integrations are not implemented. Collected context records what was captured,
not proof that tests passed or a decision was approved.

**GitHub and GitLab** repositories can be registered for governed review. Their
remote delivery adapters are planned: pull requests and merge requests will follow
the same Conductor approval and evidence rules. The local Git collector works with a checkout from either
provider. Bounded remote reads are available for GitHub.com and GitLab.com;
repository discovery, publication, and checks adapters are still pending. The read
profiles have controlled provider tests; live provider compatibility remains unverified.

Each workspace will choose **one work tracker: Linear or Jira**. Conductor will
link tickets to related packages, changes across GitHub/GitLab repositories, and
verification evidence, with explicit rules for synchronizing fields and status.
This does not mirror tickets between Linear and Jira. Ticket updates will not grant
design approval or turn missing verification into a passing result. See the
[work-tracking plan](docs/architecture/work-tracking.md).

## Run locally

You need Go 1.25+ (tested toolchain 1.26.8), Node.js 22.12+, npm, Git, and Docker with Compose. Go dependencies
and frontend dependencies have committed lockfiles.

```bash
./scripts/start-local-db.sh
export DATABASE_URL='postgres://conductor:conductor@localhost:5432/conductor?sslmode=disable'
CONDUCTOR_AUTH_MODE=local CONDUCTOR_ADDR=127.0.0.1:8080 go run ./cmd/conductord
```

The startup script creates a persistent PostgreSQL volume and initializes empty
databases with ordered migrations. Existing review data is retained; an older
database stops setup with explicit upgrade instructions. It also works with Docker
Snap when the checkout is under `/mnt`.
If Docker access has just been enabled, start a new login session or use
`sg docker -c './scripts/start-local-db.sh'` until your session has the new group.

In another terminal, start the React/TypeScript workbench:

```bash
npm --prefix apps/web ci
npm --prefix apps/web run dev -- --host 127.0.0.1
```

Open the local URL printed by Vite. The workbench forwards API requests to port
8080; set `CONDUCTOR_API_URL` to use another local API address.

Create a synthetic package and find it from another client:

```bash
go run ./cmd/conductor create --actor developer --title 'Review replay handling'
go run ./cmd/conductor list --actor reviewer
```

Use the returned change ID to submit and inspect it:

```bash
go run ./cmd/conductor submit --actor developer --revision 1 CHG-...
go run ./cmd/conductor show --actor reviewer CHG-...
go run ./cmd/conductor history --actor reviewer CHG-...
```

For an interactive terminal session, browse shared packages or open a change:

```bash
go run ./cmd/conductor tui --actor reviewer
go run ./cmd/conductor tui --actor reviewer CHG-...
```

The terminal workbench previews imported JSON files and asks for confirmation
before submitting, revising, or approving the displayed content. See the
[terminal review guide](docs/operations/terminal-review.md) for the full workflow.

The [context and history walkthrough](docs/operations/context-review.md) explains
capturing repository files, checking freshness, and reviewing changes. The
[local development guide](docs/operations/local-development.md) covers approval,
configuration, troubleshooting, and database lifecycle.

## How it is built

- **Go and Bubble Tea:** HTTP API, domain rules, PostgreSQL store, shared client,
  CLI, and interactive terminal workbench.
- **PostgreSQL:** shared package revisions, approvals, workspace membership,
  repository permissions, and audit history.
- **React 19 and TypeScript:** browser review workbench, built with Vite and plain CSS.
- **Temporal Go SDK 1.44.1:** background context sequencing, retries and cancellation;
  the first worker uses a trusted local Temporal server.
- **MCP Go SDK 1.7.0:** a bounded stdio bridge for agents with one fixed identity
  and workspace/repository selection. See the [MCP setup guide](docs/operations/mcp.md).
- **CodeGraph 1.6.0:** the selected
  [colbymchenry/codegraph](https://github.com/colbymchenry/codegraph) Rust extraction
  kernel runs in a container without network access or credentials. Graphs currently
  cover selected review paths or explicitly requested whole-repository source; see [graph setup and limits](docs/operations/repository-graph.md).
- **Codex 0.154.0 and Claude Code 2.1.270:** pinned producer adapters inside an
  isolated Docker worker, followed by checks in a separate container. Predecessor
  patches are preserved in cumulative results. See [worker setup and verification limits](docs/operations/coding-workers.md).

The current implementation focuses on durable review, shared context, and
controlled team access. Background context collection requires an operator-enabled
repository read integration and current author permission. A separate worker uses
Temporal to collect an exact commit; PostgreSQL keeps the shared result. It never
runs repository commands or edits a package automatically. See the
[background context setup](docs/operations/durable-context.md) for credentials,
commands, recovery and limits. In the signed-in browser, open **Shared context
collections**; in the authenticated terminal, press **g**. Requests and results are
shared with other repository readers. Refresh explicitly for later progress;
request cancellation and workflow cancellation are separate facts. Attaching
context requires confirmation against the package revision you inspected.
The first deployment profile uses a local development Temporal server; production
operation and live provider compatibility remain unverified. See the
[system architecture](docs/architecture/system.md) and
[shared-context model](docs/architecture/collaboration.md) for those boundaries.
The [repository provider plan](docs/architecture/repository-providers.md) describes
how GitHub and GitLab fit into the same workflow.

## Checks and contributor guidance

```bash
go test ./...
go test -race ./...
go vet ./...
npm --prefix apps/web ci
npm --prefix apps/web run typecheck
npm --prefix apps/web run build
```

Set `CONDUCTOR_TEST_DATABASE_URL` to a test database to run the real PostgreSQL and
shared-client acceptance tests. Otherwise these tests explicitly skip. They create
and remove isolated schemas; no existing application data is reset.

Set `CONDUCTOR_TEST_TEMPORAL=1` alongside that database URL to run owned Temporal
process recovery and durable collection acceptance. Install the pinned CLI first;
see the [background context guide](docs/operations/durable-context.md).

Set `CONDUCTOR_TEST_TERMINAL=1` alongside that database URL to exercise authenticated
review and collection controls in a real terminal, using a signed synthetic issuer
and the compiled CLI.
See the [terminal guide](docs/operations/terminal-review.md) for this acceptance
command and the separate local-mode regression.

Set `CONDUCTOR_TEST_BROWSER=1` with that database URL and
`CONDUCTOR_BROWSER_PYTHON` pointing to the pinned Playwright environment to exercise
browser login, shared review and collection controls in Chromium. Build the web
app first; the [browser guide](docs/operations/browser-sign-in.md) gives setup and
commands. These client tests use synthetic identity and receipt fixtures.

Set `CONDUCTOR_TEST_PROCESS_RESTART=1` when running the acceptance tests to also
verify persistence across real API and PostgreSQL process restarts. This separate
test creates and removes its own temporary Docker container and volume. It does
not restart your development database. See the
[local development guide](docs/operations/local-development.md) for the command.

Read [AGENTS.md](AGENTS.md) for engineering rules; [CLAUDE.md](CLAUDE.md) points to the
same instructions. Keep changes focused, comment the reasons behind important
invariants, and update documentation alongside behavior.

- [Documentation index](docs/README.md)
- [Package review specification](specs/001-work-package-review/spec.md)
- [Context and history specification](specs/002-context-history/spec.md)
- [Authenticated workspace specification](specs/003-workspace-access/spec.md)
- [Browser sign-in specification](specs/004-browser-sign-in/spec.md)
- [Authenticated terminal specification](specs/005-authenticated-terminal/spec.md)
- [Durable context specification](specs/006-durable-context/spec.md)
- [OpenAPI contract](api/openapi.yaml)
- [AI-DLC inspiration and plan review](docs/architecture/aidlc-plan-review.md)
- [Proposed architectural decisions](docs/adr/)
