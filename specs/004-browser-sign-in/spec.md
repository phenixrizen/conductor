# Feature 004: browser sign-in and shared review

**Status:** Implemented browser authentication and review. Verification uses a
signed synthetic identity provider, PostgreSQL, and a real browser; it does not
certify an enterprise identity-provider deployment.

## Outcome

A reviewer signs in through the configured OpenID Connect provider, chooses an
authorized workspace and repository, and inspects shared work in the web workbench.
The same domain commands and stored permissions govern API, CLI, and browser work.
An account label or review perspective never establishes authority.

## Acceptance criteria

1. Browser sign-in is an explicit addition to OIDC mode. Require a fixed public
   HTTPS origin, client ID, and protected client-secret file together. Keep local
   development explicit and isolated. Failure to discover server mode or identity
   must never fall back to a local actor.
2. Use authorization code flow, S256 PKCE, state, and nonce. Store each pending
   attempt durably with a hash of its state and browser-binding secret. A matching
   callback consumes the attempt atomically once, within five minutes, before
   exchanging the code. Wrong-browser callbacks cannot consume another attempt.
3. Verify signed RS256 ID tokens for the exact issuer and browser client audience,
   required subject/expiry/issued-at/nonce, optional authorized party and not-before,
   and access-token hash when supplied. ID tokens authenticate this login only;
   the API bearer adapter retains its separate RFC 9068 profile.
4. Keep provider credentials, ID tokens, access tokens, and refresh tokens outside
   browser state, logs, and Conductor sessions. Bound provider responses and reject
   redirect/retry behavior during code exchange. A failed or uncertain exchange
   requires a new sign-in. No refresh or provider logout is claimed.
5. Create an opaque, one-hour Conductor session only for an already provisioned,
   active human principal. Persist the cookie token's hash, session-bound request
   secret, principal association, and expiry. Use Secure, HttpOnly, host-only,
   SameSite=Lax cookies with root path. Do not enroll people or assign permissions
   from provider role, group, email, or display-name claims.
6. Cookie commands require the exact configured Origin and session-bound CSRF
   header. Reject duplicate cookies, duplicate credentials, local actor headers,
   and mixed cookie/bearer identity. Validate a supplied request secret on reads
   too, so an old tab cannot silently inspect as a newly signed-in account.
7. Recheck current principal and ordinary workspace/repository permissions on
   requests. Expired or revoked sessions cannot authorize subsequent requests.
   Logout removes the server session and clears the cookie; it does not promise
   cancellation of commands already authorized or logout at the identity provider.
8. Bound active login attempts to 4,096, sessions to 10,000 globally, and sessions
   to 20 per principal. Reject admission at capacity without evicting another
   active session. Expired rows are pruned on subsequent creation; unusable does
   not imply that physical cleanup already occurred.
9. Populate workspace and repository selectors from bounded server discovery.
   Show missing membership, empty lists, truncation, loading, session expiry,
   unavailable login, denied access, and request errors explicitly. Capabilities
   affect available controls; the service remains authoritative.
10. Changing identity, session, workspace, or repository cancels pending work and
    clears all package/history/comparison/discovery state. Capture immutable access
    details for each request and ignore superseded responses. Authentication or
    permission failure removes inspection and requires deliberate recovery.
11. Approval still submits exactly the displayed revision and digest without a
    refresh inside that action. Stale or uncertain results require renewed
    inspection. Historical records remain read-only. A persona cannot enable
    approval, and an author cannot approve their own revision.

## Boundaries

The browser reviews saved packages and their history. Package authoring remains
available through the CLI/API. Authenticated TUI access, CLI token acquisition,
provider-specific certification, session refresh, federated logout, and deployment
automation are following work. Execution, publication, and tracker synchronization
remain separate capabilities. No ADR is accepted by this implementation.
