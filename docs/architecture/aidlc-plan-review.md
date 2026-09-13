# AI-DLC inspiration and Conductor plan review

**Status: Proposed.** This review proposes product and delivery refinements; it
does not accept an ADR or implement an integration. The analysis below describes
the 2026-09-12 plan. Later implementation is recorded in the
[feature follow-through](../../specs/001-work-package-review/plan.md) and
[browser perspective guide](../operations/browser-navigation.md); it does not
retroactively accept this proposed review.

Reviewed the official [AWS Labs AI-DLC repository at commit
`9b8847b5ba34840407d3f551b4e9dd1c7da0982d`](https://github.com/awslabs/aidlc-workflows/tree/9b8847b5ba34840407d3f551b4e9dd1c7da0982d)
on 2026-09-12. Upstream observations below describe inspected documentation and
source definitions, not an independently executed compatibility test. A future
adapter still requires a pinned, tested version and capability assessment.

## What we are building

Conductor should carry engineering intent through accountable design,
implementation, verification, and delivery. Its distinguishing responsibility is
maintaining who authorized which exact work, what evidence supports it, what
changed, and what can happen next across tools and sessions.

The [plan reviewed then](../../specs/001-work-package-review/plan.md) starts in the right
place: immutable package revisions, independent approval, transactional history,
and one command boundary. Keep that foundation. Make the next steps more explicit:
the existing “Next increment” combines identity, context, and orchestration before
spelling out how the remaining Milestone 1 exit criteria close. Split those into
separate acceptance increments.

The proposed division of responsibilities preserves the
system responsibilities below (the [system guide](system.md) now records implementation):

| Component | Proposed responsibility |
|---|---|
| Spec Kit | Specification, planning, and task artifacts referenced by the package |
| ADRKit | Decision artifacts and their recorded status, referenced with provenance |
| Conductor | Identity, authorization, immutable package snapshots, gates, evidence, recovery, and integration coordination |
| AI-DLC inspiration | Techniques for selecting work, structuring stages, collaborating, and checking outputs |

The [native design-tool profile](design-tool-research.md) now implements bounded
core artifact and deterministic CLI commands within these boundaries. Broader
agentic prompt workflows are separate from passing native prerequisites or schema
checks. Retain native artifact identities and exact source revisions.

## Adopt in the next design increment

**Distinguish people, agent specializations, and authority.** AI-DLC defines both
agent personas and human user-persona artifacts. Its User Stories stage captures
users' roles, goals, pain points, and context; its team-formation stage identifies
decision-makers and a RACI matrix. Those are useful inputs to Conductor's product
model. [User Stories source][stories] [Team Formation source][team]

Propose these initial human workbench perspectives:

| Perspective | Primary question and evidence |
|---|---|
| Architect | Does the design respect boundaries, contracts, constraints, and ADRs? |
| QC / QA | Do acceptance criteria and measurable quality targets have sufficient executed evidence? |
| Developer | What exact scope, dependencies, constraints, and verification commands govern the work? |
| Product / domain owner | Does this satisfy the intended outcome and business acceptance criteria? |
| Operations / security | Are operational and security constraints addressed with current evidence? |

Allow multiple perspectives per person and use them to organize queues, context,
and explanations. A chosen persona must never grant approval rights. Server-owned
project/repository assignments determine capabilities. An AI architect or quality
agent may produce findings; it cannot become the authorized human reviewer.

**Make the workflow explicit and proportionate.** AI-DLC has declared stage inputs,
outputs, dependencies, lead/support agents, checks, and review requirements, plus
workflow profiles that choose a route and depth. Its adaptive composer proposes
a route for human approval. [Stage contract][stage] [Workflow profiles][profiles]

Conductor should first support one small route: contextualize the package, inspect
the design, authorize bounded work, produce a patch, verify it, then request
publication. Record a versioned plan with required evidence and reasons for omitted
optional steps. Add bugfix and broader feature profiles only after the first route
works. Risk classification and required gates remain controlled policy; a model's
recommended shorter plan cannot silently remove them.
Keep planned task identifiers stable; execution attempts, progress, and results
belong in run records, not edits to an approved plan.

**Connect requirements to evidence and separate reviews from approvals.** AI-DLC
build-and-test maps targets to results and records unmet or unverified targets as
failure. Its independent reviewers inspect bounded artifacts without editing them,
with source-bound receipts and bounded revision loops. [Build-and-test source][tests]
[Reviewer behavior][agents]

Add stable acceptance-criterion identifiers and links from each criterion to its
design decision, implementation, and verification result. An evidence record should
identify the package revision/digest, source revision, command or producer version,
environment, result, and retrievable output. Keep missing, stale, unavailable,
truncated, and unexecuted evidence visible. Distinguish an AI review verdict from a
human authorization and from executed verification. Preserve unresolved findings
at the gate; changing source must invalidate affected review evidence.
Specify whether each future approval covers package design, a produced patch, or
release readiness; bind each to its own artifact set. Milestone 1 package approval
must not become proof of successful execution.

**Gather brownfield context with explicit coverage.** AI-DLC's reverse-engineering
stage separates code scanning from architectural synthesis, records analyzed scope,
checks freshness, and binds publication to source and knowledge-store fingerprints.
[Reverse-engineering source][brownfield]

Start Conductor's context adapter with one repository at an immutable commit:
relevant files, build instructions, existing specifications/ADRs, interfaces,
constraints, and unresolved questions. Record what was inspected and omitted.
Derived summaries must reference source facts and report staleness; neither a
successful fetch nor a generated summary proves architectural understanding.

## Carry through orchestration and execution

**Treat recovery as product behavior.** AI-DLC persists stage progress, artifacts,
and audit records and reloads them on resume; it does not preserve unwritten
conversation state. [Session management][sessions]

Conductor should expose the pending action, exact authorized revision, last durable
checkpoint, blocked dependency, and current evidence. Reconnect or worker restart
must reconstruct that view without asking users to repeat established decisions.
PostgreSQL remains the authority for package and authorization facts; Temporal
owns execution sequencing when introduced. Use inbox/outbox reconciliation and
idempotent activities, with explicit handling of ambiguous publication outcomes.
Do not import AI-DLC's file-based state machine as another authority.

**Validate the environment before consequential work.** AI-DLC distinguishes
runtime, provider, and trust diagnostics, including actions that cannot be verified
offline. [Architecture diagnostics][architecture]

Add bounded preflight checks for required tools, database access, repository access,
worker capabilities, and publication connectivity. Record what actually succeeded;
installed binaries or a login command are not proof of working access. Produce
actionable recovery instructions and preserve the package while access is repaired.

## Adopt later, after one complete route

- **Curated knowledge and learning:** AI-DLC separates shared methodology from team
  knowledge and persists selected human-confirmed learnings. Its prose conflict
  check is explicitly advisory. Conductor can propose scoped practices with
  provenance, owner, revision, and effective date, applying changes to future plans.
  Accepted ADRs and organizational policy must not be rewritten by a learning.
  [Knowledge][knowledge] [Learning-loop limitations][learning]
- **Portable adapters and controlled extensions:** borrow the separation between a
  common core and harness-specific projections, and additive extension contracts.
  Keep Conductor's domain commands stable while adapters declare tested capabilities
  and limitations. Begin with one coding provider; retain patch-only workers and a
  separately credentialed publisher. AI-DLC's shipped personas inherit broad session
  tools in some harnesses, so persona labels are not an isolation mechanism.
  [Architecture][architecture] [Extensions][plugins] [Agent tool access][agents]
- **Selective parallelism and operational feedback:** use independent architecture
  and quality reviews when their distinct evidence adds value. Add dependency-aware
  execution only after recovery is proven. Later connect observed production outcomes
  to new work packages, preserving the distinction between deployment and verified
  outcome. AI-DLC models this operations-to-product loop. [Agent collaboration][agents]
- **Layered verification:** combine deterministic policy/contract tests with real
  adapter and workflow acceptance tests. AI-DLC separates protocol checks from live
  model stage and full-workflow checks. Conductor must label unavailable integration
  tests explicitly; structural artifacts alone do not establish correct behavior.
  [Testing strategy][strategy]

## Sequence and boundaries

1. Close Milestone 1 with real PostgreSQL durability/concurrency evidence, stale
   approval and client conflict paths, and the documented terminal-interface exit.
   Any change to that exit must be an explicit scope decision.
2. Before shared deployment, deliver authenticated, repository-aware review with a
   small human-role model, inspectable history and revision diffs, and revision-pinned
   context/evidence. Keep execution disabled.
3. Add the durable execution boundary and prove restart, retry, cancellation, stale
   evidence, and ambiguous-result recovery with one bounded workflow.
4. Add one assistant producing a patch and a trusted service producing a draft
   GitHub pull request or GitLab merge request, according to the managed
   repository's provider. Verify both adapters before declaring dual-provider
   delivery complete. Treat merge and deployment as separate authorizations.

Do not copy the full stage catalog, optional-agent roster, plugin marketplace, or
adaptive composer into Milestone 1. Do not copy profile controls that reduce review
intensity into a mechanism for waiving Conductor policy. Borrow observable workflow
contracts and evidence practices while retaining Conductor's authority model.

[stories]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/core/aidlc-common/stages/inception/user-stories.md
[team]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/core/aidlc-common/stages/ideation/team-formation.md
[stage]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/core/aidlc-common/protocols/stage-definition.md
[profiles]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/docs/guide/workflow-profiles.md
[tests]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/core/aidlc-common/stages/construction/build-and-test.md
[agents]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/docs/guide/06-agents.md
[brownfield]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/core/aidlc-common/stages/inception/reverse-engineering.md
[sessions]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/docs/guide/11-session-management.md
[architecture]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/docs/reference/01-architecture.md
[knowledge]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/docs/guide/08-knowledge.md
[learning]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/docs/guide/09-rules-and-the-learning-loop.md
[plugins]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/docs/reference/18-plugin-mechanism.md
[strategy]: https://github.com/awslabs/aidlc-workflows/blob/9b8847b5ba34840407d3f551b4e9dd1c7da0982d/docs/reference/09-testing.md
