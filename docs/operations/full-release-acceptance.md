# Complete cross-repository release acceptance

**Implemented acceptance boundary.** This gate follows shared work from two
repositories through source collection, a cross-repository graph, human approval,
coordinated production, artifact inspection, trusted draft publication, ticket
synchronization and correlated runtime evidence. All external provider responses
and producer programs are synthetic and identified as fixtures. The real Git,
Docker, PostgreSQL, MCP and Temporal processes execute the complete path.

The test creates its own PostgreSQL schemas, temporary Git repositories, HTTP
servers, worker containers, MCP/executor processes and persistent Temporal SQLite
files. It stops and restarts its owned Temporal process. It never restarts a
user-configured database, publishes to a real repository, merges a review,
deploys an application, or invokes a paid model.

Run from the repository with Docker socket access, Go, Git, and the verified
Temporal CLI 1.8.3/server 1.31.2 available. Build the images through the
[coding-worker](coding-workers.md) and [CodeGraph](repository-graph.md) instructions.
Select the test database explicitly:

```bash
export CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:55432/conductor?sslmode=disable'
export CONDUCTOR_TEST_WORKER_IMAGE='sha256:a60993075fc4f7a78139916ca362a9123fdc7adaac992abc1122a3fb5d2be3da'
export CONDUCTOR_CODEGRAPH_IMAGE='sha256:053d561c8b85daea3519a06fe6482299dee8792ffce01c59afa915f79a88168c'
export CONDUCTOR_TEMPORAL_CLI='/home/nater/.local/bin/temporal'
./scripts/test-full-release.sh
```

These are the locally tested immutable images, not registry distribution tags.
Use your verified image digests after rebuilding. The Temporal CLI path is an
example; omit it when the verified binary is on `PATH`. On a Snap installation
where the current shell has not acquired its Docker group, run the script with
`sg docker -c './scripts/test-full-release.sh'` after the documented operator
setup. The test never changes group membership or daemon settings itself.

Without `CONDUCTOR_TEST_RELEASE=1`, the full Go suite explicitly skips this gate.
After opt-in, missing test dependencies fail. The wrapper supplies the required
execution, CodeGraph and Temporal opt-ins and requires the database/image inputs.

| Stage | Actual evidence checked |
|---|---|
| Shared authoring | Compiled stdio MCP authenticates with a signed agent identity, authors package content and the task plan, and cannot authorize it. A separate human inspects and approves the exact package revisions and authorizes the exact plan. |
| Source and graph | Real Git smart HTTP transfers from controlled GitHub and GitLab protocol fixtures retain complete original commits/trees. Native CodeGraph extracts symbols. Both repository sources and a declared Go module dependency appear in the immutable graph. MCP reads related source using the exact source tuple. |
| Coordination | The compiled executor runs two independent Docker producers and a dependent QC task. The final task reads both predecessors and retains their cumulative patches. Four checks execute in separate verification sandboxes. Producer/verification credentials stay absent; exact artifact and cleanup evidence are retained. |
| Recovery | The executor observes terminal Temporal history before releasing claims. Restarting the owned Temporal process with its SQLite state retains the same aggregate receipt and three original attempts. Separate crash acceptance covers an executor killed during production, unresolved receipts and no second producer. |
| Publication | The real trusted publisher reconstructs patches from retained original bundles. Both provider fixtures create actual Git blobs, trees, commits and new branches, then drop the draft review response. Retry reads reconcile the same branch and draft; no additional POST or second review occurs. Result trees equal the independently verified cumulative patch trees. |
| Tracker | Separate runs select Linear or Jira, one per workspace. One ticket links both exact immutable publication receipt digests and package revisions. A dropped card-write response is reconciled with one write. A tracker status named Done never changes approval, unknown provider checks, draft status or production evidence. |
| Runtime | An explicitly synthetic provider deployment is observed through the real publication adapter. Groundcover metric/log/trace HTTP shapes correlate the exact deployed commit, service, environment and time window. Complete samples meet a criterion in the exact approved package; foreign-commit samples remain not verified and do not expose foreign data. Earlier receipts stay immutable. |
| Authority and privacy | Revoking the related repository blocks aggregate graph, artifact, tracker and runtime reads. Every retained Temporal history is inspected, including decoded payload data, for source text, commands and synthetic credentials. |

Publication and runtime constructors accept a deliberately explicit literal
loopback HTTP fixture origin. Their executable configurations expose no origin
override: production continues to use the pinned official provider profiles,
credential bindings, redirects disabled and explicit uncertain-result handling.
The fixture factory cannot grant authorization; the same production activity
checks still run before every provider call and at receipt commit.

This test proves the implemented protocol and isolation boundaries. It does not
establish live SaaS write compatibility, live identity-provider compatibility,
paid Codex/Claude inference, deployed application health, or satisfaction of all
production requirements. A runtime criterion marked met describes those selected
samples; the overall production outcome remains `not_verified`.
