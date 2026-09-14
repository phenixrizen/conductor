# Authenticated terminal delivery plan

1. Expose credential-free scope metadata from the Go client and retain HTTP status
   on invalid error responses. Keep bearer credentials private and reject redirects.
2. Reuse the CLI credential path for the TUI with required workspace/repository
   selection. Capture client configuration once when the terminal session begins.
3. Add bounded startup and explicit-refresh access discovery to the terminal state
   machine. Gate controls with server capabilities and preserve exact confirmation.
4. Clear sensitive review state on authentication/permission failure and require
   deliberate recovery. Prove cancelled and superseded responses cannot restore it.
5. Run real authenticated PTY acceptance with signed tokens and isolated PostgreSQL,
   retain local PTY regression, and document the supported workflow and limits.
6. Expose the Feature 006 collection commands in a separate authenticated view:
   bounded discovery, explicit request-file preview, same-key recovery, receipt
   inspection, requester cancellation, and version-checked attachment. Keep one
   credential and scope, and clear both views when access cannot be confirmed.
7. Extend real PTY acceptance for shared receipts, exact attachment requests,
   concurrent edits, lost responses, cancellation intent, and permission recovery.
   Use trusted receipt fixtures for interface tests and retain separate provider
   and Temporal process acceptance.

## Implemented follow-through

[Feature 006](../006-durable-context/spec.md) implements bounded repository-context
collection before coding execution, with PostgreSQL request/receipt facts and
Temporal sequencing. The terminal now exposes its existing commands. Production
Temporal deployment and live provider compatibility retain their documented limits.
Coding attempts now bind immutable approved inputs and separate human execution
permission in [Feature 009](../009-coordinated-execution/spec.md); collection cannot
grant that authority. [Feature 011](../011-repository-delivery/spec.md) adds separately
authorized GitHub/GitLab draft publication, and
[Feature 012](../012-work-tracking/spec.md) selects one Linear/Jira tracker per workspace.

Implementation checks do not approve an ADR, grant a package approval, authorize a
merge, or establish deployment.

[Feature 019](../019-guided-change-authoring/spec.md) adds an in-process guided
Change editor, readable Design sections and explicit separate save/submission
controls. Advanced imports remain available; fixed identity, exact confirmation,
unknown-field preservation and access-recovery requirements still apply.

## Current release delivery instruction

The user authorized stacked PRs on 2026-09-13 for the full release. Follow its
required gates and explicit dependency order in the
[full release contract](../../docs/full-release.md).

## Full-release terminal delivery

Shared CLI commands and the separate authenticated release workbench cover graph
creation/query, task proposals and human execution decisions, exact artifact review
and publication authorization, workspace tracker linking/synchronization, and
scoped runtime evidence requests and inspection. Failed and read-only task artifacts
remain inspectable without a delivery proposal. Strict request-file previews retain
keys across explicit retries. Actual signed API/PostgreSQL PTY coverage spans these
controls alongside the original workbench. See the
[release terminal guide](../../docs/operations/release-terminal.md).
