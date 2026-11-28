#!/usr/bin/env bash
# Exercise lefthook.yml's pre-push hook with real `git push` runs.
#
# The repo's lefthook.yml is copied into scratch repos with a local bare remote,
# and its `go test` line is replaced by a marker, so each case asks one
# question: did the hook start the test suite for this push? Nothing leaves the
# machine and no Go test runs.
#
# A case passes only when all of these hold, so a refused push, a missing hook
# or a hook that never ran cannot read as a skip:
#   - the push exits 0;
#   - the bare remote's ref equals the local HEAD;
#   - .git/hooks/pre-push exists and is lefthook's;
#   - the suite ran (marker printed), or lefthook's own output says it ran
#     pre-push and skipped test-race.
#
# Scratch space is mktemp under TMPDIR and is left in place; a failed case
# prints its directory. Usage: scripts/test-lefthook.sh (needs lefthook, git).
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
src="$root/lefthook.yml"
[[ -f "$src" ]] || { echo "no lefthook.yml at $src" >&2; exit 2; }
command -v lefthook >/dev/null || { echo "lefthook not on PATH" >&2; exit 2; }

base=$(mktemp -d "${TMPDIR:-/tmp}/lht.XXXXXX") || exit 2
[[ -d "$base" ]] || exit 2

# Everything below runs with the scratch HOME and no user or system git config.
export HOME="$base"
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid
export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid

marker="SUITE-RAN-MARKER"
fails=0
n=0

# newrepo <name>: scratch repo on main with go.mod committed, main pushed.
newrepo() {
  local d="$base/$1"
  git init -q --bare "$d.git"
  git init -q -b main "$d"
  # Swap the real suite for the marker; every other line is the repo's own.
  sed "s|go test ./... -race -count=1|echo $marker|" "$src" >"$d/lefthook.yml"
  grep -q "$marker" "$d/lefthook.yml" || { echo "marker not placed: lefthook.yml has no 'go test ./... -race -count=1' line" >&2; exit 2; }
  (
    cd "$d"
    git remote add origin "$d.git"
    git add lefthook.yml
    git -c commit.gpgsign=false commit -qm base
    printf 'module example.invalid/t\n\ngo 1.22\n' >go.mod
    git add go.mod
    git -c commit.gpgsign=false commit -qm gomod
    git push -q origin main >/dev/null 2>&1 </dev/null
  )
  echo "$d"
}

# check <label> <want: ran|skipped> <branch> <file> [dest-ref]
check() {
  local label=$1 want=$2 branch=$3 file=$4 dest=${5:-$3}
  n=$((n + 1))
  local d out rc=0 why=""
  d=$(newrepo "case$n")
  out=$(
    cd "$d"
    git switch -q "$branch" 2>/dev/null || git switch -q -c "$branch"
    mkdir -p "$(dirname "$file")"
    echo "x $n" >>"$file"
    git add "$file"
    git -c commit.gpgsign=false commit -qm "change $file"
    # Install after the commit, so only the push hook is under test.
    lefthook install >/dev/null 2>&1 </dev/null || echo "LEFTHOOK-INSTALL-FAILED"
    git push origin "$branch:$dest" 2>&1 </dev/null || echo "PUSH-EXIT-NONZERO"
  ) || rc=$?

  if [[ $rc -ne 0 ]]; then why="case setup failed (rc=$rc)"; fi
  if grep -q "LEFTHOOK-INSTALL-FAILED" <<<"$out"; then why="lefthook install failed"; fi
  if grep -q "PUSH-EXIT-NONZERO" <<<"$out"; then why="push exited nonzero"; fi
  if [[ -z "$why" ]]; then
    if [[ ! -f "$d/.git/hooks/pre-push" ]] || ! grep -q lefthook "$d/.git/hooks/pre-push"; then
      why="no lefthook pre-push hook installed"
    fi
  fi
  if [[ -z "$why" ]]; then
    local head remote
    head=$(git -C "$d" rev-parse HEAD)
    remote=$(git -C "$d.git" rev-parse "refs/heads/$dest" 2>/dev/null || echo none)
    [[ "$head" == "$remote" ]] || why="remote refs/heads/$dest is $remote, local HEAD is $head"
  fi

  local got=unknown
  if [[ -z "$why" ]]; then
    if grep -q "$marker" <<<"$out"; then
      got=ran
    elif grep -Eq 'test-race \(skip\)' <<<"$out" && grep -q 'pre-push' <<<"$out"; then
      got=skipped
    else
      why="no marker and no 'test-race (skip)' line from lefthook"
    fi
  fi

  if [[ -z "$why" && "$got" == "$want" ]]; then
    echo "ok   $label: suite $got"
  else
    echo "FAIL $label: want suite $want, got ${why:-$got} (dir $d)"
    fails=$((fails + 1))
  fi
}

check "(i) docs-only push to a build branch"        skipped build/marvel/x   doc.md
check "(ii) Go file push to a build branch"         ran     build/marvel/x   a.go
check "(iii) go.mod-only push to a build branch"    ran     build/marvel/x   go.mod
check "(iv) Go file push on a redgate/ branch"      skipped redgate/x        a.go
check "(v) redgate/ branch pushed to remote main"   ran     redgate/x        a.go  main
check "(vi) Go file push on main to remote main"    ran     main             a.go

if ((fails > 0)); then
  echo "$fails of $n cases failed"
  exit 1
fi
echo "all $n cases passed"
