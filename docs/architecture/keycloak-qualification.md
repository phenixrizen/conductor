# Keycloak browser qualification research

**Implemented with local provider qualification, 2026-09-13.** The selected profile
exercises browser authentication with Keycloak 26.7.3. Strict bearer API token
validation remains independent and unchanged.

| Component | Inspected and tested pin |
|---|---|
| Keycloak release/source | 26.7.3; `6d238b6558037085cc25c915893c3d301a80243e` |
| Official manifest | `quay.io/keycloak/keycloak@sha256:ff4257d0d64efbe99ed1ddfaf07765cc3c36dc7518bf8324d41961327f441c54` |
| Linux/amd64 image ID | `sha256:946db99597ac833a42348d8da2dc6e28be656211758cdb5e84d2e415e158191f` |
| Actual server runtime | OpenJDK 21.0.12.1+1-LTS; Quarkus 3.33.3.1 |
| Browser harness | Playwright 1.62.0; Google Chrome 142.0.7444.175 |

The [official download page](https://www.keycloak.org/downloads) identified 26.7.3,
and the [pinned source](https://github.com/keycloak/keycloak/tree/6d238b6558037085cc25c915893c3d301a80243e)
was inspected before adding the integration test. Relevant upstream contracts:

- [Native TLS configuration](https://www.keycloak.org/server/enabletls) accepts PEM
  certificate/key paths. [Hostname configuration](https://www.keycloak.org/server/hostname)
  fixes the issuer's externally visible HTTPS origin.
- [Realm startup import](https://www.keycloak.org/server/importExport) loads
  `<realm>-realm.json` from the container's `data/import` directory. The fixture uses
  actual client/user representations, exact callback and S256 client attributes.
- [Client representation source](https://github.com/keycloak/keycloak/blob/6d238b6558037085cc25c915893c3d301a80243e/core/src/main/java/org/keycloak/representations/idm/ClientRepresentation.java)
  defines confidential/standard-flow, direct-grant and redirect settings.
- [OIDC login protocol](https://github.com/keycloak/keycloak/blob/6d238b6558037085cc25c915893c3d301a80243e/services/src/main/java/org/keycloak/protocol/oidc/OIDCLoginProtocol.java)
  binds nonce/PKCE into the authorization code and returns issuer/session extensions.
  [Authorization-code grant](https://github.com/keycloak/keycloak/blob/6d238b6558037085cc25c915893c3d301a80243e/services/src/main/java/org/keycloak/protocol/oidc/grants/AuthorizationCodeGrantType.java)
  checks client, redirect, code lifetime and PKCE before token issuance.
- [Container documentation](https://www.keycloak.org/server/containers) distinguishes
  development mode from production setup. This is an owned development qualification,
  not a shipped or deployed identity service.

The runtime token response is inspected in memory: selected default access tokens
use `JWT` rather than RFC9068 `at+jwt`, and Conductor rejects them as bearer tokens.
A valid OIDC ID token establishes browser identity only; PostgreSQL then resolves
its active principal, membership and repository permissions. Neither provider login
nor ticket/runtime status is authority to approve a package.

Actual HTTPS protocol tests and browser actions are described in the
[qualification runbook](../operations/keycloak-qualification.md) and
[feature contract](../../specs/017-provider-qualification/spec.md). Other Keycloak
settings or versions, MFA/federation and hosted vendors require separate evidence.
