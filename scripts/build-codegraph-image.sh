#!/usr/bin/env bash
# Build the reviewed CodeGraph release without installing agent configuration.
# Pipe a compressed build context so Docker Snap can use checkouts outside HOME.
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
archive=codegraph-linux-x64.tar.gz
expected=de3391f79ed42622d937e6cd5b7642a7ea8bb7d1473607e80b879ba73ef216b0
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'The verified CodeGraph profile currently requires Linux x86_64.' >&2
  exit 1
fi
curl --fail --location --proto '=https' --tlsv1.2 --output "$work/$archive" "https://github.com/colbymchenry/codegraph/releases/download/v1.6.0/$archive"
printf '%s  %s\n' "$expected" "$work/$archive" | sha256sum --check --status
tar -xzf "$work/$archive" -C "$work"
cp "$root/integrations/codegraph/Dockerfile" "$root/integrations/codegraph/extract.cjs" "$work/"
cc -static -O2 -Wall -Wextra -Werror -o "$work/launcher" "$root/integrations/codegraph/launcher.c"
tar -czf "$work/context.tar.gz" -C "$work" Dockerfile extract.cjs launcher codegraph-linux-x64
docker build --tag conductor-codegraph:1.6.0 - < "$work/context.tar.gz"
docker image inspect conductor-codegraph:1.6.0 --format '{{.Id}}'
