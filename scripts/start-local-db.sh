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

# Bootstrap an empty local database atomically. Existing review history is never
# reset; later migrations must be applied deliberately, not on every startup.
initialized="$(docker exec "$container_id" psql -U conductor -d conductor -Atc "SELECT to_regclass('public.changes') IS NOT NULL")"
if [[ "$initialized" == f ]]; then
  docker exec -i "$container_id" psql -U conductor -d conductor \
    --set ON_ERROR_STOP=1 --single-transaction < "$repo_root/migrations/001_work_packages.sql"
fi
echo "PostgreSQL is ready on 127.0.0.1:5432. Existing local data is retained."
