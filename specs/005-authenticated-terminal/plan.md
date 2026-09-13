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

## Following increment

The proposed [Feature 006](../006-durable-context/spec.md) starts with bounded
repository-context collection before coding execution. Define collection authority,
immutable requested source, outbox, retry/reconciliation behavior, and evidence
states before introducing an external workflow runtime. Coding attempts will later
require their own immutable approved inputs and execution permission. Keep
PostgreSQL as the authority for review facts and
Temporal as the planned execution sequencer. Repository and tracker integrations
continue to require GitHub/GitLab and one Linear/Jira tracker per workspace.

Implementation checks do not approve an ADR, grant a package approval, authorize a
merge, or establish deployment. Publish focused commits and a separate PR for each
complete increment, retaining its prerequisite branch while that PR is open.
