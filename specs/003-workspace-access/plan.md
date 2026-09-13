# Authenticated review delivery plan

1. Define principal identity, canonical repository ownership, capabilities, and
   the separation between legacy local packages and authenticated workspaces.
2. Verify a bounded, researched JWT access-token profile against configured OIDC
   discovery and signing keys. Fail closed on invalid identity or unavailable keys.
3. Add an ordered migration and shared transactional authorization around the
   existing domain commands. Preserve historical content and local isolation.
4. Add explicit server authentication modes, session/repository discovery, trusted
   operator provisioning, and authenticated CLI commands.
5. Prove collaboration, all-endpoint isolation, revocation, attribution, migration
   preservation, and restart compatibility against PostgreSQL. Document the exact
   profile and deployment limits rather than claiming vendor compatibility.

## Following increment

[Feature 004](../004-browser-sign-in/spec.md) now adds configurable browser OIDC
sign-in using authorization code flow, PKCE, nonce/state validation, and protected
server-managed sessions. Browser ID-token validation remains separate from API
access-token validation. The workbench uses the same stored permissions and exact
review command path, and clears inspection when identity or scope changes.
[Feature 005](../005-authenticated-terminal/spec.md) adds authenticated terminal
access through that same boundary, with a fixed credential and scope, capability
discovery, and explicit recovery. It does not add interactive CLI token acquisition.

[Feature 006](../006-durable-context/spec.md) uses this access boundary for durable
repository-context collection. Coding execution remains later work. Deliver each
complete increment on a focused branch with a PR targeting the current `main`.
Integrate prerequisite work before marking a dependent PR ready, so reviewers do
not need to manage a chain of branches or choose a merge order.
