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
start-bg: build
    ./bin/marvel daemon &
    @sleep 1
    @echo "marvel daemon started"

# Stop the daemon and clean up all tmux sessions
stop:
    ./bin/marvel stop --teardown || true

# Stop the daemon but leave agents running; the next `just daemon` adopts them
detach:
    ./bin/marvel stop || true

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
    @echo "Attach to tmux: tmux attach -t marvel-demo"

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
clean:
    -tmux kill-session -t marvel-demo 2>/dev/null
    -rm -f /tmp/marvel.sock
    -rm -rf bin/

# Three-act runnable demo. Full runbook: docs/demo.md.
# Each recipe assumes a running daemon (`just start-bg` first).

# Act 1 — Recover: set up the pane-loss recovery team, print the next steps
demo-act1: build
    @echo "==> Act 1 (Recover). Loading the recovery team..."
    ./bin/marvel work examples/demo-act1-recovery.toml
    @sleep 5
    @echo ""
    @echo "==> Sessions (two workers, HEALTH healthy):"
    ./bin/marvel get sessions
    @echo ""
    @echo "Next, cause an unplanned pane loss and watch marvel recover it:"
    @echo "  ./bin/marvel describe session recover/line-worker-g1-0   # note PaneID %N"
    @echo "  tmux kill-pane -t %1                                     # use that PaneID"
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

# Act 2 — Observe: run the three-harness matrix, print the event-watch commands
demo-act2: build
    @echo "==> Act 2 (Observe). Loading the {claude, codex, opencode} matrix..."
    @echo "    Needs the three harness binaries and working auth for the agent stream."
    ./bin/marvel work examples/mixed-adapters.toml
    @sleep 4
    @echo ""
    @echo "==> Sessions (CPU% and RSS populate uniformly; CTX% is '-' for these harnesses):"
    ./bin/marvel get sessions
    @echo ""
    @echo "Watch the normalized agent stream across all three harnesses:"
    @echo "  ./bin/marvel events --workspace mixed"
    @echo "  ./bin/marvel events --kind agent.turn.completed    # tokens in/out per turn"
    @echo "  ./bin/marvel events --kind agent.session.ended     # per-session cost and duration"

# Act 3 — Control plane: project a policy, then re-project live with no restart
demo-act3: build
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

# Point at the full three-act runbook
demo-all:
    @echo "Marvel three-act demo. Full runbook: docs/demo.md"
    @echo ""
    @echo "  just start-bg     # start the daemon first"
    @echo "  just demo-act1    # Recover"
    @echo "  just demo-act2    # Observe (needs harness auth)"
    @echo "  just demo-act3    # Control plane"
    @echo ""
    @echo "Between acts: just stop && rm -f ~/.marvel/state/marvel.bolt && just start-bg"

# Operator console for watching a demo live: four panes in one tmux
# session — driver shell, live session table, live event tail, daemon
# log poll. Attach from your own terminal: tmux attach -t marvel-watch
demo-watch: build
    #!/usr/bin/env bash
    set -euo pipefail
    tmux kill-session -t marvel-watch 2>/dev/null || true
    tmux new-session -d -s marvel-watch -x 220 -y 55
    P0=$(tmux display -p -t marvel-watch '#{pane_id}')
    P1=$(tmux split-window -t "$P0" -v -P -F '#{pane_id}')
    P2=$(tmux split-window -t "$P0" -h -P -F '#{pane_id}')
    P3=$(tmux split-window -t "$P1" -h -P -F '#{pane_id}')
    tmux send-keys -t "$P0" 'clear; echo "DRIVER — run demo beats here (runbook: docs/demo.md). Daemon: ./bin/marvel daemon &"' C-m
    tmux send-keys -t "$P2" 'while :; do ./bin/marvel get sessions -w 1 2>/dev/null; sleep 2; clear; done' C-m
    tmux send-keys -t "$P1" 'while :; do ./bin/marvel events --follow 2>/dev/null; sleep 2; done' C-m
    tmux send-keys -t "$P3" 'while :; do clear; ./bin/marvel daemon logs -n 14 2>/dev/null || echo "(daemon not up yet)"; sleep 2; done' C-m
    tmux select-pane -t "$P0"
    echo "Operator console ready: tmux attach -t marvel-watch"
    echo "  top-left  DRIVER          top-right  sessions (live)"
    echo "  bot-left  events --follow bot-right  daemon logs"
