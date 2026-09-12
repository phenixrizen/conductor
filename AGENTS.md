# Conductor repository instructions

These instructions apply to the entire repository. A more deeply nested
`AGENTS.md` may add or override instructions for files in its directory tree.

## Mission

Conductor is an architect-governed platform for software design and agentic
programming: **Engineering intent, orchestrated.** Implement it incrementally as
working, tested software. Favor a small complete workflow over broad scaffolding.

The current focus is durable work-package review, shared discovery, and pinned
repository context. Read these before changing behavior:

1. `docs/README.md` — documentation map and status vocabulary.
2. `specs/001-work-package-review/spec.md` — normative feature behavior.
3. `specs/001-work-package-review/plan.md` — refined delivery sequence.
4. `docs/architecture/milestone-1.md` — current model and invariants.
5. `.specify/memory/constitution.md` — governing principles.
6. Applicable proposed decisions under `docs/adr/`.
7. `specs/002-context-history/spec.md` and `plan.md` for shared discovery, history,
   context provenance, evidence states, and client behavior.
8. `docs/architecture/collaboration.md` for shared-data and authority boundaries.

Do not describe an incomplete integration or mocked path as implemented. Keep
**design approved**, **implementation produced**, **implementation verified**,
**merged**, **deployed**, and **production outcome verified** distinct.

## Authority and approval safety

- Agents may propose architecture; they cannot accept their own ADRs, grant their
  own approvals, waive checks, reduce risk, or invent domain decisions.
- Leave new ADRs as `Proposed` unless an authorized person explicitly accepts them.
- Approval binds to the exact revision and digest the reviewer inspected. Never
  fetch newer content as part of an approval action or carry approval onto an edit.
- Derive actor identity on the server. Never add an arbitrary approval actor to a
  client-controlled payload.
- Route lifecycle changes through version-checked domain commands. UI code and
  persistence workers must not assign lifecycle state directly.
- Preserve historical revisions, approval records, evidence, and concise audit
  events. Effective approval is derived for the current revision and digest.
- Surface unknown, stale, unavailable, truncated, or unexecuted evidence explicitly.
  Missing evidence must never be rendered as passing.

## Product and integration boundaries

- GitHub and GitLab are required delivery providers for repositories Conductor
  manages. GitHub also hosts Conductor itself; keep project hosting separate from
  the configured provider of each managed repository. Use common domain commands
  with provider-specific adapters and explicit capability limits. See
  `docs/architecture/repository-providers.md` before adding provider behavior.
- PostgreSQL owns package revisions, approval records, authorization metadata, and
  audit events. Temporal will own durable execution sequencing when introduced.
  Do not create two execution authorities.
- External calls belong in workflow activities. Bridge database commits and future
  Temporal operations using durable inbox/outbox processing and reconciliation.
- Coding workers will produce patches; a separate trusted integration service will
  publish them. Repository-controlled commands must not receive publication or
  production credentials.
- Before adding Spec Kit, ADRKit, CodeGraph, Groundcover, Temporal, GitHub, GitLab,
  Linear, Jira, Claude Code, or Codex integration code, inspect current official
  documentation and source, pin a tested version, and record actual capabilities
  and limitations.
- Do not invent upstream commands, schemas, licensing, resumption behavior, tier
  availability, or compatibility guarantees.
- Each workspace selects one work tracker: Linear or Jira. Synchronize Conductor
  and linked repository work with that tracker; do not introduce Linear-to-Jira
  mirroring. Keep field ownership and synchronization mappings explicit: ticket
  state cannot grant package approval, establish a passing check, or prove a merge
  or deployment. See `docs/architecture/work-tracking.md` before adding
  synchronization behavior.

## Repository layout

Add directories only when they contain working functionality; do not create empty
packages to mirror the target diagram.

| Path | Responsibility |
|---|---|
| `cmd/conductor/` | CLI and future Bubble Tea entry point |
| `cmd/conductord/` | HTTP control-plane server |
| `internal/domain/` | Domain types, invariants, and typed errors |
| `internal/service/` | Version-checked use cases and command orchestration |
| `internal/api/` | HTTP transport, local auth boundary, and error mapping |
| `internal/store/` | PostgreSQL transaction implementation |
| `internal/repositorycontext/` | Bounded local Git artifact collection and freshness checks |
| `pkg/client/` | Reusable Go API client |
| `apps/web/` | React/TypeScript review workbench |
| `api/openapi.yaml` | Implemented HTTP contract |
| `migrations/` | Ordered PostgreSQL schema migrations |
| `specs/` | Feature specifications and implementation plans |
| `docs/adr/` | Architectural decision records |
| `docs/architecture/` | Current and target architecture documentation |
| `docs/operations/` | Reproducible runbooks |
| `tests/` | Cross-component fixtures and acceptance tests when introduced |

## Shared context and history

- Clients using the same API share the PostgreSQL dataset. Keep package context
  and review facts in the service, not private browser or agent-session memory.
- Shared discovery filters author-supplied repository labels exactly. These labels
  and human perspective selectors confer no authorization. Authenticate users and
  enforce workspace/repository membership before shared deployment.
- Historical approvals describe the inspected historical revision. They must not
  be rendered as effective approval for the latest revision or enable an approval
  action from a historical view.
- Preserve bounded keyset pagination and explicit truncation flags. Do not silently
  return an incomplete history as if it were complete.
- `repositoryContext` is a versioned optional content field. Validate it without
  discarding unknown extension fields or changing the immutable package digest.
- Context collection reads explicit paths from a resolved local Git commit, not
  dirty working-tree files. Keep path/output bounds, literal path handling, blocked
  transports, and cancellation. Never run repository scripts during collection.
- Collected source is not passed verification. Missing, unavailable, truncated, and
  stale evidence must stay visible. Client-supplied digests establish internal
  consistency, not authenticated provenance.
- Native Spec Kit/ADRKit files are currently imported as text artifacts. Do not
  claim command/API compatibility, accepted decisions, or execution authority from
  their contents.

## Go conventions

- Keep one Go module until a demonstrated isolation or release requirement justifies
  another. If modules are added, test every module independently.
- Put domain policy in `internal/domain` or `internal/service`, not HTTP handlers,
  CLI branches, React components, or raw SQL alone.
- Depend on narrow interfaces at boundaries. Keep transport errors and persistence
  details from leaking into domain rules.
- Wrap errors with useful operation context and preserve typed errors with `%w`.
  Map expected domain errors to stable API codes; do not expose internal details.
- Accept `context.Context` for I/O and propagate cancellation. Do not store contexts
  in structs.
- Bound decoded inputs and external responses. Reject unknown command fields where
  compatibility permits and validate before opening consequential transactions.
- Use UTC timestamps and inject clocks where deterministic tests need them.
- Format changed Go files with `gofmt`. Run `go vet` and race-enabled tests.
- Never put `try`/`catch`-style wrappers around imports.

## PostgreSQL and migration conventions

- Package mutations and their audit events must commit in one transaction.
- Serialize commands for the same package and enforce optimistic revision checks;
  allow unrelated packages to progress independently.
- Prefer append-only facts and constraints that protect revision/digest integrity.
  Avoid writable status columns that can contradict history.
- Add a new ordered migration for a schema change. Never rewrite an already applied
  migration after it has shipped.
- Reconcile ambiguous external results before retrying; do not claim globally
  exactly-once behavior.
- Include real PostgreSQL integration coverage for transaction, concurrency, and
  restart behavior when the environment supports it. Mocks prove domain behavior,
  not live database compatibility.

## API and client conventions

- Keep `api/openapi.yaml`, handlers, shared clients, examples, and tests aligned.
- Consequential requests carry the inspected revision and, for approval, its digest.
- Return bounded typed errors with stable codes and correlation IDs.
- Treat `X-Conductor-Actor` as explicitly local-development-only. It is not a
  security boundary and must be replaced before shared deployment.
- Escape resource IDs as path segments. Use idempotency keys for retryable external
  commands when those capabilities are introduced.
- Do not expose fake success responses for unavailable integrations.

## Web and terminal interfaces

- Build workflow interfaces, not generic chat surfaces. All interfaces use the same
  API commands and authorization rules.
- Preserve unknown structured package fields during form round trips.
- Make the displayed revision and digest explicit before approval. A stale response
  must require renewed inspection.
- Provide loading, empty, stale, unavailable, blocked, and error states. Do not rely
  on color alone; retain text status labels and keyboard accessibility.
- Sanitize untrusted Markdown, links, logs, and terminal control sequences. Do not
  add a privileged embedded terminal in the initial workbench.
- For perceptible web changes, run the UI checks and capture a screenshot when
  browser tooling is available. If unavailable, report that limitation.
- Commit reviewed frontend lockfiles and use reproducible installs. Do not fabricate
  a lockfile or checksum when registries are unavailable.

## Documentation

- Update the feature spec, plan, OpenAPI contract, architecture guide, runbook, and
  implementation status whenever the corresponding behavior changes.
- Clearly label diagrams and prose as current, partial, planned, or proposed.
- Use Mermaid for architecture, state, sequence, and data-flow diagrams when it
  improves comprehension. Keep a textual or table equivalent for important review
  information.
- Keep local Markdown links valid and code fences balanced.
- Keep the README understandable to a new developer: describe the purpose, current
  capabilities, shared-data model, setup, and limits in plain language. Comment code
  where the rationale or invariant is important; avoid restating obvious operations.
- Use synthetic examples only. Never include company source, customer data,
  credentials, production topology, or protected health information.

## Testing and completion

Run the narrowest relevant checks while developing, then the full applicable set:

```bash
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
npm --prefix apps/web ci
npm --prefix apps/web run typecheck
npm --prefix apps/web run build
```

Also validate `api/openapi.yaml`, local documentation links, migrations, and
meaningful UI acceptance paths when changed. A command blocked by missing Docker,
network, credentials, dependencies, or browser tooling is a reported limitation,
not a pass. Do not fabricate a successful check.

For persistence, history, shared-client, or context workflow changes, set
`CONDUCTOR_TEST_DATABASE_URL` and run the applicable live PostgreSQL tests, including
`tests/acceptance`. An unset variable produces explicit skips. Tests create isolated
schemas and must clean them up. Connection-pool reopen is not a database restart.
Use `scripts/start-local-db.sh` for the persistent local database; it pipes inputs
to Docker to support Snap installations with checkouts outside the home directory.

Before finishing:

1. Review `git diff` and preserve unrelated work.
2. Confirm generated files and lockfiles are intentional.
3. Ensure no secrets, PHI, company code, or production details were added.
4. Report implemented behavior, design decisions, exact checks and results,
   limitations, and the next concrete increment.
5. Do not merge, deploy, waive checks, force-push, or select a project license
   without explicit authority.
