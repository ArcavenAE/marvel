#!/usr/bin/env bash
# Rig verification for aae-orc-3st1.
#
# Verifies the behavior shipped in marvel #122 (socket through the layout,
# advisory lock) and #123 (adopt-or-leave default, --reclaim, reap) with
# two daemons actually running at once, which is the only condition the
# original bugs appear under.
#
# SAFETY: every daemon runs under a throwaway HOME in /tmp and a dedicated
# tmux socket NAME. Nothing touches the real ~/.marvel, whose bolt still
# holds the 2026-08-06 demo's desired state.
#
# WHY /tmp AND NOT THE SCRATCHPAD: sun_path is 104 bytes. The session
# scratchpad prefix alone measures 109, so a layout-derived socket under it
# is rejected before it is created. Scenario 0 demonstrates exactly that.
set -uo pipefail

MARVEL=/tmp/mrig-marvel
SIM=/tmp/mrig-sim
HOME_A=/tmp/mrig-a
HOME_B=/tmp/mrig-b
TMUX_A=mxA
TMUX_B=mxB
TMUX_S=mxS          # deliberately shared, for the fleet-kill scenario
SHARED_SOCK=/tmp/mrig-shared.sock

PASS=0
FAIL=0

ok()   { echo "  PASS: $*"; PASS=$((PASS+1)); }
bad()  { echo "  FAIL: $*"; FAIL=$((FAIL+1)); }
hdr()  { echo; echo "=== $* ==="; }
# NOT `pgrep -fc`: -c is not a pgrep flag on macOS, and the first run of
# this rig used it, so every process count was garbage and two scenarios
# measured nothing. Count lines instead.
sims() { pgrep -f "$SIM" 2>/dev/null | wc -l | tr -d ' '; }

cleanup() {
  hdr "cleanup"
  for s in "$TMUX_A" "$TMUX_B" "$TMUX_S"; do
    tmux -L "$s" kill-server 2>/dev/null
  done
  pkill -f 'mrig-marvel daemon' 2>/dev/null
  pkill -f mrig-sim 2>/dev/null
  sleep 1
  rm -rf "$HOME_A" "$HOME_B" "$SHARED_SOCK" "$SHARED_SOCK.lock"
  echo "  torn down"
}
trap cleanup EXIT

mkdir -p "$HOME_A" "$HOME_B"

cat > /tmp/mrig-fleet.toml <<EOF
[workspace]
name = "alpha"

[[team]]
name = "squad"

  [[team.role]]
  name = "worker"
  replicas = 2

    [team.role.runtime]
    image = "simulator"
    command = "$SIM"
    args = ["--tick", "3000"]
EOF

# ---------------------------------------------------------------- scenario 0
hdr "0. path-length assertion (paths.CheckSocketPath, #122 item 5)"
LONGHOME=/private/tmp/claude-501/-Users-michael-pursifull-work-aae-orc/8f433f41-bdba-4aba-9454-abdd716cb2ca/scratchpad/rigL
mkdir -p "$LONGHOME"
out=$(HOME="$LONGHOME" "$MARVEL" daemon --state-bolt= --pidfile= --log-file= 2>&1 | head -3)
if grep -qi "unix socket" <<<"$out" && grep -q "104" <<<"$out"; then
  ok "over-long layout socket refused, error names the limit: $(head -1 <<<"$out")"
else
  bad "expected a 104-byte refusal, got: $out"
fi
rm -rf "$LONGHOME"

# ---------------------------------------------------------------- scenario 1
hdr "1. socket lock: two daemons, SAME explicit socket (#122 item 2)"
HOME="$HOME_A" MARVEL_TMUX_SOCKET="$TMUX_A" "$MARVEL" daemon \
  --socket "$SHARED_SOCK" --log-file="$HOME_A/d.log" >"$HOME_A/out.log" 2>&1 &
A_PID=$!
sleep 3

if HOME="$HOME_A" "$MARVEL" --socket "$SHARED_SOCK" get sessions >/dev/null 2>&1; then
  ok "daemon A is up and reachable on the shared path"
else
  bad "daemon A did not come up"
fi

berr=$(HOME="$HOME_B" MARVEL_TMUX_SOCKET="$TMUX_B" "$MARVEL" daemon \
  --socket "$SHARED_SOCK" --log-file="$HOME_B/d.log" 2>&1 | head -3)
if grep -qi "another marvel daemon" <<<"$berr"; then
  ok "daemon B refused: $(head -1 <<<"$berr")"
else
  bad "daemon B did NOT refuse. Output: $berr"
fi

# The stranding case: pre-fix, B's exit unlinked A's socket.
if HOME="$HOME_A" "$MARVEL" --socket "$SHARED_SOCK" get sessions >/dev/null 2>&1; then
  ok "daemon A still reachable after B's attempt and exit (not stranded)"
else
  bad "daemon A was stranded by B"
fi
if [[ -S "$SHARED_SOCK" ]]; then
  ok "A's socket file still exists"
else
  bad "A's socket file was unlinked"
fi

HOME="$HOME_A" "$MARVEL" --socket "$SHARED_SOCK" stop >/dev/null 2>&1
wait $A_PID 2>/dev/null
sleep 1

# ---------------------------------------------------------------- scenario 2
hdr "2. socket isolation by HOME, no --socket anywhere (#122 item 1)"
HOME="$HOME_A" MARVEL_TMUX_SOCKET="$TMUX_A" "$MARVEL" daemon \
  --log-file="$HOME_A/d.log" >"$HOME_A/out.log" 2>&1 &
sleep 2
HOME="$HOME_B" MARVEL_TMUX_SOCKET="$TMUX_B" "$MARVEL" daemon \
  --log-file="$HOME_B/d.log" >"$HOME_B/out.log" 2>&1 &
sleep 3

if [[ -S "$HOME_A/.marvel/run/marvel.sock" && -S "$HOME_B/.marvel/run/marvel.sock" ]]; then
  ok "each HOME got its own socket, both live"
else
  bad "expected two layout-derived sockets; A=$(ls "$HOME_A/.marvel/run/" 2>&1) B=$(ls "$HOME_B/.marvel/run/" 2>&1)"
fi

# Both daemons must be independently reachable, and each must see only itself.
HOME="$HOME_A" "$MARVEL" work /tmp/mrig-fleet.toml >/dev/null 2>&1
sleep 6
a_sess=$(HOME="$HOME_A" "$MARVEL" get sessions 2>/dev/null | grep -c alpha)
b_sess=$(HOME="$HOME_B" "$MARVEL" get sessions 2>/dev/null | grep -c alpha)
if (( a_sess > 0 && b_sess == 0 )); then
  ok "A sees its $a_sess session(s); B sees none of them (no cross-talk)"
else
  bad "cross-talk or missing fleet: A=$a_sess B=$b_sess"
fi

# ---------------------------------------------------------------- scenario 3
hdr "3. adopt-or-leave: B joins A's tmux server (#123, the fleet-kill case)"
HOME="$HOME_A" "$MARVEL" stop >/dev/null 2>&1
HOME="$HOME_B" "$MARVEL" stop >/dev/null 2>&1
sleep 2
rm -rf "$HOME_A" "$HOME_B"; mkdir -p "$HOME_A" "$HOME_B"

HOME="$HOME_A" MARVEL_TMUX_SOCKET="$TMUX_S" "$MARVEL" daemon \
  --log-file="$HOME_A/d.log" >"$HOME_A/out.log" 2>&1 &
sleep 2
HOME="$HOME_A" "$MARVEL" work /tmp/mrig-fleet.toml >/dev/null 2>&1
sleep 7

before=$(tmux -L "$TMUX_S" list-panes -a 2>/dev/null | wc -l | tr -d ' ')
sim_before=$(sims)
echo "  before: $before pane(s) on the shared tmux server, $sim_before simulator process(es)"

# B: different HOME, its own socket, but the SAME tmux server. This is the
# exact configuration that destroyed A's fleet on 2026-08-06.
HOME="$HOME_B" MARVEL_TMUX_SOCKET="$TMUX_S" "$MARVEL" daemon \
  --log-file="$HOME_B/d.log" >"$HOME_B/out.log" 2>&1 &
sleep 6

after=$(tmux -L "$TMUX_S" list-panes -a 2>/dev/null | wc -l | tr -d ' ')
sim_after=$(sims)
echo "  after:  $after pane(s), $sim_after simulator process(es)"

if [[ "$after" == "$before" && "$sim_after" == "$sim_before" ]]; then
  ok "A's fleet SURVIVED daemon B starting on the same tmux server"
else
  bad "fleet changed: panes $before -> $after, sims $sim_before -> $sim_after"
fi

a_state=$(HOME="$HOME_A" "$MARVEL" get sessions 2>/dev/null | grep -c -i running)
if (( a_state > 0 )); then
  ok "A still reports $a_state running session(s)"
else
  bad "A reports no running sessions"
fi

if grep -q "left .* running" "$HOME_B/d.log" 2>/dev/null; then
  ok "B logged what it left: $(grep -m1 'left .* running' "$HOME_B/d.log" | sed 's/^.*AdoptOrLeave/AdoptOrLeave/')"
else
  bad "B did not log a left-running line. Log: $(tail -5 "$HOME_B/d.log" 2>&1)"
fi

if HOME="$HOME_B" "$MARVEL" events 2>/dev/null | grep -q "reconcile.left"; then
  ok "reconcile.left present in B's event ring"
  HOME="$HOME_B" "$MARVEL" events 2>/dev/null | grep "reconcile.left" | head -2 | sed 's/^/      /'
else
  bad "no reconcile.left event. Events: $(HOME="$HOME_B" "$MARVEL" events 2>&1 | tail -3)"
fi

# ---------------------------------------------------------------- scenario 4
hdr "4. reap: lists without destroying, destroys only on --confirm (#123)"
reap_out=$(HOME="$HOME_B" "$MARVEL" reap 2>&1)
echo "$reap_out" | sed 's/^/      /'
mid=$(tmux -L "$TMUX_S" list-panes -a 2>/dev/null | wc -l | tr -d ' ')
if [[ "$mid" == "$after" ]]; then
  ok "bare reap destroyed nothing ($mid pane(s) still up)"
else
  bad "bare reap changed the fleet: $after -> $mid"
fi
if grep -qi "another running daemon" <<<"$reap_out"; then
  ok "reap warns the candidates may belong to another daemon"
else
  bad "reap did not warn about live owners"
fi

HOME="$HOME_B" "$MARVEL" reap --confirm >/dev/null 2>&1
sleep 3
post=$(tmux -L "$TMUX_S" list-panes -a 2>/dev/null | wc -l | tr -d ' ')
if (( post < after )); then
  ok "reap --confirm destroyed the unrecorded state ($after -> $post pane(s))"
else
  bad "reap --confirm did not destroy: $after -> $post"
fi

# ---------------------------------------------------------------- scenario 4b
hdr "4b. --reclaim still destroys, deliberately"
HOME="$HOME_B" "$MARVEL" stop >/dev/null 2>&1
sleep 2
HOME="$HOME_A" "$MARVEL" work /tmp/mrig-fleet.toml >/dev/null 2>&1
sleep 8
pre_reclaim=$(sims)
HOME="$HOME_B" MARVEL_TMUX_SOCKET="$TMUX_S" "$MARVEL" daemon --reclaim \
  --log-file="$HOME_B/d2.log" >"$HOME_B/out2.log" 2>&1 &
sleep 7
post_reclaim=$(sims)
if (( pre_reclaim > 0 && post_reclaim < pre_reclaim )); then
  ok "--reclaim destroyed the fleet on purpose: $pre_reclaim -> $post_reclaim simulators"
else
  bad "--reclaim did not destroy: $pre_reclaim -> $post_reclaim"
fi
if grep -q "killed" "$HOME_B/d2.log" 2>/dev/null; then
  ok "reclaim logged the kill with its own identity"
else
  bad "reclaim did not log a kill"
fi

# ---------------------------------------------------------------- scenario 5
hdr "5. adopt-on-restart: same HOME re-adopts its own fleet (#123 regression guard)"
HOME="$HOME_A" "$MARVEL" stop >/dev/null 2>&1
HOME="$HOME_B" "$MARVEL" stop >/dev/null 2>&1
sleep 2
tmux -L "$TMUX_S" kill-server 2>/dev/null
rm -rf "$HOME_A"; mkdir -p "$HOME_A"

HOME="$HOME_A" MARVEL_TMUX_SOCKET="$TMUX_A" "$MARVEL" daemon \
  --log-file="$HOME_A/d.log" >"$HOME_A/out.log" 2>&1 &
sleep 2
HOME="$HOME_A" "$MARVEL" work /tmp/mrig-fleet.toml >/dev/null 2>&1
sleep 7
pre_restart=$(sims)

# Detach (agents keep running), then start again against the same HOME.
HOME="$HOME_A" "$MARVEL" stop >/dev/null 2>&1
sleep 2
survived=$(sims)
if (( survived == pre_restart && survived > 0 )); then
  ok "agents survived daemon stop ($survived alive)"
else
  bad "agents did not survive stop: $pre_restart -> $survived"
fi

HOME="$HOME_A" MARVEL_TMUX_SOCKET="$TMUX_A" "$MARVEL" daemon \
  --log-file="$HOME_A/d2.log" >"$HOME_A/out2.log" 2>&1 &
sleep 6
if grep -q "adopted pane" "$HOME_A/d2.log" 2>/dev/null; then
  ok "restarted daemon adopted its own panes: $(grep -c 'adopted pane' "$HOME_A/d2.log") adoption(s)"
else
  bad "no adoption on restart. Log: $(tail -5 "$HOME_A/d2.log" 2>&1)"
fi
readopted=$(sims)
if (( readopted == survived )); then
  ok "no agent lost across the restart ($readopted alive)"
else
  bad "agents lost across restart: $survived -> $readopted"
fi

hdr "result"
echo "  $PASS passed, $FAIL failed"
exit $(( FAIL > 0 ? 1 : 0 ))
