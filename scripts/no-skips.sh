#!/usr/bin/env bash
# Fail when a package's tests did not all run: any test or subtest in the
# package, or in a package under it, skipped, or none of its tests passed.
# CI runs it on internal/bus, where nats-server is installed, so a skip means
# the proof did not run (code-restructure-plan.md, PR M2-1; aae-orc-p0zkz).
#
#   scripts/no-skips.sh <go-test-json-file> <import-path>
#
# Exit 0 when every test ran, 1 on a skip or no run, 2 on a usage or input
# error. The input must be go test -json output; anything else is an error,
# never a pass.
set -euo pipefail

json="${1:-}"
pkg="${2:-}"
if [[ -z "$json" || -z "$pkg" || $# -ne 2 ]]; then
  echo "usage: no-skips.sh <go-test-json-file> <import-path>" >&2
  exit 2
fi
command -v jq >/dev/null || { echo "no-skips: jq is required" >&2; exit 2; }
[[ -r "$json" ]] || { echo "no-skips: cannot read $json" >&2; exit 2; }

# Validate the whole stream first, so a parse error is exit 2, not a count.
jq -e . "$json" >/dev/null 2>&1 || { echo "no-skips: $json is not go test -json output" >&2; exit 2; }

mine='select(.Package == $p or (.Package | startswith($p + "/")))'
skipped="$(jq -r --arg p "$pkg" "$mine | select(.Action == \"skip\") | \"\(.Package) \(.Test // \"(no test files)\")\"" "$json")"
passed="$(jq -r --arg p "$pkg" "$mine | select(.Action == \"pass\" and .Test != null) | .Test" "$json")"

if [[ -n "$skipped" ]]; then
  echo "no-skips: tests in $pkg skipped, so they did not run:" >&2
  printf '%s\n' "$skipped" | sed 's/^/  /' >&2
  exit 1
fi
if [[ -z "$passed" ]]; then
  echo "no-skips: no test in $pkg passed; the package did not run" >&2
  exit 1
fi
echo "no-skips: every test in $pkg ran ($(printf '%s\n' "$passed" | wc -l | tr -d ' ') passed, none skipped)"
