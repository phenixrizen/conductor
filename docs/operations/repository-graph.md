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
