# Coding worker implementation plan

1. Define immutable task, full Git bundle, adapter profile, patch and evidence
   contracts independently of persistence and workflow sequencing.
2. Build a trusted non-root container entry point that validates exact commits,
   rejects unsupported filesystem modes, and reconstructs patches outside
   producer-controlled Git metadata.
3. Run verification in a fresh offline container and restore exact source trees
   independently for each declared check.
4. Pin and inspect actual Codex/Claude CLI packages and terminal-result protocols.
   Keep source, persistent sessions and provider credentials outside workflow
   history; never substitute fixtures for live model compatibility evidence.
5. Isolate provider credentials in a separate bounded gateway with an isolated
   Docker network. Fail closed when Docker cannot provide the required topology.
6. Exercise actual Docker boundaries, native CLI discovery, command failures,
   source/path constraints, cancellation, and precise evidence retention.
7. Integrate the runner from a trusted Temporal activity. Its caller resolves
   canonical authorized full bundles and commits source-bound receipts under the
   current authority check. A separate publisher consumes exact patches.

Steps 1–6 are implemented and verified by the worker tests. Step 7 and trusted
publication are implemented and exercised by the complete release gate below. The worker
does not accept an ADR, enable a repository, or authorize itself.

Retained task artifact inspection is implemented independently of publication:
exact run/task/artifact pins, all-repository read locks, complete bounded typed
output, and shared API/client/MCP/CLI/TUI access. Failed checks and design-only
reports retain their evidence states. Live authorization/integrity checks and
actual PTY inspection cover this read path.

Browser task-artifact acceptance is verified with signed sessions and PostgreSQL:
failed reports without patches, exact receipt queries, byte-digest substitution
rejection and access-denial clearing pass. Delivery, coordination and runtime browser
regressions also pass with the shared complete artifact viewer.

The complete release gate now joins real whole-source acquisition and native
cross-repository graph extraction with compiled MCP/executor processes, Docker
DAG production/checks, exact trusted publication to both controlled provider HTTP
fixtures, one Linear or Jira workspace tracker, and correlated Groundcover
samples. Recovery preserves retained Temporal receipts and reconciles lost write
responses without duplicate branches, reviews or tracker cards. Provider/model
data remain explicitly synthetic; no live publication or inference is claimed.
See [the release gate runbook](../../docs/operations/full-release-acceptance.md).
