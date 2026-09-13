# Coordinated execution delivery

1. Implement immutable plans, exact package/graph/source pins, separate operator
   execution grants, all-source authorization, path claims and audit rollback.
   **Verified:** live PostgreSQL race tests cover collaboration, agent denial,
   stale approval, revocation, competing writers and transaction-held grants.
2. Expose strict authenticated API and shared Go client commands with digest-bound
   authorization and cancellation. **Verified:** signed MCP-to-browser/PostgreSQL
   acceptance covers shared proposals, exact human decisions and uncertain-outcome
   recovery. Terminal controls remain in progress.
3. Sequence the bounded task DAG in Temporal with only opaque history payloads.
   **Verified in SDK tests:** parallel bound, prerequisite order, blocked descendants,
   invalid graph rejection and private-error/result sanitization. Actual retained history also passes an owned Temporal process restart with
   real Docker coding tasks.
4. Connect trusted activities, canonical full Git bundles, isolated producers,
   cumulative predecessor patches and separate verification. Persist attempt and
   artifact facts; reconcile ambiguous dispatch and producer outcomes before retry.
   **Verified:** signed API, live PostgreSQL, native CodeGraph and actual Docker
   DAG acceptance retain both predecessor repositories and exact check receipts.
5. Exercise actual Docker/Temporal/PostgreSQL lifecycle, process failure, grant
   revocation, cancellation and all-client collaboration. Update the full release
   evidence table with exact results and limitations. **Runtime verified:** orphan
   cleanup without producer replacement, related-repository revocation during
   production, completed Temporal history after an actual process restart, and
   conservative claim retention. A compiled executor also passes an in-flight
   process kill/restart without repeating the lost producer. Browser/terminal/MCP
   review remains separate.

This work follows the user's stacked PR instruction. Each PR targets its immediate
prerequisite and states the merge order; none is merged or deployed by an agent.
