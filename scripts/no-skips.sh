#!/usr/bin/env bash
# Fail when a package's tests did not all run and pass: any test or subtest in
# the package, or in a package under it, skipped or failed, none of its tests
# passed, or a package that started never reported its own result. go test
# -json writes each package's pass or fail last, so a stream cut off before it
# is a run that never finished. CI runs it as the last command of a step,
# after go test -json, so its status is the step's, and a skip, a failure, an
# unfinished package or a run of nothing fails
# the job even where the shell would not stop at go test's own exit status
# (set +e, shell: bash {0}). The Linux job gates internal/bus, where
# nats-server is installed, and the macOS job the restructure packages
# (code-restructure-plan.md, PR M2-1; aae-orc-p0zkz, aae-orc-xe1pw).
#
#   scripts/no-skips.sh <go-test-json-file> <import-path>...
#
# Exit 0 when every test in each package ran and passed, 1 on a skip, a
# failure, a package with no passing test or a package that never finished,
# 2 on a usage or input error. The input must be go test -json output;
# anything else is an error, never a pass.
set -euo pipefail

usage() {
  echo "usage: no-skips.sh <go-test-json-file> <import-path>..." >&2
  exit 2
}

json="${1:-}"
[[ -n "$json" && $# -ge 2 ]] || usage
shift
for pkg in "$@"; do
  [[ -n "$pkg" ]] || usage
done
command -v jq >/dev/null || { echo "no-skips: jq is required" >&2; exit 2; }
[[ -r "$json" ]] || { echo "no-skips: cannot read $json" >&2; exit 2; }

# Validate the whole stream first, so a parse error is exit 2, not a count.
jq -e . "$json" >/dev/null 2>&1 || { echo "no-skips: $json is not go test -json output" >&2; exit 2; }

# Tests a package skips by design are named in no-skips.allow beside this
# script, one "<import path> <TestName>" per line, exact names and no wildcard
# (aae-orc-1wz40). A skip on that list is not a failure; any other skip is. A
# listed test that did not skip is reported, so the list cannot go stale unseen,
# but that report does not fail the run.
allow_file="$(dirname "${BASH_SOURCE[0]}")/no-skips.allow"
allowed='[]'
if [[ -r "$allow_file" ]]; then
  allowed="$(jq -Rn '[inputs | gsub("^\\s+|\\s+$"; "") | select(length > 0 and (startswith("#") | not)) | gsub("\\s+"; " ")]' < "$allow_file")"
fi

# Build events carry ImportPath, not Package, so a missing Package reads as "".
mine='select((.Package // "") as $q | $q == $p or ($q | startswith($p + "/")))'
rc=0
# The most lines the gate prints of a hung package's output after its running-tests block (the goroutine dump).
hang_cap=40
for pkg in "$@"; do
  bad=0
  skipped="$(jq -r --arg p "$pkg" --argjson allowed "$allowed" "$mine | select(.Action == \"skip\") | select(((.Package // \"\") + \" \" + (.Test // \"\")) as \$k | (\$allowed | index(\$k)) | not) | \"\(.Package) \(.Test // \"(no test files)\")\"" "$json")"
  # A listed test for this package that never skipped in this run.
  idle="$(jq -rn --arg p "$pkg" --argjson allowed "$allowed" --slurpfile ev "$json" '$allowed[] | select(startswith($p + " ")) | . as $k | select(([$ev[] | select(.Action == "skip") | (.Package // "") + " " + (.Test // "")] | index($k)) | not) | ltrimstr($p + " ")')"
  if [[ -n "$idle" ]]; then
    echo "no-skips: tests on the allow list for $pkg that did not skip in this run (the list may be stale):" >&2
    printf '%s\n' "$idle" | sed 's/^/  /' >&2
  fi
  failed="$(jq -r --arg p "$pkg" "$mine | select(.Action == \"fail\") | \"\(.Package) \(.Test // (if .FailedBuild then \"(failed to build)\" else \"(package failed)\" end))\"" "$json")"
  passed="$(jq -r --arg p "$pkg" "$mine | select(.Action == \"pass\" and .Test != null) | .Test" "$json")"
  # Every package with an event needs its own package-level pass, fail or
  # skip. Build events carry no Package and are left out.
  unfinished="$(jq -rs --arg p "$pkg" "[.[] | $mine | select((.Package // \"\") != \"\")] as \$e
    | ([\$e[] | .Package] | unique) - ([\$e[] | select(.Test == null and (.Action == \"pass\" or .Action == \"fail\" or .Action == \"skip\")) | .Package] | unique)
    | .[]" "$json")"

  if [[ -n "$skipped" ]]; then
    echo "no-skips: tests in $pkg skipped, so they did not run:" >&2
    printf '%s\n' "$skipped" | sed 's/^/  /' >&2
    bad=1
  fi
  if [[ -n "$failed" ]]; then
    echo "no-skips: tests in $pkg failed:" >&2
    printf '%s\n' "$failed" | sed 's/^/  /' >&2
    # The step sends go test's output to the stream, so print what the failed
    # tests and packages wrote, and the compiler output of a failed build,
    # which the job log would otherwise not show.
    jq -rs --arg p "$pkg" ". as \$all
      | [\$all[] | $mine] as \$e
      | [\$e[] | select(.Action == \"fail\") | {Package, Test}] as \$f
      | [\$e[] | select(.Action == \"fail\" and .FailedBuild) | .FailedBuild] as \$b
      | ((\$all[] | select(.Action == \"build-output\" and (.ImportPath as \$i | \$b | index([\$i])))),
         (\$e[] | select(.Action == \"output\") | select({Package, Test} as \$k | \$f | index([\$k]))))
      | (.Output // \"\") | rtrimstr(\"\\n\")" "$json" | sed 's/^/    /' >&2
    # A package that timed out has no failed test: go test -json attributes the
    # timeout panic and the goroutine dump to the running test and emits no
    # fail event for it, so only the package fails. Print that package's
    # output from the panic on: the panic and the whole "running tests:" block,
    # which names the hung test even when -parallel makes it long, then the
    # lines after it (the goroutine dump), capped.
    jq -rs --arg p "$pkg" --argjson cap "$hang_cap" "[.[] | $mine] as \$e
      | ([\$e[] | select(.Action == \"fail\" and .Test != null) | .Package] | unique) as \$tf
      | [\$e[] | select(.Action == \"fail\" and .Test == null and (.FailedBuild | not) and ((.Package // \"\") as \$q | \$tf | index(\$q) | not)) | .Package] | unique | .[] as \$q
      | [\$e[] | select(.Package == \$q and .Action == \"output\") | .Output // \"\"] as \$o
      | ([\$o | to_entries[] | select(.value | startswith(\"panic:\")) | .key] | first // 0) as \$start
      | ([\$o | to_entries[] | select(.key >= \$start and .value == \"\\trunning tests:\\n\") | .key] | first) as \$r
      | (if \$r == null then \$start else ([\$o | to_entries[] | select(.key > \$r and (.value | startswith(\"\\t\\t\") | not)) | .key] | first // (\$o | length)) end) as \$end
      | \"no-skips: \" + \$q + \" failed with no failing test; its output from the first panic, the whole running tests block, then at most \" + (\$cap | tostring) + \" more lines:\",
        ((\$o[\$start:\$end][], \$o[\$end:\$end + \$cap][]) | \"    \" + rtrimstr(\"\\n\"))" "$json" >&2
    bad=1
  fi
  if [[ -n "$unfinished" ]]; then
    echo "no-skips: packages under $pkg started and did not finish (no package-level pass or fail):" >&2
    printf '%s\n' "$unfinished" | sed 's/^/  /' >&2
    bad=1
  fi
  if [[ -z "$passed" ]]; then
    echo "no-skips: no test in $pkg passed; the package did not run" >&2
    bad=1
  fi
  if [[ $bad -eq 1 ]]; then
    rc=1
  else
    echo "no-skips: every test in $pkg ran ($(printf '%s\n' "$passed" | wc -l | tr -d ' ') passed, none skipped or failed)"
  fi
done
exit "$rc"
