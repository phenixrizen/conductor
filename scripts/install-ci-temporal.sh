#!/usr/bin/env bash
set -euo pipefail
ci_target=${1:?Usage: install-ci-temporal.sh ABSOLUTE_NEW_DIRECTORY}
[[ $ci_target = /* && ! -e $ci_target ]] || { echo 'Select a new absolute directory.' >&2; exit 1; }
ci_work=$(mktemp -d)
trap 'rm -rf -- "$ci_work"' EXIT
curl --fail --location --proto '=https' --tlsv1.2 --max-time 120 \
  --output "$ci_work/temporal.tar.gz" \
  https://github.com/temporalio/cli/releases/download/v1.8.3/temporal_cli_1.8.3_linux_amd64.tar.gz
printf '%s  %s\n' 6f0afac1e9ddea71f480c43a49f5db5167a244c21db923707f069a79bcabdfea "$ci_work/temporal.tar.gz" | sha256sum --check --status
mkdir "$ci_target"
tar -xzf "$ci_work/temporal.tar.gz" -C "$ci_target" temporal
"$ci_target/temporal" --version
