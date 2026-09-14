# Conductor documentation

Conductor is an architect-governed coordination layer for software design and
agentic programming. The documentation deliberately distinguishes the **current
implementation** from the **target architecture** so that planned integrations are
not mistaken for working capabilities.

## Start here

| Document | Purpose |
|---|---|
| [System architecture](architecture/system.md) | Implemented components, shared workflows, trust boundaries and tested limits |
| [Create and review a Change](operations/change-authoring.md) | Readable browser and terminal authoring, separate save/review actions, and preserved legacy content |
| [Native Design assistance](operations/native-design-assistance.md) | Configure a native assistant, request section suggestions and review/apply them in the shared workbench |
| [Assistance architecture](architecture/native-design-assistance.md) | Immutable suggestions, requester-only application and native credential boundaries |
| [Assistance specification](../specs/020-native-design-assistance/spec.md) | Exact section operations, idempotency and shared client behavior |
| [Native host qualification](research/native-assistant-connections.md) | Current primary sources, version evidence and provider limits |
| [Switch design language](design/brand.md) | Supplied logo, adopted web tokens and responsive ASCII terminal identity |
| [Guided authoring specification](../specs/019-guided-change-authoring/spec.md) | First usability increment: Change/Design labels, exact commands and bounded discovery summaries |
| [Release operations](operations/release.md) | Build reproducible archives, configure services, migrate, back up and restore shared data |
| [Full release contract](full-release.md) | Required complete-platform capabilities, evidence gates, and stacked PR order |
| [MCP setup](operations/mcp.md) | Connect agents to shared source, plans, artifacts, delivery, tracker and runtime evidence through authenticated stdio |
| [MCP specification](../specs/008-mcp/spec.md) | Fixed identity, strict tool inputs, bounded output, and shared authorization |
| [MCP integration research](architecture/mcp-integration-research.md) | Pinned SDK, protocol, Go toolchain, and tested limits |
| [Repository graph](architecture/repository-graph.md) | Immutable shared graphs, all-source permission checks, and explicit coverage |
| [Graph setup](operations/repository-graph.md) | Build the pinned CodeGraph image and query shared repository relationships |
| [CodeGraph research](architecture/codegraph-integration-research.md) | Selected upstream, tested Rust extraction, and version-specific adapter limits |
| [Verification criteria](operations/verification-criteria.md) | Exact approved criterion links, independent check evidence and explicit unverified coverage |
| [Coding workers](operations/coding-workers.md) | Isolated producers, cumulative patches, independent checks, and credential boundaries |
| [Coordinated execution](architecture/coordinated-execution.md) | Shared plans, exact human authorization, path claims, and Temporal sequencing |
| [Browser navigation](operations/browser-navigation.md) | Six focused workflows, keyboard/mobile controls and permission-free role guidance |
| [Execution workbench](operations/coordinated-workbench.md) | Inspect MCP proposals and confirm exact human execution/cancellation in the browser |
| [Complete release acceptance](operations/full-release-acceptance.md) | Actual shared source, graph, worker, publication, tracker and runtime path with owned process recovery |
| [Execution setup](operations/coordinated-execution.md) | Enable plan admission, provision execution grants, inspect and cancel shared work |
| [Repository delivery](operations/repository-delivery.md) | Propose, inspect and authorize exact patches; configure trusted GitHub/GitLab publication and observations |
| [Runtime evidence](operations/runtime-evidence.md) | Collect and review scoped deployment telemetry and exact approved criteria |
| [Native design tools](operations/design-tools.md) | Run pinned Spec Kit/ADRKit artifact and decision commands in isolated workers |
| [Design tool research](architecture/design-tool-research.md) | Verified upstream commands, exact versions and compatibility limits |
| [Assistant profiles](research/assistant-profiles.md) | Pinned Codex/Claude interfaces and actual versus unverified acceptance |
| [Usable assisted workflows](architecture/assisted-workflows.md) | Proposed web/TUI authoring, native/API assistant connections, implementation sequence and usability acceptance |
| [Product vocabulary](architecture/vocabulary.md) | Proposed Objective/Change/Plan/Step/Run terminology, confirmed name origin and Jira/Linear associations |
| [Milestone 1 architecture](architecture/milestone-1.md) | Implemented work-package model, lifecycle, API flow, persistence, and invariants |
| [AI-DLC inspiration and plan review](architecture/aidlc-plan-review.md) | Proposed workflow, evidence, role, recovery, and delivery refinements |
| [Shared engineering context](architecture/collaboration.md) | Shared data, authenticated workspace access, and authority boundaries |
| [Repository providers](architecture/repository-providers.md) | Repository identity, provider boundaries and GitHub/GitLab delivery |
| [Durable repository context](architecture/durable-context.md) | Shared background collection, source evidence, recovery and qualification limits |
| [Context integration research](architecture/context-integration-research.md) | Pinned Temporal/GitHub/GitLab profiles and verified versus live-service limits |
| [Feature 006 specification](../specs/006-durable-context/spec.md) | Remote collection contract with author and operator permission boundaries |
| [Feature 006 plan](../specs/006-durable-context/plan.md) | Provider research, working delivery increments, and restart acceptance |
| [Temporal TLS setup](operations/temporal-tls.md) | Shared verified TLS/mTLS, protected credentials and persistent runtime identity across workers |
| [Background context setup](operations/durable-context.md) | Enable bounded repository reads, run the worker, inspect and attach receipts, and recover safely |
| [Work tracking](architecture/work-tracking.md) | Linear/Jira options, linked work, and synchronization ownership |
| [Tracker setup and review](operations/work-tracking.md) | Configure one tracker per workspace, inspect linked work, synchronize and resolve conflicts |
| [Context and history walkthrough](operations/context-review.md) | Capture pinned artifacts, find shared work, inspect history, and check freshness |
| [Release terminal](operations/release-terminal.md) | CLI and interactive graph, agent work, artifact, delivery, tracker and runtime workflows |
| [Terminal review](operations/terminal-review.md) | Guided Change authoring, authenticated and local review, advanced imports, collection controls and recovery |
| [Feature 005 specification](../specs/005-authenticated-terminal/spec.md) | Fixed terminal identity and scope, capabilities, and explicit recovery |
| [Feature 005 plan](../specs/005-authenticated-terminal/plan.md) | Terminal implementation and signed-issuer real PTY validation |
| [Keycloak qualification](operations/keycloak-qualification.md) | Actual native HTTPS identity-provider acceptance and supported protocol limits |
| [Browser sign-in](operations/browser-sign-in.md) | Configure OIDC login, sessions, shared review, and collection controls |
| [Feature 004 specification](../specs/004-browser-sign-in/spec.md) | Browser identity, session, and exact review contracts |
| [Feature 004 plan](../specs/004-browser-sign-in/plan.md) | Browser implementation sequence and later workflow boundaries |
| [Authenticated review](operations/authenticated-review.md) | Configure identity, provision permissions, and review through the API, CLI, and terminal |
| [Feature 003 specification](../specs/003-workspace-access/spec.md) | Workspace isolation, canonical repositories, and human/agent access contracts |
| [Feature 003 plan](../specs/003-workspace-access/plan.md) | Authentication implementation scope and additional review interfaces |
| [Feature 002 specification](../specs/002-context-history/spec.md) | Shared discovery, historical review, and context evidence contracts |
| [Local development](operations/local-development.md) | Run, configure, exercise, test, and troubleshoot the current slice |
| [Feature 001 specification](../specs/001-work-package-review/spec.md) | Normative behavior and acceptance criteria |
| [Feature 001 plan](../specs/001-work-package-review/plan.md) | Original milestone sequence and implemented release follow-through |
| [ADR 0001](adr/0001-immutable-work-package-revisions.md) | Proposed immutable-revision decision |
| [ADR 0002](adr/0002-single-domain-command-path.md) | Proposed shared command-path decision |
| [ADR 0003](adr/0003-durable-context-workflow.md) | Proposed pinned context workflow before assistant execution |
| [OpenAPI contract](../api/openapi.yaml) | Current HTTP resources and payloads |
| [Agent and contributor guidance](../AGENTS.md) | Canonical repository-wide engineering instructions |

## Status legend

- **Implemented:** present in the repository and covered by available automated tests.
- **Partial:** a useful slice exists, but documented exit criteria are not yet met.
- **Planned:** target design only; clients must not assume it is available.
- **Proposed:** awaiting authorized architectural acceptance.

ADRs remain proposed until an authorized reviewer accepts them. Documentation
status is not architectural approval.
