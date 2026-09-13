# Provider qualification plan

**Status: Implemented with local provider qualification.**

1. Inspect official Keycloak source and documentation, pin 26.7.3 source and actual
   Linux/amd64 container digest, and record native client/realm/TLS capabilities.
2. Start an isolated native HTTPS provider with imported synthetic identities and
   exact confidential client settings; keep private keys outside any repository.
3. Exercise the actual React workbench, shared HTTP service and PostgreSQL through
   Chromium with the pinned fixture certificate, using real Keycloak code exchanges.
4. Retain the strict API bearer boundary, verify real default provider tokens are
   rejected, and document the narrower browser compatibility result accurately.
5. Add deterministic setup/cleanup, clear dependency failures, a reproducible script,
   protocol/identity regression checks and an operator qualification guide.

This qualifies the selected local provider/version/profile. Entra ID, Okta, arbitrary
Keycloak configurations, federation, MFA, refresh, provider token storage and hosted
production deployment are not inferred from this evidence.
