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
| [Shared engineering context](architecture/collaboration.md) | Shared data across developers/agents and planned authorization boundaries |
| [Repository providers](architecture/repository-providers.md) | Planned GitHub and GitLab support for managed repositories |
| [Context and history walkthrough](operations/context-review.md) | Capture pinned artifacts, find shared work, inspect history, and check freshness |
| [Feature 002 specification](../specs/002-context-history/spec.md) | Shared discovery, historical review, and context evidence contracts |
| [Local development](operations/local-development.md) | Run, configure, exercise, test, and troubleshoot the current slice |
| [Feature 001 specification](../specs/001-work-package-review/spec.md) | Normative behavior and acceptance criteria |
| [Feature 001 plan](../specs/001-work-package-review/plan.md) | Refined implementation sequence and next increment |
| [ADR 0001](adr/0001-immutable-work-package-revisions.md) | Proposed immutable-revision decision |
| [ADR 0002](adr/0002-single-domain-command-path.md) | Proposed shared command-path decision |
| [OpenAPI contract](../api/openapi.yaml) | Current HTTP resources and payloads |
| [Agent and contributor guidance](../AGENTS.md) | Canonical repository-wide engineering instructions |

## Status legend

- **Implemented:** present in the repository and covered by available automated tests.
- **Partial:** a useful slice exists, but documented exit criteria are not yet met.
- **Planned:** target design only; clients must not assume it is available.
- **Proposed:** awaiting authorized architectural acceptance.

ADRs remain proposed until an authorized reviewer accepts them. Documentation
status is not architectural approval.
