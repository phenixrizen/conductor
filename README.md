# Conductor

**Engineering intent, orchestrated.**

Conductor is a shared workspace for engineers to plan software changes, review
exact versions of a design, and keep the supporting repository context and review
history available to their team. It is being built to coordinate AI coding agents
under human architectural authority.

A **work package** describes a proposed change: its intent, design, scope, tasks,
context, and verification requirements. Every edit creates an immutable revision.
An independent reviewer approves the exact revision and digest they inspected.
Editing the package preserves that approval in history while making it ineffective
for the new revision.

## What works today

- Create, revise, submit, inspect, and approve packages through the HTTP API or Go CLI.
- Browse shared changes and filter by repository so teammates can find related work.
- Inspect historical revisions, approvals, and audit events; compare content in the web workbench.
- Capture selected specification, ADR, and other text files from one Git commit,
  with their original content, source IDs, and explicit collection gaps.
- Check whether a local repository ref still matches the captured commit.
- Review through architect, QC, developer, or product perspectives. These tailor
  questions and do not grant permissions.
- Keep revisions and audit records in PostgreSQL, with conflict checks when clients
  edit or approve content that another client has changed.

Developers and agent clients using the **same API and database share the same saved
context**. Work belongs to the service, not an individual browser or conversation.
The shared list shows recorded work; live presence and execution tracking are not
implemented yet.

The current identity header is for local development. Authenticated workspaces,
repository permissions, coding-agent execution, GitHub/GitLab publication, and
production integrations are planned. Spec Kit and ADRKit files can be captured as native text;
their command/API integrations are not implemented. Collected context is evidence
of what was captured, not proof that tests passed or a decision was approved.

Both **GitHub and GitLab** are planned providers for managed repositories: GitHub
pull requests and GitLab merge requests will follow the same Conductor approval
and evidence rules. The local Git collector works with a checkout from either
provider; remote discovery, publication, and checks adapters are still pending.

Each workspace will choose **one work tracker: Linear or Jira**. Conductor will
link tickets to related packages, changes across GitHub/GitLab repositories, and
verification evidence, with explicit rules for synchronizing fields and status.
This does not mirror tickets between Linear and Jira. Ticket updates will not grant
design approval or turn missing verification into a passing result. See the
[work-tracking plan](docs/architecture/work-tracking.md).

## Run locally

You need Go 1.24+, Node.js 22.12+, npm, Git, and Docker with Compose. Go dependencies
and frontend dependencies have committed lockfiles.

```bash
./scripts/start-local-db.sh
export DATABASE_URL='postgres://conductor:conductor@localhost:5432/conductor?sslmode=disable'
CONDUCTOR_ADDR=127.0.0.1:8080 go run ./cmd/conductord
```

The startup script creates a persistent local PostgreSQL volume and initializes an
empty database. It also works with Docker Snap when the checkout is under `/mnt`.
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

The [context and history walkthrough](docs/operations/context-review.md) explains
capturing repository files, checking freshness, and reviewing changes. The
[local development guide](docs/operations/local-development.md) covers approval,
configuration, troubleshooting, and database lifecycle.

## How it is built

- **Go:** HTTP API, domain rules, PostgreSQL store, shared client, and CLI.
- **PostgreSQL:** shared package revisions, approval records, and audit history.
- **React 19 and TypeScript:** browser review workbench, built with Vite and plain CSS.

The current implementation focuses on durable review and shared context. Temporal
workflow execution and external adapters are later increments. See the
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

Read [AGENTS.md](AGENTS.md) for engineering rules; [CLAUDE.md](CLAUDE.md) points to the
same instructions. Keep changes focused, comment the reasons behind important
invariants, and update documentation alongside behavior.

- [Documentation index](docs/README.md)
- [Package review specification](specs/001-work-package-review/spec.md)
- [Context and history specification](specs/002-context-history/spec.md)
- [OpenAPI contract](api/openapi.yaml)
- [AI-DLC inspiration and plan review](docs/architecture/aidlc-plan-review.md)
- [Proposed architectural decisions](docs/adr/)
