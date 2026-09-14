# Feature 004: browser sign-in and shared review

**Status:** Implemented browser authentication, review, and shared context controls.

[Feature 019](../019-guided-change-authoring/spec.md) adds guided Change creation,
Design editing and separate review requests under the same session and exact
command rules. Uncertain authoring writes require inspection, and denied access
clears editor/preview state along with review state.
Authentication verification uses a
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
    clears all package/history/comparison/discovery state, collection drafts,
    receipts, and confirmations. Capture immutable access
    details for each request and ignore superseded responses. Authentication or
    permission failure removes inspection and requires deliberate recovery, even
    when the denial response body is missing, interrupted, or stalled.
11. Approval still submits exactly the displayed revision and digest without a
    refresh inside that action. Stale or uncertain results require renewed
    inspection. Historical records remain read-only. A persona cannot enable
    approval, and an author cannot approve their own revision.
12. Bring the existing [Feature 006](../006-durable-context/spec.md) collection
    commands into the authenticated repository workbench. Read permission allows
    bounded shared discovery and inspection. Author permission comes only from
    validated repository discovery; the service still requires current permission
    and an operator-enabled read integration. Local mode cannot collect remotely.
13. Request an exact lowercase 40-hex commit and 1–32 explicit relative file paths
    with a visible idempotency key. Capture the input and key before sending.
    An uncertain result keeps that exact draft locked for an explicit same-request
    retry. Do not retry automatically or silently replace its key. Starting new
    work after a confirmed request is a separate action.
14. Refresh collection lists and inspections only on explicit actions. Show absent,
    unavailable, unresolved, and stale execution observations with their timestamps.
    Age progress locally after 30 seconds without polling the service. Cancellation
    intent does not confirm that execution stopped. A receipt is immutable source;
    its missing, unavailable, or truncated paths remain visible even after workflow
    completion. Source collection never establishes a passing check.
15. Only the currently authorized requester can request cancellation. Confirm the
    displayed collection identity before sending. Attach a receipt only to a current,
    non-stale package inspection in the selected repository, with a confirmation
    capturing package ID, revision, package digest, collection ID, and receipt digest.
    Neither confirmation refreshes source or package content inside its command.
    Attachment creates an ordinary new draft and does not carry forward approval.
    An uncertain or conflicting result requires renewed inspection; uncertain
    attachment also blocks package approval until both the current package and
    collection have been explicitly inspected again. Clearing a selection or
    receiving another mutation response does not satisfy this recovery.
16. Render version 2 source metadata and coverage as escaped text while keeping
    complete JSON available. An embedded receipt ID is an unverified reference.
    Describe linkage to an inspected server receipt only after a scoped collection
    response and full snapshot JSON comparison, including unknown nested fields.
    Preserve version 1 extension meaning and unknown outer package fields.

## Boundaries

The browser supports human sessions only. It reviews saved packages and their
history and lets repository authors request context and explicitly attach an
inspected receipt. General package creation, editing, and submission remain
available through the CLI/API and the authenticated terminal in
[Feature 005](../005-authenticated-terminal/spec.md). Agent identities use those
authenticated API clients and cannot obtain browser sessions or approve designs.
CLI token acquisition, session refresh, federated logout and deployment automation
remain unsupported. [Feature 017](../017-provider-qualification/plan.md) qualifies
one native Keycloak browser-login profile; it does not broaden API bearer support.
Context workers support explicit local or [TLS/mTLS](../015-temporal-tls/spec.md)
transport. Coordinated execution, publication, tracker synchronization and runtime
evidence are implemented under their separate feature contracts; browser controls
add no workflow or provider authority. Live provider and production qualification
remain bounded as recorded in each plan. No ADR is accepted by this implementation.

## Current full-release workflow navigation

The authenticated browser groups the implemented workbench into Review, Source &
graph, Agent work, Delivery, Tracker and Runtime. Only the selected workflow is
visible. All tabs use the same App-owned session and canonical scope, with no
permission or model selection implied by navigation or human perspective.
Architect, QC / QA, Developer, Product and Operations change guidance only.

Leaving a workflow aborts pending browser work, fences late responses and clears
unsubmitted confirmations. Interrupted writes retain their exact request/key for
explicit recovery; interrupted approval, execution/publication authorization and
source attachment require renewed inspection. Tab changes do not refresh or replay
commands. Scope/session changes and access denial clear all private state, including
hidden workflows. Keyboard navigation and phone layouts are covered by signed
Chromium/PostgreSQL acceptance. See the
[navigation runbook](../../docs/operations/browser-navigation.md).
