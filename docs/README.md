# Conductor documentation

Conductor is an architect-governed coordination layer for software design and
agentic programming. The documentation deliberately distinguishes the **current
implementation** from the **target architecture** so that planned integrations are
not mistaken for working capabilities.

## Start here

| Document | Purpose |
|---|---|
| [System architecture](architecture/system.md) | Product boundaries, target components, trust boundaries, and delivery roadmap |
| [Milestone 1 architecture](architecture/milestone-1.md) | Implemented work-package model, lifecycle, API flow, persistence, and invariants |
| [AI-DLC inspiration and plan review](architecture/aidlc-plan-review.md) | Proposed workflow, evidence, role, recovery, and delivery refinements |
| [Shared engineering context](architecture/collaboration.md) | Shared data, authenticated workspace access, and authority boundaries |
| [Repository providers](architecture/repository-providers.md) | Implemented repository identity and planned GitHub/GitLab delivery |
| [Durable repository context](architecture/durable-context.md) | Proposed first background workflow, evidence, and recovery boundaries |
| [Context integration research](architecture/context-integration-research.md) | Researched Temporal/GitHub/GitLab candidates, without compatibility claims |
| [Feature 006 specification](../specs/006-durable-context/spec.md) | Proposed remote collection contract and unresolved permission choice |
| [Feature 006 plan](../specs/006-durable-context/plan.md) | Provider research, working delivery increments, and restart acceptance |
| [Work tracking](architecture/work-tracking.md) | Planned Linear/Jira options, linked work, and synchronization ownership |
| [Context and history walkthrough](operations/context-review.md) | Capture pinned artifacts, find shared work, inspect history, and check freshness |
| [Terminal review](operations/terminal-review.md) | Authenticated and local terminal review, JSON imports, exact approval, and access recovery |
| [Feature 005 specification](../specs/005-authenticated-terminal/spec.md) | Fixed terminal identity and scope, capabilities, and explicit recovery |
| [Feature 005 plan](../specs/005-authenticated-terminal/plan.md) | Terminal implementation and signed-issuer real PTY validation |
| [Browser sign-in](operations/browser-sign-in.md) | Configure OIDC login, sessions, and shared browser review |
| [Feature 004 specification](../specs/004-browser-sign-in/spec.md) | Browser identity, session, and exact review contracts |
| [Feature 004 plan](../specs/004-browser-sign-in/plan.md) | Browser implementation sequence and later workflow boundaries |
| [Authenticated review](operations/authenticated-review.md) | Configure identity, provision permissions, and review through the API, CLI, and terminal |
| [Feature 003 specification](../specs/003-workspace-access/spec.md) | Workspace isolation, canonical repositories, and human/agent access contracts |
| [Feature 003 plan](../specs/003-workspace-access/plan.md) | Authentication implementation scope and additional review interfaces |
| [Feature 002 specification](../specs/002-context-history/spec.md) | Shared discovery, historical review, and context evidence contracts |
| [Local development](operations/local-development.md) | Run, configure, exercise, test, and troubleshoot the current slice |
| [Feature 001 specification](../specs/001-work-package-review/spec.md) | Normative behavior and acceptance criteria |
| [Feature 001 plan](../specs/001-work-package-review/plan.md) | Refined implementation sequence and next increment |
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
