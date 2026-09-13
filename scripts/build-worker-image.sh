#!/usr/bin/env bash
set -euo pipefail

# Pipe a dedicated context to support Snap Docker and checkouts outside $HOME.
task_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
task_context=$(mktemp -d)
trap 'rm -rf "$task_context"' EXIT
cd "$task_root"
CGO_ENABLED=0 GOOS=linux go build -trimpath -o "$task_context/conductor-sandbox" ./cmd/conductor-sandbox
cp deploy/worker/Dockerfile deploy/worker/package.json deploy/worker/package-lock.json "$task_context/"
tar -C "$task_context" -cf - . | docker build --tag conductor-worker:local -
docker image inspect conductor-worker:local --format '{{.Id}}'
