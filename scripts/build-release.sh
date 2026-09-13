#!/usr/bin/env bash
# Build only the selected committed tree. Uncommitted credentials and local state
# cannot enter the archive; SOURCE_DATE_EPOCH fixes archive metadata.
set -euo pipefail
ops_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ops_output=${1:-}
if [[ $# != 1 || $ops_output != /* || -e $ops_output || -L $ops_output ]]; then
  echo 'Usage: scripts/build-release.sh /absolute/new-release.tar.gz' >&2; exit 1
fi
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'The verified release target is Linux x86_64.' >&2; exit 1
fi
ops_work=$(mktemp -d)
ops_archive=$(mktemp "$(dirname -- "$ops_output")/.conductor-release-XXXXXXXX")
trap 'rm -rf -- "$ops_work"; rm -f -- "$ops_archive"' EXIT
ops_revision=$(git -C "$ops_root" rev-parse HEAD)
ops_epoch=$(git -C "$ops_root" show -s --format=%ct HEAD)
mkdir "$ops_work/source" "$ops_work/conductor"
git -C "$ops_root" archive "$ops_revision" | tar -xf - -C "$ops_work/source"
cd "$ops_work/source"
if [[ $(go env GOVERSION) != go1.26.8 || $(node --version) != v22.14.0 ]]; then
  echo 'Use the tested Go 1.26.8 toolchain and Node.js 22.14.0.' >&2; exit 1
fi
export SOURCE_DATE_EPOCH="$ops_epoch"
mkdir "$ops_work/conductor/bin"
for ops_command in cmd/*; do
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags=-buildid= -o "$ops_work/conductor/bin/${ops_command##*/}" "./$ops_command"
done
npm --prefix apps/web ci --ignore-scripts --no-audit --no-fund
npm --prefix apps/web run build
cp -a apps/web/dist "$ops_work/conductor/web"
cp -a migrations specs api deploy/release "$ops_work/conductor/"
cp README.md AGENTS.md go.mod go.sum "$ops_work/conductor/"
cp -a docs "$ops_work/conductor/"
go list -m all > "$ops_work/conductor/dependencies.txt"
printf '{"commit":"%s","sourceDateEpoch":%s,"go":"1.26.8","node":"22.14.0","target":"linux/amd64"}\n' \
  "$ops_revision" "$ops_epoch" > "$ops_work/conductor/build.json"
cd "$ops_work/conductor"
find . -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > SHA256SUMS
cd "$ops_work"
tar --sort=name --mtime="@$ops_epoch" --owner=0 --group=0 --numeric-owner -cf - conductor | gzip -n > "$ops_archive"
# Exclusive output publication preserves an existing archive even under races.
ln "$ops_archive" "$ops_output"
sha256sum "$ops_output"
