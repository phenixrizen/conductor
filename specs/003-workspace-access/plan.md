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
Authenticated terminal access follows through that same boundary.

Durable external execution remains later work, after the authenticated review
surfaces and recovery contracts are exercised. Each subsequent increment belongs
on a focused branch/PR, stacked on an open prerequisite when necessary.
