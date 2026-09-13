# Coordinated execution delivery

1. Implement immutable plans, exact package/graph/source pins, separate operator
   execution grants, all-source authorization, path claims and audit rollback.
   **Verified:** live PostgreSQL race tests cover collaboration, agent denial,
   stale approval, revocation, competing writers and transaction-held grants.
2. Expose strict authenticated API and shared Go client commands with digest-bound
   authorization and cancellation. **Implemented;** signed end-to-end acceptance
   and complete browser/terminal/MCP controls remain in progress.
3. Sequence the bounded task DAG in Temporal with only opaque history payloads.
   **Verified in SDK tests:** parallel bound, prerequisite order, blocked descendants,
   invalid graph rejection and private-error/result sanitization. Actual process
   recovery with coding workers remains unverified until the next step completes.
4. Connect trusted activities, canonical full Git bundles, isolated producers,
   cumulative predecessor patches and separate verification. Persist attempt and
   artifact facts; reconcile ambiguous dispatch and producer outcomes before retry.
5. Exercise actual Docker/Temporal/PostgreSQL lifecycle, process failure, grant
   revocation, cancellation and all-client collaboration. Update the full release
   evidence table with exact results and limitations.

This work follows the user's stacked PR instruction. Each PR targets its immediate
prerequisite and states the merge order; none is merged or deployed by an agent.
