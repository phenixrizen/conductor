# Feature 007: Shared repository graphs

**Status: Implemented bounded receipt-backed and whole-repository graphs;
broader language/runtime compatibility remains partial.** Architectural approval,
implementation verification, merge and deployment are separate facts.

## Purpose

Engineers and agents can investigate dependencies across repositories using the
same immutable, authorized source evidence. A graph binds every included repository
to an inspected context receipt at one exact commit. It never follows a moving
branch or interprets source instructions as platform authority.

## Commands and authority

1. An authenticated principal selects a workspace and anchor repository, then
   explicitly creates a graph from 1–16 distinct repository receipt selectors.
   Each selector contains canonical repository ID, collection ID and receipt digest.
   The anchor must be among the sources. All sources must belong to the workspace.
2. Creation requires current author permission on every repository. Resolve all
   receipts and lock the principal, membership and grants in the transaction that
   commits the immutable graph, source links and concise audit event.
3. The idempotency key belongs to the workspace and server-derived creator. Sort
   selectors by repository ID before hashing. Retrying the same input/key returns
   the same graph. Changed input conflicts; no transport retries are hidden.
4. Every graph inspection, query and discovery row requires current read permission
   on **every** included repository. A hidden or revoked endpoint hides the whole
   snapshot. Filter before pagination, then hold grants through read completion.
   Unknown/inaccessible IDs return the same 404; no hidden counts or cursors leak.
5. Local actors cannot access graphs. Graph creation does not approve a package,
   authorize coding or publication, or execute repository-controlled commands.

## Evidence and retrieval

Snapshots have schema version 1, deterministic SHA-256 digest, indexer profile,
source receipts, commit IDs, collection times, nodes, edges, explicit gaps and a
truncation flag. Source text stays in receipts. Nodes retain repository, collection,
path, artifact digest and source line when known. Edges retain source evidence and
upstream provenance where supplied. Freshness is `unknown`: collection at an exact
commit does not establish equality to today's branch or deployed system.

The native parser extracts Go declarations/imports, go.mod module requirements,
and package.json dependency names. Exact/longest module-prefix matching resolves
relationships only against unique manifests in the selected receipts. Ambiguous or
missing targets remain unresolved. Declared dependency versions, replacements,
build tags, dynamic dispatch and runtime compatibility are not verified.

An optional activity uses the selected upstream
[CodeGraph](https://github.com/colbymchenry/codegraph) 1.6.0, including its native Rust
kernel, in a credential-free, network-disabled container. Its source-bound symbols,
relationships, unresolved references and native/portable extraction observations
are committed atomically alongside the source receipt. Existing receipts are never
backfilled or rewritten. The graph consumes only stored trusted index facts.

Bounds are 4,096 nodes, 8,192 edges, 512 gaps and 1 MiB snapshot JSON. Capacity
trimming removes dangling edges and marks truncation. Query results contain at most
100 nodes; depth is 0–5 in both edge directions. Search is case-insensitive literal
name/path matching (maximum 256 bytes), not a regular expression. A node selector
and search cannot be combined. Every returned edge has both endpoints in the result.
Keyset discovery defaults to 20 and permits 1–100 rows.

The optional `fullSource: true` collection mode acquires a complete bounded Git
bundle while preserving the 1–32 explicit paths in the version 2 context receipt.
The bundle and full-tree index commit as separate immutable facts. Graph selectors
must explicitly include the inspected `fullSourceDigest` to use whole-tree source.
The index accepts up to 512 paths and 4 MiB text. Complete Git objects do not establish
complete semantic coverage: binary/LFS/submodule/symlink/unsupported and bounded-out
paths remain visible gaps. See [source bundles](../../docs/architecture/source-bundles.md).

## Acceptance

- Two repository manifests and their source yield a shared dependency relationship.
- Unauthorized or revoked endpoints disappear from reads, search and discovery,
  including pagination boundaries; workspace/anchor selection is enforced.
- Forged receipt digest, client source injection, duplicate source selection and
  duplicate/unknown command fields are rejected.
- Concurrent identical creates return one snapshot and one audit event; audit
  failure rolls back graph and source links. Database facts reject update/delete.
- Real CodeGraph native extraction produces source symbols and a call relation in
  a network-disabled container. Actual PostgreSQL/activity/API/client acceptance
  consumes that index and preserves receipt replay after revocation.
- Source/credential text never enters workflow payloads, diagnostic errors or logs.
  Cancellation removes the owned temporary container; missing native prerequisites
  after opting into acceptance fail explicitly.
