#!/usr/bin/env bash
# Rig verification for aae-orc-3st1.
#
# Extended for aae-orc-by6j: scenario 6.
#
# Verifies the behavior shipped in marvel #122 (socket through the layout,
# advisory lock), #123 (adopt-or-leave default, --reclaim, reap), and the
# layout-derived tmux socket name, with two daemons actually running at
# once, which is the only condition the original bugs appear under.
#
# Scenarios 0 to 5 set MARVEL_TMUX_SOCKET explicitly, including the
# deliberately shared server the fleet-kill case needs. Scenario 6 sets
# it nowhere, because the DEFAULT is what it measures.
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

# Scenario 6 helpers. `tmux ... | wc -l` reports 0 both for "server is
# live and empty" and for "there is no server", so a comparison against
# 0 passes without measuring anything. These report -1 for the absent
# server, which no expected count ever equals.
panes_on() {
  local out
  if ! out=$(tmux -L "$1" list-panes -a 2>/dev/null); then echo -1; return; fi
  printf '%s\n' "$out" | grep -c . | tr -d ' '
}
fmt_panes() { if (( $1 < 0 )); then echo "no server"; else echo "$1 pane(s)"; fi; }
sessions_on() {
  if ! tmux -L "$1" list-sessions -F '#S' 2>/dev/null; then return 1; fi
}
# The name marvel derives for a HOME: paths.Layout.TmuxSocketName().
# Reproduced here rather than asked of the binary on purpose. If marvel's
# derivation ever changes, the server this names stops existing and the
# scenario fails loudly instead of measuring a server nobody uses.
tmux_name_for_home() {
  printf 'marvel-%s' "$(printf '%s' "$1/.marvel" | shasum -a 256 | cut -c1-8)"
}

cleanup() {
  hdr "cleanup"
  for s in "$TMUX_A" "$TMUX_B" "$TMUX_S" \
           "$(tmux_name_for_home "$HOME_A")" "$(tmux_name_for_home "$HOME_B")"; do
    tmux -L "$s" kill-server 2>/dev/null
  done
  pkill -f 'mrig-marvel daemon' 2>/dev/null
  pkill -f mrig-sim 2>/dev/null
  sleep 1
  rm -rf "$HOME_A" "$HOME_B" "$SHARED_SOCK" "$SHARED_SOCK.lock" \
         /tmp/mrig-fleet.toml /tmp/mrig-fleet-beta.toml
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
# Scenario 2's fleet is still running on its own server. `marvel stop` is
# detach, not teardown, so without this the machine-global sims() count
# carries scenario 2's simulators into every later scenario's numbers.
tmux -L "$TMUX_A" kill-server 2>/dev/null
sleep 1

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
# Builds its own fleet from a clean slate rather than inheriting scenario
# 4's. Scenario 4 ends with `reap --confirm` having destroyed marvel-alpha,
# and daemon A does not respawn it (aae-orc-4bz2: the victim daemon
# records nothing), so re-applying the manifest against A's existing store
# produced no panes. The old check then measured scenario 2's leftover
# simulators on a different tmux server and read 2 -> 2 as a --reclaim
# failure. Verified 2026-08-07: --reclaim takes 2 simulators to 0 and logs
# the kill when the fleet it is aimed at actually exists.
HOME="$HOME_B" "$MARVEL" stop >/dev/null 2>&1
HOME="$HOME_A" "$MARVEL" stop >/dev/null 2>&1
sleep 2
tmux -L "$TMUX_S" kill-server 2>/dev/null
rm -rf "$HOME_A"; mkdir -p "$HOME_A"
HOME="$HOME_A" MARVEL_TMUX_SOCKET="$TMUX_S" "$MARVEL" daemon \
  --log-file="$HOME_A/d3.log" >"$HOME_A/out3.log" 2>&1 &
sleep 2
HOME="$HOME_A" "$MARVEL" work /tmp/mrig-fleet.toml >/dev/null 2>&1
sleep 8

# Pane counts on the target server, not the machine-global process count:
# panes_on reports -1 for an absent server, so no expected value is
# reachable by accident.
pre_reclaim=$(panes_on "$TMUX_S")
if (( pre_reclaim > 0 )); then
  ok "precondition: A has $pre_reclaim pane(s) on the shared server to reclaim"
else
  bad "precondition failed, nothing to reclaim: $pre_reclaim pane(s)"
fi
HOME="$HOME_B" MARVEL_TMUX_SOCKET="$TMUX_S" "$MARVEL" daemon --reclaim \
  --log-file="$HOME_B/d2.log" >"$HOME_B/out2.log" 2>&1 &
sleep 7
post_reclaim=$(panes_on "$TMUX_S")
if (( pre_reclaim > 0 && post_reclaim < pre_reclaim )); then
  ok "--reclaim destroyed the fleet on purpose: $pre_reclaim pane(s) -> $(fmt_panes "$post_reclaim")"
else
  bad "--reclaim did not destroy: $pre_reclaim -> $(fmt_panes "$post_reclaim")"
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

# --------------------------------------------------------------- scenario 6
# The one aae-orc-3st1 could not run: finding-012 verified survivability
# on a SHARED tmux server, not isolation. This is the isolation claim.
hdr "6. tmux server isolation by HOME, no MARVEL_TMUX_SOCKET anywhere (#by6j)"
HOME="$HOME_A" "$MARVEL" stop >/dev/null 2>&1
HOME="$HOME_B" "$MARVEL" stop >/dev/null 2>&1
sleep 2
tmux -L "$TMUX_A" kill-server 2>/dev/null
tmux -L "$TMUX_S" kill-server 2>/dev/null
rm -rf "$HOME_A" "$HOME_B"; mkdir -p "$HOME_A" "$HOME_B"

sed 's/name = "alpha"/name = "beta"/' /tmp/mrig-fleet.toml > /tmp/mrig-fleet-beta.toml

NAME_A=$(tmux_name_for_home "$HOME_A")
NAME_B=$(tmux_name_for_home "$HOME_B")
echo "  derived: $HOME_A -> $NAME_A"
echo "  derived: $HOME_B -> $NAME_B"
if [[ "$NAME_A" != "$NAME_B" && -n "$NAME_A" ]]; then
  ok "two HOMEs derive two different tmux server names"
else
  bad "derivation collapsed: A=$NAME_A B=$NAME_B"
fi

# Note the absence of MARVEL_TMUX_SOCKET on both. That is the point.
HOME="$HOME_A" "$MARVEL" daemon --log-file="$HOME_A/d.log" >"$HOME_A/out.log" 2>&1 &
sleep 2
HOME="$HOME_A" "$MARVEL" work /tmp/mrig-fleet.toml >/dev/null 2>&1
sleep 7
HOME="$HOME_B" "$MARVEL" daemon --log-file="$HOME_B/d.log" >"$HOME_B/out.log" 2>&1 &
sleep 2
HOME="$HOME_B" "$MARVEL" work /tmp/mrig-fleet-beta.toml >/dev/null 2>&1
sleep 7

pa=$(panes_on "$NAME_A")
pb=$(panes_on "$NAME_B")
echo "  panes: $NAME_A=$pa  $NAME_B=$pb  (-1 means no such server)"
if (( pa > 0 && pb > 0 )); then
  ok "each HOME started its own tmux server, both populated ($pa and $pb panes)"
else
  bad "expected two populated servers, got A=$pa B=$pb"
fi

sess_a=$(sessions_on "$NAME_A" | sort | tr '\n' ' ')
sess_b=$(sessions_on "$NAME_B" | sort | tr '\n' ' ')
echo "  sessions on $NAME_A: $sess_a"
echo "  sessions on $NAME_B: $sess_b"
if grep -q 'marvel-alpha' <<<"$sess_a" && ! grep -q 'marvel-beta' <<<"$sess_a"; then
  ok "A's server carries marvel-alpha and not B's marvel-beta"
else
  bad "A's server is wrong: $sess_a"
fi
if grep -q 'marvel-beta' <<<"$sess_b" && ! grep -q 'marvel-alpha' <<<"$sess_b"; then
  ok "B's server carries marvel-beta and not A's marvel-alpha"
else
  bad "B's server is wrong: $sess_b"
fi

both=$(sims)
if (( both == 4 )); then
  ok "both fleets alive: $both simulators (2 per daemon)"
else
  bad "expected 4 simulators across two daemons, got $both"
fi

a_view=$(HOME="$HOME_A" "$MARVEL" get sessions 2>/dev/null)
b_view=$(HOME="$HOME_B" "$MARVEL" get sessions 2>/dev/null)
if grep -q alpha <<<"$a_view" && ! grep -q beta <<<"$a_view"; then
  ok "A's session table shows alpha only"
else
  bad "A's session table: $a_view"
fi
if grep -q beta <<<"$b_view" && ! grep -q alpha <<<"$b_view"; then
  ok "B's session table shows beta only"
else
  bad "B's session table: $b_view"
fi

# The discriminator between "isolated" and "shared but leaving alone".
# On a shared server B would list A's marvel-alpha as a reap candidate and
# log reconcile.left for it. On its own server it cannot see alpha at all.
#
# The assertion is "no alpha", not "nothing to reap": reap legitimately
# lists one pane per session that marvel does not record, the shell pane
# tmux creates with the session itself before marvel splits replicas into
# it. That is pre-existing and unrelated to isolation (a 2-replica role
# produces 3 panes throughout this rig), so "nothing to reap" would fail
# for a reason this scenario is not about.
reap_b=$(HOME="$HOME_B" "$MARVEL" reap 2>&1)
echo "$reap_b" | sed 's/^/      /'
if ! grep -qE 'unrecorded|Nothing to reap' <<<"$reap_b"; then
  bad "B's reap produced no listing at all, so it measured nothing: $reap_b"
elif grep -q 'alpha' <<<"$reap_b"; then
  bad "B can still see A's tmux state: $reap_b"
else
  ok "B's reap candidates contain nothing from workspace alpha: A's fleet is invisible"
fi

if [[ ! -s "$HOME_B/d.log" ]]; then
  bad "B has no daemon log, so the left-running check would pass vacuously"
elif grep -q "left .* running" "$HOME_B/d.log"; then
  bad "B still met A's sessions: $(grep -m1 'left .* running' "$HOME_B/d.log")"
else
  ok "B logged no left-running line (nothing foreign was on its server)"
fi

if tmux -L default list-sessions 2>/dev/null | grep -q '^marvel-'; then
  bad "a marvel session landed on the shared default tmux server"
else
  ok "the shared default tmux server carries no marvel sessions"
fi

# Stability. A derived name that moved between restarts would orphan the
# fleet it just created, which is the failure the sha256-of-HOME shape
# exists to prevent.
HOME="$HOME_A" "$MARVEL" stop >/dev/null 2>&1
sleep 2
alive=$(sims)
HOME="$HOME_A" "$MARVEL" daemon --log-file="$HOME_A/d2.log" >"$HOME_A/out2.log" 2>&1 &
sleep 6
if grep -q "adopted pane" "$HOME_A/d2.log" 2>/dev/null; then
  ok "restarted A found the same server and adopted $(grep -c 'adopted pane' "$HOME_A/d2.log") pane(s)"
else
  bad "restarted A adopted nothing; the derived name moved. Log: $(tail -5 "$HOME_A/d2.log" 2>&1)"
fi
if (( $(sims) == alive && alive > 0 )); then
  ok "no agent lost across the restart ($alive alive)"
else
  bad "agents lost across restart: $alive -> $(sims)"
fi

HOME="$HOME_A" "$MARVEL" stop >/dev/null 2>&1
HOME="$HOME_B" "$MARVEL" stop >/dev/null 2>&1
sleep 2

hdr "result"
echo "  $PASS passed, $FAIL failed"
exit $(( FAIL > 0 ? 1 : 0 ))
