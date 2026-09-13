# Conductor repository instructions

These instructions apply to the entire repository. A more deeply nested
`AGENTS.md` may add or override instructions for files in its directory tree.

## Mission

Conductor is an architect-governed platform for software design and agentic
programming: **Engineering intent, orchestrated.** Implement it incrementally as
working, tested software. Favor a small complete workflow over broad scaffolding.

The current target is the full coordinated platform, including cross-repository
relationships, CodeGraph, MCP, coding agents, delivery and tracker integration.
Read `docs/full-release.md` for required gates and the user-authorized stacked PR
workflow. A partial pilot does not satisfy that release target.
Read these before changing behavior:

1. `docs/README.md` — documentation map and status vocabulary.
2. `specs/001-work-package-review/spec.md` — normative feature behavior.
3. `specs/001-work-package-review/plan.md` — refined delivery sequence.
4. `docs/architecture/milestone-1.md` — current model and invariants.
5. `.specify/memory/constitution.md` — governing principles.
6. Applicable proposed decisions under `docs/adr/`.
7. `specs/002-context-history/spec.md` and `plan.md` for shared discovery, history,
   context provenance, evidence states, and client behavior.
8. `docs/architecture/collaboration.md` for shared-data and authority boundaries.
9. `specs/003-workspace-access/spec.md` and `plan.md` for authenticated API/CLI
   review; `docs/operations/authenticated-review.md` for supported identity,
   provisioning, and deployment limits.
10. `specs/004-browser-sign-in/spec.md` and `plan.md` for browser sessions and
    `docs/operations/browser-sign-in.md` for provider and HTTPS setup.
11. `specs/005-authenticated-terminal/spec.md` and `plan.md` for authenticated
    terminal identity, scope, and recovery; `docs/operations/terminal-review.md`
    for controls and real PTY acceptance.
12. For remote context or Temporal, read
    `specs/006-durable-context/spec.md` and `plan.md`,
    `docs/architecture/durable-context.md`, `docs/operations/durable-context.md`, and ADR 0003 (still Proposed). The selected collection
    policy requires author permission plus an operator-enabled repository read
    integration; it does not grant coding or publication authority.

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
  audit events. Temporal owns durable context and coordinated coding execution sequencing.
  Do not create two execution authorities.
- External calls belong in workflow activities. Bridge database commits and Temporal
  operations using durable inbox/outbox processing and reconciliation.
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
| `cmd/conductor/` | CLI and Bubble Tea entry point |
| `cmd/conductord/` | HTTP control-plane server |
| `cmd/conductor-worker/` | Trusted local Temporal worker and context dispatcher |
| `cmd/conductor-executor/` | Trusted local Temporal coding worker, dispatcher and orphan recovery |
| `cmd/conductor-admin/` | Trusted database-operator access provisioning |
| `cmd/conductor-mcp/`, `internal/mcpserver/` | Authenticated fixed-scope MCP stdio bridge through the shared API |
| `cmd/conductor-sandbox/`, `internal/execution/` | Isolated patch producers, credential gateway, and separate verification |
| `internal/domain/` | Domain types, invariants, and typed errors |
| `internal/service/` | Version-checked use cases and command orchestration |
| `internal/api/` | HTTP transport, explicit authentication modes, and error mapping |
| `internal/authn/` | Bounded issuer discovery, API token verification, and browser code exchange |
| `internal/store/` | PostgreSQL transaction implementation |
| `internal/repositorycontext/` | Bounded local Git collection, remote provider reads, and local freshness checks |
| `internal/repositorygraph/`, `internal/codegraph/` | Shared graph projection and isolated pinned CodeGraph extraction |
| `internal/coordinationworkflow/` | Temporal task DAG sequencing using opaque references and retained receipt digests |
| `internal/coordinationworker/` | Reviewed profile matching, immutable attempts, Docker activity and cleanup reconciliation |
| `internal/collectionworker/` | Credential binding, context activity, fenced dispatch, and reconciliation |
| `internal/contextworkflow/` | Pinned Temporal workflow, runtime identity and history validation |
| `internal/tui/` | Interactive terminal review through the shared Go API client |
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

- Plan imports reject duplicate/unknown command fields and require a structured
  preview. Browser execution decisions use the displayed run digest without a
  refresh. Uncertain decisions require renewed inspection. MCP may propose/read
  runs but must not expose human execution authorization or run cancellation.
- Coordinated plans bind exact package, graph and source revisions. Human execution
  permission is provisioned separately from design approval. Check it on every
  included repository in the admission transaction; agents cannot authorize runs.
  Cancellation intent does not release active path claims. Temporal owns sequencing;
  database observations and missing checks cannot establish successful execution.

- Browser graph creation captures inspected receipt and optional whole-source
  digests. An uncertain response retains the exact input/key for explicit retry.
  Scope changes and source denial clear the captured graph and source selections.

- A repository graph is visible only while the principal can read every included
  repository. Apply this before pagination and retain explicit coverage gaps and
  unknown freshness. Graph creation projects stored receipts and indexes; it must
  not perform external collection or turn structural relationships into verification.
- CodeGraph indexing belongs in the collection activity, with its receipt and index
  committed together. Pin an immutable image ID. The trusted launcher sets and checks
  Linux no_new_privs before parsing; never remove the restriction to work around
  Docker Snap startup behavior. Keep source and credentials outside workflow history.

- Clients using the same API share the PostgreSQL dataset. Keep package context
  and review facts in the service, not private browser or agent-session memory.
- Shared discovery enforces server-owned workspace membership and repository
  grants before pagination. Author-supplied repository labels remain optional
  filters; these labels and human perspective selectors confer no authorization.
- Authenticated package ownership is immutable and separate from revision content.
  Provider, normalized host, and stable provider repository ID identify a managed
  repository within its workspace. A selected repository narrows all package
  operations, not only creation and listings.
- Resolve verified issuer/subject pairs to active PostgreSQL principals. Human or
  agent kind and access grants come from the server, never token role claims or
  client actor fields. An agent cannot approve even if a grant says otherwise.
- Hold principal, membership, and consequential repository-permission checks in
  the transaction that commits the domain command and audit event. Do not move
  authorization into a separate preflight query that can race with revocation.
- Local mode may access only unscoped legacy/local packages. Authenticated mode
  cannot access them, and local actor headers cannot access scoped packages even
  when both modes use the same database. Never assign legacy ownership from labels.
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
- Remote collection must preserve existing snapshot versions and digests.
  A receipt ID supplied in JSON is not authenticated provenance. Resolve trusted
  receipt linkage under canonical scope and compare the entire version 2 snapshot JSON;
  never refresh or attach a result as part of approval. Version 1 extensions, including
  case aliases of new fields, retain their original meaning and immutable digest. Keep provider credentials and source text outside workflow history.

- Remote collection requires OIDC, a selected workspace/repository, current author
  permission and an operator-enabled read integration. Request, audit and outbox
  commit together. Check access before every provider call and at receipt commit;
  a committed receipt wins an activity retry even after later revocation.
- Bind first dispatch to the actual Temporal cluster/namespace identity before RPC.
  Keep lease fencing, exact run/binding reconciliation and conservative missing-history
  handling. Never start a replacement for a known missing run or a committed receipt.
  PostgreSQL stores observations, not a second execution state machine. Unknown
  acknowledgment, stale progress and unresolved recovery must stay explicit.
- Provider/source text and credentials never enter Temporal payloads, errors, logs
  or heartbeat details. Keep the worker's initial Temporal mode explicitly local;
  do not imply hosted/TLS or production compatibility. Fixture tests do not establish
  live GitHub/GitLab compatibility. See the durable-context runbook for tested bounds.

- Coordinated attempts are immutable and admitted before a producer starts. A
  redelivery recovers the committed receipt or reconciles the same attempt; it must
  never start a second producer. Load full bundles and maximal predecessor patches
  under the recorded human's current all-repository grants and inspected pins.
  A public profile ID alone is insufficient: bind its digest and immutable image.
- Release write claims only after observed terminal Temporal execution, every
  task's non-unresolved receipt and confirmed cleanup of all admitted attempts.
  A receipt by itself does not prove an unknown workflow stopped. Source, prompts,
  commands and credentials stay out of history/logs; repository commands receive
  no publication, production or Conductor credentials. See the
  [coordinated execution runbook](docs/operations/coordinated-execution.md).

## Go conventions

- The module requires Go 1.25 and pins the tested Go 1.26.8 toolchain for MCP SDK
  1.7.0. Run checks with that toolchain; keep its version documented with upgrades.
- MCP stdio uses one token-file credential and fixed workspace/repository until
  exit. Expose no approval tool or caller-selected identity. Preserve exact write
  inputs without preflight refreshes and keep stdout exclusively for MCP frames.

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
- Require an explicit authentication mode. `CONDUCTOR_AUTH_MODE=local` binds only
  to loopback and uses `X-Conductor-Actor` for local development. OIDC mode rejects
  that header and fails closed; never fall back to local identity after failure.
- OIDC mode currently accepts the documented RFC 9068 RS256 access-token profile
  from a configured HTTPS issuer and audience. Do not claim arbitrary JWT, ID-token,
  opaque-token, or identity-provider compatibility from synthetic issuer tests.
- Keep access tokens out of logs, actor fields, command-line arguments, and
  repository commands. CLI and TUI commands read an explicitly selected token
  file; the browser uses an opaque server session. The terminal reads its token
  once for the session. Do not claim interactive CLI login or token refresh.
- Browser login uses a fixed HTTPS origin, authorization code flow, S256 PKCE,
  browser-bound one-use state, and nonce validation. ID tokens authenticate login
  only; never treat them as API bearer tokens or persist provider tokens.
- Ignore bounded, unrecognized OAuth callback extensions as the protocol requires;
  do not reinterpret them as scope or command inputs. Reject duplicate callback
  parameters, and keep package commands strict about their own query/body fields.
- Cookie commands require exact Origin and a session-bound CSRF header. Browser
  reads send that header too, preventing old tabs from acting as a newly signed-in
  account. Identity or scope changes clear inspection and cancel pending requests.
- Preserve server-side session expiry, admission limits, revocation, and hashed
  cookie credentials. Expired rows remain unusable even before later creation
  prunes them. Sign-out does not claim provider logout or cancellation of commands
  already authorized.
- Access provisioning is a trusted database-operator command, not a public API.
  Its operator label is audit context, not proof of identity. Apply bounded config
  updates with an audit event atomically; omitted records stay unchanged and false
  capability/active values revoke access without deleting historical attribution.
- Escape resource IDs as path segments. Use idempotency keys for retryable external
  commands when those capabilities are introduced.
- Do not expose fake success responses for unavailable integrations.
- Go API commands must reject redirects, including with a caller-provided HTTP
  client. A redirected POST may be replayed or converted into a GET; neither is a
  valid substitute for the command the user confirmed.
- Disable transport-level POST replay even when a command carries an idempotency
  key. Retain uncertainty so the caller explicitly retries the same captured key
  and input; a hidden retry must not mask a lost acknowledgment.

## Web and terminal interfaces

- Publication review loads the complete retained implementation artifact and checks
  displayed patch/output byte digests before enabling exact human authorization.
  Its GET artifact endpoint alone permits a 17 MiB browser response; ordinary reads
  remain bounded to 4 MiB. Never silently slice a patch for review. Denied source
  reads clear private patches and confirmations. Lost authorization requires renewed
  proposal and artifact inspection; provider refresh retries retain the same key.

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
- The Bubble Tea session keeps one identity and uses per-operation deadlines.
  Capture the displayed revision/digest before confirmation; never refresh inside
  a mutation. Conflicts and uncertain mutation outcomes require explicit inspection
  before another mutation. Cancel pending work when the session ends and ignore
  superseded asynchronous responses.
- An authenticated terminal requires a selected workspace and canonical repository,
  resolves its principal and capabilities through server discovery, and never uses
  a local actor. Keep its credential and scope fixed until exit. Missing or truncated
  discovery cannot invent access. Human/agent kind and capabilities govern controls;
  the server still authorizes every command.
- On terminal authentication or permission failure, clear inspection, capabilities,
  imported drafts, and confirmation state. Explicit `r` recovery rechecks access with
  the same credential before a fresh inspection. Never revalidate or load new content
  inside an approval action, and never silently replace the session's token.
- Import terminal package content only from an explicitly selected, bounded JSON
  file. Show a preview before replacing content and preserve unknown fields in the
  imported document. Escape terminal control characters in content and errors;
  repository text must never control the terminal or launch an editor/script.
- Collection workbenches keep request facts, immutable source coverage and aged
  execution observations separate. Refresh explicitly; display timers do not poll.
  Request forms/files preserve the same input and key after an uncertain result.
- Confirm cancellation and attachment from inspected facts. Attachment captures
  the package revision/digest and collection/receipt identity; never read newer
  content inside confirmation. Conflicts and uncertain outcomes block reattachment
  until the applicable package and collection inspections have been renewed;
  preserve each workbench's documented approval recovery requirements.
  Clear receipts and collection drafts along with package state on access failure.
- A version 2 JSON reference alone is not inspected receipt linkage. Compare the
  complete scoped server receipt before marking a snapshot as matching. Preserve
  version 1 extensions and show full escaped JSON for uninterpreted content.

## Documentation

- The user explicitly requested stacked PRs on 2026-09-13. Target each PR at its
  immediate prerequisite, record exact dependencies and review order, and reconcile
  the stack as earlier PRs merge. This supersedes older main-only PR guidance.
  Do not force-push or merge without explicit authority.
- The selected CodeGraph upstream is `https://github.com/colbymchenry/codegraph`.
  Inspect and pin its actual source/release; similarly named Rust projects are not
  substitutes. Keep native/fallback indexing capabilities and evidence explicit.

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

For authentication and authorization changes, run the signed-token and live
PostgreSQL acceptance paths covering human collaboration, agent approval denial,
workspace/repository isolation across every endpoint, revocation, forged identity,
legacy migration, and access-audit rollback. Synthetic issuer tests establish the
implemented protocol boundary, not compatibility with a real identity vendor.
For browser sign-in changes also run the signed code-exchange/CSRF/session tests
and opt into the actual Chromium acceptance with `CONDUCTOR_TEST_BROWSER=1`.
Set `CONDUCTOR_BROWSER_PYTHON` to the Python environment with the pinned Playwright
dependency and build the web app first; see the browser sign-in runbook. Missing
opt-in is a skip; missing dependencies after opt-in are a failure.

For API/process lifecycle or durability changes, run the opt-in process-restart
acceptance with `CONDUCTOR_TEST_PROCESS_RESTART=1`. It owns a temporary PostgreSQL
container and volume and starts the compiled API as a separate process. Never
restart a database selected through `CONDUCTOR_TEST_DATABASE_URL` or delete an
existing development volume to prove recovery. Missing opt-in is an explicit skip;
missing dependencies after opt-in are a failure.

For durable context changes, opt into the owned Temporal acceptance with
`CONDUCTOR_TEST_TEMPORAL=1` and `CONDUCTOR_TEST_DATABASE_URL`, including
`internal/contextworkflow` and `tests/acceptance`. Use the verified CLI 1.8.3 binary
(`CONDUCTOR_TEMPORAL_CLI` can select it). Tests own persistent SQLite and temporary
processes/schemas; do not restart user-configured services. Distinguish actual
process recovery from SDK tests and controlled provider fixtures. An absent opt-in
is a skip and unavailable dependencies after opt-in are a failure.

For terminal workflow changes, run the real PTY acceptance in `tests/terminal/`
against an explicitly configured local API and compiled CLI. Also opt into the
signed-issuer authenticated PTY acceptance with `CONDUCTOR_TEST_TERMINAL=1` and
`CONDUCTOR_TEST_DATABASE_URL`. It owns an isolated schema and temporary API/CLI
resources; it must preserve existing development data. Missing opt-in is a skip;
missing dependencies after opt-in are a failure. See
`docs/operations/terminal-review.md` for controls, limits, and acceptance commands.

Before finishing:

1. Review `git diff` and preserve unrelated work.
2. Confirm generated files and lockfiles are intentional.
3. Ensure no secrets, PHI, company code, or production details were added.
4. Report implemented behavior, design decisions, exact checks and results,
   limitations, and the next concrete increment.
5. Do not merge, deploy, waive checks, force-push, or select a project license
   without explicit authority.
