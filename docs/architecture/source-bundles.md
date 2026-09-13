# Exact repository source bundles

**Implemented:** optional complete Git bundle acquisition at an exact commit,
whole-tree bounded CodeGraph indexing, immutable source facts, and a trusted bundle
loader for coding/publication activities. This adds to version 2 selected-path
receipts; their format and digest semantics remain unchanged.

An author requests `fullSource: true` alongside the exact commit and 1–32 explicit
context paths. The existing operator-enabled repository read integration supplies
the credential. Full source requires the pinned CodeGraph worker configuration.
The activity collects the selected paths, acquires a bounded Git bundle, indexes
its tree, then commits the original receipt and separate bundle/index facts in one
authorization transaction. A replay recovers those committed facts without fetching
new content or backfilling a receipt.

The trusted adapter verifies the provider's numeric repository identity and commit
through the existing pinned REST profile. A short-lived loopback proxy forwards
only smart Git HTTP discovery and upload-pack requests to that verified provider
repository. The proxy checks current permission before every exchange, rejects
redirects, bounds bytes, and retains the read credential. Git receives a random
loopback URL and a scrubbed environment, never a token, credential helper or provider
URL. The activity rechecks provider identity after fetch and normal authorization
at commit. GitHub's returned root tree is compared with the actual Git tree.

Git runs in a private bare repository with hooks, inherited/system configuration,
external transports, templates, submodule recursion, auto-maintenance and redirects
disabled. It preserves the actual commit and tree, including history within the
bundle limit; it does not synthesize a replacement commit. Process-group
cancellation, resource limits and a disk monitor bound parsing. CodeGraph separately
runs in its credential-free container. Source bytes and credentials never enter
Temporal history.

Migration 008 stores the immutable bundle, SHA-256 digest, actual commit/tree,
canonical repository, receipt digest, bounded tree artifacts and index facts.
`Collection.fullSource` exposes only a summary. Its `fileCount` counts Git tree
entries; `indexedFiles` counts complete text files supplied to CodeGraph, whose
per-file parser coverage is retained in the graph. Trusted integration activities call
`LoadSourceBundle` with exact workspace/repository/collection/receipt/commit; a
scoped transaction checks current read permission. An unscoped trusted worker must
hold its independently authorized coding/publication command. No public endpoint
returns bundle bytes.

Graph creation explicitly supplies `fullSourceDigest` with the inspected receipt
selector to consume the whole-tree index. Omitting it preserves selected-path
behavior. Graph source records retain that digest, and every repository remains
protected by the existing all-source permission checks. Full acquisition is not
complete semantic coverage: binary files, LFS pointers, submodules and symlinks are
retained as Git objects but never followed; source/file/index limits and unsupported
parsers remain explicit gaps.

| Boundary | Enforced limit |
|---|---|
| Smart HTTP exchanges | 16, each permission checked |
| Git request / total response | 1 MiB / 32 MiB |
| Complete Git bundle | 32 MiB; exceeding it fails, never truncates Git objects |
| Acquisition / external response | 40 / 35 seconds |
| Git virtual address space / file size / CPU | 1 GiB / 64 MiB / 35 seconds per process |
| Repository scratch size | 64 MiB monitored, process group cancelled on excess |
| Tree metadata | 4 MiB; exceeding it fails |
| Index source | 512 paths, 64 KiB/file, 4 MiB text; omitted content is explicit |

Tests use actual Git 2.43.0 smart-HTTP, complete bundles and clone recovery with
controlled GitHub/GitLab metadata fixtures. Separate native CodeGraph and live
PostgreSQL acceptance proves atomic source/receipt/index persistence and shared
graph provenance. An owned Temporal acceptance also runs the full-source activity
through the production dispatcher/runtime and checks that workflow history contains
no source or credentials. Live provider acceptance has an explicit opt-in; fixtures do not
prove live provider or production deployment compatibility.
