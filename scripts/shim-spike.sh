#!/usr/bin/env bash
# Driver for the marvel-shim spike (aae-orc-e35c). One subcommand per
# pre-declared success signal, plus a latency comparison.
#
#   scripts/shim-spike.sh all
#   scripts/shim-spike.sh s1        # resize / SIGWINCH through the double PTY
#   scripts/shim-spike.sh s2        # stream-socket fidelity under fast output
#   scripts/shim-spike.sh s3        # concurrent inject via the PTY master
#   scripts/shim-spike.sh s4        # pane kill and supervisor disconnect
#   scripts/shim-spike.sh s5        # falsification: Cursor-class TTY hang
#   scripts/shim-spike.sh h claude  # a real harness TUI, plain vs under the shim
#   scripts/shim-spike.sh rtt       # double-PTY latency overhead
#
# Every tmux session runs on a private server socket (-L shimspike) so the
# operator's own sessions are never touched.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="${SHIM_SPIKE_WORK:-/tmp/shimspike}"
BIN="$WORK/bin"
RUN="$WORK/run"
TMUX=(tmux -L shimspike)

SHIM="$BIN/marvel-shim"
PROBE="$BIN/shimprobe"

build() {
  mkdir -p "$BIN" "$RUN"
  (cd "$ROOT" && go build -o "$BIN/" ./cmd/marvel-shim ./cmd/shimprobe)
}

# tmux keeps a dead pane's text only with remain-on-exit, which every signal
# here needs because the evidence is what the child printed before exiting.
new_session() {
  local name="$1" width="$2" height="$3"
  shift 3
  "${TMUX[@]}" kill-session -t "$name" 2>/dev/null || true
  "${TMUX[@]}" new-session -d -s "$name" -x "$width" -y "$height" "$@"
  "${TMUX[@]}" set-option -t "$name" -w remain-on-exit on
}

capture() { "${TMUX[@]}" capture-pane -p -t "$1"; }

kill_session() { "${TMUX[@]}" kill-session -t "$1" 2>/dev/null || true; }

banner() { printf '\n===== %s =====\n' "$*"; }

alive() { kill -0 "$1" 2>/dev/null && echo yes || echo no; }

# pids extracts one field from a `shimprobe ctl` reply line ("CTL {json}").
pids() { python3 -c 'import sys,json;print(json.loads(sys.argv[1].split(" ",1)[1]).get(sys.argv[2],0))' "$1" "$2"; }

# ---------------------------------------------------------------- signal 1
s1() {
  banner "SIGNAL 1a: SIGWINCH reaches the child through two PTYs"
  local c="$RUN/s1.ctl" s="$RUN/s1.str"
  rm -f "$c" "$s"
  new_session s1 100 30 "$SHIM --control $c --stream $s -- $PROBE winsize -for 25s"
  sleep 1
  echo "--- initial (session created 100x30) ---"
  capture s1
  "${TMUX[@]}" resize-window -t s1 -x 120 -y 40
  sleep 1
  "${TMUX[@]}" resize-window -t s1 -x 70 -y 20
  sleep 1
  echo "--- after two resizes (120x40 then 70x20) ---"
  capture s1
  kill_session s1

  banner "SIGNAL 1b: a real TUI (vim) renders and redraws on resize"
  local c2="$RUN/s1b.ctl" s2="$RUN/s1b.str"
  rm -f "$c2" "$s2"
  new_session s1b 100 30 "$SHIM --control $c2 --stream $s2 -- vim -u NONE -c 'set nocompatible' -c 'set ruler' -c 'set laststatus=2'"
  sleep 1.5
  "${TMUX[@]}" send-keys -t s1b "ihello from vim" Escape
  sleep 0.7
  echo "--- vim at 100x30, last 4 lines of pane ---"
  capture s1b | tail -4
  echo "--- vim reported columns (:echo &columns via ruler is unreliable; ask directly) ---"
  "${TMUX[@]}" send-keys -t s1b ':echo "COLS=".&columns." LINES=".&lines' Enter
  sleep 0.7
  capture s1b | grep -E 'COLS=' || echo "(no COLS line captured)"
  "${TMUX[@]}" resize-window -t s1b -x 132 -y 43
  sleep 1
  "${TMUX[@]}" send-keys -t s1b ':echo "COLS=".&columns." LINES=".&lines' Enter
  sleep 0.7
  echo "--- after resize to 132x43 ---"
  capture s1b | grep -E 'COLS=' || echo "(no COLS line captured)"
  "${TMUX[@]}" send-keys -t s1b ':q!' Enter
  sleep 0.5
  kill_session s1b
}

# ---------------------------------------------------------------- signal 2
s2() {
  local n="${1:-50000}"
  banner "SIGNAL 2: stream-socket fidelity, $n lines"
  local c="$RUN/s2.ctl" s="$RUN/s2.str" gate="$RUN/s2.gate"
  rm -f "$c" "$s" "$gate"
  # The gate makes the run deterministic: there is no replay buffer, so the
  # consumer has to be attached before the child's first byte.
  new_session s2 200 50 "$SHIM --control $c --stream $s -- sh -c 'while [ ! -e $gate ]; do sleep 0.05; done; exec $PROBE spew -n $n'"
  for _ in $(seq 1 100); do [ -S "$s" ] && break; sleep 0.05; done

  "$PROBE" sink -s "$s" >"$RUN/s2.sink" 2>&1 &
  local sinkpid=$!
  sleep 0.3
  local t0 t1
  t0=$(python3 -c 'import time;print(time.time())')
  : >"$gate"
  wait "$sinkpid" || true
  t1=$(python3 -c 'import time;print(time.time())')
  cat "$RUN/s2.sink"
  python3 -c "print('SINK elapsed=%.3fs' % ($t1-$t0))"
  echo "--- pane tail (proves the human view got the same bytes) ---"
  capture s2 | tail -2
  kill_session s2

  # 1000 lines at 5ms is ~5s of consumer lag, inside the shim's 10s drain
  # grace. A consumer slower than that grace loses the tail by design.
  banner "SIGNAL 2b: lagging consumer (5ms per line) with the same producer"
  local c2="$RUN/s2b.ctl" s2s="$RUN/s2b.str" gate2="$RUN/s2b.gate"
  rm -f "$c2" "$s2s" "$gate2"
  new_session s2b 200 50 "$SHIM --control $c2 --stream $s2s -- sh -c 'while [ ! -e $gate2 ]; do sleep 0.05; done; exec $PROBE spew -n 1000'"
  for _ in $(seq 1 100); do [ -S "$s2s" ] && break; sleep 0.05; done
  "$PROBE" sink -s "$s2s" -slow 5ms >"$RUN/s2b.sink" 2>&1 &
  local sp=$!
  sleep 0.3
  : >"$gate2"
  wait "$sp" || true
  cat "$RUN/s2b.sink"
  kill_session s2b
}

# ---------------------------------------------------------------- signal 3
s3() {
  local reps="${1:-300}" width="${2:-120}" mode="${3:-cooked}"
  local echoflag=""
  [ "$mode" = raw ] && echoflag="-raw"
  banner "SIGNAL 3: two supervisors injecting concurrently, $reps lines of $width bytes, child stdin $mode"
  local c="$RUN/s3.ctl" s="$RUN/s3.str"
  rm -f "$c" "$s" "$RUN/s3.raw"
  new_session s3 200 50 "$SHIM --control $c --stream $s -- $PROBE echo -for 90s $echoflag"
  for _ in $(seq 1 100); do [ -S "$s" ] && break; sleep 0.05; done

  "$PROBE" tee -s "$s" -o "$RUN/s3.raw" &
  local teepid=$!
  sleep 0.3

  # Payload width matters: a single write() to the PTY master is atomic only
  # while it fits the input queue, so this is the knob that finds the bound.
  local a b
  a=$(python3 -c "print('A'*$width)")
  b=$(python3 -c "print('B'*$width)")
  "$PROBE" ctl -c "$c" -cmd inject -data "${a}\\n" -repeat "$reps" >/dev/null &
  local pa=$!
  "$PROBE" ctl -c "$c" -cmd inject -data "${b}\\n" -repeat "$reps" >/dev/null &
  local pb=$!
  wait "$pa" "$pb"
  sleep 1.5
  "$PROBE" ctl -c "$c" -cmd inject -data 'QUIT\n' >/dev/null || true
  sleep 0.7
  kill "$teepid" 2>/dev/null || true
  wait "$teepid" 2>/dev/null || true

  python3 - "$RUN/s3.raw" "$reps" "$width" <<'PY'
import re, sys
raw = open(sys.argv[1], 'rb').read().decode('utf-8', 'replace')
reps = int(sys.argv[2])
echo = re.findall(r'ECHO \d+ \[([^\]]*)\]', raw)
pure_a = sum(1 for e in echo if e and set(e) == {'A'})
pure_b = sum(1 for e in echo if e and set(e) == {'B'})
mixed  = [e for e in echo if set(e) & {'A'} and set(e) & {'B'}]
width = int(sys.argv[3])
short  = [e for e in echo if e and set(e) <= {'A', 'B'} and len(e) != width]
print(f"INJECT echoed_lines={len(echo)} pure_A={pure_a} pure_B={pure_b} "
      f"expected_each={reps} mixed={len(mixed)} wrong_length={len(short)}")
if mixed:
    print("INJECT first_mixed=" + repr(mixed[0][:160]))
if short:
    print("INJECT first_wrong_length=" + repr(short[0][:160]))
ok = pure_a == reps and pure_b == reps and not mixed and not short
print("INJECT verdict=" + ("PASS" if ok else "FAIL"))
PY
  kill_session s3
}

# ---------------------------------------------------------------- signal 4
s4() {
  banner "SIGNAL 4a: kill the tmux pane, default --on-hup=kill"
  local c="$RUN/s4a.ctl" s="$RUN/s4a.str"
  rm -f "$c" "$s"
  new_session s4a 100 30 "$SHIM --control $c --stream $s -- sh -c 'while :; do echo tick; sleep 1; done'"
  for _ in $(seq 1 100); do [ -S "$c" ] && break; sleep 0.05; done
  local st cpid spid
  st=$("$PROBE" ctl -c "$c" -cmd status)
  echo "before kill: $st"
  cpid=$(pids "$st" pid)
  spid=$(pids "$st" shim_pid)
  echo "shim pid=$spid child pid=$cpid"
  "${TMUX[@]}" kill-session -t s4a
  sleep 2
  echo "after kill-session (+2s): shim alive=$(alive "$spid") child alive=$(alive "$cpid")"
  echo "control socket reachable=$("$PROBE" ctl -c "$c" -cmd status 2>&1 | head -1)"
  pkill -f "marvel-shim --control $c" 2>/dev/null || true
  kill "$cpid" 2>/dev/null || true

  banner "SIGNAL 4b: kill the tmux pane, --on-hup=detach"
  local c2="$RUN/s4b.ctl" s2s="$RUN/s4b.str"
  rm -f "$c2" "$s2s"
  new_session s4b 100 30 "$SHIM --on-hup detach --control $c2 --stream $s2s -- sh -c 'while :; do echo tick; sleep 1; done'"
  for _ in $(seq 1 100); do [ -S "$c2" ] && break; sleep 0.05; done
  local st2 cpid2 spid2
  st2=$("$PROBE" ctl -c "$c2" -cmd status)
  cpid2=$(pids "$st2" pid)
  spid2=$(pids "$st2" shim_pid)
  echo "shim pid=$spid2 child pid=$cpid2"
  "${TMUX[@]}" kill-session -t s4b
  sleep 2
  echo "after kill-session (+2s): shim alive=$(alive "$spid2") child alive=$(alive "$cpid2")"
  echo "control reply: $("$PROBE" ctl -c "$c2" -cmd status 2>&1 | head -1)"
  sleep 5
  echo "at +7s (child has tried ~7 tty writes since the pane went away):"
  echo "  shim alive=$(alive "$spid2") child alive=$(alive "$cpid2")"
  echo "  control reply: $("$PROBE" ctl -c "$c2" -cmd status 2>&1 | head -1)"
  pkill -f "marvel-shim --on-hup detach --control $c2" 2>/dev/null || true
  kill "$cpid2" 2>/dev/null || true

  banner "SIGNAL 4c: supervisor connection dies and reconnects"
  local c3="$RUN/s4c.ctl" s3s="$RUN/s4c.str"
  rm -f "$c3" "$s3s"
  new_session s4c 100 30 "$SHIM --control $c3 --stream $s3s -- $PROBE echo -for 90s"
  for _ in $(seq 1 100); do [ -S "$c3" ] && break; sleep 0.05; done
  "$PROBE" ctl -c "$c3" -cmd status -hold 30s >/dev/null &
  local hp=$!
  "$PROBE" tee -s "$s3s" -o "$RUN/s4c.raw" &
  local tp=$!
  sleep 0.5
  echo "with supervisor attached: $("$PROBE" ctl -c "$c3" -cmd status)"
  kill -9 "$hp" "$tp" 2>/dev/null || true
  wait "$hp" "$tp" 2>/dev/null || true
  sleep 0.7
  echo "after SIGKILL of both supervisor connections: $("$PROBE" ctl -c "$c3" -cmd status)"
  "$PROBE" tee -s "$s3s" -o "$RUN/s4c2.raw" &
  local tp2=$!
  sleep 0.3
  "$PROBE" ctl -c "$c3" -cmd inject -data 'after-reconnect\n' >/dev/null
  sleep 0.7
  kill "$tp2" 2>/dev/null || true
  wait "$tp2" 2>/dev/null || true
  echo "reconnected stream saw: $(tr -d '\r' <"$RUN/s4c2.raw" | grep -c 'after-reconnect' || true) matching lines"
  echo "pane still rendering:"
  capture s4c | grep -c . | sed 's/^/  non-blank pane lines: /'
  capture s4c | grep -E 'after-reconnect|ECHO' | tail -3
  kill_session s4c
}

# ---------------------------------------------------------------- signal 5
s5() {
  banner "SIGNAL 5 (falsification): Cursor-class terminal-query hang"

  echo "--- 5a: no PTY at all (pipe with nothing behind it), the failure being emulated ---"
  ( sleep 10 | "$PROBE" ttyprobe -timeout 3s 2>&1 ) | tr -d '\r' | grep TTYPROBE || true

  echo "--- 5b: plain tmux pane, no shim, raw (what a real harness does) ---"
  new_session s5b 100 30 "$PROBE ttyprobe -timeout 3s -raw"
  sleep 4.5
  capture s5b | tr -d '\r' | grep TTYPROBE || echo "(nothing captured)"
  kill_session s5b

  echo "--- 5c: under marvel-shim in a tmux pane, raw ---"
  local c="$RUN/s5c.ctl" s="$RUN/s5c.str"
  rm -f "$c" "$s"
  new_session s5c 100 30 "$SHIM --quiet --control $c --stream $s -- $PROBE ttyprobe -timeout 3s -raw"
  sleep 4.5
  capture s5c | tr -d '\r' | grep TTYPROBE || echo "(nothing captured)"
  kill_session s5c

  echo "--- 5d: plain tmux pane, cooked stdin (line discipline holds the reply) ---"
  new_session s5d 100 30 "$PROBE ttyprobe -timeout 3s -raw=false"
  sleep 4.5
  capture s5d | tr -d '\r' | grep TTYPROBE || echo "(nothing captured)"
  kill_session s5d

  echo "--- 5e: under marvel-shim, cooked stdin ---"
  local c2="$RUN/s5e.ctl" s2s="$RUN/s5e.str"
  rm -f "$c2" "$s2s"
  new_session s5e 100 30 "$SHIM --quiet --control $c2 --stream $s2s -- $PROBE ttyprobe -timeout 3s -raw=false"
  sleep 4.5
  capture s5e | tr -d '\r' | grep TTYPROBE || echo "(nothing captured)"
  kill_session s5e

  echo "--- 5f: plain tmux pane, kitty keyboard-protocol query, raw (control for 5g) ---"
  new_session s5f0 100 30 "$PROBE ttyprobe -timeout 3s -raw -query kitty"
  sleep 4.5
  capture s5f0 | tr -d '\r' | grep TTYPROBE || echo "(nothing captured)"
  kill_session s5f0

  echo "--- 5g: under marvel-shim, kitty keyboard-protocol query, raw ---"
  local c3="$RUN/s5f.ctl" s3s="$RUN/s5f.str"
  rm -f "$c3" "$s3s"
  new_session s5f 100 30 "$SHIM --quiet --control $c3 --stream $s3s -- $PROBE ttyprobe -timeout 3s -raw -query kitty"
  sleep 4.5
  capture s5f | tr -d '\r' | grep TTYPROBE || echo "(nothing captured)"
  kill_session s5f

  # The decisive control: the shim gives the child a PTY but is not a terminal
  # emulator. With no terminal above it, nothing answers the query.
  echo "--- 5h: marvel-shim headless (stdio = pipes, no tmux), raw ---"
  local c4="$RUN/s5h.ctl" s4s="$RUN/s5h.str"
  rm -f "$c4" "$s4s" "$RUN/s5h.out"
  ( sleep 10 | "$SHIM" --quiet --control "$c4" --stream "$s4s" -- \
      "$PROBE" ttyprobe -timeout 3s -raw >"$RUN/s5h.out" 2>&1 ) || true
  tr -d '\r' <"$RUN/s5h.out" | grep TTYPROBE || echo "(nothing captured)"
}

# ---------------------------------------------------------------- harnesses
# Real harness TUIs, plain tmux pane against shim-in-pane. capture-pane -e
# keeps the escape sequences, so an identical capture is a claim about what the
# terminal was told to draw, not just about the visible glyphs. Both harnesses
# are left at their trust prompt; the inject answers "No, quit", so nothing is
# sent to a model.
harness() {
  local h="${1:-claude}"
  banner "HARNESS: $h renders the same plain and under the shim, and takes inject"
  if ! command -v "$h" >/dev/null; then
    echo "$h not installed on this host; skipping"
    return 0
  fi
  local c="$RUN/h-$h.ctl" s="$RUN/h-$h.str"
  rm -f "$c" "$s"
  new_session "hp-$h" 110 32 "$h"
  new_session "hs-$h" 110 32 "$SHIM --quiet --control $c --stream $s -- $h"
  sleep 8
  "${TMUX[@]}" capture-pane -e -p -t "hp-$h" >"$RUN/$h.plain.cap"
  "${TMUX[@]}" capture-pane -e -p -t "hs-$h" >"$RUN/$h.shim.cap"
  echo "plain=$(wc -c <"$RUN/$h.plain.cap") bytes  shim=$(wc -c <"$RUN/$h.shim.cap") bytes"
  if diff -q "$RUN/$h.plain.cap" "$RUN/$h.shim.cap" >/dev/null; then
    echo "verdict=IDENTICAL (escape sequences included)"
  else
    echo "verdict=DIFFERS"
    diff "$RUN/$h.plain.cap" "$RUN/$h.shim.cap" | head -5
  fi
  "${TMUX[@]}" resize-window -t "hs-$h" -x 140 -y 44 2>/dev/null || true
  sleep 3
  "${TMUX[@]}" capture-pane -p -t "hs-$h" |
    awk '{ if (length($0)>m) m=length($0) } END { print "after resize to 140x44, widest pane line: "m }'
  echo "inject '2' + Enter through the control socket (answers the trust prompt with No):"
  "$PROBE" ctl -c "$c" -cmd inject -data '2\r'
  sleep 3
  # The harness acts on the injected keys and exits, which takes the shim and
  # its sockets with it, so socket-gone is the confirmation the inject landed.
  local after
  after=$("$PROBE" ctl -c "$c" -cmd status 2>&1 | head -1)
  case "$after" in
    CTL*) echo "after inject: $after" ;;
    *)    echo "after inject: harness acted on the keys and quit, taking the shim with it ($after)" ;;
  esac
  kill_session "hp-$h"
  kill_session "hs-$h"
}

# ---------------------------------------------------------------- latency
rtt() {
  local n="${1:-100}"
  banner "LATENCY: inject-to-observe round trip, n=$n"
  echo "--- control: one PTY, no shim, no sockets ---"
  "$PROBE" rtt -mode single -n "$n"
  echo "--- shim in a tmux pane: control socket in, stream socket out ---"
  local c="$RUN/rtt.ctl" s="$RUN/rtt.str"
  rm -f "$c" "$s"
  new_session rtt 200 50 "$SHIM --control $c --stream $s -- $PROBE echo -for 180s"
  for _ in $(seq 1 100); do [ -S "$c" ] && break; sleep 0.05; done
  sleep 0.3
  "$PROBE" rtt -mode shim -n "$n" -c "$c" -s "$s"
  kill_session rtt
}

cleanup() { "${TMUX[@]}" kill-server 2>/dev/null || true; }

main() {
  local what="${1:-all}"
  build
  case "$what" in
    s1) s1 ;;
    s2) s2 "${2:-}" ;;
    s3) s3 "${2:-}" "${3:-}" "${4:-}" ;;
    s4) s4 ;;
    s5) s5 ;;
    rtt) rtt "${2:-}" ;;
    h) harness "${2:-claude}" ;;
    all)
      s1; s2; s3; s4; s5; harness claude; harness codex; rtt
      ;;
    clean) cleanup ;;
    *) echo "unknown target $what" >&2; exit 2 ;;
  esac
  echo
  echo "tmux server for this run: tmux -L shimspike (kill with: $0 clean)"
}

main "$@"
