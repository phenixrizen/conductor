#!/usr/bin/env bash
# Runs only synthetic, test-owned Keycloak resources and an isolated PG schema.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
: "${CONDUCTOR_TEST_DATABASE_URL:?Set the PostgreSQL URL whose isolated test schema may be created}"
: "${CONDUCTOR_BROWSER_PYTHON:?Select Python with playwright==1.62.0 installed}"
command -v docker >/dev/null
command -v go >/dev/null
command -v npm >/dev/null
"$CONDUCTOR_BROWSER_PYTHON" -c 'import importlib.metadata; assert importlib.metadata.version("playwright") == "1.62.0", "playwright==1.62.0 is required"'
docker pull --platform linux/amd64 quay.io/keycloak/keycloak@sha256:ff4257d0d64efbe99ed1ddfaf07765cc3c36dc7518bf8324d41961327f441c54
npm --prefix apps/web ci
npm --prefix apps/web run typecheck
npm --prefix apps/web run build
CONDUCTOR_TEST_KEYCLOAK=1 go test -race ./tests/acceptance -run '^TestKeycloakBrowserQualification$' -count=1 -v
