#!/usr/bin/env bash
# Build actual pinned upstream tools without copying host credentials or caches.
set -euo pipefail
task_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
task_image=${CONDUCTOR_DESIGN_BASE_IMAGE:-}
if [[ ! $task_image =~ ^(sha256:[0-9a-f]{64}|[a-zA-Z0-9./:_-]+@sha256:[0-9a-f]{64})$ ]]; then
  echo 'Set CONDUCTOR_DESIGN_BASE_IMAGE to the inspected Conductor worker image digest.' >&2
  exit 1
fi
task_context=$(mktemp -d)
task_base="conductor-design-base:build-${task_context##*/}"
trap 'docker image rm "$task_base" >/dev/null 2>&1 || true; rm -rf -- "$task_context"' EXIT
# BuildKit does not resolve bare local image IDs in FROM. This owned temporary
# tag is created from the inspected digest and removed after the build.
task_digest=$(docker image inspect "$task_image" --format '{{.Id}}')
docker image tag "$task_digest" "$task_base"
curl --fail --location --proto '=https' --tlsv1.2 --max-time 120 --max-filesize 33554432 \
  --output "$task_context/source.tar.gz" \
  https://github.com/github/spec-kit/archive/96c9bd657bfd5de0d651a6165084932b7304ac99.tar.gz
printf '%s  %s\n' edb4638974b539550849cf83672746d08d6071601605a46b6b8086563789a18e "$task_context/source.tar.gz" | sha256sum --check --status
mkdir "$task_context/source"
tar -xzf "$task_context/source.tar.gz" --strip-components=1 -C "$task_context/source"
uv build --wheel --directory "$task_context/source" --out-dir "$task_context"
printf '%s  %s\n' 7d80f856bda6022556037a35b8c8e1c162e8419b1ef4e380487665ecad18f37a "$task_context/specify_cli-1.0.6-py3-none-any.whl" | sha256sum --check --status
cd "$task_root"
CGO_ENABLED=0 GOOS=linux go build -trimpath -o "$task_context/conductor-design-tools" ./cmd/conductor-design-tools
cp deploy/design-tools/Dockerfile deploy/design-tools/package.json deploy/design-tools/package-lock.json deploy/design-tools/requirements.lock "$task_context/"
tar -C "$task_context" -cf - Dockerfile package.json package-lock.json requirements.lock specify_cli-1.0.6-py3-none-any.whl conductor-design-tools \
  | docker build --build-arg "WORKER_IMAGE=$task_base" --build-arg "WORKER_DIGEST=$task_digest" --tag conductor-design-tools:1.0.6-0.13.0 -
docker image inspect conductor-design-tools:1.0.6-0.13.0 --format '{{.Id}}'
