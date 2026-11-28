#!/usr/bin/env bash
# Exercise lefthook.yml's pre-commit lint hook with real `git commit` runs.
#
# The repo's lefthook.yml is copied into a scratch repo with no GitHub remote,
# plus a linked worktree of it. A stub golangci-lint on PATH records its
# working directory and its GOLANGCI_LINT_CACHE, so each check asks one
# question: which cache directory does the hook give the linter in each
# checkout? Nothing leaves the machine and no real lint runs.
#
# Why: golangci-lint's default cache is one directory per user, so a finding
# cached from a removed sibling worktree can be replayed in another checkout
# (finding-026). A cache inside each checkout's own git directory cannot be.
#
# A check passes only when all of these hold, so a refused commit, a missing
# hook or a hook that never ran cannot read as a pass:
#   - the commit exits 0;
#   - .git/hooks/pre-commit exists and is lefthook's;
#   - the stub ran once in each checkout, from that checkout's directory.
#
# Scratch space is mktemp under TMPDIR and is left in place; a failed check
# prints its directory. Usage: scripts/test-lint-cache.sh (needs lefthook, git).
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
src="$root/lefthook.yml"
[[ -f "$src" ]] || { echo "no lefthook.yml at $src" >&2; exit 2; }
command -v lefthook >/dev/null || { echo "lefthook not on PATH" >&2; exit 2; }

base=$(mktemp -d "${TMPDIR:-/tmp}/lcache.XXXXXX") || exit 2
[[ -d "$base" ]] || exit 2
base=$(cd "$base" && pwd -P)

# Everything below runs with the scratch HOME and no user or system git config.
export HOME="$base"
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid
export GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.invalid
# Only the lint command is under test; fmt-check and vet are left out.
export LEFTHOOK_EXCLUDE=fmt-check,vet
unset GOLANGCI_LINT_CACHE

calls="$base/calls.log"
: >"$calls"
export LINT_STUB_LOG="$calls"
mkdir "$base/bin"
cat >"$base/bin/golangci-lint" <<'EOF'
#!/bin/sh
printf '%s|%s\n' "$(pwd -P)" "$GOLANGCI_LINT_CACHE" >>"$LINT_STUB_LOG"
EOF
chmod +x "$base/bin/golangci-lint"
export PATH="$base/bin:$PATH"

fails=0
n=0

# ok <label> <condition result: 0 holds> <detail on failure>
verdict() {
  n=$((n + 1))
  if [[ $2 -eq 0 ]]; then
    echo "ok   $1"
  else
    echo "FAIL $1: $3 (dir $base)"
    fails=$((fails + 1))
  fi
}

main="$base/main"
wt="$base/wt"
git init -q -b main "$main"
cp "$src" "$main/lefthook.yml"
(
  cd "$main"
  printf 'module example.invalid/t\n\ngo 1.22\n' >go.mod
  git add lefthook.yml go.mod
  git -c commit.gpgsign=false commit -qm base
  git worktree add -q -b other "$wt"
  lefthook install >/dev/null 2>&1 </dev/null || { echo "LEFTHOOK-INSTALL-FAILED" >&2; exit 3; }
)

# commit_go <dir> <name>: commit one Go file, so the hook's *.go glob matches.
commit_go() {
  (
    cd "$1"
    printf 'package a\n' >"$2"
    git add "$2"
    git -c commit.gpgsign=false commit -qm "add $2" >"$base/commit-$2.out" 2>&1 </dev/null
  )
}

setup=0
commit_go "$main" a.go || setup=1
commit_go "$wt" b.go || setup=1
verdict "setup: both commits exit 0" "$setup" "a commit failed (see $base/commit-*.out)"

hook=0
grep -q lefthook "$main/.git/hooks/pre-commit" 2>/dev/null || hook=1
verdict "setup: lefthook pre-commit hook installed" "$hook" "no lefthook pre-commit hook"

# One stub line per checkout, from that checkout's directory.
main_line=$(grep -F "$main|" "$calls" || true)
wt_line=$(grep -F "$wt|" "$calls" || true)
seen=0
[[ -n "$main_line" && -n "$wt_line" ]] || seen=1
verdict "setup: linter ran once in each checkout" "$seen" "calls.log has no line for $( [[ -z "$main_line" ]] && echo main || echo worktree )"

main_cache=${main_line#*|}
wt_cache=${wt_line#*|}
main_gitdir=$(git -C "$main" rev-parse --absolute-git-dir)
wt_gitdir=$(git -C "$wt" rev-parse --absolute-git-dir)

# (i) The main checkout's cache is its own, absolute, and under its git dir.
r=0
[[ "$main_cache" == "$main_gitdir"/?* ]] || r=1
verdict "(i) main checkout lints with a cache under its own git dir" "$r" "GOLANGCI_LINT_CACHE='$main_cache', want a path under $main_gitdir"

# (ii) The linked worktree's cache is its own, under its own git dir.
r=0
[[ "$wt_cache" == "$wt_gitdir"/?* ]] || r=1
verdict "(ii) linked worktree lints with a cache under its own git dir" "$r" "GOLANGCI_LINT_CACHE='$wt_cache', want a path under $wt_gitdir"

# (iii) The two checkouts do not share a cache.
r=0
[[ -n "$main_cache" && -n "$wt_cache" && "$main_cache" != "$wt_cache" ]] || r=1
verdict "(iii) the two checkouts use different caches" "$r" "main='$main_cache' worktree='$wt_cache'"

if ((fails > 0)); then
  echo "$fails of $n checks failed"
  exit 1
fi
echo "all $n checks passed"
