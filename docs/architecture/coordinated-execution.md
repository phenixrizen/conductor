# Shared coordinated execution

**Current:** immutable plans and authorization admission; **partial:** execution
runtime integration and complete client controls.

PostgreSQL owns plans, human authorizations, write claims, immutable task receipts
and audit facts. Temporal owns task sequencing, activity delivery and cancellation.
The API writes an outbox intent atomically with authorization. A trusted dispatcher
will bind that intent to a checked Temporal cluster/namespace and unique workflow.
A database observation is timestamped evidence of that workflow, never its scheduler.

Plan admission holds principal and workspace membership locks, repository grants,
separate human execution grants, and package revision advisory locks until commit.
Sorted repository admission locks prevent two plans from claiming the same path.
No approval action acquires new source or substitutes newer content.

The isolated producer consumes a retained complete Git bundle and verified
predecessor patches. Its write allowance applies to the change from that prepared
baseline; the retained cumulative patch applies to the original approved commit.
Verification runs in a separate container. Publication uses a separate trusted
service and credential catalog after an exact-artifact human authorization.

Every operation rechecks all repositories in a cross-repository run. Read summaries
are compact and paginated; detailed plans retain prompts and exact source bindings.
Perspective labels are descriptive and never affect these permission checks.

See the [specification](../../specs/009-coordinated-execution/spec.md),
[runbook](../operations/coordinated-execution.md), and
[worker boundary](../operations/coding-workers.md).
