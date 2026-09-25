#!/usr/bin/env bash
# Show how tmux delivers each inject form to a pane, one line per read().
# Scratch tmux server only (-L injprobe-$$); never point this at a live seat.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
out="$(mktemp -d)"
sock="injprobe-$$"
t() { tmux -L "$sock" "$@"; }
trap 't kill-server 2>/dev/null || true' EXIT
t new-session -d -s p "python3 $here/reader.py $out/reads.log bp"
sleep 1
txt="Director: a line of text long enough to be treated as a paste by the composer"
t send-keys -t p -l "A1 $txt" \; send-keys -t p Enter; sleep 0.5          # marvel today
t send-keys -t p -l "A2 $txt"; t send-keys -t p Enter; sleep 0.5          # two calls
printf 'G %s' "$txt" | t load-buffer -b "pb-$$" -
t paste-buffer -p -d -t p -b "pb-$$" \; send-keys -t p Enter; sleep 0.5   # proposed
awk '{print $1, $2, substr($0, length($0)-22)}' "$out/reads.log"
