#!/usr/bin/env bash
# Exercise lefthook.yml's pre-push hook with real `git push` runs.
#
# The repo's lefthook.yml is copied into scratch repos with a local bare remote,
# and its `go test` line is replaced by a marker, so each case asks one
# question: did the hook start the test suite for this push? Nothing leaves the
# machine and no Go test runs.
#
# Usage: scripts/test-lefthook.sh   (needs lefthook and git on PATH)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
src="$root/lefthook.yml"
[[ -f "$src" ]] || { echo "no lefthook.yml at $src" >&2; exit 2; }
command -v lefthook >/dev/null || { echo "lefthook not on PATH" >&2; exit 2; }

base=$(mktemp -d /private/tmp/lht.XXXXXX) || exit 2
marker="SUITE-RAN-MARKER"
fails=0
n=0

# newrepo <name>: scratch repo on main with the hook installed, main pushed.
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
    git -c commit.gpgsign=false -c user.name=t -c user.email=t@example.invalid commit -qm base
    printf 'module example.invalid/t\n\ngo 1.22\n' >go.mod
    git add go.mod
    git -c commit.gpgsign=false -c user.name=t -c user.email=t@example.invalid commit -qm gomod
    git push -q origin main >/dev/null 2>&1 </dev/null
  )
  echo "$d"
}

# check <label> <want: ran|skipped> <branch> <file> <refspec-suffix>
check() {
  local label=$1 want=$2 branch=$3 file=$4 dest=${5:-$3}
  n=$((n + 1))
  local d out got
  d=$(newrepo "case$n")
  out=$(
    cd "$d"
    git switch -q "$branch" 2>/dev/null || git switch -q -c "$branch"
    mkdir -p "$(dirname "$file")"
    echo "x $n" >>"$file"
    git add "$file"
    git -c commit.gpgsign=false -c user.name=t -c user.email=t@example.invalid commit -qm "change $file"
    # Install after the commit, so only the push hook is under test.
    lefthook install >/dev/null 2>&1 </dev/null
    # A scratch HOME and no fleet variables: the hook sees only what it needs.
    env -i HOME="$base" PATH="$PATH" LANG="${LANG:-C}" git push origin "$branch:$dest" 2>&1 </dev/null || true
  )
  if grep -q "$marker" <<<"$out"; then got=ran; else got=skipped; fi
  if [[ "$got" == "$want" ]]; then
    echo "ok   $label: suite $got"
  else
    echo "FAIL $label: want suite $want, got $got"
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
