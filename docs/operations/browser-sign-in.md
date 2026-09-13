# Browser sign-in and shared review

Conductor can use a configured OpenID Connect provider to sign people into the web
workbench. Reviewers choose an available workspace and repository, inspect shared
packages and history, and approve the exact content on screen. Repository authors
can also request shared context and attach an inspected receipt as a new draft.
PostgreSQL owns the shared record and permissions. Review perspectives change
prompts, not access.

## Configure the service

First provision identities and repository permissions using the
[authenticated review guide](authenticated-review.md). People must already have an
active human principal matching their provider's exact issuer and subject. Agents
use API credentials, including in the [terminal workbench](terminal-review.md),
and cannot obtain browser review sessions or approvals.

Apply each missing migration in order. An existing database at migration 001 needs
002 first; a database at 002 needs the following additive migration once:

```bash
docker exec -i conductor-local-postgres-1 psql -U conductor -d conductor \
  --set ON_ERROR_STOP=1 --single-transaction < migrations/003_browser_sessions.sql
```

Migration 003 adds login attempts and sessions without changing package content,
digests, approvals, ownership, or grants. Empty local databases receive every
ordered migration from the setup script. Existing volumes are retained.

Register a confidential OIDC client with authorization code flow, RS256 ID tokens,
S256 PKCE, and `client_secret_basic`. Register this exact callback using your real
HTTPS origin:

```text
https://conductor.example.invalid/api/v1/auth/callback
```

Keep the client secret in an operator-owned regular file outside the repository.
It is limited to 4,096 bytes; one editor-added line ending is allowed. Do not pass
the secret in a command argument. Configure the service with your real values:

```bash
export CONDUCTOR_AUTH_MODE=oidc
export CONDUCTOR_OIDC_ISSUER='https://identity.example.invalid'
export CONDUCTOR_OIDC_AUDIENCE='conductor-api'
export CONDUCTOR_PUBLIC_ORIGIN='https://conductor.example.invalid'
export CONDUCTOR_OIDC_CLIENT_ID='conductor-browser'
export CONDUCTOR_OIDC_CLIENT_SECRET_FILE='/private/path/conductor-client-secret'
CONDUCTOR_ADDR=127.0.0.1:8080 go run ./cmd/conductord
```

`DATABASE_URL` is required as in the other runbook. All three browser settings are
required together. The public origin has no subpath, query, or fragment. Serve the
built `apps/web/dist` files and proxy `/api/` to the API on that same HTTPS origin,
preserving the original Host header. Conductor does not trust forwarded origin
headers, install certificates, or configure the proxy. The ordinary Vite HTTP
development command remains the local-mode workflow.

The API audience applies to bearer access tokens. Browser login verifies the
browser client ID as its ID-token audience instead. These credentials are not
interchangeable. An issuer can support browser login while requiring additional
configuration to issue Conductor's API access-token profile.

## Use the workbench

Choose **Sign in**, complete provider authentication, then select a workspace and
managed repository. The page shows your stored principal and effective repository
capabilities. If a list is empty or truncated, ask an operator about the missing
access; changing a label or persona cannot add it.

Discover a package or enter its change ID. Inspect the revision and digest before
approval. Another engineer's edit produces a conflict and requires a new explicit
inspection. History and comparison remain available; historical approval is not
approval of the latest revision. The browser can attach an existing source receipt;
the CLI, terminal workbench, and API supply general package creation, editing, and
submission.

Changing the selected workspace, repository, or session clears the old inspection
and cancels pending requests, including collection drafts, receipts, and
confirmations. A denied or expired session also removes it, even when its HTTP
error body cannot be read. An old
tab's request secret cannot be used with a newly signed-in account. Approval never
reloads identity or content as part of its action.

**Sign out** revokes the Conductor session and clears its cookie. It does not sign
out of the identity provider, so the next login may reuse that provider's session.
Sessions last one hour, without silent refresh. Revocation or expiry blocks later
requests; an already authorized command can finish.

## Collect shared repository context

First follow the [durable context runbook](durable-context.md) to apply migration
004, enable the repository's read integration, configure its trusted worker, and
enable collection on the OIDC API. The browser reuses these existing commands and
does not need provider tokens. Remote collection is unavailable in local mode.
Browser sign-in still requires a provisioned human principal; agents use the
authenticated API, CLI, or terminal.

In **Shared context collections**, choose **Refresh collections** to discover
requests in the selected repository. The page holds at most 20 requests and shows
when more exist. **Load more collections** replaces it with the next bounded page;
refresh returns to the first page. Inspect a request to see its canonical source,
exact input, optional immutable receipt, and timestamped execution observation.

Authors can expand **Request repository context**, enter a full lowercase 40-hex
commit ID and 1–32 unique relative file paths, and inspect the visible idempotency
key before choosing **Request collection**. Files are read from the configured
repository at that commit. Branch names, abbreviated IDs, scripts, and arbitrary
URLs are not collection inputs. A recorded request confirms saved intent, even
when its worker is offline; it does not establish completed execution or coverage.

If a response is lost or uncertain, the form retains and locks the exact input and
key. Choose **Retry same request** to recover that request explicitly. The browser
does not retry automatically. After a confirmed request, **Start a new request**
creates a separate draft and key. Changing keys starts new work and is not recovery
for an unknown result.

Choose **Refresh inspected collection** to read later facts. The workbench does
not poll. Execution observations become stale after 30 seconds; missing,
unavailable, or unresolved progress stays explicit. Inspect each path's collected,
missing, unavailable, or truncated state. Even completed execution can leave gaps.
Collected source is not proof that a check passed or that a design was approved.

The currently authorized requester can choose **Request cancellation**, inspect
the displayed collection identity, then **Confirm cancellation request**. This
records intent. Only a timestamped `cancelled` execution observation confirms the
execution stopped. A receipt committed before cancellation remains available.
An uncertain cancellation result requires another collection inspection.

To attach source, first inspect the current package revision in the same selected
repository and inspect a collection with a receipt. Choose **Attach receipt to
inspected revision**. The confirmation displays the package ID, revision and
digest, plus the collection ID and receipt digest. **Confirm attachment** sends
those captured facts without refreshing source or package content. It creates a
new draft; previous approval does not apply. Read-only access, historical views,
and stale inspections cannot attach. After a conflict or unknown attachment
result, inspect both the latest package and the collection again before another
attachment or approval. The recovery warning identifies both records and provides
explicit inspection buttons. Clearing a selection or receiving a different command
response does not count as renewed inspection.

Version 2 context displays source metadata, coverage, and text alongside complete
JSON. A package's embedded receipt ID remains an unchecked reference until you
choose **Inspect linked collection** and the complete snapshot matches the scoped
server receipt. Unknown nested snapshot fields participate in that comparison.
Version 1 extensions retain their original meaning; attachment preserves unknown
outer package fields. The linkage records captured source, not independently
verified provider compatibility or a passing implementation check.

## Protocol and storage limits

The implementation uses [OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html),
[OIDC discovery](https://openid.net/specs/openid-connect-discovery-1_0.html), and
[PKCE](https://www.rfc-editor.org/rfc/rfc7636.html). It pins
[go-oidc v3.17.0](https://github.com/coreos/go-oidc/tree/v3.17.0),
[oauth2 v0.35.0](https://github.com/golang/oauth2/tree/v0.35.0), and
[go-jose v4.1.4](https://github.com/go-jose/go-jose/tree/v4.1.4) for Go 1.24.

Discovery must advertise code flow, RS256, and HTTPS authorization/token endpoints.
Omitted token authentication metadata uses the standard Basic default; an explicit
list must support Basic. PKCE always uses S256, including when metadata omits the
PKCE extension; an advertised list without S256 is rejected. Only `openid` scope
is requested. Redirects and automatic credential-style retries are rejected.

ID-token validation requires the exact issuer, one audience equal to the client
ID, subject, expiration, issued-at, matching nonce, and RS256 signature with a known
key ID. A supplied authorized party must equal the client ID. Not-before and
access-token hash are checked when supplied. Time checks use zero clock skew.
Provider tokens are discarded after this validation. Encrypted ID tokens, multiple
ID-token audiences, other signing algorithms, public clients, token refresh,
introspection, and federated logout are outside this slice.

Provider responses are limited to 256 KiB, ID tokens to 16 KiB, signing keys to 64,
and RSA keys to 2048–8192 bits. Each provider request has a five-second deadline.
Discovery/key transport uses the same bounded cache policy as the
[API verifier](authenticated-review.md): five-minute key expiry and a 30-second
minimum refresh interval, including failed refreshes. Expired keys are not reused
during an outage.

Pending login attempts expire after five minutes and are consumed once. Their
state and browser-binding secrets are hashed; the short-lived nonce and PKCE
verifier are retained until consumption or cleanup. Session cookie tokens are
hashed in PostgreSQL. Session cookies are Secure, HttpOnly, host-only, and
SameSite=Lax. Commands also require the configured Origin and session request
secret. Browser responses are not cached, and login failures omit provider errors
and credentials. Configure infrastructure logs to omit authentication callback
queries and credential headers too.

Admission is bounded to 4,096 active attempts, 10,000 sessions globally, and 20
sessions per principal. Reaching a limit rejects new admission without evicting
someone else's active session. Expired credentials stop working immediately;
physical rows are pruned on later login/session creation. There is no background
purge or claim that expiry already removed the stored row.

## Reproduce acceptance

Signed protocol and HTTP/PostgreSQL tests run with `CONDUCTOR_TEST_DATABASE_URL`.
The separate browser opt-in uses the production frontend build and Chromium:

```bash
npm --prefix apps/web ci
npm --prefix apps/web run build
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  CONDUCTOR_TEST_BROWSER=1 CONDUCTOR_BROWSER_PYTHON='/path/to/playwright/python' \
  go test -race ./tests/acceptance -run Browser -count=1 -v
```

Install the pinned Playwright version declared by the Python acceptance script in
that Python environment, and provide Chrome through `CONDUCTOR_CHROME` if it is not
at `/usr/bin/google-chrome`. The test owns isolated database schemas and temporary
TLS servers. Its synthetic issuer exercises signed code exchange; it does not
certify Entra ID, Okta, Keycloak, or production deployment. Missing opt-in is an
explicit skip; missing browser dependencies after opt-in are a failure.
