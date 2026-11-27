#!/usr/bin/env bash
# Print how many tests each package skipped in a go test -v log. Information
# only: no threshold, and the exit status is 0 whatever the log holds, so a
# step that runs it can never fail a job (marvel#646; the gate that fails on a
# skip is scripts/no-skips.sh, and the one that fails on a missing tool is
# internal/requiretool).
#
#   scripts/skip-counts.sh <go-test-log>
#
# A skip is a "--- SKIP:" line, at any depth, so a skipped subtest counts with
# its parent. A package's tests print before its "ok" or "FAIL" result line, so
# the count is taken when that line arrives.
set -euo pipefail

log="${1:-}"
if [[ -z "$log" || ! -r "$log" ]]; then
  echo "skip-counts: cannot read '${log}'; no counts printed" >&2
  exit 0
fi

awk '
  /^[[:space:]]*--- SKIP:/ { n++; next }
  /^(ok|FAIL)[[:space:]]+[^[:space:]]+/ {
    if (n > 0) { printf "%s: %d skipped\n", $2, n; total += n }
    n = 0
    next
  }
  END { printf "total: %d skipped\n", total }
' "$log"
