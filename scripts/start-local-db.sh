#!/usr/bin/env bash
# Pipe Compose input so Docker Snap can run with a checkout under /mnt. Schema
# changes use the same checked operator as release deployments, on the Go host.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/deploy/local/compose.yaml"
project_name="${CONDUCTOR_LOCAL_PROJECT:-conductor-local}"
export CONDUCTOR_POSTGRES_PORT="${CONDUCTOR_POSTGRES_PORT:-5432}"
if [[ ! "$CONDUCTOR_POSTGRES_PORT" =~ ^[1-9][0-9]{0,4}$ ]] || (( CONDUCTOR_POSTGRES_PORT > 65535 )); then
  echo 'CONDUCTOR_POSTGRES_PORT must be an integer from 1 to 65535.' >&2
  exit 1
fi

docker info >/dev/null
docker compose --project-name "$project_name" --file - up --detach --wait < "$compose_file"

# Bind this helper to the Compose database it just started, even when the caller
# has a different DATABASE_URL. Existing untracked schemas must fail closed;
# only an explicitly inspected operator baseline may establish their history.
cd -- "$repo_root"
local_database_url="postgres://conductor:conductor@127.0.0.1:${CONDUCTOR_POSTGRES_PORT}/conductor?sslmode=disable"
if ! DATABASE_URL="$local_database_url" \
  go run ./cmd/conductor-db migrate --directory "$repo_root/migrations" --operator local-development; then
  cat >&2 <<'MESSAGE'
Local database migration success was not confirmed. No API was started.
For an existing untracked database, back it up and inspect its complete schema
against the original migrations before using make db-migrate with the verified
already-applied BASELINE and an OPERATOR audit label. Explicitly select this
helper's local database for that command, even if your shell has DATABASE_URL set:
MESSAGE
  printf '  DATABASE_URL=%q make db-migrate BASELINE=<verified-count> OPERATOR=<operator-label>\n' "$local_database_url" >&2
  cat >&2 <<'MESSAGE'
Then retry make run. Never guess a baseline, replace checksums, or reset the volume.
For a custom local database port, pass the same CONDUCTOR_POSTGRES_PORT setting.
See docs/operations/local-development.md and docs/operations/release.md.
MESSAGE
  exit 1
fi
echo "PostgreSQL is ready on 127.0.0.1:${CONDUCTOR_POSTGRES_PORT}; migration checksums verified and existing data retained."
