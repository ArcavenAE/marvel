# Marvel — agent orchestration control plane

default:
    @just --list

# Build all binaries (marvel + simulator)
build:
    go build -o bin/marvel ./cmd/marvel/
    go build -o bin/simulator ./cmd/simulator/

# Build simulator only
build-sim:
    go build -o bin/simulator ./cmd/simulator/

# Run all tests
test:
    go test ./... -v

# Run tests with race detector
test-race:
    go test ./... -race -v

# Format code
fmt:
    gofumpt -w .

# Lint code
lint:
    golangci-lint run ./...

# Start the marvel daemon (foreground)
start: build
    ./bin/marvel daemon

# Start the daemon in the background
start-bg: build _store-warn
    ./bin/marvel daemon &
    @just _wait-ready

# Warn when the durable store already holds desired state. Starting a
# daemon reconciles whatever it records, which spawns live agent sessions
# and spends model tokens (aae-orc-cxdf, reproduced 2026-08-09: one
# unflagged start produced 20 sessions plus live claude, codex and
# opencode calls). Deliberately warns rather than refusing or wiping: a
# populated store is legitimate state for an operator restarting their
# own fleet, and only they can tell that from leftovers.
_store-warn:
    #!/usr/bin/env bash
    set -euo pipefail
    store="${HOME}/.marvel/state/marvel.bolt"
    if [[ -s "${store}" ]]; then
        echo "warning: ${store} already holds desired state"
        echo "         ($(du -h "${store}" | cut -f1), modified $(date -r "${store}" '+%Y-%m-%d %H:%M'))."
        echo "         The daemon will reconcile it on start, which can spawn live agents"
        echo "         and spend model tokens. To start from a clean store: just demo-reset"
        echo ""
    fi

# Block until the daemon answers, instead of assuming it is up after a
# fixed sleep. The old `sleep 1` printed "started" while sessions were
# still spawning.
_wait-ready:
    #!/usr/bin/env bash
    set -euo pipefail
    for _ in $(seq 1 50); do
        if ./bin/marvel get workspaces >/dev/null 2>&1; then
            echo "marvel daemon ready"
            exit 0
        fi
        sleep 0.2
    done
    echo "marvel daemon did not answer within 10s; see ${HOME}/.marvel/log/daemon.log" >&2
    exit 1

# Fail with the instruction, not a raw dial error, when no daemon is running.
# The demo acts need a daemon but must NOT start one: a daemon reconciles
# whatever the durable store already records, spawning live agent sessions
# (see _store-warn and aae-orc-9uzk). Refuse and point at start-bg instead.
_require-daemon:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! ./bin/marvel get workspaces >/dev/null 2>&1; then
        echo "No marvel daemon is running. Start one first:  just start-bg" >&2
        exit 1
    fi

# Stop the daemon and clean up all tmux sessions
stop:
    ./bin/marvel stop --teardown || true

# Stop the daemon but leave agents running; the next `just daemon` adopts them
detach:
    ./bin/marvel stop || true

# Was prose in `just demo-all` telling the reader to retype three
# commands; the reason it exists is aae-orc-cxdf, so it belongs in a
# recipe. Removes durable state: the demo path, not the fleet path.
# Stop, wipe the store, and start clean between demo acts
demo-reset: stop
    rm -f "${HOME}/.marvel/state/marvel.bolt"
    @just start-bg

# Load the demo manifest
demo: build
    @echo "==> Loading demo manifest..."
    ./bin/marvel work examples/demo.toml
    @sleep 2
    @echo ""
    @echo "==> Workspaces:"
    ./bin/marvel get workspaces
    @echo ""
    @echo "==> Teams:"
    ./bin/marvel get teams
    @echo ""
    @echo "==> Sessions:"
    ./bin/marvel get sessions
    @echo ""
    @echo "==> Endpoints:"
    ./bin/marvel get endpoints
    @echo ""
    @echo "Demo running. Use 'just stop' to tear down."
    @echo "Attach to tmux: tmux -L $(./bin/marvel config tmux-server) attach -t marvel-demo"

# Show running state
status: build
    @echo "==> Workspaces:"
    @./bin/marvel get workspaces
    @echo ""
    @echo "==> Teams:"
    @./bin/marvel get teams
    @echo ""
    @echo "==> Sessions:"
    @./bin/marvel get sessions

# Scale a team role: just scale demo/squad worker 5
scale team role replicas: build
    ./bin/marvel scale {{team}} --role {{role}} --replicas {{replicas}}

# Initiate a shift: just shift demo/squad
shift team: build
    ./bin/marvel shift {{team}}

# Watch sessions (interactive, sortable)
watch: build
    ./bin/marvel get sessions -w

# Demo shift lifecycle: load manifest, wait, shift, watch
demo-shift: build
    @echo "==> Loading shift-demo manifest..."
    ./bin/marvel work examples/shift-demo.toml
    @sleep 3
    @echo ""
    @echo "==> Sessions before shift:"
    ./bin/marvel get sessions
    @echo ""
    @echo "==> Initiating shift..."
    ./bin/marvel shift shift-demo/squad
    @sleep 5
    @echo ""
    @echo "==> Sessions after shift:"
    ./bin/marvel get sessions
    @echo ""
    @echo "Shift complete. Use 'just watch' to monitor or 'just stop' to tear down."

# Clean up everything (kill all marvel tmux sessions)
#
# The kill-session needs -L: since #128 each HOME has its own tmux server,
# so a bare kill-session targets tmux's shared default server and silently
# reaches nothing. Asking the binary keeps the derivation in one place
# rather than recomputing sha256(HOME) here. No build dependency, because
# this recipe deletes bin/ and should still work when it is already gone;
# it says what it skipped instead of failing or pretending.
clean:
    #!/usr/bin/env bash
    set -uo pipefail
    server=""
    if [[ -x ./bin/marvel ]]; then
      server=$(./bin/marvel config tmux-server 2>/dev/null || true)
    fi
    if [[ -n "${server}" ]]; then
      tmux -L "${server}" kill-session -t marvel-demo 2>/dev/null || true
      echo "cleaned tmux session marvel-demo on server ${server}"
    else
      echo "note: ./bin/marvel not built, so the tmux server name is unknown."
      echo "      Skipped the tmux cleanup. Run 'just build' first, or:"
      echo "      tmux -L \"\$(marvel config tmux-server)\" kill-session -t marvel-demo"
    fi
    rm -f "${HOME}/.marvel/run/marvel.sock" "${HOME}/.marvel/run/marvel.sock.lock"
    rm -rf bin/

# Four-act runnable demo. Full runbook: docs/demo.md.
# Each recipe assumes a running daemon (`just start-bg` first).

# Act 1 — Recover: set up the pane-loss recovery team, print the next steps
demo-act1: build _require-daemon
    @echo "==> Act 1 (Recover). Loading the recovery team..."
    ./bin/marvel work examples/demo-act1-recovery.toml
    @sleep 5
    @echo ""
    @echo "==> Sessions (two workers, HEALTH healthy):"
    ./bin/marvel get sessions
    @echo ""
    @echo "Next, cause an unplanned pane loss and watch marvel recover it:"
    @echo "  ./bin/marvel describe session recover/line-worker-g1-0   # note PaneID %N"
    @echo "  tmux -L \"\$(./bin/marvel config tmux-server)\" kill-pane -t %1   # use that PaneID"
    @echo "  ./bin/marvel events --kind session.crashed              # immediate"
    @echo "  ./bin/marvel events --kind session.created              # replacement (~30-60s, crash-loop backoff)"
    @echo ""
    @echo "Health-driven terminal cases (session.restarted / session.failed / role.saturated):"
    @echo "  ./bin/marvel work examples/demo-act1-health.toml"
    @echo "  ./bin/marvel events --workspace health --warnings"
    @echo ""
    @echo "Role removal (role.removed):"
    @echo "  ./bin/marvel work examples/demo-act1-roles.toml"
    @echo "  ./bin/marvel work examples/demo-act1-roles-removed.toml"
    @echo "  ./bin/marvel events --kind role.removed"

# Act 2 — Observe: run the harness matrix (-p headless + -t TUI), print the event-watch commands
demo-act2: build _require-daemon
    @echo "==> Act 2 (Observe). Loading the {claude, codex, opencode} matrix + a TUI claude..."
    @echo "    Role names carry the mode: -p = print/headless, -t = TUI (interactive)."
    @echo "    Needs the harness binaries and working auth for the agent stream."
    ./bin/marvel work examples/mixed-adapters.toml
    @sleep 4
    @echo ""
    @echo "==> Sessions (CPU% and RSS populate uniformly; CTX% arrives per producer):"
    ./bin/marvel get sessions
    @echo ""
    @echo "Watch the normalized agent stream and both CTX% producers:"
    @echo "  ./bin/marvel events --workspace mixed"
    @echo "  ./bin/marvel events --kind agent.turn.completed    # tokens in/out per turn"
    @echo "  ./bin/marvel events --kind agent.session.ended     # per-session cost and duration"
    @echo "  ./bin/marvel inject mixed/matrix-analyst-t-g1-0 'say only the word ready' -e"
    @echo "  ./bin/marvel get sessions   # -p rows: stream accountant · -t row: statusline feed"

# Act 3 — Control plane: project a policy, then re-project live with no restart
demo-act3: build _require-daemon
    @echo "==> Act 3 (Control plane). Projecting reviewer-contract v1..."
    @echo "    Needs the claude binary on PATH (auth not required for the projection events)."
    ./bin/marvel work examples/policy-projection.toml
    @sleep 4
    @echo ""
    @echo "==> Policies (VERSION 1) and the spawn projection event:"
    ./bin/marvel get policies
    ./bin/marvel events --kind policy.projected
    @echo ""
    @echo "Now swap to v2 and watch the running agent's contract change with no restart:"
    @echo "  ./bin/marvel work examples/policy-projection-v2.toml"
    @echo "  ./bin/marvel get policies                    # VERSION now 2"
    @echo "  ./bin/marvel events --kind policy.projected  # second event: re-projected after manifest change"
    @echo "  ./bin/marvel get sessions                    # same session, same GEN, no restart"

# Act 4 — Meter and Admit: three crew under a six-session budget, then refuse a fan-out
demo-act4: build _require-daemon
    @echo "==> Act 4 (Meter and Admit). Loading three crew under a six-session budget..."
    @echo "    Needs only sleep: no harness binary, no auth, no quota."
    ./bin/marvel work examples/demo-act4-budget.toml
    @sleep 5
    @echo ""
    @echo "==> Sessions (three crew, running) and the meter:"
    ./bin/marvel get sessions
    ./bin/marvel get budgets
    @echo ""
    @echo "Now ask for forty replicas and watch the verb refuse it:"
    @echo "  ./bin/marvel scale fanout/crew --role crew --replicas 40   # exits 1, nothing changed"
    @echo "  ./bin/marvel get teams                       # REPLICAS still 3"
    @echo "  ./bin/marvel get sessions                    # still three; no partial fan-out"
    @echo "  ./bin/marvel events --kind admission.refused # the arithmetic, once"
    @echo "  ./bin/marvel describe team fanout/crew       # the declared budget, verbatim"
    @echo ""
    @echo "Recovery is one manifest edit (max_sessions = 40, replicas = 40), then:"
    @echo "  ./bin/marvel work examples/demo-act4-budget.toml   # forty crew"

# Point at the full four-act runbook
demo-all:
    @echo "Marvel four-act demo. Full runbook: docs/demo.md"
    @echo ""
    @echo "  just start-bg     # start the daemon first"
    @echo "  just demo-act1    # Recover"
    @echo "  just demo-act2    # Observe (needs harness auth)"
    @echo "  just demo-act3    # Control plane"
    @echo "  just demo-act4    # Meter and Admit (no auth; runs anywhere)"
    @echo ""
    @echo "Between acts: just demo-reset"

# Width of the session-table pane: every column of `marvel get sessions`
# plus 12 characters of the model. 137 is where the LLM column starts in
# the worst case the demo actually produces (STATE=crashloop-backoff, which
# Act 1 demonstrates, and a 26-character agent name), measured against the
# renderer's tabwriter rather than estimated.
session_pane_width := "149"

# Floor for the CLI pane. The console wants 149 + 1 + this, which is why
# the window defaults to 250. On a narrower terminal the CLI keeps this
# many columns and the session table clips instead, because a full table
# beside an unusable shell is the worse trade.
cli_min_width := "100"

# Operator console for watching a demo live: four panes in one tmux
# session — live session table and a driver CLI side by side on top, event
# tail and daemon log poll full-width below. Attach from your own terminal:
# tmux attach -t marvel-watch
demo-watch: build
    #!/usr/bin/env bash
    set -euo pipefail
    tmux kill-session -t marvel-watch 2>/dev/null || true
    tmux new-session -d -s marvel-watch -x 250 -y 55
    P0=$(tmux display -p -t marvel-watch '#{pane_id}')
    P2=$(tmux split-window -t "$P0" -v -l 50% -P -F '#{pane_id}')
    P1=$(tmux split-window -t "$P0" -h -P -F '#{pane_id}')
    P3=$(tmux split-window -t "$P2" -v -l 50% -P -F '#{pane_id}')
    # tmux scales panes proportionally on resize, which is wrong for both
    # of these. The session table is content-fixed: narrower and it drops
    # the LLM column, wider and it renders blank padding. The CLI wants
    # every column it can get. So the table is pinned and the CLI absorbs
    # all the slack, re-applied on every resize because attaching from a
    # real terminal is a resize.
    RESIZE="{{justfile_directory()}}/scripts/demo-watch-resize.sh $P0 {{session_pane_width}} {{cli_min_width}}"
    $RESIZE
    # Synchronous on purpose. The script is three tmux calls, and with
    # run-shell -b a reader can catch the pane at its proportionally
    # scaled width before the hook re-pins it.
    tmux set-hook -t marvel-watch client-resized "run-shell '$RESIZE'"
    tmux set-hook -t marvel-watch window-resized "run-shell '$RESIZE'"
    # Layout: P0 sessions top-left, P1 CLI top-right, P2 events and P3 logs
    # full-width below. The two readouts you act ON sit beside the CLI you
    # act WITH; the two you read AFTER run underneath, full width, because
    # event and log lines are long and a half-width pane wraps them.
    #
    # The session table is clipped to the pane rather than wrapped. A row
    # with a real model name runs past 160 columns, and a wrapped table is
    # unreadable at a glance, which is the only thing this pane is for.
    # Clipping to the live pane width keeps it correct if you resize.
    #
    # The -t "$TMUX_PANE" is load-bearing. Bare `tmux display -p` resolves
    # against the client's ACTIVE pane, which is the CLI, so the table
    # would clip to 70 columns and lose everything from STATE rightward.
    tmux send-keys -t "$P0" 'while :; do clear; ./bin/marvel get sessions 2>/dev/null | cut -c1-$(tmux display -p -t "$TMUX_PANE" "#{pane_width}"); sleep 2; done' C-m
    tmux send-keys -t "$P1" 'clear; echo "CLI — run demo beats here (runbook: docs/demo.md). Daemon: ./bin/marvel daemon &"' C-m
    tmux send-keys -t "$P2" 'while :; do ./bin/marvel events --follow 2>/dev/null; sleep 2; done' C-m
    tmux send-keys -t "$P3" 'while :; do clear; ./bin/marvel daemon logs -n 10 2>/dev/null || echo "(daemon not up yet)"; sleep 2; done' C-m
    tmux select-pane -t "$P1"
    echo "Operator console ready: tmux attach -t marvel-watch"
    echo "  top-left   sessions ({{session_pane_width}} cols: all columns + 12 of the model)"
    echo "  top-right  CLI (focused, takes every column the table does not)"
    echo "  bottom     events --follow, then daemon logs (full width)"
