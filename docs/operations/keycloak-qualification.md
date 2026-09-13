# Qualify browser sign-in with Keycloak

Conductor's browser sign-in is exercised against an actual Keycloak 26.7.3 server,
using native HTTPS, discovery, login forms, signed ID tokens and confidential
S256 authorization-code exchange. The React workbench and PostgreSQL record real
shared author/reviewer actions. This qualification is separate from synthetic
protocol tests and from deploying an identity provider for a real workspace.

The selected default Keycloak access token has JOSE `typ: JWT`; Conductor's API
requires the documented RFC9068 `at+jwt` profile and mandatory claims. The test
verifies that default access tokens and ID tokens are rejected by bearer endpoints.
Successful Keycloak browser sign-in does not establish terminal/API token compatibility.
No token validator was relaxed for this qualification.

## Reproduce the qualification

Use Linux/amd64, Docker, the pinned Go toolchain, Node/npm, a PostgreSQL database
where the test can create/drop its own schema, Python 3.12+ with `playwright==1.62.0`,
and Chromium. The verified local browser is Google Chrome 142.0.7444.175. Select a
Chrome executable with `CONDUCTOR_CHROME` if it is not `/usr/bin/google-chrome`.

For example, prepare a dedicated Python environment and run the checked-in script:

```bash
uv venv --python 3.12 /tmp/conductor-browser-qualification
uv pip install --python /tmp/conductor-browser-qualification/bin/python playwright==1.62.0
export CONDUCTOR_BROWSER_PYTHON=/tmp/conductor-browser-qualification/bin/python
export CONDUCTOR_TEST_DATABASE_URL='postgres://conductor:conductor@127.0.0.1:5432/conductor?sslmode=disable'
./scripts/qualify-keycloak.sh
```

The script pulls the exact manifest digest, installs the reviewed frontend lockfile,
runs UI typecheck/build and the race-enabled provider acceptance. Docker access must
already be available. With an already provisioned Docker group on Snap installations,
run the script through `sg docker -c './scripts/qualify-keycloak.sh'` if the current
shell has not inherited that group. No sudo prompt or user service restart occurs.
The image remains cached; the test's container, network, schema and temporary files
are removed even after an assertion fails.

To reuse an installed image and built UI directly:

```bash
CONDUCTOR_TEST_KEYCLOAK=1 go test -race ./tests/acceptance \
  -run '^TestKeycloakBrowserQualification$' -count=1 -v
```

An unset opt-in produces an explicit skip. Missing Docker/image, database permission,
Python/Playwright, browser or built assets after opt-in is a failure. The test checks
the exact image ID and architecture. It does not accept an arbitrary user-supplied
provider endpoint, existing realm or cloud tenant.

The `native-identity-provider` job in [release verification](../../.github/workflows/verify.yml)
runs this same script on an isolated Ubuntu runner for pull requests and pushes to
`main`. It uses the existing immutable action pins, PostgreSQL image and pinned
Playwright Chromium installation. Synthetic realm/client credentials require no
hosted secrets. A successful run retains only the synthetic workbench screenshot
for seven days; provider tokens, signing keys and callback URLs are not uploaded.
A configured job is distinct from an observed hosted CI pass.

## What the test establishes

The browser creates and submits shared work as one actual Keycloak user, inspects it
as another, rejects a stale approval after a concurrent edit, and approves the exact
second revision. PostgreSQL then confirms the author, independent reviewer, revision
and digest. Other real provider identities demonstrate agent browser-session denial and
workspace isolation. No identity or role claim creates Conductor grants.

Conductor sign-out revokes its stored session, including a replayed cookie. Keycloak
SSO still exists until the actual provider logout flow is used. Switching accounts
in another tab rejects the old tab's captured session/CSRF and clears inspection
before explicit recovery. Tampered nonce and PKCE requests fail closed; a fresh
sign-in recovers. The native callback's `iss` and `session_state` extensions are
accepted without weakening strict command inputs.

The harness observes token exchanges in memory to check confidential client auth
and PKCE, and uses the actual returned access/ID tokens to prove bearer rejection.
It never writes those tokens or provider signing keys to the repository or logs.
The screenshot defaults to `/tmp/conductor-keycloak-workbench.png`; set
`CONDUCTOR_KEYCLOAK_SCREENSHOT` to a chosen synthetic acceptance artifact path.

Keycloak uses its development process and container-local H2 storage for this owned
fixture. Its internal plaintext listener is not forwarded or published; all tested
traffic reaches native HTTPS. A loopback TCP forwarder carries encrypted bytes into
the internal Docker network without terminating TLS. The Conductor Go client trusts
only the generated certificate. Chromium uses a per-test SPKI exception for that
exact generated key; it does not globally ignore certificate errors. These trust
settings belong only to the harness.

A small test-only launcher applies and checks Linux `no_new_privs` before starting
Keycloak. It avoids Snap Docker's initial executable-transition failure while
retaining privilege restrictions, the unprivileged image user and dropped capabilities.
No host directories or Docker socket are mounted into the provider.

## Configure a real browser client separately

A real deployment needs a confidential OpenID Connect client with authorization-code
flow enabled, RS256 ID tokens, S256 PKCE and `client_secret_basic`; register only the
exact HTTPS `/api/v1/auth/callback` URL. Disable implicit/direct-grant flows unless a
separate integration explicitly requires them. Configure Conductor's issuer/client
settings and provision each verified issuer/subject pair and repository grants through
the trusted operator command described in [browser sign-in](browser-sign-in.md).

Do not reuse the synthetic realm, passwords, self-signed trust, H2 development server
or test launcher as a production identity deployment. This qualification does not
cover federation, MFA, provider availability, key rotation, hosted identity vendors,
API token issuance, or production deployment. The exact inspected upstream and image
pins are in [provider research](../architecture/keycloak-qualification.md).
