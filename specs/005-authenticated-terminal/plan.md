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

## Following increment

[Feature 006](../006-durable-context/spec.md) implements bounded repository-context
collection before coding execution, with PostgreSQL request/receipt facts and
Temporal sequencing. The terminal now exposes its existing commands. Production
Temporal deployment and live provider compatibility retain their documented limits.
Coding attempts will require their own immutable approved inputs and execution
permission; collection cannot grant that authority. Managed repositories continue
to require GitHub/GitLab support, and planned tracker integration selects one
Linear/Jira tracker per workspace.

Implementation checks do not approve an ADR, grant a package approval, authorize a
merge, or establish deployment. Publish focused commits and a PR targeting the
current `main` for each complete increment. Integrate prerequisites before marking
a dependent PR ready; do not require reviewers to manage stacked merge order.
