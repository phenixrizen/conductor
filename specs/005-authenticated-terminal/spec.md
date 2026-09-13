# Feature 005: authenticated terminal review

**Status:** Implemented with signed-token, live PostgreSQL, and real PTY acceptance.
Uses the existing authenticated API and
CLI credentials; no new provider integration or API capability is introduced.

## Outcome

An engineer or agent can review the same authorized workspace and repository from
the Bubble Tea workbench. The terminal derives its identity and available controls
from the server, while preserving exact revision approval and bounded file imports.

## Acceptance criteria

1. Start authenticated `tui` with the existing token file or environment credential
   and explicit canonical workspace/repository selection. Reject a local actor
   combined with a token. Do not acquire tokens or refresh them inside the session.
2. Fix the API endpoint, credential, workspace, and repository for the running
   workbench. Resolve the principal and readable repository capabilities from
   bounded `session` and `repositories` responses before showing shared packages.
   Never derive authority from token role claims, labels, or a terminal actor flag.
3. Validate discovered principal, membership, repository ownership, capabilities,
   and bounds before enabling review. If truncated discovery omits the explicitly
   selected scope, say access could not be established from incomplete discovery;
   do not present that omission as a definitive permission denial.
4. Keep the initial principal ID and human/agent kind fixed. A later identity
   mismatch requires a new terminal session. Replacing the token file after startup
   cannot change the credential or the actor shown in an existing session.
5. Author capability enables creation, revision, and submission for humans or
   agents. Approval additionally requires a human, the approve capability, an
   independent reviewer, and the current submitted revision. Client controls
   reflect stored capabilities; the service enforces the actual command policy.
6. Preserve bounded explicit JSON-file selection, complete replacement preview,
   unknown content fields, text escaping, cancellation, and small-screen safeguards.
   The UI never runs repository text as a command, editor, or script.
7. Confirmation captures the displayed revision and digest before dispatch. No
   identity, permission-discovery, or package read occurs inside a mutation. A
   stale or uncertain write blocks further mutations until explicit inspection.
8. Every 401/403 response invalidates access, including reads and malformed error
   bodies. Clear package content, listings, drafts, confirmation, and capabilities;
   ignore late results and require explicit `r` to re-establish access. The client
   retains a bounded typed HTTP status without leaking credentials or raw bodies.
9. Explicit refresh clears the previous inspection, reads current access, then
   loads the selected package or listing. Permission revocation must be enforced
   by the server on every request even before the interface refreshes capabilities.
   Received package/list ownership must match the selected canonical scope.
10. Exercise the compiled CLI in a real PTY against signed-token authentication and
    PostgreSQL: collaboration, exact approval, stale conflicts, agent restrictions,
    revocation, token-file stability, and terminal output without credentials.
    Preserve the existing local terminal and API workflows.

## Boundaries

The workbench inspects current packages; the browser and CLI continue to provide
history and comparison where documented. Bounded discovery has no continuation
after 100 workspaces/repositories. CLI interactive login, token refresh, execution,
repository publication, and work-tracker synchronization remain separate work.
The API command and migration contracts are unchanged by this feature.
