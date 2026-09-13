# Coordinated execution

Status: **Implemented and verified runtime.** Shared plan admission, human execution
authorization, retained whole-source/profile pins, durable dispatch, isolated coding
workers, dependent cumulative artifacts and actual independent checks are tested.
Browser and other review controls, publication and full-release validation remain
separate stack work. No paid model-provider execution is claimed by fixture runs.

Conductor coordinates work across canonical repositories in one workspace. A plan
pins one current package revision and digest per repository, one retained source
receipt/commit per repository, and an inspected immutable graph covering exactly
those sources. It contains at most 32 tasks and permits at most four concurrent
tasks. Each task names an operator-configured execution profile, a human perspective,
instructions, repository write paths, dependencies and explicit verification argv.
Perspectives tailor work; they grant no authority.

Before authorization, each repository also pins its retained whole-source digest,
and each task pins the inspected operator profile digest and immutable image. Public
profile discovery is workspace-scoped and bounded; it contains no credential paths.
Updating the operator catalog cannot silently change an already inspected plan.

An author may propose a plan. Creating it records immutable intent and audit facts,
without scheduling a producer. A separately provisioned human execution grant on
every repository is required to authorize its exact digest. Authorization checks
current effective package approvals in the same database transaction; an edit
requires a new plan and inspection. Agents cannot authorize execution even if a
grant was configured incorrectly. Publication has its own separate human grant.

Tasks that may run in parallel cannot write overlapping paths in the same
repository. Dependency cycles, missing dependencies, traversal paths, Git metadata,
unbounded command inputs and ambiguous JSON are rejected. At admission, repository
locks serialize conflicting write claims across runs. A cancellation request is
durable intent and does not free a path while its producer may still be running.

All readers of a run must retain access to every included repository. Discovery
filters inaccessible runs before bounded keyset pagination. Its compact summaries
do not include prompts. Individual inspection returns the immutable plan and
separate authorization, cancellation, observed execution and task-receipt facts.
Old or unavailable observations cannot appear to be current activity.

Temporal is the only task sequencer. Its history contains opaque run/task IDs,
bounded dependency IDs and receipt digests. Activities load source, prompts,
credentials and commands outside history. Completed prerequisites gate descendants;
a failed prerequisite records descendants as blocked and unexecuted. Producer
results require separate verification against the exact source and patch trees.
An unresolved attempt cannot silently start another producer or release its claims.

Trusted activities may classify unavailable database/transport responses and bounded
rate limits as retryable. The workflow preserves only those explicit classifications
within its existing attempt/time limits. Unknown failures, changed bindings and
authority denials remain terminal; cancellation remains Temporal cancellation.
Retries retain the exact run/task reference and recover committed task or aggregate
receipts before any new work. Private error causes never enter workflow history.

Dependent tasks consume verified predecessor patches. Their output must retain
the cumulative change against the approved original repository baseline; a later
publisher must not omit predecessor work. Missing configured checks never count
as passed. Source receipt or graph evidence is not test execution evidence.

The [plan](plan.md), [architecture](../../docs/architecture/coordinated-execution.md)
and [runbook](../../docs/operations/coordinated-execution.md) track the working
commands and explicit acceptance limits.

## Shared client review

The browser and MCP share the authenticated plan API. MCP can discover execution
profiles, propose strict bounded plans and inspect retained plans/receipts; it has
no execution authorization or run-cancellation tool. Browser authors can import a
bounded version 1 JSON plan, reject duplicate/unknown fields and inspect a structured
preview before creating it. An uncertain proposal retains the exact input/key for
an explicit retry. Humans inspect the complete stored plan and exact digest before
execution authorization or cancellation. Neither action fetches replacement facts.
Conflicts and uncertain mutation outcomes require explicit renewed run inspection.
Scope changes and access denial clear private plans, imported drafts, capabilities
and confirmation state. Read-only users retain inspection controls. Missing/stale
execution observations and missing task receipts never render as verified outcomes.

Before the first external execution boundary, the trusted worker commits an
immutable attempt containing its complete input digest, profile/image pins and
bounded deadline. A redelivery cannot create a second producer for that task.
Permission or profile changes during execution cancel the producer and discard its
source-bearing output. Receipt and audit commit atomically under current authority.

A lost attempt is reconciled after its database-measured deadline by removing its
exact disposable containers/network. Recovery records uncertainty rather than
inventing a patch or successful check. Release requires observed terminal Temporal
execution, every task's non-unresolved receipt and confirmed cleanup of all admitted
attempts. These rules also apply after cancellation and process failure.

The original execution attempt must remain bounded by its admitted deadline, with
cleanup time reserved. Admission or authorization latency cannot grant another
execution window. Recheck cancellation after delayed I/O and before resource
creation so an activity resumed after recovery cannot start new Docker work.
