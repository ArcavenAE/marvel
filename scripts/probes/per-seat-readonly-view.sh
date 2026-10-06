#!/usr/bin/env bash
# Probe rig for marvel _kos/probes/probe-per-seat-readonly-view.md.
# Runs in a scratch directory against `git clone --local` copies only.
# Seats other than this shell are panes on a scratch tmux server
# (TMUX unset, own socket), never the default server.
# H1's Edit and Write tool trials are run by a Claude Code session against the
# paths this script prints; the script covers the shell side of every
# hypothesis. Usage: per-seat-readonly-view.sh <scratch-dir> <marvel-src> <orc-src> <section>
# git output is always written to a file before it is shortened: piping it to
# head kills git with SIGPIPE mid-run, before it writes HEAD (found in this run).
# Sections run in order: setup h1 h2 h3 h4 h5 h6 teardown. State persists in
# the scratch dir between sections so the H1 tool trials can run in between.
set -uo pipefail

S="$1"; MARVEL_SRC="$2"; ORC_SRC="$3"; SECTION="$4"
SHARED="$S/shared"; VIEW="$S/view"
# A unix socket path is capped near 104 bytes on macOS, and the scratch path is
# longer, so the scratch tmux server's socket lives at a short per-run path.
# The default below is a macOS scratch root; set TSOCK elsewhere on Linux.
TSOCK="${TSOCK:-/tmp/claude-501/rov-$(basename "$S")-$PPID.sock}"
[[ -f "$S/tsock" ]] && TSOCK=$(cat "$S/tsock") || echo "$TSOCK" > "$S/tsock"
unset TMUX
export TMUX_TMPDIR="$S/tmuxdir"; mkdir -p "$TMUX_TMPDIR"
T() { tmux -S "$TSOCK" "$@"; }

say() { printf '\n## %s\n' "$*"; }
treehash() { # content hash of a tree, excluding git metadata
  (cd "$1" && find . -type f ! -name .git ! -path './.git/*' -print0 | LC_ALL=C sort -z |
    xargs -0 shasum -a 256 | shasum -a 256 | cut -c1-16)
}
state() { # HEAD, status line count, content hash of the worktree view
  printf 'HEAD=%s status_lines=%s hash=%s\n' \
    "$(git -C "$VIEW" rev-parse --short HEAD 2>&1)" \
    "$(git -C "$VIEW" status --porcelain 2>/dev/null | wc -l | tr -d ' ')" \
    "$(treehash "$VIEW")"
}
ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
ro() { chmod -R a-w "$1"; }
rw() { chmod -R u+w "$1"; }
objs() { git -C "$SHARED" count-objects -v | grep -E '^(count|in-pack)' | tr '\n' ' '; echo; }

refresh_wt() { # H3 refresh: chmod u+w, checkout --force --detach, chmod a-w
  local sha="$1" t0 t1
  t0=$(ms); rw "$VIEW"
  git -C "$VIEW" checkout -q --force --detach "$sha" 2>&1
  ro "$VIEW"; t1=$(ms)
  echo "writable window ms=$((t1 - t0))"
}

[[ -f "$S/env" ]] && . "$S/env"

pause() { python3 -c "import time;time.sleep($1)"; }
pane_run() { # pane_run <target> <outfile> <cmd>: run a command in a seat pane, wait for it
  rm -f "$2" "$2.done"
  T send-keys -t "$1" "{ $3 ; } > '$2' 2>&1; touch '$2.done'" Enter
  for _ in $(seq 1 100); do [[ -f "$2.done" ]] && break; pause 0.1; done
  cat "$2"
}
seat() { # seat <name> <cwd>: a scratch tmux window with a plain shell
  if T has-session -t probe 2>/dev/null; then T new-window -d -t probe -n "$1" -c "$2" "env -i HOME=$HOME PATH=$PATH bash --norc --noprofile"
  else T new-session -d -s probe -n "$1" -c "$2" "env -i HOME=$HOME PATH=$PATH bash --norc --noprofile"; fi
  pause 0.3
}
fresh_hash() { # hash of a fresh archive of <repo> <sha>, with VIEW_SHA added like the view
  local d; d=$(mktemp -d "$S/fresh.XXXX"); git -C "$1" archive "$2" | tar -x -C "$d"; echo "$2" > "$d/VIEW_SHA"; treehash "$d"; rm -rf "$d"
}
rebuild_view() { # a clean read-only worktree view at <sha>
  [[ -d "$VIEW" ]] && rw "$VIEW"; rm -rf "$VIEW"; git -C "$SHARED" worktree prune
  git -C "$SHARED" worktree add -q --detach "$VIEW" "$1"; ro "$VIEW"
}
build_arch() { # build_arch <repo> <sha> <dir>: archive view tree, read-only
  mkdir -p "$3"; git -C "$1" archive "$2" | tar -x -C "$3"; echo "$2" > "$3/VIEW_SHA"; ro "$3"
}
swap_arch() { # swap_arch <base> <newdir>: replace base/cur by rename of a new symlink
  # BSD mv -h renames the link itself; on Linux use mv -T.
  ln -s "$2" "$1/cur.new"; mv -h "$1/cur.new" "$1/cur"
}

case "$SECTION" in
setup)
say "setup"
rm -rf "$SHARED" "$VIEW" "$S/arch" "$S/orc" "$S/ctl"
git clone -q --local --no-hardlinks "$MARVEL_SRC" "$SHARED"
OLD=$(git -C "$SHARED" rev-parse --short HEAD~1); NEW=$(git -C "$SHARED" rev-parse --short HEAD)
echo "OLD=$OLD NEW=$NEW git=$(git --version) os=$(uname -sr)"
DIFF_FILE=$(git -C "$SHARED" diff --name-only --diff-filter=M "$OLD" "$NEW" | head -1)
echo "probe file (differs OLD to NEW): $DIFF_FILE"
printf 'OLD=%s\nNEW=%s\nDIFF_FILE=%s\n' "$OLD" "$NEW" "$DIFF_FILE" > "$S/env"

say "positive control: a writable detached worktree accepts a write and a checkout"
git -C "$SHARED" worktree add -q --detach "$S/ctl" "$OLD"
echo x >> "$S/ctl/$DIFF_FILE" && echo "write ok"
git -C "$S/ctl" checkout -q --force --detach "$NEW" && echo "checkout ok: $(git -C "$S/ctl" rev-parse --short HEAD)"
git -C "$SHARED" worktree remove --force "$S/ctl"

say "step 3: create the view at OLD and make it read-only"
t0=$(ms); git -C "$SHARED" worktree add -q --detach "$VIEW" "$OLD"; ro "$VIEW"; t1=$(ms)
echo "create+chmod ms=$((t1 - t0))"; state
;;
h1)
say "H1 (shell half): a Bash redirect into the view"
( echo x >> "$VIEW/$DIFF_FILE" ) 2>&1; echo "exit=$?"
( : > "$VIEW/newfile" ) 2>&1; echo "exit=$?"
echo "H1 tool trials target: $VIEW/$DIFF_FILE"
;;
h2)
say "H2: commands in the shared checkout"
for cmd in "switch -q -c h2-branch" "checkout -q -b h2-branch2" "fetch -q origin" "gc -q" "worktree prune" "worktree remove $VIEW"; do
  echo "> git $cmd"; git -C "$SHARED" $cmd 2>&1 | sed 's/^/  /'; echo "  exit=${PIPESTATUS[0]}"; printf '  '; state
done
;;
h3)
say "H3: refresh, from torn and from clean; held cwds during the in-place refresh (run note 1)"
T kill-server 2>/dev/null
rebuild_view "$OLD"; state
EXPECT_NEW=$(git -C "$SHARED" archive "$NEW" >/dev/null; d=$(mktemp -d "$S/f.XXXX"); git -C "$SHARED" archive "$NEW" | tar -x -C "$d"; treehash "$d"; rm -rf "$d")
EXPECT_OLD=$(d=$(mktemp -d "$S/f.XXXX"); git -C "$SHARED" archive "$OLD" | tar -x -C "$d"; treehash "$d"; rm -rf "$d")
echo "expected hash OLD=$EXPECT_OLD NEW=$EXPECT_NEW"
echo "> naive: git -C view checkout --detach NEW, while read-only"
git -C "$VIEW" checkout -q --detach "$NEW" > "$S/naive.out" 2>&1; echo "exit=$?"; head -3 "$S/naive.out"; state
echo "> H3 refresh from the torn view to NEW"; refresh_wt "$NEW"; state
echo "> H3 refresh from a clean view to OLD"; refresh_wt "$OLD"; state
echo "> held cwds: seat A at the view root, seat B in internal/api"
seat A "$VIEW"; seat B "$VIEW/internal/api"
echo "A before: $(pane_run probe:A "$S/out.A" 'shasum -a 256 CLAUDE.md | cut -c1-16')"
echo "B before: $(pane_run probe:B "$S/out.B" 'shasum -a 256 manifest.go | cut -c1-16')"
refresh_wt "$NEW"; state
echo "A after:  $(pane_run probe:A "$S/out.A" 'shasum -a 256 CLAUDE.md | cut -c1-16')  expect $(git -C "$SHARED" show "$NEW":CLAUDE.md | shasum -a 256 | cut -c1-16)"
echo "B after:  $(pane_run probe:B "$S/out.B" 'shasum -a 256 manifest.go | cut -c1-16')  expect $(git -C "$SHARED" show "$NEW":internal/api/manifest.go | shasum -a 256 | cut -c1-16)"
echo "> held cwd in a directory the target commit removes: view2 at c143886, seat C in docs/reviews, refresh to c01452c"
VIEW2="$S/view2"; [[ -d "$VIEW2" ]] && rw "$VIEW2"; rm -rf "$VIEW2"; git -C "$SHARED" worktree prune
git -C "$SHARED" worktree add -q --detach "$VIEW2" c143886; ro "$VIEW2"
seat C "$VIEW2/docs/reviews"
echo "C before: $(pane_run probe:C "$S/out.C" 'pwd; ls | wc -l')"
t0=$(ms); rw "$VIEW2"; git -C "$VIEW2" checkout -q --force --detach c01452c 2>&1; ro "$VIEW2"; t1=$(ms); echo "view2 window ms=$((t1-t0)) HEAD=$(git -C "$VIEW2" rev-parse --short HEAD) docs/reviews exists: $([[ -d "$VIEW2/docs/reviews" ]] && echo yes || echo no)"
echo "C after:  $(pane_run probe:C "$S/out.C" 'pwd; ls 2>&1 | head -2; ls .. 2>&1 | head -2' | tr '\n' ' ')"
;;
h5)
T kill-server 2>/dev/null
say "H5: git aimed at the view, from the seat's own shell and from another seat"
rebuild_view "$OLD"; seat D "$SHARED"
echo "objects before: $(objs)"
for who in own other; do
  for cmd in "checkout -q --detach $NEW" "switch -q --detach $NEW" "reset -q --hard $NEW" "reset -q --soft $NEW" "update-ref HEAD $NEW"; do
    if [[ $who == own ]]; then (cd "$VIEW" && git $cmd) > "$S/own.out" 2>&1; out=$(head -2 "$S/own.out" | tr '\n' ' ')
    else out=$(pane_run probe:D "$S/out.D" "git -C '$VIEW' $cmd" | head -2 | tr '\n' ' '); fi
    printf '%-5s git %-28s -> %s\n        ' "$who" "$cmd" "${out:-ok}"; state
    rw "$VIEW"; git -C "$VIEW" checkout -q --force --detach "$OLD"; ro "$VIEW"
  done
done
echo "commit --allow-empty: not run; a commit here needs a signing key touch, and the fleet rule forbids unsigned commits. update-ref HEAD is the ref move a commit makes."
echo "objects after: $(objs)"
;;
h4)
say "H4 and H6d: cost on a local clone of the orc (5 runs each)"
ORC="$S/orc"; rm -rf "$ORC"; git clone -q --local --no-hardlinks "$ORC_SRC" "$ORC"
O1=$(git -C "$ORC" rev-parse --short HEAD~1); O2=$(git -C "$ORC" rev-parse --short HEAD)
echo "orc tracked files: $(git -C "$ORC" ls-files | wc -l | tr -d ' ')  O1=$O1 O2=$O2"
for i in 1 2 3 4 5; do
  V="$S/orcview$i"; t0=$(ms); git -C "$ORC" worktree add -q --detach "$V" "$O1"; ro "$V"; t1=$(ms)
  rw "$V"; git -C "$V" checkout -q --force --detach "$O2"; ro "$V"; t2=$(ms)
  echo "worktree run $i: create+chmod ms=$((t1-t0)) refresh ms=$((t2-t1))"
  rw "$V"; git -C "$ORC" worktree remove --force "$V"
done
A="$S/archorc"; mkdir -p "$A"
for i in 1 2 3 4 5; do
  t0=$(ms); build_arch "$ORC" "$O1" "$A/a$i"; t1=$(ms)
  [[ -L "$A/cur" ]] || ln -s "$A/a$i" "$A/cur"
  build_arch "$ORC" "$O2" "$A/b$i"; swap_arch "$A" "$A/b$i"; t2=$(ms)
  echo "archive run $i: create+chmod ms=$((t1-t0)) refresh+swap ms=$((t2-t1))"
done
;;
h6)
T kill-server 2>/dev/null
say "H6: the archive view (marvel clone), outside any work tree"
A="$S/arch"; for d in "$A"/*; do [[ -d "$d" && ! -L "$d" ]] && rw "$d"; done; rm -rf "$A"; mkdir -p "$A"
echo "arch dir inside a work tree? $(git -C "$A" rev-parse --show-toplevel 2>&1 | head -1)"
build_arch "$SHARED" "$OLD" "$A/v0"; ln -s "$A/v0" "$A/cur"
echo "H6a: view hash=$(treehash "$A/cur/") fresh hash=$(fresh_hash "$SHARED" "$OLD")"
echo "H6c: git -C cur rev-parse: $(git -C "$A/cur" rev-parse --show-toplevel 2>&1 | head -1)"
for cmd in "checkout -q --detach $NEW" "switch -q --detach $NEW" "reset -q --hard $NEW" "reset -q --soft $NEW" "update-ref HEAD $NEW"; do
  (cd "$A/cur" && git $cmd) > "$S/arch.out" 2>&1; echo "  git $cmd -> $(head -1 "$S/arch.out")  hash=$(treehash "$A/cur/")"
done
echo "H6c control: the same archive inside the shared checkout"
CA="$SHARED/ctlarch"; mkdir -p "$CA"; git -C "$SHARED" archive "$OLD" | tar -x -C "$CA"
echo "  git -C ctlarch rev-parse --show-toplevel: $(git -C "$CA" rev-parse --show-toplevel 2>&1 | head -1)"
rm -rf "$CA"
echo "H1 (shell half) on the archive view:"; ( echo x >> "$A/cur/$DIFF_FILE" ) 2>&1 | sed 's/^.*: //'; echo "  exit=${PIPESTATUS[0]}"
echo "H6b2: seat E enters the view through the path, then two refreshes"
seat E "$A/cur"
echo "E before:        $(pane_run probe:E "$S/out.E" 'cat ./VIEW_SHA; pwd -P' | tr '\n' ' ')"
build_arch "$SHARED" "$NEW" "$A/v1"; swap_arch "$A" "$A/v1"
echo "path now:        $(cat "$A/cur/VIEW_SHA")"
echo "E after one:     $(pane_run probe:E "$S/out.E" "cat ./VIEW_SHA; shasum -a 256 ./$DIFF_FILE | cut -c1-16; pwd -P" | tr '\n' ' ')"
build_arch "$SHARED" "$OLD" "$A/v2"; swap_arch "$A" "$A/v2"; rw "$A/v0"; rm -rf "$A/v0"
echo "path now:        $(cat "$A/cur/VIEW_SHA") (v0 removed, as the lifetime rule says)"
echo "E after two:     $(pane_run probe:E "$S/out.E" "cat ./VIEW_SHA 2>&1; ls 2>&1 | head -1; pwd -P 2>&1" | tr '\n' ' ')"
echo "H6b: a reader loop through the path during 20 swaps"
seat F "$S"
T send-keys -t probe:F "for i in \$(seq 1 4000); do shasum -a 256 '$A/cur/$DIFF_FILE' 2>&1 | cut -c1-16; done > '$S/out.F'; touch '$S/out.F.done'" Enter
pause 0.3
for i in $(seq 1 20); do
  if (( i % 2 )); then sha=$NEW; else sha=$OLD; fi
  build_arch "$SHARED" "$sha" "$A/s$i"; swap_arch "$A" "$A/s$i"
done
for _ in $(seq 1 300); do [[ -f "$S/out.F.done" ]] && break; pause 0.1; done
echo "reader lines: $(wc -l < "$S/out.F" | tr -d ' ')"
sort "$S/out.F" | uniq -c | sort -rn | head -5
echo "expected file hashes: OLD=$(git -C "$SHARED" show "$OLD:$DIFF_FILE" | shasum -a 256 | cut -c1-16) NEW=$(git -C "$SHARED" show "$NEW:$DIFF_FILE" | shasum -a 256 | cut -c1-16)"
echo "H1 tool trials target: $A/cur/$DIFF_FILE"
;;
teardown)
T kill-server 2>/dev/null; rm -f "$TSOCK" "$S/tsock"
for d in "$VIEW" "$S/view2" "$S"/arch/* "$S"/orcview* "$S"/archorc/*; do [[ -d "$d" && ! -L "$d" ]] && chmod -R u+w "$d"; done
echo "teardown done"
;;
esac
