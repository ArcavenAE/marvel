#!/usr/bin/env bash
# Paced companion to run.sh: how much does a capture-pane poller actually see
# when output arrives at a sustained rate rather than as one burst?
#
# The predictive model under test is capacity = pane_rows * poll_hz. A 50-row
# pane polled at 1 Hz can see at most 50 lines/s of visible-region output; at
# 10 Hz, at most 500. run.sh could not test this because its burst completed
# faster than any poll interval.
#
# Each case runs the same paced producer and scores the sink bytes.
set -euo pipefail

DURATION="${DURATION:-6}"
OUT="${OUT:-/tmp/bytepath-paced-$$}"
HERE="$(cd "$(dirname "$0")" && pwd)"
ROWS="${ROWS:-50}"
mkdir -p "$OUT"
go build -o "$OUT/producer" "$HERE/producer.go"
PROD="$OUT/producer"
SOCK=bytepathpaced

tmuxq() { tmux -L "$SOCK" "$@"; }
cleanup() { tmuxq kill-server 2>/dev/null || true; }
trap cleanup EXIT
cleanup
LEAD=1

# rate hz -> paced capture case
paced_case() {
  local rate="$1" hz="$2"
  local name="rate${rate}-hz${hz}"
  local n=$((rate * DURATION))
  local interval; interval=$(echo "scale=4; 1 / $hz" | bc)
  tmuxq new-session -d -s "$name" -x 200 -y "$ROWS" \
    "sh -c 'sleep $LEAD; exec $PROD $n 2 $OUT/$name.stats $rate'"
  tmuxq set-option -t "$name" history-limit 100000
  local pane; pane=$(tmuxq list-panes -t "$name" -F '#{pane_id}' | head -1)
  : > "$OUT/$name.txt"
  local polls=0
  while tmuxq has-session -t "$name" 2>/dev/null; do
    tmuxq capture-pane -p -e -J -t "$pane" >> "$OUT/$name.txt" 2>/dev/null || true
    polls=$((polls + 1))
    sleep "$interval"
  done
  echo "$polls" > "$OUT/$name.polls"
  echo "$n" > "$OUT/$name.n"
}

for rate in 20 40 200; do
  for hz in 1 10; do
    paced_case "$rate" "$hz"
  done
done

echo
echo "DURATION=${DURATION}s  pane 200x${ROWS}  tmux $(tmux -V)  out=$OUT"
echo "model: visible-region capacity = rows * poll_hz = $((ROWS * 1)) lines/s at 1Hz, $((ROWS * 10)) lines/s at 10Hz"
for f in "$OUT"/rate*-hz*.txt; do
  base="${f%.txt}"; name="$(basename "$base")"
  python3 - "$name" "$f" "$(cat "$base.n")" "$(cat "$base.polls")" <<'PY'
import re, sys
name, path, n, polls = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
text = open(path, 'rb').read().decode('utf-8', 'replace')
seen = set(int(m) for m in re.findall(r'SEQ:(\d+)\|', text))
print('%-16s emitted=%5d seen=%5d (%6.2f%%) polls=%s' %
      (name, n, len(seen), 100.0 * len(seen) / n, polls))
PY
done
