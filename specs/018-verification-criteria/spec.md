# Explicit coding verification criteria

**Implemented.** This feature links retained coding checks to criteria in exact
package revisions. It does not introduce business policy or a new approval grant.

Package content may contain `verificationCriteria` with `schemaVersion: 1` and
one to 32 unique criteria, each with a stable ASCII identifier and an explicit
description. The catalog is bounded to 32 KiB; individual descriptions to 4096
UTF-8 bytes. Other package fields and uninterpreted extensions retain their meaning
and immutable digest. Unknown or invalid catalogs cannot be linked.

Each declared verification command may include up to 16 `requirements`, identifying
the package change ID, revision, digest and criterion ID. Each reference must match
an exact package pin in the plan and a repository included in that task's source
scope. A command may support a related repository's criterion when that repository
is in its task scope. Duplicate, unknown, stale and foreign references are rejected.

Proposal validates the pinned criterion's existence. Human execution admission,
input loading, running authority checks and receipt commit retain the existing
current exact independent package approval and all-repository permission checks.
No token, perspective, description or check result grants execution authority.
The original links are copied into worker input and every retained check result,
including failed and unexecuted checks. Result admission and publication reject
added, omitted or substituted references.

Requirement extensions use omission when empty. Legacy package, plan, input and
artifact digests remain unchanged; prior evidence is never rewritten to infer
links. Old worker images may reject linked inputs and must be rebuilt and explicitly
selected by their inspected immutable image/profile pins.

Exact task-artifact inspection adds a separate derived `verification` envelope.
It reads the original pinned package revisions under the same all-source grant
transaction and lists each criterion as `supported` or `not_verified`. Supported
means every command declared for that criterion in this task has a complete passing
result bound to the artifact and original links, with successful producer execution, cleanup
and exact approved revision facts. Unlinked, missing, failed, unexecuted, truncated
or altered check evidence remains not verified. A criterion with no declared check
cannot inherit another criterion's passing result. Absent or uninterpretable
catalogs remain explicit gaps. There is no overall acceptance conclusion.

Historical inspection preserves original descriptions, links and artifact digests
after a package edit; it never refreshes content inside an inspection or approval.
Current all-repository read grants remain necessary. Browser, API, shared client,
MCP, CLI and TUI expose the exact links. Untrusted descriptions remain escaped.
Criterion support cannot prove all requirements, design approval, merge, deployment
or production outcomes.
