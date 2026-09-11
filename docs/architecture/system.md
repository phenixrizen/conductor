# Conductor system architecture

## Purpose and boundaries

Conductor turns engineering intent into revision-pinned work packages and, over
later milestones, coordinates context gathering, implementation, verification,
and delivery. It governs coding assistants rather than replacing them.

GitHub hosts Conductor's own source and project development. GitLab is a future
delivery integration for application repositories managed by Conductor. These are
separate responsibilities: Conductor must not treat its GitHub project state as an
application delivery fact.

## Target system context

> **Status:** The work-package control-plane slice is partial. Components with
> dashed borders are planned and are not available in the current release.

```mermaid
flowchart LR
    Architect[Architect / reviewer]
    Developer[Developer]
    DomainOwner[Domain owner]

    subgraph Conductor[Conductor control plane]
        Interfaces[Web / CLI / TUI]
        API[Authenticated API and MCP]
        Policy[Domain policy and context]
        Engine[Durable orchestration]
        Gate[Authorization and verification gate]
    end

    subgraph Evidence[Context and evidence providers]
        SpecKit[Spec Kit]
        ADRKit[ADRKit]
        CodeGraph[CodeGraph]
        Groundcover[Groundcover]
    end

    subgraph Execution[Bounded execution]
        Claude[Claude Code adapter]
        Codex[Codex adapter]
        Workers[Isolated workers]
    end

    GitLab[GitLab application delivery]
    Linear[Linear work tracking]
    GitHub[GitHub: Conductor source]

    Architect --> Interfaces
    Developer --> Interfaces
    DomainOwner --> Interfaces
    Interfaces --> API
    API --> Policy
    Policy --> Engine
    Engine --> Gate
    Policy -. planned context .-> Evidence
    Engine -. planned execution .-> Execution
    Gate -. planned publication .-> GitLab
    API -. planned projection .-> Linear
    GitHub -->|hosts this project| Conductor

    classDef planned stroke-dasharray: 6 4,fill:#f7f7f7,color:#555;
    class Engine,Gate,SpecKit,ADRKit,CodeGraph,Groundcover,Claude,Codex,Workers,GitLab,Linear planned;
```

## Architectural layers

```mermaid
flowchart TB
    UI[React workbench / Go CLI / future Bubble Tea TUI]
    Client[Shared API clients]
    HTTP[HTTP command boundary]
    Domain[Domain service and approval policy]
    Store[(PostgreSQL revisions, approvals, audit)]
    Temporal[Temporal workflows]
    Artifacts[(S3-compatible artifact storage)]
    Integrations[GitLab / Linear / context / assistants]

    UI --> Client --> HTTP --> Domain --> Store
    Domain -. Milestone 2+ .-> Temporal
    Domain -. Milestone 2+ .-> Artifacts
    Temporal -. Milestone 2+ .-> Integrations

    classDef planned stroke-dasharray: 6 4,fill:#f7f7f7,color:#555;
    class Temporal,Artifacts,Integrations planned;
```

All interfaces must invoke the same domain command path. PostgreSQL owns review
state. Temporal will eventually own execution sequencing; a database projection of
run progress must not become a second workflow authority.

## Trust boundaries

```mermaid
flowchart LR
    User[Authenticated person]
    LocalHeader[Local identity header]
    API[Conductor API]
    DB[(PostgreSQL)]
    Untrusted[Repository text, tickets, logs, tool output]
    Worker[Future isolated worker]
    Publisher[Future publication service]
    GitLab[GitLab]

    User -->|production identity: planned| API
    LocalHeader -->|development only| API
    API --> DB
    Untrusted -->|evidence, never authority| API
    API -. approved package .-> Worker
    Worker -. patch only .-> Publisher
    Publisher -. revalidated action .-> GitLab

    subgraph TrustedControl[Trusted control plane]
        API
        DB
        Publisher
    end
```

The current `X-Conductor-Actor` header is intentionally local-only. Shared
deployment is blocked until organizational identity and repository-aware
authorization exist. Future workers do not receive publication credentials.

## State ownership

| State | Authority | Current status |
|---|---|---|
| Package revisions, submissions, approvals, audit events | PostgreSQL | Partial implementation |
| Workflow sequencing, waits, retries, cancellation | Temporal | Planned |
| Immutable large artifacts | S3-compatible storage | Planned |
| Application merge, pipeline, deployment facts | GitLab | Planned |
| Priority and assignment where configured | Linear | Planned |
| Cross-repository graph | Derived Conductor read model | Planned |
| Conductor source and project history | GitHub | Implemented externally |

## Delivery sequence

```mermaid
flowchart LR
    M1[1. Durable package review] --> M2[2. Orchestration and context]
    M2 --> M3[3. One assistant to draft MR]
    M3 --> M4[4. Assistant choice and Linear]
    M4 --> M5[5. Cross-repository runtime intelligence]
    M5 --> M6[6. Security, performance, bounded autonomy]

    classDef active fill:#e8f1ec,stroke:#244c3f,stroke-width:2px;
    classDef planned fill:#f7f7f7,stroke:#777,stroke-dasharray:6 4;
    class M1 active;
    class M2,M3,M4,M5,M6 planned;
```

Agent execution remains disabled until the review foundation, production identity,
authorization, durable recovery, and context evidence meet their exit criteria.
