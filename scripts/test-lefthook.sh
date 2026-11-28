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
#   - the bare remote's ref equals the pushed local branch;
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
    printf 'package a\n' >a.go
    git add go.mod a.go
    git -c commit.gpgsign=false commit -qm gomod
    git push -q origin main >/dev/null 2>&1 </dev/null
  )
  echo "$d"
}

# judge <label> <want: ran|skipped> <dir> <setup rc> <hook output> <expected remote refs>
# Each expected remote ref is "<ref>=<local branch>" (the remote ref must equal
# that local branch) or "<ref>=absent" (the remote ref must not exist).
judge() {
  local label=$1 want=$2 d=$3 rc=$4 out=$5 refs=$6 why="" pair ref want_ref head remote
  if [[ $rc -ne 0 ]]; then why="case setup failed (rc=$rc)"; fi
  if grep -q "LEFTHOOK-INSTALL-FAILED" <<<"$out"; then why="lefthook install failed"; fi
  if grep -q "PUSH-EXIT-NONZERO" <<<"$out"; then why="push exited nonzero"; fi
  if [[ -z "$why" ]]; then
    if [[ ! -f "$d/.git/hooks/pre-push" ]] || ! grep -q lefthook "$d/.git/hooks/pre-push"; then
      why="no lefthook pre-push hook installed"
    fi
  fi
  if [[ -z "$why" ]]; then
    for pair in $refs; do
      ref=${pair%%=*}
      want_ref=${pair#*=}
      remote=$(git -C "$d.git" rev-parse -q --verify "refs/heads/$ref" 2>/dev/null || echo absent)
      if [[ "$want_ref" == absent ]]; then
        head=absent
      else
        head=$(git -C "$d" rev-parse "refs/heads/$want_ref")
      fi
      [[ "$head" == "$remote" ]] || { why="remote refs/heads/$ref is $remote, want $head"; break; }
    done
  fi

  local got=unknown
  if [[ -z "$why" ]]; then
    if grep -q "$marker" <<<"$out"; then
      got=ran
    elif grep -Eq 'test-race: skipped, ' <<<"$out" && grep -q 'pre-push' <<<"$out"; then
      got=skipped
    elif grep -Eq 'test-race \(skip\)' <<<"$out"; then
      got="skipped by lefthook's own line, not by a 'test-race: skipped, <reason>' line"
    else
      why="no marker and no 'test-race: skipped, <reason>' line"
    fi
  fi

  if [[ -z "$why" && "$got" == "$want" ]]; then
    echo "ok   $label: suite $got"
  else
    echo "FAIL $label: want suite $want, got ${why:-$got} (dir $d)"
    fails=$((fails + 1))
  fi
}

commit_file() {
  mkdir -p "$(dirname "$1")"
  echo "x $n" >>"$1"
  git add "$1"
  git -c commit.gpgsign=false commit -qm "change $1"
}

# check <label> <want: ran|skipped> <branch> <file> [dest-ref] [mode]
# mode: "" (append to file), delete (git rm file), from-docs (push <branch> from
# a checkout whose HEAD is a docs-only branch)
check() {
  local label=$1 want=$2 branch=$3 file=$4 dest=${5:-$3} mode=${6:-}
  n=$((n + 1))
  local d out rc=0
  d=$(newrepo "case$n")
  out=$(
    cd "$d"
    git switch -q "$branch" 2>/dev/null || git switch -q -c "$branch"
    if [[ "$mode" == delete ]]; then
      git rm -q "$file"
      git -c commit.gpgsign=false commit -qm "delete $file"
    else
      commit_file "$file"
    fi
    if [[ "$mode" == from-docs ]]; then
      git switch -q -c docs-checkout main
      commit_file doc.md
    fi
    # Install after the commit, so only the push hook is under test.
    lefthook install >/dev/null 2>&1 </dev/null || echo "LEFTHOOK-INSTALL-FAILED"
    git push origin "$branch:$dest" 2>&1 </dev/null || echo "PUSH-EXIT-NONZERO"
  ) || rc=$?
  judge "$label" "$want" "$d" "$rc" "$out" "$dest=$branch"
}

# check_two <label> <want> <old|new>: one push of two refs from a checkout on main.
#   new: build/docs (docs-only) and build/go (a Go file), pushed together.
#   old: delete the remote branch "old" and push build/go, in one push.
check_two() {
  local label=$1 want=$2 kind=$3
  n=$((n + 1))
  local d out rc=0 refs
  d=$(newrepo "case$n")
  out=$(
    cd "$d"
    git switch -q -c build/go main
    commit_file a.go
    git switch -q main
    if [[ "$kind" == new ]]; then
      git switch -q -c build/docs main
      commit_file doc.md
      git switch -q main
    else
      git push -q origin main:refs/heads/old >/dev/null 2>&1 </dev/null
    fi
    lefthook install >/dev/null 2>&1 </dev/null || echo "LEFTHOOK-INSTALL-FAILED"
    if [[ "$kind" == new ]]; then
      git push origin build/docs build/go 2>&1 </dev/null || echo "PUSH-EXIT-NONZERO"
    else
      git push origin :old build/go 2>&1 </dev/null || echo "PUSH-EXIT-NONZERO"
    fi
  ) || rc=$?
  if [[ "$kind" == new ]]; then refs="build/docs=build/docs build/go=build/go"; else refs="old=absent build/go=build/go"; fi
  judge "$label" "$want" "$d" "$rc" "$out" "$refs"
}

check "(i) docs-only push to a build branch"        skipped build/marvel/x   doc.md
check "(ii) Go file push to a build branch"         ran     build/marvel/x   a.go
check "(iii) go.mod-only push to a build branch"    ran     build/marvel/x   go.mod
check "(iv) Go file push on a redgate/ branch"      skipped redgate/x        a.go
check "(v) redgate/ branch pushed to remote main"   ran     redgate/x        a.go  main
check "(vi) Go file push on main to remote main"    ran     main             a.go
check "(vii) docs-only build branch pushed to main" ran     build/marvel/x   doc.md main
check "(viii) Go branch pushed to main from a docs checkout" ran build/marvel/x a.go main from-docs
check "(ix) push whose only change deletes a.go"    ran     build/marvel/x   a.go  build/marvel/x delete
check "(x) Go branch pushed to its own ref from a docs checkout" ran build/marvel/x a.go build/marvel/x from-docs
check_two "(xi) one push of a docs branch and a Go branch" ran new
check_two "(xii) one push that deletes a branch and pushes a Go branch" ran old

if ((fails > 0)); then
  echo "$fails of $n cases failed"
  exit 1
fi
echo "all $n cases passed"
