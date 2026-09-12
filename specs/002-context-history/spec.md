# Feature 002: inspectable history and pinned repository context

**Status:** Implemented for local development; shared deployment requires production
identity and repository permissions.

## Outcome

An author captures explicitly selected repository files at one immutable Git
commit, adds the snapshot to package content, and submits that revision for design
review. Reviewers can recover older content and approval records, compare revisions,
and see missing context without confusing collection with successful verification.

## Acceptance criteria

1. History lists revision metadata with bounded, stable cursor pagination. A
   specific revision returns its stored content and historical approvals. Audit
   events are inspectable in sequence order with bounded pagination.
2. Historical approval records remain visible after an edit. Historical views
   never imply that approval is effective for the current package.
3. Approval continues to send the latest revision and digest actually inspected.
   A conflict requires explicit renewed inspection. Selecting history cannot
   trigger approval, and late responses cannot replace a newer inspection.
4. Context collection runs locally against an explicitly selected Git repository,
   resolves a ref once to a full commit ID, and reads committed blobs. Uncommitted
   working-tree changes do not enter the snapshot.
5. Each selected path reports collected, missing, unavailable, or truncated.
   Collected text carries its Git blob ID and SHA-256 digest. Collection is not
   evidence that a build, test, or architectural review passed.
6. A snapshot retains repository identity, requested ref, resolved commit,
   collection time, collector version, and complete bounded text for collected
   artifacts. It becomes part of the immutable package content/digest.
7. Only explicit relative paths are inspected, at most 32 files, 64 KiB per file,
   and 256 KiB total text. Symlinks, binary files, submodules, missing files, and
   oversized content cannot be presented as collected evidence.
8. A separate freshness check compares a snapshot with the requested repository
   ref and reports current, stale, or unavailable. It does not alter the snapshot,
   refresh an approval, or execute repository-controlled commands.
9. Unknown package fields survive snapshot attachment and revision round trips.
   The optional `repositoryContext` field has a validated versioned contract.
10. Human perspectives (architect, QC, developer, product) organize review prompts.
    They confer no permissions and do not represent an authenticated role model.
11. Developers and agent clients pointed at the same Conductor API use one shared
    PostgreSQL dataset. Work is discoverable through a paginated change list and an
    exact repository-identity filter; knowing another person's change ID is not
    required to find their context.
12. Shared listings show the latest package revision and its author/approval view.
    Related work links to recoverable package content and audit history. It does
    not claim that a developer or agent is currently executing a task.
13. Concurrent clients use the existing expected-revision and inspected-digest
    checks. One client's edit becomes visible to the other, and a stale edit or
    approval is rejected rather than overwriting or approving refreshed content.

## Integration and authority boundaries

Spec Kit and ADRKit artifacts can be selected as ordinary committed files. Their
native content, paths, and revisions are retained without interpreting a tool's
status as Conductor approval. This is a Git artifact collector, not a tested
Spec Kit or ADRKit command/API adapter.

The server never accepts a filesystem path to execute a context collection.
Snapshots are author-supplied evidence; digest validation checks internal integrity,
not independent provenance or repository authorization. The current local actor
header remains local-development-only. Production identity and repository-aware
permissions remain prerequisites for shared deployment. Execution is disabled.

Repository identity is currently supplied by the author and matched exactly for
discovery. It is a grouping label, not proof of repository access or a permission
boundary. A future authenticated workspace will own canonical repository identity
and membership rather than trusting a client-supplied role or grouping label.

## Deferred work

Production identity, role assignments, policy-driven required evidence, automated
context refresh, artifact storage, TUI expansion, execution attempts, and workflow
orchestration remain separate increments. This feature does not waive the remaining
Milestone 1 exit criteria or accept proposed ADRs.
