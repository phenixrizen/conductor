# Conductor

**Engineering intent, orchestrated.**

Conductor coordinates engineers and coding agents in a shared workspace. Teams
review software designs, discover relationships across repositories, authorize
bounded implementation work, inspect its evidence, and publish reviewed changes
to GitHub or GitLab. PostgreSQL keeps the shared record; Temporal coordinates
background work and recovery.

A **work package** describes a change: its intent, design, scope, tasks, context
and verification requirements. Every edit creates an immutable revision. An
independent human approves the exact revision and digest they inspected. Editing
it preserves that historical approval while requiring review of the new revision.
Design approval, permission to execute, publication, merge, deployment and production
outcome are separate facts.

The [full release contract](docs/full-release.md) records implementation, verification
and the ordered PR stack. This branch contains release work awaiting review and
merge. It has not been deployed. The [documentation map](docs/README.md) connects
each capability to its specification, setup and tested limits.

## What you can do

| Workflow | Implemented behavior |
|---|---|
| Shared design review | Create, edit, submit and independently approve immutable work packages; inspect history, comparisons and audit records |
| Repository context | Capture selected files or bounded whole-repository source at an exact Git commit; share receipts and expose missing, stale or unavailable source |
| Cross-repository understanding | Build shared dependency and symbol graphs with the selected CodeGraph Rust extractor; read exact retained source from related authorized repositories |
| Coordinated agents | Propose dependent tasks, inspect source/design/profile pins, obtain separate human execution authorization, run independent work concurrently, and recover interrupted work |
| Implementation evidence | Inspect complete retained patches, producer reports and independent check output, including failed tasks and read-only design reports |
| GitHub and GitLab delivery | Authorize an exact artifact for a new draft PR/MR; reconcile uncertain publication and retain provider check, merge and deployment observations |
| Work tracking | Select one tracker per workspace, Linear or Jira; link existing tickets to package revisions and publication receipts, synchronize planning context and resolve conflicting Conductor-owned links |
| Specifications and decisions | Run pinned Spec Kit scaffold/template/prerequisite commands and ADRKit Proposed-ADR, lint, applicability and graph commands inside isolated workers |
| Runtime evidence | Collect scoped Groundcover metrics, logs and traces for an exact deployment/commit/window; compare complete evidence with explicitly approved criteria |
| Team access | Use a configurable OpenID Connect provider for browser sign-in; provision server-owned human/agent identities and workspace/repository permissions |

The React/TypeScript browser workbench has six workflow tabs: Review, Source &
graph, Agent work, Delivery, Tracker and Runtime. The browser, Go CLI, Bubble Tea
terminal and MCP bridge use the same API and authorization rules. Human review perspectives help organize the questions to ask;
they confer no permissions. Agents may author permitted work and produce evidence,
but cannot grant design, execution or publication approval.

Developers and agents using the **same API and database share the same saved
context**. Work is not private to a browser or conversation. Cross-repository reads
require current access to every included repository. Shared execution plans and
path reservations expose overlapping work; live presence and private conversation
synchronization are not implemented.

Coding workers receive bounded source and produce patches in isolated Docker
containers. Independent checks run separately. A trusted publication worker holds
repository write credentials; repository-controlled commands receive no publication,
production or Conductor credentials. Collected source, an agent's opinion, a ticket
status or a successful deployment cannot substitute for passing verification.

## Run locally

Use Go 1.25+ with the tested Go 1.26.8 toolchain, Node.js 22.14.0, npm, Git and
Docker with Compose. Dependencies and frontend lockfiles are committed.

```bash
./scripts/start-local-db.sh
export DATABASE_URL='postgres://conductor:conductor@localhost:5432/conductor?sslmode=disable'
CONDUCTOR_AUTH_MODE=local CONDUCTOR_ADDR=127.0.0.1:8080 go run ./cmd/conductord
```

The script creates a persistent development database, preserves existing data and
reports required upgrades. It supports Docker Snap with checkouts under `/mnt`.
After enabling Docker group access, start a new login session or use
`sg docker -c './scripts/start-local-db.sh'` until the current session has that group.

Start the React/TypeScript workbench in another terminal:

```bash
npm --prefix apps/web ci
npm --prefix apps/web run dev -- --host 127.0.0.1
```

Open Vite's printed URL. The development proxy forwards API calls to port 8080;
`CONDUCTOR_API_URL` selects another local API address.

Create synthetic work and inspect it from another client:

```bash
go run ./cmd/conductor create --actor developer --title 'Review replay handling'
go run ./cmd/conductor list --actor reviewer
go run ./cmd/conductor show --actor reviewer CHG-...
go run ./cmd/conductor tui --actor reviewer
```

Use the actual returned change ID in place of `CHG-...`. Explicit local mode
supports local review only and cannot access authenticated workspace packages.
Shared collection, coding, delivery and tracker workflows require the authenticated
setup below. See [local development](docs/operations/local-development.md) for more.

## Configure a shared team workspace

Start with [authenticated review](docs/operations/authenticated-review.md) and
[browser sign-in](docs/operations/browser-sign-in.md). An operator configures the
OIDC issuer and provisions identities, workspaces and canonical repository grants.
The browser uses an opaque server session. CLI, terminal and MCP sessions read an
explicitly selected API-token file and keep identity and scope fixed until exit.

Browser sign-in is qualified against actual self-hosted Keycloak 26.7.3 over HTTPS;
see [provider qualification](docs/operations/keycloak-qualification.md). API tokens
use the documented RFC 9068 RS256 profile. Keycloak's default access tokens and ID
tokens are rejected by that API profile; browser qualification does not imply bearer
token compatibility with every provider. Interactive terminal login and token
refresh are not implemented.

Enable the integrations you need using separate operator-owned configuration:

- [MCP](docs/operations/mcp.md): connect agents to shared scoped commands and evidence.
- [Repository source](docs/operations/durable-context.md) and [CodeGraph](docs/operations/repository-graph.md): collect exact commits and build shared graphs.
- [Coordinated execution](docs/operations/coordinated-execution.md): configure isolated profiles, execution grants, source pins and independent checks.
- [Native design tools](docs/operations/design-tools.md): create or validate Spec Kit/ADRKit artifacts without inheriting approval from their contents.
- [Repository delivery](docs/operations/repository-delivery.md): configure trusted GitHub/GitLab publication and provider observations.
- [Linear or Jira](docs/operations/work-tracking.md): select one tracker and define synchronization ownership and status mappings.
- [Runtime evidence](docs/operations/runtime-evidence.md): bind Groundcover collection to allowed services, metrics and approved requirement criteria.
- [Release terminal](docs/operations/release-terminal.md): inspect and act through the CLI or authenticated interactive terminal.

For shared operation, use the [release runbook](docs/operations/release.md) and
[Temporal TLS/mTLS setup](docs/operations/temporal-tls.md). They cover reproducible
Linux archives, HTTPS, separate service accounts, checked migrations, private
health/queue metrics and PostgreSQL backup/restore into an empty recovery database.
Temporal history and runtime identity require their own supported retention and
recovery procedure; a PostgreSQL backup does not replace them.

## Underlying tools

| System | Responsibility and tested pin |
|---|---|
| Go, Bubble Tea | API, domain rules, shared client, CLI and interactive terminal; Go toolchain 1.26.8 |
| PostgreSQL 17 | Shared revisions, approvals, identities, evidence, durable dispatch and audit history |
| React 19, TypeScript, Vite | Browser workflow workbench with plain CSS |
| Temporal Go SDK 1.44.1 | Durable context, task DAG, delivery, tracker and runtime sequencing; tested CLI 1.8.3 / server 1.31.2 |
| MCP Go SDK 1.7.0 | Authenticated stdio bridge with fixed workspace/repository scope |
| [CodeGraph 1.6.0](https://github.com/colbymchenry/codegraph) | Selected upstream's pinned native Rust extraction kernel in an isolated container |
| Codex 0.154.0 / Claude Code 2.1.270 | Pinned producer adapters behind bounded execution profiles and a credential gateway |
| Spec Kit 1.0.6 / ADRKit CLI 0.13.0 | Separately pinned native design artifacts and deterministic commands; no implied extension compatibility |
| GitHub / GitLab | Provider-specific adapters under common publication and observation commands |
| Linear / Jira | One workspace-selected tracker with explicit field ownership |
| Groundcover SDK schema 1.424.0 | Inspected REST compatibility profile for read-only correlated telemetry; Conductor uses a bounded direct HTTP adapter |

Exact source commits, image pins, protocol boundaries and evidence are documented
in each integration's research and runbook. AI-DLC inspired role perspectives,
explicit stage inputs, source-bound review, recovery and evidence distinctions;
Conductor does not import a second workflow authority. See the
[AI-DLC plan review](docs/architecture/aidlc-plan-review.md).

## Verification and limits

[Complete release acceptance](docs/operations/full-release-acceptance.md) joins two
synthetic repositories through actual Git, native CodeGraph, compiled MCP/executor
processes, Docker producers/checks, PostgreSQL and Temporal. Controlled provider
HTTP endpoints exercise both GitHub/GitLab draft publication and each Linear/Jira
workspace choice, including lost acknowledgments, exact Git trees, runtime criteria,
related-repository revocation and restart recovery. Actual Chromium and PTY tests
cover the shared interfaces. Backup/restore and deterministic archive builds have
also been exercised.

These results establish the documented local and protocol behavior. Live SaaS write
qualification, paid model inference, hosted Temporal operation and a deployment of
Conductor remain unverified. A retained historical runtime criterion result does
not establish current health or a broad production outcome. GitHub.com/GitLab.com
are the implemented hosted repository profiles; do not infer enterprise-provider
compatibility. Tracker integration links existing tickets and does not create them,
change their status/assignment, or mirror Linear and Jira.

Run the applicable checks in [AGENTS.md](AGENTS.md), including explicit opt-ins for
PostgreSQL, browser, terminal, Docker, Temporal and process recovery. Missing opt-ins
produce reported skips, not passes. The [release contract](docs/full-release.md)
tracks the remaining review and verification gates.

## Contributing

Read [AGENTS.md](AGENTS.md), [CLAUDE.md](CLAUDE.md) and the applicable specification
before changing behavior. Keep source/evidence bounds, immutable review digests,
server-derived authority, cancellation and recovery intact. Proposed ADRs remain
proposed until an authorized person accepts them. Use focused commits and review
stacked PRs in their documented dependency order; publishing a PR is not authority
to merge or deploy it.
