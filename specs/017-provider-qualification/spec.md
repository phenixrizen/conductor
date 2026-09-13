# Actual OIDC browser provider qualification

**Status: Implemented with local provider qualification.** Keycloak 26.7.3 runs as
an actual test-owned OIDC service, independently of the synthetic issuer fixtures.
No hosted identity tenant or production deployment is included. Browser sign-in
compatibility and API access-token compatibility remain separate claims.

The acceptance environment owns a pinned Linux/amd64 Keycloak container, internal
Docker network, loopback encrypted-byte forwarder, synthetic realm/client/users,
short-lived TLS certificate, PostgreSQL schema and Chromium browser contexts.
Keycloak supplies discovery, signing keys, login forms and token issuance. Conductor
uses its existing confidential authorization-code client, exact callback, S256 PKCE,
nonce validation, server sessions and transactional repository authorization.

The test must prove real human collaboration, stale revision rejection and exact
independent approval; server-owned agent browser-session denial and foreign workspace
isolation; secure HttpOnly session cookies; Conductor logout and revoked-cookie
replay denial; provider SSO remaining distinct from local logout; account switching,
old-tab denial and explicit identity recovery; changed nonce/PKCE rejection followed
by a fresh successful sign-in; and consumed callback rejection.

Default Keycloak access tokens and ID tokens must remain rejected by Conductor's
strict RFC9068 RS256 API bearer profile. Qualification must not change claim/header
requirements or synthesize a provider token to make that profile appear compatible.
No external provider credentials or raw token bodies enter committed fixtures or
test logs; the checked-in realm inputs contain synthetic qualification identities only.

The opt-in fails when required dependencies are absent. Without the opt-in it skips
explicitly. Cleanup affects only generated resource names and schemas; no existing
provider, database service, container or volume is restarted or deleted.
