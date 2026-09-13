# Shared repository graphs

Conductor can build an immutable map from inspected context receipts in up to 16
repositories. Every reader must have access to every repository in the map. The
selected repository must be one of them. Maps contain source references and
relationships; they are not design approval or passing verification.

Apply ordered migrations through 005 before using graph endpoints. Native
Go/go.mod/package.json analysis needs no external parser. To enable the selected
CodeGraph Rust parser for future context collections on Linux x86_64:

```bash
# Requires Docker, curl, tar, a C compiler and static libc development files.
# With Docker Snap, run this inside `sg docker` until group membership is active.
./scripts/build-codegraph-image.sh
# Use the exact sha256 image ID printed above, never a mutable tag.
export CONDUCTOR_CODEGRAPH_IMAGE=sha256:REPLACE_WITH_PRINTED_IMAGE_ID
export CONDUCTOR_DOCKER_BINARY="$(command -v docker)"
# Start conductord and conductor-worker using the existing durable-context guide.
```

The worker continues to require the explicit local Temporal profile and existing
provider configuration. CodeGraph is optional; an enabled adapter failure prevents
that collection from publishing a misleading successful receipt. Existing receipts
remain unchanged and do not gain an index through a retry. Request a new collection
explicitly when indexed source is needed.

The adapter uses no network, host mounts or credentials. The image is immutable,
the filesystem read-only, the process nonroot, capabilities dropped and writable
scratch volumes noexec/nosuid. Limits are 1 CPU, 1 GiB memory, 96 processes, 75 seconds,
512 files, 4 MiB source and 4 MiB output. Cancellation removes only its randomly
named owned container. Logs and errors use fixed messages without source text.

A small trusted launcher sets Linux no_new_privs before starting Node. The bridge
asserts the kernel flag. Setting Docker's flag before its initial exec is rejected
by this Docker Snap/AppArmor environment; setting the same restriction inside the
entered profile preserves the boundary and passes actual native acceptance.

Create a graph with POST `/api/v1/repository-graphs`, one `Idempotency-Key`, the
normal authenticated workspace/repository headers, and this synthetic shape:

```json
{"sources":[{"repositoryId":"application","collectionId":"00000000000000000000000000000000","digest":"0000000000000000000000000000000000000000000000000000000000000000"}]}
```

Replace zero IDs/digests with facts explicitly inspected through collection APIs.
GET `/api/v1/repository-graphs` lists authorized maps; GET `/{id}` inspects one.
GET `/{id}/query?search=Handler&depth=1&limit=20` searches and traverses it.
Use `nodeId` instead of `search` to investigate an exact displayed node. The shared
Go client exposes the same commands. Source freshness, gaps and truncation remain
visible in query responses.

```bash
export CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable'
go test -race ./internal/store ./tests/acceptance -run RepositoryGraph -count=1
CONDUCTOR_TEST_CODEGRAPH=1 go test -race ./internal/codegraph ./tests/acceptance -run CodeGraph -count=1
CONDUCTOR_TEST_TEMPORAL=1 go test ./internal/contextworkflow ./tests/acceptance -run 'Temporal|DurableContext' -count=1
```

The CodeGraph opt-in requires the image ID environment variable and Docker access;
missing prerequisites fail after opting in. PostgreSQL tests own isolated schemas.
Temporal process tests own their own services, never the developer database.
Selected-path collections cover at most 32 explicit paths. The whole-repository
mode below acquires the complete bounded Git bundle and records indexing gaps.
Broader platform/language certification and production Temporal deployment remain
separate unfinished work.

## Whole repository source

Use `conductor context-collect --full-source` with the normal commit/path/key
flags, or set `fullSource: true` in a collection request to acquire an exact Git bundle and
index its tree using the configured CodeGraph adapter. Keep the normal explicit
paths for the version 2 package context receipt. Inspect `collection.fullSource`
and supply its digest as `fullSourceDigest` on the graph source selector to use
that whole-tree index. A changed digest is a new inspected input; do not refresh
inside graph or package confirmation.

This path needs Git 2.43.0 and util-linux `prlimit` at `/usr/bin/` on the trusted
worker, plus migration 008. Provider credentials remain in the trusted HTTP proxy;
Git and CodeGraph receive none. Both required repository providers use their
existing read integration. GitLab credentials also need Git-over-HTTPS read access,
not merely permission to inspect API metadata. No new source-read/publication
permission is inferred from a ticket or graph.

```bash
go test -race ./internal/repositorycontext/remote -run FullSource -count=1
CONDUCTOR_TEST_CODEGRAPH=1 go test -race ./tests/acceptance -run FullSource -count=1
```

The first test uses real Git smart HTTP with controlled provider metadata. The
second uses an actual Git bundle, native CodeGraph container and isolated database.
Add `CONDUCTOR_TEST_TEMPORAL=1` to include the owned Temporal workflow path.
See [source architecture](../architecture/source-bundles.md) for finite limits.

## Browser workflow

Sign in and select the workspace and repository. In **Repository relationships**,
refresh graphs to inspect shared relationships and their exact source digests.
Search a symbol/path or explore a returned node, choosing a depth from zero to five.
The graph and query show bounded coverage, unresolved evidence and unknown freshness.

Authors can open **Build a graph from inspected receipts**, load available
repositories, then inspect one retained receipt per repository and add it to the
source list. Include the selected repository. Whole-source receipts offer an
explicit index checkbox and bundle digest. Record the graph after inspecting its
sources. If the response is lost, **Retry exact graph request** reuses the complete
input and key without refreshing source. Changing scope or losing source access
clears inspection and source selections.

The **Shared context collections** request form also offers whole-repository source.
The returned bundle summary exposes its commit, tree, digest and index coverage;
a successful request alone is not a completed collection or passing verification.

After building the web app, run the actual signed-login and PostgreSQL browser paths:

```bash
CONDUCTOR_TEST_BROWSER=1 go test -race ./tests/acceptance -run 'TestBrowser(RepositoryGraphs|ContextCollections)$' -count=1
```

Set the database and pinned browser Python environment as documented in the
[browser runbook](browser-sign-in.md). Graph browser fixtures retain real Git bundles
with explicitly unexecuted index entries; native CodeGraph and provider acquisition
are verified by their separate actual-process acceptance above.
## Retained source inspection

Implemented: an authenticated client can read one retained artifact from any source
in its inspected graph through `GET /api/v1/repository-graphs/{id}/artifact` and
`GetRepositoryGraphArtifact`. The request captures the graph digest, repository,
collection, receipt digest, optional full-source digest and literal path. Every
repository in the graph must remain readable in the same transaction, including
sources other than the requested artifact. The workspace and anchor repository
stay fixed; a mismatched source tuple cannot widen coverage.

Selected-path receipts and retained full-source artifacts expose at most 64 KiB of
text per clean relative path (at most 1024 UTF-8 bytes). Responses preserve source
commit/digests, unknown freshness, individual missing/unavailable/truncated states,
`selected_paths` versus `full_source` coverage, and whole-source truncation. A path
not retained returns an explicit unavailable artifact. Reads never acquire newer
source, run repository commands or expose Git bundle bytes or credentials.

Signed-token/live-PostgreSQL acceptance covers related-repository source reads,
revocation of any graph endpoint, forged tuples and identity, path/query bounds,
selected-path gaps and real Git bundle text outside the selected-path receipt.
The latter uses an explicitly synthetic unsupported extraction fact; it proves
retention and authorization, not CodeGraph execution.

In the browser, choose **Retained source repository** and a literal path inside the
inspected graph, or **Read source** on a query node. The workspace and graph anchor
stay fixed while the server checks every repository grant. The view displays exact
source digests, retained text, coverage and unknown freshness. Text digest mismatch,
missing retained content and source access loss are explicit; changing scope or
losing source access clears the graph and text.

Agents use `conductor_read_graph_source` with the same inspected graph/source tuple
and literal path. MCP output remains bounded and untrusted; the tool exposes no Git
bundle or fresh provider read. Signed stdio/PostgreSQL acceptance verifies related
source reads and revocation, and real browser acceptance verifies source selection,
exact scope/pins, altered text rejection and cached source clearing.
