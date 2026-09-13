# Authenticated review setup

The API, CLI, and [terminal workbench](terminal-review.md) share review data with
authenticated workspace and repository permissions. [Browser sign-in](browser-sign-in.md)
adds interactive OpenID Connect login through the same command service.

## Prepare the database

New development databases receive all ordered migrations. For an existing database
initialized at migration 001, apply migration 002 once before running this version.
With the local Docker setup:

```bash
docker exec -i conductor-local-postgres-1 psql -U conductor -d conductor \
  --set ON_ERROR_STOP=1 --single-transaction < migrations/002_access.sql
```

The migration adds access records and nullable package ownership. Existing package
content, digests, authors, approvals, and audit records are preserved. Those packages
remain available only in local mode; they are not silently assigned to a workspace.
Migration 003 adds browser sessions; apply it once after 002 as described in the
[browser guide](browser-sign-in.md). Do not run an older API binary against a
database containing authenticated data.

## Configure identity and permissions

Choose an OpenID Connect issuer that can issue the supported API access tokens.
Configure its exact HTTPS issuer URL and the API resource audience. Conductor does
not automatically enroll a signed-in subject or trust token role/group claims.

Prepare an operator-owned JSON file, using the actual issuer and subject IDs from
your identity provider. This synthetic example gives an author and independent
reviewer access to one repository:

```json
{
  "principals": [
    {"id":"engineer","issuer":"https://identity.example.invalid","subject":"subject-1","kind":"human","active":true},
    {"id":"reviewer","issuer":"https://identity.example.invalid","subject":"subject-2","kind":"human","active":true}
  ],
  "workspaces": [{"id":"team","name":"Example team"}],
  "memberships": [
    {"workspaceId":"team","principalId":"engineer","active":true},
    {"workspaceId":"team","principalId":"reviewer","active":true}
  ],
  "repositories": [
    {"id":"service","workspaceId":"team","provider":"github","host":"github.com","providerId":"12345","name":"example/service"}
  ],
  "grants": [
    {"repositoryId":"service","principalId":"engineer","canRead":true,"canAuthor":true,"canApprove":false},
    {"repositoryId":"service","principalId":"reviewer","canRead":true,"canAuthor":false,"canApprove":true}
  ]
}
```

Provider is `github` or `gitlab`; the stable provider ID is distinct from the
repository name. Host normalization supports DNS hostnames and optional numeric
ports. It normalizes case and default HTTPS port 443; URLs, credentials, malformed
names, and IPv6 literals are rejected. These records are operator-provisioned
identities, not independent evidence of remote repository access.

Validate the file before applying it through a trusted database connection:

```bash
go run ./cmd/conductor-admin apply --file access.json --operator local-operator --check
DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  go run ./cmd/conductor-admin apply --file access.json --operator local-operator
```

`--check` validates syntax and values only; it does not verify database references
or provider access. Files are limited to 1 MiB and 500 total records. Unknown fields
are rejected. Apply upserts only the supplied records and retains an administration
audit in the same transaction. The operator label is declared audit metadata;
the database connection is the actual administrative authority. Keep it outside
developer/agent workloads and never distribute it as an API credential.

To revoke a membership, apply its record with `active:false`. To revoke repository
access, apply its grant with all capabilities false. Omitted records are unchanged;
omitted booleans in a supplied record are false. Identity bindings, principal kind,
and repository ownership are immutable. Revocation waits for already authorized
transactions to finish and blocks subsequent commands using the same token.

## Start the authenticated API

```bash
export DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable'
export CONDUCTOR_AUTH_MODE=oidc
export CONDUCTOR_OIDC_ISSUER='https://identity.example.invalid'
export CONDUCTOR_OIDC_AUDIENCE='conductor-api'
CONDUCTOR_ADDR=127.0.0.1:8080 go run ./cmd/conductord
```

Use HTTPS for shared client connections, for example through a configured TLS proxy
to this loopback listener. The server does not install certificates or configure
your network. Local mode must be selected explicitly with `CONDUCTOR_AUTH_MODE=local`
and cannot listen on a wildcard or remote address or include OIDC settings.

## Review through the CLI

Obtain an API access token through your configured identity provider. Conductor
does not yet acquire or refresh it. Keep the token in a protected regular file;
do not paste it into a command argument or commit it to Git.

```bash
export CONDUCTOR_URL='https://conductor.example.invalid'
export CONDUCTOR_TOKEN_FILE='/private/path/conductor-access-token'
go run ./cmd/conductor session
go run ./cmd/conductor repositories --workspace team
go run ./cmd/conductor create --workspace team --repository-id service --file package.json
go run ./cmd/conductor list --workspace team
```

`session` lists the principal and memberships; `repositories` lists readable
repositories with effective capabilities. Both return at most 100 entries and an
explicit `truncated` flag. You can set `CONDUCTOR_WORKSPACE` and
`CONDUCTOR_REPOSITORY_ID` instead of repeating the flags. Repository selection is
required for creation and optionally narrows every other package operation.
The existing `--repository` filter still matches content labels and grants no rights.

Use `show`, `history`, and `events` to inspect shared work. Submission and approval
still require the inspected revision, and approval also requires its digest:

```bash
go run ./cmd/conductor submit --workspace team --revision 1 CHG-...
# In the independent reviewer's session, using that reviewer's token:
go run ./cmd/conductor show --workspace team CHG-...
go run ./cmd/conductor approve --workspace team --revision 1 --digest '<inspected digest>' CHG-...
```

Never use `--actor` with a token. `CONDUCTOR_TOKEN` is an alternative to the token
file; setting both is an error. Credentials are limited to 16 KiB. The client
rejects redirects and plaintext remote URLs before sending credentials. HTTP is
allowed only for local loopback testing. Context collection remains local and does
not read the credential settings.

For interactive review, select both a workspace and a canonical repository:

```bash
go run ./cmd/conductor tui --workspace team --repository-id service
go run ./cmd/conductor tui --workspace team --repository-id service CHG-...
```

The terminal discovers the principal and selected repository's capabilities before
loading work. Its credential and scope remain fixed until exit. `r` explicitly
rechecks access and loads current work; it does not acquire or refresh a token.
An authentication or permission failure clears inspection and imported drafts.
See the [terminal guide](terminal-review.md) for confirmation, recovery, and the
signed-issuer real PTY acceptance command. This interface uses the existing API
and permissions without adding a migration or requiring browser login settings.

## Supported token profile and limitations

The verifier follows [RFC 9068](https://www.rfc-editor.org/rfc/rfc9068.html) and
[OIDC discovery](https://openid.net/specs/openid-connect-discovery-1_0.html), using
[go-jose v4.1.4](https://github.com/go-jose/go-jose/releases/tag/v4.1.4) on Go 1.24.
It requires RS256, an access-token type (`at+jwt`, case-insensitive, also accepting
the `application/` prefix), a unique nonempty signing-key ID, and signed issuer,
audience, subject, expiration, issued-at, client ID, and token ID claims. A present
not-before claim is checked. The API uses zero clock skew. Only issuer and subject
identify a stored principal; claims do not grant Conductor capabilities.

Discovery/JWKS responses and requests are bounded and cannot redirect. Keys cache
for five minutes; unknown-key refresh is throttled to once per 30 seconds, including
failed attempts. Unknown keys may temporarily fail during rotation. Replacing key
material under the same key ID is recognized at cache expiry. An expired key cache
with an unavailable refresh fails closed. RSA keys must be 2048–8192 bits.

ID tokens, opaque/encrypted tokens, arbitrary vendor JWT profiles, token
refresh/introspection, and provider-side token revocation before expiry are not
supported by this API adapter. The separate browser adapter validates ID tokens
for login and creates Conductor sessions. Stored principal/membership/capability revocation
is enforced independently on subsequent requests. Tests use signed synthetic OIDC
fixtures and PostgreSQL, including TLS and key rotation; they do not certify Entra
ID, Okta, Keycloak, or any production issuer configuration.

Run the integrated acceptance with an explicitly selected test database:

```bash
CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable' \
  go test -race ./tests/acceptance -run Authenticated -count=1 -v
```
