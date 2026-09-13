# Repository graph delivery plan

1. Persist immutable graph/source/audit facts in migration 005; derive actor and
   authorize every source repository under the existing transaction boundary.
2. Implement deterministic receipt-backed Go/manifest analysis with explicit gaps,
   bounded cross-repository resolution, search and traversal.
3. Pin the user-selected CodeGraph 1.6.0 source/release; run its actual Rust kernel
   and library in a restricted container. Add an optional existing Temporal activity
   hook and atomically persist source plus index without changing receipt digests.
4. Expose the implemented HTTP and Go client contracts and cover signed identity,
   shared persistence, revocation, idempotency, rollback and native execution.
5. Expand trusted source acquisition to whole repository trees at exact commits,
   while retaining explicit file/output bounds and incomplete-coverage evidence.
   Add browser/terminal graph controls and coordinated-agent graph consumption.

The user's current instruction explicitly requests stacked PRs. Publish focused
commits with an explicit numbered base/dependency chain; never merge them or mark
external deployment/compatibility as verified without corresponding evidence.

The backend items, including bounded whole-repository source acquisition and
CodeGraph indexing, have implementation and targeted acceptance. Browser graph
controls now have actual signed-login/PostgreSQL/Chromium acceptance for multi-repository
inspection, retained-key retry, queries, scope/denial clearing and read-only access.
Terminal controls and coordinated-agent consumption remain required for the broader release. [Operations](../../docs/operations/repository-graph.md)
records reproducible native runtime setup and remaining limits.

## Retained source inspection

Implemented after the initial graph/source work: exact-tuple graph artifact reads
through the authenticated API and shared Go client, with all-source transaction
authorization and explicit coverage gaps. Signed PostgreSQL acceptance covers
cross-repository reads, revocation, malformed tuples and paths, and bounded text
from selected receipts and full Git source bundles. Browser/MCP controls remain
a separate parent release-stack increment.

Browser and MCP artifact controls: signed PostgreSQL/Chromium/stdio tests pass exact
related-source pins, fixed anchor, altered text rejection and revocation clearing.
