#!/usr/bin/env bash
# Pipe local inputs so Docker Snap can run even when this checkout is under /mnt.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
compose_file="$repo_root/deploy/local/compose.yaml"

docker info >/dev/null
docker compose --project-name conductor-local --file - up --detach --wait < "$compose_file"
container_id="$(docker compose --project-name conductor-local --file - ps --quiet postgres < "$compose_file")"
if [[ -z "$container_id" ]]; then
  echo "The local PostgreSQL container was not found." >&2
  exit 1
fi

# Bootstrap an empty local database with every ordered migration atomically.
# Existing databases must be upgraded deliberately; never reset review history.
initialized="$(docker exec "$container_id" psql -U conductor -d conductor -Atc "SELECT to_regclass('public.changes') IS NOT NULL")"
if [[ "$initialized" == f ]]; then
  cat "$repo_root"/migrations/*.sql | docker exec -i "$container_id" psql -U conductor -d conductor \
    --set ON_ERROR_STOP=1 --single-transaction
fi
access_schema="$(docker exec "$container_id" psql -U conductor -d conductor -Atc "SELECT to_regclass('public.access_principals') IS NOT NULL")"
if [[ "$access_schema" != t ]]; then
  echo "Existing database needs migration 002 before this API can run. Follow docs/operations/authenticated-review.md; existing history is retained." >&2
  exit 1
fi
browser_schema="$(docker exec "$container_id" psql -U conductor -d conductor -Atc "SELECT to_regclass('public.browser_sessions') IS NOT NULL")"
if [[ "$browser_schema" != t ]]; then
  echo "Existing database needs migration 003 for browser sessions. Follow docs/operations/browser-sign-in.md; existing history is retained." >&2
  exit 1
fi
context_schema="$(docker exec "$container_id" psql -U conductor -d conductor -Atc "SELECT to_regclass('public.context_runtime_bindings') IS NOT NULL")"
if [[ "$context_schema" != t ]]; then
  echo "Existing database needs migration 004 for background context collection. Follow docs/operations/durable-context.md; existing history is retained." >&2
  exit 1
fi
echo "PostgreSQL is ready on 127.0.0.1:5432. Existing local data is retained."
