# Feature 003: authenticated workspace review

**Status:** Implemented for the API and noninteractive CLI. Browser sign-in and
authenticated terminal sessions are separate increments. No external identity
provider deployment has been certified by the synthetic acceptance tests.

## Outcome

Engineers and agents share review context within explicitly authorized workspaces
and repositories. The service verifies identity, derives the durable actor, and
checks stored permissions before discovery, inspection, or a command. Knowing a
change ID or setting a role label does not grant access.

## Identity and authority

The configured OpenID Connect issuer supplies discovery and signing keys. API
authentication accepts the documented RFC 9068 RS256 JWT access-token profile,
including the exact issuer and API audience, signature, type, and required time
and identity claims. It does not accept browser ID tokens as API credentials.
Only issuer and subject identify the principal; token roles, groups, and email do
not confer Conductor permissions.

PostgreSQL owns each principal's stable ID, immutable human/agent kind, active
state, workspace membership, and repository read/author/approve capabilities.
Author capability covers create, revise, and submit. Approval also requires a
human principal, a current submitted revision, and an independent reviewer.
An agent cannot approve even if an operator sets its approve capability.

## Acceptance criteria

1. Authentication mode is explicit: `local` or `oidc`. Local mode binds only to a
   literal loopback address and cannot be combined with OIDC settings. OIDC mode
   has no local-header fallback and rejects `X-Conductor-Actor`.
2. Authenticate every shared endpoint. Missing, expired, invalid, or unregistered
   identity cannot retrieve workspace packages. Mixed or duplicate credentials
   fail rather than selecting a convenient identity.
3. A package has immutable workspace and canonical repository ownership outside
   its author-supplied JSON. Repository identity includes provider, normalized
   host, and provider ID. GitHub and GitLab are valid identity types; recording
   them does not establish a working remote delivery adapter.
4. Require workspace membership and the repository capability for every command.
   Check and lock the permission rows in the same transaction as the mutation;
   revocation cannot commit in the middle of an authorized command. Subsequent
   requests observe committed revocation even with the same unexpired token.
5. Apply authorization to current inspection, history, a numbered revision, audit
   events, and discovery. Inaccessible packages use the same 404 as unknown IDs.
   Filter readable repositories in SQL before pagination. Content labels never
   widen access. An optional repository header narrows all package operations.
6. Keep the exact revision/digest approval contract, historical approval records,
   and transactional audit. Derive the recorded actor from the stored principal,
   never from a submitted actor, role, or display name.
7. Migration preserves existing content, digests, authors, approvals, and audit.
   Legacy ownership remains null. Local mode can access only those legacy rows;
   authenticated mode cannot access them. Do not infer ownership from old labels.
8. Expose bounded session/workspace and repository/capability discovery. Return an
   explicit truncation flag after 100 entries. These are server-managed choices,
   not self-service permission assignments.
9. Provision through a trusted database operator command with validated input and
   an administration audit committed atomically. Omitted records stay unchanged;
   explicit false values revoke access. Identity and canonical ownership cannot
   be rebound by an update. The operator label is audit metadata, not a login.
10. CLI credentials come from a bounded token file or environment variable. Never
    send bearer tokens through redirects, to plaintext remote endpoints, or as a
    local actor header. Local context collection does not read credentials.
11. Exercise signed synthetic OIDC tokens and real PostgreSQL for collaboration,
    every endpoint's isolation, revocation, malicious claims/labels, migration
    preservation, and durable permission metadata. Library-only checks cannot
    substitute for the integrated access boundary.

## Boundaries

Interactive OpenID Connect login, browser sessions, token acquisition and refresh,
authenticated TUI review, enterprise identity-provider certification, and deployment
operations remain separate work. The API supports the precise access-token profile
in the [authenticated review guide](../../docs/operations/authenticated-review.md);
arbitrary vendor JWTs, opaque tokens, and ID tokens are not interchangeable.

This increment does not enable agent execution, publication, tracker synchronization,
or automatic merge. No new ADR is accepted implicitly by implementing or testing it.
