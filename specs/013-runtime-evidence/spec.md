# Feature 013: scoped runtime evidence

**Status: Implementation verified within the bounds recorded in [the plan](plan.md).**

Conductor collects read-only Groundcover metrics, logs and traces for an inspected
repository delivery and provider deployment. Engineers and agents share these
retained observations through the authenticated API, MCP and browser workbench. Evidence does not
create deployments, alter publication receipts, or establish broad production success.

## Request and authority

An author captures the delivery ID/digest, provider observation sequence,
deployment ID, exact full commit, environment and a past UTC time window. The
request carries an idempotency key. Window length is at most one hour, its start
is within seven days, and timestamps have whole-second precision. The selected
repository must own the delivery. A retained provider observation must identify
the exact requested deployment, commit and environment.

Admission holds active identity, membership, selected author permission and read
permission on every source repository in the delivery's coordinated plan in the
same transaction as the request, audit event and outbox. Agents may request reads.
No separate publication or deployment authority is granted. Operator configuration
binds a repository/environment to a Groundcover backend, service, correlation field
names and metric allowlist. It supplies no public arbitrary-query capability.

The trusted worker repeats this authority check before every provider call and at
receipt commit. Changing the integration, removing a source grant, or editing an
approved referenced criterion stops new work. A committed receipt wins an activity
retry after later revocation; revocation still denies public inspection.

## Collection and provenance

The pinned `groundcover-rest/2026-09-13-sdk1.424.0` profile uses only
`https://api.groundcover.com`, the documented metric range endpoint, and official
SDK-confirmed v2 log/trace searches. It uses a separate operator API key and backend
header. It never follows redirects, uses environment proxies, or calls ingestion,
management or deployment endpoints. All provider calls run in Temporal activities.

Every metric selector and gcQL query includes exact service, environment and full
commit filters. The adapter independently checks returned labels/attributes and
timestamps. Incorrect or missing correlation retains an explicit `uncorrelated`
state and discards the mismatched source rows. Provider-supplied attributes are
telemetry attribution, not cryptographic proof of deployed code.

Each signal retains its endpoint, query digest, bounded response digest, collection
time, explicit coverage and typed metric grid or bounded raw record JSON. Empty
queries remain `missing`; failures remain `unavailable`; bounds remain `truncated`.
No empty check or absent series is a pass. Logs and traces are source context;
they do not prove absence of failures or numeric criteria.

## Approved requirement links

Requests may link up to sixteen criteria from exact approved package revisions in
the delivery's original coordinated plan. The package's optional `runtimeCriteria`
object uses schema version 1. Each criterion explicitly supplies:

- `id`, allowlisted `metric`, and `aggregation`: `maximum`, `minimum` or `mean`.
- `operator`: `lte` or `gte`, and a finite numeric `threshold`, including explicit zero.
- `expectedSeries`, `windowSeconds`, `stepSeconds` and `maxAgeSeconds`.

Missing or unknown policy fields are rejected. The request cannot supply new
thresholds. Every link retains the package revision/digest, criterion ID and exact
criterion digest. Evaluation requires a successful provider deployment preceding
the requested window, exact commit/environment correlation, the approved window
and step, exactly the approved series count and every expected query-grid point.
A stale or incomplete window yields `not_verified`; a complete threshold comparison
yields `met` or `not_met`. MetricsQL sample evaluation timestamps do not prove the
age of the underlying application observations.

These conclusions apply only to the linked criteria and inspected window. The
receipt's broader `productionOutcome` remains `not_verified`; the historical
delivery's `productionOutcome` remains `not_observed`. No selected subset of
criteria silently proves all requirements. A later stale read displays `freshness:
stale` separately from the immutable historical evaluation.

## Durability and inspection

PostgreSQL owns immutable request, criterion linkage, receipts and audit facts.
Temporal owns execution sequencing. Dispatch binds the actual cluster, namespace,
workflow type and opaque input before RPC. Leases are fenced, acknowledgments are
reconciled, and known missing execution history is unresolved without replacement.
A stored receipt prevents duplicate source collection but is not itself a completed
Temporal observation. Source text and credentials never enter workflow history,
heartbeat details, errors or logs.

Implemented endpoints are POST/GET `/api/v1/runtime-evidence` and GET
`/api/v1/runtime-evidence/{id}`. POST returns 202 for the retained request. Listing
filters access before bounded keyset pagination. Reads expose the immutable receipt,
separate observed Temporal progress, and current freshness. No client supplies
identity, scope, backend, credentials, provider URL, query or evaluation conclusion.
See the [OpenAPI contract](../../api/openapi.yaml).

## Browser inspection

The browser lists 20 shared requests per page and inspects an explicitly selected
request. Authors import or paste bounded strict JSON, preview the exact deployment,
commit, UTC window and approved criterion references, then record the request.
Lost acknowledgments retain the same input and idempotency key for explicit retry;
no source, deployment, time window or policy is refreshed inside that decision.

The workbench exposes complete bounded metric points and escaped logs/traces,
query and response digests, source coverage and correlation, linked approved
policy, historical evaluations and separate execution observations. A local display
clock ages the retained window without polling or changing its historical result.
Access denial and identity/scope changes discard private source and pending inputs.

## Exact retained receipt representation

The receipt digest binds the stored typed receipt, including the exact compact JSON
representation of bounded log and trace records. PostgreSQL must preserve object
key order and numeric spelling inside those records. Reads and workflow receipt
reconciliation verify this identity and the historical evaluation before returning
usable evidence. Validate current source authorization before exposing a historical
integrity failure. Existing unverifiable receipts remain retained with their original
digests; migration and retries cannot reconstruct or silently replace lost source.
