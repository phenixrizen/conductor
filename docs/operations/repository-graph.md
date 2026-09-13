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
The current collector's 32 explicitly selected paths are partial coverage. Complete
repository acquisition, broader platform/language certification and production
Temporal deployment remain separate unfinished work.
