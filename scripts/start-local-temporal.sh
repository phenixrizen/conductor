#!/usr/bin/env bash
# Keep development workflow history across process restarts, outside the checkout.
set -euo pipefail

temporal_bin="${CONDUCTOR_TEMPORAL_CLI:-temporal}"
version="$("$temporal_bin" --version)"
case "$version" in
  'temporal version 1.8.3 (Server 1.31.2,'*) ;;
  *) echo 'This profile requires the verified Temporal CLI 1.8.3 / server 1.31.2.' >&2; exit 1 ;;
esac
state_dir="${CONDUCTOR_TEMPORAL_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/conductor/temporal}"
mkdir -p -- "$state_dir"
chmod 700 -- "$state_dir"
exec "$temporal_bin" server start-dev --ip 127.0.0.1 --port 7233 --headless \
  --namespace conductor --db-filename "$state_dir/temporal.sqlite"
