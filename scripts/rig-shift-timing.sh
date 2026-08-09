#!/usr/bin/env bash
# Rig for aae-orc-r4q6 / finding-017: time a marvel shift end to end,
# decomposed by stage, across role counts.
#
# Produces the 26-shift dataset in
# _kos/findings/finding-017-data-shift-timings.jsonl.
#
# SAFETY: the daemon runs under a throwaway HOME and a dedicated tmux
# socket NAME. Nothing here touches ~/.marvel, tmux -L default, or any
# other marvel-* server. Teardown kills only this rig's tmux server, by
# explicit -L name, and verifies by three negative checks.
#
# WHY AN EXPLICIT SHORT SOCKET: sun_path is 104 bytes. A layout-derived
# socket under a long HOME is rejected before it is created; see
# finding-013.
#
# Usage: scripts/rig-shift-timing.sh [tag]
set -uo pipefail

TAG="${1:-shiftrig}"
BASE="/tmp/mshift-$TAG"
export HOME="$BASE/home"
export MARVEL_SOCKET="$BASE/d.sock"
export MARVEL_TMUX_SOCKET="mshift-$TAG"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$BASE/results.jsonl"

mkdir -p "$HOME"
cd "$REPO"

hdr() { echo; echo "=== $* ==="; }

hdr "preflight: my tmux socket must not already exist"
if tmux -L "$MARVEL_TMUX_SOCKET" ls >/dev/null 2>&1; then
  echo "REFUSING: tmux server $MARVEL_TMUX_SOCKET already exists."
  echo "Pick another tag; another agent or an earlier run may own it."
  exit 1
fi
echo "clear"

hdr "build"
go build -o bin/marvel ./cmd/marvel/ || exit 1
go build -o bin/simulator ./cmd/simulator/ || exit 1

hdr "daemon"
nohup ./bin/marvel daemon --socket "$MARVEL_SOCKET" >"$BASE/daemon.log" 2>&1 &
DPID=$!
sleep 2
echo "daemon pid $DPID socket $MARVEL_SOCKET"

# Arm manifests are generated so the role count is the only variable.
mkarm() { # mkarm <workspace> <nroles> <gate>
  local ws="$1" n="$2" gate="$3" f="$BASE/$1.toml" i
  {
    echo "[workspace]"
    echo "name = \"$ws\""
    echo
    echo "[[team]]"
    echo 'name = "squad"'
    for i in $(seq 1 "$n"); do
      local role="r$i"
      [ "$i" = "$n" ] && [ "$n" -gt 1 ] && role="supervisor"
      echo
      echo "  [[team.role]]"
      echo "  name = \"$role\""
      echo "  replicas = 1"
      echo
      echo "    [team.role.runtime]"
      if [ "$gate" = "shell" ]; then
        echo '    image = "shell"'
        echo '    command = "sh"'
      else
        echo '    image = "simulator"'
        echo '    command = "bin/simulator"'
        echo '    args = ["--tick", "1000"]'
        echo
        echo "    [team.role.healthcheck]"
        echo '    type = "heartbeat"'
        echo '    timeout = "10s"'
        echo '    failure_threshold = 3'
      fi
    done
  } >"$f"
  echo "$f"
}

run_arm() { # run_arm <workspace> <nroles> <gate> <label> <n_shifts>
  local ws="$1" nroles="$2" gate="$3" label="$4" runs="$5"
  hdr "arm $label: $nroles role(s), gate=$gate, $runs shifts"
  ./bin/marvel work "$(mkarm "$ws" "$nroles" "$gate")" || return 1
  sleep 6
  local i
  for i in $(seq 1 "$runs"); do
    python3 "$REPO/scripts/shift_timing.py" "$ws" squad "${label}-$i" \
      | tee -a "$OUT"
    sleep 5
  done
}

run_arm lynxa 1 heartbeat A-sim-hb        7
run_arm lynxb 1 shell     B-shell-nohc    7
run_arm lynxc 2 heartbeat C-sim-2role     7
run_arm lynxe 4 heartbeat E-sim-4role     5

hdr "summary"
RESULTS="$OUT" python3 "$REPO/scripts/shift_timing_summary.py"

hdr "teardown"
./bin/marvel stop >/dev/null 2>&1
sleep 3
kill "$DPID" 2>/dev/null
sleep 2
tmux -L "$MARVEL_TMUX_SOCKET" kill-server 2>/dev/null
sleep 1

hdr "teardown verification (all three must report nothing)"
echo -n "processes on my socket: "
ps -eo pid,args | grep -F "$MARVEL_SOCKET" | grep -v grep || echo "none"
echo -n "processes in my workspaces: "
ps -eo pid,args | grep -E "workspace (lynxa|lynxb|lynxc|lynxe)" | grep -v grep || echo "none"
echo -n "my tmux server: "
tmux -L "$MARVEL_TMUX_SOCKET" ls 2>&1

echo
echo "results: $OUT"
