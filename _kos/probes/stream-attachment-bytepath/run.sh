#!/usr/bin/env bash
# Byte-path fidelity probe for question-stream-attachment (bd aae-orc-3cp).
# Compares six sinks against the same synthetic producer inside tmux:
#   a) fifo              - producer stdout redirected to a named pipe in the pane command
#   b) pipe-pane         - tmux copies pane output into a command's stdin
#   c) capture-visible-1hz  - poll the visible pane region once a second
#   d) capture-visible-10hz - same, ten times a second
#   e) capture-history-1hz  - poll the whole scrollback (history-limit 100000)
#   f) capture-history-1hz-default - same, tmux default history-limit 2000
# Scoring is on the sink bytes only: sequence coverage, ordering, ANSI survival,
# and bytes-read (the poll's cost). Producer write-side elapsed is recorded per
# case because it is the sink's backpressure signature.
set -euo pipefail

N="${N:-20000}"
OUT="${OUT:-/tmp/bytepath-$$}"
HERE="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$OUT"
go build -o "$OUT/producer" "$HERE/producer.go"
PROD="$OUT/producer"
SOCK=bytepath

tmuxq() { tmux -L "$SOCK" "$@"; }
cleanup() { tmuxq kill-server 2>/dev/null || true; }
trap cleanup EXIT
cleanup

# One second of lead time in every pane command so the sink is attached before
# the first byte is written. Without it, pipe-pane and the pollers race the
# producer and the measured loss is the harness's, not the path's.
LEAD=1

# --- (a) FIFO redirect -------------------------------------------------------
FIFO="$OUT/fifo"; rm -f "$FIFO"; mkfifo "$FIFO"
cat "$FIFO" > "$OUT/a.fifo.txt" &
CATPID=$!
S=$(date +%s.%N)
tmuxq new-session -d -s fifo -x 200 -y 50 \
  "sh -c 'sleep $LEAD; exec $PROD $N 0 $OUT/a.stats > $FIFO'"
wait "$CATPID"
E=$(date +%s.%N)
A_WALL=$(echo "$E - $S - $LEAD" | bc)

# --- (b) pipe-pane -----------------------------------------------------------
tmuxq new-session -d -s pipe -x 200 -y 50 \
  "sh -c 'sleep $LEAD; exec $PROD $N 0 $OUT/b.stats'"
PANE=$(tmuxq list-panes -t pipe -F '#{pane_id}' | head -1)
tmuxq pipe-pane -o -t "$PANE" "cat > $OUT/b.pipepane.txt"
S=$(date +%s.%N)
while tmuxq has-session -t pipe 2>/dev/null; do sleep 0.05; done
E=$(date +%s.%N)
B_WALL=$(echo "$E - $S - $LEAD" | bc)

# --- (c)-(f) capture-pane polling -------------------------------------------
# hold=3 keeps the pane alive past the last write so the poller's final read
# sees a settled pane; the wall time of these cases is poll-bound anyway.
capture_case() {
  local name="$1" hz="$2" scope="$3" hist="$4"
  # Separate statement: within one `local`, $name expands before it is assigned.
  local out="$OUT/$name.txt"
  # tmux parses '.' in a target as the pane separator, so the session name
  # cannot carry the case label's dots.
  local sess="${name//./-}"
  local interval; interval=$(echo "scale=4; 1 / $hz" | bc)
  tmuxq new-session -d -s "$sess" -x 200 -y 50 \
    "sh -c 'sleep $LEAD; exec $PROD $N 3 $OUT/$name.stats'"
  tmuxq set-option -t "$sess" history-limit "$hist"
  local pane; pane=$(tmuxq list-panes -t "$sess" -F '#{pane_id}' | head -1)
  local polls=0
  : > "$out"
  while tmuxq has-session -t "$sess" 2>/dev/null; do
    if [[ "$scope" == history ]]; then
      tmuxq capture-pane -p -e -J -S - -t "$pane" >> "$out" 2>/dev/null || true
    else
      tmuxq capture-pane -p -e -J -t "$pane" >> "$out" 2>/dev/null || true
    fi
    polls=$((polls + 1))
    sleep "$interval"
  done
  echo "$polls" > "$OUT/$name.polls"
}
capture_case c.capture-visible-1hz          1  visible 100000
capture_case d.capture-visible-10hz        10  visible 100000
capture_case e.capture-history-1hz          1  history 100000
capture_case f.capture-history-1hz-default  1  history 2000

# --- scoring -----------------------------------------------------------------
score() {
  local label="$1" file="$2" stats="$3" wall="${4:-}" polls="${5:-}"
  python3 - "$label" "$file" "$N" "$stats" "$wall" "$polls" <<'PY'
import os, re, sys
label, path, n, stats, wall, polls = sys.argv[1:7]
n = int(n)
raw = open(path, 'rb').read()
text = raw.decode('utf-8', 'replace')
seqs = [int(m) for m in re.findall(r'SEQ:(\d+)\|', text)]
seen, firsts = set(), []
for s in seqs:
    if s not in seen:
        seen.add(s)
        firsts.append(s)
# Ordering is scored on first-arrival order: a sink that re-presents old lines
# (any poller does) is not thereby out of order.
inversions = sum(1 for a, b in zip(firsts, firsts[1:]) if b < a)
opens, closes = text.count('\x1b[32m'), text.count('\x1b[0m')
intact = len(re.findall(r'\x1b\[32mSEQ:\d+\|x{48}\|\x1b\[0m', text))
def num(p):
    try:
        return float(open(p).read().strip())
    except Exception:
        return None
prod = num(stats)
extra = ''
if prod:
    extra += ' prod_write=%.2fs (%.0fk lines/s)' % (prod, n / prod / 1000)
if wall and float(wall) > 0:
    extra += ' sink_wall=%.2fs' % float(wall)
if polls and os.path.exists(polls):
    extra += ' polls=%s' % open(polls).read().strip()
print('%-30s uniq=%6d/%d (%6.2f%%) redundant=%7d inversions=%d '
      'intact_ansi=%6d sgr_open=%6d sgr_close=%6d bytes=%9d done=%s%s'
      % (label, len(seen), n, 100.0 * len(seen) / n, len(seqs) - len(seen),
         inversions, intact, opens, closes, len(raw),
         'DONE:%d' % n in text, extra))
PY
}

echo
echo "N=$N  tmux $(tmux -V)  pane 200x50  out=$OUT"
score a-fifo                     "$OUT/a.fifo.txt"     "$OUT/a.stats" "$A_WALL"
score b-pipe-pane                "$OUT/b.pipepane.txt" "$OUT/b.stats" "$B_WALL"
for c in c.capture-visible-1hz d.capture-visible-10hz e.capture-history-1hz f.capture-history-1hz-default; do
  score "$c" "$OUT/$c.txt" "$OUT/$c.stats" "" "$OUT/$c.polls"
done
