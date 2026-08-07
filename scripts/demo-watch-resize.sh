#!/usr/bin/env bash
# Keep the demo console's session-table pane at its content width and let
# the CLI absorb every remaining column.
#
# tmux scales panes proportionally on resize, which is wrong for both of
# these panes. The session table is content-fixed: below its width it
# silently loses the LLM and RUNTIME columns, and above it it renders
# blank padding while the CLI stays cramped. The CLI is the opposite, it
# wants every column it can get.
#
# Bound by a CLI floor, because on a narrow terminal a full table beside
# an unusable CLI is the worse trade. The table clips gracefully; a
# 40-column shell does not.
#
# Usage: demo-watch-resize.sh <session-pane-id> <target-width> <cli-min>
set -euo pipefail

pane=${1:?pane id required}
want=${2:?target width required}
cli_min=${3:?cli minimum required}

# Floor for the table itself. Below this the pane is not worth keeping
# wide at the CLI's expense; WORKSPACE through AGENT NAME still fits.
table_floor=60

width=$(tmux display -p -t "$pane" '#{window_width}' 2>/dev/null) || exit 0
[[ -n "$width" ]] || exit 0

# 1 column goes to the pane divider.
afford=$((width - 1 - cli_min))
target=$want
((afford < target)) && target=$afford
((target < table_floor)) && target=$table_floor
((target >= width)) && exit 0

tmux resize-pane -t "$pane" -x "$target" 2>/dev/null || true
