# Browser sign-in delivery plan

1. Research and pin OIDC/OAuth libraries compatible with Go 1.24. Share bounded
   issuer discovery and key rotation with the API verifier, while keeping ID-token
   and API access-token validation distinct.
2. Add migration 003 for bounded, expiring login attempts and opaque sessions.
   Prove one-time consumption, concurrent admission, revocation, and pool reopen
   with PostgreSQL. Never persist provider tokens or infer principal permissions.
3. Add fixed-origin login/callback/session/logout routes and integrate cookie
   identity with the existing authenticated command service. Require browser
   binding, CSRF protection, explicit credentials, and safe failure messages.
4. Add the browser identity and scope controls around the existing workbench.
   Remount review state on access changes and preserve exact-inspection approval.
5. Exercise a real signed TLS authorization-code flow, PostgreSQL, and Chromium;
   retain local review regression and actual API/PostgreSQL restart checks.

## Next increment

Extend authenticated terminal review using the existing Go client, server-provided
identity, and repository capabilities. Keep terminal file preview, cancellation,
exact revision confirmation, and ambiguous-outcome recovery. Then define one
durable execution workflow with its outbox and reconciliation boundary before
introducing Temporal or external repository/tracker adapters.

Publish each increment on a focused branch and PR, stacked on an open prerequisite
when needed. Merge remains a human action; successful tests do not grant approval.
