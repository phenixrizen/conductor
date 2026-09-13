#!/usr/bin/env bash
# Run only against explicitly selected test dependencies. Fixtures own temporary
# schemas, Git repositories, Docker tasks and Temporal processes; no live writes.
set -euo pipefail
: "${CONDUCTOR_TEST_DATABASE_URL:?set the explicit test PostgreSQL URL}"
: "${CONDUCTOR_TEST_WORKER_IMAGE:?set an inspected immutable worker image digest}"
: "${CONDUCTOR_CODEGRAPH_IMAGE:?set an inspected immutable native CodeGraph image digest}"
conductor_release_root=$(git rev-parse --show-toplevel)
cd "$conductor_release_root"
exec env CONDUCTOR_TEST_RELEASE=1 CONDUCTOR_TEST_EXECUTION=1 \
  CONDUCTOR_TEST_CODEGRAPH=1 CONDUCTOR_TEST_TEMPORAL=1 \
  go test -race ./tests/acceptance \
  -run '^TestFullReleaseCrossRepositoryWorkflow$' -count=1 -v
