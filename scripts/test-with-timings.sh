#!/usr/bin/env bash
# Keep Go's failure status through the reporting pipeline.
set -euo pipefail
test_report_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test_report_mode=${1:-}
test_report_flags=()
case "$test_report_mode" in
  normal) ;;
  race) test_report_flags=(-race) ;;
  *) echo 'Usage: scripts/test-with-timings.sh normal|race [Go test arguments]' >&2; exit 2 ;;
esac
shift
if [[ $# == 0 ]]; then set -- ./...; fi
go test -json "${test_report_flags[@]}" "$@" | python3 "$test_report_root/go_test_report.py" "$test_report_mode"
